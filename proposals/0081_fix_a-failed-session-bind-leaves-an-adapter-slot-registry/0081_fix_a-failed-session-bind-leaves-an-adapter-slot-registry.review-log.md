# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass 29, 2026-09-21). Read the whole ledger. The delta this pass owed was the
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
- **"The pod's shared runtime process has been given" is bound vocabulary.** It denotes `runtimeLive` cohort membership across spec/04, 15, 28, 29 and `pkg/adapter/runtimegeneration.go`; use it only for the `running` boundary.
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
- **MISTAKE, SEVEN rounds on one boundary, and the kind of answer that ends it is not a better
  predicate.** Every rewrite of the §5.2/§6.2 pre-`running` boundary changed the WIDTH of a residue
  classification in §6.2 and became the next finding. §6.2 now states no classification, list or trigger;
  reintroducing one is the same defect. Fix that block only as ONE whole rewrite.
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
- **Do NOT put the reclaim hold back inside the admission cascade as "rule 1".** Read today as: rule 2
  cites §5.2 for its scope, which is wider than the cascade's requests (a `Checkpoint` resolve is
  refused), so §4.7.1 must never restate that scope. The hold's precedence over rule 3 matters: a
  `mid_session` request during a hold would otherwise get the permanent `FAILED_PRECONDITION`.
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

### Open

- **Can the reclaim-hold refusal reach `Binder.Prepare`/`Binder.Launch` and drain the pod?** — OPEN: `errSlotReclaimInProgress` is a bare `codes.Aborted` matching neither CODE-7 sentinel, so CODE-8's guard falls through to `failPhase` and drains.
- **Does a permanently held slot identifier have to be answered as permanent?** — OPEN: a life-of-the-pod hold repeats `ABORTED`, which §15.4 publishes as retryable; the entry reaper is routed to remediation position 2.
- **Does rule 8's untokened arm compare equal across a replacement?** — UNVERIFIED: two untokened entries under one identifier compare "" to ""; no reachable interleaving shown. Check the SDK-warm `ConfigureWorkspace` path.
- **Which client envelope does a rule-6 `FAILED_PRECONDITION` at a non-setup-command bind request reach?** — UNVERIFIED: `SlotBindError.Reason()` may render non-retryable 422 `SLOT_FAILED`; rule 6 also makes a repeat `StartSession`'s `Unavailable` permanent.
- **Is the staged §5.2 ten-second graceful window the right figure, and does it want an operator override?** — OPEN: §10.1 states no window, runtime graces differ, and CODE-6 makes it per-member.
- **Does the whole-pod scrub racing the per-slot cleanup on the removing arm want an ordering statement?** — OPEN: `answerShutdown` fires asynchronous `startPodScrub` while the hold is held and the tree removal runs.
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
- **Do S16, S18 and S21's `Depends-on` lines owe S15 and S1?** — OPEN: S16 consumes S15's slot guard yet lists S14, and S11 and S13 both claim the `client.go` widenings. Harmless in order.
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
- **Is the ten-second graceful window an operator-tunable constant?** — OPEN [spec.10.review-single-source.1]: §10.1 states no close window and the code only comments "best-effort", so the figure is minted here; `code-best-practices.md` requires a non-spec default to be overridable.
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
- **Does the carrier table's completeness claim survive a LOOSER grep than round 12 ran?** — UNVERIFIED [spec.22.review-edit-sites.1]: the round-12 sweep matched only "on/at every/each session release"; patterns like "emits on release", "reports on release", "at release" and "per release" were run over `schemas/` alone and found one site. Widen the pattern before trusting the 24-row count.
- **Where is the persistent `leaked` ledger behind the `ceil(maxConcurrentSessions / 2)` replacement trigger stored, and does it have a §12.4 durable fallback?** — UNVERIFIED [spec.22.review-performance.1]: the staging routes new pre-`running` leaks into that ledger, so a Redis-backed volatile store would forget pods that should retire on a reset. Pre-existing rather than staged.
- **Do the round-19 and round-22 sentence-repeat sweeps disagree, or did their scopes differ?** — UNVERIFIED [compaction pass 29]: round 19 reported one repeat and round 22 six. Round 22 ran over four proposal files where round 19's recorded form covered `spec-changes.md`, so the counts may both be right. The newer entry is the one `### Settled` carries.
- **Does the §28.4 `ABSENT` claim-register row's precedent sentence name the wrong status?** — UNVERIFIED [spec.16.review-citations.1, spec.19.review-citations.1]: every `coordination_generation` fence row in `tests/claim-map.json` is `UNWIRED`, and only "In-flight RPC cancellation on a generation gap" is `ABSENT`. Judged decorative twice, because the sentence states the status it wants explicitly.

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
- DEFERRED [pkg/sandbox/slotstate/slotstate.go, two doc comments]: the `Released` comment and the
  `slot_cleanup → released` gloss carry the three-action effect list SPEC-4 retires from the §6.2
  fence. CODE-3 opens the file for `ValidTransitions()` alone; it owes both comment replacements.
  The same file's `:104` and `pkg/gateway/runtime/slothealth/slothealth.go:33` additionally carry the
  RETIRED "cleanup timeout exceeded" gloss for `slot_cleanup → leaked`, a fourth and fifth site beside
  the three prose homes the staging fixes. Neither is in any edit list; the code lane owns them.
- DEFERRED [schemas/lenny-adapter.proto, `Shutdown` RPC comment and `ErrorCode` enum header]: the RPC
  comment predates the two-teardown split, and "The catalog below mirrors spec §15.1" is false for
  codes 27, 28, and 29. SCHEMA-1 stages the `ReportSessionScrub` comments only.
- DEFERRED [docs/operator-guide/troubleshooting.md, the `setup_command_failed` row]: after SPEC-5 the
  reason has a second cause, a started-session refusal that ran no setup command, for which both
  listed remedies are wrong. No docs deliverable opens the page.
- DEFERRED [docs/runbooks/gateway-replica-failure.md]: the page calls a gateway crash benign for
  sessions. The crash-stranded registry entry (accepted failure mode) is a counterexample: the session
  is unstartable on that pod until the pod is replaced. The failure narrative owes that cause.
- DEFERRED [non-spec-changes.md, DOCS-3's retryable-fallback sentence]: it publishes the superseded
  refusal as "a newer start ... has superseded this one". Rule 5 fires on token inequality with no
  ordering, and the canonical case refuses the newer attempt. Reword without an age claim.
- DEFERRED [non-spec-changes.md, CODE-4 `Targets:` and `## Files touched`]: `upload_to_session.go`
  (the §7.4 pair sets `mid_session`) is a required edit named in CODE-4's prose and absent from its
  Targets list and from every checklist step. `start_test.go` is listed with no edit stated anywhere.
- DEFERRED [non-spec-changes.md, CODE-6 `ConfigureWorkspace` idempotent-repeat arm]: it reads the entry
  under a second `s.mu` acquisition after the claim returned, which CODE-2's own comment rules out.
  Have `claimSessionSlotUnderLock` return what the caller needs on its `idempotentRepeat` arm.
- DEFERRED [non-spec-changes.md, CODE-9's adapter-deferral sentence]: it says SPEC-6's row carries the
  shipped §16.1 deferral wording verbatim. SPEC-6's row reads "Adapter-side; not scraped until the
  adapter metrics endpoint is wired", which differs. Align the row or drop "verbatim".
- DEFERRED [non-spec-changes.md, the resume-path test bullet]: it asserts `fakeAssigner.released` stays
  empty, but `Binder.Resume` calls no release at all, so the assertion passes on every path. Assert
  on the attempt-scoped release the deliverable adds instead.
- DEFERRED [non-spec-changes.md, tier-7a case list]: it has a case for the stamp step's atomicity and
  none for rule 8's start step (a rule-14 reclaim removing the entry while the start confirms and
  records). The §10.1 ordering arm must park shorter than the guard-acquisition deadline.
- DEFERRED [non-spec-changes.md, logging and count wording]: `slot_tree_removal_failed` is claimed for
  every site while `terminateHeldSession` logs only `runtime_close_failed`; `answerShutdown`'s "one
  return" omits the empty-session-id return; one site still cites a whole-pod scrub §5.2 no longer names.
- DEFERRED [non-spec-changes.md, `**The mid-session conditioning.**` and the CODE-6 rule-4 comment]:
  the Design paragraph restates the §4.7.1 carriage table in prose, and the staged comment and CONF-1's
  rule-4 case attach exclusivity to rule 4 where the stamp-once rule owns it. Cite, do not restate.
- DEFERRED [spec-changes.md, commentary wording]: the untokened-entry bullet calls the §10.1
  hold-timeout termination "a request" where §5.2 says it runs under no request. The other half of this
  entry, that SPEC-3's rationale says the hold paragraph "states a terminal", was refuted in spec round
  4: the paragraph ends the hold only on completion, which entails the terminal, and the table names it.
- DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118]: the `Sandbox.status.phase` doc comment
  carries the projection-input enumeration SPEC-4 extends in §4.6.1 and §6.2, without the new input.
  It already omits "disposition", so it is a loose paraphrase that was incomplete before this proposal;
  record only. The authoring source is the Go doc comment plus `make generate`, never the two generated
  YAMLs (`charts/lenny/crds/lenny.dev_sandboxes.yaml`, `pkg/embedded/crds/lenny.dev_sandboxes.yaml`).
- DEFERRED [non-spec-changes.md, CONF-1 case "The reclaim hold against the `Shutdown` cascade"]: it
  attributes the `Shutdown` carve-out to §15.4's reclaim-hold block "rather than by either rule". After
  round 5 reduced that block's closing sentence to a pointer, the stating site is §5.2's reclaim-hold
  paragraph, so the attribution clause must be re-pointed at §5.2. Unverified whether any later pass
  applied it; the spec lane could not.
- DEFERRED [non-spec-changes.md, the scrub-outcome comment count, and summary.md's parallel]: both name
  "the two scrub-outcome comment replacements" with the RELEASED-implication qualifier. SCHEMA-1 now
  carries four replacements on that surface, the two outcome sentences plus the two report-trigger
  sentences. Drop the count rather than raising it.
- DEFERRED [implementation-checklist.md, S1]: four statements are false against the staging S1 lands.
  (a) "The slot-identifier reclaim-hold block is not rewritten" — S1 keeps the block's `ABORTED` wire
  obligation for rule 2 and reduces its closing `Shutdown` sentence to a citation of §5.2 and §4.7.1.
  (b) "§15.1's exclusion sentence is keyed on the gRPC code and stands unedited" — SPEC-5 replaces it,
  re-keyed on the setup-command request and naming `Aborted` among the excluded codes. (c) "§6.2's
  `**Client visibility:**` clause has its second clause re-keyed the same way" — that clause becomes a
  pointer at the §15.1 row, scoped to a failure of the setup-command request. (d) §15.4 "states the
  three non-conformances that are not any single rule's condition" — it states the conformance
  criterion, which reaches a wrong evaluation order, a separable step and a gRPC error for a reclaim
  outcome through its citation of §4.7.1, and deliberately never states the three.
- DEFERRED [implementation-checklist.md, S4]: the step names §5.2 alone and mentions neither §12.6 nor
  either tier-11 gate. What is true instead: the step that applies SPEC-3 also lands the §12.6 write
  re-key and read-clause reduction, and with them the two test-file edits recorded above. S4 also still
  says the `**Slot cleanup:**` bullet's leaked-outcome sentence "gains a citation of the disposition
  table"; the sentence is replaced outright by a pointer and retains none of its own disposition text.
- DEFERRED [implementation-checklist.md, S5]: it says SPEC-4 re-keys the §6.2 fence trigger and three
  clauses of §6.2's projection prose. What is true instead: §4.6.1's two claim-deletion bullets and its
  opening input enumeration are re-keyed and its false closing sentence is deleted, while §6.2's
  `claimed ──→ draining` trigger and the three claim-existence clauses of its projection prose are
  reduced to pointers at §4.6.1, with no input added to §6.2's sentence.
- DEFERRED [implementation-checklist.md, S7]: it describes DOCS-2 as the `Shutdown` and `DemoteSDK` rows
  only. What is true instead: the adapter-contract reference takes four edits, those two rows plus the
  `ReportSessionScrub` row (which points at the page's own `Shutdown` row for which cleanups are
  reported and reports none for any other release) and one bind-attempt paragraph.
- DEFERRED [implementation-checklist.md:19, S2 final sentence]: it reads "§29.4's session-end step 13
  restates the graceful-shutdown signal's co-tenancy condition and cites §4.7." Round 18 trimmed the
  staged sentence to a citation, so that is false and it directs S2 at text the staging does not
  contain. Replace it with exactly: "§29.4's session-end step 13 cites §4.7 for the graceful-shutdown
  signal's co-tenancy condition and states nothing about it itself." Do NOT use the replacement the
  archive carries at :45133 and :45174 ("...and states only that the step does not occur when the end
  leaves a bound co-tenant"): round 18 deleted that clause, so applying it restores a withdrawn claim.
  Body in `[spec.18.fix.1]`.
- DEFERRED [implementation-checklist.md, S15, S16, S19, S22]: the checklist predates the
  restructure. S15 and S16 undercount their edits and S16 must key the hold release on completion; the
  rest are untraced against the current staging.
- DEFERRED [implementation-checklist.md:17]: it states "The row's exclusion sentence is keyed on the
  gRPC code and stands unedited." SPEC-5 replaces that sentence, re-keyed on the setup-command request
  and naming `Aborted` among the excluded codes. This is the same falsehood the S1 entry above records
  as its item (b), carried at a second checklist line; both move in one edit.
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

### Retired

Bodies are in the archive file. This list names subjects only.

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

### [spec.22.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens on the spec
staging — BECAUSE every verbatim anchor in `.spec-changes.md` resolves to exactly one site in the
current tree, every artifact a staged edit references either pre-exists or is created by an earlier
spec step (the three forward references at S4 are the ones the checklist already records), and no
shipped gate hard-fails on the spec-only sequence. ALTERNATIVES: filing the §4.6.1 closing-sentence
quote mismatch (`§6.2` in the proposal versus a markdown link in the file) — already recorded and
refuted; filing the S4/S5 mutual dependency (the §5.2 post-table retirement sentence is false until
SPEC-4 lands) — the remedy is in the checklist, which this loop may not edit and which the prompt
declares not a finding.

FACT: every anchor quoted "verbatim" in the spec staging was re-derived this round and each matches
exactly one line. Spot-cites for the ones that cost the most to find: §4.1 third sentence
spec/04_system-components.md:157; §4.7 `Shutdown` row :686; §4.7 `DemoteSDK` row :674; §4.7
`ReportSessionScrub` row :692; §4.6.1 `draining, then terminated` bullet :416 and the input
enumeration :407; §4.7.9 step 5 :854; §5.2 `**Scrub model.**` spec/05:453 and the `**Slot cleanup:**`
bullet (all three of SPEC-3's anchors live on the single line :545), `**Whole-pod replacement
trigger:**` :561, `**Session count limit:**` :488; §6.2 projection prose spec/06:80, fence
`claimed ──→ draining` :95-97, `slot_cleanup ──→ leaked` :148, `receiving_uploads ──→ running` :152,
`slot_cleanup ──→ released` :155, `resuming` cancel clause and `**Client visibility:**` clause;
§7.1 atomicity paragraph spec/07:23 with its continuation line :24 inside the fence running :5-54,
§7.2 preamble :210 and step 3 :214; §12.6 prose spec/12:481 and DDL :494; §15.1
`SETUP_COMMAND_FAILED` row spec/15:1136 carries all four replaced sentences on one physical line;
§15.4 `**SDK-warm demotion contract:**` :1469 with `#### 15.4.1` at :1471.

FACT: the §16.1 catalog additions break no gate on the spec lane. `spec161Metrics`
(`pkg/observability/metrics/catalog_test.go:15`) is a HAND-MAINTAINED Go list, not parsed from
spec/16, so `TestMetricCatalogIsCompleteAgainstSpec161` and
`TestMetricCatalogHasNoUnspecifiedMetrics` (:188, :201) are inert against a new §16.1 row; and
`tests/tier11_docs/adapter_metric_catalog_test.go` only walks metrics registered in
`pkg/adapter/metrics.go`, so a catalog row for an unregistered series fires nothing. S6 before S10
is safe in both directions.

FACT: adding `receiving_uploads ──→ slot_cleanup` to §6.2's general per-slot block trips nothing.
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-37` enumerates only four
general edges and :69-74 checks that none of THOSE four appears in the scoped block; the new edge is
in neither list. The two `(see §5.2)` annotation replacements also survive, because the scoped-block
check tests for the bare substring `slot_cleanup ──→ leaked` (:65) rather than its trigger text.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43`) asserts ONLY the
addressing sentence and its opener `"The request is session-scoped: it is "`, on both carriers.
SPEC-3 leaves that sentence word for word, so the spec-lane row replacement does NOT strand the gate
between S4 and S7 (DOCS-2). The proposal's own claim about this gate at spec-changes.md:754-757 is
accurate.

FACT: `tests/tier11_docs/spec_47_rpc_row_naming_test.go` reads row names out of the whole `### 4.7 `
slice (:44-53) and holds Go comments to that SET only, one-directionally (:147-153). The §4.7.1
carriage table SPEC-5 inserts therefore only widens the allowed set and cannot fail it. It is a
different gate from the three FIRST-match gates the standing context warns about.

FACT: a whole-tree sweep for the distinctive strings the spec staging DELETES returns only four
carriers outside spec/, and all four are already dispositioned: `docs/reference/adapter-contract.md`
(the `DemoteSDK` opening clause, DOCS-2), `docs/reference/error-catalog.md` (two
`SETUP_COMMAND_FAILED` sentences, DOCS-3), and `pkg/controller/warmpool/occupancy.go` (the
"one-session-only invariant" phrase in a comment, code lane). No test file matched any of them.

WATCHOUT: `pkg/controller/warmpool/occupancy.go` carries the phrase "one-session-only invariant",
which is the §4.6.1 sentence SPEC-4 deletes as false against that very function. No spec or docs
deliverable touches it and it is not in the review log's Deferred list under that name. It is a
code-lane comment, so the spec loop cannot file it; the non-spec loop should decide whether CODE-3
or another step re-keys it. EVIDENCE: pkg/controller/warmpool/occupancy.go (grep
"one-session-only invariant"); spec-changes.md:904-911 deletes the sentence it paraphrases.

USEFUL [Traps, "MISTAKE nearly filed in FOUR separate rounds: the SPEC-4 §6.2 fence edit's anchor
form"]: it killed the candidate before I spent a verification on it. USEFUL [Traps, "Three tier-11
gates resolve `| \`Shutdown\` |` ... by FIRST match inside the `### 4.7 ` slice"]: I re-derived the
line positions (Gateway → Adapter `Shutdown` row at :686, Adapter → Gateway `ReportSessionScrub` row
at :692, `#### 4.7.2` at :695) and the ordering still holds, so the §4.7.1 insertion point is still
below both tables.

### [spec.22.review-citations.1]

DECISION: returned EMPTY under the citation-audit lens — BECAUSE every concrete citation in `spec-changes.md` re-verified: all 25 verbatim anchors hit exactly once across `spec/*.md`, all 23 markdown anchors resolve, every `pkg/`/`tests/`/`schemas/`/`docs/`/`migrations/` path named exists, and every attributed code behaviour re-derives from the tree — ALTERNATIVES: filing the §6.2-vs-§4.6.1 one-hop pointer for the post-table retirement clause (`A pod serving one session whose claim the failed bind deletes retires under the [Section 6.2] occupancy projection`), rejected because §6.2's fence keeps the `claimed ──→ draining` claim-deletion edge and its prose keeps a pointer sentence at §4.6.1, so the citation resolves.

FACT: the whole citation sweep is now THREE cheap commands and costs about ten minutes — EVIDENCE: (1) python over `re.findall(r'\n```\n(.*?)\n```\n')` from spec-changes.md counted against `glob('spec/*.md')` — the zero-hit blocks are exactly the replacement texts, the two new fence entries, the SPEC-4 `claimed ──→ draining` replacement, the §7.1/§4.7.1/§15.4/§6.2-prose inserts and the two §16.1 rows; (2) `grep -o "\`\(pkg\|tests\|schemas\|docs\|migrations\|charts\|cmd\|examples\)/[A-Za-z0-9_./-]*\`" | while read f; do [ -e "$f" ] || echo MISSING; done`; (3) a python slugger over `^#` headings of `spec/*.md` matched against `\]\((file)?#anchor\)`. Only apparent anchor failures are the two bare `#71-normal-flow` / `#73-retry-and-resume` links, which land in spec/07 and resolve there.

FACT: the SPEC-4 `claimed ──→ draining` fenced block is the REPLACEMENT, not an anchor, and the deliverable gives no verbatim anchor for it — EVIDENCE: spec/06_warm-pod-model.md:95-97 reads `claim deleted on a pod / with recycle.enabled: false;` while the staged block reads `claim deletion — see §4.6.1`. The instruction scopes it uniquely ("In the fenced `Occupancy projection` block"), and four other `claimed ──→ draining` entries exist at :114, :135, :137, :139 in other blocks. Not a finding; do not re-open it as an unresolvable anchor.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts more than the substring — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:68-77 requires both carriers to contain `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` as one string, on the single line `lineContaining` returns. SPEC-3's replacement keeps that sentence byte-identical and the row one physical line, so the gate stays green; DOCS-2 must keep the identical sentence.

USEFUL [the verbatim-anchor sweep is DONE ... re-run independently in rounds 2, 4, 5, 7, 10 and 11]: re-running it from scratch cost one command and confirmed the standing claim after the two prunes and two redesigns; the entry's stated python recipe is exactly right and saved deriving it.

### [spec.22.review-client-surface.1]

Round 22, client-facing surface integrity lens over the staged SPEC edits. Result: no findings.

USEFUL [spec.20.review-client-surface.1]: its FACT list (the §15.1 row is one physical line at
spec/15:1136 with all four replaced sentences verbatim; the served OpenAPI document is at
pkg/gateway/externalapi/openapi/openapi.json and enumerates no REST error codes; no SDK or
docs/api page carries `SETUP_COMMAND_FAILED`) held on re-check and saved the whole sweep.

FACT: the spec-changes delta since that round is terminology only. `git diff a6abb507b..HEAD --
proposals/0081_*/*.spec-changes.md` is four hunks: two Edge-cases wordings ("the reclaim's cleanup
completed"), the §7.1 **acknowledged clean** rewrite, one `leaked` clause and the §5.2 hold
paragraph's bolded **completed**. No client-facing representation moved, which is why this pass
could be short. EVIDENCE: git commit 1b12bba90

FACT: every verbatim anchor the client-facing staged edits quote still matches the tree.
§15.1 `SETUP_COMMAND_FAILED` cause/retryability/setup-output/exclusion sentences
(spec/15_external-api-surface.md:1136); §6.2 `**Client visibility:**` clause
(spec/06_warm-pod-model.md:290); §4.7 `Shutdown` row opening (spec/04_system-components.md:686);
§4.7 `DemoteSDK` row (spec/04_system-components.md:674); §4.1 third sentence
(spec/04_system-components.md:157). The §15.4 insertion point is real: the `**SDK-warm demotion
contract:**` paragraph is spec/15:1469 and `#### 15.4.1` is spec/15:1471.

FACT: the DemoteSDK row's citation "[§6.1] admits `preConnect` only at `maxConcurrentSessions: 1`"
is accurate — the compatibility paragraph and table sit at spec/06_warm-pod-model.md:69-76, inside
§6.1 (§6.2 starts at spec/06_warm-pod-model.md:78). A lens tempted to file it as a wrong-section
citation should stop here. EVIDENCE: spec/06_warm-pod-model.md:69,78

FACT: `ShutdownRequest` is named in exactly one spec sentence
(spec/04_system-components.md:157, the §4.1 Request Message Scope paragraph SPEC-1 edits), and no
spec file enumerates adapter `ErrorCode` values, so the two new codes and the two new `Shutdown`
fields have no second spec-side carrier to mirror. EVIDENCE: grep "ShutdownRequest" spec/ → one hit;
grep "ErrorCode" spec/ → spec/15:552, spec/15:1462 (both generic)

FACT: widening `SETUP_COMMAND_FAILED`'s cause does NOT falsify the five other spec sites that
mention it. spec/15:625, :646, :647, :650, :720 each state a sufficient condition ("a deterministic
non-zero setup command surfaces as ..."), never an exhaustive cause, and spec/07:390 is a bare JSON
example value in `retryPolicy.nonRetryableFailures` that defines nothing. Do not file these as
missed edit sites.

DECISION: returned an empty findings list — BECAUSE the only client-facing surfaces the staged spec
edits touch are the §15.1 row, §6.2's client-visibility clause, the §4.7 RPC rows, the §15.4
published-contract blocks and the §16.1 rows, and each parallel representation is either unaffected
(OpenAPI, client SDKs, MCP tool schemas, CRDs, §15.2 taxonomy) or staged in the non-spec lane
(error-catalog.md, adapter-contract.md, metrics.md, lenny-adapter.proto) — ALTERNATIVES: filing the
§6.2 clause's dropped "any other setup-window failure stays the retryable `STARTING_FAILED`
fallback" half, rejected because the §15.1 exclusion sentence SPEC-5 rewrites carries that mapping
and deleting the restatement is the reduction rule (g) asks for.

### [spec.22.review-docs-alignment.1]

DECISION: returned EMPTY — BECAUSE every candidate my lens produced was either already
refuted by a standing trap, or its only remedy lands in the non-spec (docs) lane, which
this loop's scope excludes — ALTERNATIVES: refiling the two accepted residues
(gateway-crash-stranded entry, self-recreated entry) and the summary's two
"further residues", both rejected; see the FACTs below.

FACT: the SPEC-3 carrier table's docs coverage is complete over the whole `docs/` tree.
`grep -rn "per-slot cleanup" docs/` returns exactly six sites. Two carry the withdrawn
report universal and are DOCS-4 carriers (docs/reference/execution-modes.md:68 "and the
adapter reports its outcome to the gateway", docs/operator-guide/security-principles.md:33
same clause). The other four carry no report clause and are correctly non-carriers:
docs/operator-guide/multi-tenancy.md:72, docs/runtime-author-guide/index.md:186,
docs/runtime-author-guide/lifecycle.md:69, docs/client-guide/session-lifecycle.md:416
(a mermaid arrow label). `grep -rn ReportSessionScrub docs/` returns only
docs/reference/adapter-contract.md:75 (`Shutdown` row, DOCS-2) and :81
(`ReportSessionScrub` row, DOCS-2). No seventh carrier exists.
EVIDENCE: docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33;
docs/operator-guide/multi-tenancy.md:72; docs/reference/adapter-contract.md:75,:81

FACT: DOCS-3 fully discharges the spec-changes Edge-cases claim that it "mirrors those same
replacements into the `SETUP_COMMAND_FAILED` row". SPEC-5 makes four §15.1 sentence
replacements (cause, retryability, setup-output, exclusion); DOCS-3 stages four matching
description-cell replacements plus a remedy-cell replacement, all in the page's own voice
with no spec section numbers and no adapter `ErrorCode` names. The claim is true as written;
do not re-check it.
EVIDENCE: non-spec-changes.md:2563-2634 (DOCS-3 block); spec-changes.md:1094-1159 (SPEC-5 §15.1)

FACT: §16.1's metric catalog table is TWO columns (`| Metric | Type |`), and SPEC-6's two
staged rows are two columns, so they fit. The adapter-side deferral phrasing SPEC-6 uses
("Adapter-side; not scraped until the adapter metrics endpoint is wired") is a shortened form
of the shipped phrasing at spec/16:188-189; the standing context already bars filing it.
EVIDENCE: spec/16_observability.md:5, :14-15, :188-189; spec-changes.md:1186-1198

FACT: §15.4's shipped `**SDK-warm demotion contract:**` paragraph (spec/15:1469) does NOT
become wrong under SPEC-1's amended §4.7 `DemoteSDK` row. It states the teardown, the 10s
timeout, the `UNIMPLEMENTED` code and "post-demotion pod state (equivalent to a freshly warmed
pod-warm pod)", none of which the added registry removal falsifies. Publishing the removal in
§15.4 is a RECORDED REJECTED ALTERNATIVE ("Rejected: stating it in rules 1-15, in §6.1/§15.4,
or as SPEC-7"), so do not file it as a missing published-contract site.
EVIDENCE: spec/15_external-api-surface.md:1469; review-log.md `### Settled`, "DECISION: SPEC-1
gains a §4.7 `DemoteSDK` row"

USEFUL [the docs-alignment lens's two accepted residues ... filed and refuted in rounds 12, 15,
18 and 20]: its mechanical check (`awk 'NR>=243' spec-changes.md | grep -n
"crash\|recreat\|empty workspace\|unstartable\|stranded"`) saved a whole candidate branch.

USEFUL [the caller-round-0 prune directives]: the summary's "Two further residues are priced
and accepted" block (summary.md:420-427) reads, to this lens, exactly like an accepted failure
mode missing from the Edge-cases section — which the lens text names as a finding. It is not:
prune 4 deliberately put those two residues in the summary and nowhere else. Without the
directive this shard would have filed it.

### [spec.22.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens on round 22 — BECAUSE every verbatim anchor in the staged blocks still resolves byte-for-byte, every intra-spec link target the staged text introduces resolves to a real heading, and every identifier the staging adds or retires re-swept clean against `spec/`, `docs/`, `schemas/` and `charts/`; every surface the sweep surfaced is already a staged edit, a named non-spec deliverable, or a SPEC-3 carrier-table row — ALTERNATIVES rejected: (a) `schemas/lenny-adapter.proto:257`, see the DEFERRED below; (b) `spec/07_session-lifecycle.md:72`'s `scrubPolicy` row, which parenthetically enumerates the cleanup's acts ("workspace removal, process-group kill, and scratch directory cleanup") — it already disagreed with §5.2's three-act list before SPEC-3 widens it to five, so it does not BECOME wrong and the divergence is pre-existing; (c) the §15.1 endpoint-precondition rows at `spec/15_external-api-surface.md:625,646,647,650,720`, each of which states the setup-command cause as sufficient rather than exhaustive and stays true under SPEC-5's widening (already on the standing refuted list).

FACT: the round-22 anchor re-verification is clean at these exact lines. §4.1 sentence `spec/04_system-components.md:157`; §4.6.1 projection preamble and the two claim-deletion bullets `:409,:416,:417`; §4.7 `Shutdown` `:686`, `DemoteSDK` `:679`-region row, `ReportSessionScrub` `:692`; §4.7.1 insertion point is immediately before `#### 4.7.2` at `:697`-region; §5.2 `**Scrub model.**` `:453`, `**Slot cleanup:**` `:545`, `**Whole-pod replacement trigger:**` `:561`; §6.2 projection prose `:80`, fence `claimed ──→ draining` `:95` (three further `claimed ──→ draining` entries at `:114,:135,:137,:139` sit in the Recycle-edges and Concurrent-occupancy groups and are NOT the target — the staged text disambiguates by naming the `Occupancy projection` group), `slot_cleanup ──→ leaked` `:148`, `receiving_uploads ──→ running` `:152`, `slot_cleanup ──→ released` `:155`, mid-resume cancel bullet `:234`, `**Client visibility:**` `:290`; §7.1 atomicity paragraph `:23` with its continuation line `(executionMode, isolationProfile, scrubPolicy summary)` at `:24`; §7.2 preamble/step 2/step 3 at `:212,:213,:214`; §12.6 `:481,:494`; §15.1 `SETUP_COMMAND_FAILED` `:1136`; §15.4 `**SDK-warm demotion contract:**` `:1469` immediately before `#### 15.4.1` at `:1471` — EVIDENCE: spec/06_warm-pod-model.md:95,114,135,137,139

FACT: `spec/06_warm-pod-model.md` lines 82–157 are ONE fenced block containing five labelled groups (Warm fill pod-warm, Warm fill SDK-warm, Occupancy projection, Recycle edges, Concurrent occupancy) plus two per-slot sub-state groups. A future agent reading "the fenced `Occupancy projection` block" must not expect a separate fence — EVIDENCE: spec/06_warm-pod-model.md:82,94,105,128,147,151

FACT: §4.9's `anthropic_direct` row (`spec/04_system-components.md:1169`) states that the adapter MUST arm a per-lease expiry timer and what it does when the timer FIRES, and states nothing about cancelling it. SPEC-3's added "cancels the §4.9 direct-delivery-mode lease-expiry timers" act therefore duplicates no §4.9 rule and contradicts none — EVIDENCE: spec/04_system-components.md:1169

FACT: §5.2's whole-pod credential purge (step 0, `spec/05_runtime-registry-and-pool-model.md:461`) is worded "Remove every per-slot credential file ... REMAINING under `/run/lenny/slots/`", so it is already compatible with a per-slot cleanup that removes the same file first. SPEC-3's widened action list opens no conflict there — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461,471

DEFERRED [schemas/lenny-adapter.proto]: the `GatewayControl` SERVICE comment at `schemas/lenny-adapter.proto:255-257` says the service "carries the §5.2 per-slot and whole-pod scrub reports (ReportSessionScrub, ReportPodScrub) the adapter emits on release." SCHEMA-1 edits only the `ReportSessionScrub` RPC comment (`:308-310`) and the `ReportSessionScrubRequest` message comment (`:451-452`), so this third site is in no edit list and in no SPEC-3 carrier-table row. I did NOT file it: the round-12 whole-tree sweep's grep patterns (`on every session release|at each session release|on each session release|at every session release`) cannot match "emits on release", so the omission is real, but the clause is a restrictive description identifying the two RPCs rather than a universal rule about releases, and it is already loose for `ReportPodScrub` (emitted at occupancy zero, not at a release). If the non-spec loop wants the belt-and-braces fix, the minimal one is dropping the four words "the adapter emits on release" from that sentence; it is not worth a carrier-table row.

UNVERIFIED: whether the round-12 "carrier table is COMPLETE against a whole-tree sweep" claim survives a grep for the looser forms ("emits on release", "reports on release", "at release", "per release"). I ran only the `schemas/` half and found the one site above. Somebody re-running that sweep should widen the pattern before trusting the 24-row count.

USEFUL [spec.12.review-edit-sites.1]: its FACT block on the carrier table, on §16.1.1's "Used on" column being descriptive prose no gate reads, and on the `SETUP_COMMAND_FAILED` endpoint rows stating a sufficient rather than exhaustive cause saved me three filings and most of a sweep. The line numbers it cites were all still accurate at round 22.

### [spec.22.review-fresh.1]

DECISION: returned EMPTY for the fresh-holistic lens at spec round 22 — BECAUSE I applied the
staged edits mentally, in order, against the live spec text and every one lands cleanly: all 25
fenced anchors hit exactly once, the four inline-quoted originals SPEC-4 names in prose (the
§4.6.1 preamble phrase and two bullet openings, the three §6.2 projection clauses) all exist
verbatim, and the residual text each deletion leaves is grammatical and non-contradictory. The
one candidate I worked up (the §4.6.1 closing sentence quoted in a parenthetical with `§6.2`
where the file has a markdown link) is below the bar: the whole bullet is replaced by a given
block, so nothing is ambiguous to apply — ALTERNATIVES: filing that parenthetical as a false
citation (rejected, it is an identification aid not a verbatim anchor); filing the §4.7
`Shutdown` row's "which teardown it asks for" against rules 12/14 (rejected, already the
round-22 mechanism lens's recorded WATCHOUT and it is wording).

FACT: the three SPEC-4 §6.2 projection-prose deletions compose into exactly the sentence the
proposal says they leave. The shipped clause is "...and a terminal claim disposition (`released`
or `failed`), or a claim deleted on a pod with `recycle.enabled: false`, projects `draining` and
then `terminated`."; removing the middle phrase gives the proposal's stated result word for
word, and the two `idle` clauses exist verbatim with their trailing "; ". Deleting the first
("a pod with no claim projects `idle`; ") leaves the colon followed by "a claim with binding
state `bound` projects `claimed`; ", which reads correctly — EVIDENCE: spec/06_warm-pod-model.md:80

FACT: the §7.2 preamble deletion leaves a grammatical two-sentence lead-in ("...onto a
replacement pod. The gateway handles both edges with the same close sequence:"), and the
paragraph after the close sequence ("**Pre-attach terminal collapse...**") independently states
the no-pod-attached case, so deleting the "no live workspace to seal" premise strands no later
sentence that depends on it — EVIDENCE: spec/07_session-lifecycle.md:212, :219

FACT: the §5.2 append lands between a run of bolded standalone paragraphs (`**Scrub model.**`
:453, `**Recycle lifecycle...**` :455, ...), not inside a list or a fence, so a markdown table
plus two paragraphs can be appended there without breaking the enclosing structure. The
append's own capitalised anchor strings are `**Slot cleanup:**` and `**Scrub model.**`; no
shipped tier-11 gate calls `requireLine(s52, "Slot cleanup")`, and the two that do call it
(`"Whole-pod replacement trigger"`, `"Session count limit"`, both in
concurrent_slot_lifecycle_doc_reconciliation_test.go) match no line the append introduces,
because the append writes "whole-pod replacement trigger" in lower case and never writes
"Session count limit" — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,488,545,561;
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155;
tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:158 (`requireLine` = first match)

FACT: the post-table residue sentence's whole-pod-scrub clause re-verifies act by act against
§5.2's own scrub procedure. Step 0 removes `/run/lenny/slots/{sessionId}/credentials.json` and
step 2 removes `/workspace/slots/*`, which is exactly "ends the slot's workspace tree and
credential file" and no more — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461,:467

FACT: `PrepareWorkspace` is the only client-streaming RPC among the seven the `**Admission.**`
preamble enumerates, re-derived from the proto service block rather than from the log, which is
what makes rule 9's first-frame form sufficient and leaves the other six needing no frame rule —
EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151,206

USEFUL [spec.22.review-mechanism.1] and [spec.22.review-single-source.1]: reading the co-round
shards before finishing killed two duplicate candidates of mine (the "two referents of teardown"
wording and the §7.1 `acknowledged clean` versus rule 15 pair) at no verifier cost. The standing
context's instruction to read the co-round shards paid for itself a second time.

USEFUL [The verbatim-anchor sweep is DONE ... re-run independently in rounds 2, 4, 5, 7, 10 and
11]: the recorded python recipe reproduced the whole sweep in one command; the only zero-hit
blocks are replacements, the two new fence entries, the SPEC-4 `claimed ──→ draining` block, the
four inserts and the two §16.1 rows, exactly as the entry predicts.

### [spec.22.review-kubernetes.1]

DECISION: returned EMPTY for the Kubernetes-idiom lens at spec round 22 — BECAUSE the round-20→22 delta
is terminology only (commit 1b12bba90, five one-phrase rewrites splitting `completed` from
`acknowledged clean`) plus the round-18 commentary prune, none of which touches a CRD, a controller, a
status subresource, a finalizer, an admission webhook or an apiserver path; and the one K8s surface the
staging does touch, SPEC-4's §4.6.1 projection re-key and §6.2 fence, re-verifies against the tree
unchanged — ALTERNATIVES: none new; the four dresses `[spec.20.review-kubernetes.1]` refuted were not
re-derived because the text they attach to is byte-identical.

FACT: the round-20→22 spec-changes delta is exactly five hunks, all wording: `git show 1b12bba90 --
proposals/0081_*/0081_*.spec-changes.md` gives two Edge-cases phrases ("after the reclaim's cleanup
completed"), the §7.1 acknowledged-clean sentence pair, the sentence after it, and a bolding of
`**completed**` in the §5.2 hold paragraph. No K8s vocabulary moved — EVIDENCE: commit 1b12bba90.

FACT (re-verified this round, third independent confirmation): SPEC-4's two verbatim anchors still match
the tree word for word — spec/04_system-components.md:409 (the input enumeration SPEC-4 extends), :415
(the `idle` hold-expiry bullet) and :416 (the `draining`, then `terminated` bullet with the false
closing sentence SPEC-4 deletes) — and `ProjectOccupancyPhase`'s no-claim arm is still
`Reserved→Idle`, `Claimed→Draining`, `("", false)` otherwise — EVIDENCE:
pkg/controller/warmpool/occupancy.go:127-142.

FACT: the §4.7 `ReportSessionScrub` "before" row the proposal quotes is byte-identical to the shipped
row, so the `lenny.dev/drain-request` clause and its §4.6.3 pointer are carried over rather than
minted, and the gateway's RBAC already grants the annotation patch it names — EVIDENCE:
spec/04_system-components.md:692 (the row), :632 (gateway SA `get`/`patch` on Pods for the
drain-request annotation, and "The gateway holds no `sandboxes/status` grant").

USEFUL [spec.20.review-kubernetes.1]: its one-grep recipe for the whole K8s surface
(`grep -n -i "finalizer\|etcd\|apiserver\|RBAC\|webhook\|Sandbox\|annotation\|field manager\|server-side
apply\|reconcil\|leader"` over the spec-changes file) still returns the same ten hits at round 22, which
reduced this lens to confirming the delta and the SPEC-4 anchors. Keep it through compaction.

USEFUL [Open: "Can a coalesced reconcile lose the claim-DELETE retirement?"]: still the only live
K8s-idiom question, still pre-existing with a code remedy, and correctly not a spec-lane finding. This
is now the fifth kubernetes lens to land on it. A future firing of this lens should read the standing
context's two K8s entries plus this one and stop; the candidate space is exhausted.

### [spec.22.review-mechanism.1]

DECISION: returned an empty findings list for the end-to-end mechanism lens — BECAUSE every flow I traced closes: the admission cascade (rules 1-7) and the `Shutdown` cascade (rules 10-14) are each total and first-match over the carriage table's field combinations; rule 8's start confirmation, the §5.2 disposition table's nine rows, the post-table residue paragraph and the reclaim-hold paragraph partition the same act set without a gap; §4.1's scrub clause, rule 10's `INVALID_ARGUMENT` and the settled one-exit CODE-1 handler agree. ALTERNATIVES: I considered and rejected filing the `Shutdown`-row clause described in the WATCHOUT below.

WATCHOUT: the word "teardown" carries two referents inside one staged block and a lens will want to file it. The §4.7 `Shutdown` row names "two teardowns" (**slot release**, **runtime teardown**) and then says "Every request states which teardown it asks for, by carrying either a non-empty `bind_attempt` ... or `unconditional_teardown`", while rules 12 and 14 BOTH "perform both teardowns under their own preconditions" and the §4.7.1 sub-heading's own second sentence says "the two teardowns follow from the removal rather than being decided separately". The rescuing reading is that "which teardown it asks for" names the request form (compensating vs. unconditional), which is how rule 12 is titled. I judged it below the bar: an implementor is sent to the numbered rules by the next sentence of the same row, so no rule is misimplementable from it, and the remedy is wording. Do not spend a round on it unless you can show a rule goes wrong. EVIDENCE: proposal .spec-changes.md:273 (row), :1033 ("the two teardowns follow from the removal"), :1037 and :1039 (rules 12, 14).

FACT: every verbatim anchor SPEC-1 through SPEC-6 quotes still hits byte for byte on 2026-09-21. Re-checked this round: spec/04:157 (§4.1 third sentence), spec/04:674 (`DemoteSDK`), spec/04:686 (`Shutdown`), spec/04:692 (`ReportSessionScrub`), spec/04:854 (§4.7.9 step 5), spec/04:415-416 (§4.6.1 two bullets), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list, reporting sentence, leaked sentence), spec/05:561 (replacement-trigger parenthetical), spec/06:80 (§6.2 projection prose clauses), spec/06:290 (`**Client visibility:**`), spec/06 fence entries, spec/07:23, :210, :213, :214, :414, spec/12:481 and :494, spec/15:1136 (four `SETUP_COMMAND_FAILED` sentences). Every section anchor the staged text links resolves (§4.6.1, §4.6.3, §4.7, §4.7.1, §4.7.9, §4.9, §5.2, §6.1, §6.2, §7.1, §7.3, §7.4, §10.1, §15.1, §15.4, §15.4.2, §15.4.3). EVIDENCE: spec/07_session-lifecycle.md:438 (`### 7.4 Upload Safety`); spec/04_system-components.md:659, :688, :695.

FACT: the `**Slot-identifier reclaim hold.**` paragraph's close-bounding sentence is exhaustive, which is not obvious. It bounds the close by the reclaiming `Shutdown`'s graceful window, that request's deadline, or ten seconds for the §10.1 hold-timeout termination, and names no other performer — correctly, because the only other cleanups are the SDK demotion (closes BEFORE it deregisters, so no hold is open across its close) and the failed-start handler (`releaseSessionSlot` calls no `Runtime.Close`). Do not file the enumeration as incomplete. EVIDENCE: proposal .spec-changes.md:635; pkg/adapter/sdkwarm.go; pkg/adapter/slotsession.go.

FACT: §10.1's "Hold state timeout" really does exist and really does terminate every session the adapter has STARTED on the pod, which is what makes the disposition table's "Slot of either kind" key sound for that performer. EVIDENCE: spec/10_gateway-internals.md:58.

FACT: §5.2's `**Session count limit:**` bullet (spec/05:488) still keys the concurrent drain on "the session release that drives the served-session count to `maxSessionsPerPod`", and SPEC-3 re-points §12.6's read clause at that bullet without editing it. I checked whether the withheld-report rule falsifies it and it does not: a release that files no report drives the count to nothing, so the sentence stays vacuously true for it. Do not file it as an unstaged carrier; it is also correctly absent from SPEC-3's carrier table.

FACT: the SPEC-4 §6.2 `claimed ──→ draining` edit is the one staged edit that shows only its REPLACEMENT block and quotes no verbatim original (the shipped entry reads "claim deleted on a pod / with recycle.enabled: false"). It is still resolvable — the block and the entry are both named — and the standing context already records it among the deliberate zero-hit blocks in the anchor sweep. Do not file it as an unresolvable anchor. EVIDENCE: proposal .spec-changes.md:841-848; spec/06_warm-pod-model.md:93-96.

### [spec.22.review-performance.1]

DECISION: returned EMPTY on the performance/scalability/failure-reliability lens — BECAUSE the staging adds no per-session or per-request write to etcd, Postgres or Redis; it net-REDUCES Postgres writes (the SPEC-3 biconditional narrows `ReportSessionScrub`, and `sessions_served` is incremented only on a report, `ScrubReporter.RecordSessionScrub` in pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go). The only new per-unit work is one compensating `Shutdown` per FAILED bind plus its per-slot cleanup, and two counters (SPEC-6). No net-new watch, no new informer cache, no new hot key, no new single-leader serialization. — ALTERNATIVES: I considered filing the leaked-slot capacity amplification and the unacknowledged-reclaim pod retirement, and rejected both as relitigating accepted failure modes the spec-changes Edge-cases section already records.

FACT: the registry critical section staged in §4.7.1 does NOT put any slow act under the one registry lock. The `Shutdown` step is "the decision of rules 11 through 14 and the deregistration"; the cleanup acts are explicitly outside it ("The hold outlasts the deregistration because the cleanup's remaining acts are addressed by the slot identifier"), and the start step is resolve+confirm+record, with `Runtime.Start` outside. So at the top documented tier (`maxConcurrentSessions: 8`, spec/05:546) the lock holds only O(1) map work. Do not re-derive. — EVIDENCE: spec-changes.md:1015; pkg/adapter/slotsession.go

FACT: `ProjectOccupancyPhase` reading back the pod's own projected phase (the input SPEC-4 adds to §4.6.1's enumeration) does NOT create a reconcile write loop. The no-claim branch is `Reserved→Idle`, `Claimed→Draining`, and `default → ("", false)`, so `draining` is a fixed point the projection stops owning. SPEC-4 is a spec-to-code alignment with zero change in controller write rate. — EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143

FACT: the §10.1 hold-timeout termination's ten-second graceful window, which the staged §5.2 reclaim-hold paragraph names, is ONE `context.WithTimeout(..., 10*time.Second)` SHARED across every member the hold deregistered, not a per-member window. I did not file it: the code's own comment justifies the sharing ("a runtime process serving more than one session returns from a non-last close without touching the child, so only the last member's close consumes the grace"), and the staged sentence bounds only "the cleanup's close of the session on the pod's shared runtime process", which is that one close. A future lens tempted by the aggregate-vs-per-unit angle should note that `emitFinalUsage` also runs on that shared ctx per member, so at `maxConcurrentSessions: 8` the budget is genuinely shared for the usage flushes — but the staged sentence says nothing about the flush, so there is nothing to correct. — EVIDENCE: pkg/adapter/holdstate.go:196-205, 236; spec-changes.md:635

FACT: `ShutdownRequest` carries `coordination_generation` (field 6, schemas/lenny-adapter.proto:1635) and the proto comment says a pod "validates the generation on every gateway-to-pod RPC", which would make a compensating reclaim from a DEPOSED coordinator rejectable and leave a stamped entry no attempt can clear — a handoff-time failure mode worse than the shipped adopt-the-entry behaviour, and one the "Spec sections deliberately untouched" entry on §10.1 denies. I did NOT file it, because the shipped adapter validates the generation only in `CoordinatorFence` and `CheckpointBarrier`; `Server.Shutdown` performs no generation check, so the scenario does not occur in the tree and the finding would rest on an unimplemented spec claim (pre-existing gap). — EVIDENCE: pkg/adapter/coordination.go:120,262 are the only two gates; grep for `CoordinationGeneration` in non-test pkg/adapter returns only coordination.go

UNVERIFIED: where the persistent `leaked` ledger behind the `ceil(maxConcurrentSessions / 2)` whole-pod replacement trigger (spec/05:561) is stored, and therefore whether it has a §12.4 durable fallback across a Redis reset. The staging routes new pre-`running` leaks into that same ledger, so if it is Redis-backed and volatile, a reset forgets pods that should retire. This is pre-existing rather than staged, so it is out of this loop's scope; a non-spec-lane or a later §12.4 reviewer should settle it.

### [spec.22.review-reliability.1]

DECISION: returned EMPTY — BECAUSE every recovery-path candidate this lens generated against the
staged blocks resolves into an already-settled entry, an already-recorded `### Open`, or an
Edge-cases bullet that prices the residue — ALTERNATIVES: filing the life-of-the-pod hold on a
non-reporting release (table rows 7 and 9) as a resource with no reclaimer; filing rule 8's
take-the-session-back-off as unbounded on failure; filing the unbounded `removeSlotTree` inside the
hold window. Each is either the recorded Open "Does a permanently held slot identifier have to be
answered as permanent?" or the recorded Open on the scrub/cleanup race, and none is a defect in a
staged block.

FACT: the two tree claims SPEC-3's action-list commentary rests on re-verify exactly.
`deregisterSlotLocked` cancels every armed expiry timer before `delete(s.slots, …)` —
EVIDENCE: pkg/adapter/slotsession.go:174-189. `RemoveTree` iterates
`slotRoot, Sessions, Artifacts, CredentialsDir` and returns the FIRST error over the four —
EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69. So "removes the slot's credential directory" and
"cancels the §4.9 timers" are both shipped, and per-act residue remains unimplementable.

FACT: §10.1's hold-state-timeout bullet does NOT state a slot cleanup report, so SPEC-3's
report biconditional falsifies nothing there. It delegates the slot disposition to the whole-pod
connection-loss paragraph, which is gateway-side (Redis `active_slots` reset and rehydrate) and
names no `ReportSessionScrub` — EVIDENCE: spec/10_gateway-internals.md:58 and :62. A lens looking
for a missed edit site on the withheld report should stop here rather than re-grep spec/10.

WATCHOUT: the reliability candidate that looks freshest on a first read is "a cleanup that fails
outside a `Shutdown` holds the identifier for the life of the pod while nothing reports it, so the
session id is unbindable on that pod forever with no signal" (staged §5.2 table row 7 plus the
`**Slot-identifier reclaim hold.**` paragraph's "A refusal is the only record the adapter makes of
the hold"). It is not a new finding: it is the standing `### Open` on a permanent hold, routed to
remediation position 2's entry reaper. Do not spend two verifiers on it.

### [spec.22.review-security.1]

DECISION: returned EMPTY. — BECAUSE a fresh, independent pass over the whole staged spec set
(SPEC-1 through SPEC-6) under both halves of the security lens found nothing meeting the bar,
which confirms the standing entry "The security lens's candidate space is EXHAUSTED across
rounds 15, 18 and 20". The delta since the lens last ran (round 18) is the round-20 two-term
split `1b12bba90`, prune 4 `a6abb507b` and the round-19/20 deletions, all of which are
terminology and commentary reductions with no control surface. — ALTERNATIVES: re-filing the
§4.9 timer-versus-credential-file ordering (rejected: it is the recorded Open
`[spec.18.review-security.1]`, routed to the pre-certification `### Open` sweep, and a re-file
costs two verifiers and closes nothing); filing the withheld cleanup-outcome report as a
relaxation of `recycle.maxSessionsPerPod` (rejected: standing refuted dress, and the report
source was already the adapter before this proposal); filing disposition row 7's
"Not entered, because nothing carries the outcome to the gateway" as a loss of degraded-state
surfacing (rejected: it records shipped `releaseSessionSlot`/`terminateHeldSession` behaviour,
the trap bars re-keying it, and the whole-pod scrub's step-6 absence re-verification retires a
pod whose residue survives).

USEFUL [The security lens's candidate space is EXHAUSTED across rounds 15, 18 and 20]: it named
the one live item up front, so the pass was spent re-deriving the control inventory against the
staged text rather than rediscovering refuted dresses. It held: nothing new surfaced.

FACT: the two code claims the §5.2 action-list replacement rests on both re-verify. `RemoveTree`
iterates `slotRoot, Sessions, Artifacts, CredentialsDir`, so the credential directory is one of
the four `os.RemoveAll` calls, and `deregisterSlotLocked` cancels every armed provider expiry
timer before `delete(s.slots, …)`. — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69;
pkg/adapter/slotsession.go:174-189

FACT: §4.6.3's ownership table row `| `Sandbox` | `status.*` | WarmPoolController | Sole writer
of phase and conditions; the occupancy phase is a level-triggered projection of `SandboxClaim`
state |` is the direct authority for SPEC-4's read-back parenthetical, and the Gateway
ServiceAccount paragraph states "The gateway holds no `sandboxes/status` grant". Checked once
more this round against the staged §4.6.1 enumeration edit; no actor violation. — EVIDENCE:
spec/04_system-components.md:632 and the §4.6.3 ownership table

FACT: SPEC-4 tightens rather than loosens the one-session-only control. The deleted closing
sentence permitted `claimed → idle` reuse on a recycling pool under its limits; the replacement
sends a claim deleted while the pod projects `claimed` to `draining, then terminated` "on a
pool of either recycle setting", leaving `reserved → idle` as the only reuse edge. A security
finding that SPEC-4 removes the control has the sign backwards; this is the third recording.
— EVIDENCE: spec-changes.md, SPEC-4 · §4.6.1 block, the `draining`, then `terminated` bullet

WATCHOUT: rule 12 reads "addressed to whatever entry the adapter holds", which scans as
pod-wide on a concurrent pod. It is not: the registry is keyed by session identifier, rule 11
scopes the cascade to "a session the adapter holds no entry for", and §4.1 fixes
`ShutdownRequest` as session-scoped with one address. Below the bar as wording; do not spend a
round dressing it as a cross-tenant teardown. — EVIDENCE: spec-changes.md, staged §4.7.1 rules
11 and 12; spec/04_system-components.md, §4.1 Request Message Scope

### [spec.22.review-single-source.1]

DECISION: returned EMPTY — BECAUSE the only change to `spec-changes.md` since the round-20
single-source sweep is commit `1b12bba90`, a five-hunk terminology split, and a split of ONE word
into TWO terms is by construction a single-source improvement rather than a new stating site: each
term is defined exactly once (`completed` in the SPEC-3 §5.2 `**Slot-identifier reclaim hold.**`
paragraph, bolded at its definition; `acknowledged clean` in the SPEC-2 §7.1 block) and every other
carrier uses the term without redefining it. I re-derived the home inventory against the live file
rather than trusting `[spec.19.review-single-source.1]`, re-ran the whole-file sentence-repeat
sweep, and read every staged block (SPEC-1 through SPEC-6) plus the Design and Edge-cases sections
end to end. — ALTERNATIVES: four candidates worked up and dropped, recorded below so round 23+ does
not re-derive them.

FACT: the whole-file (not fenced-block-only) sentence-repeat sweep at >=90 normalized characters,
run across `spec-changes.md`, `summary.md`, `non-spec-changes.md` and the checklist together, returns
six repeats and every one is benign: three are verbatim-anchor/replacement pairs inside
`spec-changes.md` (:266/:273, :745/:751, :1142/:1148), and three are spec-row/docs-mirror pairs
(spec-changes.md:273 vs non-spec-changes.md:2531; :751 vs :2549; :1136 vs :2603), which are DOCS-2's
one permitted reader-vocabulary restatement and whose remedy would in any case land outside this
loop. Nothing in the delta added a repeat. The script: split every line on `(?<=[.;])\s+`, normalize
whitespace, count occurrences. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:266,:273

FACT: the round-18 finding "the graceful-shutdown-signal condition is stated at three sites" is
CLOSED in the live staging and should not be refiled. The condition is stated once, in the staged
§4.7 `Shutdown` row ("it goes out only when the deregistration leaves the adapter holding no bound
entry"); the §29.4 step-13 sentence and the Edge-cases bullet both cite that row and state no
condition. No shipped spec section states a condition for the §15.4.2 signal either (`grep -in
graceful spec/15_external-api-surface.md spec/29_communication-scenarios.md spec/04_system-components.md`
has no competing statement), so the row is the single home in the applied spec as well. — EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:273, :337, :193; spec/15_external-api-surface.md:1702

WATCHOUT: four candidates that look like (g) on the post-`1b12bba90` file and are not. Do not
re-spend a round on them.
  (1) §7.1's `acknowledged clean` definition versus rule 15's clean-exit clause
  (spec-changes.md:377 vs :1040). Rule 15 is the home of what each OUTCOME means and of which
  outcomes report a clean exit; §7.1 is the home of the gateway-side predicate over the ANSWER.
  §7.1's trailing "so a reclaim answered `superseded` or `absent` is acknowledged clean" is a
  consequence drawn from rule 15, and the commentary at :380-386 marks it as the conforming-adapter
  reading while the predicate itself stays wide. Two different propositions.
  (2) The §5.2 disposition table's "Clean-exit flag" column versus rule 15. The column gives the
  flag only for the `reclaimed` rows, which is exactly what rule 15 delegates ("only a response
  reporting `reclaimed` can report an unclean exit, on the terms the §5.2 disposition table
  states"). A clean partition, not a copy.
  (3) The §16.1 `lenny_slot_compensation_superseded_total` row's gloss versus rule 15's `superseded`
  gloss (:1195 vs :1040). Already accepted twice: the standing-context trap enumerates rules 13/15,
  the §16.1 row and the docs mirror as the four spellings that move in one edit, and the row cites
  §4.7.1.
  (4) The Edge-cases bullet `**A bind-sequence refusal reaches the client under the envelope its
  stage already selects.**` (:203-217) versus SPEC-5's staged §15.1 `SETUP_COMMAND_FAILED`
  replacements (:1113-:1148). The overlap is partial and the bullet cites §15.1 for the code; its
  own content is the NON-setup-stage envelope, which §15.1 does not state. Below the bar, and the
  SPEC-5 commentary at :1155 deliberately points at the bullet for that half.

FACT: no staged spec block is a copy of shipped spec. Re-verified the two the delta touches:
`grep -rn "bind_attempt\|reclaim hold\|acknowledged clean\|unconditional_teardown" spec/ schemas/`
still returns nothing, so both terms are first statements in the applied corpus.

USEFUL [spec.19.review-single-source.1]: its eleven-home inventory plus its whole-file repeat sweep
are the two artifacts that let this round be a delta check with independent spot verification rather
than a rebuild. Fourth payoff; keep it.
USEFUL [spec.20.review-single-source.1]: its four live multi-site candidates (a)-(d) with the reason
each is barred meant the only new candidate work this round was the two the terminology split
created, both listed above.
USEFUL [redesign.5.fix.1]: its site-by-site list of what `1b12bba90` rewrote is exactly the delta a
single-source lens has to read, and it is accurate against the file.

### [index-reconciliation.1]

Deliverable-index and implementation-checklist reconciliation against the converged spec staging.
Scope: `summary.md`, `implementation-checklist.md`, `non-spec-changes.md` and this log. No staged
change was reopened and no decision was revisited. Every `DEFERRED` entry in the `### Deferred`
list above was read; each is closed by a `CORRECTS` line below or carried by an `OPEN` line.

INDEX: the deliverable index in `summary.md` now carries SPEC-1 through SPEC-6, SCHEMA-1, CODE-1
through CODE-9, CONF-1 and DOCS-1 through DOCS-4, each once. Three file lists were brought back
onto their deliverable headings: CODE-1 gains `pkg/adapter/slotsession.go`, CODE-4 gains
`bindattempt.go` and `pkg/gateway/sessionserver/upload_to_session.go`, and CODE-6 gains
`pkg/adapter/credentials.go`. No entry's text was otherwise rewritten.

CHECKLIST: the spec lane is the leading block S1 through S6, one lane per step, mapping S1 to
SPEC-5, S2 to SPEC-1, S3 to SPEC-2, S4 to SPEC-3, S5 to SPEC-4 and S6 to SPEC-6. S1, S2, S4 and S5
were rewritten against the current staging; S3 and S6 were already true against it and stand. DOCS-4
was staged and named by no step, so it lands as S23, appended rather than inserted beside S7 because
renumbering would invalidate every dependency line. Depends-on lines reconciled: S7 gains S4, S16
gains S15, S18 gains S1, S21 gains S15, S22 gains S15.

CORRECTS [DEFERRED implementation-checklist.md, S1]: S1 rewritten. It now states that §15.4's
reclaim-hold block keeps rule 2's `ABORTED` obligation and has its closing `Shutdown` sentence
reduced to a citation of §5.2 and §4.7.1, that §15.1's exclusion sentence is replaced and re-keyed
on the setup-command request naming `Aborted` among the excluded codes, that §6.2's client-visibility
clause becomes a pointer at that row, and that §15.4 states the conformance criterion and never
states the three non-conformances. The "all four spec steps" count is gone.

CORRECTS [DEFERRED implementation-checklist.md:17]: the same falsehood at its second site. The
sentence claiming §15.1's exclusion sentence stands unedited is gone from S1's rewrite.

CORRECTS [DEFERRED implementation-checklist.md:19, S2 final sentence]: replaced with the exact
sentence the entry names. S2 also gains the §4.7 `DemoteSDK` row, which it did not name at all.

CORRECTS [DEFERRED implementation-checklist.md, S4]: S4 rewritten. It now carries §12.6's write
re-key and read-clause reduction and the tier-11 assertion sweep those commission, and it states
that the `**Slot cleanup:**` bullet's leaked-outcome sentence is replaced outright by a pointer
rather than gaining a citation.

CORRECTS [DEFERRED implementation-checklist.md, S5]: S5 rewritten onto §4.6.1 as the re-keyed site,
with §6.2's fence trigger and its three claim-existence clauses reduced to pointers and no input
added to §6.2's own sentence.

CORRECTS [DEFERRED implementation-checklist.md, S7]: S7 now states DOCS-2's four edits, including
the `ReportSessionScrub` row and the bind-attempt paragraph, and DOCS-1's trigger and clause
replacements. Its Depends-on gains S4.

CORRECTS [DEFERRED implementation-checklist.md, S15, S16, S19, S22], in part: S16's reclaim-hold
release is now keyed on the cleanup having completed, and the Depends-on reconciliation above
settles the standing `### Open` question on whether S16, S18 and S21 owe S15 and S1. The edit-count
half is carried as an OPEN below.

CORRECTS [DEFERRED non-spec-changes.md, DOCS-3's retryable-fallback sentence]: the age claim is
gone. The staged sentence now reads "a request refused because the adapter's entry for the session
belongs to a different bind attempt".

CORRECTS [DEFERRED non-spec-changes.md, CODE-4 `Targets:`], in part: `upload_to_session.go` is now
a Targets bullet on CODE-4 and is named in checklist step S13, which is the step that mints and
carries. The `start_test.go` half is carried as an OPEN below.

CORRECTS [DEFERRED non-spec-changes.md, CODE-9's adapter-deferral sentence]: "carries the deferral
wording verbatim" becomes "records the same scrape deferral", which is what SPEC-6's row does.

CORRECTS [DEFERRED non-spec-changes.md, the resume-path test bullet]: the assertion moves off
`fakeAssigner.released`, which records `ReleaseSession` alone and discriminates nothing, onto the
attempt-scoped `Release(leaseID)` field the deliverable adds.

CORRECTS [DEFERRED non-spec-changes.md, logging and count wording]: all three. The convention
sentence now claims the event name and fields for every site this deliverable records a tree-removal
error at rather than every site it stops discarding one at, which is what leaves
`terminateHeldSession` outside it; `answerShutdown`'s "one return" becomes two, naming the
empty-session-identifier rejection beside the two-field precondition; and the residue sentence drops
its attribution of the occupancy-zero whole-pod scrub to the staged §5.2 text.

CORRECTS [DEFERRED non-spec-changes.md, `**The mid-session conditioning.**` and the CODE-6 rule-4
comment]: the Design paragraph's restatement of the carriage table is reduced to a citation of
§4.7.1's table, and the rule-4 comment attributes the sole-writer property to the stamp-once rule
instead of asserting it as rule 4's own.

CORRECTS [DEFERRED non-spec-changes.md, CONF-1 case "The reclaim hold against the `Shutdown`
cascade"]: already closed by an earlier pass. The case attributes the interaction to §5.2's
reclaim-hold paragraph. Verified at both the tier-3 and tier-10 sites; no edit made.

CORRECTS [DEFERRED non-spec-changes.md, the scrub-outcome comment count, and summary.md's
parallel]: already closed by an earlier pass. Neither file states a count on that surface, and both
cover the outcome sentences and the report-trigger sentences. Verified; no edit made.

CORRECTS [DEFERRED pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: already
discharged. The lagging pre-`Runtime.Start` rollback is a row under `## Defects in the shipped tree
that this proposal does not stage` in `summary.md`. Verified; no edit made, and not re-filed.

OPEN: the `Client.Shutdown` doc comment in `pkg/gateway/runtime/adapterclient/client.go` claims a
zero deadline lets the adapter apply its default grace period, which `resolveShutdownGrace`
contradicts. CODE-7 opens the file and stages no repair. Lands in that file, as a staged edit added
to CODE-7 in `non-spec-changes.md`.

OPEN: the `Binder.drain` doc comment in `pkg/gateway/podlifecycle/podsession/binder.go` states the
projection keying SPEC-4 retires. CODE-4 opens the file and stages no correction. Lands in that
file, as a staged edit added to CODE-4 in `non-spec-changes.md`.

OPEN: `pkg/sandbox/slotstate/slotstate.go` owes two doc-comment replacements CODE-3 does not stage
(the `Released` comment and the `slot_cleanup → released` gloss carry the retired three-action
effect list), and that file at `:104` plus
`pkg/gateway/runtime/slothealth/slothealth.go:33` carry the retired "cleanup timeout exceeded" gloss
for `slot_cleanup → leaked`. Lands in those two files, as staged edits added to CODE-3 in
`non-spec-changes.md`.

OPEN: `schemas/lenny-adapter.proto` owes two comment repairs SCHEMA-1 does not stage: the `Shutdown`
RPC comment predates the two-teardown split, and the `ErrorCode` enum header's claim that the
catalog mirrors §15.1 is false for codes 27, 28 and 29. Separately, the `GatewayControl` service
comment at `:255-257` says the service carries the scrub reports "the adapter emits on release",
which the minimal fix drops as four words rather than a carrier-table row. Lands in that file, as
staged edits added to SCHEMA-1 in `non-spec-changes.md`.

OPEN: `docs/operator-guide/troubleshooting.md`'s `setup_command_failed` row gains a second cause
under SPEC-5, a started-session refusal that ran no setup command, for which both listed remedies
are wrong. No docs deliverable opens the page. Lands in that file, as a new docs deliverable in
`non-spec-changes.md`.

OPEN: `docs/runbooks/gateway-replica-failure.md` calls a gateway crash benign for sessions, which
the crash-stranded registry entry contradicts. Lands in that file, as a new docs deliverable in
`non-spec-changes.md`.

OPEN: `docs/getting-started/architecture.md:237` enumerates the occupancy projection's inputs
without the phase the pod projects at the claim DELETE, which SPEC-4 adds to §4.6.1 and DOCS-1 adds
to the state-machine reference alone. The docs loop decides whether a getting-started page keeps the
enumeration at concept depth. Lands in that file, as a staged edit added to a docs deliverable in
`non-spec-changes.md`.

OPEN: `docs/reference/adapter-contract.md` mirrors only one of the two published §15.4 blocks. The
slot-identifier reclaim hold's `ABORTED` refusal is an adapter-author obligation of the same kind
and the page gives it neither a sentence nor a recorded exclusion. Lands in that file, as a staged
edit added to DOCS-2 in `non-spec-changes.md`.

OPEN: `docs/api/internal.md`'s gRPC status table omits `ABORTED` from its "When used" list. The page
is already wholesale drift and this proposal does not make it newly wrong, so it is a standing docs
defect rather than a missed edit site. Lands in that file, under a separate finding.

OPEN: the `Sandbox.status.phase` doc comment at `pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118`
carries the projection-input enumeration SPEC-4 extends, without the new input. It already omitted
"disposition" before this proposal, so it is recorded rather than filed. Lands in that file, with
`make generate` regenerating the two CRD YAMLs, never a hand edit to them.

OPEN: `spec-changes.md`'s commentary on the untokened-entry bullet calls the §10.1 hold-timeout
termination "a request" where §5.2 says it runs under no request. The spec lane could not edit its
own commentary at the point the correction was derived, and this pass may not edit that file. Lands
in `spec-changes.md`.

OPEN: CODE-6's `ConfigureWorkspace` idempotent-repeat arm reads the entry under a second `s.mu`
acquisition after the claim returned, which CODE-2's own comment rules out. The remedy is to have
`claimSessionSlotUnderLock` return what the caller needs on its `idempotentRepeat` arm, which is a
staged code change no non-spec lens has read. Lands in `non-spec-changes.md`, CODE-6.

OPEN: the tier-7a case list has a case for the stamp step's atomicity and none for rule 8's start
step, meaning a rule-14 reclaim removing the entry while the start confirms and records. The §10.1
ordering arm must park shorter than the guard-acquisition deadline. Authoring the case is not this
pass's to do. Lands in `non-spec-changes.md`, under `## Testing`.

OPEN: `pkg/gateway/sessionserver/start_test.go` is listed in CODE-4's test set with no edit stated
anywhere. Either the edit is stated or the file comes off the list. Lands in
`non-spec-changes.md`, CODE-4.

OPEN: checklist steps S15, S19 and S22 are untraced against the current staging, and S15 undercounts
its edits. S16's completion-predicate half is closed above. Lands in
`implementation-checklist.md`, on a pass that has read the code lane.
