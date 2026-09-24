# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass of 2026-09-24, after non-spec rounds 2 to 4 of run 0081-opt7, `[operator.redesign-r4]` and
spec round 1 of run 0081-opt8). Read the whole ledger, `[non-spec.2.fix-G1.1]` through `[spec.1.review-single-source.1]`.
The ledger reuses round ids across runs: its `[non-spec.2-4.*]` entries are run 0081-opt7 and its `[spec.1.*]` entries
are run 0081-opt8, the NEWEST; `[spec.1.*]` and `[non-spec.1-16.*]` ids cited in older Settled lines name earlier runs.
Lifted: the new block `#### Lifted 2026-09-24 (opt7 non-spec 2-4, redesign-r4, opt8 spec 1)` at the end of `### Settled`
(the §7.1 "sends no `Shutdown`" deletion, the §15.1 preamble-sentence deletion, the `releaseSessionSlotUnderGuard`
signature, the whole-file landing of the two race and fence files, the compensation's two-bound home, the DOCS-2
bind-attempt cut, the queue-head split, the redesign-r4 tier and landing facts, and about thirty tree FACTs), and new
`### Traps` entries at the end of that section (the late-insert race, the §7.1 minimum-obligation rule, the kept
"Deregister nothing here" clause, the expired-rollback guard race, and a sixth recurring non-findings list). Rewritten
under `CORRECTS` or a newer entry: the `[operator.post-r16]` "no carve-out" entry, the SETUP_COMMAND_FAILED preamble
entry and the "reads, verbatim" re-sweep entry. Closed and moved out of `### Open`: the rule-8 untokened-replacement
question (no bind path makes `StartSession` or `ConfigureWorkspace` a first request), the Go doc-comment gate question
(every remaining gate checked) and the `Binder.Resume` compensation-helper question (it is the helper). The ledger's
own OPEN on per-slot leak keying against Redis withholding was closed within the ledger by
`[non-spec.2.review-reliability.1]` and lands as a Settled FACT. Deleted as superseded: the ledger WATCHOUTs on
`slot_bind_attempt_race_test.go` and the tier-9 fence file spanning steps (both files now land whole) and on the tier-4
datastore-crossing case at S19 (it lands at S20). Added to `### Open`: the proposal-id and restating Go comments left
by `[non-spec.4.fix-G5.1]`, the SPEC-6 `cause` label against §16.1.1, and the §15.4.2 `DRAINING` against §29.4 step 13
question. `### Deferred` is unchanged: no ledger entry closes one. The ledger is not edited; the round boundary archives
it. Target: NOT reached. The section is about 930 lines (up from 830), because every lifted line is a claim nothing else
in the live record carries and the previous pass's content has no closure to retire it. The hard compaction before run
0081-opt3 archived the prior 1,438-line standing context in
`0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md` under `## Standing context archived
2026-09-23 before run 0081-opt3`.

How to use this section. Cite entries by their bold subject. The current staging is: a caller-minted per-attempt
`bind_attempt` token stamped once in `ensureSlotStateLocked` under `s.mu`; `unconditional_teardown`;
`SLOT_BIND_ATTEMPT_SUPERSEDED` (29) and `SLOT_BIND_ALREADY_STARTED` (28); §4.7.1 rules 1-9 (admission) and 10-15
(`Shutdown`), stated once; the §4.7.1 registry critical-section paragraph as the one atomicity home; the §5.2
disposition table as the one home of every cleanup's report, `leaked` and hold disposition, with residue stated once
after the table; the §5.2 reclaim-hold paragraph carrying the decision-47 premature-removal sentence; CODE-14's
removing-site table, one row per removing site carrying its whole `completed` predicate, `guarded` included; the
`## Testing` landing table as the one step-to-test map; compensation of every failed
bind, typed refusals included, with a `cause` label on the superseded counter; CONF-1 with one case per numbered rule;
and the comment-carrier reduction as one shared block with CODE-10, CODE-11 and CODE-12 as sub-blocks. An archive entry
naming a bind epoch, `expected_bind_epoch`, `ExcludePod(s)`, a per-rule §15.4 table, lettered CONF-1 clauses, a residue
column or a compensation suppression on typed refusals describes a withdrawn design. Since `[f13.human-decisions]`
the compensation, lease release and leaked disposition sit in CODE-13 (cut from CODE-4), the per-slot guard and the
expired-acquisition disposition in CODE-14 (cut from CODE-6), and the split gates in CODE-15 (cut from CODE-1); an entry
below that attributes those to CODE-4, CODE-6 or CODE-1 predates the re-cut. CODE-15 lands with CODE-1 at S16 (S17 is
retired), and CODE-14 also owns the §10.1.4 per-member close context. Line numbers into the proposal files are not
recorded here.

### Settled

- **DECISION: the disposition of every per-slot cleanup is ONE TABLE, in the staged §5.2 `**Scrub model.**` append.** Rows key on what is reclaimed, who performs it and which act fails; other sites cite it. CODE-14's removing-site table's report and reclaim-hold cells implement its rows per code site. Rejected: a §6.2 two-exit home.
- **DECISION: where the old sites disagreed, the table takes these answers.** A failed cleanup outside a `Shutdown` is not `leaked`; rows key on the failing act, never "completed"; the whole-pod scrub ends directories only; §15.4 cites §5.2.
- **DECISION: the table's two pre-`running` rows are keyed "reclaimed by a `Shutdown`", not "reclaimed by the pod-side reclaim".** The §7.1 reclaim is a `Shutdown`, and rules 12 and 14 both answer `reclaimed`. Rejected: a third row pair.
- **DECISION: the adapter's atomicity is ONE paragraph, `**The registry critical section.**`, in the staged §4.7.1 block.** Membership reads "rules 2 through 7" because rule 1 (`validateBindFields`) runs before `s.mu`. Rejected: a sixteenth rule; per-rule atomicity clauses.
- **DECISION: `## Design (as the spec must state it)` is a choice record,** one paragraph per choice naming ground and owning block. Deleting it was rejected: the over/under-approximation grounds live nowhere else. The two-field rationale's home is the summary's `unconditional_teardown` Decisions bullet (`CORRECTS` by `[spec-recheck.1.review-single-source.1]`).
- **DECISION (`[spec-recheck.1.fix-G1.1]`): single reason homes.** Two-field teardown: the summary `unconditional_teardown` bullet. Reclaim hold: Design "The adapter holds the slot identifier while its cleanup runs". Report placement and double count: Design "The cleanup-outcome report follows the `running` boundary".
- **DECISION (`[non-spec.1.fix-G1.1]`): implementation-facing reason homes.** Mid-session cascade, handler placement, epoch-vs-token and proto-window reasons: summary Decisions bullets. Atomicity: spec `## Design` "The adapter's atomicity is stated once.". Fresh-connection fence: staged §7.1. Admission-guard precondition: non-spec Design "The mid-session conditioning.".
- **DECISION (`[non-spec.1.fix-G2.1]`): gateway-side reason homes are the deliverables.** Launch-mints-none: CODE-4 citing §4.7.1's carriage lead-in. Prepare-no-compensation and attempt-scoped lease release: CODE-13. failPhase drain: CODE-8. Two ErrorCodes: CODE-7. Leaked-disposition reason: SPEC-2 commentary (decision 23).
- **DECISION (`[spec-recheck.1.fix-G3.1]`): the §7.1 connection-preference reason's one home is the summary Watch-out bullet "The §7.1 connection sentence carries no correctness load".** The SPEC-2 commentary paragraph is deleted with no pointer.
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
- **The completion predicate differs by site, deliberately, and CODE-14's removing-site table states each once, in its `completed` column.** `Shutdown` and `terminateHeldSession`: `guarded && closeErr == nil && treeErr == nil`; the guard-acquiring `releaseSessionSlot` and `releaseSessionSlotUnderGuard`, the body it calls: `guarded && treeErr == nil`, because neither closes a runtime, with `guarded` true when `Resume`'s rollback calls the body (`CORRECTS` by `[redesign.8.fix.1]`; the home and the fourth row by `[operator.redesign-r4]`).
- **DECISION (`[operator.47]`, option A): SPEC-3's reclaim-hold paragraph states that a workspace removal performed before every request still writing under the identifier has stopped writing is an act that did not return without error.** It names the ordering and never the guard. Rejected: (B) dropping `guarded`, which frees an identifier over a tree a parked `Resume` extraction is re-creating.
- **DECISION (`[operator.redesign-r4]`, `CORRECTS` `[non-spec.16.fix-G2.1]`'s removal-column-only site table): CODE-14's `**The removing-site table.**` is the ONE home of every removing site's behaviour on an expired acquisition, complete `completed` predicate including `guarded`, cleanup-outcome report and reclaim-hold disposition,** one row per site; its report and reclaim-hold cells record each site's implementation of the §5.2 disposition table's rows, and the §5.2 table remains the contract those cells cite; and its guard-acquisition and context columns summarize what CODE-1's `Shutdown` handler, CODE-14's `Resume` guard paragraph and CODE-14's **`terminateHeldSession`'s guard and close context.** own: `Shutdown`'s removing arm, the guard-acquiring `releaseSessionSlot`, `releaseSessionSlotUnderGuard` and §10.1.4 `terminateHeldSession` (pass 1 destroys nothing and is named in the table's lead). CODE-1, CODE-6 and CODE-15 cite a row and state no partial predicate, and the paragraph `**What holds at S14.**` states once what holds before `guarded` exists. The paragraph `**Disposition of an expired acquisition at a removing site.**` keeps the reason: the unguarded removal as shipped, routed onto the decision-47 sentence and the §5.2 table's failed-act rows, with `guarded` defined beside the table. Superseded: a site table with the removal column only, which scattered the predicates across CODE-6, CODE-1 and CODE-15 so that each local fix produced the next finding. Rejected: releasing the hold.
- **DECISION (`[operator.redesign-r4]`), closing the Deferred on the §10.1.4 pass-2 expired acquisition: the member's hold is retained, and CODE-14's `**An expired acquisition in the §10.1.4 pass.**` states it.** The acquisition step tries the send before it consults the context, so a member whose guard is free acquires it after an earlier member's park spent the pass's context, and only a member whose own guard is held when the pass reaches it takes the on-expiry cell. A holder on the `acquireSlotGuardForResolve` form either took the guard before pass 1 opened the hold, or passed that form's hold test before pass 1 and acquired afterwards, in which case its resolve meets the hold inside `ensureSlotStateLocked` and it does no path work; a holder on the raw `lockSlotGuard` form, which the hold never refuses (a `Shutdown` past its first decision or a rollback's `releaseSessionSlot`), can take it after and meets the removed entry. The holder is therefore a writer still in its path work or a section about to meet the hold or the removed entry, the adapter cannot tell them apart, and so the removal counts as premature. `[non-spec.6.review-mechanism.1]`'s `CORRECTS` holds. The non-spec Edge-cases bullet's "every remaining member" is narrowed to members whose own guard is held. Rejected: releasing the hold for a member whose acquisition expired on a context an earlier member spent, which frees an identifier over a tree a parked `Resume` may still be writing; a per-member acquisition deadline, which lengthens the pass by one deadline per contended member.
- **DECISION (`[non-spec.15.fix-G1.1]`): the guard's acquisition step is "non-blocking send first, then select against `ctx.Done()` only when the channel is full",** stated once in the per-slot guard's `slotGuards` preamble (CODE-14 since the re-cut) and cited by `lockSlotGuard` and `acquireSlotGuardForResolve`. A bare two-case select picks at random when both are ready.
- **DECISION: CODE-1's cleanup-outcome report stays keyed on `closeErr` ALONE.** The `errors.Join(closeErr, treeErr)` re-key retires a pod on one failed `os.RemoveAll`. A tree failure logs `slot_tree_removal_failed`; report `released`, hold held.
- **"Completed" and "reached `released`" are INDEPENDENT predicates across the three arms that traverse `slot_cleanup ──→ released`.** Any trigger or test equating them is wrong.
- **DECISION (`[redesign.5.fix.1]`): the cleanup has TWO completion terms, each defined once.** `completed` is pod-side (SPEC-3's hold paragraph) and decides the hold; `acknowledged clean` is gateway-side (SPEC-2's §7.1 paragraph) and decides `leaked`. The gateway-side predicate is never written as "completed".
- **DECISION: the `slot_cleanup ──→ released` fence entry carries a BARE POINTER and no trigger,** `(see §5.2)`, and the `leaked` entry likewise. No short trigger is true of every traversal.
- **`slotlayout.RemoveTree` is best-effort across four directories** and returns the first error, so spec text distinguishing them is unimplementable. `EnsureTree` is idempotent, so a successor materializes INTO a residue.
- **`deregisterSlotLocked` cannot fail,** so deregistration opens the hold and is not an owed act in the completion predicate. It is the sole `delete(s.slots, …)`.
- **The §5.2 "graceful window of ten seconds" for the §10.1 hold-timeout termination is the shipped constant** in `onHoldTimeout` (`holdstate.go`, one shared pass-2 context, commit `3997f502b`). CODE-14 re-scopes it to a guard-acquisition deadline plus a per-member close context.
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
- **DECISION (`[operator.post-r16]`, from `[non-spec.16.fix-G1.1]`): every failed bind attempt is compensated, typed refusals included,** at `materializeSlot` and at `Binder.Resume`'s failure branch. The compensation cannot reclaim a session the gateway treats as live. Rejected: restoring the suppression and carving refusals out of §7.1. (`CORRECTS` by opt8 `[spec.1.review-mechanism.1]` and `[spec.1.fix-G1.1]`: "matching §7.1 with no carve-out" held only after the attempt's first session-addressed RPC; a pre-`PrepareWorkspace` `stageWorkspace` failure is compensated OUTSIDE §7.1's window, which §7.1 now permits by silence and rule 11 answers `absent`.)
- **DECISION (`[operator.post-r16]`): `lenny_slot_compensation_superseded_total` keeps every compensation and gains a `cause` label, `refusal` or `failure`,** derived by `compensationCause` in CODE-13 from CODE-7's sentinels. Rejected: excluding refusal compensations from the series.
- **DECISION: the attempt-scoped credential release (CODE-13 since the re-cut) is extended to `Binder.Prepare`'s credential-assignment failure arm.** It releases through `CredentialAssigner.Release(leaseID string)`, which widens the interface (`[non-spec.13.fix-G1.1]`) with a grep-closed sweep row for the test fakes. Rejected: session-wide `ReleaseSession`.
- **DECISION (`[non-spec.16.fix-G3.1]`): the adapterclient bind-sequence method signatures have one home, CODE-4's `client.go` Targets bullet.** `AssignCredentials` and `RunSetup` take a trailing `bindAttempt`, `PrepareWorkspace` a trailing `bindAttempt, midSession`, `FinalizeWorkspace` a `bindAttempt` before its shipped `midSession`, and `ResumeParams` a `BindAttempt`.
- **The no-entry compensation case is grounded on a `stageWorkspace` FAILURE PATH, never on an upload-free plan.** `stageWorkspace` has five error returns before the `PrepareWorkspace` send, which is guarded by `len(uploads) > 0`.
- **`mid_session` is SHIPPED on `FinalizeWorkspaceRequest` (field 4) and ABSENT from `PrepareWorkspaceRequest`.** SCHEMA-1 adds it to `PrepareWorkspace` alone; no production code sets it today, and the proposal wires it first in `upload_to_session.go`.
- **SCHEMA-1's field numbers are fixed and re-verified.** `bind_attempt` on PrepareWorkspace 5, FinalizeWorkspace 6, RunSetup 5, AssignCredentials 4, Resume 16, Shutdown 7; `PrepareWorkspaceRequest.mid_session = 6`; `ShutdownResponse.slot_reclaim = 3`; `ErrorCode` 28 and 29. Do not re-derive.
- **DECISION (`[operator.20-41-45]`, decision 20): the two new `ErrorCode` values take 28 and 29.** The enum has run contiguously 1 to 27. Rejected: the 1000-1999 range, a scheme nobody defined.
- **Two adapter `ErrorCode` values are minted, 28 and 29, and they take NO §15.1 row.** §15.1 catalogs the client-facing `code`, and no adapter `ErrorCode` string reaches REST. The enum is nested, so the Go constants are `adapterv1.Error_ERROR_CODE_*`.
- **DECISION (`[spec.2.fix-G1.1]`, `CORRECTS` `[spec.1.fix-G2.1]`): the staged §4.7.1 paragraph after rule 9 names NO envelope owner.** It is the home of why neither adapter code takes a §15.1 row, and ends at "the bind stage that received the refusal." Rejected: one owner (round 1), two owners keyed on pool mode (round 2), a stage-keyed three-arm list, an unkeyed pointer list, and a §15.1 `SLOT_FAILED` row (decides open decision 29).
- **The stage-to-envelope rule's homes are the staged §4.7.1 paragraph after rule 9 and the widened §15.1 `SETUP_COMMAND_FAILED` row; its reason's home is summary open decision 29 (`[spec.1.fix-design-G2.1]`).**
- **A setup-command refusal on a CONCURRENT pool reaches 422 `SETUP_COMMAND_FAILED` (`FailedPrecondition`) or the 503 fallback (`Aborted`), never `SLOT_FAILED`.** `slotbinder.go` wraps `RunSetup` errors in `SetupCommandFailure`, and `start.go`'s `setupFail` case precedes `slotFailed` (`[spec.2.review-mechanism.1]`).
- **The concurrent-path `AssignCredentials` adapter error is NOT wrapped in `CredentialAssignmentError`** (only lease minting is), so §5.2's exhaustion envelope is right for non-setup stages. A snapshot `Binder.Resume` refusal hits `writePodClaimError`'s default arm (503 `RESUME_FAILED`); a snapshotless concurrent resume goes through `applySlotRetryPolicy` and answers 422 `SLOT_FAILED`, consistent with §5.2 (opt7 `[non-spec.1.review-client-surface.1]`).
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
- **DECISION (`[spec-recheck.1.fix-G2.1]`, `CORRECTS` the `[prune.4.fix.1]` rationale-paragraph entry): the SPEC-5 §15.1 block's preamble is the ONE home of why the `SETUP_COMMAND_FAILED` row is widened.** The closing commentary after the exclusion-sentence fence is deleted; the untouched-list §15.1 bullet ends "The section itself is edited under SPEC-5's §15.1 block."; the spec Edge-cases envelope bullet is deleted (`[spec.1.fix-G2.1]`). Opt8 `[spec.1.fix-G2.1]` also deleted the preamble's second sentence, "A refusal arriving at any other bind-sequence request reaches the client under the envelope that stage already selects.", as a copy of the §4.7.1 after-rule-9 paragraph.
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
- **`leaked` and `acknowledged clean` agree ROW FOR ROW with the disposition table, checked at round 21.** `leaked` ⟺ not acknowledged clean over every `Shutdown`-performed cleanup, which CODE-13's `sbe.Leaked = cerr != nil || !cleanly` implements.
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
- **DECISION: the §7.3 resume classification is closed by TWO arms in `isTransientPodClaimError`, owned by CODE-5** (`CORRECTS` by `[operator.post-r16]`). `codes.Aborted` returns true; `errors.Is(err, ErrSlotBindAlreadyStarted)` holds the row in `awaiting_client_action` for the client's retry. Other resume causes keep the shipped classification. Exception: a refusal at a `/resume` setup-command request is wrapped in `*SetupCommandFailure`, whose existing `setupFail` case returns false first, so that row goes to `failed`, matching 422 `SETUP_COMMAND_FAILED`.
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
- **Two shipped tier-11 gates go red on SPEC-3's §12.6 edits, and they are the only two.** `TestPerReleaseSessionCountDrainAgrees_F5231` (delete its spec/12 block, remove the `12.1` spec-map row and the orphaned §12 credits) and `podStateGatewayWrittenSentence` (re-key; keep the trailing `ReportPodScrub` clause).
- **DECISION (`[non-spec.1.fix-G4.1]`, `CORRECTS` `[index-reconcile.6]`): the SPEC-3 tier-11 sweep's ONE home is the `**For SPEC-3**` paragraph in non-spec `### Documentation reconciliation tests, tier 11`, landed by S7.** S4 carries Tiers 0 and one deferral sentence. The spec-lane handler (`runSpecStep` in `implement-proposal-build.js`) edits only spec files and runs no tests, so a spec step cannot carry test work.
- **DECISION (`[non-spec.1.fix-G4.1]`): `## Spec files touched` reduces its spec/16 bullet to "(the rows SPEC-6 stages)" and its closing docs paragraph to a pointer at DOCS-1 to DOCS-4, CODE-9 and SCHEMA-1.** S7, S23 and the summary DOCS index lines are pointers; site lists live only in the DOCS blocks and the tier-11 Testing block.
- **DECISION: SCHEMA-1 absorbs the two proto report-trigger sentences,** `schemas/lenny-adapter.proto:308-310` and `:451-452`. The `:310` sentence boundary falls mid-line, so SCHEMA-1 quotes whole physical lines.
- **CORRECTION: `st.sessionID` has TWO setters,** so the shipped `Shutdown` takes `bound == true` for an `AssignCredentials`-bound slot that never started; the staged re-gating removes that arm.
- **§28.4's claim register is the file `tests/claim-map.json`.** Generator output under a tier-0 byte-identity gate; the validator does not check that `surface` paths exist.
- **DECISION (`[non-spec.14.fix-G2.1]`): SCHEMA-1's two `WIRED` claim-register rows land at S9 and the `ABSENT` row with CONF-1 at S22,** because the conformance cases its `surface` names, the tier-10 file and the tier-3 behavioural cases, land at S22 (the tier-3 directory itself is created at S9 by the descriptor gate) (`CORRECTS` by `[operator.redesign-r4]`). SCHEMA-1's `**Claim register.**` paragraph states it once.
- **DECISION (`[non-spec.16.fix-G4.1]`): the `## Testing` preamble in non-spec-changes.md is the single home of the step conventions** (the landing table, the regression tiers 0, 1 and 11 whose union with a step's rows is its Tiers line, spec-map registration, `// spec:` and `// diagnosis:`, claim-map seeding through `EXPLICIT` regenerated in the same commit; `CORRECTS` by `[operator.redesign-r4]`). The checklist preamble cites it.
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
- **`compensationCause` lives in CODE-13 (S19; CODE-4 before the re-cut) rather than CODE-9 (S10),** because it reads CODE-7's sentinels and S10 lands before S11; CODE-9's forwarder takes the cause as a string.
- **The adapter metric gate runs CODE → catalogs, never catalogs → code.** `adapter_metric_catalog_test.go` admits an adapter metric only when it reaches both `metrics.md` and §16.1; `spec161Metrics` is a hand-maintained list. SPEC-6 must land before CODE-9.
- **`docs/reference/metrics.md` is AUTHORED, and its `## Adapter metrics` table has five columns** (`Metric | Type | Labels | Description | Used by`). `observability.md` is a curated operator subset and owes no rows (decision 44).
- **The new-identifier sweep is ONE command and returns nothing:** `bind_attempt`, `unconditional_teardown`, `slot_reclaim`, both `SLOT_BIND_*` codes and both new counters have zero pre-existing carriers in `spec/`, `docs/`, `schemas/`, `charts/` and `migrations/`.
- **No non-proto wire schema, SDK or OpenAPI surface carries anything this proposal adds or retires.** `openapi.json` enumerates no REST error code, and `schemaassert.go` builds the conformance catalog by regex with no count pin.
- **The §7.4 mid-session upload path funnels every adapter error into `502 UPSTREAM_ERROR`,** and its admission guard is tenant-scoped, so the hold and rule-3 refusals owe no catalog or OpenAPI change.
- **The exclusive-path `SandboxClaim` strand CANNOT last,** because the WarmPoolController's orphan-claim GC drains the pod after `--claim-orphan-timeout`. A typed refusal means another attempt or a live session owns the pod.
- **The reclaim-hold refusal cannot drain a healthy pod from `Binder.Prepare` or `Binder.Launch` on the exclusive path** (`[prune.5.fix.1]`). It reaches `failPhase` only on a pod that is already draining or that carries a failed cleanup's residue.
- **DECISION (`[non-spec.10.fix-G2.1]`): CODE-8's closing paragraph is a pointer at the non-spec Edge-cases bullet "A pod whose drain failed keeps a dead attempt's token".** The residue is time-bounded by the orphan-claim GC; the shipped `failPhase` log-and-continue is a defects row (`[f6.apply.drain-failure-residue]`).
- **DECISION (`[non-spec.1.fix-G6.1]`): the failed-drain residue's ONE home is the summary defects entry "A failed compensating drain leaves the pod holding the dead attempt's registry entry.",** the only site stating the orphan-claim-collector bound. The Edge-cases bullet is a one-line pointer to it; the entry's owner clause names the spec-changes Edge-cases section (which names the reaper), so the two never point at each other.
- **DECISION (`[non-spec.1.fix-G6.1]`): the `SocketRuntimeProcess.Close` false-doc-comment defect lives only in the summary defects entry;** the Watch-out copy is deleted. Agreement between two copies is not the test; single source is.
- **DECISION (`[non-spec.11.fix-G1.1]`): the per-slot cleanup budget `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` is CITED and never instantiated in the proposal.** Worked numbers were wrong twice.
- **DECISION (`[f6.apply.compensation-queue-hold]`): the compensation holding a `queue`-pool's FIFO head is accepted, and the shipped wait bound that ignores pre-admission time is a defects row.** Open decision 46 is answered.
- **The §4.9-timer-before-tree-removal ordering is UNIFORM across every release path and predates this proposal** (decision 42, a defects row). `RotateCredentials` and `ExtendCredentialLease` cannot re-create a removed credential file.
- **DECISION (open decision 38, RESOLVED as staged): a cleanup whose runtime close succeeds and whose later act fails owes NO `leaked` signal.** Recorded as an unstaged-defects row; the credential-reachability companion stands in `### Open`.
- **DECISION (open decisions 35, 39 and 43, RESOLVED as staged).** 35: no scrub-ordering statement is owed, since `answerShutdown` starts the scrub after the removing arm's acts return. 39: the emptied-workspace outcome stays proposal-only. 43: coordinator preemption needs no second unsent-reclaim trigger.
- **The open decisions left with the human are 29, 30, 32, 33, 34, 36 and 50.** 20, 41, 45, 47, 48, 49, 51, 52 and 53 are answered by operator or f13 blocks; 28, 31, 35, 40 and 46 by the staging. Decision 35's stale contested flag was cleared in phase state rather than the entry restored (`[operator.f13-repair]`).
- **DECISION (`[f13.human-decisions]`, decision 51): whether §5.2 names `SLOT_FAILED` and 422 is left to a separate proposal.** It is recorded as the unstaged-defects row "§5.2. The exhaustion error names no code value or status."; naming it would not settle decision 29, which turns on the setup-command stage.
- **DECISION (`[f13.human-decisions]`, decision 52): SPEC-6's `lenny_adapter_leaked_slots` row is kept as staged,** restating §6.2's per-pod, until-termination definition (spec is right, code is the defect). The per-replica emission is appended to the unstaged-defects row on the gauge's attribution.
- **DECISION (`[f13.human-decisions]`, decision 53): CODE-1, CODE-4 and CODE-6 are RE-CUT so each deliverable appears in one checklist step, CODE-14 excepted as the `CORRECTS` clause below states.** CODE-1 keeps the precondition and cascade; CODE-15 takes the `if bound` gate, `Shutdown`'s doc comment, the `SessionScrubReporter` comment, `deregisterSlotLocked`'s doc comment and `runtimeHoldsLocked` ("The split gates"). CODE-4 keeps mint, carry and `unconditional_teardown`; CODE-13 takes the compensation, lease release and leaked disposition. CODE-6 keeps the hold; CODE-14 takes the per-slot guard and the expired-acquisition disposition. (`CORRECTS` by `[operator.redesign-r4]`: CODE-14's removing-site table states CODE-6's per-site hold predicates without landing them; its **What holds at S14.** paragraph describes what CODE-6 lands at S14, and its `Shutdown` rows land with CODE-1 at S16.)
- **CODE-14 owns the §10.1.4 per-member ten-second close context and the `guarded` conjunct in `terminateHeldSession`** (`CORRECTS` by `[non-spec.3.fix-design-G1.1]`, `[non-spec.3.review-applicability.1]`): the close context in its paragraph **`terminateHeldSession`'s guard and close context.**, and the `guarded` conjunct in the `completed` cell of the `terminateHeldSession` row of the removing-site table (`CORRECTS` by `[operator.redesign-r4]`). CODE-6 (S14) names no guard symbol and cites the removing-site table's rows, whose `**What holds at S14.**` paragraph states the S14 predicates (`terminateHeldSession` on `closeErr == nil && treeErr == nil` on the pass `ctx`, `releaseSessionSlot` on `treeErr == nil`) (`CORRECTS` by `[operator.redesign-r4]`). CODE-1 still owns `answerShutdown` and the scrub start.
- **The 0082 impact row (`[f13.other-proposals]`, corrected by `[operator.f13-repair]`):** no 0082 deliverable loses its subject; CODE-13's attempt-scoped lease release precedes 0082 CODE-3's `revokeFinalizeLease`, so each lease is released once in either order. The action asks a textual rebase and, if 0082 lands, a trigger or guard statement for CODE-8's `Binder.Prepare` arm.
- **DECISION: CODE-3 is the one home of the retired `leaked` trigger's retirement across every Go carrier.** The command is `grep -rn "cleanup tim" pkg/ --include=*.go | grep -v _test.go`; no `tests/` gate pins the rewritten strings.
- **CODE-1 contains NO markdown table.** The cascade is `shutdownReclaimOutcome`, whose five arms map onto rules 11-14, with rule 10 the preceding four-row precondition.
- **The staged handler has TWO returns that bypass `answerShutdown`.** The enumeration has one home, the `answerShutdown` rationale paragraph.
- **DECISION (`[non-spec.1.fix-G5.1]`): the `removeSlotTreeFn` / `removeSlotTreeVia` tree-removal seam is homed in CODE-6 and lands at S14.** `removeSlotTree` has exactly three production call sites and all use the seam; homing it in CODE-1 forces an S14↔S16 cycle.
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
- **Checklist coverage map (`[operator.f13-repair]`):** S1=SPEC-5, S2=SPEC-1, S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=SPEC-6, S7=DOCS-1/2/3 plus the SPEC-3 tier-11 sweep, S8=CODE-3, S9=SCHEMA-1, S10=CODE-9, S11=CODE-7, S12=CODE-8, S13=CODE-4, S14=CODE-6, S15=CODE-14 except its `Shutdown` rows, S16=CODE-1, CODE-15 and CODE-14's `Shutdown` rows, S18=CODE-2, S19=CODE-13, S20=CODE-5, S22=CONF-1, S23=DOCS-4, S24-S26=CODE-10..12. S21 is deleted and its id retired; S17 is folded into S16 and its id retired; test landing is the non-spec landing table's (`CORRECTS` by `[operator.redesign-r4]`).
- **DECISION (`[index-reconcile.4]`): each code step's `Depends on:` names the spec steps staging what it implements,** and S22 names S18. The deliverable index lists SPEC-1..6, CODE-1..15, CONF-1, SCHEMA-1 and DOCS-1..4 once each; CODE-11's line names its three files (`errorclassify.go`, `start.go`, `resume_setup_demotion_internal_test.go`).
- **DECISION (`[non-spec.1.fix-G5.1]`): checklist steps S10 and S12 to S18 are POINTERS,** "<deliverable> in full" or "<deliverable> except <part>, which lands with <step>", each ending with the tests the landing table assigns to the step (`CORRECTS` by `[operator.redesign-r4]`), plus only the cross-step splits the checklist owns; the literal and fake sweeps are landing-table rows the step names among its tests. S15's restatement had drifted from CODE-14's derivation table.
- **S14 and S16 carry tier 9 through their sweep rows in the non-spec landing table, and the tier-9 credential-fence file lands whole at S19** (`CORRECTS` `[non-spec.1.fix-G5.1]`'s "S16 carries tier 9 and the registered-but-unbound class of the tier-9 matching-reclaim arm", per the ledger's `[non-spec.4.fix-G2.1]`, `[non-spec.4.review-test-coverage.1]` and `[operator.redesign-r4]`). The `ShutdownRequest{` grep names three `tests/tier9_security` files and the bind-field grep names one, so neither tier-9 entry is stale.
- **DECISION (`[non-spec.16.fix-G2.1]`): S15 lands the guard-acquisition, context and on-expiry columns and the `guarded` conjunct of CODE-14's removing-site table at the `releaseSessionSlot` and `terminateHeldSession` rows, together with the `releaseSessionSlotUnderGuard` row; S16 lands the table's `Shutdown` row** (`CORRECTS` by `[operator.redesign-r4]`; test landing is the landing table's).
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
- **golangci-lint is NON-FATAL in tier 0 today** (`cmd/lenny-test/cmd_run.go`, "WARNING (non-fatal)"). A helper landing before its caller (the untokened accessor at S10, called at S16; `noteCompensationOutcome` at S10, called at S19; `slotCleanupBudget` at S13, called at S19) trips `unused` but fails no gate until the lint flips to hard-fail.
- **Agent pods render `RestartPolicy: Never`,** so an adapter crash ends the pod, and the registry, hold and `slotGuards` never survive into a restarted adapter. No adapter-restart recovery case is owed.
- **Leaked-slot occupancy is durable on the §7.1 reclaim path:** `SlotClaimer.ReleaseSlot(leaked=true)` skips the Redis DECR, so the slot stays counted in the §12.4-backed counter. Only `slotstate.Registry` and `slothealth.Tracker` are in-process.
- **The lock orders never cycle.** `Shutdown` is `s.mu → release → guard → s.mu`; `releaseSessionSlot`, `terminateHeldSession` and `acquireSlotGuardForResolve` are `guard → s.mu`; §10.1.4 pass 1 holds `s.mu` across every member and takes no guard.
- **The only writers still writing under an identifier after its deregistration are the guarded sections** (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `Resume`); `Checkpoint` only reads. `AssignCredentials` writes `credentials.json` inside `s.mu`.
- **`Resume` restores into two roots (`paths.Current`, `paths.Sessions`), and "the slot's workspace directory" names the whole tree-removal act,** as spec/05:545 uses it. `ExtractTree` is synchronous, so `Resume`'s own rollback runs after its writing stopped and the decision-47 sentence never marks it failed.
- **On an already-expired parent, `contextWithGraceDeadline` returns the expired context and `resolveShutdownGrace` falls back to the configured or default grace,** so an unguarded removal on an expired `Shutdown` context cannot hang `SocketRuntimeProcess.Close`.
- **§7.4 mid-session uploads buffer the whole body at the gateway (`parseUploadToSession`)** before streaming, so the guard they hold is short. The §7.4 admission guard is a live-binding check returning 409 `TARGET_NOT_READY`, which keeps `mid_session`'s exemption from being a bypass.
- **`SessionUsageMeter.Usage` and `Cumulative` ignore their context,** so `emitFinalUsage` cannot lose a report to an expired context. "No final usage report is lost" in the §10.1.4 bullet is not a finding.
- **`slotCleanupBudget` cannot divide by zero.** `slotBindRequest` is built only under `MaxConcurrentSessions > 1`, and `ResumeRequest.MaxConcurrentSessions` is floored at 1 by `maxConcurrentSessions()`.
- **`CleanupTimeoutSeconds` on `SlotBindRequest`/`ResumeRequest` is ZERO on every non-recycling pool and on a pool with no mirror row, so `slotCleanupBudget` returns its 5s floor on the common path** (opt7 `[non-spec.1.review-test-coverage.1]`, `resolve.go:114-128`; newer than and disagreeing with the archived "populated on every concurrent pool" entry, which is retired). `poolstore` rejects only a NON-zero value below `maxConcurrentSessions*5`, so the floor binds only when the value is unset.
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
- **DECISION (`[non-spec.2.fix-G5.1]`): the two mandatory-field literal sweeps (`ShutdownRequest{` and the `adapterv1.` bind-field grep) have ONE home each, the files-touched "Every other file that grep ... names" bullets.** Checklist S14/S16 point there; Testing "Scope accounting" keeps the silent-failure reason and the `adapterclient.Client` signature note, and each sweep's step and tiers are its row in the landing table (`CORRECTS` by `[operator.redesign-r4]`). The `adapterv1.` qualifier and the `files_updated_test.go` mid-session exception live in the bind-field bullet, whose grep now also carries `\|adapterclient\.ResumeParams{`. This replaces the older "three byte-identical sites" entry.
- **`binder.go`'s only apiserver writes are the two `podclaim.DeleteClaim` calls,** so the tier-2 case's "leaves the `SandboxClaim` present and the `Sandbox` untouched" is exact. No component writes another's status, no finalizer is added, and no reconcile sits on a request path.
- **DECISION (`[non-spec.1.fix-G3.1]`): the tier-3 `**The behavioural cases**` paragraph is a POINTER at CONF-1, in the tier-10 form.** Its bulleted list is deleted, so a CONF-1 change now reaches one site for driver text as well as assertion sets.
- **Every untokened registry entry is STARTED from the moment it exists.** Only `StartSession` and `ConfigureWorkspace` create one, both through `claimSessionSlotUnderLock`, which sets `st.started` in the same `s.mu` hold; `st.started` has no other writer and is never reset. A tokened non-mid-session request against it always meets rule 6.
- **DECISION (`[non-spec.1.fix-design-G3.1]`): CONF-1's stamp-once case is "a `StartSession` creates an untokened, started entry; a request carrying B is refused under rule 6; a later `Shutdown` naming B answers `superseded`".** `superseded` (rule 13) against `reclaimed` (rule 14) is the discriminating read. Rejected: deleting the case; a tokened-entry variant.
- **DECISION (`[non-spec.1.fix-G3.1]`): SCHEMA-1's `ErrorCode` and `SlotReclaimOutcome` proto comments are §4.7.1 rule pointers,** keeping only category and gRPC status (codes) or the answering rule (outcomes); the field-comment paragraph is one sentence. This deleted the false "An entry carrying no token is admitted rather than refused". Shipped values 1-27 carry no per-value comments.
- **The `ErrorCode` header "mirrors spec §15.1" was already false before this proposal,** for about nine shipped codes with no §15.1 row (`SESSION_NOT_FOUND`, `INVALID_WORKSPACE_PLAN`, `RUNTIME_OPTIONS_INVALID`, `TOKEN_BUDGET_EXHAUSTED`, `DEADLINE_EXCEEDED`, the three delegation codes, `PLATFORM_DEGRADED`), so codes 28 and 29 do not newly falsify it and the Deferred is not owed (`CORRECTS` by `[non-spec.1.review-client-surface.1]`).
- **`b.stageWorkspace` owes no named edit** (`CORRECTS` by `[non-spec.1.review-mechanism.1]`; `[non-spec.1.review-fresh.1]` had filed it). CODE-4 requires both callers to carry the token on `PrepareWorkspace`, and the `Client.PrepareWorkspace` signature change makes the unthreaded call a compile error. Same class as the `cl.Close()` refutation.
- **The positive arm of the reclaim-hold release at `releaseSessionSlot` IS pinned** by the tier-1 case "An uncontended acquisition on a cancelled context holds the guard" (`CORRECTS` by `[non-spec.1.review-mechanism.1]` and `[non-spec.1.review-test-coverage.1]`).
- **No per-slot process-group kill exists; kills live inside `Runtime.Close`** (`[spec-recheck.1.fix-G1.1]`). The deleted SPEC-3 commentary claiming one is gone, which closes the question of whether that kill falls inside the completion predicate.
- **`RunSetup`'s workspace-root `FailedPrecondition` producer is reachable only with an empty `--workspace-base`** (default "/workspace", `cmd/lenny-adapter/main.go`), and the shipped §15.1 row already omitted it, so the widened row owes it nothing (`[spec-recheck.1.review-client-surface.1]`).
- **No client SDK and no served OpenAPI document names `SETUP_COMMAND_FAILED` or any adapter `ErrorCode`.** The OpenAPI file is `pkg/gateway/externalapi/openapi/openapi.json`; `pkg/gateway/openapi` does not exist. `docs/reference/error-catalog.md` (DOCS-3) is the only parallel of the widened row.
- **SPEC-1's §4.7 `Shutdown` graceful-shutdown condition (signal only when no bound entry remains) is shipped** (`if !boundRemains` in `session.go`), so no runtime SDK owes an edit.
- **No §16.5 alert, `pkg/alerting` rule or `docs/runbooks` page references leaked slots, slot failures or `SessionScrub`,** so SPEC-6 owes no runbook companion.
- **`docs/reference/error-catalog.md`'s `RESUME_FAILED` row already states the row stays in `awaiting_client_action`,** so CODE-5's classifier arms owe no docs edit.
- **Every "reads, verbatim" anchor in SPEC-1 to SPEC-6 resolves exactly once in the tree, re-checked at 4374a5350 (opt8 `[spec.1.review-fresh.1]`, `[spec.1.review-citations.1]`, `[spec.1.review-applicability.1]`), and no commit touched `spec/`, `pkg/`, `cmd/`, `tests/`, `docs/` or `schemas/` since cd18be606** (opt7 `[non-spec.1.review-citations.1]`). Two quoted texts need not be verbatim: the SPEC-4 `claimed ──→ draining` block is the replacement, and the §4.6.1 closing sentence reads `[Section 6.2](...)` where the proposal writes `§6.2` (the bullet is located by its opening). A re-sweep is owed only after a commit to those paths. The §29.4 step-13 tail has three hits and is disambiguated by the step.
- **A scripted sweep of about 300 `file:line` citations across the proposal files found them accurate within one or two lines (`[non-spec.1.review-citations.1]`).** Do not re-sweep.
- **The `slotFailure*` stage constants are declared in the const block at `binder.go:288-298`,** and `slotfailure.go` declares `SlotBindError` and the `SlotFailureReason` values only.
- **Every production construction of the bind-sequence request messages and of `ShutdownRequest` is in `adapterclient/client.go`;** `cmd/` and `sdks/` hold none, so the `*_test.go` sweeps over `pkg/` and `tests/` cover every literal, including the one-line form in `tracing_context_release_race_test.go`.
- **`Binder.Launch` passes `idempotentRepeat=true` unconditionally, so `allowStarted` for `ConfigureWorkspace` is always true;** that is still equivalent to rule 6's exemption because the entry is keyed by session identifier.
- **`CredentialAssigner` declares only `AssignProto` and `ReleaseSession` today;** CODE-13 adds `Release(leaseID string)`, which `credassign.go` and `credassign/client.go` already implement. `bind.Adapter` is the concrete `*adapterclient.Client`, so the client signature changes need no interface edit.
- **The shipped concurrent path (`materializeSlot`) releases no §4.9 lease on any stage failure,** and `Binder.Prepare`'s credential-assignment failure leaks minted leases today (`leaseAssigned` is set only after success). CODE-13's release is new behaviour rather than a narrowing of an existing control.
- **Both assign loops range over `req.CredentialPools`, a MAP, and return on the first per-provider mint error,** so an earlier provider's lease is already minted. `fakeAssigner.AssignProto` mints `"cl-"+pool` and records `ReleaseSession` only.
- **The lease-release test cases have ONE home, the Testing bullet `**The credential release is scoped to the attempt**`,** with both entry points and three arms (mid-loop `AssignProto` failure, ordinary `AssignCredentials` failure, typed refusal).
- **`resumeOnPod` has TWO production callers,** `handleResume` and the §8.10 tree-recovery `sessionNodeReattacher.ReattachNode`, so CODE-5's `accountSlotFailure` also runs on tree recovery; the classifier arms run on `handleResume` alone. Its post-Resume failure arms roll back through `rollbackBinding` and the unconditional `Shutdown`.
- **`ReleaseSlotReservation` passes `recycle=false`,** so a failed-bind reservation release never fires the whole-pod scrub edge.
- **Every production adapter `Shutdown` caller is covered by CODE-4's table or `Client.shutdown`'s builder** (`slotbinder.go` two sites, `binder.go` two sites, `cmd/lenny-gateway/user_revocation.go`), so the §11.4 revoke teardown does not regress under rule 10.
- **The gateway writes the first `bound` claim status at claim acquisition, before any pod-side bind RPC,** so a failed bind's claim DELETE lands on a pod projecting `claimed` except inside the recorded coalescing window. §4.6.3 gives the gateway no `sandboxes/status` grant.
- **`RunSetupRequest` carries no `mid_session` field;** CODE-6's pre-resolve `mid_session` read is in `FinalizeWorkspace` and `PrepareWorkspace` only.
- **An entry created by `PrepareWorkspace` has `sessionID == ""`,** so the shipped `bound` gate does not remove its tree; any case asserting a release ran on such an entry needs S16 (CODE-15).
- **The two gates reading SPEC-1/SPEC-3-replaced rows stay green:** `recycle_scrub_trigger_consistency_test.go` reads substrings in the unchanged remainder of the §4.7 `Shutdown` row, and `adapter_metric_catalog_test.go` checks registered-to-catalog only, so S6's rows landing before S10 break nothing.
- **The SPEC-6 `cause` predicate matches `compensationCause`.** The reclaim-hold and rule-8 refusals map to `failure`, which is consistent because a hold refusal's compensation answers `absent` and never reaches the superseded counter.

#### Lifted 2026-09-23 (non-spec 2-8, prune-r8, opt7 non-spec 1)

Step ownership and the checklist:

- **DECISION (`[non-spec.6.fix-G1.1]`): S16 lands CODE-1 and CODE-15 together, and the S17 line is deleted without renumbering.** CODE-1's snippet ends at `reclaimSlotLocked`'s results and every consumer is CODE-15's. Step ids skip S17 and S21; nothing depends on either. S16's tiers (0,1,2,3,4,7a,9,10) and Depends are supersets of old S17's.
- **The tier-7a start-versus-reclaim race lands after `live` exists,** because S18 depends on S16, which carries CODE-15. The shipped `Shutdown` files `ReportSessionScrub` for any bound removed entry, so the race's `StartSession` arm passes only once CODE-15 and CODE-2 are in.
- **DECISION (`[non-spec.2.fix-G3.1]`): S9, S20 and S22 are pointers.** S9 is "SCHEMA-1 in full, except its `ABSENT` claim-register row, which lands with S22"; S20 "CODE-5 in full"; S22 "CONF-1 in full, with the `ABSENT` row SCHEMA-1 stages for it"; each ends with the tests the landing table assigns to the step (`CORRECTS` by `[operator.redesign-r4]`).
- **DECISION (`[non-spec.6.fix-G1.1]`): the send-before-require ordering rule's home is the checklist preamble;** the summary Watch-out is the pointer "S13 lands before S14 and S16."
- **DECISION (`[non-spec.1.fix-G4.1]`, opt7): client.go request-side ownership splits by step.** CODE-7 (S11) declares only `ShutdownReclaim`; CODE-4 (S13) owns the bind-method signatures, `ResumeParams.BindAttempt` and `unconditional_teardown` inside the `Client.shutdown` builder. CODE-4's Targets bullets no longer say call sites set the field.
- **The implement-proposal-build executor extracts deliverable ids by regex only in spec-lane steps** (`runSpecStep`), so a code or docs step line naming a SPEC id as a test subject is not parsed as landing it.
- **Checklist S19 names CODE-13's parts and one clause of bundling reason and restates no mechanism** (opt7 `[non-spec.1.review-applicability.1]`).
- **The S4-to-S5 forward reliance of §5.2's retirement clause is below the bar:** S4 runs tier 0 only and defers tier 11 to S7, which follows S5.
- **The decision-53 wording "CODE-13, CODE-14 and CODE-15 carry their later steps" was corrected in `[operator.prune-r8]`** to CODE-15 with CODE-1 at S16, CODE-14 at S15 and CODE-13 at S19; since `[operator.redesign-r4]`, CODE-14's `Shutdown` rows land with CODE-1 at S16.

Testing labels:

- **DECISION (`[operator.redesign-r4]`, `CORRECTS` opt7 `[non-spec.1.fix-G5.1]`): the `## Testing` landing table is the one step-to-test map.** Each row names a test file, the case or arm, its owning deliverable, its landing step and its tiers; every file in the files-touched Tests list has a row, and the literal and fake sweeps are rows. Each checklist step cites the table and lists no test carve-out, and its Tiers line is the union of its rows' tiers and the regression tiers the Testing preamble states once. The subsections state what each case asserts and name no step. Superseded: attributing each case to a deliverable alone, which left `tests/tier8_chaos/compensation_loss_test.go` with no landing step and spread landing clauses across Testing and the checklist.
- **DECISION: the adapter tier-1 heading is "### Adapter tests for CODE-6 and CODE-14, tier 1",** and the landing table's S15 row gives CODE-14 the guard-subject cases and the hold case's "refused at its guard acquisition" assertion (`CORRECTS` `[non-spec.2.fix-design-G1.1]`'s rejection of the rename; the preamble sentence that carried the split moved into the table under `[operator.redesign-r4]`).
- **DECISION (`[non-spec.2.fix-G2.1]`): the gateway tier-1 heading is "Gateway tests for CODE-4, CODE-5, CODE-7, CODE-8, CODE-9 and CODE-13, tier 1",** its group "CODE-4, CODE-5, CODE-8, CODE-9 and CODE-13's cases"; the spec-map bullets for `slotbinder_test.go` and `binder_test.go` say "the cases this change lands there".
- **DECISION: `tests/tier7a_load_local/slot_reclaim_hold_race_test.go` lands whole at S16, owned by CODE-1 with CODE-14's arms,** as its landing-table row states (`CORRECTS` by `[operator.redesign-r4]`). The §10.1.4 second run drives no `Shutdown`, so the file lands at S16 because the table gives it to CODE-1, and not because every case needs CODE-1's handler (ledger `CORRECTS` at `[non-spec.2.fix-G1.1]`).
- **DECISION (opt7 `[non-spec.1.fix-G2.1]`): the budget floor is pinned by running the "Per-stage compensation table." case twice,** once with `cleanupTimeoutSeconds` unset and once above the floor, citing CODE-4's **The budget:** rather than writing figures.
- **DECISION: "The counters." runs once per CODE-7 sentinel and once through `Binder.Resume`'s failure branch;** it is the one home of the `SlotReclaim` hook assertions.
- **The operator post-r16 additions have pinned tests:** the `cause` label arms and `TestHoldOrFailOnResumeErrorSlotRefusals_spec_7_3` with a negative bare-`FailedPrecondition` case.
- **The `runtime_close_failed` and `slot_tree_removal_failed` warnings are not asserted, deliberately:** they are diagnostic with no spec-named behaviour, and the hold outcome they accompany is asserted.
- **The §7.2 test annotation reads `§7.2 (interactive session model)`,** the heading at spec/07.

Single-source homes:

- **DECISION (`[non-spec.2.fix-G4.1]`): the S-2 window reasoning's home is the summary Decisions bullet "`schemas/lenny-adapter.proto` is opened once"; SCHEMA-1's opening paragraph owns the additivity facts.** The R1b impact row is a pointer keeping only "no identifier R1b renamed is touched".
- **DECISION (`[non-spec.4.fix-G1.1]`): the files-touched `pkg/adapter/holdstate.go` bullet is a pointer at CODE-14 and CODE-6.** The event names and fields of `runtime_close_failed` and `slot_tree_removal_failed` live only in CODE-6, which the removing-site table's report cells name, and the `terminateHeldSession` release predicate lives only in that row's `completed` cell of CODE-14's removing-site table (`CORRECTS` by `[operator.redesign-r4]`); the separately deferred guard release only in CODE-14.
- **The `releaseSessionSlot` guard-acquiring / under-guard split's one home is CODE-14's `Resume` guard paragraph.**
- **The summary 0073 impact row credits CODE-14 with the §10.1.4 close-context split** (`[non-spec.2.follow-up-fix.1]`).
- **DECISION (`[non-spec.8.fix-G1.1]`): the pod-exclusion withdrawal grounds' home is CODE-5 ("Three grounds, each sufficient");** the summary `SlotID == SessionID` Watch-out became a pointer and was later deleted in prune-r8.
- **CODE-13 names `slotbinder.go:543` as `Binder.ReleaseSlot`'s session-end teardown;** the tree ships no compensating `Shutdown`, and CODE-13 introduces `compensateFailedSlotBind`.
- **DECISION (opt7 `[non-spec.1.fix-G3.1]`): SPEC-6's §16.1 row is the ONE statement of when `lenny_slot_shutdown_untokened_entry_total` is non-zero.** CODE-1's reachability paragraph is deleted; the staged `answerShutdown` comment cites §4.7.1 rule 13 and §16.1; CODE-9's cell and the summary cite the row.
- **The untokened arm IS reachable from a compensated path,** through SPEC-5's edge case of an abandoned attempt's late `StartSession` after the reclaim's cleanup; "Nothing a compensated path produces reaches it" was false and is deleted.
- **DECISION: CODE-1's removing-arm comment cites the on-expiry cell of the `Shutdown` row of CODE-14's removing-site table in one sentence** and restates none of it (`CORRECTS` by `[operator.redesign-r4]`).
- **DECISION (opt7 `[non-spec.1.fix-G6.1]`): nine G6 copies are reduced to pointers** (CODE-15 doc-comment bullets, `exited_cleanly` restatements, CODE-2's `Aborted` argument, the `slotResolveError` reason, the wait-and-retry rejection, the guard RPC enumerations, the Design chokepoint and hold paragraphs, DOCS-3's envelope opening). The chokepoint pointer carries no count.
- **DECISION: CODE-13's bounded-wait argument is a pointer at the non-spec Edge-cases bullet, placed at the paragraph END** so "That is what carries the disposition" keeps its `*SlotBindError` referent. The Edge-cases bullet is relabelled "Faster pod churn." and points at CODE-5's trade paragraph, which names the reserved branch and the §7.3 re-attach.
- **The summary 0079 impact row names the two Go files 0079 and 0081 share** (`scrubreport_server.go`, `scrubreporter_seams.go`), different functions on each side; its action cell reads "the five shared spec files and the two shared Go files".
- **The refusal-cost comparison ("cheaper than a destroyed session") is a single site** in the summary.

Prune-r8 (`[operator.prune-r8]`):

- **The summary's "Where that lands:" lead-in and its mechanism bullets are deleted;** each restated a deliverable.
- **The summary Watch-out bullets on the pre-`PrepareWorkspace` failure, `UnhealthyThreshold(2)`, `TestValidTransitions_spec_6_2` and `SlotID == SessionID` are deleted,** each homed in a deliverable or Testing case.
- **The listener-close Watch-out became the ordering pointer "CODE-15 lands before CODE-13 (S16 before S19)",** and the Non-goals bullet names the defects entry **A pod whose runtime process has been closed cannot serve another session.**
- **The summary glosses the reclaim hold at first use in the file-collision Decisions bullet, and "the start-confirmation rule" as rule 8 in `## Goals`;** the deleted bullets held the only prior glosses.
- **The summary Decisions identity-refusal bullet names "the `codes.Aborted` arm CODE-5 adds to `isTransientPodClaimError`".**
- **The phase-gate Watch-out keeps its trap sentence and points at CODE-6's "Production call sites that must thread the parameter." table** for per-site `allowStarted` values.
- **The open-decisions preamble is one sentence** pointing at this log's `### Settled`, changelog and archive for answered entries.
- **The Deliverable index entries are id, files and heading phrase only;** CODE-7 and DOCS-1 to DOCS-4 are unchanged, CODE-10 keeps its shared-block pointer, CODE-11 its three files, and CODE-1 lists `session.go` alone.
- **Revision-history prose ("this revision withdrew", "is withdrawn") is removed from the staging;** revision history lives in this log.
- **Files-touched bullets for production, schema, claim-register and docs files are path, deliverable ids and regen commands.** The `tests/spec-map.json`, slot-address registration, comment-carrier and three sweep bullets stay verbatim because they state gate constraints.

Tree facts:

- **DECISION (opt7 `[non-spec.1.fix-G1.1]`): CODE-5 keys the gateway's persistent leak record BY SLOT,** in "The persistent leak record is keyed by slot.": `Tracker.RecordLeak(pod, slotID)` over a per-pod set, `DrainLedger.RecordLeak(ctx, podID, slotID)`, and `RecordSessionScrub` forwards the session id it discarded. Rejected: skipping the response route, suppressing the report on a tokened `Shutdown`, and dedup via `Registry.MarkLeaked`.
- **The leak double-book was CONFIRMED and is new with the compensation:** `MarkLeaked` is per-slot idempotent but `Tracker.RecordLeak` was `t.leaked[pod]++`, and one tracker feeds the report route and `accountSlotFailure`. Shipped `Binder.ReleaseSlot` never calls `RecordLeak`.
- **Accepted consequence of per-slot keying: a same-session retry on one pod whose compensation also goes unanswered counts one leak,** where the shipped code counted two; not written into staged text.
- **`DrainLedger` implementers are `drainLedger`, `fakeLedger` and `perReleaseNoopLedger`;** `scrub_report_wiring_test.go` already uses distinct session ids.
- **`SlotID == SessionID` on the concurrent path, so the report's session id is the slot key** (`slotbinder.go:149`).
- **The SCHEMA-1 proto comments cite matching rules:** code 28 rule 6, code 29 rule 5, `RECLAIMED` rules 12 and 14, `ABSENT` rule 11, and `SUPERSEDED` rule 13.
- **`adapterclient.ResumeParams{` test literals exist only in `client_test.go` (four, over bufconn to a real adapter);** the non-test literal is `binder.go:1607`. A struct field escapes the compiler, which is why the sweep grep carries the params literal.
- **The only tier-3 closed field set over a message SCHEMA-1 opens is `shutdown_recycle_wire_test.go`;** `session_address_wire_test.go` and `generation_fence_wire_test.go` pin named fields only.
- **`adapter_metric_catalog_test.go` derives the adapter metric set from `pkg/adapter/metrics.go` by regex,** so the untokened counter is held by S6-before-S10 ordering with no map edit.
- **The only tier-11 files pinning a §12.6 `sessions_served` sentence are `spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`;** `spec_28_index_rows_test.go`'s hits are a slugify fixture.
- **An `AssignProto` failure inside `Binder.assignCredentials` returns through the same failure arm as the `AssignCredentials` RPC failure** (`binder.go:949`), so CODE-13's mid-loop arm at `Binder.Prepare` is reachable.
- **The shared §10.1.4 close context and its comment are `holdstate.go:197-203`.**
- **Every `CredentialAssigner` implementer (about twelve test fakes, including `cred_renewal_extend_wiring_test.go`) falls inside the files-touched `ReleaseSession(` grep sweep;** `fakeExtendAssigner` already has `Release`.
- **`credassign.Client.Release` bounds itself with its own timeout on `context.Background()`,** so CODE-13's attempt-scoped release is safe on an expired caller context.
- **The compensation is bounded:** it runs on a detached context with the `slotCleanupBudget` timeout, and the resume-path guard wait is bounded by the `Resume` handler context through `GetChunk`.
- **The compensation's leaked disposition is stricter than the shipped concurrent-bind failure path,** where `ReleaseSlotReservation` always passed `false` (`slotbinder.go:502`); no pod self-report relaxes a bound.
- **The `start.go` `slotBinder` interface declares `DrainSandbox` and `ReleaseSlotReservation`;** CODE-13's call-site table stages the signature change.
- **The `cause` label multiplies a pool- and pod-labelled series by two,** matching `lenny_slot_failure_total`'s cardinality precedent; write amplification stays zero.
- **The gateway's drain-request write is a JSON merge patch on the agent Pod's annotations with FieldOwner `gateway`;** both WarmPoolController arms write `Sandbox.status.phase` under one field manager.
- **`resumeOnPod`'s snapshotless branch runs the full start bind, `RunSetup` included,** so a `/resume` reaches every bind-sequence stage.
- **DELETE on a `resuming` row writes the row and bumps `coordination_generation` but does not cancel the in-flight `resumeOnPod`,** so §7.2 step 1's cancellation is unimplemented and the mid-resume reclaim is testable only at `Binder` level with a cancelled context.
- **`resumeOnPod`'s failure branch does not touch `slotHealth`;** the only `slotHealth` use in `start.go` is `applySlotRetryPolicy`.

Closed questions (moved from `### Open`):

- **A `PrepareWorkspace` frame on an admitted call cannot write into a tree being removed (REFUTED):** CODE-14 holds the guard to the end of the call, and `stream.Recv` returns on server-context cancellation.
- **`s.reclaiming` growth is not a new defect:** one short key per failed cleanup, bounded like `slotGuards`; the non-recycling occupied pod is the `slotGuards` Open.
- **The recycle-scrub overlap with a different slot's in-handler rollback is pre-existing,** because shipped code releases that reservation with no `Shutdown`, and `onScrubFailure` absorbs a failed scrub.
- **`docs/reference/adapter-contract.md` owes no mirror of §4.7.1's rules;** DOCS-2's "Bind attempt token." paragraph links §4.7.1 and §15.4 and also covers the hold's `ABORTED` exclusion.
- **The wrapped comment at `recycle_scrub_path_test.go:1081-1083` is a non-carrier:** it describes the test's own three clean releases.
- **The summary R1b impact row no longer restates the proto-window reasoning** (`[non-spec.2.fix-design-G4.1]`), and "What the change costs." carries no counts.
- **Staged-added text: every staged spec link anchor resolves, and no staged spec, docs or proto text carries an N3 reserved phrase.** Gates over staged Go doc comments are unchecked (see `### Open`).
- **CODE-4's "Its requests carry an empty `bind_attempt`" sentence has no surviving copy.**

#### Lifted 2026-09-24 (opt7 non-spec 2-4, redesign-r4, opt8 spec 1)

Spec staging (opt8 spec round 1):

- **DECISION (opt8 `[spec.1.fix-G1.1]`): SPEC-2's staged §7.1 paragraph drops "the gateway sends no `Shutdown`, and";** it reads "Where this obligation does not reach an attempt, the same paragraph states what becomes of the slot state that attempt left on the pod." Rejected: moving the obligation's start to the reservation (reopens §4.7.9 step 5 and §6.2 `receiving_uploads`); dropping the `stageWorkspace` compensation; a permitting sentence.
- **A `stageWorkspace` failure returns a `SlotBindError` before any session-addressed RPC;** the only earlier pod-side call is `NegotiateVersion`, which carries no `session_id` (opt8 `[spec.1.fix-G1.1]`).
- **The exclusive path's no-reclaim disposition is stated by SPEC-3's §5.2 table row for a pre-`running` slot no cleanup reclaims and the Edge-cases bullet "A failed bind on a pod serving one session, which no reclaim reaches",** never by §7.1 (opt8 `[spec.1.fix-design-G1.1]`).
- **The §4.7 `Shutdown` row edit is labelled "first sentence" but its verbatim block is the first TWO sentences;** the replacement re-ends with the second and the remainder anchor resolves, so it applies cleanly.
- **The §6.2 Occupancy projection block holds the only `claimed ──→ draining` entry "in the fenced Occupancy projection block";** four more sit in the Recycle and Concurrent blocks, so that phrase is the disambiguator.
- **The 10s pass-2 close-context figure first landed in 6b4dc9341 (2026-06-04); 3997f502b (2026-08-22) reshaped it into the shared pass-2 context,** so "landed by commit 3997f502b" refers to the context form and is accurate. The SPEC-3 provenance citations (holdstate.go, spec/11 SIGTERM 10s, session.go, socketruntime.go, mcpruntime.go 5s) all hold.
- **§4.9's Standard-level rotation path (Checkpoint, replacement pod, `AssignCredentials`, `Resume`) stamps at `AssignCredentials`** under rule 4, and the later `Resume` compares equal.
- **`RegisterAdapterUnderTest` in spec/15 and §24.8 is the ExternalProtocolAdapter (client-protocol) suite,** so it does not contradict SPEC-5's commentary that no third-party gateway-adapter harness exists.
- **The deleted §4.6.1 sentence "The one-session-only invariant of §6.2 is the `recycle.enabled: false` configuration" has no inbound citation;** spec/06:402's use is unrelated data-residue text.
- **§6.1 admits preConnect only at `maxConcurrentSessions: 1`,** so the `DemoteSDK` row's "registry's single entry" holds.
- **SPEC-4's projection input is the controller's own last write read from its informer cache;** it adds no watch and no status write. The §4.6.3 ownership-table note "a level-triggered projection of `SandboxClaim` state" is a one-clause summary and below the bar as a missed edit site.
- **The carriage-table cells against rule 1 are two rules (carriage versus refusal), not a single-source copy.**
- **Opt8 spec-changes.md differs from the converged spec loop (cd18be606) only by commentary pruning and the CODE re-attribution;** every staged fenced block except the §4.7.1 carriage paragraph's last sentence is byte-identical. `state-machines.md:236-251` still carries the pre-change triggers, which are DOCS-1's.

Checklist and landing (opt7 non-spec 2-4, redesign-r4):

- **DECISION (`[non-spec.4.fix-G2.1]`): each new multi-case test file lands WHOLE with the last deliverable its cases need.** `slot_bind_attempt_race_test.go` with CODE-2 at S18; `slot_credential_reclaim_fence_test.go` with CODE-13 at S19; each with its spec-map and `slotAddressCaseFiles` rows. Rejected: per-arm labels across steps; landing at S14 with skips.
- **Every NEW CODE-14 tier-7a case lands at S16 in `slot_reclaim_hold_race_test.go`** (`[non-spec.2.fix-G1.1]`); S15's Tiers line still carries 4, 7a and 9 through its regression rows (`[operator.redesign-r4]`), which is not a contradiction.
- **The tier-7a "concurrent-resolve race" case covers the `bind_attempt` stamp, not CODE-14,** so it is never an S15 case.
- **Redesign-r4 Tiers-line facts:** S13 gains 8 (`token_service_unavailability_guard_test.go`), S13 and S20 gain 9, S19 and S20 gain 3 (`slot_address_absence_test.go`), S12 gains 3, 4 and 9, S10 gains 2, 3, 4 and 9, S15 runs 0, 1, 4, 7a and 9, and S18 runs 0, 1, 2, 3, 4, 7a, 9 and 10, all through regression rows.
- **Redesign-r4 landing steps:** `compensation_loss_test.go` at S19; the tier-3 descriptor gate at S9 (creates `tests/tier3_contract/adapter_bind_attempt/` and its spec-map entry); `TestRecyclePathUnansweredReclaimLeaksTheSlot_spec_5_2` at S20 (needs CODE-5's accounting); CODE-9's workspace-stage metric case at S10; each gateway tier-1 case at its subject's deliverable step (CODE-4 rows S13, CODE-13 rows S19, CODE-5 rows S20). A fixture extension lands in the earliest step whose row uses it.
- **The files-touched Tests list gains `spec_28_register_writers_test.go`, `concurrent_slot_lifecycle_doc_reconciliation_test.go` and `upload_to_session_test.go`** (whole-file credits under 5.2, 6.2 and 7.1), and drops `start_test.go`, which has no staged case, sweep hit or changed-signature call.
- **The `pkg/adapter` test callers of the widened helpers are named by the landing table** (resolve helpers: eight files; `noteRuntimeStarted`: four; `ReleaseSlotForTest`/`releaseSessionSlot`: seven); CODE-6's "the tier-1 work below names the files" points at it.
- **`Shutdown` keeps the shipped `deregisterSlotLocked` call until CODE-1 routes it through `reclaimSlotLocked` at S16;** CODE-6 and **What holds at S14.** say so.
- **In the `Shutdown` removing-site row, only the FIRST decision's `absent` and `superseded` arms acquire no guard;** a second decision answering either holds the guard until the handler returns (`defer unlockSlot()`).
- **S20 edits the `RecordLeak` doc comment "as CODE-3 leaves it" without naming S8 in Depends-on;** the linear order saves it (below the bar).

Deliverable homes (opt7 non-spec 4):

- **DECISION (`[non-spec.4.fix-G1.1]`): `releaseSessionSlotUnderGuard` takes `(sessionID string, guarded bool)`;** the guard-acquiring wrapper passes `lockSlotGuard`'s boolean and `Resume` passes true, because the hold's only release is deferred inside the body. The only signature site is CODE-14's `Resume` guard paragraph, which restates no predicate. Rejected: the body returning `treeErr` (a second release site); inlining the body.
- **DECISION (`[non-spec.4.fix-G3.1]`): CODE-13's "`SUPERSEDED` and `ABSENT` are reclaims acknowledged clean" paragraph is deleted whole;** outcome meanings are rule 15, the untokened arm rule 13, the acknowledged-clean inclusion staged §7.1, and the reason SPEC-2's commentary.
- **DECISION (`[non-spec.4.fix-G3.1]`): the compensation's two bounds (RPC deadline = budget, graceful window = budget/2) and the SIGTERM-pivot reason live ONLY in CODE-13's `compensateFailedSlotBind` doc comment.** `slotCleanupBudget` returns the budget and halves nothing; the "Per-stage compensation table." case asserts the half-budget relation itself.
- **DECISION (`[non-spec.4.fix-G3.1]`): DOCS-2's "Bind attempt token." paragraph ends its second sentence at "name the entry it is entitled to destroy."** The deleted clause said a no-longer-owning attempt gets `superseded`, false under rule 11 (`absent`); the page's `Shutdown` row carries both outcomes.
- **DECISION (`[non-spec.4.fix-G4.1]`): the queue-head hold is split by subject.** The summary defects entry "A queued waiter's wait bound does not cover the time it spends behind the FIFO head." owns the shipped wait-bound defect; the non-spec Edge case "A compensating `Shutdown` holds the pool's queue head for its own budget." owns the increment, deadline, `WARM_POOL_EXHAUSTED` exit and off-path rejection; each cites the other by bold label.
- **DECISION (`[non-spec.4.fix-G4.1]`): the ground against re-keying the report onto both errors lives in CODE-15's "The report stays keyed on `closeErr`";** the summary cleanup-residue defects entry cites it.
- **DECISION (`[non-spec.4.fix-G5.1]`): the CODE-2 rollback, CODE-5 started-session arm and `ensureSlotStateLocked` comments are a spec citation plus the act plus at most one trap clause.** A Go comment never cites a proposal deliverable id, which means nothing once the code lands in `pkg/`.
- **The chokepoint-placement reason (a comparison in the workspace handlers leaves the claim path ungated) survives only in the summary Decisions bullet;** the deleted Design chokepoint paragraph lost nothing, since CODE-6's `ensureSlotStateLocked` doc comment carries the callers, arm order and hold test.

Tree facts (opt7 non-spec 2-4):

- **Per-slot leak keying against Redis withholding (closes the `[non-spec.2.review-mechanism.1]` OPEN):** a same-session retry landing on the pod where its first attempt leaked is refused `SUPERSEDED` and its compensation answers `superseded` or `ABSENT` (clean), so no second withheld Redis unit hides under the per-slot set (`[non-spec.2.review-reliability.1]`, `[non-spec.2.review-performance.1]`).
- **A re-sent `ReportSessionScrub` now records its slot once** where the shipped `t.leaked[pod]++` counted twice; `drainLedger.RecordLeak` still re-runs `Unhealthy` and the idempotent `StampDrainRequest`, and `ReportSessionScrub` rejects an empty slot id.
- **`slotstate.Registry.MarkLeaked` is keyed by slotID and idempotent; the report route never calls it,** so a leaked report plus an unclean response for one slot books one leak on each ledger.
- **The tracker is per replica, so CODE-5's per-slot set dedupes the two routes only when both land on one replica,** and is otherwise no worse than the shipped per-pod int. `Tracker.Forget` has one production caller; a pod drained through `drainLedger.RecordLeak` is never forgotten (pre-existing growth).
- **The full `DrainLedger` implementer set is `drainLedger`, `fakeLedger` and `perReleaseNoopLedger`;** `scrub_report_wiring_test.go` names `RecordLeak` only in a comment. `drainLedger`'s forwarding is pinned indirectly: `recordNLeaks` must pass distinct slot ids or the threshold-drain envtest cases fail.
- **`emitFinalUsage` is a no-op in production today:** `Server.Usage` has no production `UsageMeter` implementation.
- **Every helper the staged Go blocks call exists in the tree or is declared by a staged deliverable** (checked round 4 across adapter and gateway), and `CredentialAssigner.Release` has both production implementers.
- **The only in-tree non-generated `ShutdownRequest` literal is `client.go`'s builder; the four-argument `Shutdown` invocations are `binder.go`, `slotbinder.go` and `user_revocation.go`,** all in CODE-4's table.
- **`cmd/lenny-compliance` derives the runtime-response `error.code` catalog from the proto `ErrorCode` enum by regex,** so codes 28 and 29 join the §15.4.6 battery's accepted set; below the bar, since no runtime code reaches `translateSlotBindRefusal`, and the new comments carry no brace or `=`.
- **CODE-12's perl matcher catches the tree's two Go claim-deletion carriers,** `binder.go`'s `drain` comment (joined across the break) and `podclaim/claimer.go`'s "projects `idle`".
- **Every §-number in spec-changes.md and non-spec-changes.md resolves to a numbered spec heading,** so `citation_document_test.go` cannot fail on staged text; `checkpoint_*_comment` and `identifier_resolution` gates do not reach staged text (closes the Go doc-comment gate Open, `[non-spec.4.review-fresh.1]`).
- **No bind path makes `StartSession` or `ConfigureWorkspace` an attempt's first request:** `Binder.Prepare` always sends a tokened `FinalizeWorkspace` (closes the rule-8 untokened-replacement Open, `[non-spec.4.review-mechanism.1]`). The abandoned attempt's late `StartSession` stays the only untokened producer.
- **CODE-13's `Binder.Resume` branch calls `compensateFailedSlotBind`** with the helper's arguments off `ResumeRequest`, and the cancelled-context case's `Binder.Resume` row pins the detached context (closes that Open).

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
- **WATCHOUT: CODE-14's removing-site table is the ONLY home of what each removing site does on an expired
  acquisition, completes on, reports and does with the reclaim hold** (`CORRECTS` by `[operator.redesign-r4]`). Its
  guard-acquisition and context columns summarize homes that stay: CODE-1's handler comment (the raw hand-out form
  and the unlock deferred ahead of the hold's release), CODE-14's `Resume` guard paragraph (the split and its call
  sites) and **`terminateHeldSession`'s guard and close context.**; do not cut those in a single-source pass. The
  report column names the `runtime_close_failed` and `slot_tree_removal_failed` log events whose `slog.Warn` form and
  fields CODE-6 states; a reduction keeps CODE-6's statements of them. The report and reclaim-hold cells record each
  site's implementation of the §5.2 disposition table's rows, and the §5.2 table stays the contract they cite. A site
  that states any of the four in its own words, or a partial `completed` predicate, is restored to a citation of its row; staged Go code keeps its expression and its comment cites the row. Keep the
  sentence beside the table that defines `guarded`, and answer a new removing site with a new row, never a second
  statement.
- **WATCHOUT: CODE-1's removing arm proceeds destructively when `lockSlotGuard` fails, and that is not a
  bypassable gate.** Removing unguarded is shipped behaviour; the decision is re-made under `s.mu`, and
  since decision 47 the hold is RETAINED and the pre-`running` clean exit is false.
- **WATCHOUT: `DemoteSDK` names two different things.** `SDKWarmRuntime.DemoteSDK` is what CODE-2's
  refusal arm calls; `Server.DemoteSDK` deregisters through `releaseSessionSlot` and must not be used there.
- **WATCHOUT: `ShutdownReclaim` must NOT route through `Client.shutdown`.** That builder sets
  `unconditional_teardown`, so a reclaim built through it would tear down the successor. The builder sets the
  field only from S13, so an S11 implementor could legally reuse it; the trap's one home is CODE-4's paragraph
  after the non-compensating caller table.
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
  filenames; `sed -n` over the glob concatenates the non-spec file first, so offsets past its end read the
  wrong file. A one-line grep misses a phrase wrapped across comment lines. Correct a ledger claim with
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
  in-pod-attacker dress collapses into the `### Deferred` entry on the adapter's unrendered TLS flags, and the capacity
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
  untokened-accessor Open read "OPEN, FILED" for rounds with the call still undeclared. Of the two spec-changes.md
  Deferreds filed in spec round 1, the gauge one was closed by `[non-spec.1.fix-G4.1]`; the "a request" one stays open.
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
  same class (see `### Settled`); a re-filing owes an argument that distinguishes it from that refutation.
- **WATCHOUT: CODE-4's `noteCompensationOutcome` rationale says "can" where its argument needs "cannot".** Below the
  bar; a repair must not read it as licence to make the forwarder package-level.
- **WATCHOUT: read CONF-1 before filing a tier-3 or tier-10 coverage gap.** The tier-3 block points at CONF-1's case
  list and abbreviates it. A checklist step's Tiers line is the union of its landing-table rows' tiers and the
  regression tiers the `## Testing` preamble states (`[operator.redesign-r4]`), and a deliverable's "Tiers:" line
  names the tiers its own new gates land at; neither is a coverage claim.
- **WATCHOUT: do not re-add a partial list of step conventions to the checklist preamble.** It keeps "Every spec step
  leads", the sentence citing the landing table and the Tiers-line union rule that the `## Testing` preamble
  states, and the ordering rule (`CORRECTS` by `[operator.redesign-r4]`).
- **WATCHOUT: the two tier-4/tier-8 guard tests are edited at S13** (the Client signature change), although the adapter
  begins refusing an empty token only at S14. Their landing-table rows put tiers 4 and 8 on S13's line;
  `[operator.redesign-r4]` added the missing 8.
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
  before this proposal (about nine earlier codes have no §15.1 row). Non-spec round 1 settled the disagreement with the
  `[index-reconcile.4]` Deferred in favour of this reading (see `### Settled`); do not re-file the header against SCHEMA-1.
- **Recurring non-findings, third list:** bare-basename citations (each resolves by context); `status.Convert`'s two
  test hits under `pkg/gateway`; the summary's "125 times" (spelling-specific, do not "correct" to 139); the
  `ShutdownRequest.recycle` proto comment (pre-existing); `SLOT_FAILED` missing from `error-catalog.md`; DOCS-4's
  "beside DOCS-2" against S23; `summary.md`'s SPEC-7/SPEC-8 (other proposals'); off-by-two drift in the 0078 row;
  `runtimeHoldsLocked` (created and consumed at S16); CODE-2's header omitting `slotsession.go`; the untested
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
- **Recurring non-findings, fourth list (spec rounds 2 to 4, spec-recheck 1, non-spec 1):** the §16.1.1 `k8s.pod.name`
  "Used on" cell (descriptive, no gate reads it); spec/28's credential-rotation fallback reached through §7.3; spec/15's
  `examples/runtimes/echo/` reference (the directory does not exist; pre-existing); §6.4's "on the first reference" (already
  loose); §4.6.1 naming no projection for a claim deleted in `sdk_connecting` (the tree leaves it to the reconciler too);
  §4.6.3's `SandboxClaim` row listing only "hold expiry" (pre-existing); `docs/api/internal.md`'s stylized
  `RuntimeAdapter` service; `tests/tier10_conformance/README.md` "Current state" (not exhaustive).
- **MISTAKE, three rounds naming WHICH envelope a refusal reaches in the §4.7.1 paragraph after rule 9.** Round 1 named
  one owner; round 2 named two keyed on pool mode; both were false for some stage and pool-mode pair, because the real key
  is the bind stage. Do not re-add an owner list, a §5.2 citation, or any envelope sentence there. The stage-keyed lead
  clause alone is true; the widened §15.1 row owns the setup-command mapping.
- **WATCHOUT: do not re-add commentary after SPEC-5's exclusion-sentence code fence.** The fence ends the §15.1 block and
  the preamble carries the reason. Likewise do not restore the SPEC-2 §7.1 connection-preference commentary paragraph.
- **WATCHOUT: deleting SPEC-3 commentary orphaned two antecedents ("the paragraph names", "The bullet's own reporting
  sentence"), now re-pointed to "the reclaim-hold paragraph" and "the `**Slot cleanup:**` bullet".** A later deletion
  near SPEC-3's commentary must recheck them. The SPEC-5 lead-in "for the reason the SPEC-1 commentary above gives" must
  point at the summary's `unconditional_teardown` decision. Do not carry "process-group kill" into any surviving site.
- **WATCHOUT: the non-spec Design paragraphs on the epoch ("The two residues the token closes that the epoch could
  not.", "What the caller holds, and why nothing travels on a response."), the S-2 window, "Where the gateway mints.",
  "The refusal must not drain a healthy pod." and "How the refusal reaches the gateway." are DELETED as copies.** Do not
  restore them; the summary Decisions bullets and the deliverables carry the reasoning.
- **TRIED AND WITHDRAWN: any claim about what an outcome-keyed default arm in the leaked disposition would do.** Copies
  called it "fail-closed" (drains a healthy pod) and "fail-open on version skew" (treats a failed close as clean); they
  described different hypothetical alternatives, and an unrecognized outcome arises only on skew, which does not apply
  pre-production. The built expression is `cerr != nil || !cleanly`; add no default-arm hazard claim anywhere.
- **WATCHOUT: a "second provider fails" lease-release test must fail from the Nth call, never a named pool,** because
  `CredentialPools` is a map. `assignCredentials`/`assignSlotCredentials` must return the identifiers minted so far on
  EVERY return, the mid-loop `CredentialAssignmentError` included; the mid-loop arm is what pins it.
- **WATCHOUT: do not reintroduce prose meaning into SCHEMA-1's proto value comments.** Generated Go copies them, so each
  prose copy has to be kept in sync with §4.7.1 by hand.
- **WATCHOUT: the tier-1 stamp-once case legitimately drives an admitted resolve against an UNSTARTED untokened entry,**
  because tier 1 calls `ensureSlotStateLocked` directly (`RegisterUnboundSlotForTest` is test-package only). Do not
  "fix" it to match tiers 3 and 10, where such an entry is unreachable.
- **WATCHOUT: S16 carries the tier-1 `exited_cleanly` injected-failure cases through its landing-table row** (Testing,
  "Adapter tests for CODE-1 and CODE-2", driving `Server.removeSlotTreeFn`), because "CODE-15 in full" does not carry
  them (`[operator.redesign-r4]`). S14's admission-guard sentence stays a pointer at "The mid-session conditioning."; do not re-expand it.
- **WATCHOUT: do not re-add a coverage claim to the S7 step line.** Its old "extended in place with the substrings that
  pin them" overstated coverage; the Testing section adds substrings only for the `Shutdown` row and the paragraph.
- **WATCHOUT: CODE-8's closing paragraph names the non-spec Edge-cases bullet by its bold title;** renaming that bullet
  breaks the chain CODE-8, then the edge case, then the summary defects entry.
- **WATCHOUT: CODE-14's heading names the files its guard edits actually touch** (`bindattempt.go`, `server.go`,
  `staging.go`, `resume.go`, `slotsession.go`, `session.go`, `sdkwarm.go`, `holdstate.go`), because the hand-out helpers
  live in `bindattempt.go`, `slotGuards` in `server.go`, and the guarded sections in `staging.go` and `resume.go`.
- **WATCHOUT: §7.2 step 4 bumps `coordination_generation` AFTER the step-3 reclaim,** so the reclaim is not
  generation-stale; a reviewer who reads step 4 first may think it is.
- **WATCHOUT: a create-time-reserved session whose pod holds its identifier for the pod's life is refused `ABORTED` on
  every `/start` retry until the pod retires.** That is decision 41; do not re-file it as a stall.
- **WATCHOUT: the §10.1.4 recycle-scrub overlap Open is PRE-EXISTING.** The shipped gateway already releases the
  reservation without waiting on the adapter's start-handler rollback; do not file it as introduced by CODE-13.
- **WATCHOUT: the adapter gRPC server requires and verifies a client certificate whenever `--tls-client-ca-file` is set**
  (`pkg/adapter/transport.go`, wired in `cmd/lenny-adapter/main.go`), and spec/04's contract names mTLS. The podspec
  renders none of the adapter's TLS flags, a pre-existing gap recorded under `### Deferred`; `unconditional_teardown`
  adds no capability an unfenced `Shutdown` lacked.
- **WATCHOUT: do not split CODE-1 and CODE-15 into separate checklist steps again (`[non-spec.6.fix-G1.1]`).** CODE-1's
  snippet ends at `st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)` and every consumer of those
  locals is CODE-15's, so CODE-1 alone fails `go build` on an unused `release`. A stub at S16 was rejected as invented code.
- **WATCHOUT: checklist steps are pointers, so a stale landing-table row is a step-assignment defect.** When a deliverable
  is re-cut, update its rows in the `## Testing` landing table and the Testing group label and heading with the checklist,
  and recompute the Tiers line of every step whose rows moved. The landing table is the owner list for the test files
  (`[operator.redesign-r4]`); do not add one back to files-touched. Name a case by what it checks, not by a helper name a later
  fix can rename.
- **WATCHOUT: S9 must except and S22 must name the `ABSENT` claim-register row.** It is the one cross-step fact the
  deliverables cannot express; "SCHEMA-1 in full" at S9 lands it twice and tier 0 fails on the duplicate `EXPLICIT` claim.
- **WATCHOUT: do not re-expand the files-touched `holdstate.go` bullet,** and do not restate CODE-14's split in CODE-6.
  The copy drifted before (no `guarded`, `removeSlotTree` for `removeSlotTreeVia`). Files-touched bullets are pointers.
- **WATCHOUT: do not restore the R1b row's sentence on proposal 0076's comment-only proto reopen.** It is reviewer-facing,
  and the problem statement's S-2 paragraph still records the verification. Do not re-add the "before `s.mu.Lock()`"
  clause to Testing; the Design's "The two-field precondition, checked before the lock is taken." is its home.
- **MISTAKE (opt7 `[non-spec.1.review-applicability.1]`): an archived entry said the `adapterclient.ResumeParams{`
  change makes every call a compile error, so no sweep was needed.** `ResumeParams` gains a struct FIELD, so the four
  `client_test.go` literals compile unchanged, send an empty `bind_attempt` over bufconn, and are refused by
  `validateBindFields` from S14. A future field on any adapterclient params struct escapes both the compiler and an
  `adapterv1.`-only grep; extend the sweep pattern with the params literal.
- **WATCHOUT: per-slot leak keying breaks tests that record several leaks under one slot id.** `concurrentSlotBinder.BindSlot`
  in `slotretry_load_test.go` returns "slot" for all 64 goroutines and never reaches threshold; `recordNLeaks` and
  `slothealth_test.go` repeat pod-only calls. The Testing bullet "The leak record is per slot." names them. A
  `RecordSessionScrub` forwarding an empty slot id collapses a pod's leaks to one (fail-open); the forwarded-id assertion discriminates.
- **WATCHOUT: "fail-closed arm" stays as a NAME** (the `untokened` doc comment, CODE-9's "Fires when" cell, the Testing
  create-and-stamp case). Those make no reachability claim; do not rename them while pruning reachability prose.
- **WATCHOUT: the guard scope by RPC is stated only in CODE-14's derivation table.** An earlier CODE-6 copy had silently
  dropped `Resume`; the fix was the pointer, not adding `Resume`. Do not re-add an RPC list at the hold paragraph, CONF-1
  rule 2 or the Testing hold case.
- **WATCHOUT: the reasons for `Shutdown`'s guard placement, the deferred hold release, the `live` gate and the scrub on
  every outcome live only in CODE-1's and CODE-15's staged inline comments** plus CODE-15's paragraph "The gate is `live`
  rather than `st.started`". Keep that paragraph when deleting nearby restatements; do not re-add doc-comment bullets.
- **WATCHOUT: the non-spec file has a SECOND edge-case section** (`## Edge cases and accepted failure modes`). Grep
  deliverable prose against it as well as against the spec-changes Edge-cases section when hunting copies.
- **WATCHOUT: the credassign package is `pkg/gateway/credentials/credassign`,** never `pkg/gateway/credassign`.
- **WATCHOUT: the untokened-entry residue survives a recycle** (the whole-pod scrub releases no registry entry and no
  per-slot process kill exists). It is pre-existing, recorded in the spec-changes accepted-failure list, and deferred to
  remediation position 2; do not file it as a cross-session security regression.
- **WATCHOUT: Testing's "one leak does not drain at threshold 2" case drives `accountSlotFailure` against a fake with no
  scrub-report route,** so it cannot observe a double book; the per-slot keying tests are what pin that.
- **Recurring non-findings, fifth list (non-spec 2-8 and opt7 non-spec 1):** DOCS-4's "lands beside DOCS-2" against S23;
  `summary.md`'s decision-53 record after the S17 fold; CODE-1 placing `reclaimSlotLocked` in `bindattempt.go` while CODE-6
  and files-touched say `slotsession.go`; `SocketRuntimeProcess.Close` mechanics described at three summary sites for three
  arguments; `docs/api/internal.md`'s StopSession-era service and status table; the `RESUME_FAILED` cause parenthetical
  (`error-catalog.md:157`, mirrors spec/15); `glossary.md:258`'s claim-deletion sentence (already false); `troubleshooting.md`'s
  `WARM_POOL_EXHAUSTED` cause table; `architecture.md:237` and §16.1.1's "Used on" column; the §4.7 RPC tables sitting under
  the `#### 4.7.1` heading; `binder.go:1629` off by one; and the handleFinalize `failSession` on a `Prepare` typed refusal (the
  0082 race).
- **WATCHOUT (opt8 `[spec.1.fix-G1.1]`): §7.1 states a MINIMUM obligation; do not re-add any sentence saying the gateway
  sends no `Shutdown` outside it.** Such a sentence re-forbids CODE-13's compensation of a pre-`PrepareWorkspace`
  `stageWorkspace` failure. An over-send is permitted and rule 11 answers it `absent`; the exclusive path's `failPhase`
  behaviour is a tree fact carried by the Edge-cases bullet and the §5.2 table.
- **WATCHOUT (opt8 `[spec.1.fix-G2.1]`): do not re-add a stage-by-stage envelope mapping anywhere in the spec-changes
  commentary** (preamble, Edge cases or after the SPEC-5 fence). The mapping lives only in the §4.7.1 paragraph after rule 9
  and the staged §15.1 row; the reason lives in summary open decision 29.
- **WATCHOUT (opt8 `[spec.1.review-reliability.1]`): the late-insert race is WORSE under the token design than when first
  disputed.** `PrepareWorkspace` has no ctx check before `resolvePrepareStagingDir`, and grpc-go can hand a buffered first
  frame to the handler after RST_STREAM, so an attempt's own first frame can create an entry stamped with its token AFTER
  its reclaim answered `absent`: the stuck entry of the gateway-crash Edge-cases bullet, reached with no crash. A later
  round wanting to file it owes a staged-spec remedy (a cancelled-call check under the registry lock, or a tombstone), not the race.
- **WATCHOUT (`[non-spec.4.review-reliability.1]`): a `StartSession` rollback on its expired context can remove the entry
  unguarded while a compensating `Shutdown` holds the guard,** if it takes `s.mu` before the `Shutdown`'s second decision.
  Both removals succeed but the hold is retained for the pod's life, and the `Shutdown` answers `ABSENT` (clean), so nothing
  counts the pod. Microsecond window, bounded by windowed-failure drain on refused retries; same class as CODE-14's premature removal. Not filed.
- **WATCHOUT (`[non-spec.4.fix-G5.1]`): the CODE-2 rollback comment keeps "Deregister nothing here" deliberately.** It is the
  only warning at the site where an implementor would add `releaseSessionSlot`, which deletes a successor attempt's staged
  workspace; do not cut it as restatement.
- **WATCHOUT: the Testing note "turns red equally if the report is keyed on both errors" is a discriminator, not a copy of
  CODE-15's argument;** keep it.
- **WATCHOUT: the non-spec Edge case on the queue-head hold carries `spec/05:545` and `:440` line citations.** `proposals/`
  is outside the citation-gate domain, so they are evidence; do not file them as drift.
- **WATCHOUT: "counts every session served" (`configuration.md:91`, `execution-modes.md:23`,
  `operator-guide/configuration.md:307`) mirrors the unedited spec/05:404 comment,** and a running session ended by the §10.1
  hold-timeout termination files no report under SPEC-3. Any fix is a spec edit first; not filed.
- **WATCHOUT: §5.2 "Recycle lifecycle" says `cleanupCommands` run after every ended session's per-slot tree is removed;** a
  failed last-slot cleanup inside the recycle `Shutdown` falsifies it, but the gap predates 0081. Not filed.
- **WATCHOUT: SPEC-4's informer read-back opens a stale-read window** (reserved, bound and deleted before the controller's
  `claimed` write is observed projects `idle`). It is the summary's §4.6.1 claimed-and-released defects row; do not re-file against SPEC-4.
- **WATCHOUT (opt8 `[spec.1.review-single-source.1]`): most remaining duplication in spec-changes.md is commentary
  restating a staged block's reason;** the orchestrator bars commentary-only findings, so spend no verifiers on them.
- **WATCHOUT: the `compensateFailedSlotBind` comment's §11.4 revoke fan-out precedent sentence is a defence, not an
  instruction;** it was not filed.
- **Recurring non-findings, sixth list (opt7 non-spec 2-4, opt8 spec 1):** the RunSetup workspace-root `FailedPrecondition`
  omitted from the §15.1 cause sentence; the §4.7 RPC tables under the `#### 4.7.1` heading; the three spec/29 step-13 anchor
  matches (only the §29.4 one is staged, scoped by step number); `error-catalog.md:155-156` "a retry rebinds a fresh pod"
  (mirrors spec/15); the proto `Error` comment's "spec-mandated error code from §15.1" (already false); the §4.6.3 ownership
  note; the SPEC-3 biconditional as a `maxSessionsPerPod` relaxation (the tree files `ReportSessionScrub` only from `Shutdown`);
  `IncSlotCompensationSuperseded` taking an unlabelled outcome argument.

### Open

- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** OPEN, summary open decision 29: the same refusal is non-retryable at one stage and retryable at another. The `/resume` setup-command addendum is closed: its premise was false (opt7 `[non-spec.1.review-client-surface.1]`, see `### Settled` on the §7.3 classifier).
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** OPEN, summary open decision 36: a third-party harness cannot force a failed removal.
- **Neither newly recorded residue has any observability** OPEN, summary open decision 33.
- **Are "registry entry" and "bound entry" defined anywhere?** OPEN, summary open decision 32.
- **Churn from the third accounting caller** OPEN, summary open decision 34: at `maxConcurrentSessions: 2` a failed §7.3 re-attach drains a fresh pod.
- **Should SPEC-5 also correct `spec/06:290`?** OPEN, summary open decision 30. Pre-existing.
- **Should the tier-3 descriptor gate pin the `SlotReclaimOutcome` enum values?** OPEN, summary open decision 50 (`[index-reconcile.5]`).
- **Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?** OPEN, pre-existing: `slothealth.Tracker` and `slotstate.Registry` are replica-local and in-process, so a restart forgets leaked pods, and this proposal raises the leak rate.
- **Tier-4 fixture extension** OPEN: whether the per-case interceptor perturbs the other `recycleAdapterDialer` call sites, and whether `recycle_scrub_path_test.go` can carry the concurrent-slot compensation case, is untraced.
- **Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?** OPEN, recorded in the summary's unstaged-defects rows: a CREATE, `bound` patch and DELETE inside one reconcile window leaves `("", false)` and an idle unscrubbed pod. Correctly scoped out; do not re-file.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** UNVERIFIED: one leaked slot retires the pod there.
- **Does §5.2's "still in flight" mean "not yet admitted"?** UNVERIFIED: CODE-6 says an upload already past its resolve is not being admitted. Pre-existing.
- **Does the ten-second provenance paragraph belong in SPEC-3's commentary at all?** OPEN: decision 40 appended sentences to it; the next prune decides.
- **Does rule 8's take-back, stated outside the critical section, let a lagging attempt close a successor's session?** OPEN, below the bar: every reachable ordering has the reclaiming `Shutdown` tear the runtime down first.
- **Does the rolled-back-start successor outcome need spec or docs text?** OPEN, FILED (`[non-spec.8.review-docs-alignment.1]`): the non-spec Edge-cases bullet "A rolled-back start can close a successor's runtime session" has a client-visible outcome (the successor's started session loses its runtime) that no landing spec or docs text states; rule 8 says only "takes the session back off the shared runtime process". No fix entry is recorded.
- **Do `scrubreporter_seams.go:389-391` and `scrubreporter_seams_test.go:726-727` fall inside CODE-10's predicate?** UNVERIFIED: the application-time grep decides.
- **Is `pkg/adapter/sessionscrub_emit_test.go:16-19` stale against its own terminate case?** UNVERIFIED, pre-existing.
- **Does an implementor's reflow of the podspec and gatewaylink comments shift two `tests/claim-map.json` line surfaces?** UNVERIFIED: the implementing step re-checks both rows.
- **What retires a continuously occupied `maxConcurrentSessions > 1`, `recycle.enabled: false` pod?** OPEN, FILED: nothing does, which makes the `slotGuards` never-pruned bound claim false there.
- **Does the Testing section owe a disposition for DOCS-2's `DemoteSDK` row?** OPEN, FILED: the DOCS-2 block dispositions three of four edits and no gate reads the row.
- **Does the files-touched line "`slotfailure.go` · CODE-9 and CODE-13" still credit CODE-9 with an edit CODE-9 does not stage?** OPEN, FILED (opt7 `[non-spec.1.review-citations.1]`, `[non-spec.1.review-edit-sites.1]`, `[non-spec.1.review-single-source.1]`): `slotFailureWorkspaceFinalize` belongs in the stage-constant block at `binder.go:288-298`, a file CODE-9's heading lists, and CODE-9 edits nothing in `slotfailure.go`. The prune cut the bullet that located the constant. No fix entry is recorded; the Open closes once the line drops CODE-9. staticcheck's `unused` marks a const BLOCK used when one member is, so the constant is safe only inside that group.
- **Does CODE-6's paragraph "The reclaim hold has one predicate and two test points." still name `acquireSlotGuardForResolve`, which is CODE-14's (S15)?** OPEN (opt7 `[non-spec.1.fix-design-G5.1]`): at S14 the only test point is `ensureSlotStateLocked`. Same step-ownership class; the G6 pointer reduction may have removed the name.
- **Is the reservation release on the caller's context a defect?** OPEN, FILED (opt7 `[non-spec.1.review-reliability.1]`, `[non-spec.1.review-test-coverage.1]`): `SlotClaimer.ReleaseSlot` runs a Redis script on the caller's ctx and `Client.StartSession`/`Resume` apply no per-RPC timeout, so a bind that failed on its deadline fails its reservation release too; on a cancelled resume ctx `releaseResumeSlot` errors and CODE-13's `leaked || relErr != nil` marks the slot leaked whatever the reclaim answers. A mechanism lens decides whether that is intended.
- **Does the summary's failed-drain defects entry name an owner the spec Edge-cases section does not state?** UNVERIFIED (`[non-spec.2.review-single-source.1]`): the entry says the reaper's owner "is named in the spec-changes file's Edge-cases section", which names the reaper but no owner (decision 41 names remediation position 2). Below the bar; a fixer touching it points at decision 41.
- **Does the non-spec Design text still restate the two-field `unconditional_teardown` argument?** OPEN [`[spec-recheck.1.fix-G1.1]`, `[spec-recheck.1.fix-design-G1.1]`]: filed for the non-spec lane; `[non-spec.1.fix-G1.1]` then made the summary bullet the home, so a check that no copy survives is what remains.
- **Do two staged Go comments still carry proposal-level text?** OPEN (`[non-spec.4.fix-G5.1]`, `[non-spec.4.fix-design-G5.1]`), left unedited by design as separate findings: CODE-5's `Aborted` arm comment names CODE-6 and CODE-2, ids meaningless in `pkg/gateway`; `ensureSlotStateLocked`'s second doc paragraph ("no handler carries a second copy") restates CODE-6's prose lead-in.
- **Does SPEC-6's `cause` label (`refusal`, `failure`) break §16.1.1 "Distinguishing `error.type` from `reason`"?** OPEN (`[non-spec.4.review-edit-sites.1]`, re-noted by opt8 `[spec.1.review-fresh.1]` and `[spec.1.review-edit-sites.1]`): a label naming a failure's cause should be `error_type`. Not filed: the name was the operator's (`[operator.post-r16]`) and the tree ships a `cause` label already. A human decides.
- **Is §15.4.2's `DRAINING` "signals the agent to stop" the same act as §29.4 step 13's CH-RUNTIMEOPS `terminate` frame?** UNVERIFIED (opt8 `[spec.1.review-citations.1]`): the staged §29.4 sentence ties the frame to the §4.7 row's graceful-shutdown condition; plausible, not proven. A mechanism lens decides.

### Deferred

- DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start` failure
  branches release by session identifier alone. Carried in the summary's unstaged-defects section.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline lets the
  adapter apply its default grace period" is false. CODE-7 opens the file and stages no repair.
- DEFERRED [spec-changes.md, commentary wording]: the untokened-entry commentary calls the §10.1 hold-timeout
  termination "a request" where §5.2 says it runs under no request. Filed in spec round 1 as a contradiction against the
  §5.2 hold paragraph's "runs under no request" (`[spec.1.review-citations.1]`, `[spec.1.review-fresh.1]`); no fix entry
  is recorded, so it stays open. Spec-recheck round 1 judged it below the bar as unapplied commentary and did not
  re-file it; a fixer touching that sentence for another reason corrects it in the same edit.
- DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118]: the `Sandbox.status.phase` doc comment lacks the input
  SPEC-4 adds. Record only; any fix goes through the Go comment plus `make generate`.
- DEFERRED [docs/api/internal.md]: the gRPC status table omits `ABORTED`. Pre-existing, for a separate finding.
- DEFERRED [schemas/lenny-adapter.proto, the LEAKED comment's drain-ledger sentence]: it may restate a rule whose home
  is §4.6.3 or §5.2. SPEC-3 does not falsify it; a reduction is a finding of its own.
- DEFERRED [pkg/controller/sandbox/podspec and cmd/lenny-adapter/main.go, a separate security finding]: the podspec
  renderer passes none of `--tls-cert-file`, `--tls-key-file` or `--tls-client-ca-file` to `lenny-adapter`, so in a
  rendered pod the adapter gRPC server runs plaintext without client authentication (`cmd/lenny-adapter/main.go:112-115`)
  and an in-pod agent can send a co-tenant's unconditional `Shutdown` (`[non-spec.6.review-security.1]`). Pre-existing:
  `unconditional_teardown` grants nothing the shipped untokened `Shutdown` did not.
- DEFERRED [summary.md, the 0078 impact row] (opt7 `[non-spec.1.fix-G4.1]`, `[non-spec.1.fix-design-G4.1]`): the row
  says "CODE-4 sets `unconditional_teardown` at every non-compensating `Shutdown` caller, so
  TestConcurrentShutdownsSendOneDrainSignal_spec_6_4 ... take that field at their `ShutdownRequest` literals". That is
  false: CODE-4 edits no test literal. Those literals take the field through the `ShutdownRequest{` sweep that CODE-1's
  two-field precondition forces (files-touched sweep bullet; Testing "Scope accounting"). Marked a separate finding by the
  G4 design, so not edited.
- DEFERRED [implementation-checklist.md, the S23 line] (opt7 `[non-spec.1.review-applicability.1]`): "It carries the step
  id S23 rather than a position beside S7 because renumbering the earlier steps would invalidate every dependency line"
  gives a false reason, since a step can be listed at any position without renumbering, and DOCS-4's own text says it lands
  beside DOCS-2. Filed as part of the DOCS-4 ordering finding; no fix entry is recorded.

## Ledger

### [spec.2.review-fresh.1]
FACT: the spec.1 fixes are three deletions only: the Edge-cases bullet "A bind-sequence refusal reaches the client under the envelope its stage already selects", the SPEC-5 §15.1 preamble sentence restating it, and "the gateway sends no `Shutdown`" in the SPEC-2 §7.1 paragraph. No other proposal file names the deleted bullet or the deleted clause. EVIDENCE: diff against scratchpad/cp-snap/0081-opt8/spec-r1-prefix; grep of the proposal directory.
FACT: after the reduction, the stage-to-envelope rule has two staged clause-length statements: the §4.7.1 paragraph after rule 9 ("the client receives the error the gateway already returns for the bind stage that received the refusal") and the tail of the replacement §15.1 `SETUP_COMMAND_FAILED` exclusion sentence, which covers only a FailedPrecondition answered at another stage. Each is one clause, so neither is a rule (g) copy. EVIDENCE: spec-changes.md:1012, :1116
USEFUL [spec.1 fix of the §7.1 no-Shutdown finding]: the new wording ("Where this obligation does not reach an attempt, the same paragraph states ...") still resolves. The §5.2 paragraph after the table covers the single-session failPhase case in its closing sentence. EVIDENCE: spec-changes.md:352, :596

### [spec.2.review-mechanism.1]
FACT: The round-1 spec diff has three hunks: it deletes the Edge-cases bullet on bind-sequence refusal envelopes, deletes "the gateway sends no `Shutdown`" from SPEC-2's §7.1 paragraph, and deletes the duplicate "any other bind-sequence request" sentence from SPEC-5's §15.1 preamble. After those deletions the envelope rule has two homes that agree: §4.7.1's paragraph after rule 9 and the staged §15.1 exclusion sentence (the second covers only the setup-command row). Nothing in the proposal still points at the deleted bullet or the deleted clause. EVIDENCE: spec-changes.md:1006, :1116; non-spec-changes.md:2705, :2754-2755
FACT: §7.1's surviving pointer "Where this obligation does not reach an attempt, the same paragraph states ..." resolves. The §5.2 paragraph after the table ends with the single-session claim-deletion retirement, and the table has a row for a pre-`running` slot that no cleanup reclaims. EVIDENCE: spec-changes.md:592-593
USEFUL [spec.1.fix-G1.1]: its WATCHOUT (do not re-add a no-Shutdown sentence outside §7.1) matches the rationale at spec-changes.md:426-428, which reads "owes no reclaim" as a minimum obligation rather than as a prohibition.

### [spec.2.review-single-source.1]
DECISION: no findings on the round-1 fix hunks (Edge-cases envelope bullet deleted, §7.1 "sends no Shutdown" clause cut, SPEC-5 §15.1 preamble sentence cut) — BECAUSE no other proposal site names the deleted text, and the stage-to-envelope rule now sits in the §4.7.1 paragraph after rule 9 and the staged §15.1 exclusion sentence, which round 1 settled as its homes — ALTERNATIVES: filing the §15.1 exclusion sentence's closing clause as a copy of the §4.7.1 paragraph, rejected as re-litigating the round-1 fix.

### [spec.3.review-applicability.1]
DECISION: no findings for the applicability lens in spec round 3 of run 0081-opt8 — BECAUSE the only change since the spec-r1 snapshot is two deletions (the Edge-cases envelope bullet; the §7.1 "sends no `Shutdown`" clause and the SPEC-5 §15.1 preamble sentence), neither leaves a dangling reference inside spec-changes.md, and every staged anchor still resolves — ALTERNATIVES: re-filing the §4.6.1 "one-session-only invariant of §6.2" paraphrase as an unresolvable anchor, rejected as settled (archive entries at spec.15 and opt8 spec.1: the bullet is located by its opening and replaced whole).
FACT: re-checked byte-exact with grep -cF at this round; every fenced "reads, verbatim" anchor matches exactly once: spec/04 §4.1 third sentence, `Shutdown`/`DemoteSDK`/`ReportSessionScrub` rows (:674, :686, :692), §4.6.1 lead-in and both claim-deletion bullet openings (:408-416), §4.7.9 step 5; spec/05 :453, :545, :561 anchors; spec/06 :80 prose clauses, :95 fence entry, :148, :152, :155, :290; spec/07 §7.1 atomicity paragraph, §7.2 preamble/step 2/step 3, §7.3 item 4; spec/12 prose and DDL; spec/15 :1136 row's four sentences, :1469 insertion point directly above `#### 15.4.1`. The §29.4 step-13 anchor matches three times in spec/29 (:645, :711, :982) but "step 13" scopes it to :711 alone. EVIDENCE: spec/29_communication-scenarios.md:703-711
FACT: every staged cross-file anchor (#471, #479, #49, #101, #71, #73, #74, #1542, #154, #61, #62, #52, #151, #463, #461, #1611) resolves to an existing heading, and `ShutdownResponse.exited_cleanly` backs the disposition table's clean-exit column. EVIDENCE: schemas/lenny-adapter.proto:1665-1668; spec/16_observability.md:289
USEFUL [spec.2.review-fresh.1]: its statement that nothing in the proposal names the deleted Edge-cases bullet held for spec-changes.md; the summary's "Edge-cases section" pointers (summary.md:529, :962) name the section, not the bullet.

### [spec.3.review-citations.1]
DECISION: no findings — BECAUSE every verbatim anchor the staged spec edits quote (§4.1 third sentence, §4.7 `Shutdown`/`DemoteSDK`/`ReportSessionScrub` rows, §4.7.9 step 5, §4.6.1 occupancy bullets and opening input clause, §5.2 scrub-model opening, slot-cleanup action/reporting/leaked sentences, §6.2 fence entries and projection clauses, §6.2 resuming-cancel clause and Client-visibility clause, §7.2 preamble/step 2/step 3, §7.3 list item 4, §12.6 prose and DDL, §15.1 SETUP_COMMAND_FAILED four sentences, §15.4 insertion point, §29.4 step 13 tail) matches the tree, and every code citation checked holds — ALTERNATIVES: filing the §4.6.1 closing-sentence parenthetical quote ("of §6.2" vs the link form), rejected because the whole bullet is replaced and the quote is not marked verbatim.
FACT: the UNVERIFIED "§15.4.2 DRAINING signal = §29.4 step 13 CH-RUNTIMEOPS terminate frame" holds in the tree: Shutdown calls drainViaLifecycle, which sends Lifecycle.Terminate (the `terminate` frame) and whose doc comment calls it "the §15.4.2 DRAINING-state graceful-shutdown signal", gated on !boundRemains. The open entry can close. EVIDENCE: pkg/adapter/session.go:246-261, :285-297
FACT: code citations in SPEC-3's ten-second commentary all resolve: holdstate.go:201 pass-2 `context.WithTimeout(context.Background(), 10*time.Second)`, commit 3997f502b dated 2026-08-22, spec/11:264 is §11.4 step 3 "wait up to 10s", session.go:219-224 is the deadline_ms doc paragraph, socketruntime.go:470-473 defaultSocketShutdownGrace=10s, mcpruntime.go:86 defaultMCPShutdownGrace=5s, ShutdownGrace set nowhere outside mcpruntime.go. EVIDENCE: as cited
USEFUL [archive WATCHOUT "staged at position 2 of the gateway-runtime-comms remediation plan" is NOT a false citation]: saved re-filing it; the remediation plan has only R-steps and waves, and the queue-position reading was settled twice.
USEFUL [archive WATCHOUT on the §28.4 ABSENT-row precedent]: still true (all coordination_generation field rows are UNWIRED; one ABSENT generation row exists); below the bar as commentary.

### [spec.3.review-client-surface.1]
DECISION: no client-surface findings in the staged spec edits — BECAUSE every client-visible change (the §15.1 `SETUP_COMMAND_FAILED` row's four replaced sentences, the §6.2 client-visibility pointer) matches the current row text verbatim, and the untouched sufficient-condition statements of the same mapping (spec/15 `/v1/sessions/start`, `/finalize`, `/start`, `/resume` rows; spec/07 "Pre-attached vs. post-attached failure visibility") stay true because none claims setup-command exit is the only cause — ALTERNATIVES: filing spec/07's "Pre-attached vs. post-attached" enumeration as an unstaged site, rejected because it is not exhaustive and nothing in it becomes false.
FACT: the gateway selects `SETUP_COMMAND_FAILED` only through `writeSetupCommandError` on a `podsession.SetupCommandFailure`, which is built only around the RunSetup stage, and its 503 fallback also carries `details.reason: setup_command_failed`. EVIDENCE: pkg/gateway/sessionserver/start.go:236-249; pkg/gateway/podlifecycle/podsession/binder.go:938, slotbinder.go:304
FACT: the CH-RUNTIMEOPS `terminate` frame carries no session field (`type`, `deadlineMs`, `reason`), so it is pod-global, which is consistent with the staged §4.7 `Shutdown` row's "signal is pod-global and names no session" and the staged §29.4 step-13 sentence. EVIDENCE: spec/28_communication-channels.md:1082
FACT: `PrepareWorkspaceRequest` carries no `mid_session` today; SCHEMA-1 adds it as field 6, which the staged §4.7.1 carriage table relies on. EVIDENCE: schemas/lenny-adapter.proto:682-696
DEFERRED [schemas/lenny-adapter.proto, `Error` message and `ErrorCode` enum comments]: "The Code field is the spec-mandated error code from spec §15.1's catalog" (:529-530) and "The catalog below mirrors spec §15.1" (:556) are already false for `PROTOCOL_VERSION_INCOMPATIBLE`, and the two SLOT_BIND codes add more codes with no §15.1 row. The fix is a schema comment edit, so it is outside the spec loop; below the bar as a pre-existing precedent the proposal cites.

### [spec.3.review-docs-alignment.1]
DECISION: returned no findings for the docs-alignment lens in spec round 3. BECAUSE every accepted or deferred edge case whose fix could land in staged spec text already resolves to landing text or is settled: the deleted envelope bullet's outcomes (setup-stage already-started refusal becomes SETUP_COMMAND_FAILED, setup-stage superseded refusal becomes the retryable fallback, any other stage takes that stage's envelope) all sit in the staged §15.1 `SETUP_COMMAND_FAILED` cause and exclusion sentences plus the §4.7.1 paragraph after rule 9. The gateway-crash stuck entry resolves through the §5.2 table row "Pre-`running` slot no cleanup reclaims" plus the paragraph after the table ("Pod termination ends all of it") and rule 5. The emptied-workspace outcome is proposal-only by decision 39. ALTERNATIVES: re-filing the rolled-back-start successor outcome (Open, FILED), rejected because four earlier lenses declined it. Filing the mid-session upload's client-visible error when it meets the hold, rejected because the session is already being torn down, so the case is below the bar.
FACT: after the round-1 deletions the spec Edge-cases section no longer names the stage-to-envelope outcome, but summary open decision 29 does, and landing text (§15.1 row, §4.7.1 after rule 9) states it, so this is not an "accepted mode missing from Edge cases" finding. EVIDENCE: spec-changes.md SPEC-5 §15.1 block (exclusion-sentence replacement); §4.7.1 block paragraph after rule 9
WATCHOUT: the only live docs-alignment gap on this surface is docs-lane: `docs/operator-guide/troubleshooting.md` "Setup command failures" row remedies (archived DEFERRED). Do not file it in the spec loop. EVIDENCE: review-log-archive.md DEFERRED on troubleshooting.md:41

### [spec.3.review-edit-sites.1]
DECISION: no findings — BECAUSE every "reads, verbatim" anchor in the spec staging still matches the tree exactly once (§4.1 third sentence, §4.7 Shutdown/DemoteSDK/ReportSessionScrub rows, §29.4 step 13 tail, §7.2 preamble/step 2/step 3, §7.3 item 4, §6.2 cancel clause and Client visibility clause, §4.7.9 step 5, §5.2 action list/scrub-model opening/reporting/leaked-outcome/whole-pod parenthetical, §12.6 prose and DDL, §4.6.1 opening phrase, §15.1 SETUP_COMMAND_FAILED four sentences), and no spec surface grepped for the changed concepts (ReportSessionScrub, sessions_served, session release, cleanup timeout, receiving_uploads/slot_cleanup, claim deletion, half-claimed replacement pod, setup_command_failed, DemoteSDK, graceful-shutdown signal) is left false and unstaged — ALTERNATIVES: filing §6.2's projection-input enumeration and §4.6.3's "projection of SandboxClaim state" cell, rejected as recorded recurring non-findings; filing §5.2's "Fresh workspace guarantee ... even if the failed slot's cleanup has not yet completed", rejected because the hold only makes it vacuously true.
FACT: the round-1 spec diff (three deletions) touched no anchor any other staged edit locates by quoted text. EVIDENCE: diff -ru against scratchpad/cp-snap/0081-opt8/spec-r1-prefix
FACT: §15.1 endpoint rows for /sessions/start, /start, /resume (spec/15_external-api-surface.md:625,647,650,720) state setup-command exit as one producer of SETUP_COMMAND_FAILED, not the only one, so SPEC-5's second producer leaves them true.
FACT: docs/reference/state-machines.md:234-251 mirrors the §6.2 per-slot fence and is staged by DOCS-1, so it is not a spec-lane gap. EVIDENCE: non-spec-changes.md DOCS-1 heading

### [spec.3.review-fresh.1]
DECISION: no findings in spec round 3 — BECAUSE the only delta since spec-r1-prefix is the round-1 deletions (Edge-cases envelope bullet, §7.1 "sends no `Shutdown`" clause, SPEC-5 §15.1 preamble sentence), and every verbatim anchor the staged edits quote was re-grepped and found in spec/04, 05, 06, 07, 12, 15 and 29 as quoted — ALTERNATIVES: filing the reservation-release `leaked` ground (below), rejected as a shipped gateway-side producer the summary already records and the table's cleanup-scoped rows do not claim to exclude.
FACT: the §29.4 step-13 `terminate` frame IS the §15.4.2 DRAINING graceful-shutdown signal in the tree: `Server.Shutdown` calls `drainViaLifecycle` (documented as "the §15.4.2 DRAINING-state graceful-shutdown signal"), which sends `Lifecycle.Terminate`, gated on `!boundRemains`. This closes the Open "Is §15.4.2's `DRAINING` ... the same act as §29.4 step 13's CH-RUNTIMEOPS `terminate` frame?" as yes. EVIDENCE: pkg/adapter/session.go:250-260, :294-303
FACT: every verbatim anchor in the spec-changes file resolves in the current tree; the one quoted with a bare "§6.2" ("The one-session-only invariant of §6.2 is the ...") reads "[Section 6.2](...)" in spec/04:416, but it sits inside a whole-bullet replacement located by its opening phrase, so it does not affect application. EVIDENCE: spec/04_system-components.md:416
WATCHOUT: `applySlotRetryPolicy` enters `leaked` on a failed `ReleaseSlotReservation` (gateway-side Redis/claim rollback), a ground the §5.2 disposition table does not name; the table's rows are keyed on the pod-side cleanup, and the summary's `slot_assigned` defects entry records the producer. Refuted repeatedly in the archive; do not re-file without a new argument. EVIDENCE: pkg/gateway/sessionserver/start.go:2834-2848; summary.md:656-668

### [spec.3.review-kubernetes.1]
DECISION: no findings under the Kubernetes-idiom lens — BECAUSE the only staged spec edits touching the apiserver are SPEC-4's re-keying of the §4.6.1/§6.2 occupancy projection, which adds no writer, no status field, no finalizer and no reconcile on a request path; the round-2 diff is three deletions with no Kubernetes surface — ALTERNATIVES: filing §6.2:80's input enumeration against §4.6.1's added "phase the pod currently projects" input, rejected as a listed recurring non-finding (review log, recurring non-findings list naming "§6.2's projection-input enumeration becomes incomplete"); filing SPEC-3's §5.2 sentence "A pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection" against the between-two-reconciles coalescing window, rejected as the scoped-out Open on §4.6.1.
FACT: §4.6.1's staged "the controller's own last level, which it may read back because it is the sole writer of that field" holds against the tree: both the OccupancyReconciler and the Sandbox-to-Pod reconciler write Sandbox.status.phase under the single field manager lenny-warm-pool-controller, and §4.6.3 grants the gateway no sandboxes/status verb. EVIDENCE: pkg/controller/warmpool/occupancy.go OccupancyReconciler doc comment ("Both write Sandbox.status.phase under the single lenny-warm-pool-controller field manager"); spec/04_system-components.md §4.6.3 table row `Sandbox` `status.*` and the Gateway ServiceAccount RBAC paragraph.
USEFUL [Settled: "binder.go's only apiserver writes are the two podclaim.DeleteClaim calls"]: it bounds the Kubernetes surface to claim DELETE, so this lens only needed SPEC-4 checked.

### [spec.3.review-mechanism.1]
DECISION: no findings in spec round 3 — BECAUSE the spec-changes diff since the spec-r1 snapshot is only the three round-1 deletions, and the whole staging, traced end to end under the mechanism lens, left no predicate drift or unreachable trigger that clears the bar. The traces covered the §4.1 scrub clause, the §4.7 Shutdown and DemoteSDK rows, the §4.7.1 cascade and critical section, the §5.2 table and hold, the §6.2 fence and projection re-key, §7.1/§7.2/§7.3, the §15.1 row, the §15.4 blocks and SPEC-6 — ALTERNATIVES: filing the new §15.1 cause sentence's either/or as non-exhaustive (RunSetup also answers FailedPrecondition on "adapter is not configured with a workspace root", pkg/adapter/staging.go:344-350). Rejected because the shipped sentence omitted that producer too, and it is a misconfiguration no rendered pod reaches.
FACT: the §15.4.2 DRAINING graceful-shutdown signal IS the §29.4 step 13 CH-RUNTIMEOPS `terminate` frame. The adapter's Shutdown sends it through drainViaLifecycle, which calls RuntimeOps.Terminate, and that writes a `terminate` frame carrying deadlineMs and reason. It is gated on `!boundRemains`, so the staged §29.4 sentence tying step 13 to the §4.7 row's signal condition is correct. This closes the Open "Is §15.4.2's `DRAINING` ... the same act as §29.4 step 13's ... `terminate` frame?". EVIDENCE: pkg/adapter/session.go:259-260, :299-305; pkg/adapter/runtimeops.go:486-491
FACT: every verbatim anchor the spec staging quotes still exists in the tree. The anchors checked were §4.6.1 at spec/04:409, :415 and :416, the §6.2 projection prose and fences at spec/06:80, :95 and :148-155, the §6.2 client-visibility clause at spec/06:290, §7.2 at spec/07:210-214, §7.3 at spec/07:414, §4.7.9 step 5 at spec/04:854, §12.6 at spec/12:481 and :494, §15.1 SETUP_COMMAND_FAILED at spec/15:1136, the §15.4 SDK-warm contract at spec/15:1469, and the §4.7.1 tables ending before 4.7.2 at spec/04:695.
FACT: the SPEC-3 ten-second commentary citations resolve. They are spec/11:264, pkg/adapter/session.go:219-224, socketruntime.go:470-473, mcpruntime.go:86 (5s) and holdstate.go:201. EVIDENCE: as cited.
WATCHOUT: §10.1's Hold state timeout bullet says the adapter "sends `terminate` on the CH-RUNTIMEOPS", but terminateHeldSession sends no drain signal. This mismatch predates the proposal and the staging does not touch it, so do not file it against 0081. EVIDENCE: spec/10:58; pkg/adapter/holdstate.go:211-216

### [spec.3.review-performance.1]
DECISION: no findings — BECAUSE the round-2 spec diff is two deletions (the §7.1 "the gateway sends no `Shutdown`" clause and the §15.1 preamble "any other bind-sequence request" sentence), neither of which touches a write path, and the staging otherwise adds no etcd, Postgres or Redis write — ALTERNATIVES: filing the Redis-reset loss of a leaked slot's occupancy, rejected as pre-existing (rehydration and the Postgres fallback count `sessions WHERE state = 'active'` for every leaked slot, including running ones) and no worse than shipped, which DECRed a failed pre-`running` bind's reservation outright.
FACT: `lenny:pod:{pod_id}:active_slots` rehydration after a Redis restart and the §12.4 Postgres fallback both read `SessionStore.GetActiveSlotsByPod`, so no store records `leaked` occupancy durably across a Redis reset. The gap predates 0081. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:551, spec/12_storage-architecture.md:219, spec/06_warm-pod-model.md:160
FACT: SPEC-6's superseded counter carries `pool` x `k8s_pod_name` x `cause`(2), the same cardinality class as the shipped `lenny_slot_failure_total` (`error_type`, `pool`, `k8s_pod_name`). EVIDENCE: spec/16_observability.md:14
FACT: shipped `PrepareWorkspace` goes straight to `stream.Recv()` with no blocking act before the first frame, so the late-insert race in the Traps entry needs the handler goroutine unscheduled for a whole `Shutdown` round trip. That is rare, and earlier rounds did not file it. EVIDENCE: pkg/adapter/staging.go:56-85
USEFUL [Settled: "The staging adds NO per-session or per-request write to etcd, Postgres or Redis"]: rechecked against SPEC-3 §12.6 (the `sessions_served` trigger moves onto the report, so fewer writes happen) and SPEC-6. Still true.
OPEN: spec/10 §10.1 has pods validate `coordination_generation` on every gateway-to-pod RPC, so under the spec a stale coordinator's compensating `Shutdown` after a handoff is rejected and books the slot `leaked`. This is split-brain-only and below the bar. Nobody has traced it against the §7.1 obligation. EVIDENCE: spec/10_gateway-internals.md:30

### [spec.3.review-reliability.1]
DECISION: returned no findings for the reliability lens in spec round 3 of run 0081-opt8 — BECAUSE the only spec-changes diff since the round-1 snapshot is the round-1 deletions (the Edge-cases envelope bullet, "the gateway sends no `Shutdown`" in SPEC-2's §7.1 paragraph, and the SPEC-5 §15.1 preamble sentence), none of which touches a recovery path; every recovery path re-traced (gateway crash between abandon and reclaim, coordinator handoff mid-bind, an unanswered reclaim, the life-of-the-pod hold on a failed act, the ten-second hold-timeout close window, and the mid-resume cancel reclaim ordered before §7.2 step 4) is bounded by the §5.2 disposition table or recorded in Edge cases as an accepted failure mode with named out-of-scope recoveries — ALTERNATIVES: re-filing §7.2 step 3's unconditional "connection that attempt still holds" (rejected: refuted repeatedly, archive `[spec.1.review-reliability]` decision); filing the reservation release on the caller's cancelled ctx (rejected: the Open entry's remedy is code, owned by the non-spec loop).
FACT: `Shutdown` carries `coordination_generation` but the adapter validates it only in `CoordinatorFence` and `CheckpointBarrier`, so a compensation sent by a coordinator that lost a handoff is still admitted and the lost-compensation residue is limited to process death. EVIDENCE: schemas/lenny-adapter.proto:1630-1634; review-log-archive.md:310
USEFUL [archive :310 "Shutdown is unfenced" FACT]: it closed the handoff question without a fresh trace.

### [spec.3.review-security.1]
DECISION: no security findings in spec round 3 — BECAUSE the round-1 diff is three deletions (Edge-cases envelope bullet, §7.1 "sends no `Shutdown`" clause, §15.1 preamble sentence), none of which touches a control, and the staged SPEC-4 re-key of §4.6.1 is stricter than the shipped text (a claim deleted while the pod projects `claimed` drains on either recycle setting; `idle` only from `reserved`, which only a recycled-and-scrubbed pod reaches), so one-session-only reuse is not weakened — ALTERNATIVES: filing the report-only-on-`running` rule as under-counting `sessions_served`, rejected as a settled design choice that is at most less strict (Design "The cleanup-outcome report follows the `running` boundary"; shipped `Shutdown` reports only for a bound entry, pkg/adapter/session.go:238-279).
FACT: SPEC-4's new §4.6.1 input "the phase the pod currently projects" is a read-back by the WarmPoolController of a field it alone writes (spec/04_system-components.md:409 "the gateway does not write `Sandbox.status`"), so it stays inside the §4.6.3 ownership split. EVIDENCE: spec/04_system-components.md:409, :634
USEFUL [Traps "Security and capacity families are exhausted"]: every in-pod-attacker variant still collapses into the Deferred TLS-flags entry; nothing in the round-1 diff reopens it.

### [spec.3.review-single-source.1]
DECISION: no findings in round 3. BECAUSE the round-2 diff contains only the Edge-cases envelope bullet deletion, the §7.1 "sends no `Shutdown`" cut and the §15.1 preamble sentence cut, and a re-inventory of the staged rules finds one stating site each: the reporting rule (the §5.2 scrub-model opening), dispositions (the §5.2 table), atomicity (the §4.7.1 critical-section paragraph), the teardown preconditions and the graceful-signal condition (the §4.7 `Shutdown` row), the hold's status (the §15.4 hold block), and the outcome meanings (rule 15). Every other site either cites the home or gives it in a clause. ALTERNATIVES: filing the reclaim-before-pod-release ordering that appears in both the §7.2 step 3 and the §7.3 appended sentence; rejected because each is one clause scoped to a different trigger. Filing the SPEC-6 superseded row's gloss "so the reclaim released nothing and the slot is not leaked"; rejected because it is a one-clause summary, and the spec states no reservation-release leak that it could contradict.
WATCHOUT: the §5.2 paragraph after the table says a single-session pod "retires under the §6.2 occupancy projection", but SPEC-4 reduces §6.2's claim-deletion clauses to a pointer at §4.6.1. The citation resolves in two hops (§6.2 to §4.6.1). It is not a copy, so do not file it as one. EVIDENCE: spec-changes.md:594, :826
USEFUL [spec.2.review-single-source.1]: its settlement of the stage-to-envelope homes (the §4.7.1 paragraph after rule 9 and the §15.1 exclusion tail) still holds, and nothing in the proposal changed it since.

### [index-reconcile.7]
DECISION: rebuilt the checklist's SPEC-lane block against SPEC-1 to SPEC-6 as pointer lines, keeping the step ids S1 to S6 and the application order SPEC-5, SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-6, and added `pkg/gateway/sessionserver/start.go` to CODE-13's deliverable-index row, because CODE-13's call-site table stages the `slotBinder` interface, `applySlotRetryPolicy` and `rollbackClaim` edits there. Added S6 to the Depends-on of S16 and S19, because CODE-1 increments and CODE-13 feeds the counters whose §16.1 rows S6 stages. No other Depends-on names a missing or wrong spec step.
CORRECTS [DEFERRED summary.md, the 0078 impact row]: the row now says the `ShutdownRequest{` literal sweep that CODE-1's two-field precondition forces adds `UnconditionalTeardown: true` to the `shutdown_drain_gate_race_test.go` literals, in place of the false "CODE-4 sets `unconditional_teardown` at every non-compensating `Shutdown` caller, so".
CORRECTS [DEFERRED implementation-checklist.md, the S23 line]: the false renumbering reason is removed, and S23 is listed directly after S7 with the ground DOCS-4's own block gives (it lands beside DOCS-2, after SPEC-3). The step id is unchanged.
CORRECTS [Open "Does SPEC-6's `cause` label (`refusal`, `failure`) break §16.1.1"]: carried into the summary's open decisions as decision 54, with the ground this log gives and no recommendation.
OPEN: DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go]: the pre-`Runtime.Start` failure branches release by session identifier alone. The remedy is a code change for a later proposal; the summary's unstaged-defects section carries it.
OPEN: DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline lets the adapter apply its default grace period" is false. The repair is a staged Go comment edit under CODE-7, which no non-spec lens has written.
OPEN: DEFERRED [spec-changes.md, SPEC-3 untokened-entry commentary]: the commentary calls the §10.1 hold-timeout termination "a request" where the §5.2 hold paragraph says it runs under no request. The repair lands in spec-changes.md.
OPEN: DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go, `Sandbox.status.phase` doc comment]: the comment lacks the projection input SPEC-4 adds. The repair is a Go comment edit plus `make generate`, staged in non-spec-changes.md.
OPEN: DEFERRED [docs/api/internal.md]: the gRPC status table omits `ABORTED`. Pre-existing; a separate finding.
OPEN: DEFERRED [schemas/lenny-adapter.proto, the LEAKED comment's drain-ledger sentence]: it may restate a rule whose home is §4.6.3 or §5.2. A reduction would be a staged SCHEMA-1 edit; a separate finding.
OPEN: DEFERRED [pkg/controller/sandbox/podspec and cmd/lenny-adapter/main.go]: the podspec renderer passes no TLS flags to `lenny-adapter`, so the adapter gRPC server runs without client authentication. Pre-existing; a separate security finding.
OPEN: DEFERRED [schemas/lenny-adapter.proto, `Error` message and `ErrorCode` enum comments] (`[spec.3.review-client-surface.1]`): the comments claim every code comes from §15.1's catalog, which is false for `PROTOCOL_VERSION_INCOMPATIBLE` and for both new SLOT_BIND codes. The repair is a staged SCHEMA-1 comment edit, which no non-spec lens has written.
