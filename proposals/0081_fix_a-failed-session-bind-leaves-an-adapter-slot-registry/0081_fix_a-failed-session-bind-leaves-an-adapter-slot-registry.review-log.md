# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass after spec rounds 2 to 4, the f13 decision phase, spec-recheck rounds 1 and 2, and non-spec
round 1 of run 0081-opt4, 2026-09-23). Read the whole ledger, from `[spec.2.fix-G1.1]` through
`[non-spec.1.review-test-coverage.1]`, and lifted its durable residue. Lifted: the envelope-sentence reduction after rule 9
(no owner list), the decision-53 re-cut into CODE-13, CODE-14 and CODE-15 and the re-pointed checklist, the resolutions of
open decisions 51, 52 and 53, the single reason homes set by spec-recheck round 1 and non-spec round 1 fixes, the S4
tier-11 sweep moving to the Testing `**For SPEC-3**` paragraph landed by S7, the tier-3 case list becoming a pointer at
CONF-1, SCHEMA-1's proto comments becoming rule pointers, and about thirty FACT and WATCHOUT lines. Rewritten under
`CORRECTS`: the `## Design` choice-record entry (the two-field rationale home is the summary's `unconditional_teardown`
bullet), the §4.7.1 envelope-home entry, the SPEC-5 §15.1 rationale entry, the S4 sweep entry, the CODE-8 residue entry,
the checklist map, and CODE-4/CODE-6 attributions that moved to CODE-13/CODE-14. Closed and moved to `### Settled`: Opens
51, 52 and 53, the process-group kill question, the `ErrorCode` header disagreement (older reading wins), the
`stageWorkspace` threading gap, the positive reclaim-hold release arm, the CONF-1 driver restatement, and `RunSetup`'s
workspace-root producer. Deleted: the tier-3 driver-restatement WATCHOUT (superseded by the pointer), the `ErrorCode` header
Deferred and the `## Spec files touched` gauge Deferred (both closed), and the closed checklist-S1 Deferred. Every f13 and
non-spec-round-1 DEFERRED in the ledger was closed by a later `CORRECTS` or fix and is not carried. Added to `### Open`:
the S4-to-S5 forward reliance, the §10.1.4 close-context ownership, the `WIRED` rows at S9, the tier-7a race step, the
`/resume` setup-command refusal (folded into the decision-29 Open), and seven reduction leftovers. Target: NOT reached. The
section is about 675 lines (up from 580), because `### Settled` holds about 295 one-line entries and every `### Traps` entry records a dead end; dropping either
loses a claim nothing else carries. The prior pass's history: the hard compaction before run 0081-opt3 archived the prior
1,438-line standing context in `0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md`
under `## Standing context archived 2026-09-23 before run 0081-opt3`.

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
column or a compensation suppression on typed refusals describes a withdrawn design. Since `[f13.human-decisions]`
the compensation, lease release and leaked disposition sit in CODE-13 (cut from CODE-4), the per-slot guard and the
expired-acquisition disposition in CODE-14 (cut from CODE-6), and the split gates in CODE-15 (cut from CODE-1); an entry
below that attributes those to CODE-4, CODE-6 or CODE-1 predates the re-cut. Line numbers into the proposal files are
not recorded here.

### Settled

- **DECISION: the disposition of every per-slot cleanup is ONE TABLE, in the staged §5.2 `**Scrub model.**` append.** Rows key on what is reclaimed, who performs it and which act fails; other sites cite it. Rejected: a §6.2 two-exit home.
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
- **The completion predicate differs by site, deliberately, and all three are stated.** `Shutdown` and `terminateHeldSession`: `guarded && closeErr == nil && treeErr == nil`; guard-acquiring `releaseSessionSlot`: `guarded && treeErr == nil`, because it closes no runtime (`CORRECTS` by `[redesign.8.fix.1]`).
- **DECISION (`[operator.47]`, option A): SPEC-3's reclaim-hold paragraph states that a workspace removal performed before every request still writing under the identifier has stopped writing is an act that did not return without error.** It names the ordering and never the guard. Rejected: (B) dropping `guarded`, which frees an identifier over a tree a parked `Resume` extraction is re-creating.
- **DECISION: CODE-14's (formerly CODE-6's) `**Disposition of an expired acquisition at a removing site.**` is the ONE home of what a removing site does when its guard acquisition expires.** It keeps the unguarded removal as shipped, routes the case onto the decision-47 sentence and the table's failed-act rows, and defines `guarded`; its site table carries the removal column only (`[non-spec.16.fix-G2.1]`). Rejected: releasing the hold.
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
- **DECISION (`[operator.post-r16]`, from `[non-spec.16.fix-G1.1]`): every failed bind attempt is compensated, typed refusals included,** at `materializeSlot` and at `Binder.Resume`'s failure branch, matching §7.1 with no carve-out. The compensation cannot reclaim a session the gateway treats as live. Rejected: restoring the suppression and carving refusals out of §7.1.
- **DECISION (`[operator.post-r16]`): `lenny_slot_compensation_superseded_total` keeps every compensation and gains a `cause` label, `refusal` or `failure`,** derived by `compensationCause` in CODE-13 from CODE-7's sentinels. Rejected: excluding refusal compensations from the series.
- **DECISION: the attempt-scoped credential release (CODE-13 since the re-cut) is extended to `Binder.Prepare`'s credential-assignment failure arm.** It releases through `CredentialAssigner.Release(leaseID string)`, which widens the interface (`[non-spec.13.fix-G1.1]`) with a grep-closed sweep row for the test fakes. Rejected: session-wide `ReleaseSession`.
- **DECISION (`[non-spec.16.fix-G3.1]`): the adapterclient bind-sequence method signatures have one home, CODE-4's `client.go` Targets bullet.** `AssignCredentials` and `RunSetup` take a trailing `bindAttempt`, `PrepareWorkspace` a trailing `bindAttempt, midSession`, `FinalizeWorkspace` a `bindAttempt` before its shipped `midSession`, and `ResumeParams` a `BindAttempt`.
- **The no-entry compensation case is grounded on a `stageWorkspace` FAILURE PATH, never on an upload-free plan.** `stageWorkspace` has five error returns before the `PrepareWorkspace` send, which is guarded by `len(uploads) > 0`.
- **`mid_session` is SHIPPED on `FinalizeWorkspaceRequest` (field 4) and ABSENT from `PrepareWorkspaceRequest`.** SCHEMA-1 adds it to `PrepareWorkspace` alone; no production code sets it today, and the proposal wires it first in `upload_to_session.go`.
- **SCHEMA-1's field numbers are fixed and re-verified.** `bind_attempt` on PrepareWorkspace 5, FinalizeWorkspace 6, RunSetup 5, AssignCredentials 4, Resume 16, Shutdown 7; `PrepareWorkspaceRequest.mid_session = 6`; `ShutdownResponse.slot_reclaim = 3`; `ErrorCode` 28 and 29. Do not re-derive.
- **DECISION (`[operator.20-41-45]`, decision 20): the two new `ErrorCode` values take 28 and 29.** The enum has run contiguously 1 to 27. Rejected: the 1000-1999 range, a scheme nobody defined.
- **Two adapter `ErrorCode` values are minted, 28 and 29, and they take NO §15.1 row.** §15.1 catalogs the client-facing `code`, and no adapter `ErrorCode` string reaches REST. The enum is nested, so the Go constants are `adapterv1.Error_ERROR_CODE_*`.
- **DECISION (`[spec.2.fix-G1.1]`, `CORRECTS` `[spec.1.fix-G2.1]`): the staged §4.7.1 paragraph after rule 9 names NO envelope owner.** It is the home of why neither adapter code takes a §15.1 row, and ends at "the bind stage that received the refusal." Rejected: one owner (round 1), two owners keyed on pool mode (round 2), a stage-keyed three-arm list, an unkeyed pointer list, and a §15.1 `SLOT_FAILED` row (decides open decision 29).
- **The stage-to-envelope rationale's home is the spec-changes Edge-cases bullet "A bind-sequence refusal reaches the client under the envelope its stage already selects."** The widened §15.1 `SETUP_COMMAND_FAILED` row owns the setup-command mapping.
- **A setup-command refusal on a CONCURRENT pool reaches 422 `SETUP_COMMAND_FAILED` (`FailedPrecondition`) or the 503 fallback (`Aborted`), never `SLOT_FAILED`.** `slotbinder.go` wraps `RunSetup` errors in `SetupCommandFailure`, and `start.go`'s `setupFail` case precedes `slotFailed` (`[spec.2.review-mechanism.1]`).
- **The concurrent-path `AssignCredentials` adapter error is NOT wrapped in `CredentialAssignmentError`** (only lease minting is), so §5.2's exhaustion envelope is right for non-setup stages. A resume refusal hits `writePodClaimError`'s default arm (`[non-spec.1.review-client-surface.1]`).
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
- **DECISION (`[spec-recheck.1.fix-G2.1]`, `CORRECTS` the `[prune.4.fix.1]` rationale-paragraph entry): the SPEC-5 §15.1 block's preamble is the ONE home of why the `SETUP_COMMAND_FAILED` row is widened.** The closing commentary after the exclusion-sentence fence is deleted; the untouched-list §15.1 bullet ends "The section itself is edited under SPEC-5's §15.1 block."; the stage-by-stage mapping's one home is the Edge-cases envelope bullet.
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
- **Two shipped tier-11 gates go red on SPEC-3's §12.6 edits, and they are the only two.** `TestPerReleaseSessionCountDrainAgrees_F5231` (delete its spec/12 block, remove the `12.1` spec-map row and the orphaned §12 credits) and `podStateGatewayWrittenSentence` (re-key; keep the trailing `ReportPodScrub` clause).
- **DECISION (`[non-spec.1.fix-G4.1]`, `CORRECTS` `[index-reconcile.6]`): the SPEC-3 tier-11 sweep's ONE home is the `**For SPEC-3**` paragraph in non-spec `### Documentation reconciliation tests, tier 11`, landed by S7.** S4 carries Tiers 0 and one deferral sentence. The spec-lane handler (`runSpecStep` in `implement-proposal-build.js`) edits only spec files and runs no tests, so a spec step cannot carry test work.
- **DECISION (`[non-spec.1.fix-G4.1]`): `## Spec files touched` reduces its spec/16 bullet to "(the rows SPEC-6 stages)" and its closing docs paragraph to a pointer at DOCS-1 to DOCS-4, CODE-9 and SCHEMA-1.** S7, S23 and the summary DOCS index lines are pointers; site lists live only in the DOCS blocks and the tier-11 Testing block.
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
- **DECISION (`[f13.human-decisions]`, decision 53): CODE-1, CODE-4 and CODE-6 are RE-CUT so each deliverable appears in one checklist step.** CODE-1 keeps the precondition and cascade; CODE-15 takes the `if bound` gate, `Shutdown`'s doc comment, the `SessionScrubReporter` comment, `deregisterSlotLocked`'s doc comment and `runtimeHoldsLocked` ("The split gates"). CODE-4 keeps mint, carry and `unconditional_teardown`; CODE-13 takes the compensation, lease release and leaked disposition. CODE-6 keeps the hold; CODE-14 takes the per-slot guard and the expired-acquisition disposition.
- **CODE-6 still owns the per-member ten-second close context in `terminateHeldSession`,** and CODE-1 still owns `answerShutdown` and the scrub start; only the guard moved to CODE-14 (`[spec-recheck.1.review-applicability.1]`).
- **The 0082 impact row (`[f13.other-proposals]`, corrected by `[operator.f13-repair]`):** no 0082 deliverable loses its subject; CODE-13's attempt-scoped lease release precedes 0082 CODE-3's `revokeFinalizeLease`, so each lease is released once in either order. The action asks a textual rebase and, if 0082 lands, a trigger or guard statement for CODE-8's `Binder.Prepare` arm.
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
- **Checklist coverage map (`[operator.f13-repair]`):** S1=SPEC-5, S2=SPEC-1, S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=SPEC-6, S7=DOCS-1/2/3 plus the SPEC-3 tier-11 sweep, S8=CODE-3, S9=SCHEMA-1, S10=CODE-9, S11=CODE-7, S12=CODE-8, S13=CODE-4, S14=CODE-6, S15=CODE-14, S16=CODE-1, S17=CODE-15, S18=CODE-2, S19=CODE-13, S20=CODE-5, S22=CONF-1, S23=DOCS-4, S24-S26=CODE-10..12. S21 is deleted and its id retired; S16 lands the Testing subsection "Tier-1 adoption-ordering tests, every case walked".
- **DECISION (`[index-reconcile.4]`): each code step's `Depends on:` names the spec steps staging what it implements,** and S22 names S18. The deliverable index lists SPEC-1..6, CODE-1..15, CONF-1, SCHEMA-1 and DOCS-1..4 once each; CODE-11's line names its three files (`errorclassify.go`, `start.go`, `resume_setup_demotion_internal_test.go`).
- **DECISION (`[non-spec.1.fix-G5.1]`): checklist steps S10 and S12 to S18 are POINTERS,** "<deliverable> in full" or "<deliverable> except <part>, which lands with <step>", plus only the cross-step splits and sweeps the checklist owns. S15's restatement had drifted from CODE-14's derivation table.
- **DECISION (`[non-spec.1.fix-G5.1]`): S17 carries tier 9 and the registered-but-unbound class of the tier-9 matching-reclaim arm,** which needs CODE-15's widening of the tree-removal gate from `bound` to removed; S16 keeps tier 9 for the superseded and lease-release arms.
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
- **Every "reads, verbatim" anchor in SPEC-1 to SPEC-6 resolves exactly once in the tree (spec round 4), and no commit touched `spec/` since 911d93b13 or `pkg/`, `schemas/`, `docs/`, `cmd/` since c5d35bb05.** A re-sweep is owed only after a commit to those paths. The §29.4 step-13 tail has three hits and is disambiguated by the step.
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
- **An entry created by `PrepareWorkspace` has `sessionID == ""`,** so the shipped `bound` gate does not remove its tree; any case asserting a release ran on such an entry needs S17 (CODE-15).
- **The two gates reading SPEC-1/SPEC-3-replaced rows stay green:** `recycle_scrub_trigger_consistency_test.go` reads substrings in the unchanged remainder of the §4.7 `Shutdown` row, and `adapter_metric_catalog_test.go` checks registered-to-catalog only, so S6's rows landing before S10 break nothing.
- **The SPEC-6 `cause` predicate matches `compensationCause`.** The reclaim-hold and rule-8 refusals map to `failure`, which is consistent because a hold refusal's compensation answers `absent` and never reaches the superseded counter.

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
  same class (see `### Open`); a re-filing owes an argument that distinguishes it from that refutation.
- **WATCHOUT: CODE-4's `noteCompensationOutcome` rationale says "can" where its argument needs "cannot".** Below the
  bar; a repair must not read it as licence to make the forwarder package-level.
- **WATCHOUT: read CONF-1 before filing a tier-3 or tier-10 coverage gap.** The tier-3 block points at CONF-1's case
  list and abbreviates it. The checklist's per-step "Tiers:" lines are tiers to RUN, and a deliverable's "Tiers:" line
  names the tiers its own new gates land at; neither is a coverage claim.
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
  before this proposal (about nine earlier codes have no §15.1 row). Non-spec round 1 settled the disagreement with the
  `[index-reconcile.4]` Deferred in favour of this reading (see `### Settled`); do not re-file the header against SCHEMA-1.
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
- **Recurring non-findings, fourth list (spec rounds 2 to 4, spec-recheck 1, non-spec 1):** the §16.1.1 `k8s.pod.name`
  "Used on" cell (descriptive, no gate reads it); spec/28's credential-rotation fallback reached through §7.3; spec/15's
  `examples/runtimes/echo/` reference (the directory does not exist; pre-existing); §6.4's "on the first reference" (already
  loose); §4.6.1 naming no projection for a claim deleted in `sdk_connecting` (the tree leaves it to the reconciler too);
  §4.6.3's `SandboxClaim` row listing only "hold expiry" (pre-existing); `docs/api/internal.md`'s stylized
  `RuntimeAdapter` service; `tests/tier10_conformance/README.md` "Current state" (not exhaustive); S22's and S18's
  `Depends on:` omitting S17 (harmless in listed order).
- **MISTAKE, three rounds naming WHICH envelope a refusal reaches in the §4.7.1 paragraph after rule 9.** Round 1 named
  one owner; round 2 named two keyed on pool mode; both were false for some stage and pool-mode pair, because the real key
  is the bind stage. Do not re-add an owner list, a §5.2 citation, or any envelope sentence there. The stage-keyed lead
  clause alone is true; the Edge-cases bullet and the widened §15.1 row own the mapping.
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
- **WATCHOUT: S17 carries the tier-1 `exited_cleanly` injected-failure cases in pointer form** (Testing, "Adapter tests
  for CODE-1 and CODE-2", driving `Server.removeSlotTreeFn`), because those live in Testing and "CODE-15 in full" does not
  carry them. S14's admission-guard sentence stays a pointer at "The mid-session conditioning."; do not re-expand it.
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
- **WATCHOUT: the adapter gRPC server requires and verifies a client certificate whenever `--client-ca-file` is set**
  (`pkg/adapter/transport.go`, wired in `cmd/lenny-adapter/main.go`), and spec/04's contract names mTLS. Whether the
  podspec always renders the flag is the open part; `unconditional_teardown` adds no capability an unfenced `Shutdown`
  lacked.

### Open

- **Does rule 8's untokened arm compare equal across a replacement?** UNVERIFIED: two untokened entries under one identifier compare "" to "", so `noteRuntimeStarted`'s replaced-entry check would record. No gateway path producing it is known; start from whether a `StartSession` or `ConfigureWorkspace` can be an attempt's FIRST request on a pod.
- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** OPEN, summary open decision 29: the same refusal is non-retryable at one stage and retryable at another. `[non-spec.1.review-docs-alignment.1]` adds whether a `SLOT_BIND_ALREADY_STARTED` at a `/resume` setup-command request (422, "do not retry") agrees with CODE-5 holding the row in `awaiting_client_action`.
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** OPEN, summary open decision 36: a third-party harness cannot force a failed removal.
- **Neither newly recorded residue has any observability** OPEN, summary open decision 33.
- **Are "registry entry" and "bound entry" defined anywhere?** OPEN, summary open decision 32.
- **Churn from the third accounting caller** OPEN, summary open decision 34: at `maxConcurrentSessions: 2` a failed §7.3 re-attach drains a fresh pod.
- **Should SPEC-5 also correct `spec/06:290`?** OPEN, summary open decision 30. Pre-existing.
- **Should the tier-3 descriptor gate pin the `SlotReclaimOutcome` enum values?** OPEN, summary open decision 50 (`[index-reconcile.5]`).
- **Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?** OPEN, pre-existing: `slothealth.Tracker` and `slotstate.Registry` are replica-local and in-process, so a restart forgets leaked pods, and this proposal raises the leak rate.
- **Does the podspec always render the adapter's `--client-ca-file`?** UNVERIFIED, pre-existing [`[non-spec.1.review-security.1]`]: the server enforces mTLS only when the flag is set; if unset, an in-pod agent could send a co-tenant's unconditional `Shutdown`.
- **Tier-4 fixture extension** OPEN: whether the per-case interceptor perturbs the other `recycleAdapterDialer` call sites, and whether `recycle_scrub_path_test.go` can carry the concurrent-slot compensation case, is untraced.
- **Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?** OPEN, recorded in the summary's unstaged-defects rows: a CREATE, `bound` patch and DELETE inside one reconcile window leaves `("", false)` and an idle unscrubbed pod. Correctly scoped out; do not re-file.
- **Does the gateway double-book a leak for a runtime-given slot whose close fails?** UNVERIFIED: row 2 gives both a `leaked` report and an unclean response into one tracker with no per-session dedup. Check `MarkLeaked` idempotency.
- **Can a `PrepareWorkspace` frame on an already-admitted call write into a tree `removeSlotTree` is deleting?** UNVERIFIED: turns on whether the handler honours the cancelled server context.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** UNVERIFIED: one leaked slot retires the pod there.
- **Does `s.reclaiming` grow without bound over a long-lived recycling pod?** UNVERIFIED, code lane: one entry per failed cleanup, held for the pod's life, and the adapter process survives pod reuse.
- **Does `docs/reference/adapter-contract.md` owe a mirror of the §4.7.1 numbered-rule block?** UNVERIFIED: DOCS-2 stages four edits only.
- **Would any gate newly fail because of text the staging ADDS, rather than text it replaces?** UNVERIFIED: gate breakage was checked only for replaced lines.
- **Can a recycle-carrying `Shutdown` answering `absent` start the whole-pod scrub while a DIFFERENT slot's cleanup is still inside `removeSlotTree`?** UNVERIFIED: the scrub now runs inside `answerShutdown` and takes no slot guard. Outside the arm decision 35 describes. `[non-spec.1.review-reliability.1]` judges it pre-existing (see `### Traps`).
- **Does §5.2's "still in flight" mean "not yet admitted"?** UNVERIFIED: CODE-6 says an upload already past its resolve is not being admitted. Pre-existing.
- **Does the ten-second provenance paragraph belong in SPEC-3's commentary at all?** OPEN: decision 40 appended sentences to it; the next prune decides.
- **Does rule 8's take-back, stated outside the critical section, let a lagging attempt close a successor's session?** OPEN, below the bar: every reachable ordering has the reclaiming `Shutdown` tear the runtime down first.
- **Do `scrubreporter_seams.go:389-391` and `scrubreporter_seams_test.go:726-727` fall inside CODE-10's predicate?** UNVERIFIED: the application-time grep decides.
- **Is `pkg/adapter/sessionscrub_emit_test.go:16-19` stale against its own terminate case?** UNVERIFIED, pre-existing.
- **Does an implementor's reflow of the podspec and gatewaylink comments shift two `tests/claim-map.json` line surfaces?** UNVERIFIED: the implementing step re-checks both rows.
- **What retires a continuously occupied `maxConcurrentSessions > 1`, `recycle.enabled: false` pod?** OPEN, FILED: nothing does, which makes the `slotGuards` never-pruned bound claim false there.
- **Does the Testing section owe a disposition for DOCS-2's `DemoteSDK` row?** OPEN, FILED: the DOCS-2 block dispositions three of four edits and no gate reads the row.
- **Does CODE-10's line-oriented grep miss the wrapped carrier at `tests/tier4_integration/recycle_scrub_path_test.go:1081-1083`?** UNVERIFIED [`[non-spec.9.review-edit-sites.1]`]: "Each" ends one comment line and "release reports the per-slot cleanup outcome" starts the next, and the grep returned zero hits there after round 9's added alternation. CODE-12's joined-comment form is the known remedy shape. `[non-spec.1.review-citations.1]` adds that in context it describes the test's three clean running-slot releases, which still report under SPEC-3, so it may not be a carrier.
- **Does CODE-9's heading owe `pkg/gateway/podlifecycle/podsession/slotfailure.go`?** UNVERIFIED [`[non-spec.10.review-edit-sites.1]`]: the files-touched list places `slotFailureWorkspaceFinalize` there; judged bookkeeping. staticcheck's `unused` marks a const BLOCK used when one member is, so the constant is safe only inside the used group at `binder.go:288-298`.
- **Does the §5.2 retirement clause rely on S5 while S4 lands first?** UNVERIFIED [`[spec-recheck.1.review-applicability.1]`]: between S4 and S5, "A pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection" is false against the unedited §6.2 and §4.6.1 for a recycling pod, and S4's line does not record the forward reliance as S1 and S3 do. A checklist remedy.
- **Do SCHEMA-1's two `WIRED` claim-register rows landing at S9 contradict the `ABSENT` row's deferral to S22?** OPEN, filed by `[non-spec.1.review-applicability.1]` and `[non-spec.1.review-fresh.1]`, no fix entry: the named surfaces (`newBindAttempt` S13, the stamp S14, `Shutdown`'s comparison S16, `compensateFailedSlotBind` S19) do not exist at S9. No tier-0 gate checks surface existence.
- **Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's `live`?** UNVERIFIED [`[non-spec.1.review-fresh.1]`]: S18 has no S17 dependency and the Testing section does not say.
- **Does the non-spec Design text still restate the two-field `unconditional_teardown` argument?** OPEN [`[spec-recheck.1.fix-G1.1]`, `[spec-recheck.1.fix-design-G1.1]`]: filed for the non-spec lane; `[non-spec.1.fix-G1.1]` then made the summary bullet the home, so a check that no copy survives is what remains.
- **Should the SPEC-5 §15.1 preamble's "A refusal arriving at any other bind-sequence request reaches the client under the envelope that stage already selects." be cut?** OPEN [`[spec-recheck.1.fix-G2.1]`, `[spec-recheck.2.review-single-source.1]`]: it restates the Edge-cases bullet; judged a close variant of the round-1 fix, left for a later prune.
- **Does the summary's R1b impact row still restate the S-2 proto-window reasoning?** RESOLVED [`[non-spec.2.fix-design-G4.1]`]: the row is cut to a pointer at the summary's proto-window decision.
- **Does `**What the change costs.**` duplicate SCHEMA-1's field table in its counts?** OPEN [`[non-spec.1.fix-design-G1.1]`].
- **Is CODE-4's mint-site sentence "Its requests carry an empty `bind_attempt`" gone?** OPEN [`[non-spec.1.fix-design-G2.1]`]: it is wrong for `Binder.Launch`, whose RPCs carry no such field; the G2 reduction reportedly drops it as a side effect.
- **Does checklist S19 still restate CODE-13's mechanism?** OPEN [`[non-spec.1.fix-design-G5.1]`]: the attempt-scoped lease-release reasoning; outside G5's finding.
- **Is the fresh-connection proposition stated more than once?** OPEN [`[non-spec.1.review-single-source.1]`]: the staged spec states it in §4.7.1's carriage lead-in and the §7.1 reclaim paragraph, with copies in the summary, non-spec Design and the `compensateFailedSlotBind` doc comment; `[non-spec.1.fix-G1.1]` names staged §7.1 the home.

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

### [non-spec.2.fix-G1.1]
DECISION: CODE-14 is the single owner of the §10.1.4 per-member close-context split and of the `guarded` conjunct on `terminateHeldSession`'s hold release; CODE-6 (S14) closes on the pass `ctx` and releases on `closeErr == nil && treeErr == nil` — BECAUSE the guard acquisition, its deadline and `guarded` all land with CODE-14 at S15, so CODE-6's copy could not compile at S14 — ALTERNATIVES: pulling the split and guard into CODE-6 (re-cuts the settled decision-53 deliverables); a new "Adapter tests for CODE-14" section (wider than needed, the CODE-6 test section already labels CODE-14 cases inline).
FACT: the one description of the split is the files-touched `pkg/adapter/holdstate.go` entry, which now names CODE-14; CODE-14's expired-acquisition table row `terminateHeldSession ... on the member's own close context` is its other home — EVIDENCE: non-spec-changes.md files-touched holdstate.go entry.
WATCHOUT: CODE-6's `releaseSessionSlot` sentence still names the guard-acquiring form and `guarded && treeErr == nil`, which also land with CODE-14 at S15; same defect class, left as a separate finding by the design — EVIDENCE: non-spec-changes.md `### CODE-6`, the releaseSessionSlot hold-release sentence.
FACT: review-log Settled line "`Shutdown` and `terminateHeldSession`: `guarded && closeErr == nil && treeErr == nil`" states the end-state predicate after S15 and stays true.

### [non-spec.2.fix-G2.1]
DECISION: CODE-13's tier-1 compensation cases were relabelled in place. The Testing heading now reads "Gateway tests for CODE-4, CODE-5, CODE-7, CODE-8, CODE-9 and CODE-13, tier 1", and the case group reads "CODE-4, CODE-5, CODE-8, CODE-9 and CODE-13's cases" with its `// spec:` annotation unchanged — BECAUSE the Testing labels are the only map from checklist steps to tests, and S19 lands the CODE-13 symbols the compensation cases drive — ALTERNATIVES: a split-out CODE-13 group was rejected because its bullets interleave with CODE-8, CODE-9 and CODE-13; naming the test block in S19's step line was rejected because checklist steps are pointers.
WATCHOUT: a case in this group belongs to the step that lands the symbols it drives. When a deliverable is re-cut again, update the group label along with the checklist. — EVIDENCE: non-spec-changes.md, heading "Gateway tests for CODE-4, CODE-5, CODE-7, CODE-8, CODE-9 and CODE-13, tier 1"

### [non-spec.2.fix-G3.1]
DECISION: S9, S20 and S22 reduced to pointers ("SCHEMA-1 in full, except its `ABSENT` claim-register row, which lands with S22"; "CODE-5 in full."; "CONF-1 in full, with the `ABSENT` claim-register row SCHEMA-1 stages for it.") — BECAUSE SCHEMA-1's **Claim register.** paragraph already places the ABSENT row at S22 and owns the field table, gate move and WIRED rows; CONF-1 owns its cut, tiers, precedents and withdrawal reason — ALTERNATIVES: reviewer's longer pointers re-listed SCHEMA-1/CONF-1 content; a bare "SCHEMA-1 in full" at S9 would land the ABSENT row twice (duplicate EXPLICIT claim turns tier 0 red).
WATCHOUT: S9 and S22 must keep naming the ABSENT row's placement; it is the one cross-step fact the deliverables cannot express in their own step. EVIDENCE: non-spec-changes.md SCHEMA-1 **Claim register.**
FACT: G1 and G2 did not reduce S19; the review-log OPEN entry was narrowed to S19 rather than deleted.

### [non-spec.2.fix-G4.1]
DECISION: the summary Decisions bullet "`schemas/lenny-adapter.proto` is opened once" is the single home of the S-2 window reasoning and step-ordering precondition; SCHEMA-1's opening paragraph is the single home of the additivity facts (buf breaking); the R1b impact row is a pointer keeping only "no identifier R1b renamed is touched" — BECAUSE checklist S9 and non-spec "What the change costs" already cite "the summary's proto-window decision" — ALTERNATIVES: moving the home into SCHEMA-1 (breaks two pointers).
WATCHOUT: the R1b row's former sentence about proposal 0076's comment-only reopen of the proto argued 0081 still falls within S-2's second window; it is reviewer-facing and was dropped from the row, not lost evidence — the problem statement's S-2 paragraph still records the verification. Do not restore it to the row.

### [non-spec.2.fix-G5.1]
DECISION: the two mandatory-field literal sweeps (ShutdownRequest, adapterv1 bind-field) have one home, their `Every other file that grep ... names` bullets under `## Files touched on application (non-spec)`; the Testing "Scope accounting" paragraph points there and keeps only the silent-failure reason, the tiers per sweep, and the adapterclient.Client signature-change note — BECAUSE checklist S14/S16 already point at the files-touched list — ALTERNATIVES: Testing as home (rejected: S14 points at files-touched; an edit set is not a case list).
FACT: the adapterv1. qualifier trap and the files_updated_test.go mid-session exception now live only in the bind-field files-touched bullet — EVIDENCE: pkg/adapter/files_updated_test.go:52,80 (MidSession: true); tokensv1.AssignCredentialsRequest{ in pkg/tokenservice/grpc_test.go, podsession.ResumeRequest{ in pkg/gateway/podlifecycle/podsession/*_test.go.
WATCHOUT: do not re-add the "before s.mu.Lock()" clause to Testing; the Design paragraph **The two-field precondition, checked before the lock is taken.** is its home.

### [non-spec.2.fix-design-G1.1]
DECISION: CODE-14 owns the §10.1.4 per-member close-context split; CODE-6's terminateHeldSession bullet keeps `Runtime.Close(ctx, ...)` and releases on `closeErr == nil && treeErr == nil`, with one clause saying CODE-14 adds `guarded` — BECAUSE the guard acquisition, the pass's acquisition deadline and `guarded` all come from CODE-14 (S15), after S14 lands CODE-6 — ALTERNATIVES: move CODE-14 to land before CODE-6 (rejected: the re-cut is settled); rename "### Adapter tests for CODE-6, tier 1" to include CODE-14 (rejected here: wider than the finding; the section's other CODE-14 cases already label themselves "pins CODE-14's", so relabelling the one case matches).
WATCHOUT: "### Adapter tests for CODE-6, tier 1" already holds several CODE-14 cases (expired acquisition, uncontended cancelled acquisition, admission-RPC expiry), labelled inline rather than by heading. S15's checklist line counts them as CODE-14's. A case without an inline CODE-14 label reads as S14 work. — EVIDENCE: non-spec-changes.md ~2993, ~3008, implementation-checklist.md S15
OPEN: CODE-6's releaseSessionSlot sentence (non-spec-changes.md ~1413-1420) also depends on S15: it names "its guard-acquiring form" and `guarded && treeErr == nil`, and the guard split into releaseSessionSlot/releaseSessionSlotUnderGuard is CODE-14's. This is the same step-ownership defect outside this group's finding. A later round should file it.

### [non-spec.2.fix-design-G2.1]
DECISION: close "CODE-13's tier-1 cases are filed under CODE-4 and CODE-5" by relabelling in place: the heading becomes `### Gateway tests for CODE-4, CODE-5, CODE-7, CODE-8, CODE-9 and CODE-13, tier 1` and the group label at the `CODE-4 and CODE-5's cases, each // spec: §7.1 ...` line becomes `CODE-4, CODE-5, CODE-8, CODE-9 and CODE-13's cases, each ...`. BECAUSE each case names the symbols it drives, so a step writes the cases whose code it lands once the label stops excluding CODE-13; the checklist stays a pointer. ALTERNATIVES: splitting out a separate `CODE-13's cases` group (rejected: it means sorting about 15 interleaved bullets by owning deliverable, and bullets such as the counters, accounting and resume path span CODE-8, CODE-9 and CODE-13, so a mis-sort is likely to become next round's finding); naming the test block in S19's step line (rejected: checklist steps are pointers, and the preamble already defers to Testing).
FACT: the heading text `Gateway tests for CODE-4, CODE-5, CODE-7 and CODE-8` is cited by no live proposal file (checklist, summary, spec-changes). The only hits are in review-log-archive.md, which is history. Renaming it breaks no citer. EVIDENCE: grep over the proposal directory.
USEFUL [archive OPEN at review-log-archive.md:64382]: that entry already noted that the heading leaves out CODE-9. This rename closes it as well.

### [non-spec.2.fix-design-G3.1]
DECISION: S9, S20 and S22 reduce to pointers ("SCHEMA-1 in full, except its ABSENT row" / "CODE-5 in full" / "CONF-1 in full, with SCHEMA-1's ABSENT row") — BECAUSE SCHEMA-1 already states the same-commit regeneration, the claim rows and the closed-field-set gate move (non-spec-changes.md `**The tier-3 closed-field-set gate moves in the same commit.**`, `**Claim register.**`), and the files-touched `tests/spec-map.json` bullet states that each per-case entry lands in the step creating the file — ALTERNATIVES: the reviewer's longer S20/S22 pointers that enumerate spec-map and tier files; rejected because they list what the deliverable carries.
WATCHOUT: the ABSENT claim-register row is staged inside SCHEMA-1 but lands at S22, so S9 must except it and S22 must name it; "CONF-1 in full" alone would leave the row without a step. EVIDENCE: non-spec-changes.md `**Claim register.**` ("The `ABSENT` row lands with CONF-1's tier-10 file at S22").

### [non-spec.2.fix-design-G4.1]
DECISION: the summary Decisions bullet "`schemas/lenny-adapter.proto` is opened once" stays the home of the S-2 window reasoning (no third window, step-ordering precondition over session.go/credentials.go/slotcreds.go/sdkwarm.go); SCHEMA-1's opening paragraph is the home of the additivity facts (what is added, nothing removed or renumbered, buf breaking silent). The bullet's second sentence ("The edit adds fields and enum values only ... nothing to fire on.") is deleted; its "additively" in the first sentence stays. The R1b impact cell is cut to one sentence that keeps only the R1b-specific clause (no identifier R1b renamed is touched) and cites the decision. BECAUSE checklist S9 and non-spec "What the change costs" already cite "the summary's proto-window decision", so moving the home would break two pointers. ALTERNATIVES: making SCHEMA-1 the home of everything (breaks S9 and costs pointers); keeping the R1b row's 0076 comment-only reopen sentence (it defends why this is still the second window, which is reviewer-facing, not implementor-facing).
FACT: the R1b row's "no identifier R1b renamed is touched" appears nowhere else in the proposal; it is the one clause the R1b owner needs, so the cut keeps it. EVIDENCE: summary.md:1169
WATCHOUT: this closes the review-log Open "Does the summary's R1b impact row still restate the S-2 proto-window reasoning?" (review-log.md:651); the fixer should mark it resolved.

### [non-spec.2.fix-design-G5.1]
DECISION: the two files-touched "Every other file that grep ... names" bullets are the single home of both mandatory-field literal sweeps (set by grep, edit per literal, the `adapterv1.` qualifier trap, the files_updated_test.go mid-session exception). Testing "Scope accounting" keeps only what it alone owns: the silent-failure reason, the tiers each sweep reaches, and the adapterclient.Client signature-change note. BECAUSE checklist S14 already points at the files-touched list, so the home the implementor is sent to must carry the exception and the qualifier trap. ALTERNATIVES: making Testing the home (rejected: S14 points elsewhere, and a Testing list is for cases, not file edit sets); leaving both and copying the exception into files-touched (rejected: that makes the copies agree without deduping, which is the drift pattern).
WATCHOUT: the `adapterv1.` qualifier sentence and the files_updated_test.go exception are NEVER-CUT traps; they move into the BindAttempt files-touched bullet, they are not deleted. The before-the-lock ordering sentence in Testing is dropped because the Design's **The two-field precondition, checked before the lock is taken.** paragraph (non-spec-changes.md:101) is its home. EVIDENCE: non-spec-changes.md:3213-3235, :4175-4187, :101

### [non-spec.2.follow-up-fix.1]
DECISION: summary 0073 impact row now credits CODE-14 with the §10.1.4 close-context split — BECAUSE CODE-14 owns it everywhere else (summary CODE-14 index line, non-spec-changes CODE-14) — ALTERNATIVES: deleting the sentence lost the record of what this proposal takes back from 0073.
DECISION: the S-2 second-window precondition ("every in-flight `pkg/adapter` handler edit has merged first") is stated once, in the summary's proto-window decision bullet; the R1b impact row points at it — BECAUSE G4 left "The precondition" with no antecedent — ALTERNATIVES: restoring it in the R1b row made two statements.
DECISION: the spec-map bullets for slotbinder_test.go and binder_test.go now say "the cases this change lands there" rather than naming owner deliverables — BECAUSE an owner list parallel to the Testing group label drifted once (G2 added CODE-9 and CODE-13) and would drift again — ALTERNATIVES: the suggested "CODE-4, CODE-5, CODE-8 and CODE-13" still omitted CODE-9, which the group label names.
WATCHOUT: the Testing gateway group label (non-spec-changes, "CODE-4, CODE-5, CODE-8, CODE-9 and CODE-13's cases") is now the only owner list for those tier-1 files; do not add one back to Files touched.

### [non-spec.2.review-applicability.1]
FACT: The implement-proposal-build executor extracts deliverable ids by regex only for spec-lane steps (`/SPEC-[A-Za-z0-9.-]+/g` in runSpecStep); a docs- or code-lane line that mentions a SPEC id as a test subject (S7 now does) is not parsed as naming that deliverable — EVIDENCE: .claude/workflows/implement-proposal-build.js:1408
FACT: After the decision-53 re-cut, no Testing subsection names CODE-13. Its tier-1 cases (compensation token, leaked disposition, typed refusal compensated, attempt-scoped lease release, per-stage table, cancelled context) sit under "Gateway tests for CODE-4, CODE-5, CODE-7 and CODE-8, tier 1" in the group labelled "CODE-4 and CODE-5's cases" — EVIDENCE: non-spec-changes.md:3274, :3306
WATCHOUT: Round-1 fixes reduced S10-S19 to "CODE-n in full" pointers. That makes the Testing section's deliverable labels the only step-to-test map, so a stale label there is now a step-assignment defect. Before this round the step lines carried the mapping themselves.
USEFUL [history: Spec-lane step S4 carries test-file edits]: the fix moved the sweep into the non-spec tier-11 **For SPEC-3** block and to S7. Every pointer (spec-changes table rows :584-585, SPEC-3 §12.6 commentary, CODE-10 sub-block, spec-map listing, 0071 impact row) was checked and resolves.

### [non-spec.2.review-client-surface.1]
- FACT: No SDK, docs page or schema other than schemas/lenny-adapter.proto names an adapter ErrorCode, ShutdownRequest or SlotReclaimOutcome, so SCHEMA-1's wire additions have no SDK or docs parallel to mirror; the regenerated pkg/proto/adapter/v1 and the tier-3 gate are the only parallels. EVIDENCE: grep over sdks/ docs/ schemas/ returned only schemas/lenny-adapter.proto
- FACT: Round-2 SCHEMA-1 proto comments cite rule numbers that match staged §4.7.1: code 28 rule 6, code 29 rule 5, RECLAIMED rules 12 and 14, ABSENT rule 11, SUPERSEDED rule 13. EVIDENCE: spec-changes.md:1013-1028
- FACT: The re-cut stamp-once case (StartSession-created untokened entry, refused under rule 6, then Shutdown naming B answers superseded) is the only reachable form: untokened entries are created only by starting requests, and rule 13's untokened arm answers superseded, so a defective stamp would flip it to reclaimed. EVIDENCE: spec-changes.md:1013-1026
- DEFERRED [summary.md]: the S-2 proto-window step-ordering reasoning is written in full at the summary decision bullet (:172-178) and again in the R1b impact row (:1169). The row should cite the bullet. Filed this round under (g).
- CORRECTS [Open "Is CODE-4's mint-site sentence ... gone?"]: gone. The round-1 fix replaced it with a pointer to §4.7.1's carriage lead-in (non-spec-changes.md CODE-4 mint sites).

### [non-spec.2.review-docs-alignment.1]
FACT: round-2 diff touches docs surfaces only through reductions: checklist S7 and S23 are now pointers (S7 at the non-spec tier-11 block, S23 at DOCS-4), the summary DOCS-1..4 index lines are one clause each, and the spec-changes closing docs paragraph is one sentence naming DOCS-1..4, CODE-9's metrics.md rows and SCHEMA-1. Each DOCS deliverable body (non-spec-changes.md `### DOCS-4` at :2843) still carries its own site list, so no docs site was lost. EVIDENCE: non-spec-changes.md:2843-2852; checklist S7/S23
FACT: the SPEC-3 tier-11 sweep moved from checklist S4 to the non-spec `**For SPEC-3**` block, landed at S7; S4 now runs tier 0 only and S7 depends on S4. The only tier-11 hits for `sessions_served` are spec_28_register_writers_test.go:99/:745 and concurrent_slot_lifecycle_doc_reconciliation_test.go:20,:47,:170. EVIDENCE: grep -rn sessions_served tests/
WATCHOUT: DOCS-4's body still says it "lands beside DOCS-2", while DOCS-2 lands at S7 and DOCS-4 at S23 (depends only on S4); bookkeeping, not filed. EVIDENCE: non-spec-changes.md:2851-2852

### [non-spec.2.review-fresh.1]
FACT: every pointer the round-1 reductions introduced resolves: "the summary's cascade decision" (summary.md:131-143), "the summary's `unconditional_teardown` decision" (summary.md:121-129), "the summary's proto-window decision" (summary.md:168-174), the failed-drain defects entry (summary.md:1104), "§7.1 states that the fence does not depend on it" (spec-changes.md:366), "SPEC-2's commentary gives why it reads no outcome" (spec-changes.md:369-374), "CODE-7 gives why" (non-spec-changes.md:1831-1835). EVIDENCE: as cited.
FACT: the shipped Shutdown files ReportSessionScrub for any bound removed entry (pkg/adapter/session.go:238-279), so the tier-7a start-versus-reclaim race's StartSession arm ("reclaim files no ReportSessionScrub") passes only once CODE-15 (S17) and CODE-2 (S18) are in; it cannot land at S14 or S16. EVIDENCE: non-spec-changes.md:3550-3584.
USEFUL [Open, "Is CODE-4's mint-site sentence ... gone?"]: yes, the round-1 diff removed it; this OPEN can close.
USEFUL [Open, "Does `**What the change costs.**` duplicate SCHEMA-1's field table"]: the counts were removed in round 1; closeable.
DECISION: filed three findings: (1) the start-versus-reclaim/reverse-ordering tier-7a cases have no landing step and S18 omits S17 (Open entry at review-log.md:648); (2) summary R1b impact row restates the proto-window decision bullet (Open entry "R1b impact row"); (3) checklist S20 and S22 restate CODE-5/CONF-1 (the round-1 pointer reduction skipped them) — BECAUSE each is the caller-named Open class or rule (g) under the pointer directive — ALTERNATIVES: S4 tier-11 deferral to S7 not filed (same accepted pattern as S5); CONF-1 stamp-once rewrite not filed (settled, review-log.md:988).

### [non-spec.2.review-mechanism.1]
DECISION: filed one finding (contradiction, step ownership): CODE-6's `terminateHeldSession` bullet (non-spec-changes.md ~1370-1385) says "this deliverable mints" the per-member ten-second close context "after the guard acquisition returns" and gates the hold release on CODE-14's `guarded` term, and Testing files the per-member close-budget case as CODE-6's (heading "Adapter tests for CODE-6, tier 1", case at ~3021; spec-map note ~3997), while the summary deliverable index and files-touched holdstate.go entry (~4055-4071) give the split to CODE-14 (S15). CODE-6 lands at S14, before any guard exists — BECAUSE the review log OPEN "Which deliverable owns the §10.1.4 per-member ten-second close-context split?" had to be settled this round per the caller — ALTERNATIVES: filing S20's residual mechanism restatement (below bar for this lens; S20 agrees with CODE-5).
FACT: the r1->r2 diff is almost entirely pointer reductions; every new pointer target resolves (summary cascade / unconditional_teardown / proto-window decisions, spec-changes Design "The adapter's atomicity is stated once.", SPEC-2 commentary on the acknowledged-clean predicate, CODE-13 "The lease release is scoped to the attempt.", tier-11 "**For SPEC-3**" sweep, Testing "Tier 9, the credential fence."). EVIDENCE: summary.md:130-143, spec-changes.md:56, :369-374, non-spec-changes.md:1039, :3692, :3715
FACT: no non-test Go file outside adapterclient/client.go and generated pb.go builds the bind or Shutdown request literals, so the two _test.go-scoped grep sweeps plus CODE-4's client.go target are complete. EVIDENCE: grep over pkg/ tests/ cmd/ sdks/
USEFUL [non-spec.1.fix-design-G3.1]: the stamp-once CONF-1 rewrite (untokened started entry, B refused under rule 6, Shutdown naming B answers superseded) is consistent with staged rules 5, 6, 13 and 14.
OPEN: releaseAttemptCredentials releases user-source leases (UserCredentials.MintProto, binder.go:1248) through CredentialAssigner.Release, which is a no-op for an ID credassign does not hold (credassign.go:375-384); whether usercreds leases live in that store is untraced. Pre-existing, outside this round's diff.

### [non-spec.2.review-single-source.1]
FACT: every pointer the r1 fix round wrote resolves: spec-changes Design **The adapter's atomicity is stated once.** (spec-changes.md:56), the summary cascade and `unconditional_teardown` decisions (summary.md:122-143), SPEC-2 commentary on the outcome-free leaked disposition (spec-changes SPEC-2 §7.1 commentary), CODE-7's opening (non-spec:1829-1836), CODE-8's opening (non-spec:1926-1937), CODE-13 **The lease release is scoped to the attempt.** (non-spec:1039), the non-spec tier-11 **For SPEC-3** block (non-spec:3715). No stale "checklist S4" sweep reference remains.
USEFUL [review-log Open "Does the summary's R1b impact row still restate the S-2 proto-window reasoning?"]: still true; filed this round with SCHEMA-1's opening as the third site.
USEFUL [review-log Open "Do checklist S19 and S20 still restate?"]: S19 is reduced; S20, S9 (full field-number table) and S22 still restate. Filed.
WATCHOUT: the r1 reductions cut the DOCS and CODE-8 index lines but left CODE-2, CODE-6, CODE-9 and CODE-14's index lines restating site lists; and the two mandatory-field literal sweeps are stated in full both in Testing (non-spec:3213-3234) and in files-touched (non-spec:4175-4187). EVIDENCE: summary.md:1182-1192.
UNVERIFIED: summary.md:1117-1119 now says the reaper's owner "is named in the spec-changes file's Edge-cases section"; that section (spec-changes.md:180-192) names the reaper but no owner (summary decision 41 names remediation position 2). Below the bar as commentary; a fixer touching it should point at decision 41.

### [non-spec.2.review-test-coverage.1]
DECISION: no test-coverage finding this round — BECAUSE every r1->r2 hunk is a reduction to a pointer, and each behaviour whose test text moved still has a concrete home: the SPEC-3 sessions_served sweep moved from checklist S4 into Testing `### Documentation reconciliation tests, tier 11` **For SPEC-3** and S7 carries it; the Binder.Prepare minted-lease assertion moved from the CODE-8 bullet into the CODE-13 bullet **The credential release is scoped to the attempt** (now three arms, both bind entry points); the tier-3 behavioural list now points at CONF-1's per-rule list, which still carries every rule 1-15 case plus the four cross-rule cases; S17 now lists tier 9 for the registered-but-unbound matching-reclaim arm — ALTERNATIVES: filing S4's tier-11 deferral leaving S6's tier-11 run red between S4 and S7 (rejected: S5 already deferred tier 11 to S7 before this round, so S6's tier 11 was red already; sequencing, not test listing).
FACT: the only tier-11 files pinning a §12.6 sessions_served sentence are spec_28_register_writers_test.go (podStateGatewayWrittenSentence, :99-101, asserted :745) and concurrent_slot_lifecycle_doc_reconciliation_test.go; spec_28_index_rows_test.go's hits are a slugify fixture and pin nothing in §12.6 — EVIDENCE: tests/tier11_docs/spec_28_index_rows_test.go:778-782
FACT: an AssignProto failure inside Binder.assignCredentials returns through the same `:949` failure arm as the AssignCredentials RPC failure, so the CODE-13 "second AssignProto call fails" arm at Binder.Prepare is reachable at that one arm — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:949-951, :1221-1230
USEFUL [review-log line 988, CONF-1 stamp-once re-set]: explained why the tier-3/10 stamp-once case drives a rule-6-refused request and reads the Shutdown naming B; it still discriminates a stamp-before-compare implementation.

### [non-spec.3.fix-G1.1]
DECISION: CODE-14's new paragraph **`terminateHeldSession`'s guard and close context.** (after the derivation table) is the one home of the per-member close-context split, the onHoldTimeout comment rewrite, and the separate deferral of the guard release — BECAUSE the split exists only once CODE-14's guard lands (S15), and the files-touched and 0073-row copies had no home to point at — ALTERNATIVES: files-touched bullet as home (a file inventory, not a deliverable); split back in CODE-6 (guard deadline does not exist at S14).
DECISION: CODE-6 (S14) now names no guard symbol: releaseSessionSlot releases on `treeErr == nil` and terminateHeldSession on `closeErr == nil && treeErr == nil`, each with one clause "CODE-14 adds the `guarded` conjunct"; the guard-acquiring/under-guard split lives only in CODE-14's Resume paragraph — BECAUSE S14 precedes S15 and cannot compile against lockSlotGuard.
WATCHOUT: the two-deferred-releases trap in terminateHeldSession now lives once, in CODE-14's new paragraph; the files-touched holdstate.go bullet still names both releases as a per-file list and the hold-predicate restatement there is an open reduction (separate finding) — EVIDENCE: non-spec-changes.md files-touched holdstate.go bullet.
FACT: the shared close context and its comment are pkg/adapter/holdstate.go:197-203 (comment 197-200, closeCtx 201) — EVIDENCE: pkg/adapter/holdstate.go:197-203.

### [non-spec.3.fix-design-G1.1]
DECISION: CODE-14 gets one new bold paragraph, placed after its derivation table and before **Disposition of an expired acquisition at a removing site.**, headed **`terminateHeldSession`'s guard and close context.** It is the one home of the §10.1.4 per-member close-context split, the rewrite of the `onHoldTimeout` comment, and the trap that the guard release is deferred separately from CODE-6's hold release. CODE-6 drops every guard term: `releaseSessionSlot` releases on `treeErr == nil`, and the guard-acquiring/under-guard split sentence and the two-releases sentences are deleted. The files-touched holdstate.go CODE-14 sentence and the summary 0073 row become pointers — BECAUSE S14 lands CODE-6 before S15 lands CODE-14, and the split had no home in CODE-14's body — ALTERNATIVES: (a) keep the files-touched sentence as the home, rejected because a file list is not a deliverable; (b) keep the "distinct releases" sentence in CODE-6, rejected because the guard release does not exist at S14.
CORRECTS [standing context "CODE-6 still owns the per-member ten-second close context in `terminateHeldSession`"]: CODE-14 owns it (see [non-spec.2] entries at review-log DECISION lines on CODE-14 single owner). The entry should be deleted at compaction.
WATCHOUT: CODE-14 already states the split of releaseSessionSlot into releaseSessionSlot(ctx, id) and releaseSessionSlotUnderGuard in its Resume paragraph, so CODE-6 needs no pointer to it. EVIDENCE: non-spec-changes.md CODE-14 "so `releaseSessionSlot` splits in two:".

### [non-spec.3.review-applicability.1]
FACT: the r2->r3 diff reduces checklist S9, S20 and S22 to "in full" pointers. Each reduction's destination carries what the old step line said. SCHEMA-1 carries the proto edits, `make generate-proto`, the closed-field-set gate move, the two WIRED rows, and the rule that the ABSENT row lands at S22. CODE-5 carries the helper narrowing and both classifier arms. CONF-1 plus the files-touched spec-map listing carry the tier-3 directory, the tier-10 file and both spec-map entries. — EVIDENCE: non-spec-changes.md SCHEMA-1 "**Claim register.**" paragraph; CODE-5 Targets; files-touched `tests/spec-map.json` bullet
FACT: CODE-14's deliverable body never names `onHoldTimeout`. The only full statement of the per-member close-context split is the files-touched `pkg/adapter/holdstate.go` bullet ("CODE-14 edits `onHoldTimeout` ..."). CODE-14's expired-acquisition table row presupposes the split ("on the member's own close context"). At S14, CODE-6's `Runtime.Close(ctx, ...)` runs on the shipped shared pass context (pkg/adapter/holdstate.go:201-205), so S14 works as a standalone step. — EVIDENCE: non-spec-changes.md CODE-6 terminateHeldSession sentence; files-touched holdstate.go bullet
CORRECTS [review-log Standing context, "CODE-6 still owns the per-member ten-second close context in `terminateHeldSession`"]: the r2 fix moved that ownership to CODE-14 in CODE-6, Testing, the spec-map listing, the SPEC-3 commentary and the 0073 impact row. CODE-6 now keeps only the close/tree error capture and the `closeErr == nil && treeErr == nil` release. CODE-14 adds the `guarded` conjunct and the context re-scope.
DECISION: no findings this round — BECAUSE every hunk's relocation has its destination text staged, and no stale CODE-6 attribution for the close context survives in the staged files (checked with grep for per-member, close budget and ten-second) — ALTERNATIVES: filing CODE-14's body for not naming `onHoldTimeout` was rejected, because the files-touched bullet states the edit in full and attributes it to CODE-14, so nothing is lost.

### [non-spec.3.review-client-surface.1]
FACT: The r2-to-r3 diff touches no client-facing representation: S9/S20/S22 were reduced to pointers ("SCHEMA-1 in full"), and SCHEMA-1's own text still carries the regen, the two WIRED rows and the closed-field-set gate move (wantReq 7/8, ShutdownResponse 3) — EVIDENCE: 0081...non-spec-changes.md:2337-2342, :2507-2528; tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208,:211,:259
FACT: The bind-field sweep's qualifier and mid-session exemption moved from Testing into the files-touched entry and the Testing cross-reference resolves to it — EVIDENCE: non-spec-changes.md:3211, :4161-4174; pkg/adapter/files_updated_test.go:78-80 (MidSession: true, no token)

### [non-spec.3.review-fresh.1]
FACT: after round 2's reductions, the §10.1.4 per-member close-context split has its single home in the `pkg/adapter/holdstate.go` bullet of `## Files touched on application (non-spec)`, attributed to CODE-14; CODE-6's body cites it ("on the context CODE-14 re-scopes") and CODE-14's body carries only the `terminateHeldSession` expired-acquisition row. CODE-14's `guarded` conjunct is stated once, in its **Disposition of an expired acquisition at a removing site** paragraph. — EVIDENCE: non-spec-changes.md:1376-1379, :1753-1756, :4043-4052
FACT: the ABSENT claim-register row is staged in SCHEMA-1 and lands with CONF-1 at S22; SCHEMA-1, the CONF-1 tier-10 block, files-touched and checklist S9/S22 all agree. — EVIDENCE: non-spec-changes.md:2527-2528, :3467, :3918-3920; implementation-checklist.md S9, S22
FACT: the two test-literal sweeps now have their single home in the files-touched grep entries; the Testing scope-accounting paragraph cites them. No other `ShutdownRequest` Go type exists in the tree, so the unqualified `ShutdownRequest{` grep is exact. — EVIDENCE: non-spec-changes.md:3209-3218, :4155-4174; pkg/proto/adapter/v1/lenny-adapter.pb.go:5628

### [non-spec.3.review-mechanism.1]
FACT: the r2 fix moved terminateHeldSession's `guarded` conjunct and the per-member close context out of CODE-6 into CODE-14 by pointer, but CODE-14's body (non-spec-changes.md CODE-14 section) never stages the onHoldTimeout re-scope; its only full statement is the files-touched `pkg/adapter/holdstate.go` bullet ("CODE-14 edits `onHoldTimeout` ..."). EVIDENCE: non-spec-changes.md:1379, :4043-4051
WATCHOUT: CODE-6 (S14) still states releaseSessionSlot's `guarded && treeErr == nil` and terminateHeldSession's deferred slot-guard release, both of which need `lockSlotGuard` from CODE-14 (S15); the sibling terminateHeldSession site now defers `guarded` to CODE-14. EVIDENCE: non-spec-changes.md:1381, :1394-1396, :1409-1412
FACT: the S9/S20/S22 checklist reductions lose nothing: CONF-1/Testing carry the spec-map entries (:3444-3446, :3463, :3931-3936), SCHEMA-1 carries the ABSENT-row timing (:2527-2528), CODE-5 carries the narrowed signature and both classifier arms.

### [non-spec.3.review-single-source.1]
FACT: after the r2 fix moved the §10.1.4 per-member close-context split off CODE-6, CODE-14's deliverable body (non-spec-changes.md `### CODE-14`) still does not state the split; its only full statement is the files-touched `pkg/adapter/holdstate.go` bullet ("CODE-14 edits `onHoldTimeout` ..."), with a second full copy plus the non-last-close reason in the summary 0073 impact row. CODE-6's new pointer "on the context CODE-14 re-scopes" lands on a deliverable that never says it re-scopes anything. EVIDENCE: non-spec-changes.md CODE-6 terminateHeldSession bullet; CODE-14 expired-acquisition table row "on the member's own close context"; summary.md 0073 row.
DECISION: filed as one single-source finding with the remedy: state the split once in CODE-14's body, cut the files-touched bullet and the 0073 row to pointers — BECAUSE the review-log decision (CODE-14 owns the split) names a home that holds no statement — ALTERNATIVES: making files-touched the home was rejected because a files-touched bullet is an edit-site list, and CODE-6/Testing already cite CODE-14.
FACT: the r2 hunks for S9/S20/S22, the scope-accounting reduction, the R1b row, and the proto-window bullet each resolve cleanly: SCHEMA-1 states the ABSENT row's S22 landing once, and both sweep grep patterns plus the `adapterv1.` qualifier now live only in the files-touched sweep bullets.

### [non-spec.4.fix-G1.1]
DECISION: reduced the Files-touched `pkg/adapter/holdstate.go` bullet's terminateHeldSession sentences to a pointer at CODE-14's **`terminateHeldSession`'s guard and close context.** and CODE-6 — BECAUSE the copy had drifted (no `guarded` conjunct, `removeSlotTree` where CODE-6 stages `removeSlotTreeVia(m.state)`) — ALTERNATIVES: correcting the copy in place, rejected because it keeps a second statement that drifts again.
WATCHOUT: Files-touched bullets are pointers; the release predicate, `runtime_close_failed` log contract and error-handling of terminateHeldSession live only in CODE-6, the separate guard release only in CODE-14. Do not re-expand the bullet. — EVIDENCE: non-spec-changes.md CODE-6 terminateHeldSession bullet; CODE-14 **`terminateHeldSession`'s guard and close context.**

### [non-spec.4.fix-design-G1.1]
DECISION: files-touched `pkg/adapter/holdstate.go` bullet reduced to a pointer naming CODE-14 (guard acquisition, two deferred releases, close context) and CODE-6 (hold release, completion predicate, captured close and tree-removal errors); the expired-acquisition and pass-1 clauses stay — BECAUSE the bullet restated CODE-6's release predicate and log contract and had drifted (missing `guarded`, `removeSlotTree` for `removeSlotTreeVia`) — ALTERNATIVES: editing the bullet into agreement with CODE-6/CODE-14, rejected per the pointer directive.
FACT: after this fix the only statements of terminateHeldSession's release predicate and `runtime_close_failed` are CODE-6's terminateHeldSession bullet (non-spec-changes.md ~1374-1392), and the separately-deferred guard release lives only in CODE-14's **`terminateHeldSession`'s guard and close context.** — EVIDENCE: grep runtime_close_failed non-spec-changes.md hits only ~1383, ~1404 besides the removed bullet.

### [non-spec.4.review-mechanism.1]
FACT: the r3 fix moved the terminateHeldSession two-release and per-member close-context split into one CODE-14 home, `**`terminateHeldSession`'s guard and close context.**` (paragraph after CODE-14's scope table); CODE-6, files-touched, the 0073 row, summary Decisions and spec-changes' ten-second paragraph now carry at most one clause. EVIDENCE: non-spec-changes.md:1745-1752, summary.md:1160
FACT: the releaseSessionSlot guard-acquiring / under-guard split has its one home in CODE-14's `Resume` guard paragraph (releaseSessionSlotUnderGuard), after r3 deleted the CODE-6 forward reference. EVIDENCE: non-spec-changes.md:1783-1790
WATCHOUT: the files-touched `pkg/adapter/holdstate.go` bullet still writes out CODE-6's terminateHeldSession completion predicate, its runtime_close_failed log fields and its no-control-flow-change invariant, without the `guarded` conjunct and naming `removeSlotTree` where CODE-6 stages `removeSlotTreeVia`. Reported this round as a (g) reduction. EVIDENCE: non-spec-changes.md:4040-4047 vs :1376-1392

### [non-spec.4.review-single-source.1]
FACT: r3 fix moved the §10.1.4 per-member close-context split into one CODE-14 home, **`terminateHeldSession`'s guard and close context.** (non-spec-changes.md ~1745); CODE-6 (~1379), files-touched holdstate bullet (~4049) and the 0073 impact row (summary.md:1160) now cite it. The split of releaseSessionSlot into guard-acquiring and under-guard forms now lives only in CODE-14 (~1784-1790). EVIDENCE: non-spec-changes.md:1745-1752
WATCHOUT: the same r3 fix left the adjacent sentences of the files-touched holdstate.go bullet (~4040-4047) restating CODE-6's terminateHeldSession release predicate (both close and tree-removal errors nil), the runtime_close_failed logging, and CODE-14's two-deferred-release structure; that bullet was filed this round as a (g) copy. Reduce it to a pointer; do not edit it into agreement with CODE-6/CODE-14. EVIDENCE: non-spec-changes.md:1381-1386, :1745-1746, :4040-4047

### [non-spec.5.review-mechanism.1]
FACT: the r4-to-r5 diff (review logs excluded) is one hunk: the files-touched holdstate.go bullet is reduced to pointers at CODE-14's **`terminateHeldSession`'s guard and close context.** and CODE-6. Both homes state what the pointers attribute to them (CODE-14 guard release deferred separately from CODE-6's hold release; CODE-6 hold release, `closeErr == nil && treeErr == nil` predicate, `runtime_close_failed` logging) — EVIDENCE: non-spec-changes.md:1372-1394, :1745-1752, :4040-4046
FACT: the Open entry asking whether CODE-4's "Its requests carry an empty `bind_attempt`" survives is closed: no copy remains outside the review logs (grep).

### [non-spec.5.review-single-source.1]
FACT: The r4-to-r5 diff (review log excluded) is one hunk: the files-touched `pkg/adapter/holdstate.go` bullet was reduced to pointers at CODE-14's **`terminateHeldSession`'s guard and close context.** and CODE-6. Every element it points at is stated at its home: hold release, completion predicate, captured close and tree errors, and `runtime_close_failed` in CODE-6 (non-spec-changes.md:1373-1393), and the guard acquisition and separate deferred release in CODE-14 (non-spec-changes.md:1741, 1745-1752). No site the rewrite affected was left stale. — EVIDENCE: non-spec-changes.md:4040-4046

### [non-spec.6.fix-G1.1]
DECISION: S16 now lands CODE-1 and CODE-15 together; the S17 line is deleted without renumbering (as S21 was) — BECAUSE CODE-1's staged removing arm ends at `st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)` and every consumer of `release`/`st` (deferred completed-gated release, removeSlotTreeVia, the removing arm's answerShutdown return) is CODE-15's, so S16 alone fails tier 0 and its own Shutdown test rows — ALTERNATIVES: moving the consumers into CODE-1 (re-cuts the settled decision-53 split); a temporary stub in S16 (invents unstaged code, leaves the reclaim hold unreleased).
WATCHOUT: Do not split CODE-1 and CODE-15 into separate checklist steps again; CODE-1 does not compile without CODE-15's consumers of `reclaimSlotLocked`'s results. EVIDENCE: non-spec-changes.md end of the CODE-1 snippet vs CODE-15's split-gates snippet.
CORRECTS [review-log.md "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's `live`?"]: CODE-15 now lands in S16, and S18 depends on S16, so the race lands after `live` exists.
DECISION: The send-before-require ordering rule's single home is the checklist preamble; the summary Watch-out bullet is now a pointer ("S13 lands before S14 and S16. The implementation checklist's preamble gives the reason.") — BECAUSE step order belongs to the checklist — ALTERNATIVES: summary as home (drifts on every re-cut).

### [non-spec.6.fix-design-G1.1]
DECISION: merge CODE-1 and CODE-15 into one checklist step S16 and delete the S17 line outright (S21 was retired by deletion, not a tombstone) — BECAUSE CODE-1's staged snippet ends at `st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)` and every consumer of those locals is CODE-15's, so S16 alone fails tier 0 — ALTERNATIVES: moving the removing-arm tail into CODE-1 (re-cuts deliverables; the decision-53 re-cut is settled); keeping S17 with a stub at S16 (invents code).
FACT: S16's tier list (0,1,2,3,4,7a,9,10) already covers S17's (0,1,2,4,9); no Depends-on line names S17; S18, S19 and S22 already depend on S16 — EVIDENCE: implementation-checklist.md:44-54.
CORRECTS [non-spec.1.review-fresh.1]: the UNVERIFIED question at review-log.md:647 (which step lands the tier-7a race reading CODE-15's `live`) is answered once S16 carries CODE-15: S18 depends on S16.
DECISION: summary Watch-out send-before-require bullet cut to a pointer at the checklist preamble, which stays the home — BECAUSE the step order belongs to the checklist.

### [non-spec.6.review-applicability.1]

DECISION: filed one finding: S16 (CODE-1) cannot land alone. CODE-1's staged removing arm ends at `st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)` (non-spec-changes.md:278), and every consumer of `release`, Shutdown's `removeSlotTreeVia`/`treeErr`, the `completed` predicate and the removing arm's `answerShutdown` return (:325-431) are CODE-15's staged code at S17, while CODE-1 deletes the shipped trailing clause three. Remedy proposed: land CODE-1 and CODE-15 in one checklist step — BECAUSE it touches only the checklist and keeps the settled re-cut — ALTERNATIVES: staging an interim release in CODE-1 (adds text and a throwaway mechanism).
FACT: shipped Shutdown's tail (pkg/adapter/session.go:238-290) has no `release` variable and returns `&ShutdownResponse{ExitedCleanly: closeErr == nil}` after a trailing `startPodScrub`, so CODE-1's partial snippet leaves an unused `release` (go build fails) — EVIDENCE: pkg/adapter/session.go:238,:283-290
WATCHOUT: "Adapter tests for CODE-6, tier 1" (non-spec-changes.md:2866) holds cases whose subject is CODE-14's guard (serialization, destructive expiry, uncontended acquisition, admission expiry, and the hold-admission case's acquireSlotGuardForResolve refusal site); only S15's "its tier-1 expired-acquisition case" says one of them is CODE-14's. Judged resolvable by an implementor and not filed; a fixer relabelling that heading would close it.
WATCHOUT: CODE-7 (S11, non-spec-changes.md:1903-1906) and CODE-4's client.go target (S13, :818-824) both claim `unconditional_teardown` inside Client.Shutdown and the new ShutdownReclaim; CODE-4's Targets bullets for slotbinder.go/binder.go say the call sites set `unconditional_teardown`, which CODE-4's body (:872-878) denies. Harmless in order; not filed.
USEFUL [review-log Settled, golangci-lint NON-FATAL]: saved filing the S10 unused-helper gate failure.

### [non-spec.6.review-citations.1]
DECISION: no findings — BECAUSE every full-path and short-form file:line citation in spec-changes, non-spec-changes, summary and checklist (about 210 full-path, 110 short-form) resolves to what the proposal claims — ALTERNATIVES: filing binder.go:1629 (the fmt.Errorf wrap is at :1630) and adapter_metric_catalog_test.go:56 as drift; rejected, off-by-one without meaning change.
FACT: the round-5 holdstate.go files-touched bullet now points at CODE-14's **`terminateHeldSession`'s guard and close context.** (non-spec-changes.md ~1745) and at CODE-6 (~1370-1400); both anchors exist and carry what the bullet attributes. EVIDENCE: non-spec-changes.md:4040-4044
FACT: CredentialAssigner (binder.go:319-332) declares AssignProto and ReleaseSession only; CODE-13 stages the Release(leaseID) member, and both production implementors already have it (credassign.go:380 Service.Release, credassign/client.go:299 Client.Release). Both look the lease up by ID in the shared lease store, so user-source leases minted by MintProto are covered. EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:380-384,414-423
USEFUL [Open line 151, releaseAttemptCredentials user leases]: can close; binder.go:342-343 states user leases share the pool assigner's lease store, and Release is keyed on that store.
FACT: every Server method staged Go code calls either exists (noteRuntimeStartedLocked runtimegeneration.go:36, slotStateLocked slot.go, emitFinalUsage/drainViaLifecycle session.go, reportSessionScrub, startPodScrub) or is declared by a deliverable (runtimeHoldsLocked CODE-15, lockSlotGuard CODE-14, noteCompensationOutcome/compensationCause CODE-9/CODE-13).

### [non-spec.6.review-client-surface.1]
FACT: no client SDK (sdks/client/*) or runtime SDK (sdks/runtime/*) consumes schemas/lenny-adapter.proto or names SETUP_COMMAND_FAILED; the served OpenAPI (pkg/gateway/externalapi/openapi/openapi.json, not pkg/gateway/openapi/) carries no error-code enum and only the setup-output path. The SPEC-5 §15.1 re-key therefore has exactly three parallels: errorclassify.go and start.go comments (CODE-11) and docs/reference/error-catalog.md:129 (DOCS-3). — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:936-943
FACT: the only non-test ShutdownRequest literal is Client.shutdown (pkg/gateway/runtime/adapterclient/client.go:814); cmd/lenny-gateway/user_revocation.go:129 reaches it through Client.Shutdown, so CODE-1's in-builder unconditional_teardown covers the revoke fan-out. — EVIDENCE: client.go:807-866
WATCHOUT: docs/api/internal.md:75-160 still shows a StopSession-era RuntimeAdapter service with a StartSessionRequest that predates the shipped proto; it is pre-existing drift (already DEFERRED for ABORTED), not an edit site this proposal makes wrong. — EVIDENCE: docs/api/internal.md:110

### [non-spec.6.review-docs-alignment.1]
DECISION: no docs-alignment finding in round 6 — BECAUSE the r5->r6 diff touches only the files-touched holdstate.go bullet (reduced to a pointer at CODE-14/CODE-6), which is not a docs surface. The DOCS-1..4 site lists, CODE-9's metrics.md rows and the tier-11 blocks still match the tree. ALTERNATIVES: (1) The RESUME_FAILED row's cause parenthetical (docs/reference/error-catalog.md:157, spec/15:1133) does not list the refusals CODE-5 now holds in awaiting_client_action. Not filed: the spec row is unedited, the docs page mirrors it, and the parenthetical explains "generic reason" rather than closing the set. (2) The glossary statement "the per-pod claim is deleted only when the reserved hold expires or the pod terminates" (docs/reference/glossary.md:258) was already false before this change, because failPhase drain and the existing accountSlotFailure DrainSandbox tail both delete the claim. Not filed.
FACT: DOCS-2's added substrings for the Shutdown-row gate ("no other bound session", "a session whose start the adapter has admitted", "either the bind attempt", "reclaimed", "absent") each occur verbatim in the staged row. The shipped gate asserts only four substrings (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307). EVIDENCE: non-spec-changes.md DOCS-2 row
FACT: DOCS-1's leaked clause "a reclaim it sent is never answered" matches staged §7.1, where an error response counts as unanswered ("A reclaim the adapter does not answer ... not acknowledged clean", spec-changes.md:366). An error-refused reclaim (for example, the coordinator-hold case) is therefore covered. EVIDENCE: spec-changes.md:366
FACT: the metrics.md gateway slot table ("### Session recycle and slot metrics", docs/reference/metrics.md:158-167) has four columns and no "Used by" column; only "## Adapter metrics" (:171-180) has five. EVIDENCE: docs/reference/metrics.md:160,:177

### [non-spec.6.review-edit-sites.1]
FACT: the r5 reduction of the files-touched `holdstate.go` bullet points at CODE-14's **`terminateHeldSession`'s guard and close context.** label (non-spec-changes.md:1745) and at CODE-6's closeErr/treeErr text (non-spec-changes.md:1372-1395); both homes exist and carry what the pointer names. EVIDENCE: non-spec-changes.md:4040-4046
FACT: `docs/api/internal.md` hand-copies proto messages (StartSessionRequest, StopSession, DemoteSDKRequest) that already diverge from schemas/lenny-adapter.proto and has no Shutdown section, so bind_attempt/unconditional_teardown make it no more wrong; its gRPC status table omitting ABORTED is pre-existing (checkpoint already answers codes.Aborted, pkg/adapter/checkpoint.go:115). Not an edit site owed by 0081. EVIDENCE: docs/api/internal.md:103-121, :488-500
FACT: the retired "sessions_served incremented at each session release" carrier in migrations/0167_runtime_definitions_execution_mode_service.up.sql:102 wraps across lines but CODE-10's last alternation (`sessions_served ... each` on one `--` line) still matches line 102. EVIDENCE: non-spec-changes.md:2139
FACT: every tree type implementing `AssignProto` outside credassign is a test fake; the files-touched `ReleaseSession(` grep sweep covers adding `Release(string)`; `fakeExtendAssigner` already has it. EVIDENCE: cmd/lenny-gateway/cred_renewal_extend_wiring_test.go:127
FACT: `docs/operator-guide/multi-tenancy.md:72` states only that the per-slot cleanup runs at each release, with no reporting clause, so it stays true under SPEC-3 and is correctly outside DOCS-4. EVIDENCE: spec-changes.md SPEC-3 non-carrier list

### [non-spec.6.review-fresh.1]
- FACT: the round-5 holdstate.go files-touched reduction points at CODE-6 (hold release, completion predicate, captured close and tree errors, non-spec-changes.md CODE-6 "Every site that deregisters" bullet) and at CODE-14's **`terminateHeldSession`'s guard and close context.** label, which exists; both homes carry what the pointer names. EVIDENCE: non-spec-changes.md:4040-4042, :1374-1393, :1745
- FACT: `CredentialAssigner` (binder.go:319-333) lacks `Release`; CODE-13 stages the member, and both production implementations already declare it (credentials/credassign/credassign.go:380, client.go:299). Note the package path is pkg/gateway/credentials/credassign, not pkg/gateway/credassign. EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:380
- FACT: CODE-4's Shutdown caller citations (slotbinder.go:542/:574, binder.go:2037/:2043, user_revocation.go:129, client.go:807-824/:860-867) and CODE-14's releaseSessionSlot / ReleaseSlotForTest caller enumeration match the tree exactly; client.go:814 is the only non-test ShutdownRequest literal. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:814
- USEFUL [Open "WIRED rows at S9", "tier-7a race step"]: both already refuted in history; do not re-file.

### [non-spec.6.review-kubernetes.1]
FACT: The proposal's only apiserver-facing surface is the gateway's existing SandboxClaim delete in failPhase's drain (spec/04 §4.6.1 "Pod claim mechanism") and the §4.6.1/§6.2 projection wording; no staged edit adds a status write, finalizer, webhook rule, or controller on the bind hot path. The bind_attempt token lives in adapter memory and gRPC, never in a CRD. A Kubernetes-idiom lens has nothing to review beyond that. EVIDENCE: non-spec-changes.md Testing "Tier 2, the adapter against envtest" paragraph; spec/04_system-components.md:407-409

### [non-spec.6.review-mechanism.1]
FACT: the r5-to-r6 diff (review logs excluded) is the same single hunk as r5: the files-touched holdstate.go bullet reduced to pointers at CODE-14's **`terminateHeldSession`'s guard and close context.** and CODE-6; both homes carry what the pointer attributes (CODE-6 cites CODE-14 for the `guarded` conjunct). EVIDENCE: non-spec-changes.md:4040-4043, :1372-1394, :1745-1752
FACT: CODE-13's `CredentialAssigner.Release(leaseID string)` is backed in the tree by `credassign.Service.Release` and `credassign.Client.Release`, and the test fakes' new no-op method is staged in files-touched. EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:380, client.go:299, non-spec-changes.md:1048, :4168-4170
FACT: every staged `s.Runtime.*` call is `Close`, which `RuntimeProcess` declares. EVIDENCE: pkg/adapter/session.go:46-62
CORRECTS [Deferred, CODE-6 parked-member bullet]: an expired acquisition at a pass-2 member still means the member's guard was held when the deadline passed (a free guard acquires), so "premature removal" holds for that member too; the deferred claim of falsity looks overstated. Leave for the code-lane owner.

### [non-spec.6.review-performance.1]
FACT: The bind_attempt design adds no etcd, Postgres, or Redis write per bind or per failure. The token is in-memory on the gateway and in the adapter registry. Compensation adds one gateway-to-adapter Shutdown RPC per failed bind, plus the existing ReleaseSlotReservation Redis call. At the top tier the write amplification is zero, and no new watch or informer exists. EVIDENCE: non-spec-changes.md CODE-13 accountSlotFailure callers; spec-changes.md "§10.1's coordinator handoff" bullet.
FACT: The failure modes are no worse than shipped. If the gateway dies mid-compensation, the entry is left exactly as the shipped design leaves every failed bind. A durable pending-reclaim record is deferred to 0082 by the proposal's own text. EVIDENCE: spec-changes.md "a durable record of the pending reclaim" paragraph.
FACT: The r5 holdstate.go files-touched reduction is correct. The pointer target **`terminateHeldSession`'s guard and close context.** exists in CODE-14, and runtime_close_failed is stated in CODE-6. EVIDENCE: non-spec-changes.md:1383, :1745, :4040-4044.

### [non-spec.6.review-reliability.1]
FACT: the attempt-scoped lease release covers user-source leases without extra threading: `UserCredentials.MintProto` leases live in the same lease store, and `credassign.Service.Release(leaseID)` resolves any lease by id through `releaseLocked` — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:338-347, pkg/gateway/credentials/credassign/credassign.go:380-384
FACT: shipped `materializeSlot` releases no credential lease on any failure arm today; the only session-wide `releaseCredentials` calls in podsession are binder.go:1077, :1101, :1963 and slotbinder.go:549 — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:265-351
FACT: every `CredentialAssigner` implementer in the tree (13 incl. cmd/lenny-gateway/cred_renewal_extend_wiring_test.go:128 and tier4/tier9 fakes) falls inside the files-touched `grep -rn "ReleaseSession(" --include=*_test.go pkg/ cmd/ tests/` sweep; both production types already have `Release` — EVIDENCE: credassign.go:380, client.go:299

### [non-spec.6.review-security.1]
FACT: the podspec renderer passes no `--tls-cert-file`, `--tls-key-file` or `--tls-client-ca-file` to lenny-adapter (no hit in pkg/controller/sandbox/podspec/*.go), so in the rendered pod the adapter gRPC server runs plaintext without client authentication (cmd/lenny-adapter/main.go:112-115). This answers the Open entry **Does the podspec always render the adapter's `--client-ca-file`?**: it never renders it. The gap predates this proposal. `unconditional_teardown` grants nothing beyond what the shipped untokened `Shutdown` already granted, so it is no regression here. Raise it as a separate security finding. EVIDENCE: cmd/lenny-adapter/main.go:112-115; grep of pkg/controller/sandbox/podspec
FACT: the only production `ShutdownRequest{` literal is pkg/gateway/runtime/adapterclient/client.go:814, so setting `unconditional_teardown` inside the client methods reaches every non-compensating caller, including §11.4 revoke, and no revoke path is left to fail InvalidArgument. EVIDENCE: grep over pkg, cmd, tests and sdks
FACT: `leaked = err != nil || !cleanly` was already the fail-closed disposition at the shipped `ReleaseSlot` (pkg/gateway/podlifecycle/podsession/slotbinder.go:541-543). CODE-13's compensation reuses it, so no security bound gains a new pod self-report source. EVIDENCE: slotbinder.go:541-543
FACT: `CredentialAssigner` (binder.go:319-332) declares no `Release`. CODE-13 stages its addition, and both implementations have `Release(leaseID string)` (credassign.go:380, client.go:299). EVIDENCE: pkg/gateway/credentials/credassign/

### [non-spec.6.review-single-source.1]
FACT: the only change since r4-prefix is the files-touched holdstate.go bullet, now a pointer at CODE-14's **`terminateHeldSession`'s guard and close context.** and CODE-6 (non-spec-changes.md:4040-4043); CODE-6 states the completion predicate and close-error logging at non-spec-changes.md:1373-1392, so the pointer resolves. EVIDENCE: diff against scratchpad/cp-snap/0081-opt6/non-spec-r4-prefix
DECISION: filed one (g): the send-before-require ordering rule with its reason is stated in full in the checklist preamble (implementation-checklist.md:7-11) and in the summary's Watch-out bullet (summary.md:199-202) — BECAUSE ordering is the checklist's to own — ALTERNATIVES: leaving it as a watch-out copy, rejected as a restated ordering rule.
FACT: the Open entry "Is the fresh-connection proposition stated more than once?" is now closed in fact: grep finds it only in staged §7.1 (spec-changes.md:366); summary.md:104 is a one-clause epoch reason. EVIDENCE: grep -i "fresh connection"
WATCHOUT: SocketRuntimeProcess.Close's listener-teardown mechanics are described at summary.md:209-216, :747-757 and :759-765; they are a tree fact serving three different arguments, not one rule copied; not filed.

### [non-spec.6.review-test-coverage.1]
FACT: the round-5 edit reduced the files-touched holdstate.go bullet to a pointer at CODE-6 and CODE-14; the hold-release predicate it used to restate is pinned in Testing by the tree-removal-failure and runtime-close-failure keep-the-hold cases. EVIDENCE: non-spec-changes.md:1375-1392 (the home), :2960-2972 (the tests), :4040-4042 (the pointer)
DECISION: I did not file the missing assertions on the `runtime_close_failed` and `slot_tree_removal_failed` warnings — BECAUSE they are diagnostic events with no spec-named behavior, and the hold outcome they accompany is asserted — ALTERNATIVES: filing them as missing tests, rejected as nice-to-have under the lens bar

### [non-spec.7.review-applicability.1]
FACT: the r6->r7 diff is two hunks: S17 folded into S16 (CODE-1 and CODE-15 in one step; S16's tiers and Depends already cover old S17's 0,1,2,4,9 and S1,S2,S4), and the summary send-before-require Watch-out reduced to a pointer at the checklist preamble. No proposal file outside the review log still names S17. EVIDENCE: implementation-checklist.md S16 line; summary.md Watch out for
FACT: the Open entry "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's live?" is moot after the fold: S18 depends on S16, which now carries CODE-15. EVIDENCE: implementation-checklist.md S18 Depends line
FACT: step ids now skip S17 and S21 (both retired); nothing depends on either.

### [non-spec.7.review-single-source.1]
FACT: the r6->r7 diff is two hunks: checklist S16 absorbs S17 (CODE-15 lands with CODE-1), and the summary Watch-out send-before-require bullet is reduced to a pointer at the checklist preamble. After it, no file in the proposal names S17, and the preamble at implementation-checklist.md:7-11 is the only stating site of the send-before-require rule. EVIDENCE: grep "S17\b" over the proposal returns nothing outside the review log; summary.md:200.
FACT: the Decisions entry at summary.md:437-438 still reads "so that CODE-13, CODE-14 and CODE-15 carry their later steps". It records how decision 53 was answered, and CODE-15 now shares S16 with CODE-1. That leaves the entry imprecise but does not make it a second site stating the rule, so it is not a (g) finding.

### [non-spec.8.fix-G1.1]
DECISION: summary Watch-out cut to a pointer at CODE-5 for the pod-exclusion withdrawal grounds — BECAUSE CODE-5 is the single home and the summary copy had already dropped the third ground — ALTERNATIVES: adding the third ground to the summary (a second statement that drifts).
FACT: `slotbinder.go:543` (`leaked = err != nil || !cleanly`) sits inside `Binder.ReleaseSlot`, the session-end teardown; the tree ships no compensating Shutdown, CODE-13 introduces it — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:528,543

### [non-spec.8.fix-design-G1.1]
DECISION: summary Watch-out `SlotID == SessionID` bullet cuts the pod-exclusion withdrawal grounds to "CODE-5 records why the pod-exclusion mechanism is withdrawn." — BECAUSE CODE-5 (non-spec-changes.md, "Three grounds, each sufficient") is the single home and the summary copy had already dropped the third ground — ALTERNATIVES: adding the third ground to the summary (restates the reason twice).
DECISION: CODE-13 **The leaked disposition, at the caller.** names the slotbinder.go:543 site as `Binder.ReleaseSlot`'s session-end teardown — BECAUSE the tree ships no compensating Shutdown; CODE-13 introduces compensateFailedSlotBind.

### [non-spec.8.review-applicability.1]

FACT: the round-7 fix merged S17 into S16 (CODE-1 and CODE-15 in full). No `S17` reference survives in the checklist, summary, non-spec or spec staging; S16's tiers (0,1,2,3,4,7a,9,10) cover CODE-15's former 0,1,2,4,9, and S16's Depends line is a superset of S17's. S18 and S22 depend on S16, so CODE-15's `live` gate precedes CODE-2. — EVIDENCE: implementation-checklist.md S16, S18, S22 lines; `grep -n '\bS17\b'` over the non-review-log files returns nothing
CORRECTS [Open "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's `live`?"]: moot after the merge; S18 now depends on S16, which carries CODE-15.
FACT: `CredentialAssigner.Release(leaseID string)` is a staged interface widening (binder.go:319-332 today has AssignProto and ReleaseSession only); both production implementations already exist at pkg/gateway/credentials/credassign/credassign.go:380 and client.go:299. The files-touched "landing at S13 with the signature change" at non-spec-changes.md:4135 means `Client.AssignCredentials`'s bindAttempt parameter (CODE-4), not the CredentialAssigner widening (S19). — EVIDENCE: non-spec-changes.md:820, :1048-1052
WATCHOUT: CODE-1's bullet at non-spec-changes.md:93 places `reclaimSlotLocked` in bindattempt.go, while CODE-6 (:1355) and files-touched (:4031) place it in slotsession.go. Bookkeeping, same class as the refuted slotfailure.go constant; not filed.

### [non-spec.8.review-citations.1]
DECISION: filed one finding: non-spec-changes.md:962-964 calls `slotbinder.go:543` "the shipped compensation call site"; the line is inside `Binder.ReleaseSlot` (slotbinder.go:528), the session-end teardown, and no compensating Shutdown ships today — BECAUSE it misattributes the site's role; summary.md:894-896 attributes the same line correctly — ALTERNATIVES: dropping it as wording; rejected because the label names a component that does not exist in the tree.
FACT: the r6->r7 diff (S17 folded into S16, summary Watch-out reduced to a pointer) leaves no dangling S17 reference; the only other step references in non-spec/summary (S10/S16 at :98, S19 at :1976, S22 at :2533, S13 at :4135) still resolve. EVIDENCE: grep "\bS17\b" over the proposal returns nothing outside the review log
FACT: the remaining sampled full-path citations in non-spec, summary and spec-changes resolve (reserveResumeSlot empty-id guard binder.go:1674-1677, UnhealthyThreshold slothealth.go:215-220, holdOrFailOnResumeError start.go:3609-3610, Reason() switch slotfailure.go:85-104, credassign Release at credassign.go:380 and client.go:299, start.go:2834-2848 leak marking). EVIDENCE: as listed
USEFUL [non-spec.6.review-citations.1]: its full sweep let this round limit itself to the diff plus a re-sample.

### [non-spec.8.review-client-surface.1]
FACT: every field number SCHEMA-1 assigns (PrepareWorkspace 5/6, FinalizeWorkspace 6, RunSetup 5, AssignCredentials 4, Resume 16, ShutdownRequest 7/8, ShutdownResponse 3) is free, and ErrorCode tops out at 27, so 28/29 are free; the "nine fields" count matches the table — EVIDENCE: schemas/lenny-adapter.proto:554-585 and the message blocks for each request
FACT: no client SDK (sdks/) or client doc mirrors the adapter ErrorCode enum or ShutdownRequest; the only generated mirror is pkg/proto/adapter/v1, which SCHEMA-1 regenerates. SETUP_COMMAND_FAILED appears in no openapi.json or SDK file, only docs/reference/error-catalog.md (DOCS-3) — EVIDENCE: grep over sdks/ docs/ pkg/gateway/externalapi/openapi/openapi.json
WATCHOUT: docs/api/internal.md documents a pre-Shutdown RPC set (StopSession, old StartSessionRequest fields); it is stale before this proposal and names no Shutdown, so the proposal makes nothing there newly wrong — EVIDENCE: docs/api/internal.md:74-150
FACT: the r7 S16/S17 merge left no dangling S17 reference in checklist, summary, spec-changes or non-spec-changes, and S16's tier and Depends lists are supersets of retired S17's — EVIDENCE: implementation-checklist.md:43-44

### [non-spec.8.review-docs-alignment.1]

- FACT: after the r7 merge of S17 into S16, no file in the proposal directory other than the review log names S17; S18, S19 and S22 depend on S16, which now carries CODE-15. EVIDENCE: implementation-checklist.md, the S16 line.
- FACT: the DOCS-2 tier-11 substrings ("no other bound session", "a session whose start the adapter has admitted", "either the bind attempt", "reclaimed", "absent") all occur in the staged `Shutdown` row, and the shipped gate asserts only four substrings today. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311
- FACT: no docs/runbooks page and no pkg/alerting rule covers slot failures or leaked slots, so no runbook companion is owed for the SPEC-6 rows. EVIDENCE: grep -rli slot docs/runbooks returns only redis-failure, token-store-unavailable, ephemeral-container-cred-guard-unavailable
- USEFUL [review-log FACT on docs/api/internal.md]: the StopSession-era page and its gRPC status table without ABORTED are pre-existing drift, and they are not an edit site for 0081.
- WATCHOUT: the non-spec Edge-cases bullet "A rolled-back start can close a successor's runtime session" has a client-visible outcome: a successor attempt's started session loses its runtime. No landing spec or docs text states that outcome, and rule 8 says only "takes the session back off the shared runtime process". This round filed it as a finding. EVIDENCE: pkg/adapter/socketruntime.go:435-467

### [non-spec.8.review-edit-sites.1]
DECISION: no findings — BECAUSE the r6->r8 diff is only the S17-into-S16 fold and the summary Watch-out pointer; no step reference outside the checklist names S17 (non-spec-changes.md:98 "S10 precedes S16", :1976 S19, :2533 S22, :4135 S13 all still resolve), and the metric identifiers re-grepped clean — ALTERNATIVES: filing summary.md:438 "CODE-13, CODE-14 and CODE-15 carry their later steps" as stale, rejected (decision record, already judged in [non-spec.7.review-single-source.1]).
FACT: no alert rule, runbook or chart names lenny_slot_failure_total, lenny_adapter_leaked_slots or the two new counters, and docs/reference/metrics.md:166 enumerates no error_type values, so the new `workspace_finalize`-class stage constant owes no docs edit — EVIDENCE: grep over pkg/alerting docs/runbooks charts; docs/reference/metrics.md:166
FACT: the superseded counter's `cause` label is consistent across SPEC-6 §16.1 row (spec-changes.md:1170), CODE-9 table (non-spec-changes.md:2067), the tier-1 case (:3348-3350) and the tier-11 metrics.md reference test (:3763-3769).
FACT: podsession's Binder has no consumer-side interface for the adapter client (only HoldCanceller, RecycleBoundaryArmer, CredentialAssigner, UserCredentialAssigner, PoolPolicyReader, observedLevelProbe), so the new Client.ShutdownReclaim needs no interface widening — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:221-346, resolve.go:224
USEFUL [non-spec.7.review-applicability.1]: confirmed the fold left no dangling S17 reference; saved a full re-walk.

### [non-spec.8.review-fresh.1]
DECISION: no findings — BECAUSE the r6->r8 diff (S17 folded into S16; summary send-before-require Watch-out reduced to a checklist pointer) leaves every step dependency satisfiable (S18, S19, S22 depend on S16, which now carries CODE-15; S16's tiers are a superset of old S17's), no file outside the review log names S17, and every staged Go call on an interface or Server/Binder receiver resolves to a tree symbol or a deliverable-declared one — ALTERNATIVES: filing summary.md:437-438 "CODE-13, CODE-14 and CODE-15 carry their later steps" as stale after the fold; rejected, it records how decision 53 was answered and states no rule.
FACT: staged calls to cl.ShutdownReclaim (CODE-7 declares it, non-spec-changes.md:1908-1914), runtimeHoldsLocked (CODE-15), lockSlotGuard (CODE-14), removeSlotTreeVia/reclaimSlotLocked (CODE-6/CODE-14), noteCompensationOutcome/compensationCause (CODE-9/CODE-13) are all declared; SlotBindRequest and ResumeRequest both carry Pool, SessionID, CleanupTimeoutSeconds and MaxConcurrentSessions. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:28-108, binder.go:603-667
USEFUL [non-spec.6.review-citations.1, non-spec.6.review-mechanism.1]: the interface-method and Server-method sweeps there matched this round's and saved re-deriving CredentialAssigner.Release.
WATCHOUT: CODE-13's sentence "a method on `Binder` rather than a free function, because ... both of which a package-level function can reach" (non-spec-changes.md:975-979) reads inverted ("cannot reach" is meant); below the bar as commentary, but a fixer touching that paragraph should correct it.

### [non-spec.8.review-kubernetes.1]
FACT: The proposal's only Kubernetes-object surface is SPEC-4's occupancy re-key and the pre-existing drain paths. `Binder.drain` deletes the claim (podclaim.DeleteClaim) and `Binder.DrainSandbox` stamps the drain-request annotation (podclaim.StampDrainRequest). No staged code writes Sandbox.status or SandboxClaim.status, and nothing gives the adapter an apiserver call. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1200-1202, slotbinder.go:601-603
FACT: SPEC-4's "the controller's own last level, which it may read back because it is the sole writer of that field" holds. Both WarmPoolController arms (occupancy and Sandbox-to-Pod) write Sandbox.status.phase under the one lenny-warm-pool-controller field manager, and the gateway holds no sandboxes/status grant. EVIDENCE: pkg/controller/warmpool/occupancy.go:150-157; spec/04_system-components.md:632
FACT: The re-keyed §4.6.1 bullets (reserved+no claim -> idle; claimed+no claim -> draining on either recycle setting) match ProjectOccupancyPhase exactly. EVIDENCE: pkg/controller/warmpool/occupancy.go:118-145

### [non-spec.8.review-mechanism.1]
FACT: after the round-7 fix S17 is gone and S16 lands CODE-1 and CODE-15 together; no reference to S17 survives in the checklist, summary, spec-changes or non-spec-changes (grep), and S16's tier and Depends lines are supersets of the retired S17's (tiers 0,1,2,4,9; S1,S2,S4). S18 depends on S16, so the tier-7a start-versus-reclaim case that reads CODE-15's `live` now has its prerequisite; the Open entry "Which step lands the tier-7a start-versus-reclaim race" is closed by the merge. — EVIDENCE: implementation-checklist.md S16 and S18 lines
FACT: `CredentialAssigner.Release(leaseID string)` is satisfied by both production implementations as CODE-13 claims. — EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:380, pkg/gateway/credentials/credassign/client.go:299; the interface at pkg/gateway/podlifecycle/podsession/binder.go:319-333 lacks it today, and CODE-13 stages the widening
WATCHOUT: the credassign package lives at pkg/gateway/credentials/credassign, not pkg/gateway/credassign.

### [non-spec.8.review-performance.1]
FACT: Round 8's only proposal change folds S17 (CODE-15) into S16; S16's tier list (0,1,2,3,4,7a,9,10) and Depends line cover every tier and dependency S17 carried, and no "S17" reference survives in the summary, non-spec, spec-changes or status files. EVIDENCE: implementation-checklist.md S16 line; grep "S17" over the proposal returns nothing outside the review log.
USEFUL [non-spec.6.review-performance.1]: the zero-write-amplification finding still holds; the merge changes landing order only, not the mechanism, so no new etcd, Postgres or Redis write and no new watch.
OPEN: the Open entry "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's live?" is now moot, because CODE-15 lands at S16 and S18 depends on S16; compaction may move it to Settled.

### [non-spec.8.review-reliability.1]
FACT: the round-7 checklist edit folded S17 (CODE-15) into S16; no staged text in non-spec-changes, summary or spec-changes still names S17, and S18 depends on S16, so the Open entry "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's live?" is resolved by the merge — EVIDENCE: implementation-checklist.md S16 and S18 lines.
FACT: `CredentialAssigner.Release(leaseID string)`, the member CODE-13 adds to the interface, is already implemented by both production types — EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:380, pkg/gateway/credentials/credassign/client.go:299; interface at pkg/gateway/podlifecycle/podsession/binder.go:319-332.
FACT: the compensation is bounded: it runs on a detached context with the slotCleanupBudget timeout, and the resume-path guard wait is bounded by the Resume handler context, because GetChunk builds its request with that context — EVIDENCE: pkg/adapter/checkpointtransport.go:99-100.
DECISION: no reliability findings this round — BECAUSE the gateway-crash residue is an accepted failure mode with a pinned test, retries are bounded by the slot-retry policy, and every added RPC carries a deadline — ALTERNATIVES: filing the stranded-entry refusal loop after a gateway crash; rejected because it is already recorded as accepted and out of scope.

### [non-spec.8.review-security.1]
FACT: the r6->r8 diff (excluding review logs) is only the S17-into-S16 fold in the checklist and the summary send-before-require Watch-out reduced to a pointer; neither touches a security control. S16's tiers (0,1,2,3,4,7a,9,10) and Depends cover the retired S17's tiers 0,1,2,4,9 and S1,S2,S4, so the tier-9 registered-but-unbound matching-reclaim arm keeps its tier. EVIDENCE: implementation-checklist.md S16 line; diff against scratchpad/cp-snap/0081-opt6/non-spec-r6-prefix
FACT: the `start.go` `slotBinder` consumer interface (pkg/gateway/sessionserver/start.go:2724-2728) also declares `ReleaseSlotReservation`; CODE-13's call-site table stages its signature change ("`start.go` `slotBinder` interface declaration | signature only"), so the widened call has a declaring interface. EVIDENCE: non-spec-changes.md CODE-13 call-site table
USEFUL [non-spec.6.review-security.1]: its FACTs (plaintext adapter gRPC pre-existing, single production ShutdownRequest literal, `leaked = err != nil || !cleanly` shipped disposition) settled the trust-boundary check without re-tracing.

### [non-spec.8.review-single-source.1]
FACT: the r7->r8 proposal text is unchanged from r7 (the diff against non-spec-r6-prefix is the same two hunks: S17 folded into S16, and the send-before-require Watch-out cut to a pointer). No file outside the review log names S17. EVIDENCE: implementation-checklist.md S16 line; summary.md:200
DECISION: filed one (g): the withdrawal grounds for the per-request pod-exclusion mechanism are written out at CODE-5's opening (non-spec-changes.md:1143-1152) and again in the summary Watch-out `SlotID == SessionID` bullet (summary.md:232-236). Home is CODE-5 — BECAUSE the deliverable that removes the mechanism owns the reasoning — ALTERNATIVES: summary as home, rejected because the Watch-out list is pointers.
FACT: the `slotBinder` interface signature change for `ReleaseSlotReservation(..., leaked bool)` is staged (CODE-13 call-site table row "`start.go` `slotBinder` interface declaration"); `s.podBinder` is a concrete `*podsession.Binder` (sessionserver.go:188), and every Server field CODE-5's resume caller passes exists (sessionserver.go:406-421). EVIDENCE: pkg/gateway/sessionserver/start.go:2727
FACT: the refusal-cost comparison ("cheaper than a destroyed session ...") appears only at summary.md:242-244, so it is a single site. EVIDENCE: grep "empty workspace"

### [non-spec.8.review-test-coverage.1]
FACT: the r7 fix folded S17 (CODE-15) into S16; S16's tier line (0,1,2,3,4,7a,9,10) covers CODE-15's former 0,1,2,4,9, and no "S17" reference survives in the summary, non-spec changes or spec changes — EVIDENCE: implementation-checklist.md S16 line; grep for S17 returns nothing outside the review log
USEFUL [non-spec.1.review-fresh.1]: the Open entry "Which step lands the tier-7a start-versus-reclaim race that reads CODE-15's live?" is now moot, because S18 depends on S16, which carries CODE-15. It can be closed.
FACT: the operator post-r16 additions have pinned tests: the superseded counter `cause` label (refusal and failure arms, exposition line with cause) and CODE-5's started-session arm (`TestHoldOrFailOnResumeErrorSlotRefusals_spec_7_3`, with a negative case for a bare FailedPrecondition) — EVIDENCE: non-spec-changes.md:3339-3352, :3428-3440
