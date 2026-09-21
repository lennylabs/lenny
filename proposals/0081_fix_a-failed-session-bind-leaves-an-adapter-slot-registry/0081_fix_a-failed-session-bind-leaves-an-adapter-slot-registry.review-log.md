# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass 30, 2026-09-21). Read the whole ledger, which this pass found far larger
than any it followed: the two spec rechecks (spec-recheck.1 through .4 and spec-recheck-2.1), the two
non-spec rechecks (non-spec-recheck.1 and non-spec-recheck-2.1), the `running`-boundary redesign
`[redesign.6.fix.1]`, spec round 23's twelve lenses, `[index-reconcile.1]`, four `[f1.human-decisions]`
firings, `[f1.cleanup]` and `[spec-recheck.5.review-single-source.1]`. Lifted 44 Settled lines, 21 Traps,
14 Open entries and the carried-forward Deferred list. Honoured four `CORRECTS`: the reclaim-hold trap's
"(a `Checkpoint` resolve is refused)" parenthetical is rewritten to the narrowed scope; the ledger claim
that rule 2 reaches the §7.4 upload through the `**Admission.**` preamble's closing clause is corrected
(a mid-session upload is one of the seven enumerated RPCs); the claim that `slotstate.go:100` was already
staged by CODE-3 is corrected and then discharged by the widened CODE-3; the checklist-S7 "already fixed"
claim is corrected and then closed by `[index-reconcile.1]`. Retired 22 of the 30 Deferred entries, all
discharged by `[index-reconcile.1]` or by the staging, plus seven Open entries closed by open decisions
35, 38, 39 and 40 and by `[spec-recheck.4.review-edit-sites.1]`. Deleted no trap. Recorded two
DISAGREEMENTS as `UNVERIFIED` rather than picking a winner in the text: whether SCHEMA-1 already stages
the `SESSION_SCRUB_OUTCOME_LEAKED` comment, and whether `troubleshooting.md:41`'s
`reason: setup_command_failed` is the REST envelope reason or a warmup-failure label. Did NOT reach the
977-line target and the section is longer than pass 29 left it. Nothing that mattered was dropped to make
the number: this window is the largest single delta the log has carried, its four human decisions each
retire a question rather than a fact, and the two recheck lanes each re-derived a tree inventory (the
bind-field sweep, the retired-`leaked`-trigger carriers, the `shutdownReclaimOutcome` arms) that a later
round would otherwise pay for again.

Prior pass (29, 2026-09-21). Read the whole ledger. The delta this pass owed was the
whole of spec round 22: its twelve lenses (applicability, citations, client-surface, docs-alignment,
edit-sites, fresh, kubernetes, mechanism, performance, reliability, security, single-source), every
one of which returned an EMPTY findings list. Lifted 33 Settled lines, 6 Traps, 2 Open entries and 1
Deferred entry. The round filed no `CORRECTS`, so no standing claim was rewritten on that account.
One DISAGREEMENT was settled by keeping the newer entry: the round-19 Settled line saying the
whole-file sentence-repeat sweep "returns exactly ONE repeat over 90 normalized characters" is
superseded by round 22's sweep, which returns six over four files; the older line is in `### Retired`
with a note, and an `UNVERIFIED` records that the two sweeps had different file scopes. Retired no
Open entry, because an empty round closes none. Deleted no trap and dropped no `OPEN`, `UNVERIFIED`
or `DEFERRED`. Did NOT reach the 704-line target; this section is again longer than the last pass
left it, because a round of twelve empty lenses is almost pure durable residue: each empty verdict
carries the gate facts, anchor re-verifications and refuted dresses that bought it, and dropping them
would have a later round re-derive the twelve sweeps that produced nothing. Nothing that mattered was
dropped to make the number.

Prior pass (28). Lifted `[f1.cleanup.3]`, `[redesign.5.fix.1]` and `[spec.21.review-reliability.1]`
plus the residue of spec rounds 12 through 20 that pass 27 left behind: 6 Settled lines, 4 Traps and
1 Open entry. It honoured no live `CORRECTS`, retired nothing, and reached no target.

Prior pass (27). Lifted spec rounds 12 through 20 plus `[prune.3.fix.1]`, `[prune.4.fix.1]`,
`[f1.cleanup.1]` and `[f1.cleanup.2]`: 28 Settled lines, 14 Traps, 12 Open entries and 4 Deferred
entries. Honoured three `CORRECTS`, retired two Open entries the staging closed (§16.1.1's "Used on"
column; §6.2's projection preamble enumeration) and narrowed one (the per-runtime `Runtime.Close`
question, closed for the new-behaviour reading and open only as pre-existing). It reached no target
and said so.

Prior pass (26). Lifted `[redesign.2.fix.1]`'s carrier-table decision and carrier-sweep WATCHOUT, the
two `f1.open-decisions` blocks, and the four round-11 lenses: 21 Settled lines, 5 Traps, 1 Open entry.
It reached no target either and said so.

Prior pass (25). Lifted the durable residue of spec rounds 2 through 10 plus the two `f1.open-decisions`
passes and `prune.1.fix.1`: 33 Settled lines, 18 Traps, 13 Open entries, 9 Deferred entries. Retired two
Open entries a `CORRECTS` closed (whether §15.4 states a rule of its own; whether its conformance
criterion over-reaches) and two Deferred entries the staging discharged (SCHEMA-1's two proto
report-trigger sentences, DOCS-2's `adapter-contract.md` `ReportSessionScrub` row).

A hard compaction on 2026-09-20 moved the prior standing context whole, all 2,308 lines, into
`0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md` under the heading
`## Standing context as of 2026-09-20, before hard compaction`.
A claim absent from this section is one this log no longer carries. Recover an old entry from the archive by its bold subject.

How to use this section. Entries are cited by their bold subject. The current staging is: a caller-minted
per-attempt `bind_attempt` token stamped once in `ensureSlotStateLocked` under `s.mu`;
`unconditional_teardown`; `SLOT_BIND_ATTEMPT_SUPERSEDED` and `SLOT_BIND_ALREADY_STARTED`; §4.7.1 rules
1-9 (admission) and 10-15 (`Shutdown`), stated once; the §4.7.1 registry critical-section paragraph as
the one atomicity home; the §5.2 disposition table as the one home of every cleanup's report, `leaked`
and hold disposition, with the residue a cleanup leaves and what ends it stated once in the paragraph
after the table and in no column (`[prune.3.fix.1]`); Design as a
choice record; accepted failure modes in the spec file's Edge-cases section only; CONF-1 with one case
per numbered rule plus one rule-5-before-rule-6 ordering case. An archive entry that mentions a bind
epoch, `expected_bind_epoch`, an epoch latch, `ExcludePod(s)`, a per-rule §15.4 table, or lettered
CONF-1 clauses describes a withdrawn design: its tree facts may survive and its design claims do not.
First command of any round: `diff -rq` across the snapshot chain; when snapshots are byte-identical the
delta is `git diff HEAD -- proposals/0081_*/`. Line numbers into the proposal files are not recorded here.

### Settled

- **DECISION: the disposition of every per-slot cleanup is ONE TABLE, in the staged §5.2 `**Scrub model.**` append.** Rows key on what is reclaimed, who performs it, which act fails; other sites cite it. Rejected: §6.2 two-exit home.
- **DECISION: where the old sites disagreed, the table takes these answers.** A failed cleanup outside a `Shutdown` is not `leaked`; rows key on the failing act, never "completed"; the whole-pod scrub ends directories only; §15.4 cites §5.2.
- **DECISION: the table's two pre-`running` rows are keyed "reclaimed by a `Shutdown`", not "reclaimed by the pod-side reclaim".** The §7.1 reclaim is a `Shutdown`; rules 12 and 14 both answer `reclaimed`. Rejected: a third row pair.
- **DECISION: the adapter's atomicity is ONE paragraph, `**The registry critical section.**`, in the staged §4.7.1 block.** Membership reads "rules 2 through 7" because rule 1 (`validateBindFields`) runs before `s.mu`. Rejected: a sixteenth numbered rule; per-rule atomicity clauses.
- **DECISION: `## Design (as the spec must state it)` is a choice record,** one paragraph per choice naming ground and owning staged block. Deleting it was rejected: the over/under-approximation grounds and the two-field rationale live nowhere else.
- **DESIGN GENERATION: the consolidation commit `fcac6a13c`.** It made §4.7.1 one numbered list, rules 1-15; every other site reaches a rule by number. `git show fcac6a13c` on spec-changes.md is the highest-yield command on this proposal.
- **DECISION: §15.4's fifteen-row per-rule wire table is DELETED.** §15.4 is a pointer at §4.7.1 plus the conformance criterion; the hold block still publishes `ABORTED`. Rejected: §15.4 owning status/ErrorCode, since rule 5's ordering argument needs its category.
- **DECISION: §15.4's conformance criterion quantifies over REQUESTS and states no rule-selection predicate.** §4.7.1's cascades are total and first-match, so one behaviour exists per request. Rejected: "earliest-numbered rule" wording; deleting the criterion; a rule 5/6 exception clause.
- **§4.7.1's two cascades are TOTAL, and that totality is what the §15.4 criterion rests on.** Rule 7 admits any other request; rules 11-14 exhaust `Shutdown`; rules 8, 9 sit outside. A partial cascade voids the criterion.
- **DECISION: the §4.7.1 "Which fields each request carries" table is the single home of `bind_attempt`/`mid_session` carriage.** The closure over unlisted requests sits on the table's lead-in; the prose keeps rationale only.
- **DECISION: the stamp contract is PARTITIONED between rule 4 and the stamp-once paragraph, and neither proposition has two homes.** Rule 4 owns the write and its condition; stamp-once owns exclusivity, immutability, ownership.
- **Rule 4 produces a FRESH ENTRY stamped with the attempt's EXISTING token; nothing on the demotion path produces a token.** Demotion and the following pod-warm bind are one bind attempt; "a fresh token" wording is a defect.
- **DECISION: the preamble's universal "No rule is scoped by the name of an RPC." is DELETED and the condition sentence widened.** Rules 1, 6 and 9 name RPCs. Rule 8's "conditioned on what the request does" stays.
- **DECISION: the closed entry-creating-RPC list lives in §4.7.1's `**Admission.**` preamble and §5.2 cites it.** Rule 2 delegates the hold's scope to §5.2 while §5.2 takes the request set from §4.7.1; different facts crossing, not a citation loop.
- **The seven entry-creating RPCs §4.7.1 enumerates are exactly the tree's set, with no eighth.** `ensureSlotStateLocked` has three callers (`ensureSlotPaths`, `assignCredentialsSlot`, `claimSessionSlotUnderLock`); `RotateCredentials` and `ExportPaths` create nothing. Re-derived by many shards; do not repeat.
- **DECISION: the clean-exit gap is closed by ONE clause on §4.7.1's reclaim-outcome rule.** `absent` and `superseded` report a clean exit; only `reclaimed` can report unclean. It is the corpus's first definition of `exited_cleanly`.
- **DECISION (open decision 23, RESOLVED): §7.1's leak predicate keeps the WIDE form,** "whatever outcome that answer carries". It fails closed against a non-conforming adapter and lets CODE-4 read only the RPC error and the clean-exit flag.
- **DECISION: §4.7.1's attempt-mismatch rule is narrowed to "No `Shutdown` naming a bind attempt removes such an entry; within this cascade only the unconditional-teardown rule does."** `DemoteSDK`, `ConfigureWorkspace` rollbacks and the hold-timeout pass also remove untokened entries.
- **The refusal a later bind meets at an UNTOKENED entry is the STARTED-SESSION rule, never the attempt identity rule.** Rule 5 requires the resolved entry to carry a non-empty token.
- **DECISION: the §4.7 `Shutdown` row's slot-release predicate is "whenever the request REMOVES an entry", not "whenever the adapter HOLDS an entry".** Rule 13 holds an entry and performs neither teardown.
- **DECISION: SPEC-1 gains a §4.7 `DemoteSDK` row, and it is the single spec home of the demotion's registry removal.** Conditional, no completion guarantee; DOCS-2 mirrors it. Rejected: stating it in rules 1-15, in §6.1/§15.4, or as SPEC-7.
- **`Server.DemoteSDK` returns `codes.Internal` from `sw.DemoteSDK` BEFORE it deregisters anything** (`pkg/adapter/sdkwarm.go`), so a failed demotion close opens no hold and the entry stands; the table has its own row. No CODE reorder is staged.
- **`DemoteSDK`'s three call paths, and which one the shipped gateway takes.** In `Binder.Prepare` it runs before any entry exists; the `ConfigureWorkspace`-failure fallback is specified but unimplemented (pre-existing); SIGTERM `ShutdownDemoteSDK` is third. The row is reachable.
- **A pre-`running` slot can be released by the demotion or the hold-timeout pass, not only by a `Shutdown`.** `deregisterStartedSessions` selects on `st.started`, set before `Runtime.Start`; `DemoteSDK` releases `anyRegisteredSession()` unfiltered.
- **The hold-timeout pass deregisters in PASS 1** (`deregisterStartedSessions` in `onHoldTimeout`, holdstate.go) and closes in `terminateHeldSession`; only this path deregisters before closing. It does not terminate the pod, and `s.hold.active` clears before pass 1.
- **`terminateHeldSession` calls `s.noteRuntimeClosed` unconditionally,** so `runtimeLive` believes the session gone even after a failed close. The reclaim hold, not `runtimeLive`, carries the failed-close case.
- **DECISION: the reclaim hold ends only on a cleanup that COMPLETED, and otherwise holds for the life of the pod.** Code: a `completed` flag gates the deferred `release()`. Rejected: an "unreclaimed" marker, a permanent status, a gauge.
- **The completion predicate differs by site, deliberately, and all three are stated.** `Shutdown`: `closeErr == nil && treeErr == nil`; `releaseSessionSlot`: tree removal alone (it closes no runtime); `terminateHeldSession`: both.
- **DECISION: CODE-1's cleanup-outcome report stays keyed on `closeErr` ALONE.** The `errors.Join(closeErr, treeErr)` re-key was reverted: it retires a pod on one failed `os.RemoveAll`. A tree failure logs `slot_tree_removal_failed`; report `released`, hold held (table row three).
- **"Completed" and "reached `released`" are INDEPENDENT predicates across the three arms that traverse `slot_cleanup ──→ released`.** Runtime-given reports `released`; pre-`running` reports nothing; clean-close/tree-fail reports `released` without completing. Any trigger or test equating them is wrong.
- **DECISION (`[redesign.5.fix.1]`): the cleanup has TWO completion terms, each defined once.** `completed` is the pod-side predicate (every act the cleanup owes the slot returned without error), defined in SPEC-3's §5.2 `**Slot-identifier reclaim hold.**` paragraph, and it decides the hold column. `acknowledged clean` is the gateway-side predicate (the adapter answered and the answer reports a clean exit, `superseded` and `absent` included), defined in SPEC-2's §7.1 paragraph, and it decides `leaked` together with the report. Table row 3 (close succeeds, another act fails) is acknowledged clean and not completed. The gateway-side predicate is never written as "completed".
- **DECISION: the `slot_cleanup ──→ released` fence entry carries a BARE POINTER and no trigger,** `(see §5.2)`; the `leaked` entry likewise. No short trigger is true of every traversal. Precedent: warm-fill `sdk_connecting ──→ failed … see §6.1` (spec/06:90).
- **`slotlayout.RemoveTree` is best-effort across four directories** and returns the first error, so no caller learns which removal failed; spec text distinguishing them is unimplementable. `EnsureTree` is idempotent, so a successor materializes INTO a residue.
- **`deregisterSlotLocked` cannot fail,** so deregistration opens the hold and is not an "owed act" in the completion predicate; do not prepend it to §5.2's action list. It is the sole `delete(s.slots, …)`.
- **`pkg/adapter/podscrub.go` is purely on-disk.** It enumerates `/workspace/slots` and `/run/lenny/slots` and touches neither `s.slots` nor §4.9 timers; the adapter process survives pod reuse. `startPodScrub` is asynchronous.
- **The agent pod's `RestartPolicy` is `Never`,** so an adapter crash ends the pod; "the in-memory hold cannot last the life of the pod" is refuted.
- **The §5.2 "graceful window of ten seconds" for the §10.1 hold-timeout termination is the shipped constant** in `onHoldTimeout`, one context shared across pass 2; §10.1 states no figure. CODE-6 re-scopes it: guard-acquisition deadline plus per-member close context.
- **`releaseSessionSlot` reports nothing, calls NO `Runtime.Close`, and `reportSessionScrub` has exactly ONE caller,** the `Shutdown` handler. SPEC-3's report biconditional records shipped behaviour and reduces the Postgres write rate.
- **DECISION: the §4.7 `ReportSessionScrub` row's trigger clause is re-keyed into a citation of §5.2 and its `sessionsServed` clause onto the report,** under SPEC-3. The addressing sentence stays verbatim on one physical line; a tier-11 gate reads it.
- **The production adapter-`Shutdown` caller set is FIVE sites behind ONE builder** (`Client.shutdown`, adapterclient/client.go); CODE-4 sets `unconditional_teardown` there, so no caller edit is owed and the §11.4 revoke cannot hit rule 10. No coordinator-handoff `Shutdown` exists.
- **DECISION: CODE-1's handler has ONE exit, `answerShutdown(outcome, exitedCleanly, untokened)`,** which runs the recycle scrub; the shipped trailing scrub clause is deleted or `ReportPodScrub` double-sends. Only the two `INVALID_ARGUMENT` returns bypass it.
- **The `Shutdown` two-field precondition is a wire-visible break on every in-tree test literal, and the compiler cannot see it.** Sweep by `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/` (~64 sites, 26 files); it belongs to the CODE-1 step.
- **DECISION: the per-slot guard's scope is a PREDICATE plus a derivation table, never an RPC enumeration.** Path work outside `s.mu` takes the slot guard before the resolve: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `Resume`, and the destructive sections.
- **DECISION: `Shutdown` compares under `s.mu` FIRST, acquires the guard only on the arm that destroys, and RE-DECIDES under `s.mu` afterwards.** Non-removing arms never wait on unbounded guard holders; the re-decision catches hold-timeout pass-1 removal and answers `absent`.
- **DECISION: `releaseSessionSlot` splits into a guard-acquiring form and `releaseSessionSlotUnderGuard`,** because `Resume` holds the non-re-entrant guard across its seven rollback sites and would self-deadlock. It gains a `ctx` parameter; `ReleaseSlotForTest` too.
- **DECISION: CODE-8 is a closure SIGNATURE change, `reclaim := func(cause error)`, plus an enumerated call-site sweep** in `Prepare` and `Launch`. `Launch`'s only reachable refusal is `SLOT_BIND_ALREADY_STARTED`; no credential release may sit on the refusal arm.
- **DECISION: CODE-4's attempt-scoped credential release is extended to `Binder.Prepare`'s credential-assignment failure arm.** Leases are minted before the RPC on both paths; `credassign.Service.Release` no-ops on unknown ids. Rejected: session-wide `ReleaseSession` (strips a live session's leases).
- **The no-entry compensation case is grounded on a `stageWorkspace` FAILURE PATH, never on an upload-free plan.** All five error returns precede the `PrepareWorkspace` send; inject an `uploadFile` source with `Binder.Blobs` nil. A `FinalizeWorkspace` failure leaves an entry.
- **`mid_session` is SHIPPED on `FinalizeWorkspaceRequest` (field 4) and ABSENT from `PrepareWorkspaceRequest`.** SCHEMA-1 adds it to `PrepareWorkspace` alone; the §4.7.1 table marking both "carried" agrees. Twenty-plus lenses read this backwards. `ShutdownRequest` fields 7 and 8 are free.
- **The metric-gate topology, settled and re-derived by six shards.** The adapter series is gated against §16.1 and metrics.md by `adapter_metric_catalog_test.go` and must not enter `spec161Metrics`; the gateway series has no docs gate. SPEC-6 must land before CODE-9.
- **The three deregister-then-destroy sites are exactly three, and `ShutdownDemoteSDK` is not a fourth.** `Shutdown`, `releaseSessionSlot` (via `deregisterSlot`) and `deregisterStartedSessions` (hold-timeout pass); `ShutdownDemoteSDK` delegates to `DemoteSDK`, whose release is `releaseSessionSlot`.
- **`claimSessionSlot`'s `idempotentRepeat=true` has exactly ONE production caller,** `ConfigureWorkspace` (sdkwarm.go); `StartSession` and `Resume` pass false, so rule 6's repeat exemption is equivalent to the code. Do not file it as predicate drift.
- **The reclaim hold cannot refuse a mandatory credential control.** Revoke, rotate, extend and `CoordinatorFence` resolve through read-only `slotStateLocked`/`boundSlotState`, never `ensureSlotStateLocked`. Moving one onto `ensureSlotStateLocked` would make this a fail-open; re-check then.
- **There is NO per-session missing-report timeout.** Every missing-report timer is the §5.2 whole-pod recycle-boundary timer (recycleboundary.go), so withholding `ReportSessionScrub` on the pre-`running` path leaves no timer to fire.
- **`PodExecutor.Release` calls `registry.Remove` BEFORE `binder.ReleaseSlot`** (session/executor/pod.go), the ordering the tier-7a upload arm's narrow window and the accepted "upload after pruning reaches no adapter" statement rest on.
- **The SCHEMA-1 field numbers under the token design, verified free by many independent lenses.** SCHEMA-1's staged table is the record; `ErrorCode` 28 and 29 are next after 27; `ShutdownResponse` gains `slot_reclaim` = 3 only. Do not re-derive.
- **`ShutdownResponse.slot_was_started` was minted and then DELETED from the whole staging,** with its only consumer metric; the clean-exit flag is computed from the close and tree errors instead.
- **Two adapter `ErrorCode` values are minted, 28 and 29, and they take NO §15.1 row.** §15.1 catalogs the client-facing `code`; the gateway renders no adapter `ErrorCode` into REST. Precedent: `PROTOCOL_VERSION_INCOMPATIBLE` = 27 has no catalog row anywhere.
- **Field well-formedness is `validateBindFields(bindAttempt string, midSession bool) error` in `pkg/adapter/slot.go`,** returning bare `codes.InvalidArgument` at handler entry. It must NOT route through `slotResolveError`, which would hand `SlotBindError.Reason()` a workspace-validation reason.
- **`StartSession` and `ConfigureWorkspace` both CREATE the entry when none exists,** via `claimSessionSlot` → `ensureSlotStateLocked`, so either can create an untokened entry: rule 13 answers `superseded`; only rule 12, demotion, hold-timeout or pod retirement removes it.
- **`FinalizeWorkspace` is UNCONDITIONAL on both bind paths and precedes the start,** so on a correct ordering a token-carrying RPC always creates the entry; the untokened entry needs an abandoned or late start. `PrepareWorkspace` is conditional (uploads only).
- **`SLOT_BIND_ALREADY_STARTED` on `RunSetup` is reachable:** B's entry, abandoned A's tokenless late `StartSession` starts on it, B's `RunSetup` matches the token and meets rule 6. Do not dismiss the setup-window envelope mapping as unreachable.
- **`writeSetupCommandError` branches on the gRPC code ALONE.** `FailedPrecondition` gives 422 `SETUP_COMMAND_FAILED`, anything else (`Aborted` included) the retryable 503 fallback. `SetupCommandFailure` is constructed only at the two `RunSetup` calls, so the "setup window" is that RPC alone.
- **DECISION: §15.1's `SETUP_COMMAND_FAILED` row is WIDENED rather than the gateway's envelope selection narrowed;** narrowing the client code is outside this proposal. DOCS-3 mirrors the row's replacements in `error-catalog.md`, adds no row, and never names the adapter codes.
- **§5.2's retry placement prefers the SAME pod.** `applySlotRetryPolicy` re-submits the identical request with no exclusion and `maxSlotRetries == 1`, so a same-pod refusal is the request's last attempt. No placement rule is staged; `**Max retries:**` stands untouched.
- **DECISION: §7.1's reclaim obligation is scoped by whether the pod survives the failure, rather than by a list of call sites.** `Binder.Prepare` and `Binder.Launch` drain through `failPhase` and send no compensation; the exclusive start discharges by retirement.
- **DECISION: SPEC-2's §7.1 atomicity-parenthetical edit is DELETED and the shipped parenthetical stands.** Session creation leaves no pod-side state: `finalize.go` returns before `podBinder.Prepare` on service mode or `MaxConcurrentSessions > 1`; exclusive `Prepare` failure drains the pod.
- **`Binder.Prepare` runs at `/finalize` and `Binder.Launch` at `/start`, two independent HTTP requests, with nothing carrying in-process state between them.** `Launch` re-dials and cannot hold `Prepare`'s token, which is why the two starts carry none.
- **`Binder.failPhase` is `releaseCredentials` plus `drain` and issues no adapter RPC;** `drain` is a bare `podclaim.DeleteClaim` writing no claim disposition. Cleanups outside `Shutdown` run only on start paths and the hold-timeout pass; no workspace/setup/credential handler releases a slot.
- **The occupancy projection reads the pod's CURRENT PHASE, never the recycle setting.** `ProjectOccupancyPhase`: `Claimed` with no claim projects `Draining`, `Reserved` projects `Idle`. No test pins the old `recycle.enabled: false` clause, so SPEC-4 costs docs only.
- **The graceful-shutdown-signal gate is shipped verbatim,** `if !boundRemains { s.drainViaLifecycle(...) }` in pkg/adapter/session.go, so the §4.7 row's and §29.4 step 13's statement of it must not be filed as new behaviour.
- **The adapter DOES hold its own pod identity,** `POD_NAME` from the Downward API cached as `s.podID`, so the adapter-side `k8s_pod_name` label is emittable. The proto comment "holds no pod identity" concerns the wire-delivered `RecycleScrub.pod_id`.
- **Both gateway leak ledgers share ONE `slothealth.Tracker` instance** (minted in `cmd/lenny-gateway/sessiondeps.go`), so "the withheld report lands in a different ledger" dies here. Only the gauge differs: the bind-failure path alone publishes it.
- **The adapter today refuses a double start with `codes.Unavailable`, not `FailedPrecondition`,** so rule 6's `FAILED_PRECONDITION`/`CATEGORY_PERMANENT` answer is a deliberate retryability change on that path rather than shipped behaviour restated.
- **A grace-expired close is reported as CLEAN.** `SocketRuntimeProcess.Close` and `MCPRuntime.Close` return nil on the kill arm; only a natural non-zero exit or a listener-close failure yields `closeErr`. A shorter window cannot manufacture `leaked`.
- **The adapter enforces NO per-slot cleanup timeout of its own.** Only the whole-pod scrub's `CleanupTimeout` exists in `pkg/adapter`, and `resolveShutdownGrace` applies no floor, so §5.2's "minimum 5s enforced by the adapter" has no implementation. Pre-existing.
- **`removeSlotTree` takes NO `context.Context` on either cleanup path.** It runs after the grace-bounded `Runtime.Close`, and the `Shutdown` handler blocks on it before answering, so no layer bounds the directory removal and the hold outlasts the close.
- **The reclaim gate has exactly THREE production entry points.** `Binder.BindSlot` (through `applySlotRetryPolicy`), `Binder.BindReservedSlot` and `Binder.Resume`; `bindReservedSlot` funnels into `materializeSlot`, so one wrapper covers both bind paths. Do not re-derive "is a bind path missed".
- **`live && !started` is unreachable, and so is `live ⊄ started`.** `runtimeLive` is written only by `noteRuntimeStartedLocked`, after `claimSessionSlotUnderLock` set `st.started`, which is never cleared. That makes CODE-1's `live` report gate safe in both directions.
- **`deregisterSlotLocked` returns a NIL `*slotState` when `removed` is false.** CODE-1's `started := removed && st.started` and `live := removed && s.runtimeHoldsLocked(sessionID)` are safe only through `&&` short-circuit; keep `removed` first.
- **CODE-1's staged clause two preserves the shipped ORDER exactly for a started session.** emitFinalUsage, drain, Close, noteRuntimeClosed, removeSlotTree, cancelPodMCPIfRuntimeIdle, reportSessionScrub; `started ⊆ removed` and `live ⊆ removed`. An ordering finding stops here.
- **`codes.Aborted` collides with nothing on this surface.** In `pkg/` only `checkpoint.go` and `oplock.go` use it; no interceptor or retry policy treats a status specially, and the gateway's shipped start.go comment already classes `Aborted` as transient.
- **The two surviving "create or resolve a registry entry" phrases are the RECLAIM HOLD's admission predicate.** Deliberately wider than the seven bind RPCs (`Attach`, `Interrupt`, etc.); narrowing them opens the hold mid-cleanup. Do not unify the two predicates.
- **Retryability on the bind path is derived from the gRPC STATUS CODE alone.** `InvalidArgument` and `FailedPrecondition`/`PermissionDenied` classify non-retryable; everything else, `Aborted` at every stage included, defaults transient. Nothing on the slot-bind path reads the proto `Category`.
- **The adapter has THREE shipped bind-path refusals that are not the reclaim hold.** Pod-not-idle and already-started (`codes.Unavailable`) and the unresolvable-slot `InvalidArgument`. DECISION: §5.2's "only refusal" sentence was deleted rather than importing a partial list.
- **The hold is keyed on the slot identifier, which IS the session identifier.** N concurrent cleanups refuse N distinct identifiers and never each other. This kills most availability findings against the hold.
- **The whole-pod scrub is a RECYCLING-pod mechanism.** A scrub-bounds-residue claim owes the non-recycling half, where the pod retires at the occupancy-zero boundary; spec/06's recycle boundary already waits for all slot cleanup to finish.
- **§7.2 orders the step-3 reclaim BEFORE step 4's `coordination_generation` bump.** The compensating `Shutdown` carries the generation the pod still holds. `adapterclient` sets the generation only on `CheckpointBarrier`, and only `CheckpointBarrier`/`CoordinatorFence` validate it.
- **DECISION: the §7.3 resume classification gap is closed by ONE arm in `isTransientPodClaimError`, owned by CODE-5.** `codes.Aborted` returns true; its one caller is `holdOrFailOnResumeError`. `status.Code` walks the wrap chain; the adapter-local sentinel is unexported, so no `errors.Is`.
- **`resumeOnPod`'s two branches are mutually exclusive.** The snapshotless rebuild accounts through `applySlotRetryPolicy`; the checkpoint-restore branch calls `podBinder.Resume` with no retry loop, so CODE-5's `accountSlotFailure` caller there cannot double-account. `s.podBinder` already satisfies `slotBinder`.
- **The compensation is synchronous inside `materializeSlot`.** `applySlotRetryPolicy` re-enters `BindSlot` only after it returned, so the gateway's own retry never races its own reclaim; every racing retry is a fresh client request or a second replica.
- **`applySlotRetryPolicy`'s exhausted return is `SlotFailedError`, never an exhaustion sentinel.** `runWithQueue` re-enters only on `ErrNoIdlePod`, `ErrNoConcurrentSlot` or `ErrTenantMismatch`, so a refusal ends the request on every pool mode, `queue` included.
- **The shipped tier-3 CLOSED field-set gate is `TestShutdownMessagePostRemovalDescriptor_spec_4_1`.** It pins `ShutdownRequest` and `ShutdownResponse` field numbers, so SCHEMA-1's new fields turn it red until edited. `tests/claim-map.json` is generator output held byte-identical by tier 0.
- **Adapter predicate nesting.** Entry present (`s.slots[id]`) ⊃ bound (`st.sessionID` set, by `assignCredentialsSlot` and `claimSessionSlotUnderLock`) ⊃ started (`st.started`, set only in `claimSessionSlotUnderLock`) ⊃ `runtimeLive` (set only by `noteRuntimeStarted`).
- **`st.started` precedes `Runtime.Start`, and it has THREE setters.** `claimSessionSlotUnderLock` serves `StartSession`, `Resume` and SDK-warm `ConfigureWorkspace`; the gap before `noteRuntimeStarted` is wide. Never name one RPC in a start predicate.
- **The `running` boundary has ONE home and ONE vocabulary (`[redesign.6.fix.1]`).** The home is the record step of the staged §4.7.1 registry critical-section paragraph ("the record is the adapter's recording of the pod's shared runtime process as holding the session, and the slot reaches `running` at that record"); every other site says `running` / pre-`running` and cites it. "given the session", "was given", "runtime is given", "has been given" and "reaches the runtime" are grep-banned across the five proposal files and the grep returns nothing; rule 8's rollback clause "takes the session back off the shared runtime process" names the undo rather than the boundary and stands.
- **`runtimeLive` is a pod-level cohort.** Its only writers are `noteRuntimeStartedLocked` and `noteRuntimeClosed`; a started co-tenant keeps it non-empty, and `deregisterSlotLocked` does not touch it, so `live` is computable after deregistration under the same lock.
- **DECISION: two predicates, not one.** Runtime teardown gates on `st.started` (fails closed toward closing); the cleanup-outcome report gates on `runtimeLive` through `runtimeHoldsLocked` (fails closed toward not counting). Rejected: one shared predicate.
- **DECISION: the teardown precondition names no RPC and is stated in one place.** The §4.7 `Shutdown` row is the home; §4.1 points at it. "Claim" is avoided because it denotes the `SandboxClaim` CRD.
- **`RecordSessionScrub` has no per-session dedup.** It increments `sessions_served` unconditionally and also feeds the leak ledger, so two reports are two served sessions, and withholding the report withholds the leak signal too. The adapter alone holds at-most-one.
- **DECISION: the refused `StartSession` reports no outcome.** The start-confirmation rollback files no `ReportSessionScrub`, on the `Resume` arm as well; the slot never reached `running`, and the reclaiming cleanup files the one report.
- **DECISION: CODE-2's rollback drops `releaseSessionSlot` and calls `cancelPodMCPIfRuntimeIdle()` directly, on both the `StartSession` and the `Resume` arm.** The entry found may be a successor's; releasing it would delete that entry and its tree.
- **§5.2's `**Slot cleanup:**` bullet is concurrency-scoped.** It sits under a `maxConcurrentSessions > 1` heading, as does the slot retry policy block, so a rule holding at either concurrency belongs in the `**Scrub model.**` paragraph.
- **§6.2's per-slot fence is two blocks, and the split is load-bearing.** `running → failed` and `slot_cleanup → leaked` are scoped to concurrent occupancy; a tier-11 gate pins that. Do not move edges between blocks.
- **The per-slot sub-state machine is a contract model.** Production calls only `MarkLeaked`, `MarkReleased` and `ForgetPod`, never `Registry.Transition`; `MarkLeaked` seeds untracked slots without consulting the edge list. `{Running, SlotCleanup}` already exists, so CODE-3 adds one edge.
- **`SlotID == SessionID` on every path.** Stated in §5.2 and pinned by a tier-11 glossary gate; a same-pod retry reuses the registry key and on-disk tree, hence the identifier hold.
- **`SlotClaimer.ReleaseSlot(leaked=true)` returns early.** It performs no Redis decrement, claim DELETE or recycle patch, so releasing the reservation and a leaked slot holding occupancy are compatible. `ReleaseSlotReservation` hard-codes `recycle=false`.
- **The leak disposition is keyed on the gateway's acknowledgement.** `leaked = err != nil || !cleanly` on every bind path; the gateway, not the adapter, emits `lenny_adapter_leaked_slots` and calls `MarkLeaked`. The ledger is replica-local in-process state.
- **`UnhealthyThreshold` is `(maxConcurrent+1)/2`, which is 1 at concurrency 1 AND at 2.** One windowed failure drains the pod at concurrency 2, so persistent-leak churn differs only from 3 upward; concurrency-1 arguments concern §5.2's heading scope.
- **`Shutdown` is unfenced.** The adapter validates `coordination_generation` only in `CoordinatorFence` and `CheckpointBarrier`, and `Shutdown` has no `checkSessionBound` guard or context-expiry check, so a late reclaim executes destructively unless the token refuses it.
- **`Shutdown`'s entry removal and its tree removal are two moments.** `deregisterSlotLocked` runs under `s.mu`; `removeSlotTree` runs afterwards outside it, before the response. The reclaim hold covers that gap.
- **The occupancy-zero concurrent release sends TWO RPCs.** `Binder.ReleaseSlot` sends the per-slot `Shutdown`, then a recycle `Shutdown` reusing the released session id, which meets no entry; the whole-pod scrub must therefore run on every outcome, `absent` included.
- **`stageWorkspace` sends `PrepareWorkspace` only under `if len(uploads) > 0`.** An upload-free plan reaches `FinalizeWorkspace` as its first adapter RPC, so any of the token-carrying requests can be an attempt's first entry-creating RPC.
- **The registered-but-unbound successor state is the ORDINARY state, not a rare interleaving.** `ensureSlotPaths` creates an entry with empty `sessionID` on every workspace RPC, so a retry's staging creates exactly the entry a lagging start or reclaim meets.
- **`SocketRuntimeProcess.Start` has no acknowledgement step.** It returns nil immediately when connected, with no ctx check, and releases `p.mu` before accepting, so both orderings of the reclaim-versus-start race are reachable. `Runtime.Close` ignores cancellation.
- **The three attempt kinds do not map onto three code paths, and `bindConcurrentSlot`'s reserved branch is a CONJUNCTION.** Only a non-recovery row with a `PodAssignment` takes `BindReservedSlot`, which re-runs `materializeSlot` on the same pod and tree, unretried.
- **`applySlotRetryPolicy` wraps `binder.BindSlot` only.** `maxSlotRetries` is a hard-coded const of 1 in `start.go`, so one invocation is two attempts; `BindReservedSlot` and `ClaimSlot` never traverse it, and a `queue` re-entry ends in exhaustion.
- **The slot routes are concurrency-gated end to end.** Every slot path sits behind `MaxConcurrentSessions > 1`; `prepareAtFinalize` returns nil there, so a concurrent pool has no `ready` gap; the exclusive path uses `failPhase`, which sends no `Shutdown`.
- **`Binder.Resume`'s failure branch releases nothing on an exclusive pool.** `reserveResumeSlot` returns `""` at `MaxConcurrentSessions <= 1`; on a concurrent pool it increments Redis. Only the checkpoint-restore re-attach lacked accounting; say that, never "the resume path".
- **DECISION: `accountSlotFailure` gains a THIRD caller.** It serves the retry path, the `BindReservedSlot` branch and `resumeOnPod`'s failure branch; it is a pre-`running` leak's only route to the §5.2 trigger, because the report is withheld there.
- **§7.2 owns the mid-resume close sequence.** Both §6.2 `resuming` bullets defer to it, so only the cancel bullet takes a clause. Steps 1 and 3 are unimplemented, so correcting step 3 creates no code obligation.
- **DECISION: §7.1's obligation window ends when the attempt SUCCEEDS.** `running` is bound vocabulary SPEC-4 pins, so "has the session running" ended the obligation too early. §7.1 states no report rule and no deadline.
- **CODE-1's response gate is `live`, not `started`.** A runtime-given slot answers clean on the runtime close alone; a pre-`running` slot only if every act succeeds. `started && !live` is unreachable at an ordinary session end.
- **§29.4 is scoped to a started session.** Its preconditions scope the whole trace to a completed §29.2 startup, so step 12 needs no edit; only `terminate` emits the graceful-shutdown signal, so step 13 is the whole mirror.
- **The adapter implements no process-group kill.** `removeSlotTree` is one line and `pkg/adapter` holds no `Setpgid` or `syscall.Kill`, so §5.2's action list is ahead of the code before and after SPEC-3. Pre-existing; do not file it.
- **DECISION: the disposition table's runtime-given `Shutdown` rows are QUANTIFIED over the act set, never keyed on a named act.** Row 3 reads "The runtime close succeeds and any other act fails", so an act added to §5.2's list cannot reopen the partition. Rejected: a fourth row; re-keying the three rows on `closeErr`/`treeErr`.
- **The three staged sites that restated the leaked-cleanup universal are now pointer-only**: the `**Slot cleanup:**` leaked-outcome sentence, the `**Whole-pod replacement trigger:**` parenthetical, and the §6.2 `slot_cleanup ──→ leaked` fence entry. The shipped `See **`leaked` slot semantics**` sentence after the first is unstaged and stays.
- **DECISION: DOCS-2 makes FOUR edits.** The `Shutdown` row (`adapter-contract.md:75`), the `DemoteSDK` row (:64), the `ReportSessionScrub` row (:81, re-keyed to "for the cleanups the `Shutdown` row states ... and for no other release") and one bind-attempt paragraph. The stale "three edits" count is deleted rather than re-counted.
- **DECISION: §15.4's hold block closes with a PURE POINTER,** "`Shutdown` is outside this hold, on the terms [§5.2] states, and is answered under the rules [§4.7.1] states for it." §15.4's only statement of its own is rule 2's `ABORTED` status, which its lead-in already declares. Rejected: deleting the sentence; a third declared exception.
- **DECISION: §15.4's `**Bind attempt token contract:**` block is TWO sentences, a pointer at §4.7.1 and a request-quantified conformance criterion, and carries no domain sentence, no exception clause and no answer enumeration.** The criterion reads "refuses or admits, performs or withholds the acts, and answers as that section states for that request", so it needs no "and no others" universal and no statement of what per-RPC work is outside the rules; none is made in §15.4 or in §4.7.1's `**Admission.**` preamble. Rejected, one per rewrite: the per-rule wire table (fcac6a13c), the rule-quantified criterion with two exceptions (440cde9ec), the round-10 domain sentence. `[redesign.2.fix.1]`.
- **The two "exceptions" the deleted §15.4 clause named were never exceptions.** Rule 2 cites §15.4 for its status in its own text, and rule 15 sits inside §4.7.1.
- **DECISION: rule 7 is one sentence, "Any other request is admitted."** Its no-overwrite clause is carried by the stamp-once paragraph. The clause is what makes the admission cascade total, which the §15.4 criterion rests on; do not re-add a token clause.
- **DECISION: §7.1 keeps only the consequence, "A compensation therefore always names one."** The minting timing and the response-independence live in §4.7.1's token block alone; the same pair was written a third time in the Edge-cases fenced-bind bullet and was swept to a citing form in the same edit; `[prune.4.fix.1]` deleted that bullet.
- **DECISION: SPEC-5 also replaces §15.1's `SETUP_COMMAND_FAILED` exclusion sentence,** re-keyed on the setup-command REQUEST rather than on the gRPC code alone, naming `Aborted` among the excluded codes. Without it a `FailedPrecondition` at another bind-sequence request had no stated envelope anywhere.
- **The §15.1 row is the single home of the mapping FOR THE SETUP-COMMAND REQUEST ONLY.** Every other `/start` setup-window failure falls to the bullet's runtime-launch clause and §15.1's `STARTING_FAILED` row. The wide phrase "the single home of the setup-window mapping" is the defect; both summary sites now carry the narrow one.
- **DECISION: §6.2's projection prose surrenders THREE clauses to one pointer at §4.6.1,** the two claim-deletion clauses plus the unqualified no-claim clause. Reducing only two leaves §6.2 answering `idle` where §4.6.1 answers `draining`.
- **§4.6.1's untouched first bullet absorbs §6.2's deleted no-claim clause,** "`idle` for a pod in a warm-inventory phase with no claim", and its warm-inventory scope is why it does not collide with the re-keyed `claimed` bullet.
- **DECISION: "The whole-pod scrub ends state only on a pod that reaches one." is DELETED from the post-table paragraph.** The whole-pod REPLACEMENT trigger drains and replaces a pod; the whole-pod SCRUB runs at the occupancy-zero recycle boundary. Any sentence conditioning one on the other is wrong on its face.
- **DECISION: SPEC-3 re-keys §12.6's WRITE trigger onto the cleanup-outcome report and reduces its READ clause to a pointer at §5.2's `**Session count limit:**` bullet,** in both carriers (prose and DDL comment). The evaluation point is one rule with three statements; re-keying only the §12.6 pair left two vocabularies.
- **Two shipped tier-11 gates go red on SPEC-3's §12.6 edits, and a whole-tree sweep says they are the only two.** `TestPerReleaseSessionCountDrainAgrees_F5231` (remedy: DELETE its spec/12 substring block, since §12.6 then states no evaluation point) and `podStateGatewayWrittenSentence` in `tests/tier11_docs/spec_28_register_writers_test.go` (remedy: re-key onto "incremented on each cleanup-outcome report"). The constant ends at the write clause, so the read and DDL edits do not reach it.
- **DECISION: SCHEMA-1 absorbs the two proto report-trigger sentences,** `schemas/lenny-adapter.proto:308-310` and `:451-452`, in DOCS-2's voice. Rejected: a SCHEMA-2 deliverable for the same file and surface.
- **CORRECTION: `st.sessionID` has TWO setters,** `claimSessionSlotUnderLock` and `assignCredentialsSlot`. The shipped `Shutdown` therefore takes `bound == true` for an `AssignCredentials`-bound slot that never started, sending the terminate frame, closing the shared runtime and filing a report; the staged re-gating on `st.started`/`runtimeLive` removes that whole arm.
- **There is exactly ONE non-test, non-generated `&adapterv1.ShutdownRequest{` in the tree,** `pkg/gateway/runtime/adapterclient/client.go`. This is the claim whose failure would make rule 10 a security regression, and it re-verifies clean.
- **§28.4's claim register is the file `tests/claim-map.json`, not spec prose,** so the promised `ABSENT` row is a SCHEMA-1 deliverable and its absence from "Spec files touched" is correct. No `gateway-to-pod` channel card covers the bind-sequence RPCs either.
- **`exited_cleanly` appears nowhere in `spec/`,** so the §5.2 table's "Clean-exit flag on the `Shutdown` response" column is the corpus's first definition of the shipped field and contradicts nothing.
- **The verbatim-anchor sweep is DONE and was re-run independently in rounds 2, 4, 5, 7, 10 and 11; every anchor hits byte for byte and EXACTLY ONCE.** Re-check only an anchor a later fix moves. The cheap form is a python script that extracts every fenced block from spec-changes.md and counts occurrences across `glob('spec/*.md')`; the zero-hit blocks are exactly the replacement texts, the two new fence entries, the SPEC-4 `claimed ──→ draining` replacement and the §16.1 rows.
- **The §4.7 row's "[§15.4.2] graceful-shutdown signal" citation is sound although §15.4.2 never uses the phrase.** `drainViaLifecycle`'s own doc comment established the convention. A round was spent on this; do not file it.
- **`PrepareWorkspace` is the only STREAMING RPC among the seven entry-creating RPCs, and its request is a FLAT message** with `session_id` on every frame and no `oneof`. That is what makes rule 9's first-frame form sufficient and leaves §4.1's stream-envelope paragraph and its tier-0 gate untouched.
- **`docs/api/internal.md` is wholesale pre-existing drift** (`StopSession`, `UploadFiles`, a `clean_exit` field, no `Shutdown`), so nothing this proposal stages newly falsifies it.
- **No OpenAPI document and no language SDK mirrors any edited surface. USEFUL, re-verified in round 11 and it saved a full sweep.** `SETUP_COMMAND_FAILED` appears in neither `pkg/gateway/externalapi/openapi/openapi.json` nor `sdks/`; the runtime SDKs carry JSONL and CH-RUNTIMEOPS types only, and the one `setup_command` hit under `sdks/` is an unrelated runtime-type field.
- **`docs/operator-guide/multi-tenancy.md:72` states the cleanup without the reporting clause and stays true.** Only `execution-modes.md:68` and `security-principles.md:33` carry the withdrawn tail.
- **The table's outside-`Shutdown` rows are keyed "an act fails AFTER the deregistration", and that clause is load-bearing.** `releaseSessionSlot` and the hold-timeout pass both deregister first; only the SDK demotion closes first, which is why it keeps its own row. Do not "simplify" the key to "an act fails".
- **The staged §4.7 `Shutdown` row records the shipped `Server.Shutdown` order exactly**: `emitFinalUsage` → `drainViaLifecycle` gated on `!boundRemains` → `Runtime.Close` → `removeSlotTree` → `reportSessionScrub`, with `ExitedCleanly: closeErr == nil`.
- **THREE distinct instants exist on the start path and the staging uses a different one at each site.** Admission of the starting RPC (`st.started`, the §4.7 teardown precondition); `Runtime.Start` returning, which the row names explicitly as NOT the boundary; the recording (`runtimeLive`), which is the `running` boundary for the report biconditional, the table keys and rule 8.
- **DECISION (prune 1): contract-level accepted failure modes live in `spec-changes.md`'s Edge-cases section only.** The same-named section in `non-spec-changes.md` carries code cases; four bullets were folded up and a lead paragraph names the owning case. The two tellings of the refused retry had drifted and the spec file's is right.
- **DECISION (prune 3): `spec-changes.md` carries NO line-number citation.** Adding one reintroduces the false-citation class round 1 confirmed.
- **DECISION (prune 4): the "does a `Shutdown` that removes nothing still run the whole-pod scrub" family is CLOSED** by one clause in SPEC-1's §4.1 sentence: the scrub runs when the recycle disposition is set on a request that passed the teardown-pairing rule, whatever outcome it answers. It changes no behaviour.
- **Open decision 27 stays with the human in its narrowed form, and the controller-side fix costs no schema field and no migration.** The terminal dispositions, `podclaim.WriteDispositionStatus` and the recycle-boundary caller all exist; the cost is one extra status write on the release path.
- **`summary.md` already carries its eight required sections in order, and `**Accepted failure modes.**` stays inside `## Non-goals`.** Entries 20, 21 and 27 are the three left with the human.
- **§16.1's house style is a long mechanism description plus a `see §X` pointer,** so the `lenny_slot_compensation_superseded_total` row's `superseded` gloss is not a duplication of rule 15. If a round wants a reduction, the row is the site, never rule 15.
- **§4.6.3 gives `Sandbox.status.*` to the WarmPoolController as sole writer, and both writing reconcilers share one field manager,** so SPEC-4's read-back parenthetical is sound and the two-managers-racing-one-field dress is dead. The gateway holds no `sandboxes/status` grant.
- **DECISION: every carrier of the withdrawn "the adapter reports on every session release" universal has ONE home, the carrier table in SPEC-3's §5.2 commentary.** It carries 24 rows: 5 staged in SPEC-3's own blocks, 7 mirrored (DOCS-2, DOCS-4 twice, SCHEMA-1 three times counting the regenerated `pkg/proto` copies, CODE-1), 12 deferred to the non-spec loop. It was produced from one grep over `spec/`, `docs/`, `schemas/`, `pkg/`, `migrations/` and `tests/`; the set had been discovered one carrier per sweep in rounds 7, 8 and 10.
- **DECISION: DOCS-4 exists and is exactly two page edits,** `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33`, both carrying "and the adapter reports its outcome to the gateway". It is in `summary.md`'s index, the non-spec files-touched list and the tier-11 section.
- **DECISION (`[redesign.2.fix.1]`): the round-7 objection to deleting §12.6's read clause is OVERTURNED and the round-10 reduction to a pointer at §5.2's `**Session count limit:**` bullet STANDS.** The round-7 ground was that it silently retires a tier-11 gate; the retirement is now recorded in the carrier table's two tier-11 rows and in SPEC-3's §12.6 block, which states the one grep the implementor runs instead of the twenty-five-line gate-bookkeeping paragraph.
- **DECISION (round 11): §12.6's WRITE clause is NOT reduced to a pointer, although the rule now has three staged statements.** `podStateGatewayWrittenSentence` pins that clause byte-exactly for the §28.3 REG-PODSTATE writer register, and §12.6 is the column's own documentation. The asymmetry with the read clause is deliberate; reopening it owes an answer for the register gate.
- **Every markdown anchor written in a staged block resolves,** eighteen distinct `file#anchor` targets plus five same-file `#anchor` targets, slugged out of spec-changes.md and matched against the headings of `spec/*.md`. The relative-versus-absolute choice is correct at every site: same-file fragments only inside spec/04 edits, full filenames from spec/05, 06, 07, 15, 16 and 29.
- **The staged §4.7 `Shutdown` row keeps every substring `TestRecycleTriggerConsistentAcrossSpec47And52_F5215` pins,** because the replacement stops at "...rather than selecting a scope." and the gated strings live in the untouched remainder. The row is one physical line before and after.
- **The new §5.2 action-list sentence naming `/run/lenny/slots/{sessionId}/` and `credentials.json` does NOT trip the credential-literal sweep,** which bans only the retired pod-global literal `/run/lenny/credentials.json`.
- **Adding the two §16.1 rows turns no gate red on its own.** `adapter_metric_catalog_test.go` walks registered → documented, so a catalog row with no registered metric passes; it goes red only if CODE-9 registers `lenny_slot_shutdown_untokened_entry_total` before SPEC-6 lands. The gate also REQUIRES an adapter metric to reach both §16.1 and `docs/reference/metrics.md`, so putting the adapter series in §16.1 is required rather than a violation.
- **The §6.2 per-slot fence gate compares edge PRESENCE and block membership only, never trigger text,** so replacing both `slot_cleanup` annotations with `(see §5.2)` and inserting `receiving_uploads ──→ slot_cleanup` into the general block is safe. The scoped block precedes the general block, which the gate's slicing assumes; the new edge's 2-space indent and 41-column continuation match the existing entries.
- **Every code symbol and path cited in spec-changes.md commentary re-verifies (round 11),** including `maxSlotRetries = 1`, `Binder.failPhase`, `releaseSessionSlot`, `terminateHeldSession`, `Server.DemoteSDK`, `ScrubReporter.RecordSessionScrub`, `slotlayout.RemoveTree` and `deregisterSlotLocked`. The `ShutdownRequest.recycle` proto quote is real but WRAPPED across two comment lines, so a one-line grep misses it.
- **"Spec files touched" is complete against the staged edits,** matched one for one in round 11: nothing staged is missing and the list names nothing unstaged.
- **spec-changes.md carries no line-number citation and no N3 reserved phrase,** so the naming lint and the line-citation ratchet stay green on that file.
- **The only spec site naming `ShutdownRequest` or `ShutdownResponse` is `spec/04_system-components.md:157`, which SPEC-1 edits,** so the two new `Shutdown` fields have exactly one spec carrier and it is staged. `Shutdown` appears NOWHERE in spec/28, so "§28's registers take no row" is true by absence.
- **The adapter `ErrorCode` enum has no spec-side enumeration,** so minting 28 and 29 owes no spec table edit. The only spec mention of any value is `PROTOCOL_VERSION_INCOMPATIBLE` in the §15.4.2 `INIT` row; the enum's proto comment "The catalog below mirrors spec §15.1" is the one site that goes false and it is SCHEMA-1's.
- **`ShutdownResponse.exited_cleanly` is a SHIPPED field, number 1,** so the §5.2 disposition table's clean-exit column needs no new wire field; only `slot_reclaim` = 3 is new.
- **SCHEMA-1's nine-field table and the staged §4.7.1 carriage table AGREE field for field,** checked in both directions: `bind_attempt` on PrepareWorkspace, FinalizeWorkspace, RunSetup, AssignCredentials, Resume and Shutdown; `mid_session` on PrepareWorkspace (new) and FinalizeWorkspace (shipped); neither on StartSession or ConfigureWorkspace.
- **§15.4.6's conformance categories run the RUNTIME BINARY against a fake adapter,** so the "deliberately untouched" justification is sound and the new adapter obligations cannot land there.
- **SPEC-5's four `SETUP_COMMAND_FAILED` verbatim anchors match `spec/15_external-api-surface.md:1136` word for word, and §6.2's `**Client visibility:**` clause matches `spec/06_warm-pod-model.md:290`.** The `details.reason` sentence sits after the exclusion sentence in the live row, which the staging does not claim otherwise.
- **The only docs hits for `setup_command_failed` outside `error-catalog.md` are a troubleshooting remediation row and a `lenny_warmpool_warmup_failure_total` `error_type` value,** neither stating the catalog code's cause, so SPEC-5 opens no unlisted docs mirror beyond DOCS-3.
- **No docs page mirrors §12.6's `sessions_served` sentences,** so the round-10 read-clause reversal owes no docs edit. The nearest reader-facing statement, `docs/reference/state-machines.md:248`, is the evaluation point, which the change leaves untouched.
- **`TestPerSlotCleanupStatedOnEverySessionModeRow` asserts only the substring "Per-slot cleanup" in each residual-state row,** so DOCS-4's removal of the reporting clause from the prose sentence below the table does not touch it.
- **Round 11 inventoried single-sourcing and found one home each, with every other site citing:** rules 1-15 in §4.7.1; the registry critical section; stamp-once; the carriage table; the §5.2 disposition table; the §5.2 reclaim-hold paragraph; the §5.2 scrub-model report biconditional; §7.1's reclaim obligation; §15.4's two pointer blocks; the §15.1 `SETUP_COMMAND_FAILED` mapping; §4.6.1's claim-deletion projection.
- **Round 11 declined five single-source candidates, each a house-style row, a choice record, a scope declaration or a derivation:** SPEC-6's `superseded` gloss, the Design rule-5-before-rule-6 ordering sentence, the "no §15.1 row" proposition at three sites, the §4.7 row's pairing clause against rule 10, and the Edge-cases §15.1-envelope walkthrough.
- **The round-5 single-source shard filed FOUR groups** (the §5.2 whole-pod-scrub sentence, the §15.4 hold block's closing sentence, rule 7 versus §7.1 minting, the duplicate start-race ordering). The graceful-shutdown triple it also recorded was NOT among them, so that one is unfiled rather than refuted.
- **Rounds 11's applicability, client-surface and docs-alignment lenses each returned EMPTY.** The delta since the docs lens last ran empty in round 7 is the prune `9c23121af` and the redesign `9c59dabec`, both docs-neutral.
- **DECISION (`[prune.3.fix.1]`): the disposition table's residue column is DELETED and the residue is ONE post-table sentence.** It reads: a cleanup act that fails OR DOES NOT RUN leaves what it would have removed or ended; a cleanup that did not deregister also leaves the entry and its armed §4.9 timers; pod termination ends all of it and the whole-pod scrub, on a pod that reaches one, ends the directories; the one-session retirement clause moved here from the last row. The table is now six columns and nine rows.
- **The residue rule names the RUNTIME CLOSE as an act beside the `**Slot cleanup:**` action list,** because the table's key column and the hold paragraph both treat it as one. Adding the close to the action list was rejected: it edits shipped text and the close is not a per-slot act on a pre-`running` slot.
- **DECISION (round 12): the §15.4 pointer's narrowing "to a request that carries it" is DELETED.** The domain of rules 1-15 is §4.7.1's `**Admission.**` preamble plus rule 10's cascade; §15.4 states no domain. The narrowing excluded `StartSession` and `ConfigureWorkspace`, the very requests rule 8 exists for.
- **Gateway-side occupancy is the gateway's OWN `active_slots` counter, and the adapter's registry is never an input to it.** `ReleaseSlotReservation` decrements unconditionally on every bind-failure path, so a standing adapter entry with nothing reported does NOT pin occupancy. This settles the rows-8/9 question three rounds carried.
- **`leaked` pins occupancy and the whole-pod scrub runs only at occupancy zero,** stated in shipped spec three times (spec/05:395, :453, :488; spec/06:160). This is the chain every scrub-reachability argument rests on.
- **On a ONE-SESSION recycling pod the ending session's own `Shutdown` carries the recycle disposition and runs the whole-pod scrub,** and such a pod has no `leaked` sub-state at all. Any reachability argument keyed on `leaked` is therefore false at concurrency 1.
- **DECISION (round 18): the graceful-shutdown signal's co-tenant condition has ONE home, the §4.7 `Shutdown` row.** §29.4 step 13 is trimmed to a citation and states nothing itself; the Edge-cases bullet keeps only its own ground (why the gate is the binding rather than `started`). This closes the round-11 triple.
- **DECISION (`[prune.4.fix.1]`): the SPEC-5 §15.1 rationale names NO client-facing code for any request other than the setup-command request.** The `CREDENTIAL_POOL_EXHAUSTED` counter-example and the "after the replacement the row is total" sentence are deleted; the stage-by-stage mapping's one home is the Edge-cases envelope bullet.
- **DECISION (`[prune.4.fix.1]`): the Edge-cases section carries ACCEPTED FAILURE MODES ONLY.** Two bullets are deleted (the fenced first-entry-creating-RPC bullet and the compensation-still-on-the-wire bullet) plus the create-time-reserved rationale tail. `summary.md`'s block is one pointer sentence plus the two priced residues (the refused retry's bounded window; the entry that outlives its attempt costing the pod its MCP arming), which appear nowhere else.
- **DECISION (round 16): the §5.2 hold paragraph loses its SDK-demotion tail,** keeping "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers when that cleanup completes." The demotion instance moved to the DOCS-2 `DemoteSDK` row, conditioned "When that cleanup completes, ...".
- **DECISION (round 20): the post-table clause "as a reclaim whose act fails does" is DELETED.** It contradicted row 3, where a failing non-close act reports `released` and does not enter `leaked`. The surviving proposition, that an unanswered §7.1 reclaim enters `leaked`, is stated nowhere else.
- **`OccupancyReconciler` WATCHES ITS OWN OBJECT (`For(&Sandbox{})`), which is what answers convergence findings against SPEC-4's read-back,** separately from the sole-writer fact that answers field-manager findings. A later change narrowing that watch (a predicate, `GenerationChangedPredicate`, `Owns`) makes the lagging-read hazard live.
- **`ReportSessionScrub` is AT-MOST-ONCE on the wire.** `Client.ReportSessionScrub` performs no retry and returns the transport error, so the gateway's missing dedup cannot double-count `sessions_served` under redelivery. This kills the at-least-once dress against the §12.6 re-key.
- **The compensating `Shutdown` is IDEMPOTENT under replay by construction.** A second copy naming the same token meets no entry, is answered `absent` by rule 11, and rule 15 makes that a clean-exit success.
- **`sessions_served` feeds two capacity controls and both survive the biconditional.** `RecordSessionScrub` increments and then calls `RetireOnSessionCount` with the post-increment value; `mode_factor` comes from `lenny_pod_session_reuse_count`, observed at pod RETIREMENT rather than per release.
- **`lenny_adapter_leaked_slots` is emitted by the GATEWAY despite its name** (`gatewaymetrics_credential.go`), so every gateway-side `leaked` entry in the table does reach the gauge.
- **The whole-pod scrub PURGES residual credential files and workspace trees rather than failing on their presence;** it enumerates them on disk and step 6 re-verifies absence, and an enumeration error fails the scrub outcome, retiring the pod.
- **`ConfigureWorkspace` is the LAST step of the SDK-warm bind, not the first,** so it never creates the attempt's entry and the entry it resolves already carries `PrepareWorkspace`'s token. This kills "an SDK-warm bind runs its whole attempt on an untokened entry".
- **`ConfigureWorkspace`'s own failure rollback is a FOURTH `releaseSessionSlot` performer,** and table row 6's "the adapter's own handler for a start that fails" covers it: `noteRuntimeStarted` runs only after `sw.ConfigureWorkspace` returns, so the rollback releases a pre-`running` slot.
- **The §15.1 re-key holds on the SLOT path, not only the exclusive one.** `slotBindError` wraps `SetupCommandFailure` and start.go's `errors.As(err, &setupFail)` case PRECEDES the `slotFailed` case, so a slot-path `RunSetup` refusal reaches `writeSetupCommandError`.
- **`SLOT_BIND_ALREADY_STARTED` classifies `SlotReasonPolicyRejection` at every stage except the workspace stages, where `SlotBindError.Reason()` forces transient,** so §5.2's three non-retryable categories need no new member. The "deliberately untouched" conclusion holds although its stated ground does not address rule 6.
- **The new-identifier sweep is ONE command and returns nothing:** `bind_attempt`, `unconditional_teardown`, `mid_session`-as-new, `slot_reclaim`, both `SLOT_BIND_*` codes and both new counters have zero pre-existing carriers in `spec/`, `docs/`, `schemas/`, `charts/`, `migrations/`.
- **`charts/` and `pkg/alerting/` carry nothing this proposal touches,** and `docs/reference/metrics.md` is AUTHORED rather than generated (plain kramdown front matter, no generation note), so CODE-9 editing it directly is correct.
- **SCHEMA-1's field numbers are fixed and re-verified: `PrepareWorkspaceRequest.mid_session = 6`, `ErrorCode` 28 = `SLOT_BIND_ALREADY_STARTED` and 29 = `..._ATTEMPT_SUPERSEDED`, `ShutdownResponse.slot_reclaim = 3` carrying a new `SlotReclaimOutcome` enum.** Every carriage-table cell has a matching SCHEMA-1 row.
- **The §7.1 insertion lands INSIDE spec/07's fenced flow listing (lines 5-54),** so its markdown links render literally, exactly as the shipped atomicity paragraph's already do. Precedent-matched; three rounds have confirmed it.
- **The eleven-home single-source inventory was re-derived independently at rounds 12, 15, 18, 19 and 20 and has not moved,** now including the post-table residue rule as the residue home and §12.6-plus-§5.2's `**Session count limit:**` for the counter. It has paid for itself four times; do not rebuild it.
- **`leaked` and `acknowledged clean` agree ROW FOR ROW with the disposition table, checked at round 21.** Rows 1, 3 and 4 (clean-exit flag Set) are `Not entered`; rows 2 and 5 (Not set) are `Entered`; an unanswered reclaim is `Entered` by the post-table sentence. So `leaked` ⟺ not acknowledged clean over every `Shutdown`-performed cleanup, and CODE-4's `sbe.Leaked = cerr != nil || !cleanly` implements exactly that.
- **The two-term split is grep-checkable at five sites and nowhere else.** "acknowledged clean" hits only §7.1's staged block and its commentary; the predicate sense of `completed` hits only §5.2's hold paragraph and §7.1's residue pointer. CODE-1's `completed` variable keeps its name because it implements the pod-side predicate.
- **This log has NO top-level `## Retired`.** `### Retired` is a subsection of `## Standing context` and `## Ledger` is the file's last section, so an entry appended at the end of the file lands at the end of the ledger and buries nothing. Three cleanup firings have had to re-derive this.
- **`summary.md`'s section offsets move with every prune, so locate its sections by heading text.** The eight required sections and their order have not changed across passes; the line numbers earlier firings recorded no longer resolve. The stray blank line before the `§7.1, §7.5` defects entry, which renders that bullet list loose, is left deliberately: three firings recorded it as a decision rather than a format correction.
- **`TestGoCommentsNameOnlyLiveSpec47RPCRows` harvests every `| \`Name\` |` row in the WHOLE `### 4.7 ` slice,** so the §4.7.1 carriage table's rows join its row set. The gate asserts comment-claims ⊆ rows, so the insertion only widens the allowed set and the `rows["Shutdown"]` sanity guard still holds.
- **The Design's stated direction is that the cleanup-outcome report UNDER-approximates toward not counting while the gateway-side `leaked` reading OVER-approximates.** That asymmetry is what answers a finding that one side of the pair books the wrong disposition; it is a recorded choice rather than a defect.
- **Rounds 12 through 20 produced six filed findings in total, all closed by DELETION:** the §15.4 narrowing, the two `leaked`-row scrub cells (later moot), the residue-cell wordings, the DOCS-3 "one replacement §15.1 does not need" sentence, the hold paragraph's SDK-demotion tail, the §29.4 co-tenant restatement and the post-table `leaked` analogy. Every lens that returned findings returned at most one.
- **ROUND 22'S TWELVE LENSES ALL RETURNED EMPTY**, on applicability, citations, client-surface, docs-alignment, edit-sites, fresh, kubernetes, mechanism, performance, reliability, security and single-source. The spec staging has now taken a full-breadth pass with no filing; the security and kubernetes candidate spaces are declared exhausted by their own lenses.
- **The round-20→22 spec-changes delta is exactly five hunks, all wording (commit `1b12bba90`),** plus the round-18 commentary prune: two Edge-cases phrases, the §7.1 acknowledged-clean sentence pair, the sentence after it, and the bolding of `**completed**` in the §5.2 hold paragraph. Nothing client-facing, K8s-facing or normative moved, which is why four round-22 lenses could be delta checks.
- **The round-22 anchor re-verification is clean, and these are the anchors that cost the most to find:** §4.1 spec/04:157; §4.6.1 enumeration :407-409 and the two claim-deletion bullets :415-417; §4.7 `DemoteSDK` :674, `Shutdown` :686, `ReportSessionScrub` :692, §4.7.1 insertion point immediately before `#### 4.7.2` at :695-697; §4.7.9 step 5 :854; §5.2 :453, :488, :545 (all three SPEC-3 anchors on one line), :561; §6.2 :80, fence :95, :148, :152, :155, cancel bullet :234, `**Client visibility:**` :290; §7.1 :23 with continuation :24; §7.2 :210-214; §12.6 :481 and :494; §15.1 :1136; §15.4 :1469 before `#### 15.4.1` at :1471.
- **The whole citation sweep is THREE cheap commands and costs about ten minutes.** (1) python `re.findall(r'\n```\n(.*?)\n```\n')` over spec-changes.md counted against `glob('spec/*.md')`; (2) a grep for backticked `pkg|tests|schemas|docs|migrations|charts|cmd|examples` paths piped through `[ -e ]`; (3) a python slugger over `^#` headings matched against `\]\((file)?#anchor\)`. The two bare `#71-normal-flow`/`#73-retry-and-resume` links land in spec/07 and resolve.
- **The whole-file sentence-repeat sweep at round 22 returns SIX repeats and every one is benign.** Three are verbatim-anchor/replacement pairs inside spec-changes.md and three are spec-row/docs-mirror pairs against non-spec-changes.md, which is DOCS-2's one permitted reader-vocabulary restatement. The script splits every line on `(?<=[.;])\s+`, normalizes whitespace and counts; it runs over spec-changes.md, summary.md, non-spec-changes.md and the checklist together.
- **`spec161Metrics` is a HAND-MAINTAINED Go list in `pkg/observability/metrics/catalog_test.go`, not parsed from spec/16,** so `TestMetricCatalogIsCompleteAgainstSpec161` and `TestMetricCatalogHasNoUnspecifiedMetrics` are inert against a new §16.1 row. S6 before S10 is safe in both directions.
- **Adding `receiving_uploads ──→ slot_cleanup` to §6.2's general per-slot block trips nothing.** `per_slot_substate_scope_doc_reconciliation_test.go` enumerates four general edges and checks that none of THOSE appears in the scoped block; the new edge is in neither list, and the scoped-block check tests the bare substring `slot_cleanup ──→ leaked` rather than its trigger text.
- **`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the FULL addressing sentence as one string on BOTH carriers,** not merely its opener. SPEC-3 keeps it byte-identical and the row one physical line, so the gate is not stranded between S4 and S7; DOCS-2 must keep the identical sentence.
- **`spec_47_rpc_row_naming_test.go` reads row names from the whole `### 4.7 ` slice and holds Go comments to that SET one-directionally,** so the §4.7.1 carriage table only widens the allowed set. It is a DIFFERENT gate from the three FIRST-match gates the traps warn about.
- **A whole-tree sweep for the distinctive strings the spec staging DELETES returns only four carriers outside `spec/`, all already dispositioned:** `adapter-contract.md`'s `DemoteSDK` opening clause (DOCS-2), `error-catalog.md`'s two `SETUP_COMMAND_FAILED` sentences (DOCS-3), and `pkg/controller/warmpool/occupancy.go`'s "one-session-only invariant" comment (code lane). No test file matched any of them.
- **`spec/06_warm-pod-model.md` lines 82-157 are ONE fenced block holding five labelled groups** (Warm fill pod-warm, Warm fill SDK-warm, Occupancy projection, Recycle edges, Concurrent occupancy) plus two per-slot sub-state groups. "The fenced `Occupancy projection` block" is a group inside that one fence, not a fence of its own.
- **§4.9's `anthropic_direct` row (spec/04:1169) states that the adapter MUST arm a per-lease expiry timer and what happens when it FIRES, and says nothing about cancelling it,** so SPEC-3's added timer-cancellation act duplicates no §4.9 rule and contradicts none.
- **§5.2's whole-pod credential purge (step 0, spec/05:461) is worded "every per-slot credential file REMAINING under `/run/lenny/slots/`",** so it is already compatible with a per-slot cleanup that removed the same file first. SPEC-3's widened action list opens no conflict.
- **The post-table residue sentence's whole-pod-scrub clause re-verifies act by act against §5.2's own procedure:** step 0 removes `/run/lenny/slots/{sessionId}/credentials.json` and step 2 removes `/workspace/slots/*`, which is exactly "ends the slot's workspace tree and credential file" and no more.
- **The three SPEC-4 §6.2 projection-prose deletions compose into exactly the sentence the proposal says they leave,** word for word, and each residual reads grammatically: deleting "a pod with no claim projects `idle`; " leaves the colon followed by the `bound`/`claimed` clause.
- **The §7.2 preamble deletion leaves a grammatical two-sentence lead-in,** and the `**Pre-attach terminal collapse**` paragraph after the close sequence independently states the no-pod-attached case, so deleting the "no live workspace to seal" premise strands no later sentence.
- **The §5.2 append lands between a run of bolded standalone paragraphs, not inside a list or a fence,** so a markdown table plus two paragraphs fit there. No shipped gate calls `requireLine(s52, "Slot cleanup")`, and the two that call `requireLine` match no line the append introduces.
- **§10.1's "Hold state timeout" exists and terminates every session the adapter has STARTED on the pod (spec/10:58),** which is what makes the disposition table's "Slot of either kind" key sound for that performer.
- **§10.1's hold-state-timeout bullet states NO slot cleanup report,** delegating the slot disposition to the whole-pod connection-loss paragraph (spec/10:62), which is gateway-side (Redis `active_slots` reset and rehydrate) and names no `ReportSessionScrub`. SPEC-3's biconditional falsifies nothing in spec/10; do not re-grep it.
- **§5.2's `**Session count limit:**` bullet stays TRUE under the withheld report:** a release that files no report drives the count to nothing, so the bullet is vacuously true for it. SPEC-3 re-points §12.6's read clause at the bullet without editing it, and its absence from the carrier table is correct.
- **The `**Slot-identifier reclaim hold.**` paragraph's close-bounding sentence is EXHAUSTIVE, which is not obvious.** The only other cleanups are the SDK demotion, which closes before it deregisters so no hold is open across its close, and the failed-start handler, whose `releaseSessionSlot` calls no `Runtime.Close`. Do not file the enumeration as incomplete.
- **The staging adds NO per-session or per-request write to etcd, Postgres or Redis and net-REDUCES Postgres writes.** The only new per-unit work is one compensating `Shutdown` per failed bind plus its cleanup, and the two SPEC-6 counters. No new watch, informer cache, hot key or single-leader serialization.
- **The staged registry critical section puts NO slow act under the one registry lock.** The `Shutdown` step is the rules-11-14 decision plus the deregistration; the cleanup acts are explicitly outside it, and the start step is resolve-confirm-record with `Runtime.Start` outside. At the top documented tier (`maxConcurrentSessions: 8`) the lock holds O(1) map work.
- **`ProjectOccupancyPhase` reading back the pod's own projected phase creates NO reconcile write loop.** The no-claim branch is `Reserved→Idle`, `Claimed→Draining`, `("", false)` otherwise, so `draining` is a fixed point the projection stops owning. SPEC-4 changes the controller write rate by zero.
- **The §10.1 hold-timeout ten-second graceful window is ONE `context.WithTimeout` SHARED across every member the hold deregistered, not a per-member window.** The code's comment justifies the sharing, and the staged sentence bounds only the one close, so the aggregate-versus-per-unit angle has nothing to correct. `emitFinalUsage` also runs on that shared ctx per member.
- **`ShutdownRequest` carries `coordination_generation` (field 6) and the proto comment claims per-RPC validation, but `Server.Shutdown` performs NO generation check.** `pkg/adapter/coordination.go` holds the only two gates, so a deposed coordinator's reclaim being rejected does not occur in the tree; a finding there would rest on an unimplemented spec claim.
- **§4.6.3's `Sandbox`/`status.*` ownership row is the DIRECT AUTHORITY for SPEC-4's read-back parenthetical,** and the Gateway ServiceAccount paragraph (spec/04:632) states the gateway holds no `sandboxes/status` grant. No actor violation.
- **SPEC-4 TIGHTENS the one-session-only control rather than removing it.** The deleted closing sentence permitted `claimed → idle` reuse on a recycling pool under its limits; the replacement sends a claim deleted while the pod projects `claimed` to `draining, then terminated` on either recycle setting, leaving `reserved → idle` as the only reuse edge. Third recording.
- **The round-18 "graceful-shutdown condition is stated at three sites" finding is CLOSED in the live staging.** The condition is stated once, in the §4.7 `Shutdown` row; §29.4 step 13 and the Edge-cases bullet both cite it, and no shipped spec section states a competing condition for the §15.4.2 signal.
- **No staged spec block is a copy of shipped spec.** `grep -rn "bind_attempt\|reclaim hold\|acknowledged clean\|unconditional_teardown" spec/ schemas/` still returns nothing, so both new terms are first statements in the applied corpus.
- **§15.4's shipped `**SDK-warm demotion contract:**` paragraph does NOT become wrong under SPEC-1's amended §4.7 `DemoteSDK` row.** It states the teardown, the 10s timeout, the `UNIMPLEMENTED` code and the post-demotion pod state, none of which the added registry removal falsifies, and publishing the removal in §15.4 is a recorded rejected alternative.
- **DOCS-3 fully discharges the Edge-cases claim that it "mirrors those same replacements".** SPEC-5 makes four §15.1 sentence replacements and DOCS-3 stages four matching description-cell replacements plus a remedy-cell replacement, in the page's own voice with no spec section numbers and no adapter `ErrorCode` names. Do not re-check it.
- **§16.1's metric catalog table is TWO columns (`| Metric | Type |`) and SPEC-6's two staged rows are two columns,** so they fit; the shortened deferral phrasing is a form of the shipped phrasing at spec/16:188-189 and filing it is already barred.
- **The SPEC-3 carrier table's docs coverage is COMPLETE over the whole `docs/` tree.** `grep -rn "per-slot cleanup" docs/` returns six sites: two DOCS-4 carriers (`execution-modes.md:68`, `security-principles.md:33`) and four correct non-carriers (`multi-tenancy.md:72`, `runtime-author-guide/index.md:186`, `lifecycle.md:69`, `client-guide/session-lifecycle.md:416`, a mermaid arrow label). `grep -rn ReportSessionScrub docs/` returns only `adapter-contract.md:75` and `:81`, both DOCS-2. No seventh carrier exists.
- **The `DemoteSDK` row's citation "[§6.1] admits `preConnect` only at `maxConcurrentSessions: 1`" is ACCURATE:** the compatibility paragraph and table sit at spec/06:69-76 inside §6.1, and §6.2 starts at :78. A lens tempted to file it as a wrong-section citation stops here.
- **`ShutdownRequest` is named in exactly ONE spec sentence (spec/04:157) and no spec file enumerates adapter `ErrorCode` values,** so the two new codes and the two new `Shutdown` fields have no second spec-side carrier to mirror.
- **Widening `SETUP_COMMAND_FAILED`'s cause falsifies none of the five other spec sites that mention it.** spec/15:625, :646, :647, :650 and :720 each state a sufficient condition, and spec/07:390 is a bare JSON example value in `retryPolicy.nonRetryableFailures` that defines nothing.
- **`spec/07:72`'s `scrubPolicy` row parenthetically enumerates the cleanup's acts and already disagreed with §5.2's list before SPEC-3 widens it,** so it does not BECOME wrong. The divergence is pre-existing; round 22 declined it.
- **DECISION (`[redesign.6.fix.1]`, restated): the `running` boundary's one home is the record step of the staged §4.7.1 registry critical-section paragraph,** and rule 8 alone is not that home: rule 8 names the action ("records the pod's shared runtime process as holding a session") and never ties it to the word `running`, so the two are complementary rather than duplicate. A single-source filing against the pair dies here.
- **DECISION: the reclaim hold's scope is NARROWED to the seven requests §4.7.1's admission rules govern,** create or resolve, the §7.4 mid-session upload among them. The `, apart from the reclaim hold of rule 2` clause leaves §4.7.1's `**Admission.**` preamble and §5.2's unqualified "create or resolve" sentences are replaced. The wide phrase had FOUR copies, not three (non-spec-changes.md twice, summary.md twice, the fourth in different words at summary.md:48); all four now read "the seven requests §4.7.1's admission rules govern". The non-spec half has landed; the spec half is still Deferred.
- **`slotStateLocked` and `ensureSlotStateLocked` are different functions with different reach:** eleven production call sites against three. `Checkpoint`, the credential controls, the manifest reader and the tracing-context reader all resolve through the read-only one, which is why they sit outside the hold and why the hold cannot refuse a mandatory control.
- **DECISION (open decision 35, RESOLVED): no ordering statement is owed for the whole-pod scrub against the per-slot cleanup.** The entry's premise was false: `answerShutdown` starts the scrub goroutine after `Runtime.Close` and the tree removal have both returned on the removing arm, and `spec/05:455` already states that the whole-pod scrub runs after every ended session's per-slot tree and credential lease are removed. The only act still outstanding is the hold's deferred release, which is conservative.
- **DECISION (open decision 38, RESOLVED as staged): a cleanup whose runtime close succeeds and whose later act fails owes NO `leaked` signal.** The withdrawn universal describes a control the tree never exercised (`removeSlotTree`'s error is discarded at `pkg/adapter/session.go:271`, and `sessionScrubOutcome` keys on `closeErr` alone), and re-keying would retire a pod on one failed `os.RemoveAll` because `RecordLeak`'s count is never pruned and stamps `drain-request` at threshold 1. Recorded as a row under the summary's unstaged-defects list. The `:272` citation earlier entries carry is one line off; the discard is `:271`.
- **DECISION (open decision 39, RESOLVED): the emptied-workspace outcome of a recreated entry stays proposal-only.** The spec-changes Edge-cases section is proposal-internal analysis that lands nowhere, prune 1 fixes it as the single home of contract-level accepted failure modes, and no `spec/` or `docs/` convention for "accepted limitation" exists to make the omission inconsistent.
- **DECISION (open decision 40, RESOLVED as staged): the ten-second graceful close window is NOT operator-tunable.** `code-best-practices.md` conditions the override obligation on a default the spec does not fix, and SPEC-3's §5.2 paragraph fixes it. Ten seconds is the platform's existing SIGTERM-to-SIGKILL pivot (§11.4 step 3 at spec/11:264, the gateway's `deadline_ms` comment, `defaultSocketShutdownGrace`), `defaultMCPShutdownGrace` is five, and `grep -rn "ShutdownGrace:" --include=*.go .` returns nothing, so no runtime configures a grace the fixed window truncates.
- **The ten-second figure's provenance, verified by at least ten shards this window and never again worth checking:** `pkg/adapter/holdstate.go:201` is `context.WithTimeout(context.Background(), 10*time.Second)` inside `onHoldTimeout`'s pass 2 (declared :177, rationale comment :196-200), landed by commit `3997f502b`, "Terminate every started session when the coordinator hold times out", 2026-08-22.
- **The open decisions left with the human are 20, 29, 30, 32, 33, 34 and 36.** Entries 35, 39 and 40 left as resolved, 38 left for the unstaged-defects list, and each departure is recorded in the section preamble. Entry 30's recommendation is to leave the §6.2 client-visibility correction out of SPEC-5 and record it as a shipped-tree defect.
- **DECISION (`[index-reconcile.1]`): the deliverable index and the implementation checklist needed no edit.** SPEC-1..6, CODE-1..9, CONF-1, SCHEMA-1 and DOCS-1..4 each appear once in the index with their files and once in the checklist, the six spec steps lead, each step names one lane, every `Depends on:` resolves to an earlier step, and S16 lists S15 and S1, S18 lists S1, S21 lists S1 and S15.
- **DECISION: CODE-3 is the one home of the retired `leaked` trigger's retirement and now covers every Go carrier.** Seven sites over four files: `slotstate.go:44,:104`, `slothealth.go:33,:110`, `registry.go:93`, `sessionserver.go:419,:1667`, `gatewaymetrics_credential.go:72,:220`. The reduction is the same at each: delete the retired ground clause, keep the consequence, keep the existing §6.2 citation, add no §5.2 pointer to a prose comment. The command is `grep -rn "cleanup tim" pkg/ --include=*.go | grep -v _test.go`.
- **Nothing under `tests/` pins any slotstate.go or slothealth.go comment string CODE-3 rewrites,** so the re-key breaks no gate, and the only `docs/` carrier is `state-machines.md:251`, which DOCS-1 already owns. S8 stays at tiers 0 and 1.
- **CODE-1 contains NO markdown table.** The six-row `| Request | Entry | Answer | Removes |` table was deleted by the rule-renumbering restructure (last seen at `14f82426c`); the cascade is `shutdownReclaimOutcome`, a switch whose five arms (`!ok`, unconditional, empty token, mismatched token, default) map one to one onto rules 11-14, with rule 10 the preceding four-row precondition and rule 15 the outcome assertion. The two `absent` rows collapsed into one arm, so no count ever corresponded.
- **There are TWO mandatory-field wire breaks, not one.** Rule 10 on `Shutdown` (the `ShutdownRequest{` sweep, 64 literals, all `adapterv1.`-qualified) and rule 1 on the five bind-sequence RPCs. The second sweep's grep MUST carry the `adapterv1.` qualifier, spans tiers 1, 3, 4, 7a, 8, 9 and 10, and returns no `BindAttempt` and exactly one `MidSession: true` literal (`pkg/adapter/files_updated_test.go:80`), which rule 1 admits unchanged with no token. Stage the grep, never a count of literals or files.
- **`Client.shutdown` (adapterclient/client.go:813) is the builder behind `Shutdown` (:807) and `ShutdownRecycle` (:860) and behind nothing else,** which is what makes `unconditional_teardown` unforgettable by a caller and keeps rule 10 from turning the §11.4 revoke fan-out (`cmd/lenny-gateway/user_revocation.go:129`) into an `INVALID_ARGUMENT`. There is exactly one non-test `adapterv1.ShutdownRequest{` literal, at :814.
- **`stageWorkspace` has FIVE error returns before the `PrepareWorkspace` send** (rewrite error, nil `Blobs` with an original `uploadFile` ref, `ParseURI`, `Blobs.Get`, `io.ReadAll`) and a sixth that is the send's own error. The send is the last statement and is guarded by `len(uploads) > 0`, so it cannot fail on an upload-free plan at all. The corrected wording is "raised before `PrepareWorkspace` is sent, through a source-rewrite error or a blob-store failure".
- **The `ReportSessionScrub` proto comment's sentence boundary falls MID-LINE.** `schemas/lenny-adapter.proto:310` ends "... recycling cases alike. The outcome is", so SCHEMA-1 gives those three words to the opening-sentence block and quotes whole physical lines throughout. The neighbouring RELEASED/LEAKED block also ends mid-line, at a clean sentence boundary, and is mechanically replaceable as it stands.
- **`docs/reference/adapter-contract.md:393` is the page's ONLY absolute spec link, and its form is the mirror image of DOCS-2's:** the number rides in the link text (`Spec §15.4 -- Translation Fidelity Matrix`) and not in the anchor. DOCS-2's precedent clause claimed the opposite and is deleted; the staged links themselves are correct.
- **`tests/claim-map.json` holds 76 claims, 32 WIRED, 24 UNWIRED and 20 ABSENT,** which is what the 0080 impact row states before SCHEMA-1's three rows. Every `coordination_generation` fence row is `UNWIRED` with `deferral_id: "R16"`. Neither tier-0 claim-register gate checks that a row's `surface` paths exist, so landing the `ABSENT` row at S9 while its tier-3 and tier-10 surfaces arrive at S22 turns nothing red.
- **`TestPerReleaseSessionCountDrainAgrees_F5231` requires the §12.6 PROSE read clause specifically.** Its backticked DDL alternative does not match today, because the DDL comment writes `non-vm-restart` without backticks, so deleting the prose clause alone fails the case. `grep -rn sessions_served tests/ --include=*.go` reaches both tier-11 carriers and neither of the ten `pkg/` and `migrations/` ones.
- **The non-spec lane's delta baseline is commit `14f82426c` (2026-09-17, "baseline before open-decisions firing 8"), never a `cp-snap` snapshot.** The spec-lane snapshots differ from the live tree in `spec-changes.md` alone, so using one for the non-spec lane hides roughly a thousand changed lines. `non-spec-recheck-r2` is byte-identical to the live proposal; its usable predecessor is `non-spec-recheck-r1-prefix`.
- **`Runtime.Close` does NOT abort on a cancelled context in any of the three implementations** (`embedded.go:188` ignores it, `mcpruntime.go:266,:312-324`, `socketruntime.go:435-467`), and `resolveShutdownGrace` falls back to the implementation default when the deadline has passed. CODE-2's rollback close on an already-cancelled inbound context therefore still performs the close.
- **§10.1.4 pass 1 is `deregisterStartedSessions` at `pkg/adapter/slotsession.go:375`, not in `holdstate.go`;** `holdstate.go:190` is only the call site. The claim made about pass 1 (one `s.mu` hold across every member, no guard) is true of the `slotsession.go` body. The shipped discards CODE-6 replaces are `holdstate.go:249` and `:254`.
- **The adapter metric gate runs CODE → catalogs, never catalogs → code,** so SPEC-6's §16.1 row at S6 with no registration until CODE-9 at S10 fails nothing, and `spec161Metrics` is a hand-transcribed slice reconciled against `MetricCatalog()` alone. `adapter_metric_catalog_test.go:92-116` admits a new adapter metric only when it reaches both `docs/reference/metrics.md` and the §16.1 catalog, which SPEC-6 and CODE-9 together satisfy.
- **`spec/11:200-225` (§11.3 Timeouts and Cancellation) carries hard-coded non-configurable figures too,** so "not configurable" is not why the ten-second window is absent from it; an added row was judged below the bar because the table claims no exhaustiveness. `spec/11:264` already states "SIGTERM to agent, wait up to 10s, then SIGKILL" and §10.1 states no close window, so the staged sentence fills a gap rather than overriding a figure.
- **The §7.4 mid-session admission guard is real and tenant-scoped:** `s.store.Get(ctx, tenantID, id)` then `s.podRegistry.Get(row.ID)`, with a nil bind answered `TARGET_NOT_READY` before any adapter RPC (`upload_to_session.go:72,:75,:112-121`). Its two adapter calls at `:128` and `:134` are the gateway's only §7.4 mid-session callers.
- **CODE-8's fail-closed argument checks out line for line.** `failPhase`'s `leaseAssigned` gates `releaseCredentials` alone with `b.drain` outside the block; `Binder.Launch`'s closure passes the literal `true`; `Binder.Prepare` sets `leaseAssigned` only after the `assignCredentials` call site, so `failPhase` releases nothing on that arm. Skipping it on `SLOT_BIND_ALREADY_STARTED` avoids stripping the live session's own §4.9 leases.
- **`pkg/gateway/externalapi/openapi/openapi.json` is the real path (the lens briefs name a path that does not exist) and it enumerates NO REST error code at all;** its only error enum is `category` and `retryPolicy.nonRetryableFailures` is an open string array. Re-verified three times this window.
- **The §16.1 counters add no label dimension.** `lenny_slot_compensation_superseded_total` takes `pool` and `k8s_pod_name`, `lenny_slot_shutdown_untokened_entry_total` takes `k8s_pod_name`, both already carried by shipped session-slot series, so cardinality is per-pod. No adapter-registered metric carries a pod label today, but `Server.podID` exists, so the label is fillable.
- **The spec-changes Edge-cases section now carries TWELVE bullets, not the nine two earlier docs-alignment runs walked.** Re-count rather than reusing a number; eleven of the twelve resolve to staged §4.7.1 rules 1-15, the §5.2 disposition table, the §5.2 residue paragraph or SPEC-5's §15.1 row.
- **The whole-file sentence-repeat sweep over `spec-changes.md` alone (90+ normalized characters) returns exactly TWO repeats,** both the verbatim-quote/replacement pairing the staging format requires: the recycle-disposition sentence and the `ReportSessionScrub` addressing sentence. A sweep over the four proposal files together returns six. The two counts have different scopes.
- **The `receiving_uploads ──→ running` trigger has exactly TWO carriers tree-wide and neither is a diagram:** `spec/06:152-153` (SPEC-4) and `docs/reference/state-machines.md:235` (DOCS-1). No SVG under `docs/assets/diagrams/` carries the per-slot sub-state machine; the `receiving_uploads` hits in `pod-warm-path.svg` and `sdk-warm-path.svg` are session states.
- **The §4.7.1 entry-creating enumeration is exhaustive against the tree, re-derived once more with call sites:** `ensureSlotPaths` (`staging.go:134,:181,:337`), `assignCredentialsSlot` (`slotcreds.go:26`, reached only from `credentials.go:74` = `AssignCredentials`) and `claimSessionSlotUnderLock` (`slotsession.go:75`, reached from `session.go:111`, `resume.go:50`, `sdkwarm.go:217`).
- **`ConfigureWorkspace` is issued from `Launch` at `binder.go:1009`, after the tokened sequence,** so the ordinary SDK-warm bind never runs on an untokened entry and never makes a compensating reclaim answer `superseded`. A shard spent a pass on this; do not re-derive.
- **`status.Code` in grpc v1.80.0 resolves through wrappers via `errors.As`,** so CODE-5's new `Aborted` arm does fire through `*SlotBindError`.
- **The staged handler has TWO returns that bypass `answerShutdown`,** not one: the empty-session-id rejection at `pkg/adapter/session.go:228-231` precedes everything, and it is a tree fact rather than a numbered rule. Any "single exception" phrasing is wrong; the enumeration has one home, the `answerShutdown` rationale paragraph.
- **The SDK-warm `noteRuntimeStarted` call is on the `fresh` arm only** (`sdkwarm.go:217,:255-262`), the non-fresh arm being the idempotent repeat of a session already recorded, so CODE-1's `live := removed && s.runtimeHoldsLocked(sessionID)` cannot silently withhold the report at an ordinary SDK-warm session end.
- **Defer order in CODE-1's removing arm is correct as staged.** `defer unlockSlot()` registers before the `completed`-gated `defer release()`, so LIFO releases the hold first and unlocks the guard last, and `answerShutdown` runs before both because it is evaluated in the return expression; the scrub is a goroutine, so nothing deadlocks against the held guard or `s.mu`.
- **`tests/spec-map.json` registration and the claim-register arithmetic in `## Files touched` are exact,** checked file by file (slotbinder_test.go, client_test.go, slotsession_test.go, sdkwarm_test.go, holdstate_test.go, concurrent_workspace_test.go, recycle_scrub_path_test.go and the three directory entries).
- **A near-exhaustive citation sweep of the non-spec staging holds.** Roughly 170 `file:line` claims across `pkg/adapter`, `pkg/gateway`, `cmd/`, `schemas/`, `docs/`, `spec/`, `tests/` and `migrations/` verified with no false attribution and no meaning-changing drift; a later mechanical pass over 261 extracted citations resolved 213 uniquely and found the remaining ~20 to be unambiguous bare basenames. Do not re-run the whole sweep.
- **`scripts/check-markdown-links.sh` shells out to `markdown-link-check`, skips silently when the tool is absent, and does not validate GitHub blob-URL fragments,** so DOCS-2's two absolute spec anchors are ungated. Both are nonetheless correct GitHub slugs.
- **`assertFieldSet` exists only in `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:259`,** so SCHEMA-1's "the only closed field set over the messages SCHEMA-1 opens" holds; `checkpoint_stream_wire_test.go:151`'s length check is over `CheckpointStart`, which SCHEMA-1 does not open.
- **`UnhealthyThreshold` is `int((maxConcurrent+1)/2)` clamped at 1, the leak count is persistent and released only by `Forget`, and the failure count ages out of a rolling five-minute window.** Every arithmetic claim CODE-5, summary decision 34 and the accepted-failure bullets make against these is correct.
- **`summary.md` carries its required sections in the required order and `**Accepted failure modes.**` stays at the end of `## Non-goals`,** deliberately: its subject is the priced residues and its body is a pointer at the spec-changes Edge-cases section. Three passages outside `## Impacts on other proposals` name another proposal (0080, 0078/0079, R12); each is a citation of ownership and each of those proposals already holds an impact row.

### Traps

- **A residue-disposition cell is changed in the §5.2 table and NOWHERE ELSE, and atomicity in `**The
  registry critical section.**` and nowhere else.** A finding that §7.1, §6.2, the hold paragraph, the
  `**Slot cleanup:**` bullet, §15.4 or Design "does not say" a report, `leaked` or hold fact is
  answered by the table, and one about a cleanup's residue or what ends it by the one sentence in the
  paragraph after the table. An uncovered case is ONE TABLE ROW; residue is never a column or a
  per-row clause.
- **MISTAKE, FIVE edit passes in four rounds on ONE disposition-table column, the residue column, and
  the answer that ended it was deletion (`[prune.3.fix.1]`).** Per-row residue had to stay consistent
  with the action list and with a tree where `RemoveTree` returns one error over four directories and
  no per-slot process-group kill exists, and every re-key became the next finding. A finding about a
  cleanup's residue is answered by the one post-table sentence or not at all.
- **The §5.2 disposition table's row keys are performer-scoped, so read the KEY COLUMN before
  concluding a case is covered.** Two shards read the body and misjudged an unconditional `Shutdown` on a
  pre-`running` slot. Never answer a key-column gap with prose at a citing site.
- **The staged §4.7.1 block governs itself.** Its "refers to a rule by its number and name rather than
  restating it" and "the only statement of that atomicity" are the cheapest handle on a duplication
  finding. Conversely, never take a "stated once here" sentence at face value: grep its distinctive
  clause across the whole directory.
- **The pre-`running` boundary was rewritten WHOLE in `[redesign.6.fix.1]` after seven facet-at-a-time
  rounds; do not reopen it a facet at a time.** §6.2 states no classification, list or trigger and cites
  §4.7.1's record step; reintroducing a classification, a second definition or a retired phrase is the
  same defect. A finding on this boundary is answered by the home sentence or by the grep in `### Settled`.
- **MISTAKE, THREE rounds choosing an outcome predicate for the `slot_cleanup ──→ released` fence
  annotation.** No member of that family is correct, so both `slot_cleanup` fence entries are bare `(see
  §5.2)` pointers; put no trigger back. "Cleanup timeout exceeded" is none of the table's `leaked`
  grounds. Sweep both fence siblings and state-machines.md in one edit.
- **MISTAKE, FOUR attempts at ONE §15.4 paragraph, each answering the last one's defect.** A per-rule
  wire table, then a rule-quantified criterion unsatisfiable when a request meets rules 5 and 6 at
  once, then a domain sentence duplicating §4.7.1's preamble in other words. The fifth form is the
  whole rewrite from the settled decision (two sentences, `[redesign.2.fix.1]`); fix it only as a
  whole. §15.4 carries no rule number, condition, selection predicate or domain; rule 2 alone
  delegates its status there. §15.4 completeness findings are refuted six times.
- **Do NOT enumerate the cleanup's acts anywhere but §5.2's `**Slot cleanup:**` bullet.** A second
  list, even one opened with "includes", goes stale. The failed-cleanup residue is NOT all on disk: a
  failed process-group kill leaves live processes sharing `/tmp`, cgroup and network with co-tenants.
- **Do NOT unify the two pre-`running` cleanup PERFORMERS.** The adapter's failed-start handler reaches
  no `leaked` state by design: `releaseSessionSlot` discards `removeSlotTree`'s error, the compensation
  answers `absent`, CODE-4 reads that as completed. For the same reason write no completion guarantee
  into the `DemoteSDK` row or the reporting rule.
- **Do NOT concurrency-qualify §7.1's §7.3 re-attach item.** `reserveResumeSlot` returns empty for
  `MaxConcurrentSessions <= 1` and `resumeOnPod` returns with no rollback, so a failed one-session
  re-attach drains NOTHING; "the pod retires" holds only for the §15.1 start via `failPhase`.
  `Binder.Resume` issues no Prepare, Finalize, RunSetup or AssignCredentials.
- **Do NOT reinstate the deleted complement sentence "A cleanup that reclaims a slot the pod's shared
  runtime process was given is a session release like any other and reports its outcome."** The `**Scrub
  model.**` biconditional is the home. Do NOT delete the one-report sentence: rule 8 and §7.1 cite it.
  The `DemoteSDK` slot-release wording double-counts without that amended opening.
- **MISTAKE, and it is the most expensive of this window: re-keying CODE-1's cleanup-outcome report
  onto `errors.Join(closeErr, treeErr)`.** One failed `os.RemoveAll` at an ordinary session end books a
  never-pruned leak and stamps `drain-request` at threshold 1. The report stays on `closeErr` with a
  `slot_tree_removal_failed` warn. Do not qualify `leaked` semantics instead.
- **`leaked` is never a free adjective for a cleanup outcome in this codebase.** It means retained
  occupancy. Do NOT make a surviving `credentials.json` `leaked`: that pins occupancy above zero and
  blocks the whole-pod scrub that would collect it. `RemoveTree` returns one error over four directories,
  so a narrower reading is unimplementable.
- **TRIED AND WITHDRAWN, then REINSTATED: the failed-cleanup terminal on the reclaim hold.** Deleted
  once as the scrub model's, reinstated because `leaked` is gateway-side and fences nothing on the pod.
  The hold and `leaked` occupancy stay separate objects. Ending the hold at the cleanup TIMEOUT is
  rejected: it readmits a successor onto a tree `os.RemoveAll` is deleting.
- **Do NOT flip the `Shutdown` compare so a named reclaim removes an untokened entry, and do NOT
  reclassify the untokened `superseded` as a reclaim not acknowledged clean.** It may be a live
  successor's `StartSession` entry. Only the gloss widens, and every spelling of the `superseded` gloss
  (rules 13 and 15, the §16.1 row, the docs mirror) moves in one edit.
- **The remedy for the two accepted-residue findings is ONE SENTENCE EACH, never a request to build
  remediation position 2** (durable pending-reclaim record, reaper, tree generation, bind-scoped lock).
  The gateway-crash and self-recreated-entry residues are recorded in the spec file's Edge-cases section;
  rule 13 carries the untokened-entry residue. Sweep `### Open` for FILED items before convergence.
- **MISTAKE: widening one clause of spec/06's two-clause claim-deletion partition gave one input two
  answers; both clauses move together.** The rule sits at five sites across spec/04 and spec/06. Do NOT
  touch the Recycle-edges `a failed session` clause or add a "treated as a failed session" exception.
- **MISTAKE nearly filed, repeatedly: "SPEC-4's re-key introduces new pod churn" and "SPEC-4 removes
  the one-session-only control".** `ProjectOccupancyPhase` already drains a claim-less `claimed` pod on
  either recycle setting, and returns `("", false)` in warm-fill phases. §4.6.1's `sessionPolicy` input
  survives on `scrubProfile` and `recycle.maxPodUptimeSeconds`.
- **The zero-value hazard of `mid_session` has two faces, and rule 9 (first frame) exists for both.** A
  per-frame pairing or disagreement predicate refuses every multi-chunk §7.4 upload, since later frames
  carry an empty token and a default-false marker. A recorded WATCHOUT is filed or refuted, never
  carried. Only `PrepareWorkspaceRequest` gains the field.
- **MISTAKE: the staged `ensureSlotStateLocked` create arm called `s.newSlotStateLocked(slotID)`, a
  symbol that exists nowhere.** The shipped create branch lazily seeds `s.slots`, runs `resolveSlotPaths`
  (the `ValidateSlotID` traversal guard) and `slotlayout.EnsureTree`, and returns both errors: keep it
  inline plus the stamp. `Server.reclaiming` and `Server.slotGuards` need lazy-init too.
- **MISTAKE, three rounds on ONE sentence: the per-slot guard's scope written as an RPC enumeration.**
  It is a PREDICATE plus a derivation table; the list omitted `Resume`. `defer unlockSlot()` registers
  BEFORE `defer release()` (LIFO). Never drop a guard-table entry. On expiry a destructive section
  proceeds unguarded and an admission RPC is refused.
- **WATCHOUT: `DemoteSDK` names two different things and the bare name reads as the wrong one.**
  `SDKWarmRuntime.DemoteSDK` is what CODE-2's refusal arm calls; `Server.DemoteSDK` deregisters through
  `releaseSessionSlot` and must not be used there. Retiring `deregisterSlot` breaks two callers in
  `podmcp_arming_internal_test.go`; replace them inline under `s.mu`.
- **Process and refuted index.** `leaseAssigned` false proves only that `failPhase` skips the
  session-wide release; `fakeAssigner.released` records `ReleaseSession` alone. A self-announced
  "correction" paragraph is the least-checked prose. Refuted: `slotFailureWorkspaceFinalize`, SCHEMA-1
  field numbers, capacity and security families. Snapshots: order by MTIME; `slot_was_started` is phantom.
- **MISTAKE nearly filed, the security family.** Refuted dresses: `superseded`/`absent` as pod
  self-reports exempting a slot from leak accounting; a compromised adapter refusing every teardown;
  a late `StartSession` orphan holding credentials; the withheld report relaxing `maxSessionsPerPod`.
  Bar: the lie must make the gateway do something new; `leaked = err != nil || !cleanly` is shipped.
- **Do NOT re-derive the capacity family.** Refuted or accepted dresses: hold refusal counted against
  the unhealthy threshold (`UnhealthyThreshold(2) == 1`); the §10.1 fence blocking a compensation;
  Redis-reset occupancy loss; Fresh-workspace guarantee versus the hold; the §10.1.4 N-identifier
  hold; resume-path latency. Each is an accepted edge case, the shipped baseline, or a barred close.
- **Do NOT file the §15.4 "`Shutdown` is not held" carve-out against the wide hold predicate.** The
  quoted wording no longer exists in the staging (round 5 reduced that sentence to a pointer), so read
  this as a refuted filing rather than a live quotation; the guidance itself still holds. During
  the hold the entry is deregistered, so a `Shutdown` resolves nothing and answers `absent` either
  way. The same vacuous-resolve reading kills every "hold reaches a non-bind RPC" dress: those
  handlers resolve through `slotStateLocked`/`boundSlotState`, never `ensureSlotStateLocked`.
- **`superseded` for a tokenless entry is CORRECT on the `Shutdown` path and WRONG on the bind path.**
  Rule 5 fires only when both tokens are non-empty and differ. Do not sweep "carries no token" out of
  rule 13, and do not import it into the bind-path gate.
- **Do NOT put the reclaim hold back inside the admission cascade as "rule 1".** Rule 2 cites §5.2 for
  its scope and §4.7.1 must never restate that scope. CORRECTED (`[non-spec-recheck-2.1.fix-G1.1]`): the
  old parenthetical "(a `Checkpoint` resolve is refused)" no longer describes the staging. The scope is
  now the seven requests §4.7.1's admission rules govern, and a `Checkpoint` under a hold answers
  not-found on its own path. The hold's precedence over rule 3 still matters: a `mid_session` request
  during a hold would otherwise get the permanent `FAILED_PRECONDITION`.
- **"Carries `bind_attempt`" means the message TYPE declares the field, never that the value is
  non-empty.** Bare proto3 scalars have no presence. That reading is what excludes `StartSession` and
  `ConfigureWorkspace` from rule 1 with no carve-out clause.
- **MISTAKE, two rounds on one sentence: per-RPC reachability narration inside §4.7.1's Admission
  paragraph.** Deleted, because each rule selects by its own condition; a third enumeration is the
  same defect. Do NOT delete or harmonize the other "Every other RPC ... outside these rules"
  sentence, which is admission-scoped and correct.
- **Do NOT move the §7.2 reclaim after step 4.** It sits at step 3, before the
  `coordination_generation` bump; reordered, every mid-resume reclaim fails the generation check.
  Also refuted: step 3 versus `handleDelete`/`handleTransition`, because §7.2's pod release is itself
  unimplemented today.
- **MISTAKE: checking proto field-number FREENESS and stopping there.** `buf breaking` is silent on
  additive edits, while `TestShutdownMessagePostRemovalDescriptor_spec_4_1` pins the field SET; grep
  `tests/` for `Fields().Len()` and `assertFieldSet`. Related: an `awk '/^message X \{/,/^\}/'` range
  over one-line `message AssignCredentialsResponse {}` falls into `RotateCredentialsRequest`.
- **Do NOT hand-edit `tests/claim-map.json`.** It is generator output under a tier-0 byte-identity
  gate; author in `EXPLICIT` in `scripts/seed-claim-register.py` and re-run in the same commit. Also
  do not copy a neighbour's generation comment into a SCHEMA-1 field:
  `adapter_proto_generation_scope_test.go` pins that sentence's count.
- **MISTAKE the credit-inventory gate punishes: creating a `slot*_test.go` file.** The regexp matches
  `slot` anywhere in the basename, and a body match (`slotstate.`, `ClaimSlot(`, `BindReservedSlot(`
  etc.) pulls an existing file in. Listed files owe a `tests/spec-map.json` credit per annotated
  section; `creditsMissing` accepts a whole-file credit in both modes. That question is settled.
- **Do NOT open an `Aborted` case in `SlotBindError.Reason()`.** Rule 8's rollback relies on the
  transient default; the arm belongs in `isTransientPodClaimError`, tested in
  `resume_setup_demotion_internal_test.go` (`start_test.go` is an external package). Never
  `errors.Is` an adapter sentinel from the gateway: only the status code crosses gRPC.
- **Do NOT delete the `ReportSessionScrub` mention from the `adapter-contract.md` `Shutdown` row.**
  `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs four substrings on ONE physical
  line (`lineContaining`). DOCS-2 prose cites no spec section numbers, and DOCS-3 adds no row: the
  two adapter `ErrorCode` values never reach the client-facing catalog. Do not re-delete DOCS-3.
- **WATCHOUT: the `release` closure `reclaimSlotLocked` returns must take `s.mu` ITSELF.** It is
  deferred with and without the lock held. `Server.reclaiming` needs a lazy-init guard like `s.slots`
  (tests build `Server` by literal). In §10.1.4 defer each member's release in the pass-2 loop.
  `releaseSessionSlot` reaches no `Runtime.Close`; its synchronous hold covers only waiting callers.
- **Do not re-gate the cleanup-outcome report on `st.started`.** Collapsing the two predicates
  reintroduces the double report through CODE-2's rollback and advances `sessions_served` for a
  session that never ran. The tier-1 "Claimed but not yet recorded" case exists to fail on it.
- **Do NOT simplify CODE-1's three predicates into fewer.** Report and response gates are `live`
  (`runtimeLive`); the teardown stays `st.started`, because CODE-2's rollback fires only when
  `Runtime.Start` returns, so a hung start would serve an abandoned session forever. Do not surface
  `treeErr` unconditionally on the started path: ordinary session ends would become `leaked`.
- **TRIED AND WITHDRAWN: the no-retry rule.** Making the failed attempt non-retryable costs roughly
  eleven mirrored exception clauses across §5.2, §6.2, §15.1, §29 and the error catalog plus a new
  `SlotReason`, and withdraws availability during a partial pod outage. No failure class changes
  retryability. Do not re-derive it.
- **TRIED AND WITHDRAWN, five alternatives to the per-attempt epoch.** A caller-minted token carries
  no order, so an adapter that lets a later request adopt or re-stamp an entry lets a straggler fence
  the live successor out. Stamp-once first-writer-wins is what replaces the order. Never add
  adoption, re-stamping, or a response-carried fence.
- **MISTAKE, four separate occurrences: the bound/started drift.** `bound ⊋ started`:
  `assignCredentialsSlot` binds, only `claimSessionSlotUnderLock` sets `started` (with `sessionID`).
  Check every sentence naming the runtime teardown against §4.7's "a session whose start the adapter
  has admitted", and every `exited_cleanly` argument against both boundaries. Fix the phrase only.
- **MISTAKE, the one-caller reading a second time: "`StartSession` is the only site the
  compensation can race".** `Resume` (resume.go) and SDK-warm `ConfigureWorkspace` (sdkwarm.go) reach
  `claimSessionSlotUnderLock` and `noteRuntimeStarted` too, and CODE-4 compensates `Binder.Resume`.
  Check any sentence naming `StartSession` alone against all three.
- **MISTAKE, a FOURTH occurrence of the unscoped `leaked` terminal, and the most expensive.** The
  `**Scrub model.**` paragraph holds on a pod of either concurrency, so any sentence appended there
  naming `leaked`, the replacement trigger or the gauge needs its own concurrency scope. The staged
  paragraph after the disposition table carries that scope; check every append against it.
- **Do NOT put the credential-lease release back inside `compensateFailedSlotBind`,** and do not
  make it conditional on `assignSlotCredentials` having succeeded: it mints per provider and returns
  on the first error, so a failed stage can hold leases. The release is unconditional in the wrapper
  that owns the lease-minting stages; `ReleaseSession` is a no-op for a session holding none.
- **MISTAKE: CODE-2's rollback answering `codes.FailedPrecondition`.** It made a transient
  start-versus-reclaim race a permanent non-retryable 422. The answer is `ABORTED`; do not make the
  superseded refusal permanent either, and do not carve `slotFailureSessionStart` out of
  `slotfailure.go`'s `Reason()` switch. Tests far from CODE-2 assert the code, so grep its name.
- **Do NOT "simplify" the split back to a single bound.** `contextWithGraceDeadline` returns the
  parent unchanged for a non-positive grace, so `deadlineMs: 0` makes a completed reclaim unreportable
  as clean. The compensation sends `budget/2` under `context.WithoutCancel(ctx)`, which is what
  survives a cancelled resume; the tier-1 per-stage assertion pins both. Justify with `MCPRuntime`.
- **The §5.2 append can silently redirect four tier-11 gates, and only case sensitivity is stopping
  it.** `lineContaining` returns the FIRST match and the append precedes every §5.2 bullet. Do not
  recase "whole-pod replacement trigger" or add capitalised anchor strings to the append; a future
  gate on "Slot cleanup" must anchor below it. The §4.7 rows stay one physical line.
- **Do not file §11.4 revoke step 3.** Family index of pre-existing looseness this proposal restates
  rather than creates, each already false on a co-tenanted pod under the shipped `!boundRemains`
  gate: spec/12's and spec/15's terminate-delivery sentences, §5.2's "Recycling and integration
  levels" sentence, runtime-author-guide lifecycle.md, §4.1's retained second sentence, "session mode".
- **MISTAKE, twice, the same shape: a design reversal leaves behind every sentence a grep on the new
  vocabulary misses.** A fix that rewrites the narrative and leaves the staged normative block
  asserting the opposite is this proposal's most repeated failure. Judge a fix by grepping the staged
  blocks for the claim, never only the commentary.
- **The glob `*spec-changes.md` in this proposal directory matches BOTH `.spec-changes.md` and
  `.non-spec-changes.md`,** so line numbers read through it are wrong; use full filenames. A one-line
  grep misses a phrase wrapped across source lines, and `awk '/^message X/,/^}/'` overruns a one-line
  proto message into the next one. Correct a ledger claim with `CORRECTS`, never in place.
- **MISTAKE, cost one round: reusing an old gate clearance after the staging moved under it.** The
  archived "`spec_28_register_writers_test.go` pins a §12.6 sentence this proposal does not touch" was
  true when written and went false the moment SPEC-3 acquired the §12.6 prose replacement; round 7's
  applicability lens then published "the spec-only edits break no shipped gate". Re-grep the pinned
  substrings whenever SPEC-3's §12.6 text changes; "spec/28 needs no edit" and "no spec/28 gate goes
  red" are different claims and only the first was ever true.
- **MISTAKE nearly filed in FOUR separate rounds: the SPEC-4 §6.2 fence edit's anchor form.** It gives
  one block after "replace the trigger list of the `claimed ──→ draining` entry" with no "reads,
  verbatim" original, and the block IS the replacement (it carries "see §4.6.1"). Judged determinable
  every time, because the `Occupancy projection` fence holds exactly one such entry and the §4.6.1
  bullet replacements use the same form. Do not spend a fifth round unless you can show a
  misapplication; an implementor should anchor on the `recycle.enabled: false` substring.
- **MISTAKE nearly filed at least six times: the "on a session release" family outside §12.6.**
  spec/05:488, spec/06:139, spec/16:12 and `docs/reference/state-machines.md:248` are restrictive or
  existential ("the session release THAT DRIVES the count"), so a release filing no report leaves them
  unmet rather than false. Only §12.6's universal "at each / on each session release" needed the
  re-key. Filing one of these owes an argument that the existential reading is unavailable.
- **MISTAKE nearly filed at least four times: a disposition-table row for a RUNTIME-GIVEN slot no
  cleanup reclaims** (the abandoned attempt's late untokened `StartSession`). The table's scope for a slot
  no cleanup reclaims is explicitly the pre-`running` case, §6.2 and §7.1 cite it with that scope, and the running orphan
  is disposed of by rule 13 plus its own Edge-cases bullet. Adding a row breaks the partition.
- **MISTAKE, twice, once costing a third of a pass: §7.2 step 3's flat "the connection that attempt
  still holds" against §7.1's "when that connection is still open".** It is a summary clause inside a
  step that cites §7.1, which is the carve-out the §29.4 step-13 refutation already turned on. If a
  round wants the reduction it deletes the clause from step 3 and never re-words §7.1.
- **MISTAKE, the most expensive of the round-7 reliability pass: reading the wide hold predicate as
  refusing a mandatory credential control.** The answer is the hold's WINDOW, not its predicate: the
  hold opens AT the deregistration, so throughout it there is no entry for `CoordinatorFence`, a §11.4
  revoke, `RotateCredentials` or `ExtendCredentialLease` to resolve, and the hold's only effect is to
  turn a not-found into a transient `ABORTED`. Settled #75 is right about the code and silent on this.
- **MISTAKE: the §12.6 rationale written as "the read trigger moves with the write trigger because the
  gateway performs both in one step".** The one-step grounding is real for the INCREMENT and says
  nothing about where the evaluation point is STATED, so it licensed a second statement of a rule §5.2
  owns. The rationale is narrowed to the write trigger.
- **TRIED AND WITHDRAWN: re-keying §12.6's READ clause onto "on each cleanup-outcome report".** Round 7
  did it; round 10 replaced it with a pointer at §5.2's `**Session count limit:**` bullet, because the
  re-key left one rule with two vocabularies across spec/05, spec/06 and spec/12. Do not re-derive the
  re-key, and do not re-key §5.2's bullet or §6.2's fence edge to match it either.
- **Two bounds on one cleanup is NOT a contradiction.** The `**Slot cleanup:**` bullet keeps
  `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` while the hold paragraph bounds the close by
  the `Shutdown`'s graceful window, else the request's deadline, else ten seconds. The formula has no
  implementation, the staging says it "stands as written", and `removeSlotTree` takes no context.
- **Two neighbouring proto comments look like the same site and stay TRUE.**
  `schemas/lenny-adapter.proto:437` describes the CLEANUP, which does run on every session release, and
  `:468-473` states the `sessionsServed` increment as a gateway-side effect with no trigger. SCHEMA-1
  names both as deliberately unedited. Only :308-310 and :451-452 assert the withdrawn report trigger.
- **Do NOT reinstate the §15.4 lead-in's deleted two-exception clause, and do NOT touch the
  slot-identifier reclaim-hold block.** The block is the single home of rule 2's `ABORTED` status and
  rule 2 points at it; absorbing the deleted clause there re-opens the drift the round-10 design closed.
- **MISTAKE nearly filed twice: "§4.1's commentary cites the §4.7.1 carriage table for
  `unconditional_teardown`, which the table has no column for".** It dies on the table's own `Shutdown`
  row, whose `bind_attempt` cell reads "pairs with `unconditional_teardown` under rule 10".
- **"`ProjectOccupancyPhase` switches on the pod's current phase alone" is a LOOSE SUMMARY, not a false
  citation.** The function branches on `HasClaim` and `Binding` first and reaches the phase switch only
  on the no-claim arm, which is the half SPEC-4 edits. Judged below the bar twice; a third filing owes a
  rule that depends on the wide reading.
- **MISTAKE nearly filed twice: SPEC-6's row deferral wording against the shipped sibling rows.** The
  staged row says "Adapter-side; not scraped until the adapter metrics endpoint is wired" where the four
  neighbours cite §16.9 at length, and CODE-9 claims SPEC-6 carries the wording "verbatim". The row is
  shorter rather than wrong and the gate reads names alone, so the live defect, if any, is CODE-9's
  claim rather than the spec row.
- **The §5.2 table's pre-`running` "an act fails → Entered" cells are NEW `leaked` occupancy the shipped
  `sessionID == ""` arm never produced,** and at `maxConcurrentSessions: 2` one such event retires the
  pod. Not a finding: the acts are four `os.RemoveAll` calls plus the deregistration, the first
  accepted-failure bullet already states the concurrency-2 consequence, and the capacity family is barred.
- **On a late round, read the CO-ROUND shards before finishing, not only the standing context.** Round
  10's fresh lens turned three of its candidates into delta checks and killed one duplicate filing that
  way; the single-source, citations and edit-site shards of the same round repeatedly cover each other's
  near-misses. USEFUL, round 11: reading the round-5 fix groups is what separated "recorded and refuted"
  from "recorded and never filed" on the graceful-shutdown triple, which the standing context alone
  does not distinguish.
- **A newly found carrier of the withdrawn "reports on every session release" universal is added as ONE
  ROW to the SPEC-3 carrier table and NOWHERE ELSE**: not as a Deferred entry here, not as an Open item,
  not as a sentence in a deliverable's rationale. A row's disposition is one of "Staged here",
  "Mirrored by <deliverable>" (and that deliverable must carry the edit) or "Deferred to the non-spec
  loop" with the lane. A sweep that finds a carrier already in the table has found nothing.
- **`schemas/lenny-adapter.proto:256-257` is a THIRD proto site on that surface and is deliberately
  absent from the carrier table.** It says "the adapter emits on release" with no universal quantifier,
  so it falls in the existential family the standing context records as refuted six times. Round 11
  found it and did not file it. A later round that wants it owes the argument that the existential
  reading is unavailable.
- **CORRECTED, three times over (rounds 12, 15, 18): SPEC-4's quoted `occupancy.go` sentence IS in the
  file, verbatim.** "the §6.2 state machine encodes the recycle-versus-one-session distinction in the
  phase the pod sits in at the claim DELETE" is the `ProjectOccupancyPhase` DOC COMMENT (occupancy.go
  around :57-59). The round-11 shard compared it against a different, differently-worded sentence in
  the function body fifty lines down. The quotation is exact, so stop re-checking it; the conclusion
  (not a finding) was right for the wrong reason.
- **Do NOT file the §15.1 endpoint precondition rows or `spec/07:208` as missed SPEC-5 edit sites.**
  Each states the `SETUP_COMMAND_FAILED` cause as a sufficient condition ("surfaces here as") rather
  than an exhaustive one, so widening the catalog row leaves them true. The standing context already
  cut them for budget once; this is the second refusal.
- **Do NOT file `docs/reference/configuration.md:91` or `docs/reference/execution-modes.md:23` for
  glossing `recycle.maxSessionsPerPod` as "counts every session served".** Under the staged
  biconditional a session released outside a `Shutdown` files no report and is not counted, so the
  gloss undercounts, but the shipped handlers already file nothing there, so the inaccuracy predates
  this proposal and is not a behaviour the staging changes.
- **TRIED AND WITHDRAWN: resolving open decision 21 inside the loop.** `[f1.open-decisions-apply.id21]`
  answered it "no", deleted entry 21 from `summary.md` and added a defects-section bullet for its
  residual-state half; `[redesign.2.fix.1]` withdrew all of that, because the human reserved 21 for
  themselves. Entry 21 stands restored as it was at `440cde9ec`, the preamble is back to its pre-firing
  wording and the added defects bullet is deleted. No change file was edited for 21 in either
  direction, and the `f1` ledger blocks stand as the record of what the firing wrote.
- **MISTAKE, the whole arc of rounds 12 through 15 and the reason the residue column no longer
  exists.** Round 12 deleted "The whole-pod scrub or " from the two `leaked`-Entered cells because a
  `leaked` slot pins occupancy. Round 13 found that false at `maxConcurrentSessions: 1`, where no
  `leaked` sub-state exists and the ending session's own recycle-carrying `Shutdown` runs the scrub,
  and re-keyed the whole column from enders to residue. Round 14 found two commentary sites still
  describing it as an ender column. Round 15 found the re-keyed cells omitted the session a failed
  close leaves open, in two rows and in two different shapes. The prune then deleted the column. Any
  future text that decides PER ROW what a cleanup leaves or what ends it is the sixth occurrence.
- **MISTAKE, cost one round: the hand redesign `9c59dabec` narrowed the §15.4 pointer while
  shortening it,** rewriting "the numbered rules an adapter applies to it" into "...to a request that
  carries it". A hand edit that shortens a normative pointer can narrow its domain silently, and this
  one excluded the two token-less requests rule 8 is written for. After any hand restructure, re-read
  the pointers it shortened against the domain their target declares.
- **Do NOT re-add the §29.4 complement clause or re-describe two statement sites.** The trimmed step
  13 is "On a pod serving concurrent sessions this step occurs only under the condition the [§4.7]
  `Shutdown` row states for the graceful-shutdown signal." and nothing more. The commentary under it
  ("so a later refinement of the condition lands in one place") is now true of the text; editing it
  back, or restoring "an end that leaves a bound co-tenant writes no frame" for reader convenience,
  re-opens the triple three lenses missed for seven rounds.
- **The DOCS-2 `DemoteSDK` row is the one LICENSED DOCS RESTATEMENT in the proposal,** because
  `doc-content.md` bars it from citing a spec section. That makes it the single site a spec-lane
  retraction leaves stale, and it is deliberately the only carrier of the demotion's fresh-entry
  consequence since round 16. Do not file the asymmetry with §5.2, and re-check the row whenever a
  §5.2 hold sentence or the rule-4 predicate changes. No tier-11 gate reads it.
- **Three tier-11 gates resolve `| \`Shutdown\` |` and `| \`ReportSessionScrub\` |` by FIRST match
  inside the `### 4.7 ` slice, and the §4.7.1 carriage table adds a second matching line.** They stay
  green only because the Gateway → Adapter RPC table precedes the §4.7.1 insertion point. Moving the
  §4.7.1 block above the RPC tables breaks all three silently. Every §4.7 row and the §12.6
  `agent_pod_state` paragraph must also stay on ONE physical markdown line.
- **Do NOT "fix" the §7.1 paragraph's unrendered markdown links by moving it out of spec/07's code
  fence.** The fence runs lines 5-54, the shipped atomicity paragraph sits inside it with three such
  links, and the staged insertion anchors on the step-8 continuation line. Filing it owes an argument
  against the shipped precedent; three rounds have declined it.
- **`pkg/adapter/gatewaycontrol/scrubreport.go` holds TWO comments on this surface and only one is a
  carrier.** `:13` (`SessionScrubOutcome`) states the CLEANUP runs on every release and is
  deliberately excluded; `:72` (`Client.ReportSessionScrub`) states the REPORT does and is a table
  row. The same line-level distinction applies to `schemas/lenny-adapter.proto` (`:437` cleanup,
  `:257` existential, versus `:309` and `:452` universals). Do not file the excluded ones.
- **`spec/05:555` ("The retry is always assigned to a **new slot** on the same pod") is NOT a
  contradiction of "names the same slot identifier".** spec/05:395 fixes the slot identifier as the
  session identifier, so a retry of the same session on a new slot reuses the identifier. Two rounds
  could burn a finding here.
- **§12.6 is headed "Interface Design", not "agent_pod_state table schema".** The `sessions_served`
  prose and DDL sit at spec/12:481 and :494 inside a section running 369-766, and the proposal's
  parenthetical names the bolded sub-block. Three lenses have had to record this; do not file it as a
  wrong-section citation.
- **The docs-alignment lens's two accepted residues (gateway-crash-stranded entry, self-recreated
  entry after an unconditional teardown) have been filed and refuted in rounds 12, 15, 18 and 20.**
  The mechanical check is `awk 'NR>=243' spec-changes.md | grep -n
  "crash\|recreat\|empty workspace\|unstartable\|stranded"`, which returns only the two §15.1
  exclusion sentences. A further filing costs two verifiers and closes nothing.
- **The security lens's candidate space is EXHAUSTED across rounds 15, 18 and 20.** Its one live item
  is the §4.9 timer-versus-credential-file ordering, a recorded Open rather than an unfiled defect,
  and every "an in-pod attacker sends X" dress collapses into the standing Open about the adapter's
  gateway-facing gRPC server being reachable from the agent container. Route the live item through the
  `### Open` pre-certification sweep; a fresh firing returns the same single item.
- **Do NOT file SPEC-6's adapter row for not carrying the `lenny_adapter_*` prefix or the shipped
  §16.9 deferral phrase.** No spec text or lint makes either normative and §16.9 enumerates no
  individual metric, so the finding would be preference. A lens that reaches for it needs a rule to
  cite first. The live defect, if any, is CODE-9's claim that the row carries the wording "verbatim".
- **Do NOT re-key disposition row 3 so that any failing act enters `leaked`.** The report is keyed on
  `closeErr` alone by recorded decision, the prune bars cell re-keys, and the round-20 remedy for the
  clause that contradicted the row was a four-word deletion. The gateway predicate
  `sbe.Leaked = cerr != nil || !cleanly` already agrees with the table; do not "fix" it either.
- **WATCHOUT: `completed` is POD-SIDE ONLY, and the gateway-side predicate is never written as
  "completed", "did not complete" or "incomplete" anywhere in the proposal.** A site that means the
  adapter answered with a clean exit writes "acknowledged clean"; one that means every act returned
  without error writes "completed"; one about what a cleanup leaves on the pod writes "a cleanup that
  did not complete". "Unacknowledged" alone still means unanswered, which is one of the two ways a
  reclaim is not acknowledged clean, and the three sites that keep it mean that narrower case. The
  one-word form was tried and withdrawn: a qualifier ("completed on the pod") is dropped by the next
  paraphrase, and six repairs each fixed one facet before the split.
- **MISTAKE nearly filed (round 21): a compensation whose RPC times out while the adapter's cleanup
  then succeeds books a gateway-side `leaked` for a healthy slot.** Not a finding: the
  over-approximation is the staged Design's stated direction and the reclaimer exists, because
  `ceil(maxConcurrentSessions / 2)` whole-pod replacement retires the pod. A filing owes an argument
  that the slot has no reclaimer at all.
- **WATCHOUT: any docs page using "slot" in prose must link the glossary entry,** enforced by
  `TestPagesUsingSlotLinkTheGlossaryEntry` in
  `tests/tier11_docs/slot_definition_glossary_reconciliation_test.go`. DOCS-2's new bind-attempt
  paragraph uses the word and `adapter-contract.md` already links the entry, so nothing is owed today;
  a NEW docs page in the code lane trips it.
- **"staged at position 2 of the gateway-runtime-comms remediation plan" is NOT a false citation, and
  two rounds have declined it.** That plan has steps R1-R25 and waves and no "position" concept, but
  "position" in this proposal's own vocabulary means a QUEUE position, which makes the claim a
  forward-looking planning statement. A round that wants it owes a reading that distinguishes plan step
  from queue position.
- **A hand prune can leave a lens reading a line number that no longer exists.** Rounds 13 through 20
  each recorded a table line map that the next prune invalidated, and `[spec.20.fix-G1.1]` had to
  correct a ledger UNVERIFIED for quoting both a deleted clause and a stale line. Cite the staged
  block by its heading and its distinctive phrase; the standing context records no line numbers into
  the proposal files for this reason.
- **WATCHOUT: `pkg/controller/warmpool/occupancy.go` carries the phrase "one-session-only
  invariant",** which paraphrases the §4.6.1 sentence SPEC-4 deletes as false against that very
  function. No spec or docs deliverable touches it and it is under no name in the Deferred list. It
  is a code-lane comment, so the spec loop cannot file it; the non-spec loop decides whether CODE-3
  or another step re-keys it.
- **WATCHOUT: the word "teardown" carries TWO REFERENTS inside one staged block and a lens will want
  to file it.** The §4.7 `Shutdown` row names two teardowns (slot release, runtime teardown) and then
  says every request states "which teardown it asks for", while rules 12 and 14 both perform both.
  The rescuing reading is that the phrase names the REQUEST FORM (compensating versus
  unconditional), which is how rule 12 is titled; the next sentence sends the implementor to the
  numbered rules, so no rule is misimplementable. Below the bar as wording; do not spend a round
  unless you can show a rule goes wrong.
- **WATCHOUT: rule 12's "addressed to whatever entry the adapter holds" scans as POD-WIDE on a
  concurrent pod, and is not.** The registry is keyed by session identifier, rule 11 scopes the
  cascade to a session the adapter holds no entry for, and §4.1 fixes `ShutdownRequest` as
  session-scoped with one address. Do not dress it as a cross-tenant teardown.
- **WATCHOUT: the freshest-looking reliability candidate is already an Open.** "A cleanup that fails
  outside a `Shutdown` holds the identifier for the life of the pod while nothing reports it, so the
  session id is unbindable on that pod forever with no signal" reads as new off table row 7 plus the
  hold paragraph's "a refusal is the only record". It is the standing Open on a permanent hold,
  routed to remediation position 2's entry reaper. Do not spend two verifiers on it.
- **FOUR single-source candidates look live on the post-`1b12bba90` file and are not; round 22 worked
  each up and dropped it.** (1) §7.1's `acknowledged clean` definition versus rule 15's clean-exit
  clause: rule 15 is the home of what each outcome means, §7.1 of the gateway-side predicate over the
  ANSWER, and §7.1's trailing consequence is drawn from rule 15 while the predicate stays wide. (2)
  The disposition table's clean-exit column versus rule 15: the column gives the flag only for the
  `reclaimed` rows, which is exactly what rule 15 delegates. (3) The §16.1 `superseded` gloss versus
  rule 15's: accepted twice, and the four spellings move in one edit. (4) The Edge-cases
  envelope bullet versus SPEC-5's §15.1 replacements: the bullet's own content is the NON-setup-stage
  envelope, which §15.1 does not state.
- **The summary's "Two further residues are priced and accepted" block reads, to a docs-alignment or
  Edge-cases lens, exactly like an accepted failure mode missing from the Edge-cases section.** It is
  not: prune 4 deliberately put those two residues in `summary.md` and nowhere else. Round 22 would
  have filed it without the caller's prune directive; check this block before filing an Edge-cases
  omission.
- **MISTAKE, and the one this window paid for twice: a fix pass rewrote checklist S7 WITHOUT adding the
  item the finding named.** The finding was "S7 enumerates DOCS-1 without the `receiving_uploads` →
  `running` trigger replacement". The fix added the `slot_cleanup` → `released` trigger, the leaked
  clause and the pod-state paragraph, and left the named item out. Three later lenses then found it
  live while their prompts listed it as already fixed, so the defect was masked behind a
  do-not-relitigate entry. `[index-reconcile.1]` finally closed it. When a fix rewrites the block a
  finding names, diff the block against the finding's own words before declaring it closed.
- **MISTAKE: a `DEFERRED` that names TWO sites was closed on its first half only.** The entry named
  `upload_to_session.go` (missing from CODE-4's Targets) and `start_test.go` (listed with no edit
  stated); the fix landed the first and left the second, and a later citation lens had to re-file it.
  When a `DEFERRED` names two sites, check both before retiring it.
- **MISTAKE, one repeated finding: the `"The outcome is"` proto orphan.** An earlier round had already
  spotted that SCHEMA-1's two `ReportSessionScrub` replacements are SENTENCE-scoped while the comment
  wraps MID-LINE at `schemas/lenny-adapter.proto:310`, so applying both literally orphans three words.
  It was recorded and not closed, and this window paid a second finding for it. A quote over a proto or
  doc comment is checked against the whole source LINE, never against the sentence the proposal names.
- **MISTAKE: the rule-renumbering restructure deleted CODE-1's six-row table and left two sites pointing
  at it.** The tier-1 case list still said "one case per row of CODE-1's table" and the doc-comment
  bullet still said "Name the single exception" against a paragraph the same pass had rewritten to "the
  two returns". Both were one-line repairs and both cost a finding. After deleting an artifact, grep the
  file for every noun that named it.
- **WATCHOUT: `ShutdownReclaim` must NOT route through `Client.shutdown`.** That unexported builder sets
  `unconditional_teardown`, which rule 10 makes the destructive form; a reclaim built through it would
  tear down the successor. The staging's phrase "Both exported forms build the request through one
  unexported builder" reads as if it covered all three methods and does not.
- **WATCHOUT: the bind-field sweep's grep MUST carry the `adapterv1.` qualifier.** The unqualified
  pattern pulls in `tokensv1.AssignCredentialsRequest` and `podsession.ResumeRequest`, which are other
  services' messages with no `bind_attempt` field, and would tell an implementor to add a field that
  does not exist. The `ShutdownRequest{` sweep has no such hazard; all its hits are qualified.
- **WATCHOUT: `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` is hit by BOTH mandatory-field
  sweeps,** four `ShutdownRequest` literals and two bare `FinalizeWorkspaceRequest` literals, so the
  summary's 0078 impact row cannot describe it as a one-field edit. The same holds for
  `tests/tier4_integration/concurrent_delegation_proxy_test.go`.
- **Do NOT sweep every "row" in the non-spec file onto "arm".** Two uses are correct and mean something
  else: the tier-1 bullet's "The two-field precondition, four rows" names the four request-field
  combinations, and the metrics deliverable's "untokened-entry row" names a docs table row. Everything
  else denotes an arm of `shutdownReclaimOutcome`.
- **Do NOT put the reclaim-hold test inside `slotStateLocked`, for two independent sufficient reasons.**
  The staged `Shutdown` handler resolves through it twice, so the test would refuse the reclaim its own
  pass opened, and `releaseSessionSlot` and `terminateHeldSession` resolve the same way; and it is a
  fail-open on the credential controls while being unreachable anyway, because `reclaiming[id]` is set
  in the same step that deletes `s.slots[id]`.
- **The `SessionScrubOutcome` ENUM HEADER comment is not a missed SCHEMA-1 site.** `schemas/lenny-adapter.proto:436-437`
  says the CLEANUP runs on every session release, and the cleanup universal survives; only the REPORT
  universal is withdrawn, and the staged §5.2 scrub-model replacement preserves the cleanup clause
  explicitly. At least four shards have reached for this. Stop here.
- **`docs/reference/adapter-contract.md:393` is a pre-existing `doc-content.md` violation and is NOT
  licence.** Do not "align" DOCS-2's staged links with it by moving `§4.7.1` or `§15.4` into published
  link text; that is the tempting local edit and it is the thing the rule forbids. The archive records
  this trap twice already.
- **`docs/reference/adapter-contract.md:84` (`**Scrub responsibilities.**`) reads like an unlisted
  carrier of the withdrawn report universal and is not.** It names neither `ReportSessionScrub` nor
  "per-slot cleanup", so it falls outside both sweeps the carrier table was built from, and it is scoped
  to a session END, which is a slot that reached `running`. Worked up and dropped twice; a later lens
  owes an argument that "each session end" reaches a pre-`running` slot.
- **`spec/28:140` (`REG-PODSTATE`) calls `sessions_served` a counter "the recycle disposition
  evaluates", which is already inaccurate on a concurrent non-`vm-restart` pool.** It is wrong before
  and after the staging, so it fails the "becomes wrong" criterion and is its own pre-existing finding.
  Do not file it against 0081.
- **The §6.2 fence's four edited entries now point at TWO different targets, two at §5.2 and two at the
  pre-`running` paragraph, and that is correct.** §5.2 owns the disposition of the cleanup; the
  paragraph owns the boundary and the edge conditions. A lens that "unifies" all four onto §5.2 puts an
  edge condition into a section that does not state it. The untouched `running ──→ slot_cleanup` entry
  keeps its shipped trigger, so "the fence carries only pointers" is not a ground for anything.
- **`docs/reference/state-machines.md:235` is the ONE licensed restatement of the `running` boundary,**
  because a reader-facing page may not carry a spec citation. Do not later "fix" it into a pointer. The
  Go comments in CODE-3 take the opposite form, `see §6.2 "Pre-running slot cleanup"`, because code may
  cite the spec; comparing the two texts character by character reads as a divergence and is not one.
- **MISTAKE nearly filed: the graceful-shutdown frame is called `terminate` in §29.4 and `shutdown` in
  §15.4.2/§15.4.3.** The two-name discrepancy predates this proposal and no staged block introduces or
  depends on it. Related and also refuted: the §4.7 row cites §15.4.2 for the signal while
  `spec/28:1082` (§28.5.3) is the stronger anchor; the DRAINING row does state the signalling, so the
  citation stands.
- **WATCHOUT: CODE-1's removing arm proceeds destructively when `lockSlotGuard` fails to acquire.** That
  reads as a bypassable mandatory gate and is not: removing unguarded is the shipped behaviour of every
  deregister-then-destroy site today, so the guard is strictly additive, and the entry-level decision is
  still re-made under `s.mu` after the attempt, so no successor's entry is deregistered on a stale
  comparison.
- **`materializeSlot`'s `releaseAttemptCredentials(minted)` also runs on the `SLOT_BIND_ALREADY_STARTED`
  refusal arm, and that is not a finding.** Releasing a lease revokes it, which is fail-closed; the slot
  identifier is the session identifier, so there is no cross-session or cross-tenant reach; and the
  tier-9 third arm pins that a failed attempt's release strips none of a successor's leases.
- **Do NOT file `Server.slotGuards` or `s.reclaiming` growth as a capacity finding.** A map entry is a
  session-id string plus a capacity-one channel, on the order of 200 bytes, so a pod churning eight
  slots for a day holds tens of kilobytes. The "Bounded cohort" configuration is the one the stated
  `maxSessionsPerPod` bound does not cover, and the magnitude answers it anyway.
- **CODE-3's "faithful transcription of the fence" is loose and is not a finding.** The Go glosses are
  paraphrases ("task dispatched", "task completes or fails") where spec/06 says "session dispatched to
  runtime with its session identifier" and "session completes or fails". Nothing becomes wrong when
  SPEC-4 lands. The same applies to `OccupiesSlot`'s §6.2 quote, which says `active_slots` where spec/06
  says "the pod's Redis slot-counter occupancy": pre-existing drift the proposal neither creates nor owes.
- **`RemoveTree` lives in `pkg/adapter/slotlayout/tree.go`; `slotstate` is the package under
  `pkg/sandbox`.** A grep on the wrong root returns nothing and reads as an absent symbol.
- **Do NOT file the §15.1 row's second sentence, "Surfaced wherever the gateway runs setup commands".**
  Its load is the endpoint enumeration and that set is unchanged, because the refused request is the
  setup-command request the gateway issues at exactly those four endpoints. Refuted four separate times
  now; a filing owes an argument that the endpoint set, rather than the phrasing, is wrong.

### Open

- **Can the reclaim-hold refusal reach `Binder.Prepare`/`Binder.Launch` and drain the pod?** — OPEN: `errSlotReclaimInProgress` is a bare `codes.Aborted` matching neither CODE-7 sentinel, so CODE-8's guard falls through to `failPhase` and drains.
- **Does a permanently held slot identifier have to be answered as permanent?** — OPEN: a life-of-the-pod hold repeats `ABORTED`, which §15.4 publishes as retryable; the entry reaper is routed to remediation position 2.
- **Does rule 8's untokened arm compare equal across a replacement?** — UNVERIFIED: two untokened entries under one identifier compare "" to ""; no reachable interleaving shown. Check the SDK-warm `ConfigureWorkspace` path.
- **Which client envelope does a rule-6 `FAILED_PRECONDITION` at a non-setup-command bind request reach?** — UNVERIFIED: `SlotBindError.Reason()` may render non-retryable 422 `SLOT_FAILED`; rule 6 also makes a repeat `StartSession`'s `Unavailable` permanent.
- **Does the process-group kill fall inside the "every act that cleanup owes the slot" completion predicate?** — UNVERIFIED: nobody traced where the kill runs or whether its failure is observable.
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** — OPEN: a third-party harness cannot force a failed removal, and the recycle-carrying `Shutdown` scrub has no case for want of a `ReportPodScrub` observer.
- **Neither newly recorded residue has any observability** — OPEN: a dead attempt's stamped entry yields only transient `SLOT_BIND_ATTEMPT_SUPERSEDED` refusals, and the untokened-entry counter is unscraped. For a human.
- **Open decision 20: do the two new `ErrorCode` values take 28 and 29 or the Phase-2 range?** — OPEN, for a human; the next adjudication owes a recommendation, alternatives and confidence.
- **Are the two `WIRED` claim-register rows owed at all?** — OPEN: §28.4 obliges a row only for a normative §28 statement and `Shutdown` carries none; dropping them is cheaper than editing the generator.
- **Are "registry entry" and "bound entry" defined anywhere?** — OPEN: the staged contract rests on "registry entry" and `spec/` never defines it. A human may want one defining clause in §4.7.1.
- **The queue FIFO amplifies the synchronous compensating `Shutdown`** — OPEN, FILED: the uncancellable compensation runs inside the queued attempt, so two 15s attempts equal default `maxQueueWaitSeconds`. Keepalive's effect is unverified.
- **Churn from the third accounting caller** — OPEN, unpriced: at `maxConcurrentSessions: 2` a failed §7.3 re-attach drains a fresh pod; node loss, resume storms and transport blips multiply it. Fallback: account the leaked arm only.
- **Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?** — OPEN, pre-existing: `slothealth.Tracker` is replica-local, so no replica need reach the threshold, and this proposal raises the leak rate.
- **Does `lenny_adapter_leaked_slots` now need a §16.1 row?** — OPEN, pre-existing: no catalog or docs row exists, and whether `/healthz` `leaked_slots` moves when the report is withheld is unverified.
- **Is the adapter's gateway-facing gRPC server reachable from the agent container, and does it require mTLS?** — UNVERIFIED: if unauthenticated, an in-pod agent could send a co-tenant's unconditional `Shutdown`. Pre-existing.
- **Can the adapter populate `k8s_pod_name` on every pod?** — UNVERIFIED: `podID` may be empty; the code lane decides whether the untokened-entry counter drops the label or withholds, as the scrub reporter does.
- **What replaces `deregisterSlot` at its two test callers?** — UNVERIFIED: `podmcp_arming_internal_test.go:88,:245`; routing them through `reclaimSlotLocked` opens a hold nothing releases. Use an inline lock plus `deregisterSlotLocked`.
- **Tier-4 fixture extension** — OPEN: whether the per-case interceptor perturbs the six other `recycleAdapterDialer` call sites, and whether `recycle_scrub_path_test.go` can carry the concurrent-slot compensation case, is untraced.
- **Does `pkg/gateway/podlifecycle/podsession/binder_envtest_test.go` have a reachable envtest home?** — UNVERIFIED, disputed between two shards; whoever lands the tier-2 case settles it, and whether it owes a `slotAddressCaseFiles` row.
- **Does `buf breaking` actually run in this repo's gates?** — UNVERIFIED: nobody found the gate; whether the docs-lane step's write lease admits `tests/tier11_docs/*.go` is equally unchecked.
- **Does "takes the session back off the shared runtime process" hold for every runtime implementation?** — UNVERIFIED, narrowed to PRE-EXISTING by [spec.18.review-reliability.1]: `MCPRuntime.Close`/`InProcessRuntime.Close` ignore the identifier while `SocketRuntimeProcess.Close` is sibling-safe, but the shipped `Shutdown` calls the same method, so rule 8 adds no arm. Closed for the new-behaviour reading.
- **Can a coalesced reconcile lose the claim-DELETE retirement?** — UNVERIFIED [spec.7.review-kubernetes.1, spec.10.review-kubernetes.1]: a CREATE, `bound` patch and DELETE inside one window leaves `("", false)` and an idle unscrubbed pod holding the residue. Pre-existing; remedy is code.
- **Does a gateway replica's §10.1.7 preStop drain discharge the §7.1 reclaim obligation?** — UNVERIFIED [spec.10.review-reliability.1]: the Edge-cases bullet covers a crash, and a graceful drain is distinguishable.
- **Does the gateway double-book a leak for a runtime-given slot whose close fails?** — UNVERIFIED [spec.7.review-reliability.1]: row 2 gives both a `leaked` report and an unclean response into one `slothealth.Tracker` with no per-session dedup. Check `MarkLeaked` idempotency.
- **Does withholding the report change the `lenny_pod_session_reuse_count` histogram and the `mode_factor`?** — UNVERIFIED [spec.7.review-performance.1]: unknown whether the observation shares `RecordSessionScrub`'s call site.
- **Can a `PrepareWorkspace` frame on an already-admitted call write into a tree `removeSlotTree` is deleting?** — UNVERIFIED [spec.10.review-mechanism.1]: rule 2 does not re-admit a frame; reachability turns on whether the handler honours the cancelled server context.
- **Does `podStateGatewayWrittenSentence` keep its trailing `ReportPodScrub` clause after the re-key?** — UNVERIFIED [spec.9.review-fresh.1]: the implementor should keep the full sentence rather than truncate the constant.
- **Has the "which teardown it asks for" vocabulary leaked into the DOCS-2 `adapter-contract.md` mirror?** — UNVERIFIED [spec.5.review-mechanism.1]: only `spec-changes.md` was reviewed; grep the whole directory before declaring that fix complete.
- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** — OPEN, for a human [spec.4.fix-design-G3.1]: the same refusal is non-retryable at one stage and retryable at another, and nobody tested it against a client.
- **Do `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` and the session-scrub addressing gate live in the same file?** — UNVERIFIED [spec.4.fix-G2.1]: DOCS-2's `## Testing` names a third file. Nothing depends on it yet.
- **Does a coordinator handoff or preemption leave the §7.1 reclaim unsent?** — OPEN, FILED [spec.20.review-performance.1]: §10.1.5 item 1 forbids the preempted replica from sending it, which is a second and more routine trigger than the gateway-crash bullet's "a gateway that dies". No fix entry followed.
- **Does the gateway's preStop drain hand a mid-bind session to another replica, or fail the bind on the draining replica?** — UNVERIFIED [spec.20.review-performance.1]: if there is no mid-bind handoff the preemption trigger narrows to lease expiry and partition rather than disappearing.
- **Is the §4.9 timer cancellation ordered wrongly against the credential-file removal?** — OPEN, FILED [spec.18.review-security.1] and unfixed: the deregistration cancels the mandatory §4.9 expiry timer and cannot fail, while the removal of the file that timer protects is best-effort and may fail after it. The staged action list asserts both in one sentence.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** — UNVERIFIED [spec.18.review-performance.1], corrected by [spec.20.fix-G1.1]: the clause it quoted ("as a reclaim whose act fails does") is deleted and the site moved, but the question stands. One leaked slot retires the pod there and the spec publishes no bind-failure rate to multiply.
- **Does `s.reclaiming` grow without bound over a long-lived recycling pod?** — UNVERIFIED [spec.18.review-reliability.1]: one entry per cleanup whose act failed, held for the life of the pod, and the adapter process survives pod reuse, so nothing sweeps the map. Code-lane.
- **Are `pkg/adapter/gatewaylink.go:71` and `pkg/controller/sandbox/podspec/podspec.go:168,:591,:906` missing rows of the SPEC-3 carrier table?** — UNVERIFIED [spec.18.review-edit-sites.1]: the table is complete over `spec/`, `docs/`, `schemas/` and the pb.go copies, and these four Go comments say "reports each per-slot cleanup outcome via ReportSessionScrub". `podspec.go:687` is NOT one.
- **Should SPEC-5 also correct `spec/06:290`?** — OPEN, for a human [spec.18.fix-G1.1]: the shipped §6.2 Client-visibility bullet attributes a workspace-validation envelope to the §15.1 finalize precondition note, which states none. Pre-existing, and SPEC-5 edits a different clause of the same bullet.
- **Does the widened §15.1 row owe `RunSetup`'s third deterministic `FailedPrecondition` producer?** — UNVERIFIED [spec.18.review-fresh.1]: "adapter is not configured with a workspace root" (`pkg/adapter/staging.go:345-349`) maps to 422 the same way. The row was equally non-total before SPEC-5, and the "row is total" sentence has since been deleted.
- **Does `docs/runtime-author-guide/` owe a mirror of §15.4's two published blocks?** — UNVERIFIED [spec.12.review-client-surface.1]: only `docs/reference/adapter-contract.md` is staged, and the remedy is a docs edit.
- **Does `docs/reference/adapter-contract.md` owe a mirror of the §4.7.1 numbered-rule block?** — UNVERIFIED [spec.15.review-client-surface.1]: DOCS-2 stages four row and paragraph edits only; whether the tier-11 adapter-contract gates want the rules is untraced.
- **Does `docs/reference/metrics.md`'s `## Adapter metrics` table use the column set CODE-9 assumes?** — UNVERIFIED [spec.15.review-docs-alignment.1]: nobody has opened that table.
- **Where is the persistent `leaked` ledger behind the `ceil(maxConcurrentSessions / 2)` replacement trigger stored, and does it have a §12.4 durable fallback?** — UNVERIFIED [spec.22.review-performance.1]: the staging routes new pre-`running` leaks into that ledger, so a Redis-backed volatile store would forget pods that should retire on a reset. Pre-existing rather than staged.
- **Do the sentence-repeat sweeps disagree, or did their scopes differ?** — UNVERIFIED [compaction passes 29 and 30]: round 19 reported one repeat, round 22 six over four proposal files, and `[spec-recheck.5.review-single-source.1]` two over `spec-changes.md` alone. The scopes differ, so all three may be right; `### Settled` carries the two-over-`spec-changes.md` result and the six-over-four-files result side by side. Fix the scope once and stop re-running it.
- **Does the §28.4 `ABSENT` claim-register row's precedent sentence name the wrong status?** — UNVERIFIED [spec.16.review-citations.1, spec.19.review-citations.1]: every `coordination_generation` fence row in `tests/claim-map.json` is `UNWIRED`, and only "In-flight RPC cancellation on a generation gap" is `ABSENT`. Judged decorative twice, because the sentence states the status it wants explicitly.
- **Is a `credentials.json` left by a failed cleanup on a concurrent pod reachable by a later session through the shared runtime process?** — UNVERIFIED, the surviving companion of resolved decision 38: `pkg/adapter/slot.go:219-225` returns one `Runtime` for every registered session. Answering it the other way would reverse decision 38, so it is recorded inside the new unstaged-defects row as its own finding against the isolation model. Nothing opened has established filesystem reachability of another slot's credential path.
- **Does DOCS-1's own-voice trigger cell for the new `receiving_uploads → slot_cleanup` row still agree with the paragraph?** — UNVERIFIED [spec-recheck.4.fix-design-G1.1]: the reduction to a pointer did not touch the docs row, and nobody re-read that cell against the paragraph's current wording.
- **Would any gate newly fail because of text the staging ADDS, rather than text it replaces?** — UNVERIFIED [spec-recheck-2.1.review-applicability.1]: gate breakage was checked only for replaced lines. Nothing in `tests/tier11_docs` looked like a structural counter over the §4.7.1 block's new rows or bold paragraphs, but nobody enumerated them.
- **Does S23's tier list "0, 11" agree with DOCS-4's own "Tiers: 0"?** — UNVERIFIED [non-spec-recheck.1.review-applicability.1]: judged immaterial, because running the docs tier costs nothing.
- **In which step do SCHEMA-1's three claim-register rows land?** — UNVERIFIED [non-spec-recheck-2.1.review-applicability.1]: SCHEMA-1 says all three at S9, CONF-1 says the `ABSENT` row lands with the tier-10 file at S22, and checklist S9 names the two `WIRED` rows. The gates tolerate either placement; settle it in ONE home.
- **Can an entry stamped with a dead token on the EXCLUSIVE path strand the pod's `SandboxClaim` forever?** — UNVERIFIED [non-spec-recheck.1.review-reliability.1, carried by spec-recheck-2.1.review-kubernetes.1]: CODE-8 makes `Binder.Prepare`'s refusal arm skip `failPhase`, so the drain that deletes the claim does not run. On the concurrent-`/finalize` case that is correct; on the gateway-crash residue case nothing obviously reclaims it. Whether the WarmPoolController's orphaned-claim GC reaches it is untraced.
- **Can a recycle-carrying `Shutdown` answering `absent` start the whole-pod scrub while a DIFFERENT slot's `releaseSessionSlot` or §10.1.4 termination is still inside `removeSlotTree`?** — UNVERIFIED [non-spec-recheck.1.review-security.1]: a real ordering question, outside the arm resolved decision 35 describes.
- **Does §5.2's "still in flight" mean "not yet admitted"?** — UNVERIFIED [non-spec-recheck-2.1.fix-design-G1.1]: §5.2 says the hold refuses a §7.4 mid-session upload still in flight when the cleanup opens the hold, while CODE-6 says an upload already past its resolve "is not being admitted and re-enters no resolve". Pre-existing and independent of the scope narrowing.
- **Does wiring the adapter scrape target move CODE-9's staged rows?** — OPEN [non-spec-recheck-2.1.review-mechanism.1]: if the human answers decision 33 that way, N4's deferred metric half and the §16.1 adapter row both move, and nobody has checked what that does to CODE-9.
- **Does `docs/operator-guide/observability.md` owe rows for the two SPEC-6 counters?** — OPEN [spec-recheck.4.review-client-surface.1]: CODE-9 stages `docs/reference/metrics.md` alone, and whether that page is a full §16.1 mirror or a curated subset is untraced. A docs-lane question.
- **Does the ten-second provenance paragraph belong in SPEC-3's commentary at all?** — OPEN [spec-recheck.1.review-applicability.1], and now larger: the caller's prune directive bars adding a rationale sentence to any SPEC-n commentary, and resolved decision 40 appended six more sentences to that same paragraph. Whoever runs the next prune decides whether the provenance belongs there or whether the figure standing in the staged §5.2 sentence is the whole statement.
- **Does rule 8's take-back, stated outside the registry critical section, let a lagging attempt close a successor's session?** — OPEN, dropped below the bar [spec-recheck.4.review-reliability.1] and recorded so it is not re-derived: reaching it needs attempt A descheduled across a whole reclaim, cleanup, hold release and fresh bind, and the reclaiming `Shutdown` has already torn A's runtime down on the admission precondition, so the take-back is belt-and-braces on every reachable ordering. The area is what the `**A start that races the reclaim.**` Edge-cases bullet records.
- **Can `terminateHeldSession` be skipped for a member whose hold pass 1 already opened?** — UNVERIFIED [non-spec-recheck-2.1.review-reliability.1]: no early return after pass 1 exists, so the only skip is a panic, which ends the process under `RestartPolicy: Never`. Do not spend a round on it unless a later staging adds a per-member `continue`.
- **Does SCHEMA-1 already stage the `SESSION_SCRUB_OUTCOME_LEAKED` comment?** — UNVERIFIED, a disagreement compaction pass 30 did not settle: `[non-spec-recheck-2.1.fix-G3.1]` and its design twin say SCHEMA-1 now edits that comment on the same terms as its `RELEASED` sibling, while the later `[index-reconcile.1]` carries it forward as still owed. The Deferred entry keeps the newer form. One grep of SCHEMA-1's comment block settles it.
- **Is `troubleshooting.md:41`'s `reason: setup_command_failed` the REST envelope reason or a warmup-failure label?** — UNVERIFIED, a second unsettled disagreement: `[spec-recheck.4.review-client-surface.1]` deferred the row as carrying a remedy that is wrong for SPEC-5's new cause, and `[non-spec-recheck-2.1.review-docs-alignment.1]` refuted it, reading the row's section as warm-pool exhaustion and the label as a `lenny_warmpool_warmup_failure_total` value. `[index-reconcile.1]` carried the deferral forward. Read the section around the row once and close whichever half is wrong.

### Deferred

- DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start`
  failure branches release the slot by session identifier alone, so a lagging one deletes a later
  attempt's entry and tree. Carried in the summary's unstaged-defects section; do not re-file here.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline
  lets the adapter apply its default grace period" is false; `resolveShutdownGrace` prefers the caller
  ctx's remaining time. CODE-7 opens the file, and no deliverable stages the one-sentence repair.
- DEFERRED [pkg/gateway/podlifecycle/podsession/binder.go, `Binder.drain` doc comment]: it states the
  retired projection keying (recycle setting and limits). `ProjectOccupancyPhase` returns `Draining`
  from `Claimed` on every pool, as SPEC-4 states. CODE-4 opens the file and stages no correction.
- DEFERRED [schemas/lenny-adapter.proto, `Shutdown` RPC comment and `ErrorCode` enum header]: the RPC
  comment predates the two-teardown split, and "The catalog below mirrors spec §15.1" is false for
  codes 27, 28, and 29. SCHEMA-1 stages the `ReportSessionScrub` comments only.
- DEFERRED [docs/operator-guide/troubleshooting.md, the `setup_command_failed` row]: after SPEC-5 the
  reason has a second cause, a started-session refusal that ran no setup command, for which both
  listed remedies are wrong. No docs deliverable opens the page.
- DEFERRED [docs/runbooks/gateway-replica-failure.md]: the page calls a gateway crash benign for
  sessions. The crash-stranded registry entry (accepted failure mode) is a counterexample: the session
  is unstartable on that pod until the pod is replaced. The failure narrative owes that cause.
- DEFERRED [spec-changes.md, commentary wording]: the untokened-entry bullet calls the §10.1
  hold-timeout termination "a request" where §5.2 says it runs under no request. The other half of this
  entry, that SPEC-3's rationale says the hold paragraph "states a terminal", was refuted in spec round
  4: the paragraph ends the hold only on completion, which entails the terminal, and the table names it.
- DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118]: the `Sandbox.status.phase` doc comment
  carries the projection-input enumeration SPEC-4 extends in §4.6.1 and §6.2, without the new input.
  It already omits "disposition", so it is a loose paraphrase that was incomplete before this proposal;
  record only. The authoring source is the Go doc comment plus `make generate`, never the two generated
  YAMLs (`charts/lenny/crds/lenny.dev_sandboxes.yaml`, `pkg/embedded/crds/lenny.dev_sandboxes.yaml`).
- DEFERRED [docs/getting-started/architecture.md:237]: it enumerates the occupancy projection's inputs
  as "claim existence, binding state, and disposition". SPEC-4 adds the phase the pod currently
  projects to §4.6.1's enumeration and DOCS-1 adds it to `docs/reference/state-machines.md` only, so
  this page is left short one input. The docs loop decides whether a getting-started page keeps the
  enumeration at concept depth or takes the fourth input.
- DEFERRED [docs/reference/adapter-contract.md, the §15.4 reclaim-hold half]: SPEC-5 adds TWO published
  blocks to §15.4 (the bind attempt token contract and the slot-identifier reclaim hold). DOCS-2
  mirrors only the token and states its ground for leaving the cascade in §4.7.1. The hold's `ABORTED`
  refusal is an adapter-author obligation of the same kind and the page gives it neither a sentence nor
  an explicit exclusion rationale. Mirror it or record why it is excluded beside the cascade.
- DEFERRED [docs/api/internal.md, the gRPC status table]: its "When used" list omits the status the two
  new adapter refusals and the reclaim hold answer on (`ABORTED`), on a page whose hand-written proto
  excerpt is already wholesale drift (`StopSession`, `UploadFiles`, no `Shutdown` RPC). This proposal
  does not make the page newly wrong, so it is not a missed edit site; it is a standing docs defect the
  non-spec loop or a separate finding should take.
- DEFERRED [schemas/lenny-adapter.proto:255-257, the `GatewayControl` SERVICE comment]: it says the
  service "carries the §5.2 per-slot and whole-pod scrub reports (ReportSessionScrub, ReportPodScrub)
  the adapter emits on release." SCHEMA-1 edits only the `ReportSessionScrub` RPC comment (:308-310)
  and the request-message comment (:451-452), so this third site is in no edit list and in no SPEC-3
  carrier-table row; the round-12 sweep's patterns cannot match "emits on release". Round 22 did NOT
  file it, because the clause is a restrictive description identifying the two RPCs rather than a
  universal rule about releases, and it is already loose for `ReportPodScrub` (emitted at occupancy
  zero rather than at a release). The minimal belt-and-braces fix is dropping the four words "the
  adapter emits on release" from that sentence; it does not warrant a carrier-table row.
- DEFERRED [spec-changes.md, the reclaim hold's scope, the half that actually closes the finding]: the
  non-spec lane landed the wording corrections and could not land the spec half. What is false today:
  the clause ", apart from the reclaim hold of rule 2" in §4.7.1's `**Admission.**` preamble, and
  §5.2's unqualified "admits no request that would create or resolve a registry entry under it" with
  its companion "A request that resolves an entry without creating one is refused on the same terms".
  What is true instead: the hold refuses exactly the requests §4.7.1 enumerates as governed by its
  admission rules, whether they would create an entry or resolve one, the §7.4 mid-session upload
  among them. Keep the create case, the resolve case and the §7.4 example when replacing the three
  sentences with one. The finding is not closed until this lands. Body in
  `[non-spec-recheck-2.1.fix-design-G1.1]`.
- DEFERRED [schemas/lenny-adapter.proto:445-448, the `SESSION_SCRUB_OUTCOME_LEAKED` comment]: it states
  that LEAKED is reported when "a resource could not be reclaimed at the session release". Under the
  staged §5.2 disposition table row 3 the adapter reports `released` when the runtime close succeeds
  and a later act fails, so that condition clause names a set the table splits across both outcomes.
  What is true instead: the cleanup reports LEAKED on the terms §5.2's disposition table states. The
  remedy is one more verbatim replacement inside SCHEMA-1, on the same terms as its `RELEASED`
  sibling, keeping the drain-ledger sentence and the `spec:` line untouched and adding no rationale
  paragraph. Note the unsettled disagreement in `### Open`: two fix firings say SCHEMA-1 already
  carries this edit and the later `[index-reconcile.1]` says it is still owed. Two further sites name
  SCHEMA-1's comment scope and both undercount once this lands: the summary's deliverable-index bullet
  and spec-changes.md's non-spec-deliverable summary sentence. The block's lead-in counts the edited
  comments as "both comments"; rewrite it count-free as "these comments", never as "all three".
- DEFERRED [schemas/lenny-adapter.proto, the LEAKED comment's drain-ledger sentence]: "The gateway
  feeds the outcome into the unhealthy-threshold ledger behind the `lenny.dev/drain-request`
  annotation" may itself be a restatement of a rule whose home is §4.6.3/§5.2. SPEC-3 does not falsify
  it, so it stands; a later round can file the reduction to a citation as its own finding.

### Retired

Bodies are in the archive file. This list names subjects only.

- Retired in compaction pass 30, Deferred entries discharged by `[index-reconcile.1]` or by the staging: `pkg/sandbox/slotstate/slotstate.go`'s two doc comments (CODE-3 now stages the four constant docs, the three edge-list glosses, `slothealth.go`'s two sentences and the three further carriers in `registry.go`, `sessionserver.go` and `gatewaymetrics_credential.go`, and checklist S8 names the same set); DOCS-3's retryable-fallback age claim; CODE-4's `Targets:` and files-touched halves, both now present; CODE-6's `ConfigureWorkspace` idempotent-repeat double lock, now an `allowStarted` field on the resolve descriptor; CODE-9's "verbatim" adapter-deferral sentence; the resume-path test bullet's assertion; the tier-7a rule-8 start-step case; the logging and count wording; the `**The mid-session conditioning.**` restatement and the CODE-6 rule-4 comment; CONF-1's reclaim-hold attribution, now §5.2; the scrub-outcome comment count in both carriers; implementation-checklist S1, S4, S5, S7, S15, S16, S19, S22, the S2 final sentence at :19 and the second carrier at :17; summary.md:119's false scope phrase; DOCS-2's `:393` precedent claim; the §16.1 carrier list's "row"; and the `pkg/proto/adapter/v1/lenny-adapter.pb.go` regenerated copy, which is a negative record because `make generate-proto` lands it in the same commit.
- Retired in compaction pass 30, Open entries closed: "Is the staged §5.2 ten-second graceful window the right figure, and does it want an operator override?" and "Is the ten-second graceful window an operator-tunable constant?" (both closed by open decision 40, resolved as staged: the window is fixed and the override rule's antecedent fails); "Does the whole-pod scrub racing the per-slot cleanup on the removing arm want an ordering statement?" (closed by open decision 35, whose premise was false); "Does disposition-table row 3 withdraw a residual-state control without recording the withdrawal?" (closed by open decision 38, resolved as staged and recorded as an unstaged-defects row; its credential-reachability companion stands in `### Open`); "Do S16, S18 and S21's `Depends-on` lines owe S15 and S1?" (discharged at HEAD by `[index-reconcile.1]`); "Does the carrier table's completeness claim survive a LOOSER grep than round 12 ran?" (answered yes by `[spec-recheck.4.review-edit-sites.1]`, whose wider pattern found every carrier already in the table); and the round-19-versus-round-22 form of the sentence-repeat question, replaced by a three-scope form that also carries `[spec-recheck.5.review-single-source.1]`'s count.
- Retired in compaction pass 30, superseded Settled text: the round-22 entry recording the whole-file sentence-repeat sweep as six repeats over four files stands, and `[spec-recheck.5.review-single-source.1]`'s two-over-`spec-changes.md` result is recorded beside it rather than replacing it, because the two sweeps had different file scopes and neither corrects the other.

- Retired by `[prune.4.fix.1]`: the Open "The graceful-shutdown-signal condition is stated at three sites" (CLOSED by round 18, body in `[spec.18.fix.1]`).
- Retired by `[prune.3.fix.1]`: the disposition table's residue column ("State the cleanup leaves on the pod") and every standing entry that described it as a column; the round-14 UNVERIFIED on whether the trailing enders sentence duplicates the scrub model (moot, the sentence is now the one residue rule); the checklist-S4 "what ends the state left on the pod" DEFERRED of `[spec.13.fix.1]` and `[spec.13.fix-G1.1]` (discharged, the S4 clause now names the post-table paragraph).

- Open, answered by the current text or recorded in the summary's open decisions or unstaged-defects list: socket runtime on a recycling pool; mid-start `Shutdown` bricks the pod; §4.1's retired vocabulary; `receiving_uploads` on an upload-free plan; §5.2's bullet at concurrency 1; `terminate` frame reason value and `deadlineMs`; whose view of "running" §7.1 means; does §7.3 owe a release step; the other `resuming` bullets; concurrent `/start` serialization; retry re-incrementing the Redis reservation; `maxSessionsPerPod` counting a bind that reached `RunSetup` (decision 21); `ProjectOccupancyPhase` no-claim arm and claim-observed generation (decision 27); pre-`running` residue's return-to-pool exit; one-session replacement pod terminal; gateway `leaked` versus adapter `leaked`; §5.2 and §7.1 both stating the `leaked` disposition; hold's "create or resolve" predicate width; rule 10's pre-registry timing; does a bind attempt span `/finalize` to `/start`; headline over-states what the token closes; two residues living only in reasoning; one-session-pod residue bullet; §4.7 `ReportSessionScrub` row edit; `adapter-contract.md:81` row; three docs sentences against the withheld report (two pages survive as the DOCS-4 item); §10.1.4 termination owing a report; CODE-4 session-keyed credential release; second `AssignCredentials` double-count; MCP surface a retry inherits; successor inheriting armed timers; is tier 8 reached; guard-acquisition expiry tests; rule 8 and deregister-and-hold CONF-1 cases; §7.4 mid-session rule; `metrics.md` untokened row; hold refusal accounting on the resume path; hold bounded at concurrency 1; §16.1 superseded row qualifier; §15.4 "stated once" claim; §15.1 exclusion narrowing; §6.2 Client-visibility inclusion clause; §6.2 fence versus prose on `leaked` at concurrency 1; spec/05 versus spec/06 pod retirement wording; `StartSession` row states no refusal.
- Open, mechanism or wording no longer staged: `/finalize` Gap-2 no-re-dial window (epochless form); which step routes `Shutdown` through `reclaimSlotLocked` (old S9/S10); CONF-1 lettered clauses (a)-(j) and clause (j) buildability; tier-9 epoch arms; the "spends one of the attempts" clause; who owns `slot_cleanup`'s start instant; SPEC-3's exclusive-pod arm; exclusive-pod abandonment disposition; "Seven residues survive" count; nine-versus-eleven adapter file lists; `spec-changes.md:98-102` blob-store clause; deleted `## Revision history`; summary host for accepted failure modes; round-8 unlanded findings; lens-retirement questions; tier-7a park placement; late-reclaim arm `leaked=false` observer.
- Open, still unclosed and cut for budget (implementor-level or pre-existing; re-derive from the archive when one matters): §29 incomplete-enumeration rule; §29.10 shared list; concurrent resume occupancy and orphan GC reach; §15.1 retry reaching a draining pod and `ClaimSlot` pass 1; resume-path `error_type`; compensation budget pin and `budget/2` margin, `SocketRuntimeProcess.Close` grace, and override; lease hold bound under §4.9; `SLOT_FAILED` undocumented; `configuration.md:99`; `/sessions/` and `/artifacts/` in the action list and §6.4; timer cancellation conditioned on credential removal; handler writing after `removeSlotTree`; hold spanning the scrub report; non-`Shutdown` hold-retention seam; CODE-2 idempotency sentence and `Resume` confirmation reacher; `ensureSlotStateLocked` whole-of-the-rule comment; zero-frame `PrepareWorkspace` senders; `slotResolveError` preserving `ABORTED`; coordinator handoff fencing the reclaim; §28.3 `LNK-POD-GRPC` multiplicity; N8 in proposal rationale; `superseded` producers; `recordSetupCommandFailed` audit; spec/07:208 and §15.1 precondition rows and "fresh pod" sentence; `state-machines.md` released-row actions; DOCS-2 tier-11 pins, annotation widening and audience clause; `metrics.md` gateway row placement; `Client.Shutdown` doc comment; CODE-1 leak-accounting paragraph; `SlotReclaim` hook arity; `slotFailureWorkspaceFinalize`; CODE-4 `Targets:` list and deliverable heading file lists; checklist tier digits; unused `noteCompensationOutcome` at S10; test-file lists (`binder_test.go`, `start_test.go`, `manifest_fields_test.go`, tier-3 file names, `slot` basename credit, `client_test.go` §15.4 credit); §11.4 fan-out test; setup-envelope tier-3 case; `holdstate_test.go` fake runtime; `fakeSDKWarmRuntime` workspace base; `terminal_reclaim_internal_test.go`; 0079 overlap row; summary index third anchor and `summary.md:141` attribution; `Resume` stall leak rate; re-placement onto a held pod.
- Deferred entries dropped in the 2026-09-20 hard compaction (bodies in the archive): proto `ReportSessionScrub`/`SessionScrubOutcome` comments (discharged, SCHEMA-1); spec/06 `slot_cleanup → released` annotation and its docs mirror (discharged, SPEC-4 and DOCS-1); error-catalog "nothing, deliberately" (mechanism gone, no-retry alternative); docs four sites that stay true (negative record); archive WATCHOUT re-point and ledger "fresh token" sites (log-only targets); CONF-1 per-frame epoch case, precedence note, property count, short-four-cases (discharged, CONF-1 is one case per rule with rule 9); every "RETIRED IN PASS 22" checklist and non-spec entry (discharged); DOCS-3 missing, DOCS-3 index line, summary "four replacements" (discharged, SPEC-5 now stages four); DOCS-1 `state-machines.md` projection clause (discharged); DOCS-2 `ReportSessionScrub` row (discharged); registered-but-unbound credential claim (discharged); §5.2 action list four trees (carried in summary defects); CODE-5 `Targets:` `isTransientPodClaimError`, CODE-6 heading file list, `noteCompensationOutcome` inverted clause, CODE-9 `catalog.go` path, SPEC-6 preamble count, summary CODE-9 bullet, summary "both RPCs", summary SPEC-2/SPEC-3 bullets, summary "Watch out for" connection bullet, summary Decisions file list, "Spec files touched" `Shutdown`-row description, §15.4 table dependants (discharged); SCHEMA-1 gate-text quote and tier-1 wire-rule five-RPC wording (mechanism gone or cosmetic); checklist frame-predicate and S10 negatives (negative records); accepted-residue two-item enumeration (discharged by the disposition table); duplicates of the `Client.Shutdown`, `Binder.drain`, CODE-4 Targets, CODE-9 verbatim, tier-7a, and checklist S1/S15 entries (duplicate).
- Retired in compaction pass 25, closed by a `CORRECTS` or discharged by the staging: the Open "Does §15.4 state a rule of its own after all?" (round 5 reduced the block's closing sentence to a pointer, leaving rule 2's `ABORTED` status, which the lead-in declares); the Open "Does the staged §15.4 conformance criterion over-reach?" (round 10 deleted the answer enumeration and added the scope sentence, and the related UNVERIFIED entries about "except in two places" no longer describe the staged text); the Deferred on SCHEMA-1's two proto report-trigger sentences (absorbed into SCHEMA-1 in round 10); the Deferred on DOCS-2's `adapter-contract.md` `ReportSessionScrub` row (added as DOCS-2's fourth edit in round 4). Carried unsettled for a later pass: `prune.1.fix.1` corrects a Settled entry, "the 'what can meet the reclaim hold' finding was closed by prose at three sites", saying the non-spec accepted-failure-mode bullet is no longer one of the three; that entry is in neither this section nor the live text, so the correction is recorded here verbatim rather than applied.
- Retired by `[redesign.2.fix.1]`: the Open "Do `execution-modes.md` and `security-principles.md` need a DOCS-4?" (DOCS-4 now stages both edits); the UNVERIFIED on the §15.4 criterion's scope vocabulary (the scope sentence is deleted); the Deferred entries on `agentpodstate.go`, `migrations/0167_*.up.sql` and the tier-11 addressing gate's header, on the two docs pages, and on the two tier-11 `sessions_served` gate files (each now has one row in the SPEC-3 carrier table, and the §12.6 block states the grep the implementor runs).
- Retired in compaction pass 29: the Settled line "The whole-file sentence-repeat check (round 19) returns exactly ONE repeat over 90 normalized characters", superseded by round 22's sweep, which returns six over the four proposal files. The two DISAGREE on the count and neither corrects the other, so the newer entry stands in `### Settled` and an `### Open` records that the file scopes may differ. No `OPEN`, `UNVERIFIED` or `DEFERRED` was closed, because all twelve round-22 lenses returned empty findings lists; the round's two `WATCHOUT`s and its one `DEFERRED` were additions rather than closures.
- Retired in compaction pass 28: nothing. The delta this pass read (`[f1.cleanup.3]`, `[redesign.5.fix.1]`, `[spec.21.review-reliability.1]`) closed no `OPEN`, no `UNVERIFIED` and no `DEFERRED`: round 21's reliability lens returned empty, both cleanup firings reported no-edit-needed, and `[redesign.5.fix.1]`'s three `CORRECTS` had already been applied to this section by the commit that wrote them, including the retirement of the Traps entry that distinguished "completed", "the cleanup does not complete" and "the `leaked` predicate", whose content is now the Settled `[redesign.5.fix.1]` line.
- Retired in compaction pass 27: the Open "Do the two SPEC-6 series belong in §16.1.1's 'Used on' column?" (answered NO by `[spec.15.review-edit-sites.1]`: §16.1.1 requires only that a label NAME appear in its table, and `pool` and `k8s_pod_name` both do; the "Used on" column is descriptive prose no gate reads); the Open "Should §6.2's projection preamble at spec/06:80 keep enumerating the inputs at all?" (answered by `[spec.20.review-edit-sites.1]`: §6.2's surviving enumeration names none of the projected phase, which is what licenses SPEC-4's "no input is added to that sentence's enumeration"). The per-runtime `Runtime.Close` Open was narrowed rather than retired.
- Retired in compaction pass 26: nothing. Round 11's four lenses closed no Open, no UNVERIFIED and no Deferred entry, and the two `CORRECTS` in `[redesign.2.fix.1]` name claims this section already states in their corrected form (the §12.6 read clause as round 10's pointer at §5.2, open decision 21 as the human's), so no entry was rewritten or moved out.
- Retired by earlier passes (1 through 24, `prune.1.fix.1`), bodies in the archive: the `coordination_generation` fence question; the resume-path exclusion; the slothealth-ledger resume question; §29.4 scoping; the tier-7a drain-gate items; Redis rehydration of leaked occupancy (own proposal); `releaseCredentials` and user-source leases; the reclaim-deadline margin; the spec/18 edit question; a fourth `SlotReason`; every bind-epoch, epoch-latch, `bind_epoch` field-number, and `ExcludePod(s)` entry; the fourteen-step checklist; the §10.1.4 and `DemoteSDK` report carve-outs; the "one return-to-pool edge" question; whether `superseded`, a pairing-rule failure, or `absent` still runs the whole-pod scrub.

## Ledger

### [non-spec-recheck.2.fix-G1.1]

DECISION: closed CODE-1's retired-form spec citation by DELETING the parenthetical `(spec/05_runtime-registry-and-pool-model.md:455)` from the staged `answerShutdown` doc comment and rewrapping the tail, adding nothing — BECAUSE the sentence already opens with "the order §5.2 states for the whole-pod boundary" and the block closes on `// spec: §5.2 recycle lifecycle; §4.7 Shutdown recycle disposition.`, so the pointer was load-bearing nowhere — ALTERNATIVES: a heading-form `(§5.2, Recycle lifecycle)` replacement (rejected: a third §5.2 citation in one comment block, and this run prefers the remedy that removes text); seeding a baseline row in `tests/registers/line-citations.yaml` (rejected: the ratchet only writes counts downward and the register is `files: []` by design, and it is outside this loop's editable surface); quoting §5.2's wording more fully so no pointer is needed (rejected: one home per rule, the ordering rule lives in §5.2).

FACT: `pkg/adapter/session.go` carries no line-citation baseline row, and the ratchet register is empty by design, so ANY staged comment that lands a `spec/NN_*.md:LINE` citation in a tracked Go file turns tier 0 red on its first occurrence. The proposal file itself trips nothing because `proposals/` is read-excluded, so the defect is invisible until the deliverable lands. — EVIDENCE: tests/registers/line-citations.yaml:5-8,15; tests/tier0_static/line_citation_ratchet_test.go:139,244; scripts/specshift/scope/scope.go:95

WATCHOUT: the ratchet's matcher keys on the `NN_name.md:LINE` spelling, so Go-file line citations of NON-spec files (the staged comment still cites `pkg/gateway/podlifecycle/podsession/slotbinder.go:542` and `binder.go:1994`) are outside it and were deliberately left alone. Do not sweep those as part of an N8 fix; N8 is about specification citations. — EVIDENCE: scripts/specshift/citation/grammar.go:118-145

FACT: two `spec/NN_*.md:LINE` citations remain in non-spec-changes.md, at the `§16.1` adapter-scrape-deferral rationale paragraph and the `§28.4` obligation paragraph. Both are proposal rationale prose that lands in no tracked file, so neither reaches the ratchet. A future agent should not file them as N8 defects without first establishing that the text lands outside `proposals/`. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md (search `spec/16_observability.md:` and `spec/28_communication-channels.md:`)

WATCHOUT: review-log.md:3677 and :3877 use the same `spec/05_runtime-registry-and-pool-model.md:455` form. They are historical review-log prose, outside the ratchet's read domain, and editing them rewrites the audit record. Leave them. — EVIDENCE: review-log.md:3677,3877

### [non-spec-recheck.2.fix-G2.1]

DECISION: Staged the ten SPEC-3 carrier rows as a NEW deliverable CODE-10 (plus checklist S24, summary roster bullet, files-touched bullet, one `## Testing` line) rather than nesting the sweep inside CODE-3 — BECAUSE CODE-3's subject is SPEC-4's fence reduction, landed by S8 with `Depends on: S5`, while these sites carry SPEC-3's withdrawn reporting universal, landed by S4; nesting would drag CODE-3's four scope restatements and give one step two unrelated spec dependencies for no saving — ALTERNATIVES: the finding's own suggested CODE-3 nesting (rejected, above); distributing the sites over CODE-1/SCHEMA-1/a new deliverable (rejected: writes the reduction rule in three homes and puts SQL inside SCHEMA-1's fixed proto window); deleting the rows from the carrier table (rejected: `agentpodstate.go` and the `// diagnosis:` on `TestPerSlotCleanupStatedOnEverySessionModeRow` would keep asserting a rule the applied spec no longer states).

DECISION: The `migrations/0167_...up.sql` row's open cell ("the code lane decides whether a landed migration's comment is edited") is closed as "edited in place, no new migration" — BECAUSE the file carries no checksum or immutability gate and a comment-only edit changes no applied DDL; the project is pre-deployment.

FACT: The SPEC-3 carrier table's disposition column now reads `Mirrored by CODE-10` on the ten rows and `Staged by checklist S4's tier-11 sweep, re-key` / `, deletion` on `spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`. No `Deferred to the non-spec loop` cell survives — EVIDENCE: 0081...spec-changes.md:607-618. This is the only spec-changes edit this round; the fenced staged blocks were not touched.

WATCHOUT: At `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8`, a literal reading of the design's "delete the reporting clause" would strand the next sentence ("The request is session-scoped: ..."), which has no antecedent without the RPC clause, and would delete the very subject of the case. CODE-10 therefore stages deletion of the `at every session release` trigger and keeps the reporting clause, which is exactly the general arm ("delete that trigger clause and leave the rest of the sentence") — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-13.

FACT: `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` carries the withdrawn clause only in the `// diagnosis:` at :456-458. Its file-header comment (:18) and its failure message (:473) state that the per-slot cleanup runs at each session release with no reporting clause, which stays true under SPEC-3, so both are non-carriers and are untouched — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:18,:456,:473.

FACT: `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go`'s three "every release" comments are two doc comments (:422, :455) and one in-body comment (:444); the carrier table calls all three "doc comments". CODE-10 names them by function and position instead — EVIDENCE: scrubreport_server_test.go:422,:444,:455.

OPEN: `scrubreport_server.go` and `scrubreporter_seams.go` also carry the `per-release drain` / `per-release maxSessionsPerPod retirement` vocabulary outside the three clauses CODE-10 re-keys. Whether that naming should follow the cleanup-outcome report too is a question for a later round; the design explicitly scoped CODE-10 away from it.

UNVERIFIED: CODE-10 asserts no gate reads any of the ten comments. Verified for the `// spec:` glosses (section numbers unchanged, so `tests/spec-map.json` and `tests/claim-map.json` are unaffected) and for the tier-11 assertion sets. Nobody has grepped for a lint that reads Go doc-comment prose in these packages.

### [non-spec-recheck.2.fix-design-G1.1]
DECISION: Delete the parenthetical `(spec/05_runtime-registry-and-pool-model.md:455)` from the staged `answerShutdown` doc comment in CODE-1 (non-spec-changes.md:325), leaving the sentence ending at "...have been removed." — BECAUSE the retired line-citation form would land verbatim in pkg/adapter/session.go and trip the tier-0 ratchet on its first citation in an unregistered file, and the sentence already names §5.2 while the block closes on `// spec: §5.2 recycle lifecycle; §4.7 Shutdown recycle disposition.` — ALTERNATIVES: replacing it with a heading-form reference "(§5.2, Recycle lifecycle)" (rejected: it restates a citation the same comment already carries twice, and the caller directive prefers the reduction); seeding a baseline row in tests/registers/line-citations.yaml (rejected: the ratchet only writes counts downward and the register is `files: []`).
FACT: the line-citation ratchet's read scope excludes `proposals/` (scripts/specshift/scope/scope.go:95 `readExcludedPrefix = "proposals/"`), so a retired-form citation staged in a proposal's fenced Go block is invisible to tier 0 until the deliverable lands. Any fenced Go or markdown block in a proposal must be swept by hand for `spec/NN_*.md:<line>`. — EVIDENCE: scripts/specshift/scope/scope.go:95; tests/registers/line-citations.yaml:15; tests/tier0_static/line_citation_ratchet_test.go:244
FACT: no Go file in pkg/, cmd/ or tests/ carries the `spec/NN_*.md:<line>` spelling today, verified by grep; the ratchet is effectively a flat prohibition for Go. — EVIDENCE: tests/registers/line-citations.yaml:15 `files: []`
WATCHOUT: review-log.md:3677 and :3877 use the same spec/05:455 line-number form, but they are historical review-log prose, not staged deliverable text, and they are not in the ratchet's domain. Do not "fix" them; editing the log's historical entries is not this loop's work. — EVIDENCE: proposals/0081_.../...review-log.md:3677,3877


### [non-spec-recheck.2.fix-design-G2.1]

DECISION: The ten deferred SPEC-3 carrier rows get ONE new deliverable, `CODE-10`, appended after
CODE-9 and before CONF-1, modelled exactly on DOCS-4 (a small single-purpose deliverable with a
summary-roster bullet, a "no file" line in `## Testing`, one `## Files touched` bullet and an
appended checklist step S24, `Depends on: S4`, tiers 0 and 11) — BECAUSE the reduction those ten
sites take is SPEC-3's (the withdrawn "reports on every session release" universal, landed by
checklist S4), while CODE-3's subject is SPEC-4's fence reduction and the retired cleanup-timeout
trigger, landed by S5. ALTERNATIVES rejected: (a) the finding's own suggested fix, nesting the
sweep inside CODE-3 — it forces all four restatements of CODE-3's scope to move, turns the CODE-3
header into a fifteen-file title, and gives S8 two unrelated spec dependencies, while saving none
of the new text; (b) splitting the ten across CODE-1 (the pkg/adapter pair), SCHEMA-1 (the
migration) and a new deliverable — it writes the reduction rule three times; (c) folding the
migration row into SCHEMA-1 — SCHEMA-1 is the single proto window under staging rule S-2 and must
not carry unrelated SQL comment text.

DECISION: the open cell in the carrier table's migration row ("the code lane decides whether a
landed migration's comment is edited") is closed as EDIT IN PLACE, no new migration — BECAUSE the
tree edits landed migrations routinely and `0167` itself has been amended after landing three
times, and no checksum or immutability gate over `migrations/` exists.
EVIDENCE: `git log --oneline -- migrations/0167_runtime_definitions_execution_mode_service.up.sql`
returns `46db676ee`, `97d4a505d`, `e39eac915`, `7083d0e4f`; no checksum gate under `migrations/`
or `scripts/`.

FACT: a comment re-key at these sites needs NO `tests/spec-map.json` or `tests/claim-map.json`
edit. `spec-map.json` registers by file path and `path::TestName`, and the two `// spec:`
annotations that change (`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:20-21`,
`tests/tier4_integration/concurrent_delegation_proxy_test.go:48-49` and `:120-122`) change only the
parenthetical gloss, never the cited section numbers 4.7 / 5.2 / 6.1 / 8.2 / 4.9.
EVIDENCE: tests/spec-map.json:632, :947.

FACT: two of the twelve deferred rows are NOT part of this fix's new deliverable. Their
dispositions already name the step that applies SPEC-3, which is checklist S4, whose last sentence
carries `grep -rn sessions_served tests/`. The cheapest correction there is to re-word the cell to
"Staged by checklist S4's tier-11 sweep" and drop the misleading "Deferred to the non-spec loop".
EVIDENCE: spec-changes.md:608-609; implementation-checklist.md:23.

WATCHOUT: the SPEC-3 carrier table lives in `spec-changes.md` but is NOT inside a staged fence —
it is staging bookkeeping between the `**Scrub model.**` replacement block and the appended block.
Flipping a disposition cell therefore does not reopen the converged spec lane. Keep the edit to the
cells; add no prose to the table or to the paragraph above it.
EVIDENCE: spec-changes.md:585-618 (the table sits outside the ``` fences at :570-576 and :620+).

WATCHOUT: `pkg/adapter/gatewaycontrol/scrubreport.go` holds TWO comments naming a per-session-release
event, and only one is a carrier. The `SessionScrubOutcome` type comment ("the per-slot cleanup the
adapter runs on every session release") stays true and is explicitly excluded by the carrier table's
non-carrier paragraph; the `Client.ReportSessionScrub` method comment ("reports ... on every session
release") is the carrier. EVIDENCE: pkg/adapter/gatewaycontrol/scrubreport.go:12-14 vs :71-72.

WATCHOUT: three of the carriers state the served-count advance rather than the report, so a plain
clause deletion leaves them saying nothing. They take the re-key arm (per release → per
cleanup-outcome report), matching SPEC-3's §12.6 replacement "gateway-written on each
cleanup-outcome report". EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:195-196 and :472,
pkg/agentpodstate/agentpodstate.go:60; spec-changes.md:829.

OPEN: the term "per-release drain" / `RetireOnSessionCount`'s "per release" framing in
`scrubreport_server.go` and `pkg/gateway/session/recycle/scrubreporter_seams.go` survives this fix
because only the named clauses are re-keyed. After SPEC-3 the drain decision fires per
cleanup-outcome report, so the mechanism's own name reads one step stale. Someone should decide in a
later round whether §5.2's `**Session count limit:**` bullet still calls it per-release; if it does,
nothing is wrong. This is deliberately NOT pulled into this edit.

### [non-spec-recheck.2.review-applicability.1]

FACT: the line-citation ratchet is a live tier-0 gate over the WHOLE tracked tree with an EMPTY
baseline, so any tree file's FIRST retired-form citation fails tier 0. — EVIDENCE:
tests/registers/line-citations.yaml:15 (`files: []`),
tests/tier0_static/line_citation_ratchet_test.go:139 (`TestLineCitationRatchetCertifiesTheTree`),
scripts/specshift/gate/ratchet.go:22.

FACT: the retired form's path spelling is `(?:spec/)?\d{2}_[A-Za-z0-9._-]+\.md` followed by a
colon and a member, so `spec/05_runtime-registry-and-pool-model.md:455` matches and
`pkg/.../slotbinder.go:542` does NOT. Code-path `file:line` inside a staged Go comment is
therefore harmless; a spec-path one is a tier-0 failure. — EVIDENCE:
scripts/specshift/citation/grammar.go:121,:135.

FACT: `proposals/` is read-excluded from every specshift pass and gate, so a spec line citation
in the proposal's own commentary trips nothing. Only text a deliverable lands in a tree file
counts. — EVIDENCE: scripts/specshift/scope/scope.go:95 (`readExcludedPrefix = "proposals/"`).

WATCHOUT: the only spec-path line citation staged into tree text is at
non-spec-changes.md:325, inside CODE-1's `answerShutdown` doc comment (the 2026-09-21
open-decisions delta that closed open decision 35). The whole rest of the ~170 `file:line`
claims in the non-spec staging are commentary or code paths and are fine. Grep for the finding
with `grep -nE '(spec/)?[0-9]{2}_[A-Za-z0-9._-]+\.md:[0-9]' non-spec-changes.md` and then decide
per hit whether it lands in a tree file. — EVIDENCE:
proposals/0081_*/0081*.non-spec-changes.md:325.

FACT: the same citation is also doomed on content. SPEC-3 inserts a nine-row table and two
paragraphs into §5.2 immediately after the `**Scrub model.**` paragraph at spec/05:453, so the
Recycle-lifecycle sentence now at spec/05:455 moves down many lines before S16/S17 land the
comment. — EVIDENCE: spec-changes.md ~570-640; spec/05_runtime-registry-and-pool-model.md:453,:455.

FACT: SPEC-3's carrier table defers TWELVE carriers to this lane and the non-spec staging stages
NONE of the ten that are not the §12.6 tier-11 pair. `grep -n "agentpodstate\|leasecontrol\|
scrubreporter_seams\|sessionscrubreporter\|migrations/" non-spec-changes.md implementation-checklist.md`
returns nothing. The two that ARE covered are covered only by S4's `grep -rn sessions_served tests/`.
— EVIDENCE: spec-changes.md:603-615; the ten carriers verified live in the tree at
pkg/adapter/sessionscrubreporter.go:13, pkg/adapter/gatewaycontrol/scrubreport.go:13,:72,
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472,
pkg/gateway/session/recycle/scrubreporter_seams.go:245, pkg/agentpodstate/agentpodstate.go:60,:128,
tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122,
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458.

USEFUL [the review log's `### Settled` anchor inventory]: every DOCS-1/2/3 verbatim anchor
re-verified byte-for-byte in one pass (state-machines.md:138,:235,:237,:251;
adapter-contract.md:10,:53,:64,:75,:81; error-catalog.md:129). Do not re-run that sweep.

FACT: the checklist itself is clean under this lens. 21 deliverables, 23 steps, every deliverable
named at least once, no step naming an unstaged deliverable, one lane per step, the six spec steps
leading with no interleave, every `Depends on:` resolving to an earlier step, and every box
unchecked. `TestShutdownMessagePostRemovalDescriptor_spec_4_1`'s `wantReq`/`assertFieldSet` shape
matches S9's description exactly; the two mandatory-field sweeps return 46 and 64 literals, matching
the ledger. Do not re-derive any of this.

OPEN: which step lands `answerShutdown`'s doc comment is not stated. The comment names
`removeSlotTreeVia`, which S17 creates, while the helper reads as S16's. Too thin to file, but a
reconciliation pass could pin the helper's comment to S17 and remove the ambiguity.

### [non-spec-recheck.2.review-citations.1]

DECISION: filed exactly one finding, the spec LINE citation inside the CODE-1 `answerShutdown` Go comment the open-decisions firing for entry 35 added — BECAUSE it is the only text in this lane nobody has read, its content is accurate but its FORM is the retired one N8 prohibits and the tier-0 ratchet fails a Go file on its first such citation — ALTERNATIVES: filing the same paragraph's "The helper is the handler's only exit" as a self-contradiction against the same deliverable's ":514-516" sentence naming the two returns that bypass the helper (rejected as wording: the qualified form "only exit after the two-field precondition" stands twenty lines up in the same comment, so no rule is misimplementable — but the remedy, deleting the restating clause, is a reduction if a single-source lens wants it); re-running the whole ~170-citation non-spec sweep (rejected: `### Settled` records it as done and the delta is 12 lines).

FACT: the whole non-spec delta since this lane last converged is TWELVE lines, one commentary paragraph in CODE-1's `answerShutdown` comment block, plus two summary hunks (entry 35/38/39/40 removals and the new unstaged-defects row). `git diff HEAD -- <the two files>` is the whole of it; the only committed delta is `a4e2ce503`'s 24 lines of running-boundary vocabulary — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:319-329.

FACT: every `file:line` citation in the new summary text re-verifies exactly, so a later lens can skip them — EVIDENCE: pkg/adapter/session.go:271 (`_ = removeSlotTree(st)`), pkg/adapter/sessionscrubreporter.go:39-44 (`sessionScrubOutcome`), pkg/adapter/podscrub.go:139,:155 (`slotCredentialFiles`, `slotWorkspaceTrees`), pkg/gateway/runtime/slothealth/slothealth.go:121,:136, pkg/gateway/session/recycle/scrubreporter_seams.go:184, pkg/adapter/slot.go:219. The `session.go:272` → `:271` correction the firing made is right.

FACT: the new Go comment's quotation of §5.2 is faithful — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455 "deployer-defined `cleanupCommands` execute ... after every ended session's per-slot tree and credential lease have been removed ... the Lenny whole-pod scrub runs". Only the citation's FORM is the defect, never its content.

FACT: the tier-0 line-citation ratchet reads the whole tracked tree minus `proposals/`, `testdata/` and the two registers, Go files included, and `tests/registers/line-citations.yaml` is `files: []`, so ANY first line citation in a `pkg/` file fails tier 0 — EVIDENCE: tests/tier0_static/line_citation_ratchet_test.go:139 `TestLineCitationRatchetCertifiesTheTree`, :244 `TestLineCitationRatchetFailsOnAFirstCitationInAnUnregisteredFile`; scripts/specshift/scope/scope.go:95 `readExcludedPrefix = "proposals/"`; scripts/specshift/citation/grammar.go:118-121 `refExpr` admits `(?:spec/)?\d{2}_....md` and the colon branch. A tree-wide grep for that spelling in `pkg/ cmd/ tests/` *.go returns zero today.

WATCHOUT: the proposal files themselves may carry `file:line` citations freely (the `proposals/` prefix is outside the gate's read domain, and prune 3 bans them in `spec-changes.md` by decision rather than by gate). The prohibition bites only on text the staging asks an implementor to WRITE INTO the tree: a Go comment, a doc page, a schema comment, a test comment. Check the fenced code blocks, not the prose — EVIDENCE: scripts/specshift/scope/scope.go:95.

FACT: the twelve "Deferred to the non-spec loop" rows of the SPEC-3 carrier table all name real files and real symbols that really carry the withdrawn universal; the attribution audit on them is clean, so the lane's remaining work is the EDIT, not a re-verification — EVIDENCE: spec-changes.md:607-618 against pkg/adapter/sessionscrubreporter.go:12-13 ("on every session release"), pkg/adapter/gatewaycontrol/scrubreport.go:71-72, pkg/agentpodstate/agentpodstate.go:60, pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:195,:441, pkg/gateway/session/recycle/scrubreporter_seams.go:244.

USEFUL [the review log's `### Settled` line on the near-exhaustive non-spec citation sweep]: it let this pass spend its whole budget on the twelve-line delta and on citation FORM rather than re-resolving 261 citations, which is how the ratchet defect surfaced at all.

### [non-spec-recheck.2.review-docs-alignment.1]

FACT: the whole docs surface of this proposal is verified quote-for-quote as of today. DOCS-1's four
anchors all match the tree (docs/reference/state-machines.md:138, :235, :237, :251); DOCS-2's four
(docs/reference/adapter-contract.md:10, :53, :64, :75, :81); DOCS-3's five sentence/remedy quotes
(docs/reference/error-catalog.md:129); DOCS-4's two (docs/reference/execution-modes.md:68,
docs/operator-guide/security-principles.md:33). A later round need not re-verify these unless the
tree moves. EVIDENCE: docs/reference/state-machines.md:235, docs/reference/adapter-contract.md:75

FACT: the "and the adapter reports its outcome to the gateway" clause has exactly two carriers in
docs/ (`grep -rn "adapter reports its outcome" docs/ spec/`), both assigned to DOCS-4. The third hit
is spec/29_communication-scenarios.md:748 and is about `ReportPodScrub`, not the per-slot report.
EVIDENCE: docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33

FACT: no docs/ page names `lenny_adapter_leaked_slots`, `bind_attempt`, `unconditional_teardown`,
`SlotReclaimOutcome` or either new adapter `ErrorCode`, and no docs/ page enumerates the adapter
`ErrorCode` enum at all. There is therefore no docs mirror owed for SCHEMA-1's enum additions beyond
DOCS-2/DOCS-3. EVIDENCE: grep over docs/ returns nothing for all five terms

FACT: DOCS-2's two absolute spec links are precedented and sanctioned. docs/about/style-guide.md:31
tells authors to "Cite the spec section with a link ... Treat the spec like RFC references", and
docs/ carries 53 such github.com/lennylabs/lenny/blob/main/spec links already. Both anchors resolve
(`#### 4.7.1 Role and Gateway RPC Contract` at spec/04_system-components.md:659, `### 15.4 Runtime
Adapter Specification` at spec/15_external-api-surface.md:1458). Do not file this as a doc-content.md
violation. EVIDENCE: docs/about/style-guide.md:31

CORRECTS [the ledger UNVERIFIED on troubleshooting.md:41]: `reason: setup_command_failed` at
docs/operator-guide/troubleshooting.md:41 is the **warm-pool warmup-failure label**, not the REST
envelope reason. The row sits in the "Warm Pool Exhaustion / Common causes and resolution" table
whose sibling rows are `image_pull_error`, `resource_quota_exceeded` and `node_pressure`, i.e. the
`lenny_warmpool_warmup_failure_total` error_type set (docs/reference/metrics.md:135). DOCS-3 does not
reach it and it stays true. The question is settled; drop the UNVERIFIED.
EVIDENCE: docs/operator-guide/troubleshooting.md:38-43, docs/reference/metrics.md:135

FACT: docs/api/internal.md is confirmed wholesale pre-existing drift (`StopSession` at :83/:139/:146,
`bool clean_exit = 1` at :157, no `Shutdown` anywhere). Nothing this proposal stages newly falsifies
it. Re-confirmed this round; do not re-derive. EVIDENCE: docs/api/internal.md:83, :157

WATCHOUT: the twelve "Deferred to the non-spec loop" rows of the SPEC-3 carrier table are still
unstaged in the non-spec file, but TWO of them are already discharged by checklist S4's sweep
instruction ("The step runs `grep -rn sessions_served tests/` and re-keys every tier-11 assertion
pinning a §12.6 write clause ... and deletes every assertion pinning the read clause"): both
`spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go` contain
`sessions_served` and so fall inside that grep. The other ten do not; `migrations/0167_*.up.sql` is
outside the grep's `tests/` scope, and none of the seven pkg/ comment carriers nor
`concurrent_delegation_proxy_test.go` nor the two tier-11 header/diagnosis carriers contains the
string. Whoever fixes this should stage the remaining ten and leave S4's two alone rather than
restate them. EVIDENCE: 0081_*.implementation-checklist.md:23, `grep -rn sessions_served tests/`

WATCHOUT: the hand edit that closed open decision 35 added a 12-line comment paragraph to CODE-1
that cites a spec LINE, `(spec/05_runtime-registry-and-pool-model.md:455)`. The tree has ZERO
file:line spec citations in Go (`grep -rn "spec/[0-9][0-9][a-z_-]*\.md:[0-9]" --include=*.go pkg/
cmd/ tests/` returns nothing), and channel-naming N8 bans the form "in any spelling". The
line-citation ratchet only matches the `§X line L` spelling, so no gate goes red and this will
survive unless a reviewer catches it. The same comment block already ends `// spec: §5.2 recycle
lifecycle`, so the remedy is a deletion of the parenthetical.
EVIDENCE: 0081_*.non-spec-changes.md:325, .claude/rules/channel-naming.md N8

FACT: the four new citations the spec-changes delta added to the SPEC-3 ten-second commentary all
verify: spec/11_policy-and-controls.md:264 ("wait up to 10s, then SIGKILL"),
pkg/adapter/session.go:219-224, pkg/adapter/socketruntime.go:470-473
(`defaultSocketShutdownGrace = 10 * time.Second`), pkg/adapter/mcpruntime.go:86
(`defaultMCPShutdownGrace = 5 * time.Second`). `MCPRuntime.ShutdownGrace` (mcpruntime.go:70) is set
nowhere outside `_test.go`, so "set nowhere" holds. EVIDENCE: pkg/adapter/mcpruntime.go:70,86

FACT: the six citations the summary's new unstaged-defect row (ex-decision-38) carries all verify:
pkg/adapter/session.go:271 is `_ = removeSlotTree(st)`; sessionscrubreporter.go:39-44 is
`sessionScrubOutcome(closeErr)`; podscrub.go:132-156 is `slotCredentialFiles`/`slotWorkspaceTrees`,
both on-disk; slothealth.go:121-134 is `RecordLeak`/`Unhealthy`; scrubreporter_seams.go:184-197 is
`drainLedger.RecordLeak`; slot.go:219-225 is `runtimeForSession` returning the pod-global
`s.Runtime` for any registered slot. Note the row corrected the old decision 38's `session.go:272`
to `:271`, and `:271` is the right line. EVIDENCE: pkg/adapter/session.go:271, pkg/adapter/slot.go:219

FACT: DOCS-4's gate claim holds. `TestPerSlotCleanupStatedOnEverySessionModeRow`
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:462) reads only the
residual-state table rows through `residualStateTable`, anchored on the header
`| Configuration | Scrub at session release`, and asserts the substring "Per-slot cleanup". Deleting
the reporting clause from the prose sentence below the table does not reach it.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:465-476, :485

FACT: DOCS-3's "nothing holds the two to one text" is true. `grep -rn "error-catalog" tests/ scripts/
cmd/ Makefile` returns nothing. EVIDENCE: verified this round


### [non-spec-recheck.2.review-edit-sites.1]

DECISION: filed three findings — the ten SPEC-3 carrier-table rows marked "Deferred to the non-spec loop" that no non-spec deliverable stages, a retired spec LINE CITATION inside a staged Go comment, and SCHEMA-1's stale "Two comment sentences" lead-in count — BECAUSE each is a surface that becomes wrong or a gate that goes red once the staging is applied, and each remedy lands in the non-spec staging. ALTERNATIVES: splitting the ten carriers into ten findings (rejected: one area, one remedy — one deliverable or one sweep paragraph); filing the two tier-11 carrier rows too (rejected: checklist S4 already carries `grep -rn sessions_served tests/` and the §12.6 re-key for them).

FACT: the line-citation ratchet baseline `tests/registers/line-citations.yaml` is now `files: []`, a FLAT PROHIBITION, and `grep -rnE "spec/[0-9]{2}_[A-Za-z0-9._-]+\.md:[0-9]+" pkg/ cmd/ tests/ --include=*.go` returns NOTHING today. Any staged Go comment carrying that form is the tree's first and fails tier 0. — EVIDENCE: tests/registers/line-citations.yaml:15, scripts/specshift/citation/grammar.go:132-145 (headExpr's path-form colon branch), tests/tier0_static/line_citation_ratchet_test.go:139
FACT: `proposals/` is EXCLUDED from the ratchet's read domain (`readExcludedPrefix = "proposals/"`), so the proposal's own prose citations at non-spec-changes.md:2086 and :2471 are harmless; only text inside a staged code block that lands in `pkg/` matters. Grep the staged fenced Go blocks, not the whole file. — EVIDENCE: scripts/specshift/scope/scope.go:99

FACT: the twelve deferred carriers verify in the tree, at `pkg/agentpodstate/agentpodstate.go:60,:128`, `pkg/gateway/session/recycle/scrubreporter_seams.go:245`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472`, `pkg/adapter/sessionscrubreporter.go:13`, `pkg/adapter/gatewaycontrol/scrubreport.go:72`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go:422,:444,:455`, `tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122`. The one-command check is `grep -n "every session release\|each session release\|on every release" <files>`.
WATCHOUT: `pkg/adapter/gatewaycontrol/scrubreport.go` holds TWO comments on this surface and only `:72` is a carrier; `:13` states the CLEANUP runs on every release and stays true. The standing context already records this; do not widen the re-key to `:13`.

FACT: SCHEMA-1 DOES stage the `SESSION_SCRUB_OUTCOME_LEAKED` comment replacement, which settles the disagreement `### Open` records between `[non-spec-recheck-2.1.fix-G3.1]` and `[index-reconcile.1]`. The block makes FIVE comment replacements (RPC RELEASED/LEAKED sentence, the two enum-value comments, the RPC report-universal sentence, the request-message opening sentence). — EVIDENCE: non-spec-changes.md SCHEMA-1 `**The scrub-outcome and report-trigger comments.**` block

FACT: `docs/operator-guide/observability.md` contains NO `lenny_slot_*` or `lenny_adapter_*` row at all, so the `### Open` question "does observability.md owe rows for the two SPEC-6 counters" answers NO. — EVIDENCE: `grep -n "lenny_slot\|lenny_adapter" docs/operator-guide/observability.md` returns nothing.
FACT: `lenny_slot_failure_total`'s `error_type` value set is enumerated ONLY in the Go const block; spec/16:14 and §16.1.1 name the label and no value. CODE-9's new `slotFailureWorkspaceFinalize` stage constant therefore owes no spec or docs edit. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:286-298, spec/16_observability.md:14, :302
FACT: the CODE-3 retired-`leaked`-trigger sweep is COMPLETE. `grep -rn "cleanup tim" pkg/ --include=*.go | grep -v _test.go` returns nine sites; seven are CODE-3's, and `poolstore.go:569` and `recycleboundary.go:63` are about `cleanupTimeoutSeconds` config rather than the edge trigger. `docs/reference/state-machines.md:251` is DOCS-1's.
FACT: the `releaseSessionSlot` production call-site arithmetic in checklist S15 is right: `session.go` 3 + `sdkwarm.go` 4 = seven; `resume.go`'s seven go to the under-guard form. `ReleaseSlotForTest` has exactly five callers and the files-touched list names all five.

UNVERIFIED: whether any OTHER staged fenced code block in the non-spec staging carries a retired citation spelling the path-form regex does not catch (for example `§5.2 line 455`). I grepped `§[0-9.]+ lines? [0-9]` and the path form and found only the one site; a spelling neither pattern covers would be a gap in my sweep.

### [non-spec-recheck.2.review-fresh.1]

FACT: the SPEC-3 carrier table has TWELVE rows dispositioned "Deferred to the non-spec loop", and exactly TWO of them are discharged elsewhere — `spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`, both reached by checklist S4's `grep -rn sessions_served tests/` sweep. The other TEN are in no deliverable, no sweep, no checklist step and no `## Files touched` entry. — EVIDENCE: spec-changes.md:607-618 (the twelve rows); implementation-checklist.md:23 ("The step runs `grep -rn sessions_served tests/` …"); non-spec-changes.md:3781-4019 (Files touched, none of the ten).
FACT: the sweep-coverage test is mechanical and I ran it: `grep -c sessions_served` returns 5 for `spec_28_register_writers_test.go`, 3 for `concurrent_slot_lifecycle_doc_reconciliation_test.go`, and 0 for `session_scrub_report_addressing_doc_reconciliation_test.go` and `basic_level_echo_stamp_doc_reconciliation_test.go`. Do not assume S4's sweep reaches a tier-11 file just because it is a tier-11 file. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458 ("and the adapter reports its outcome to the gateway", the exact clause DOCS-4 deletes from the two docs pages).
FACT: SCHEMA-1 DOES now stage the `SESSION_SCRUB_OUTCOME_LEAKED` comment replacement, which settles the disagreement `### Open` records between `[non-spec-recheck-2.1.fix-G3.1]` and `[index-reconcile.1]`. The fix-firing entries were right; the `[index-reconcile.1]` carry-forward is stale. — EVIDENCE: non-spec-changes.md:2344-2360 (the verbatim/becomes pair over `schemas/lenny-adapter.proto:445-448`).
CORRECTS [the Traps entry "MISTAKE, one repeated finding: the `\"The outcome is\"` proto orphan"]: the orphan is CLOSED in the current staging and is no longer a live defect. SCHEMA-1's report-trigger replacement deliberately quotes "The outcome is" as trailing words of the same physical line and drops them, and the separate scrub-outcome replacement rewrites the following sentence to open with "RELEASED and LEAKED are the outcomes §5.2 states…", so the two replacements compose into whole sentences. Do not re-file it. — EVIDENCE: non-spec-changes.md:2317-2324 and :2366-2380 against `schemas/lenny-adapter.proto:308-313`.
FACT: every code/spec citation added by the uncommitted open-decisions delta verifies exactly. `spec/05:455`, `spec/11:264`, `pkg/adapter/session.go:219-224` and `:271`, `pkg/adapter/socketruntime.go:470-473`, `pkg/adapter/mcpruntime.go:86`, `pkg/adapter/sessionscrubreporter.go:39-44`, `pkg/adapter/podscrub.go:132-156`, `pkg/gateway/runtime/slothealth/slothealth.go:121-134`, `pkg/gateway/session/recycle/scrubreporter_seams.go:184-197`, `pkg/adapter/slot.go:219-225`. `MCPRuntime.ShutdownGrace` is indeed set nowhere in production (only `mcpruntime.go:67-70` and `shutdown_grace_internal_test.go`). Do not re-verify these.
FACT: CODE-1's staged predicates agree row-for-row with the §5.2 disposition table. `exited_cleanly = closeErr == nil && (live || treeErr == nil)` yields Set/Not-set exactly as rows 628-632 state, and the `if live` report gate yields `released`/`leaked`/None exactly as the report column states. Checked all five `Shutdown` rows; do not re-derive. — EVIDENCE: spec-changes.md:628-632 against non-spec-changes.md (split-gates block and `return answerShutdown(outcome, closeErr == nil && (live || treeErr == nil), false)`).
FACT: "these four and `one_session_only_test.go` hold every `ReleaseSlotForTest` call site" is exact. `grep -rn ReleaseSlotForTest pkg/ tests/` returns five call sites (checkpoint_stream, credexpiry, integrationlevel, one_session_only, tracingcontext_addressing) plus the definition in `pkg/adapter/export_test.go:28,:32`. A lens reading "four" against six listed files will misread it.
WATCHOUT: `docs/reference/state-machines.md` line anchors in DOCS-1 (`:138`, `:235`, `:237`, `:251`) are all still exact at HEAD, as are `docs/reference/adapter-contract.md` `:10`, `:53`, `:64`, `:75`, `:81`. Re-checked this round; they have not drifted.

### [non-spec-recheck.2.review-mechanism.1]

FACT: the working-tree delta this recheck was launched for is exactly two hunks (`git diff HEAD -- proposals/0081_*/0081*.non-spec-changes.md 0081*.summary.md`): one new paragraph in CODE-1's `answerShutdown` rationale comment recording the scrub's order against the per-slot cleanup (closing open decision 35), and the summary's move of decisions 35/38/39/40 out of the open list. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:319-329

FACT: every citation in that new CODE-1 paragraph re-verifies. `spec/05_runtime-registry-and-pool-model.md:455` is the `**Recycle lifecycle**` paragraph and does carry "after every ended session's per-slot tree and credential lease have been removed". The summary's new unstaged-defects row also re-verifies: `pkg/adapter/session.go:271` is `_ = removeSlotTree(st)`, `pkg/adapter/podscrub.go:132-156` is `slotCredentialFiles`/`slotWorkspaceTrees` enumerating on-disk children, `pkg/gateway/runtime/slothealth/slothealth.go:121-134` is `RecordLeak` plus the unpruned-leak note, `pkg/adapter/slot.go:219-225` is `runtimeForSession`. Do not re-check these. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455; pkg/adapter/session.go:271

FACT: CODE-1's outcome/report/clean-exit predicates agree row for row with the staged §5.2 disposition table, checked in both directions. `exited_cleanly = closeErr == nil && (live || treeErr == nil)` gives Set/Not set/Set/Set/Not set on rows 1-5; `if live { reportSessionScrub(closeErr) }` gives released/leaked/released/None/None; `completed = closeErr == nil && treeErr == nil` gives the hold column. `answerShutdown(outcome, true, …)` on the two refusal arms matches rule 15's clean-exit clause. No predicate drift. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:447-502 against 0081_....spec-changes.md:628-636

FACT: CODE-6's `ensureSlotStateLocked` switch maps one-to-one onto staged rules 3,4,5,6,7 with the hold ahead of the map lookup as rule 2, and CODE-1's `(attempt == "") == !unconditional` is exactly rule 10 in all four field combinations. Both verified by truth table; do not re-derive. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1586-1634, :206-210

FACT: the two gateway `Shutdown`-caller line citations CODE-1's comment rests on are exact — `pkg/gateway/podlifecycle/podsession/slotbinder.go:542` (`result.Adapter.Shutdown`) then `:574` (`ShutdownRecycle`), sequential, and `binder.go:2037`/`:2043` are the mutually exclusive arms of `shutdownAdapter`, recycle first. So "the concurrent release answers ABSENT, the session-mode release answers RECLAIMED" holds. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:542,:574; binder.go:2031-2044

FILED: the SPEC-3 carrier table's ten "Deferred to the non-spec loop, code-lane comment re-key" rows are discharged by NOTHING in the non-spec staging. Checklist S4's `grep -rn sessions_served tests/` reaches only the two tier-11 gate rows (`spec_28_register_writers_test.go`, `concurrent_slot_lifecycle_doc_reconciliation_test.go`); the other ten carriers hold no `sessions_served` string, or live outside `tests/`, and appear in no CODE-n deliverable, no `## Files touched on application (non-spec)` entry and no checklist step. Verified live in the tree: `pkg/adapter/sessionscrubreporter.go:13`, `pkg/adapter/gatewaycontrol/scrubreport.go:72`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472`, `pkg/gateway/session/recycle/scrubreporter_seams.go:245`, `pkg/agentpodstate/agentpodstate.go:60,:128`, `migrations/0167_runtime_definitions_execution_mode_service.up.sql:102`, `tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122`, `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6,:21`, and `TestPerSlotCleanupStatedOnEverySessionModeRow`'s `// diagnosis:` tail at `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-461`.

WATCHOUT: the grep for these carriers must use several patterns. `every session release`, `each session release`, `on every release` and `is incremented at each session` each find a different subset, and the migration's carrier wraps mid-line ("incremented at each session" / "release"). A single-pattern sweep undercounts. — EVIDENCE: migrations/0167_runtime_definitions_execution_mode_service.up.sql:102

USEFUL [the `### Traps` entry on `pkg/adapter/gatewaycontrol/scrubreport.go` holding TWO comments, only one a carrier]: it stopped this lens filing `:13` (the CLEANUP universal, which survives) alongside `:72` (the REPORT universal, which does not). Only `:72` is in the finding.

UNVERIFIED: whether the carrier table's own two tier-11 rows are correctly described as "in the step that applies SPEC-3" when SPEC-3 is a spec-lane step (S4) whose stated tiers are 0 and 11 — the re-key is a `tests/` edit landing inside a spec step's write lease. `[spec-recheck]`'s open entry on whether the docs-lane write lease admits `tests/tier11_docs/*.go` is the same question one lane over. Whoever lands S4 settles it.

### [non-spec-recheck.3.fix.1]

DECISION: rewrote the 0079 impact row's no-collision ground in `summary.md` from "opens neither `pkg/sandbox/podscrub` nor `pkg/controller/sandbox`" to a file-level statement — BECAUSE this round's own fix added `pkg/controller/sandbox/podspec/podspec.go` to CODE-10 (`non-spec-changes.md` CODE-10 heading and site bullet, the `## Files touched` entry, the SPEC-3 carrier table row in `spec-changes.md`, and the summary's deliverable index), which falsified the package-level denial while leaving the row's conclusion correct on the facts — ALTERNATIVES: dropping `pkg/controller/sandbox` from CODE-10 (rejected: the carrier is a live one and the reduction is owed); leaving the row and noting the exception elsewhere (rejected: a reader checks the ground at the row).

FACT: the conclusion survives at file granularity. 0079 stages `pkg/controller/sandbox/controller.go` (CODE-1) and `pkg/controller/sandbox/controller_test.go` (TEST-3); CODE-10 here opens only `podspec/podspec.go`, and only its `PodNameEnvVar` doc comment, the sidecar adapter container comment and the `podNameEnv` doc comment. The row's other three grounds still hold: `pkg/sandbox/podscrub` is opened nowhere (the staged `pkg/adapter/podscrub.go` sites are a different package), and `pkg/adapter/socketruntime.go` is untouched, the staged neighbour being `pkg/adapter/socketruntime_test.go`. — EVIDENCE: proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:554,:566; non-spec-changes.md CODE-10 site list and `## Files touched on application (non-spec)`

WATCHOUT: any later round that adds or removes a file under `pkg/controller/sandbox` or `pkg/sandbox/podscrub` in the non-spec staging must re-read this row. Its ground is now a file-level enumeration, so it goes stale on a file change rather than on a package change.

### [non-spec-recheck.3.fix-G1.1]

DECISION: two carriers of the withdrawn "reports on every session release" universal joined CODE-10 on the delete arm, `pkg/adapter/gatewaylink.go` (the `ConnectGateway` `SessionScrubReporter` retention comment) and `pkg/controller/sandbox/podspec/podspec.go` (the `PodNameEnvVar` doc comment, the sidecar adapter container comment and the `podNameEnv` doc comment) — BECAUSE CODE-10 owns comment carriers and each reduction is a plain quantifier deletion — ALTERNATIVES: CODE-1 (a behavioural deliverable with a fixed pkg/adapter file set that excludes gatewaylink.go), a re-key (adds a rule statement at a site that does not own it), a new controller-side deliverable (pure accretion).
DECISION: the §12 evaluation-point gloss on `scrubreport_server.go`'s `SessionCountRetirer` comment and on the `...PostIncrementCount` test comment is RE-KEYED onto the write §12.6 keeps rather than deleted — BECAUSE the deletion drags `tests/spec-map.json` (the test is the sole entry under section 12), CODE-10's "no annotation loses a section number" close and checklist S24's closing clause with it — ALTERNATIVES: delete the `§12 (...)` clause (the finding's own suggestion, rejected as the larger edit).
FACT: `pkg/adapter/gatewaylink.go:70-77` carries the universal in TWO fragments, `per-session-release` and `on every slot release`; deleting only the second leaves it standing. EVIDENCE: pkg/adapter/gatewaylink.go:70-77
FACT: `pkg/agentpodstate/agentpodstate.go:67-69` (the `SessionsServed` FIELD comment) carries no trigger and is not a carrier; the trigger sits in the `RecycleCounters` type comment at :57-64 and in `IncrementSessionsServed` at :124-131. The SPEC-3 carrier row named the wrong one. EVIDENCE: pkg/agentpodstate/agentpodstate.go:57-69,124-131
FACT: `pkg/controller/sandbox/podspec/podspec.go:686-688`, the embedded-runtime comment, states no universal and stays true; the other three pod-identity comments do state it. EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:686-688
MISTAKE: CODE-10 put `IncrementSessionsServed` on the delete arm while its own arm rule, its reasoning sentence and summary.md's roster all assign a served-count trigger to the re-key arm. Applying it as written would have stripped the method's only statement of when it is called, asymmetric with `IncrementScrubFailureCount`, which keeps its trigger.
WATCHOUT: the `scrubreport_server_test.go` bullet carried a stale count ("All three ... so all four take the same arm") that named five comments; it is reworded to name no count. EVIDENCE: non-spec-changes.md, the CODE-10 site list
WATCHOUT: CODE-10's file set is stated in FOUR places and all four must move together: the SPEC-3 carrier table (spec-changes.md), the CODE-10 heading and site list, the "Files touched on application (non-spec)" entry, and summary.md's Deliverable Index parenthetical. Checklist S24 states the set by CATEGORY, so a file in a new category (the pod-spec pod-identity comments) needs it edited too.
UNVERIFIED: whether any further carrier of the withdrawn universal remains outside the table. The sweep that found these two searched `pkg/`; a later round should grep the tree for `on every session release`, `each per-slot cleanup outcome` and `per-session-release` before declaring the table closed.

CORRECTS (post-fix): this pass added the `pkg/adapter/sessionscrub_emit_test.go` site bullet on the DELETE arm while CODE-10's own arm rule sends every served-count carrier to the RE-KEY arm, and the paragraph recorded one exception only, to the first arm. The delete stands, because the sentence's subject is the cleanup-outcome report the case asserts (`pkg/adapter/sessionscrub_emit_test.go:92-93`, "This advances / sessions_served (feeding maxSessionsPerPod) on every clean release", where "This" denotes the emitted report), so re-keying the trigger onto that report would restate the subject. The rule now records the second exception: the two-arm paragraph names the file and the ground, and the site bullet cites the exception rather than standing alone against the rule.
CORRECTS (post-fix): `summary.md`'s CODE-10 parenthetical still read "the comments that state the served-count trigger are re-keyed onto the cleanup-outcome report" after this pass added a served-count carrier that is deleted instead; it now carries the exception clause, so the index sentence and the deliverable agree.
WATCHOUT: the two exceptions are asymmetric and a later round must keep them apart. The first-arm exception (`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go`) takes a replacement clause; the second-arm exception (`pkg/adapter/sessionscrub_emit_test.go`) takes a plain deletion. Any further served-count carrier whose subject is the report itself joins the second exception and its file must be named in the paragraph, not left to the bullet.


### [non-spec-recheck.3.fix-design-G1.1]

DECISION: the two §12 evaluation-point glosses (`scrubreport_server.go:216-217`, `scrubreport_server_test.go:460`) are RE-KEYED onto the write §12.6 keeps ("`sessions_served` incremented on each cleanup-outcome report"), not deleted — BECAUSE the section number then stays, so CODE-10's "no `// spec:` annotation loses a section number" (non-spec-changes.md:2175-2176) and checklist S24's "No assertion and no cited section number moves" (implementation-checklist.md:63) both stay true, `tests/spec-map.json` (hand-maintained, not generated: `scripts/refactor/audit.go:80`) needs no edit, and the gloss is truthful against the staged §12.6 replacement (spec-changes.md:800-804). The evaluation point's home is §5.2's `**Session count limit:**` bullet, already cited in both annotations, so the site cites the home and nothing is restated. ALTERNATIVES: the finding's suggested deletion of the `§12 (...)` clause — rejected: same one added sentence, plus a spec-map.json row removal, plus two falsified restatements, for no gain. In-tree precedent for a surviving §12 gloss on this surface: `scrubreport_server.go:456` "§12 (RETURNING sessions_served)".

DECISION: `pkg/adapter/gatewaylink.go` and `pkg/controller/sandbox/podspec/podspec.go` join CODE-10 (one carrier-table row each, one site bullet each), not a new deliverable and not CODE-1 — BECAUSE CODE-10 is the comment-carrier deliverable and CODE-1 (non-spec-changes.md:161) is a behavioural deliverable over a fixed pkg/adapter file set that does not include gatewaylink.go. ALTERNATIVES: deleting the exhaustive CODE-10 file lists from summary.md:1071 and the files-touched entry and citing the deliverable instead — rejected here: every deliverable in the summary's Deliverable Index carries its file list and the files-touched section is a whole-proposal manifest, so changing that convention is a proposal-wide restructure, not this edit.

FACT: the withdrawn "reports on every session release" universal has NO further unstaged non-test Go carrier. `grep -rn ReportSessionScrub --include=*.go pkg/ cmd/ tests/` (minus `_test.go` and `pkg/proto`) returns, beyond the table's rows, only `pkg/adapter/session.go:273-277`, `pkg/agentpodstate/memstore/memstore.go:209` and `pkg/agentpodstate/pgstore/pgstore.go:302-304`, none of which carries a universal quantifier. EVIDENCE: pkg/adapter/session.go:273 ("Report the cleanup outcome so the gateway advances sessions_served" — this site, existential).

FACT: `podspec.go` has three carriers and one non-carrier. Carriers: `:167-169` (`PodNameEnvVar`), `:590-593` (sidecar adapter container), `:905-907` (`podNameEnv`), each "the adapter reports **each** per-slot cleanup outcome". Non-carrier: `:686-688`, the embedded-runtime comment, which says only "it emits ReportSessionScrub and ReportPodScrub keyed on the pod identity". EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:686-688.

MISTAKE: the SPEC-3 carrier table's agentpodstate row (spec-changes.md:617) names "the `SessionsServed` field comment", which carries no trigger (pkg/agentpodstate/agentpodstate.go:67-69); the carrier is the `SessionsServed` clause of the `RecycleCounters` type comment (:57-64), which CODE-10's bullet already names correctly. Cost: a reader following S24 through the table edits the wrong comment.

MISTAKE: CODE-10's bullet put `IncrementSessionsServed` on the delete arm while its own arm rule, its reasoning sentence ("so all four take the same arm", non-spec-changes.md:2143-2146) and summary.md:1071 all assign it to re-key. The comment (pkg/agentpodstate/agentpodstate.go:124-131) states a served-count advance keyed on the release, which is arm 2's antecedent verbatim.

USEFUL [spec.18.review-edit-sites.1]: the standing-context UNVERIFIED asking whether `gatewaylink.go:71` and `podspec.go:168,:591,:906` are missing carrier-table rows is now CONFIRMED for all four, with `podspec.go:687` confirmed a non-carrier exactly as that entry predicted. It saved the whole sweep.

WATCHOUT: the standing-context rule "a newly found carrier is added as ONE ROW and NOWHERE ELSE" (review-log.md standing context) governs where the DISPOSITION is stated. It does not exempt the three file-list enumerations (CODE-10's heading, summary.md:1071, the files-touched entry at non-spec-changes.md:4039-4046) or checklist S24's category enumeration, which are lists of files and categories and go stale if a site is added and they are not. review-log.md:1104 records the precedent that all of them were updated together when CODE-10 was created.

### [non-spec-recheck.3.review-applicability.1]

FACT: the round-3 delta is exactly four hunks: checklist S24 (new), the whole CODE-10 block
(non-spec-changes.md:2113-2178), the SPEC-3 carrier table's disposition column
(spec-changes.md:607-618), the summary roster bullet (summary.md:1071), the `## Testing`
"For CODE-10, no file" line (:3704) and the files-touched bullet (:4036-4046), plus the
deletion of the `spec/05...:455` parenthetical from CODE-1's `answerShutdown` comment
(:321-327). Nothing else moved. — EVIDENCE: diff against
scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2-prefix

FACT: the two carrier rows now reading "Staged by checklist S4's tier-11 sweep" ARE reachable
by S4's sweep: S4's scope is `grep -rn sessions_served tests/`, and both
tests/tier11_docs/spec_28_register_writers_test.go:99 and
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170 contain
`sessions_served`. The write/read split also matches (:99 pins the §12.6 write clause → re-key;
:170-174 pins the read clause → delete). This cell pair is sound; do not re-check it.
— EVIDENCE: implementation-checklist.md:23; the two test files

FACT: every verbatim quote CODE-10 makes of a tree comment checks out byte for byte: the
addressing test header (session_scrub_report_addressing_doc_reconciliation_test.go:6-8), the
`, and the adapter reports its outcome to the gateway` clause
(basic_level_echo_stamp_doc_reconciliation_test.go:458), and `via ReportSessionScrub on every
session release` at concurrent_delegation_proxy_test.go:49 and :122. The
`RecordSessionScrub` inline comment the carrier table names exists at scrubreport_server.go:472.

FACT: a whole-tree sweep for the withdrawn universal
(`grep -rn "every session release\|each session release\|on every release"` over pkg/ cmd/
tests/ schemas/ migrations/ docs/ spec/) returns no carrier the SPEC-3 table omits. The near
misses are all non-carriers: pkg/adapter/session_test.go:467 (a second teardown, not a report),
tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198 (the Shutdown RPC),
cmd/lenny-gateway/scrub_report_wiring_test.go:137 (scenario narration over the call below it),
docs/operator-guide/multi-tenancy.md:72 (cleanup only). Do not re-run this sweep.

WATCHOUT: scrubreport_server.go:66-67 is a THIRD structural case beside the exception CODE-10
names. `The adapter sends it on every session release.` is a standalone sentence, so arm 1's
"delete the trigger clause and leave the rest of the sentence standing" leaves `The adapter
sends it.` Filed this round. — EVIDENCE: pkg/gateway/.../scrubreport_server.go:66-67

WATCHOUT: after SPEC-3, §12.6 states NO evaluation point (spec-changes.md:838). Two comments
CODE-10 re-keys gloss §12 as the home of that evaluation
(scrubreport_server.go:217, scrubreport_server_test.go:460); the arm changes only the trigger
wording, so the misattribution survives the reduction. Filed this round. The correct reduction
is the one S4 applies in tests/: drop the read-clause gloss, or point it at §5.2's
`**Session count limit:**` bullet.

CORRECTS [non-spec-recheck.2.fix-design-G2.1]: that entry's watchout lists THREE served-count
carriers taking the re-key arm (scrubreport_server.go:195-196, :472, agentpodstate.go:60) and so
left `agentpodstate.go:124-131` (`IncrementSessionsServed`, "The gateway calls this at each
session release") under the delete arm. It states the same fact as its neighbour at :60, so the
two sentences in one file take opposite treatments. Filed this round.

OPEN: the carrier table row at spec-changes.md:617 names "the `SessionsServed` field comment",
but the SessionsServed field's own comment (agentpodstate.go:67-68) carries no trigger; the
carrier is the `RecycleCounters` type comment at :57-64, which CODE-10's body names correctly.
S24 scopes itself by that table, so the cell matters. Filed this round.

### [non-spec-recheck.3.review-citations.1]

FACT: the delta this round is exactly one new deliverable, CODE-10 (non-spec-changes.md:2113-2179), plus its checklist step S24, its summary bullet, its "Files touched" entry and the ten SPEC-3 carrier-table rows re-pointed from "Deferred to the non-spec loop" to "Mirrored by CODE-10" / "Staged by checklist S4's tier-11 sweep" — EVIDENCE: spec-changes.md:604-619; implementation-checklist.md:63-64
FACT: the CODE-10 block in the tree is byte-identical to the r3 snapshot, so `diff` against `non-spec-recheck-r3` shows nothing; the only usable baseline is `non-spec-recheck-r2-prefix` (14:31) — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r3 (14:55, taken at this round's start)
FACT: CODE-10's per-site citations verify. The addressing-test header sentence is verbatim at tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8; the `, and the adapter reports its outcome to the gateway` clause is verbatim in the diagnosis comment at tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-458 AND in both DOCS-4 pages (docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33); the tier-4 gloss `via ReportSessionScrub on every session release` is at tests/tier4_integration/concurrent_delegation_proxy_test.go:49 and :122; the re-key words "on each cleanup-outcome report" are SPEC-3's §12.6 replacement words (spec-changes.md:802,829).
FACT: the two rows re-pointed to "checklist S4's tier-11 sweep" are genuinely reached by S4's `grep -rn sessions_served tests/` — tests/tier11_docs/spec_28_register_writers_test.go:99 (write clause, re-key) and tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-176 (read/evaluation clause, deletion), which is what S4's sentence prescribes — EVIDENCE: implementation-checklist.md:23
FACT: no gate holds migration comment text. tests/tier2_component/migrations/prod_columns_test.go:487-490 asserts 0167's COLUMNS only, and no test reads a migration's comment, so CODE-10's "edited in place, no checksum gate" claim survives a search.
FACT: the shipped tier-11 addressing gate asserts only the substring "addressed by the identifier of the released session and names no slot" plus its opener; both staged rows (spec-changes.md:776, non-spec-changes.md:2743) keep it verbatim on one line, so CODE-10's "keep every substring and every check" claim holds — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33
USEFUL [spec.18.review-edit-sites.1]: the standing UNVERIFIED asking whether pkg/adapter/gatewaylink.go:71 and pkg/controller/sandbox/podspec/podspec.go:168,:591,:906 are missing carrier rows is TRUE and is now filed. gatewaylink.go:70-72 says the adapter "emits on every slot release"; the three podspec comments say the adapter "reports each per-slot cleanup outcome via ReportSessionScrub". podspec.go:686-689 (embedded) is NOT a carrier, as that entry said.
WATCHOUT: CODE-10's two-arm rule does not mechanically produce the right edit at two of its sites, and only the per-site bullets do. Applying arm 1 literally to the basic_level_echo diagnosis comment would delete "at each session release" and LEAVE "the adapter reports its outcome to the gateway", i.e. the universal itself; the bullet instead deletes the reporting clause. Trust the bullets over the arms — EVIDENCE: non-spec-changes.md:2120-2127 vs :2168-2173
OPEN: CODE-10 edits tests/tier4_integration/concurrent_delegation_proxy_test.go but checklist S24 lists tiers 0 and 11 only. Comment-only, so nothing can break there; not filed, but a later round may want S24 to name tier 4 for symmetry with why it names tier 11.

### [non-spec-recheck.3.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the docs-alignment lens on the round-3 delta — BECAUSE the delta (new CODE-10 deliverable, the twelve SPEC-3 carrier-table rows flipping from "Deferred to the non-spec loop" to "Mirrored by CODE-10" / "Staged by checklist S4's tier-11 sweep", checklist S24, the summary CODE-10 bullet, and the line-citation deletion in CODE-1's `answerShutdown` comment) touches only Go, SQL and test COMMENTS; every docs/ carrier of the withdrawn reporting universal was already staged and I re-verified each against the tree — ALTERNATIVES: I worked up and then dropped a candidate on `docs/reference/state-machines.md:248` (see FACT below).

FACT: the complete docs/ carrier set for "the adapter reports the per-slot cleanup outcome" is four sites, and all four are staged. `docs/operator-guide/security-principles.md:33` and `docs/reference/execution-modes.md:68` (identical trailing clause `, and the adapter reports its outcome to the gateway`) → DOCS-4; `docs/reference/adapter-contract.md:75` (Shutdown row) and `:81` (ReportSessionScrub row) → DOCS-2. `docs/operator-guide/multi-tenancy.md:72` carries the same sentence WITHOUT the clause and `docs/runtime-author-guide/lifecycle.md:390` is the whole-pod scrub (`ReportPodScrub`), so neither is a carrier — EVIDENCE: docs/operator-guide/multi-tenancy.md:72, docs/runtime-author-guide/lifecycle.md:390.

FACT: the candidate I dropped, and why, so nobody re-derives it. `docs/reference/state-machines.md:248` reads "Served-session count reaches `recycle.maxSessionsPerPod` on a session release … The gateway stamps the drain request per release", which looks like an arm-2 (served-count-per-release) carrier that no deliverable stages. It is NOT a defect: SPEC-3 leaves §5.2's `**Session count limit:**` bullet unedited and makes §12.6's read clause cite it, and that bullet itself says the pod "transitions to `draining` on the session release that drives the served-session count to `maxSessionsPerPod`" — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488. The docs page mirrors the post-change spec home verbatim in substance, so a finding here would be asking the doc to diverge from its own home. Same reasoning clears `docs/reference/metrics.md:163` ("at the per-release `maxSessionsPerPod` drain") and `docs/reference/configuration.md:91` / `docs/reference/execution-modes.md:23` ("counts every session served").

FACT: CODE-10's claim that its two tier-11 files "keep every substring and every check they hold today" holds. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts only `sessionScrubAddressingRule` plus the shared opener `The request is session-scoped: it is …` against the §4.7 row and the adapter-contract row, and BOTH staged replacements keep that sentence verbatim — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:31,66-74 vs spec-changes.md:776 and non-spec-changes.md:2743. `TestPerSlotCleanupStatedOnEverySessionModeRow` asserts only the substring `Per-slot cleanup` inside the residual-state tables of execution-modes.md and multi-tenancy.md, neither of which DOCS-4 edits — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:436-476.

FACT: CODE-10's two new repository claims check out. No checksum gate exists over `migrations/` (only `0123_ops_backups.up.sql` and `0136_ops_restore_test_results.up.sql` even contain the word, as DDL), so a comment-only in-place edit of `migrations/0167_…up.sql` is safe; and no test under tests/tier11_docs or tests/tier0_static pins that migration's comment against §12.6 — EVIDENCE: migrations/0167_runtime_definitions_execution_mode_service.up.sql:101-110.

FACT: the two carrier rows re-assigned to "checklist S4's tier-11 sweep" are genuinely reachable by that sweep. S4 states the step runs `grep -rn sessions_served tests/`, and both `tests/tier11_docs/spec_28_register_writers_test.go:99` (`podStateGatewayWrittenSentence`) and `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170` (the spec/12 substring block) are in that grep's output — EVIDENCE: implementation-checklist.md:23.

UNVERIFIED: CODE-10 assigns the DELETE arm to the `IncrementSessionsServed` doc comment (`pkg/agentpodstate/agentpodstate.go:128`, "The gateway calls this at each session release on the ReportSessionScrub RPC"), while its own two-arm rule puts a comment stating the served-count advances per release under RE-KEY, and the deliverable's own sentence "the same fact the `agentpodstate.go` and `scrubreporter_seams.go` comments state, so all four take the same arm" reads as putting agentpodstate under re-key. This is the same class as the already-fixed "CODE-10 assigns the delete arm to three comments its own two-arm rule puts under re-key". It is outside the docs lens, so I did not file it; a code or mechanism lens should settle it — EVIDENCE: non-spec-changes.md:2147-2149 against pkg/agentpodstate/agentpodstate.go:124-131.

UNVERIFIED: the SPEC-3 carrier table row names "`pkg/agentpodstate/agentpodstate.go`, the `SessionsServed` field comment", but the `SessionsServed` FIELD comment (agentpodstate.go:67-69) carries no release trigger; the trigger lives in the `RecycleCounters` STRUCT comment (agentpodstate.go:57-65), which is what CODE-10 correctly names. An edit-sites or citations lens should decide whether the table row's wording is a false attribution — EVIDENCE: spec-changes.md:618 against pkg/agentpodstate/agentpodstate.go:57-69.

### [non-spec-recheck.3.review-edit-sites.1]

DECISION: filed three findings, all on the round-2 fix stage (CODE-10 and the SPEC-3 carrier table) — BECAUSE the whole diff since the r2-prefix snapshot is the new CODE-10 deliverable, the twelve carrier-table rows re-dispositioned off "Deferred to the non-spec loop", the S24 checklist line and the summary index bullet; everything else is byte-identical — ALTERNATIVES: filing the "all four take the same arm" arithmetic at non-spec-changes.md:2143-2145 (three test comments + two named comments = five), rejected as the same bookkeeping-count class the SCHEMA-1 lead-in count was refuted under.

FACT: the withdrawn-universal carrier sweep over the whole tree is now complete and cheap to redo: `grep -rn -i "every session release\|each session release\|every release" --include=*.go --include=*.md --include=*.proto --include=*.sql spec/ docs/ schemas/ charts/ pkg/ tests/ migrations/ cmd/ sdks/` plus `grep -rn "ReportSessionScrub" --include=*.go | grep "//"`. Everything it returns is either in the carrier table, declared a non-carrier by the table's lead-in, or one of the two omissions I filed (pkg/adapter/gatewaylink.go:70-75, pkg/controller/sandbox/podspec/podspec.go:168,592,687,907). EVIDENCE: pkg/adapter/gatewaylink.go:70-75; pkg/controller/sandbox/podspec/podspec.go:166-169.

FACT: these are NOT carriers, and a later round should not re-file them: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198 ("the single end-of-session teardown the gateway calls on every release" — Shutdown, not the report); pkg/adapter/session_test.go:467 (the §11.4 revoke); pkg/controller/sandbox/podspec/podspec_test.go:325 ("pod identity for every ReportSessionScrub" — true of every call that is made); the per-release vocabulary throughout pkg/gateway/session/recycle/scrubreporter_seams.go outside the `sessionCountRetirer` comment, because spec/05:488 `**Session count limit:**` is not edited by SPEC-3 and still keys the drain on "the session release that drives the served-session count to maxSessionsPerPod". EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488.

FACT: the tier-11 addressing gate survives SPEC-3 and DOCS-2 untouched. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the literal "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." in BOTH the spec §4.7 row and the adapter-contract row, and both staged replacements keep it verbatim. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:64-73; spec-changes.md:776; non-spec-changes.md:2743.

FACT: `grep -rn sessions_served tests/` does reach both files the carrier table now hands to checklist S4's tier-11 sweep, but for concurrent_slot_lifecycle_doc_reconciliation_test.go only through the comment at :170; the two assertion strings at :173-174 do not contain the token. An implementor grepping for the assertion text alone will miss it. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-175; tests/tier11_docs/spec_28_register_writers_test.go:99-101.

FACT: migrations carry no checksum gate in this tree, so CODE-10's in-place edit of 0167's column comment is safe; the 0167 test asserts DDL substrings only, none of them inside the comment. EVIDENCE: migrations/runtime_definitions_execution_mode_service_test.go:54-60,:103-107; migrations/0167_runtime_definitions_execution_mode_service.up.sql:101-110.

USEFUL [non-spec-recheck-2.2.fix.1]: that entry's record of which sites the round-2 fixer moved from the delete arm to the re-key arm is what let me see that `IncrementSessionsServed` was left behind on the delete arm; without it I would have read the residue as deliberate.

### [non-spec-recheck.3.review-fresh.1]

DECISION: filed ONE finding — CODE-10's `IncrementSessionsServed` bullet takes the delete arm while the deliverable's own two-arm rule and its own reasoning sentence put every served-count-trigger comment under re-key — BECAUSE it is the same defect class an earlier round confirmed and fixed for three sites, and this fourth site in the same file was missed, leaving the two `agentpodstate.go` comments on opposite arms. ALTERNATIVES: filing the stale `§12 (sessions_served evaluated per release …)` glosses (rejected, see OPEN below); filing tier 2 as an omitted tier for the `migrations/0167` edit (rejected: a comment-only SQL edit changes no DDL and tier 2 would pass trivially, even though `tests/change-graph.json` maps `migrations/` to `component`).

FACT: the delta this round is exactly four files — checklist S24, non-spec-changes (CODE-1's line-citation parenthetical deleted at ~:321, the whole new CODE-10 deliverable at :2113-2178, one `## Testing` line, one Files-touched bullet), spec-changes (carrier-table disposition cells only, :607-618), summary (:1071). No staged spec fence moved.

FACT: every claim CODE-10 makes about the tree checks out. The ten carriers exist at `pkg/adapter/sessionscrubreporter.go:13`, `pkg/adapter/gatewaycontrol/scrubreport.go:72`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472`, `scrubreport_server_test.go:422,:444,:455`, `pkg/gateway/session/recycle/scrubreporter_seams.go:245`, `pkg/agentpodstate/agentpodstate.go:60,:128`, `migrations/0167_...up.sql:102`, `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6`, `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:456-458`, `tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122`.

FACT: the two-arm reduction breaks no assertion. `TestPerSlotCleanupStatedOnEverySessionModeRow` only requires the literal `Per-slot cleanup` inside each residual-state row (basic_level_echo_stamp…:470-474), so DOCS-4's sentence edit after the table cannot reach it; `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` pins only `The request is session-scoped: it is addressed by the identifier of the released session and names no slot.` (…addressing…_test.go:32,:65), which no staged edit touches. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:470-474; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,:64-73

FACT: the tier-4 file's comment reduction is safe because every release that file asserts a report for goes through `shutdownSlotCleanly` (a `Shutdown` on a slot that reached running), so the body assertions at :305-332 stay true under SPEC-3's biconditional. — EVIDENCE: tests/tier4_integration/concurrent_delegation_proxy_test.go:308-332,:419-431

FACT: `migrations/0167` comment-in-place editing has an in-tree precedent and an in-tree gate that expects it: `migrations/runtime_definitions_execution_mode_service_test.go:123-177` re-keys 0033/0084 `--` source comments in place and asserts 0167 adds no `COMMENT ON`. Nothing there pins the `sessions_served` comment text. — EVIDENCE: migrations/runtime_definitions_execution_mode_service_test.go:91-95,:123-177

FACT: S4's sweep `grep -rn sessions_served tests/` does reach both rows the carrier table reassigns to it — `tests/tier11_docs/spec_28_register_writers_test.go:99-101` (byte-exact write sentence, re-key) and `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-174` (read-clause substrings, deletion). It does NOT reach `pkg/gateway/.../scrubreport_server_test.go`, which lives under `pkg/`; CODE-10 covers that one. — EVIDENCE: implementation-checklist.md S4 last sentence; tests/tier11_docs/spec_28_register_writers_test.go:99; tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170

OPEN: after S4, §12.6 states NO evaluation point (its read clause becomes a citation of §5.2's `**Session count limit:**` bullet, spec-changes.md:808-812). Two live `// spec:` glosses still attribute the evaluation point to §12: `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:216-217` (`§12 (sessions_served evaluated per release on a concurrent pool)`) and `pkg/gateway/.../scrubreport_server_test.go:460` (`12 (sessions_served evaluated per release)`). CODE-10 re-keys the prose of the first comment but says every existing citation is kept verbatim, so both glosses land stale. Not filed: it is a gloss, the section numbers do not move so `spec-map.json` is unaffected, and `[non-spec-recheck.2.fix-G2.1]` records the per-release vocabulary as deliberately out of CODE-10's scope. A later round or the implementor should decide.

USEFUL [non-spec-recheck.2.fix-design-G2.1]: its WATCHOUT naming the three served-count carriers that must take the re-key arm is exactly what made the fourth (`IncrementSessionsServed`) visible as a miss rather than as a deliberate choice.

### [non-spec-recheck.3.review-mechanism.1]

DECISION: filed exactly one finding — CODE-10 assigns the DELETE arm to `agentpodstate.go`'s `IncrementSessionsServed` doc comment while its own two-arm rule and the summary roster bullet put a served-count-per-release statement under RE-KEY — BECAUSE it is the same defect class a prior round verified for three sibling comments, and the fix left this fourth site behind; ALTERNATIVES rejected: filing the "all four take the same arm" miscount in the test-file bullet (3 test comments + agentpodstate + seams is five, but the count is narrative bookkeeping and the refuted SCHEMA-1 count finding settles that class), and filing the two S4-attributed carrier rows as absent from `## Files touched on application (non-spec)` (a checklist step now stages them, which discharges the earlier finding's disjunction).

FACT: every site CODE-10 names exists in the tree with the text CODE-10 quotes. Verified: `pkg/adapter/sessionscrubreporter.go:13`, `pkg/adapter/gatewaycontrol/scrubreport.go:72` (carrier) vs `:12-14` (non-carrier), `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:195-197,:472`, `scrubreport_server_test.go:421-422,:444-447,:453-455`, `pkg/gateway/session/recycle/scrubreporter_seams.go:244`, `pkg/agentpodstate/agentpodstate.go:60,:128`, `migrations/0167_runtime_definitions_execution_mode_service.up.sql:102-103`, `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8,:20-21`, `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-461`, `tests/tier4_integration/concurrent_delegation_proxy_test.go:48-49,:120-122`.

FACT: the two carrier rows re-attributed this round to "checklist S4's tier-11 sweep" are genuinely covered: S4's last sentence is `The step runs grep -rn sessions_served tests/ and re-keys every tier-11 assertion pinning a §12.6 write clause ... and deletes every assertion pinning the read clause` — EVIDENCE: implementation-checklist.md:23; spec-changes.md:608-609.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the literal sentence "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." on ONE line in BOTH the spec §4.7 `ReportSessionScrub` row and the `adapter-contract.md` row. Both staged replacements keep it verbatim, so the gate stays green — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,:65-73; spec-changes.md:776; non-spec-changes.md:2743.

WATCHOUT: do NOT file `docs/reference/state-machines.md:248` ("Served-session count reaches `recycle.maxSessionsPerPod` on a session release") or §5.2's `**Session count limit:**` bullet as carriers the re-key misses. Both read existentially (the release that drives the count), which stays true when only some releases file a report — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488; docs/reference/state-machines.md:248; review-log.md:612.

WATCHOUT: the only "every/each session release" strings left in the tree outside the carrier table are non-carriers or already assigned: `pkg/adapter/server.go:169` (CODE-1), the generated `pkg/proto/adapter/v1/*.pb.go` copies (SCHEMA-1 regeneration), `docs/operator-guide/multi-tenancy.md:72` and `tests/tier4_integration/recycle_scrub_path_test.go:978` (neither states the reporting universal). A whole-tree grep confirms no unassigned carrier — EVIDENCE: `grep -rn "session release" docs/ schemas/ charts/` and the same over pkg/ tests/ migrations/ cmd/.

CORRECTS [non-spec-recheck.2.fix-G2.1]: its WATCHOUT says "CODE-10 therefore stages deletion of the `at every session release` trigger and keeps the reporting clause" for the addressing test. The text that landed does the opposite: it KEEPS the first clause including "at every session release" and REPLACES the reporting clause with the Shutdown/`running` predicate (non-spec-changes.md:2155-2166). The log entry is stale; read the deliverable, not the entry.

### [non-spec-recheck.4.fix-G1.1]

DECISION: `pkg/adapter/gatewaylink_test.go` and `pkg/adapter/sessionscrub_emit_test.go` each join CODE-10 as their OWN SPEC-3 carrier-table row, on the delete arm — BECAUSE one row per file keeps the carrier table diffable against the four file-list enumerations that mirror it — ALTERNATIVES: merging the test into the `gatewaylink.go` row (rejected: breaks the one-row-per-file key the heading, files-touched and summary lists mirror); a new CODE-11 for test carriers (rejected: CODE-10 already holds test-file carriers); re-keying the `sessionscrub_emit_test.go` sentence onto "on each cleanup-outcome report" as the finding suggested (rejected: circular, since that sentence's subject "This" already denotes the report).
FACT: CODE-10's file set is stated in FIVE places, not the four the earlier WATCHOUT names. The fifth is `summary.md`'s Deliverable Index parenthetical, which the round-3 and round-4 fixes each left stale. The five are: the SPEC-3 carrier table (spec-changes.md), the CODE-10 heading, the CODE-10 site bullets, the `## Files touched on application (non-spec)` CODE-10 entry, and the summary Deliverable Index bullet. All five moved in this edit.
FACT: `implementation-checklist.md` S24 states CODE-10's scope by CATEGORY and names no file, so adding a carrier in an existing category (adapter scrub-report surfaces) needs no checklist edit. Only a carrier in a new category does.
WATCHOUT: the delete arm on `pkg/adapter/gatewaylink_test.go` is a quantifier reduction in the `// spec:` comment ("the per-slot cleanup outcome" to "a per-slot cleanup outcome") plus deletion of the `per-session-release` fragment in the `// diagnosis:` comment. Deleting either comment outright would strip an annotation `test-coverage.md` requires — EVIDENCE: pkg/adapter/gatewaylink_test.go:107-116.
UNVERIFIED: two further candidate carriers of the served-count-per-release universal are in no carrier row: `pkg/gateway/session/recycle/scrubreporter_seams.go:389-391` and `scrubreporter_seams_test.go:726-727`. Nobody has checked them against the staged §5.2 rule; a later round should.

### [non-spec-recheck.4.fix-design-G1.1]
DECISION: both G1 carriers (pkg/adapter/gatewaylink_test.go, pkg/adapter/sessionscrub_emit_test.go) are added as ONE new SPEC-3 carrier-table row EACH (not merged into the existing gatewaylink.go row), plus the CODE-10 heading, one CODE-10 site bullet each, the files-touched CODE-10 entry, and summary.md:1071 — five enumerations, one pass — BECAUSE every other table row is keyed on one file and the heading/files-touched/summary lists are per-file, so 1:1 row-to-file correspondence is what makes the five enumerations mechanically checkable against each other. ALTERNATIVES: finding 0's merged "gatewaylink.go and gatewaylink_test.go" row (breaks the 1:1 correspondence the other four lists rely on); a new deliverable for test-comment carriers (a second mechanism for one reduction); collapsing the five enumerations to one home per STATE-EACH-RULE-ONCE (right in principle, but the heading format and the files-touched section are proposal-wide conventions and a G1-local exception would itself be hair).
DECISION: sessionscrub_emit_test.go takes CODE-10's DELETE arm, not the re-key arm finding 1 suggested — BECAUSE the sentence's subject is the report itself ("This advances sessions_served (feeding maxSessionsPerPod) on every clean release", sessionscrub_emit_test.go:92-93), so re-keying to "on each cleanup-outcome report" is circular; deleting the "on every clean release" tail leaves the true sentence. The arm is chosen by the sentence's subject: subject = the report → delete the trigger; subject = the count advancing across releases → re-key.
FACT: gatewaylink_test.go has four ConnectGateway tests and only TestConnectGatewayWithAddrWiresSessionScrubReporter_spec_5_2 carries the universal, in its `// spec:` (:109-110) and `// diagnosis:` (:113-114) comments. TestConnectGatewayNoAddrLeavesSessionScrubReporterNil_spec_5_2 and the two §9.1 tests carry none, and the in-body comment at :129-131 states only that the seams share one client. EVIDENCE: pkg/adapter/gatewaylink_test.go:11-38,105-190
WATCHOUT: summary.md:1071 (Deliverable Index CODE-10 parenthetical) is the fifth enumeration and is the one the last two rounds' fixes forgot; implementation-checklist.md:63 (S24) names CODE-10's scope by category, not by file, so it stays true and must NOT be edited. EVIDENCE: proposals/.../summary.md:1071, .../implementation-checklist.md:63
UNVERIFIED: a grep sweep of the tree for the reporting/served-count universal turns up two further candidate carriers that are in no table row: pkg/gateway/session/recycle/scrubreporter_seams.go:389-391 (in-body comment, "the count ... advances by one per release"; the table row names only the sessionCountRetirer type comment) and pkg/gateway/session/recycle/scrubreporter_seams_test.go:726-727 ("a concurrent pool over maxSessionsPerPod is never drained per release"). Both are served-count-per-release statements of the same class as the listed agentpodstate.go carrier. Not filed here and not in G1's scope; a later round should verify them against the per-release drain tree fact before deciding. Not carriers, checked and cleared: pkg/adapter/session_test.go:467, tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198 (both about teardown, not the report).
MISTAKE: rounds 3 and 4 each appended carriers one at a time after a hand search, and each round's addition was the next round's finding. The durable lesson is that the carrier set must be closed by a grep sweep over the tree at application time rather than by a reviewer noticing the next file; the sweep predicate is the two comment forms CODE-10's two arms already name.

### [non-spec-recheck.4.review-applicability.1]

DECISION: filed exactly one finding this round, the `pkg/adapter/gatewaylink_test.go:113` carrier omitted by the same fix that added `pkg/adapter/gatewaylink.go` — BECAUSE the fix round's own bullet (non-spec-changes.md:2141-2145) names `per-session-release` as one of the two fragments carrying the withdrawn universal, and the identical compound stands in the paired test file's `// diagnosis:` comment, in no carrier-table row, no deliverable and no files-touched entry, while the table declares itself the single home of every carrier's disposition (spec-changes.md:586-587) — ALTERNATIVES: also filing `tests/tier4_integration/concurrent_delegation_proxy_test.go:91,:138` ("one independent per-slot cleanup report per release") — rejected: those two describe what THIS fixture asserts about its own releases, which stay true, unlike the two `// spec:` glosses at `:48-49` and `:120-122` that state the rule and are already staged.

FACT: the round-4 delta is small and fully enumerated by the diff: CODE-10 gains `pkg/adapter/gatewaylink.go` and `pkg/controller/sandbox/podspec/podspec.go` (heading, site bullets, files-touched, two new SPEC-3 carrier rows, summary deliverable index, checklist S24), `agentpodstate.go`'s `IncrementSessionsServed` moves from the delete arm to the re-key arm, a §12-gloss re-key sentence joins the lead-in, and the 0079 impact row's ground drops to file granularity. EVIDENCE: non-spec-changes.md:2113,2128-2132,2141-2145,2158-2168,4058,4062; spec-changes.md:617-619; summary.md:1047,1071; implementation-checklist.md:63

FACT: every other delta claim verified against the tree and holds. `gatewaylink.go:70-77` does carry the universal in two fragments and its `PodScrubReporter` neighbour (`:64-69`) does not; `podspec.go:167-169`, `:591-596`, `:905-907` each say "reports each per-slot cleanup outcome" and `:686-692` (embedded runtime) does not; `agentpodstate.go:60` and `:128` both key the advance on the session release and both keep a §5.2 evaluation clause; the two §12 evaluation glosses (`scrubreport_server.go:214-217`, `scrubreport_server_test.go:460`) each already cite §5.2, so the lead-in's "which each such annotation already cites" is true; 0079 does stage `controller.go` (CODE-1) and `controller_test.go` (TEST-3). EVIDENCE: pkg/adapter/gatewaylink.go:64-77; pkg/controller/sandbox/podspec/podspec.go:166-175,591-597,904-913; pkg/agentpodstate/agentpodstate.go:57-69,124-132; proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:553,566

USEFUL [round that filed the gatewaylink/podspec omissions]: its UNVERIFIED asking a later round to grep `per-session-release` tree-wide is what found this round's finding; the grep returns exactly two tracked Go sites, `gatewaylink.go:71` (now staged) and `gatewaylink_test.go:113` (not).

WATCHOUT: a carrier sweep that greps only non-test Go files misses paired `_test.go` doc and `// diagnosis:` comments that repeat the production comment's wording. `gatewaylink.go` and `gatewaylink_test.go` are the worked example. EVIDENCE: pkg/adapter/gatewaylink_test.go:106-116

FACT: these remain non-carriers and should not be re-filed: `pkg/adapter/session_test.go:460-467` (a second teardown's refusal, not the report), `pkg/adapter/server.go:168-169` (covered by the CODE-1 carrier row), `pkg/adapter/gatewaycontrol/scrubreport.go:13` (the `SessionScrubOutcome` comment the table's lead-in declares a non-carrier), `scrubreporter_seams.go:390` and `scrubreporter_seams_test.go:727` (the per-release drain vocabulary spec/05:488 still licenses). EVIDENCE: spec-changes.md:587-591

### [non-spec-recheck.4.review-citations.1]
FACT: the round-4 delta is four hunks only — CODE-10 gains `pkg/adapter/gatewaylink.go` and `pkg/controller/sandbox/podspec/podspec.go` (heading, bullets, files-touched list, checklist S24, summary CODE-10 bullet), the SPEC-3 carrier table gains two rows and re-words the agentpodstate row, the §12-gloss re-key paragraph is added, and the 0079 impact row is rewritten. EVIDENCE: non-spec-changes.md:2113,2128-2134,2141-2145,2163-2173; spec-changes.md:617-619; summary.md:1047,1071
FACT: every tree claim the new text makes checks out verbatim — gatewaylink.go:70-77 carries `per-session-release` and `on every slot release` with the `PodScrubReporter` comment above at :64-69; podspec.go:167-168, :591, :905-906 each say "reports each per-slot cleanup outcome" and each carries `spec: §4.7, §5.2`; the embedded-runtime comment at :686-692 carries no quantifier; agentpodstate.go:60 and :128 both key on "at each session release"; both §12 glosses (scrubreport_server.go:214-217, scrubreport_server_test.go:460) already cite §5.2. EVIDENCE: pkg/adapter/gatewaylink.go:64-77; pkg/controller/sandbox/podspec/podspec.go:166-175,591-596,904-913,686-692
WATCHOUT: the sibling test file `pkg/adapter/gatewaylink_test.go:113` carries the SAME `per-session-release` fragment the new gatewaylink.go bullet calls a carrier, and it is in no carrier-table row and no deliverable. Filed this round. A tree grep for the three carrier phrases now returns nothing else outside the table. EVIDENCE: pkg/adapter/gatewaylink_test.go:113
UNVERIFIED closed: the standing question at review-log.md:1417 ("grep the tree for `on every session release`, `each per-slot cleanup outcome` and `per-session-release` before declaring the table closed") was run this round over `--include=*.go --include=*.sql --include=*.md --include=*.proto --include=*.json`, excluding proposals/. Every hit maps to a table row or to the table's stated non-carrier list except `pkg/adapter/gatewaylink_test.go:113`.
FACT: 0079 stages `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go` and `pkg/gateway/session/recycle/scrubreporter_seams.go` under its CODE-3, and CODE-10 here opens both; the 0079 impact row names only five shared SPEC files and its guidance says to merge those five. EVIDENCE: proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:429,556-557; summary.md:1047,1071
DECISION: did NOT file the podspec "delete the quantifier" arm mismatch (the three comments instance no session-release trigger clause, so the first arm's own definition does not fit them, and removing `each` leaves a bare generic) — BECAUSE the already-refuted "The adapter sends it." entry settles that a generic naming of what the RPC carries is not the withdrawn universal, and the bullet is unambiguous about what to delete. ALTERNATIVES: filing it as a second exception to arm 1 (rejected: wording).

### [non-spec-recheck.4.review-edit-sites.1]
DECISION: filed exactly one finding, a 13th unlisted carrier of the withdrawn reporting universal at pkg/adapter/sessionscrub_emit_test.go:92-93 — BECAUSE the SPEC-3 carrier table declares itself the single home of every carrier's disposition and that comment ("This advances sessions_served (feeding maxSessionsPerPod) on every clean release") is the same proposition as the listed agentpodstate.go/scrubreporter_seams.go re-key sites — ALTERNATIVES: rejected reporting the 0079 impact row for omitting the two shared CODE-3 code files (pkg/gateway/session/recycle/scrubreporter_seams.go, .../leasecontrol/scrubreport_server.go), because the row's stated claims stay true and the remedy would add text; rejected an S-4 (podspec-touching) note on the remediation row for the same reason.
FACT: the exhaustive carrier sweep is `grep -rn -i "every session release|each session release|every slot release|every clean release|every release|per session release" --include=*.go --include=*.sql --include=*.proto` filtered to comment lines. Every hit resolves to a listed carrier, a declared non-carrier, or an unrelated "reservation release"/"lease release" sense, except the one filed. — EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:92
FACT: the remediation plan's S-2 "two renamed channel files" are pkg/adapter/adapterevents.go and pkg/adapter/runtimeops.go (rename commit b97d15b23, from controlchannel.go and lifecyclechannel.go). pkg/adapter/gatewaylink.go is NOT an S-2 covered file, so CODE-10 adding it does not falsify the remediation row's "three of S-2's covered handler files". — EVIDENCE: gateway-runtime-comms-remediation.md:1886-1887
FACT: 0079 stages pkg/controller/sandbox/controller.go (CODE-1) and controller_test.go (TEST-3) and opens podspec.go nowhere; its files list confirms. The amended 0079 impact row's new clause checks out. — EVIDENCE: proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:554,566
FACT: every podspec.go site CODE-10 names exists and carries `spec: §4.7, §5.2` as the bullet claims (PodNameEnvVar :165-174, sidecar container :591-596, podNameEnv :904-914), and the embedded-runtime comment :686-692 genuinely states no per-release universal. — EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:686
WATCHOUT: podspec_test.go:325 and sessionscrubreporter_internal_test.go:11-15 look like carriers but quantify over *reports* ("for every ReportSessionScrub and ReportPodScrub"), not over releases, so they stay true and are not carriers. Do not file them. — EVIDENCE: pkg/controller/sandbox/podspec/podspec_test.go:325

### [non-spec-recheck.4.review-fresh.1]
FACT: the r3→r4 delta is four files and small: CODE-10 gains `pkg/adapter/gatewaylink.go` and `pkg/controller/sandbox/podspec/podspec.go`, `IncrementSessionsServed` moves from the delete arm to the re-key arm, a new lead-in paragraph re-keys the `§12` per-release-evaluation glosses, and the 0079 impact row is corrected to admit `podspec/podspec.go`. — EVIDENCE: proposals/.../0081...non-spec-changes.md:2113,2128-2132,2141-2145,2158-2168; .../summary.md:1047
FACT: every delta claim about the tree checks out. gatewaylink.go carries the universal in exactly the two fragments named (`per-session-release`, `on every slot release`) — EVIDENCE: pkg/adapter/gatewaylink.go:70-77. The three podspec comments each state "reports each per-slot cleanup outcome" and each carries `spec: §4.7, §5.2` — EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:166-175,591-596,904-913. Both agentpodstate comments state the per-release trigger — EVIDENCE: pkg/agentpodstate/agentpodstate.go:57-65,124-132. 0079's CODE-1/TEST-3 do stage `pkg/controller/sandbox/controller.go` and `controller_test.go` — EVIDENCE: proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:554,566.
FACT: the two `§12` glosses the new paragraph covers are `scrubreport_server.go:216-217` ("sessions_served evaluated per release on a concurrent pool") and `scrubreport_server_test.go:460` ("12 (sessions_served evaluated per release)"); both already cite §5.2, so the paragraph's "which each such annotation already cites" holds. The third `§12` gloss, `scrubreport_server.go:455-456` ("RETURNING sessions_served"), is a write gloss and needs nothing. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:214-217,455-456
FACT: the `RecordSessionScrub` inline comment the carrier table means is the one at scrubreport_server.go:472 ("evaluate the served-session count on every release"), not the post-increment capture comment at :452-456. A fixer re-keying the wrong one will leave the carrier standing. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:472-477
WATCHOUT: the same universal-sweep that produced the r3→r4 additions still misses `pkg/adapter/gatewaylink_test.go:113`, the `// diagnosis:` comment on the test of the very wiring comment the delta now edits. It spells the withdrawn universal with the identical fragment (`per-session-release ReportSessionScrub`) that CODE-10 deletes from gatewaylink.go:71. — EVIDENCE: pkg/adapter/gatewaylink_test.go:113-116
FACT: non-carriers verified so they are not re-filed. `pkg/controller/sandbox/podspec/podspec_test.go:325` ("pod identity for every ReportSessionScrub") quantifies over reports, not releases, and stays true. `pkg/adapter/session_test.go:467` and `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198` say "on every release" about revoke/teardown, not about reporting. `scrubreport_server.go:37-45` (`RecordSessionScrub` interface method) says "at a session release", indefinite, and is not a universal.
FACT: the shipped adapter already calls `reportSessionScrub` from `Shutdown` alone (pkg/adapter/session.go:279 is the only call site), so SPEC-3's narrowing for non-`Shutdown` releases matches the tree and `pkg/adapter/sessionscrub_emit_test.go` needs no edit for that half. — EVIDENCE: pkg/adapter/sessionscrubreporter.go:61, pkg/adapter/session.go:273-279

### [non-spec-recheck.4.review-mechanism.1]

FACT: the round-4 delta is small and entirely CODE-10: `pkg/adapter/gatewaylink.go` and
`pkg/controller/sandbox/podspec/podspec.go` added as carriers, `IncrementSessionsServed` moved
from delete to re-key, a new paragraph on the §12 gloss, plus the matching SPEC-3 carrier rows,
the S24 checklist line, the summary CODE-10 line and the 0079 impact row.
EVIDENCE: proposals/.../non-spec-changes.md:2128-2168, spec-changes.md:617-619,
implementation-checklist.md:63, summary.md:1047,1071

FACT: every tree claim the delta makes checks out. The three podspec comments do say "reports
each per-slot cleanup outcome" and each carries `spec: §4.7, §5.2`
(pkg/controller/sandbox/podspec/podspec.go:167-168, :591-592, :905-906); the embedded-runtime
comment at :686-688 carries no quantifier; gatewaylink.go:70-77 carries both fragments and the
`PodScrubReporter` comment above it at :64-69 is about the whole-pod scrub only.

FACT: the 0079 impact row's new clause is true. 0079's CODE-1 stages
`pkg/controller/sandbox/controller.go` and TEST-3 `controller_test.go`
(proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:419,:554,:566),
so `podspec/podspec.go` is the only `pkg/controller/sandbox` file both open and they do not collide.

FACT: adding `pkg/adapter/gatewaylink.go` does NOT widen the remediation's S-2 window claim.
S-2's covered files are `session.go`, `lifecycle.go`, `checkpoint.go`, `coordination.go`,
`credentials.go`, `slotcreds.go`, `attach.go`, `sdkwarm.go` and the two renamed channel files,
which are `adapterevents.go` and `runtimeops.go` (rename commit b97d15b23); `gatewaylink.go` is
none of them, so the summary's "three of S-2's covered handler files" stands.
EVIDENCE: gateway-runtime-comms-remediation.md:1883-1890

FACT: `tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go:145` builds a
`ShutdownRequest` with `SessionId` alone and is NOT enumerated in the files list, but it is
inside the declared `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/` sweep set
(non-spec-changes.md:3176-3182, :4103-4108), so it is staged. Do not file it.

WATCHOUT: `tests/claim-map.json` carries code line surfaces into two files CODE-10 now edits:
`podspec.go:607` (line 45) and `pkg/adapter/gatewaylink.go:36-79` (line 470). CODE-10 asserts
"neither tests/spec-map.json nor tests/claim-map.json takes an edit"
(non-spec-changes.md:2193-2196). That stays true only if the implementer deletes the words in
place without collapsing a comment line. The tier-0 gate only rejects a BARE line surface
(tests/tier0_static/claim_register_test.go:145-157,:278), so nothing catches drift. Not filed as
a finding because the line shift is optional, not forced.

UNVERIFIED: whether an implementer's reflow of the podspec and gatewaylink comments shifts those
two claim-map surfaces. The implementing step should re-check both rows after the edit.

### [non-spec-recheck.5.review-applicability.1]

DECISION: returned an EMPTY findings list for round 5 — BECAUSE the whole delta since r4 is the two
new CODE-10 carrier sites (`pkg/adapter/gatewaylink_test.go`, `pkg/adapter/sessionscrub_emit_test.go`)
plus the arm-2 exception sentence, and each lands in all four places the staging requires: SPEC-3
carrier table (spec-changes.md:618-619), CODE-10 heading (non-spec-changes.md:2113), site bullets
(:2150-2165), files-touched (:4077-4078) and the summary's CODE-10 entry (summary.md:1071).
ALTERNATIVES: rejected filing that the gatewaylink_test.go bullet declares "the same arm" (arm 1 =
delete the trigger clause) while prescribing an article substitution ("the" → "a" per-slot cleanup
outcome); the edit's output is stated verbatim so an implementor invents nothing, and the material
skeptic refuted a neighbouring wording finding on exactly that ground.

FACT: the two new bullets' anchors verify. `pkg/adapter/gatewaylink_test.go:107-116` carries the
`// spec:` ("reports the per-slot cleanup outcome through") and `// diagnosis:`
("the per-session-release ReportSessionScrub") comments; the function is
`TestConnectGatewayWithAddrWiresSessionScrubReporter_spec_5_2` at :117.
`pkg/adapter/sessionscrub_emit_test.go:89-93` carries "This advances sessions_served (feeding
maxSessionsPerPod) on every clean release." on the doc comment of
`TestShutdownSlotEmitsReleasedOnCleanClose_spec_5_2` (:99). The bullet names the function without
its `_spec_5_2` suffix, which matches the comment's own opening word and is unique in the file.
EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:89-99

FACT: a full tree sweep for the withdrawn universal
(`grep -rn "on every session release\|per-session-release\|on every slot release\|at every session
release\|at each session release\|on each session release\|on every release\|per session release"`
over pkg/ tests/ migrations/ docs/ schemas/ cmd/) now returns no carrier that the SPEC-3 table
misses. The four hits outside the table are non-carriers: `pkg/adapter/session_test.go:467`
(revoke/recycle produce a second teardown), `tests/tier3_contract/gatewaycontrol_scrub/
shutdown_recycle_wire_test.go:198` (Shutdown is the teardown the gateway calls, not the report),
`tests/tier4_integration/recycle_scrub_path_test.go:978` (per-release drain), and
`pkg/agentpodstate/agentpodstate.go:128`, which the table already carries under the
`IncrementSessionsServed` row. EVIDENCE: spec-changes.md:591-622

FACT: `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173-174` asserts the
spec/12 substring SPEC-3 removes, and it is NOT an unstaged gate failure: the carrier table files it
under "Staged by checklist S4's tier-11 sweep, deletion" (spec-changes.md:609) and S4's last sentence
runs `grep -rn sessions_served tests/` and deletes every read-clause assertion
(implementation-checklist.md:23). Do not re-file this.
EVIDENCE: proposals/.../implementation-checklist.md:23

FACT: `pkg/adapter/sessionscrub_emit_test.go` builds `&adapterv1.ShutdownRequest{...}` in
`shutdownSlotReq` (:83-87), so after rule 10 it needs `UnconditionalTeardown: true`. That is covered
by the unenumerated mechanical sweep entry ("Every other file that `grep -rn \"ShutdownRequest{\"
--include=*_test.go pkg/ tests/` names"), non-spec-changes.md:4124-4129, so adding the file to
CODE-10 creates no double-listing defect. EVIDENCE: non-spec-changes.md:4124-4129

FACT: adding the two test files falsifies no impact-row ground. The 0078 row's ground is that this
proposal opens neither `pkg/adapter/socketruntime.go` nor `cmd/lenny-adapter/main.go`; the 0071 row's
is `socketruntime.go`, `attach.go`, `heartbeat.go`, `slotframe.go`; the 0079 row's is
`pkg/sandbox/podscrub`, `socketruntime.go` and `pkg/controller/sandbox` (only `podspec/podspec.go`).
No other live proposal names either new file (`grep -rln` over proposals/ returns only 0073, which is
implemented). EVIDENCE: summary.md:1045,1047,1049

FACT: the checklist still carries 24 steps, none ticked, and S24 names the CODE-10 sites by reference
to the SPEC-3 carrier table rather than enumerating files, so adding carriers to the table needs no
checklist edit. EVIDENCE: implementation-checklist.md:63-64

### [non-spec-recheck.5.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens this round — BECAUSE the whole delta since the r4 snapshot is three hunks (two new SPEC-3 carrier rows, two new CODE-10 bullets plus the second-arm exception sentence, the two new files in the CODE-10 heading, the "Files touched" list and the summary's CODE-10 sentence), and every repository claim in them verifies verbatim — ALTERNATIVES: rejected filing the "`// spec:` comment is edited but the `spec: §4.7 …` annotation is untouched" reading as a self-contradiction, because the proposal QUOTES the annotation string it means (the section list), which bounds it; and rejected filing "the quantifier goes" for a definite article that is replaced by **a** rather than deleted, as wording.

FACT: the two newly staged carriers verify exactly. `pkg/adapter/gatewaylink_test.go:107-116` carries "the seam the §5.2 slot-release path reports the per-slot cleanup outcome through" in the `// spec:` block and "the per-session-release ReportSessionScrub" in the `// diagnosis:` block; `pkg/adapter/sessionscrub_emit_test.go:92-93` carries "This advances sessions_served (feeding maxSessionsPerPod) on every clean release." — EVIDENCE: pkg/adapter/gatewaylink_test.go:107,113; pkg/adapter/sessionscrub_emit_test.go:93

FACT: CODE-10's three inventories are now consistent at 14 files each — the heading (non-spec-changes.md:2113), the bullet list (2144-2216), the "Files touched on application" entry (4076-4088) and the SPEC-3 "Mirrored by CODE-10" rows (spec-changes.md:600-622) enumerate the same set with no extra and no missing member. A future fixer adding a carrier must touch all four sites plus summary.md:1071 — EVIDENCE: 0081…non-spec-changes.md:2113; 0081…spec-changes.md:618-619

FACT: the tree-wide sweep for the withdrawn universal (`grep -rn -iE "(on|at) (every|each) (session |slot )?release" --include=*.go`) now returns no Go carrier outside the carrier table. The three near-misses are non-carriers: `pkg/adapter/session_test.go:467` ("the §11.4 full revoke and the occupancy-zero recycle edge both produce on every release" — about a repeated teardown, not a report), `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198` ("the single end-of-session teardown the gateway calls on every release" — about `Shutdown`, not the report), and `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:18` (states only that the cleanup runs, which the proposal already calls out as untouched) — EVIDENCE: pkg/adapter/session_test.go:467; tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198

WATCHOUT: the doc comment in `pkg/adapter/sessionscrub_emit_test.go` opens with the bare name `TestShutdownSlotEmitsReleasedOnCleanClose` while the function is `TestShutdownSlotEmitsReleasedOnCleanClose_spec_5_2`. The proposal's anchor uses the bare name and therefore resolves against the comment text; do not "fix" it to the suffixed name without checking that the anchor is meant to name the comment rather than the function — EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:89 vs :99

UNVERIFIED: `pkg/adapter/sessionscrub_emit_test.go:16-19` says the recording double counts reports "since the withhold and terminate paths emit none", while `TestTerminateShutdownEmitsTheSessionScrub_spec_5_2` at :260 asserts a terminate DOES emit one. This is a pre-existing tree inconsistency, outside this proposal's carrier set and outside this lens' bar; someone auditing the adapter's report paths should settle which "terminate" each sentence means — EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:19,248-260

### [non-spec-recheck.5.review-edit-sites.1]

DECISION: returned an EMPTY findings list — BECAUSE the r4→r5 delta is exactly the three-file G1 fix (two new SPEC-3 carrier rows, two new CODE-10 site bullets, the second-arm exception sentence, the CODE-10 heading, the files-touched entry and summary.md:1071), and all five enumerations now agree 1:1 by file, with every new claim verified in the tree — ALTERNATIVES: filing `tests/tier4_integration/concurrent_delegation_proxy_test.go:91,:138` as unlisted carriers (rejected on the same ground `[non-spec-recheck.4]` gave at review-log.md:1598: both describe what THAT fixture asserts about its own releases, every one of which is a `Shutdown` on a running slot, so they stay true); filing the arm-label divergence on the `gatewaylink_test.go` `// spec:` comment (rejected, below the bar, and its remedy adds a third exception sentence against the caller's reduction directive).

FACT: the delta's two new carriers verify verbatim. `pkg/adapter/gatewaylink_test.go:107-116` carries `the seam the §5.2 slot-release path reports the per-slot cleanup outcome through` (`// spec:`) and `the per-session-release ReportSessionScrub has no GatewayControl link` (`// diagnosis:`), with the `sessions_served` / `maxSessionsPerPod` / leak-ledger tail the bullet says it keeps. `pkg/adapter/sessionscrub_emit_test.go:92-93` carries `This advances sessions_served (feeding maxSessionsPerPod) on every clean release`, whose subject `This` is the emitted report, which is the ground the new second-arm exception states. — EVIDENCE: pkg/adapter/gatewaylink_test.go:107-116, pkg/adapter/sessionscrub_emit_test.go:89-98

FACT: the exhaustive carrier sweep now closes. `grep -rn -iE "every (session|slot|clean) release|each session release|per-session-release|on every release" --include=*.go --include=*.sql --include=*.proto --include=*.md pkg/ cmd/ tests/ docs/ schemas/ charts/ migrations/ spec/` returns, outside the table's rows and its declared non-carrier list, only sites in an unrelated sense: `pkg/adapter/session_test.go:467` (a second teardown produced on every release, not a report), `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:198` (the `Shutdown` RPC the gateway calls on every release), `tests/tier9_security/scaffolds_test.go` (pen-test cadence) and the `pkg/controller/warmpool` per-release drain comments. `docs/operator-guide/multi-tenancy.md:72` states only that the CLEANUP runs, which the table names a non-carrier. — EVIDENCE: schemas/lenny-adapter.proto:309,437,452; docs/operator-guide/multi-tenancy.md:72

WATCHOUT: the proposal anchors the second new bullet on `TestShutdownSlotEmitsReleasedOnCleanClose`, but the tree's function is `TestShutdownSlotEmitsReleasedOnCleanClose_spec_5_2` (`pkg/adapter/sessionscrub_emit_test.go:99`); the unsuffixed spelling is the first word of the doc comment itself (`:89`), so the anchor still resolves uniquely. Do not "fix" it into a second mismatch — the sibling bullet uses the suffixed spelling because `gatewaylink_test.go`'s comment does not repeat the name. — EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:89,99

FACT: `pkg/adapter/sessionscrubreporter.go:46` (`reportSessionScrub` doc comment, "after a session release") and `:23` (the interface method comment) are NOT carriers and need no row: neither quantifies over releases, and `reportSessionScrub` has one caller, the `Shutdown` handler, so "after a session release" stays true under SPEC-3. A future sweep that widens to "after a session release" will re-find them; it has found nothing. — EVIDENCE: pkg/adapter/sessionscrubreporter.go:21-25,46-47; pkg/adapter/session.go:273-279

USEFUL [non-spec-recheck.4 DECISION at review-log.md:1598]: its recorded rejection of `concurrent_delegation_proxy_test.go:91,:138`, with the ground, saved this round from re-filing a refused finding; the two lines are the most carrier-looking strings left in the tree.

### [non-spec-recheck.5.review-fresh.1]
FACT: the r4→r5 delta is three files and four hunks only: CODE-10's heading + two new bullets + the lead-in second-arm exception (non-spec-changes.md:2113,2127-2131,2150-2165), two new SPEC-3 carrier rows (spec-changes.md:618-619), the CODE-10 summary bullet (summary.md:1071) and the files-touched list (non-spec-changes.md:4077-4078). — EVIDENCE: proposals/.../0081...non-spec-changes.md:2113
FACT: both new carrier sites verified in the tree and the staged edits are correct. `pkg/adapter/gatewaylink_test.go:107-116` carries the universal in two fragments ("the seam the §5.2 slot-release path reports the per-slot cleanup outcome through" and "the per-session-release ReportSessionScrub"), mirroring `pkg/adapter/gatewaylink.go:70-76`. `pkg/adapter/sessionscrub_emit_test.go:93` is "This advances sessions_served (feeding maxSessionsPerPod) on every clean release." — EVIDENCE: pkg/adapter/gatewaylink_test.go:107
FACT: the carrier table is now exhaustive for the mechanical sweeps. `grep -rn -iE "every (session|slot|clean) release|each (session|slot) release|per-session-release" --include=*.go --include=*.sql pkg cmd tests migrations` returns 30 hits and every one is either a carrier-table row, a non-carrier the table names (the `SessionScrubOutcome` comments, the tier-11 residual-state assertions), or an unrelated use ("every slot released cleanly" at pkg/gateway/podlifecycle/podsession/slotbinder.go:520 and slotbinder_test.go:676, which is the occupancy-zero recycle edge). — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:520
FACT: the served-count sweep (`sessions_served|SessionsServed` in comments joined with release/each/every) adds no unlisted carrier. `cmd/lenny-gateway/scrub_report_wiring_test.go:137` ("A clean session release increments sessions_served.") and `tests/tier4_integration/recycle_scrub_path_test.go:861,1083` ("per-release drain") narrate the case's own step or the §5.2 per-release drain that SPEC-3 leaves standing; `pkg/adapter/sessionscrub_emit_test.go:217` is a Shutdown recycle release of a running slot and stays true. — EVIDENCE: cmd/lenny-gateway/scrub_report_wiring_test.go:137
FACT: `tests/spec-map.json` keys on `file::TestName` (`:612` gatewaylink_test, `:1063-1065,1133-1135` sessionscrub_emit_test), so CODE-10's claim that neither map takes an edit survives the two new comment-only sites. — EVIDENCE: tests/spec-map.json:612
UNVERIFIED: `pkg/adapter/sessionscrubreporter.go:46-50` (the `reportSessionScrub` function comment, "emits the §5.2 per-slot cleanup outcome to the gateway after a session release") is NOT in the carrier table; the row for that file names only the `SessionScrubReporter` interface comment. I judged "after a session release" indefinite rather than universal, so below the bar. A later lens with a stricter reading should re-check it. — EVIDENCE: pkg/adapter/sessionscrubreporter.go:46
UNVERIFIED: SPEC-3's carrier row for `scrubreport_server.go` names "the `RecordSessionScrub` inline comment on evaluating the count on every release". The only inline comment inside `RecordSessionScrub` is at pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:452-456, which is about capturing the post-increment value and cites §12 (RETURNING sessions_served), i.e. the write §12.6 keeps. The anchor resolves but the row's description ("evaluating the count on every release") fits the `RetireOnSessionCount` comment at :219-224 better. Nobody has pinned which one CODE-10 means. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:452

### [non-spec-recheck.5.review-mechanism.1]

FACT: the delta this round is small and entirely CODE-10: two new carrier rows (`pkg/adapter/gatewaylink_test.go`, `pkg/adapter/sessionscrub_emit_test.go`), a new second-arm exception paragraph, two new bullets, the summary CODE-10 line and the Files-touched list. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:2113,2127,2150,2159,4077; .spec-changes.md:618-619; .summary.md:1071
FACT: both new tree claims check out exactly. `pkg/adapter/gatewaylink_test.go:107-116` carries the `// spec:` and `// diagnosis:` comments the bullet describes, with the annotation prefix `spec: §4.7 (ReportSessionScrub), §5.2 (maxSessionsPerPod)` quoted verbatim; `pkg/adapter/sessionscrub_emit_test.go:89-98` has the served-count sentence at :92-93, then the `// diagnosis:` at :95-97 and the `// spec:` at :98 ("below it" is right). — EVIDENCE: pkg/adapter/gatewaylink_test.go:107; pkg/adapter/sessionscrub_emit_test.go:93
WATCHOUT: the function in `sessionscrub_emit_test.go` is `TestShutdownSlotEmitsReleasedOnCleanClose_spec_5_2` (`:99`), while the proposal anchors on `TestShutdownSlotEmitsReleasedOnCleanClose`. The anchor still resolves because the doc comment's own first words are the unsuffixed name (`:89`), so this is not a defect; do not file it. — EVIDENCE: pkg/adapter/sessionscrub_emit_test.go:89,99
FACT: the mirror-test carrier sweep is still incomplete. CODE-10 re-keys `pkg/gateway/session/recycle/scrubreporter_seams.go`'s `sessionCountRetirer` comment but stages nothing in its unit test `pkg/gateway/session/recycle/scrubreporter_seams_test.go`, which restates "per-release" ~20 times including three `// spec:` glosses (`:724`, `:800`, `:973`) and "a per-release report on a pod ..." (`:916`, `:935`, `:950`). Filed this round as the only finding. — EVIDENCE: pkg/gateway/session/recycle/scrubreporter_seams_test.go:677,724,727,916,973
FACT: carriers I checked and cleared, so a later round need not re-walk them: `tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go`, `pkg/adapter/gatewaycontrol/scrubreport_test.go`, `tests/tier10_conformance/recycle_scrub_conformance_test.go`, `pkg/agentpodstate/{memstore,pgstore}` and their tests, `pkg/controller/sandbox/podspec/podspec_test.go`, `pkg/adapter/{slotsession,holdstate,sessionscrubreporter_internal}_test.go`, `tests/tier7a_load_local/coordinator_hold_termination_race_test.go` — none carries a release-keyed reporting or served-count universal. — EVIDENCE: git grep -n ReportSessionScrub over *.go
UNVERIFIED: `cmd/lenny-gateway/scrub_report_wiring_test.go:137` carries the inline comment "A clean session release increments sessions_served.", which is release-keyed and narrows under SPEC-3. I judged it below the bar because it labels the RPC call the fixture is about to make rather than stating a rule, and did not file it. A later round that widens the carrier table should decide it explicitly. — EVIDENCE: cmd/lenny-gateway/scrub_report_wiring_test.go:137
