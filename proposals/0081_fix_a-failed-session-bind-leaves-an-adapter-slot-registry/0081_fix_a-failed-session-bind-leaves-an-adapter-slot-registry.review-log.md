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
- **Does disposition-table row 3 withdraw a residual-state control without recording the withdrawal?** — OPEN, FILED [spec-recheck.1.review-security.1], lifted by `[redesign.6.fix.1]`: row 3 reports `released` with the clean-exit flag set when the runtime close succeeds and a later act (credential-directory or workspace removal) fails, and SPEC-3 deletes the shipped "If cleanup fails, the slot is leaked" universal, so that failure reaches no `leaked` count, no `lenny_adapter_leaked_slots` series and no drain. The tree already behaves this way (`pkg/adapter/session.go:272` discards the error; `sessionscrubreporter.go:39-44` keys on `closeErr`). Proposed remedy: a decision record, either a Decisions entry in the summary or a row under its unstaged-defects list, rather than a cell change, because the table's six columns are settled and the Traps entry on `errors.Join(closeErr, treeErr)` bars re-keying the report. Its UNVERIFIED companion (whether a leftover `credentials.json` on a concurrent pod is reachable by a later session through the shared runtime process) stands with it.

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

### [spec-recheck.1.fix-G1.1]

DECISION: The `running` boundary now sits at the adapter's RECORD of the session on the pod's shared runtime process, stated once in staged §6.2's `**Pre-`running` slot cleanup.**` paragraph; every other staged site uses the term `running` and cites §6.2 rather than restating a predicate. Rule 8 of staged §4.7.1 is unedited and its ground clause ("because the slot never reached `running`") becomes true. BECAUSE the tree holds the handoff and the record as two events (`pkg/adapter/session.go:156` `s.Runtime.Start`, then `:163` `s.noteRuntimeStarted`; identically `pkg/adapter/resume.go:140,144`), and only the record is durable state a later request reads under `s.mu`; the staged code lane already keys the report on `runtimeLive` membership (non-spec-changes.md CODE-1 rationale). ALTERNATIVES: keep the handoff boundary and reword rule 8 (rejected: it makes the adapter advance `sessions_served` for a session the pod never served, the double-count SPEC-3 exists to prevent, and contradicts the staged `runtimeLive` gate); add a qualifier to rule 8 or a per-window row to the §5.2 disposition table (rejected: the window is unobservable outside the adapter and the table is settled at six columns); add a sub-state for a start in flight (rejected: no consumer); edit only rule 8's ground clause (rejected: SPEC-3's biconditional and three table row keys carry the same retired phrase).

FACT: `noteRuntimeStarted` is the record, and its doc comment says so: `pkg/adapter/runtimegeneration.go:21-23` — "records that sessionID has been given to the pod's one shared runtime process. It runs immediately after a successful start." EVIDENCE: pkg/adapter/runtimegeneration.go:21

WATCHOUT: the shipped fence entry `spec/06_warm-pod-model.md:152-153` states `receiving_uploads ──→ running` as "session dispatched to runtime with its session identifier", which is the handoff. The proposal previously only INSERTED after that entry and never edited it, so it would have shipped a second, contradicting definition three lines above its home. SPEC-4 now carries a third fence replacement converting that entry to a pointer. EVIDENCE: spec/06_warm-pod-model.md:152

WATCHOUT: the same defect exists in the reader-facing mirror `docs/reference/state-machines.md:235`, which DOCS-1 previously only inserted after. DOCS-1 now replaces that row's Trigger cell in the page's own voice (a docs page restates rather than cites, per doc-content.md). EVIDENCE: docs/reference/state-machines.md:235

WATCHOUT: the phrase "given to the pod's shared runtime process" is retired as a BOUNDARY predicate throughout the staged text. It survives legitimately in two places and neither is a defect: staged rule 8's rollback clause ("takes the session back off the shared runtime process"), which names the handoff because the handoff is what is undone, and the summary's residue-class sentence at summary.md:299 ("in the class where the runtime was given it"), which names the same handoff class. Do not sweep those onto `running`.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S5 (SPEC-4) says "Two fence edits, two prose edits in §6.2 and one bullet pair in §4.6.1" and enumerates the fence gaining one edge. That is now false: SPEC-4's §6.2 fence work is the added `receiving_uploads → slot_cleanup` edge plus three annotation replacements, the `receiving_uploads ──→ running` entry among them, each replaced with a pointer. Step S7 (DOCS-1) enumerates the state-machine reference's edits and omits the `receiving_uploads` → `running` trigger-cell replacement DOCS-1 now carries.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md]: the accepted-failure-mode bullet "**A cleanup the adapter runs inside its own start handler reports its failure nowhere.**" states its window as "before the runtime was given the session". The window it describes (a `Runtime.Start` that returns an error, `pkg/adapter/session.go:156-158`) is pre-`running` under the fixed boundary too, so the bullet's disposition is unchanged, but its wording states the retired boundary term and should read "before the slot reaches `running`". The design for this round scoped it out, so it is not edited here.

OPEN: staged §4.7's `Shutdown` row says the teardown precondition is "taken at the moment of admission rather than at the moment the session reaches the runtime". That sentence contrasts admission with the handoff and is about the teardown precondition rather than the `running` boundary, so it was left as written. A later round should confirm a reader does not take "the moment the session reaches the runtime" as a third boundary term.

### [spec-recheck.1.fix-design-G1.1]

DECISION: the `running` boundary moves to the ADAPTER'S RECORD (`noteRuntimeStarted` / `runtimeLive`), and `running` becomes the one predicate word at every keying site in the staged spec text; "given to the pod's shared runtime process" is retired as a boundary phrase — BECAUSE the code lane already keys the report on that record (non-spec-changes.md:571 "only a slot that reached running is owed one, so the report reads `runtimeLive` membership while the teardown reads `st.started`"), and rule 8's ground ("the slot never reached `running`") is true only under it — ALTERNATIVES: keep the handoff boundary and reword rule 8 to owe a report (rejected: a `Shutdown` landing between `Runtime.Start` returning and `noteRuntimeStarted` would advance `sessions_served` for a session the pod never served, the exact double-count SPEC-3 exists to prevent, and it contradicts CODE-1/CODE-2); a per-window qualifier on rule 8 or an extra disposition row (rejected as hair — the window is unobservable to any other participant and the adapter branches on one predicate); a new sub-state between `receiving_uploads` and `running` (rejected: new state, no consumer).

FACT: the handoff and the record are two separate events in the tree, and only the second is durable state anything can read: `s.Runtime.Start(ctx, sessionID)` then `s.noteRuntimeStarted(sessionID)` — EVIDENCE: pkg/adapter/session.go:156,163; pkg/adapter/resume.go:140,144; pkg/adapter/runtimegeneration.go:21-23 ("records that sessionID has been given to the pod's one shared runtime process").

WATCHOUT: the phrase "given the session" is the boundary's carrier in NINE staged places, not the two the finding names: spec-changes.md:27, :32 (Design choice heading and its ground), :136 (Edge cases), :569 and :574 (SPEC-3 biconditional + commentary), :623-625 (three disposition-table row keys), :642 and :722 (SPEC-3 commentary), :935 (SPEC-4 fence-edge insert), :989 (§6.2 paragraph, three times in one paragraph), :1257 (checklist edit-site list), plus non-spec-changes.md:791 (CODE-3 doc comment), :2416 and :2539 (DOCS-1 row, DOCS-2 `Shutdown` row). Fixing :989 alone leaves eight sites asserting the retired boundary — EVIDENCE: grep -n "given" on the two proposal files.

FACT: the tree carries the retired boundary in exactly two places, both of which the proposal currently does NOT edit (it only inserts after them), so the fix must add two edit sites — EVIDENCE: spec/06_warm-pod-model.md:152; docs/reference/state-machines.md:235. No other hit exists in spec/, docs/, schemas/ or charts/ for "given the session" / "was given" / "runtime is given".

WATCHOUT: the staged §6.2 paragraph states the boundary THREE times in four sentences (definition, the "whose session has not yet reached the runtime" appositive, and "a session the runtime has already been given is a slot in `running`"). A fixer that swaps only the first sentence leaves the paragraph self-contradicting — EVIDENCE: spec-changes.md:989.

DECISION: spec/06:152's trigger becomes a pointer at the paragraph below rather than a reworded trigger — BECAUSE the SPEC-4 block's own rationale already argues that a trigger short enough for a fence entry either restates the owning paragraph or excludes a traversal, and it converts two neighbouring entries to pointers on that ground; a reworded trigger would be a second definition of the boundary three lines above its home — ALTERNATIVES: "(workspace ready, the adapter records the runtime as holding the session)" (rejected: restatement the single-source lens would file next round).

OPEN: docs/reference/state-machines.md:235 cannot cite a spec heading (doc-content.md), so its row must restate the boundary in reader terms. That is the one licensed restatement; do not "fix" it later by turning it into a citation.

### [spec-recheck.1.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens on the spec
staging — BECAUSE the whole spec-lane delta since round 22 is ONE added commentary paragraph
(spec-changes.md:664-673, the ten-second graceful-window provenance note), it stages no edit, mints
no artifact, creates no forward reference and moves no anchor, and a full re-run of the three
mechanical sweeps over the rest of the staging is still clean. ALTERNATIVES: filing the new
paragraph as rationale the caller's prune directive bars from SPEC-n commentary — rejected, it is
not a restatement of a staged rule and "commentary bloat" is not one of my finding classes; filing
the reclaim-hold sentence's three-case close-bound enumeration as omitting the SDK demotion and the
failed-start handler's own release — rejected as speculative mechanism territory that twelve empty
lenses at round 22 already read.

FACT: the delta paragraph's four checkable claims all hold. `pkg/adapter/holdstate.go:201` is
exactly `closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`, inside
`onHoldTimeout` (:177); `git log -1 3997f502b` is "Terminate every started session when the
coordinator hold times out", dated 2026-08-22; CODE-6 does keep ten seconds while re-scoping the
context from the pass to one per member (non-spec-changes.md:3778-3780, :1396-1398); and
`.claude/rules/code-best-practices.md`'s override rule is indeed conditioned on "A default not fixed
by the spec". This is the SEVENTH file:line citation in spec-changes.md; an older Settled line says
there are six.

FACT: the three mechanical sweeps re-run clean this round and cost about four minutes together.
(1) Fenced-block sweep: `re.findall(r'\n```\n(.*?)\n```\n')` over spec-changes.md yields 64 blocks;
every "reads, verbatim" block occurs exactly once across `glob('spec/*.md')` and every replacement
block occurs zero times. (2) Anchor sweep: every `](file#slug)` and same-file `#slug` in the staging
resolves against a slugged heading of `spec/*.md`; zero misses. (3) Insertion-point sweep: §4.7.1's
point (`*Adapter → Gateway RPCs:*` spec/04:688, `#### 4.7.2` :695), §15.4's (`**SDK-warm demotion
contract:**` spec/15:1469, `#### 15.4.1` :1471), §7.1's (atomicity paragraph spec/07:23, continuation
:24) and §16.1's catalog row shape (two columns, spec/16:14) all stand where the staging says.

FACT: the §5.2 sentence-position claims in the staged text are true of the shipped file, and that is
worth not re-deriving: `**Scrub model.**` is spec/05:453, `**Session count limit:**` :488,
`**Slot cleanup:**` :545 (all three SPEC-3 anchors on one physical line) and
`**Whole-pod replacement trigger:**` :561. So the appended block's "the **Slot cleanup:** bullet
below" and the bullet's "the **Scrub model.** paragraph above" are both correct, and the
whole-pod-trigger parenthetical's "the disposition table above" is too.

FACT: §6.2's `**`leaked` slot semantics.**` paragraph (spec/06:160) states what a leaked slot holds
and counts toward and states NO trigger, so SPEC-3's withdrawal of the cleanup-timeout predicate
strands nothing there. The proposal's claim to that effect is accurate and needs no re-check.

USEFUL [Traps, "MISTAKE nearly filed in FOUR separate rounds: the SPEC-4 §6.2 fence edit's anchor
form"]: I reproduced the zero-hit on that block mechanically and would have spent a verification on
it. The trap is right: the `Occupancy projection` fence at spec/06:95-97 holds exactly one
`claimed ──→ draining` entry, while the file holds five in total (:95, :114, :135, :137, :139), so
the edit is determinable only through the named group. USEFUL [archive, the three refutations of the
§15.1 "Surfaced wherever the gateway runs setup commands" sentence]: killed my only other candidate
before I spent anything on it.

OPEN: the caller's round-0 prune directive says to add no rationale sentence to any SPEC-n
commentary, and the only spec-lane change since round 22 is exactly such a paragraph
(spec-changes.md:664-673). It is factually correct and I did not file it. Whoever runs the next
prune should decide whether the provenance of the ten-second figure belongs in SPEC-3's commentary
at all, or whether the figure standing in the staged §5.2 sentence is the whole statement.

### [spec-recheck.1.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens on spec round 23 — BECAUSE every
citation in the staging verified against the tree, including the whole delta — ALTERNATIVES:
considered filing the SPEC-3 carrier table for omitting `docs/reference/adapter-contract.md`'s
`Shutdown` row (line 75: "reports the per-slot cleanup outcome through `ReportSessionScrub`", a
carrier of the withdrawn universal) while declaring itself "the single home of their dispositions";
rejected because the row IS in an edit list — the `Spec files touched` closing paragraph assigns it
to DOCS-2 — so (d) does not hold and the remedy would ADD a table row, against the caller directive.

FACT: the whole spec staging contains exactly ONE file:line code citation,
`pkg/adapter/holdstate.go:201`, and it is the delta. EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:666. A future citation lens can extract them with
`grep -on "pkg/[A-Za-z0-9_/.-]*\.go:[0-9]*" <spec-changes>` and be done in one command.

FACT: all 25 `verbatim:` fenced quotes in the staging were machine-checked against `spec/*.md` and
`schemas/*` with whitespace normalised, and all 25 matched. EVIDENCE: the script form is in this
round's transcript; re-run it rather than eyeballing quotes. All 21 distinct markdown anchors in the
staging resolve to real headings in spec/04, 05, 06, 07, 10, 12, 15.

FACT: the delta paragraph's three claims all hold. `onHoldTimeout`'s pass-2 context is
`context.WithTimeout(context.Background(), 10*time.Second)` at pkg/adapter/holdstate.go:201 (function
declared at :177); commit `3997f502b` is dated 2026-08-22 with subject "Terminate every started
session when the coordinator hold times out"; CODE-6 does keep ten seconds while splitting the pass
context into a guard-acquisition deadline plus a per-member close context
(non-spec-changes.md:3778-3786). The `.claude/rules/code-best-practices.md` override rule is indeed
conditioned on "A default not fixed by the spec".

WATCHOUT: the staging says the ABSENT harness row follows "the rows the `coordination_generation`
fence already carries" (spec-changes.md ~:1099 commentary). In `tests/claim-map.json` the
per-request `coordination_generation` rows are status `UNWIRED`, not `ABSENT`; the fence's ABSENT
rows are the different claims "In-flight RPC cancellation on a generation gap" and "Quiesce
enforcement against operational RPCs" (both deferral R16). The sentence reads as a precedent for
"the fence carries claim-register rows", which is true, so it is not a false citation — but a later
lens that reads it as "those rows are ABSENT" will chase it. It has now been chased; do not refile.

USEFUL [Settled: "The §5.2 'graceful window of ten seconds' … is the shipped constant in
`onHoldTimeout`, one context shared across pass 2; §10.1 states no figure. CODE-6 re-scopes it"]:
this standing line is exactly what the delta paragraph records, so verifying the delta cost one
`sed` and one `git log` instead of a re-derivation.

### [spec-recheck.1.review-client-surface.1]

DECISION: returned EMPTY under the client-facing-surface lens — BECAUSE every client-facing spec surface the staging touches is complete and its parallels are accounted for; the delta since spec-r23 is one commentary paragraph in `.spec-changes.md` (the 10s graceful window / `pkg/adapter/holdstate.go:201` note) with no client-facing content at all — ALTERNATIVES: considered filing (i) the §15.1 retryability sentence's "another start already holds the slot identifier" as imprecise for the same-attempt arm of rule 6, and (ii) §15.4.2's unconditional `DRAINING` row vs the new co-tenant gate on the graceful-shutdown signal; both are word-level/pre-existing-generality and neither makes the applied spec wrong, so both were dropped under the bar.

FACT: the whole delta this recheck exists for is one added paragraph — EVIDENCE: `diff -u scratchpad/cp-snap/0081-opt2/spec-r23/*.spec-changes.md proposals/0081_*/*.spec-changes.md` returns a single `+` hunk after the reclaim-hold commentary (proposal `.spec-changes.md` around line 664).

FACT: every verbatim anchor SPEC-1/SPEC-5 quotes still matches the tree byte for byte — EVIDENCE: `spec/04_system-components.md:157` (§4.1 third sentence), `:686` (§4.7 `Shutdown` row opening), `:674` (`DemoteSDK` row), `spec/15_external-api-surface.md:1136` (all four `SETUP_COMMAND_FAILED` sentences), `:1469` (`**SDK-warm demotion contract:**`), `:1471` (`#### 15.4.1`), `spec/29_communication-scenarios.md:711` (step 13 tail `([§15.4.3](...), §28.5.3).`).

FACT: the §4.7.1 carriage table's eight RPC names and rule 9's "PrepareWorkspace is client-streaming" are exact against the proto — EVIDENCE: `schemas/lenny-adapter.proto:41` is the only `stream`-taking RPC among the eight (`:41,48,55,62,72,89,151,206`); `spec/04_system-components.md:662-686` carries every name.

FACT: the "no new §15.1 row" precedent holds. `PROTOCOL_VERSION_INCOMPATIBLE` really has no §15.1 catalog row; its only spec mention is the §15.4.2 `INIT` row — EVIDENCE: `spec/15_external-api-surface.md:1699` is the sole hit for that string in `spec/`, while `schemas/lenny-adapter.proto:585` carries `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27`.

WATCHOUT: `schemas/lenny-adapter.proto:556` comments the `ErrorCode` enum as "The catalog below mirrors spec §15.1." Code 27 already breaks that, and the two new `SLOT_BIND_*` codes break it further. Do not file it in the spec lane: the remedy is a `schemas/` comment edit and SCHEMA-1 owns it — EVIDENCE: `schemas/lenny-adapter.proto:554-586`.

FACT: the §15.1 widening's parallel client surfaces are already staged in the non-spec lane, so they are not spec-lane edit-site gaps — EVIDENCE: `docs/reference/error-catalog.md:129` is covered by DOCS-3 (`*.non-spec-changes.md:2571`), `docs/reference/adapter-contract.md` by DOCS-2 (`:2501`).

FACT: the §15.1 `SETUP_COMMAND_FAILED` mapping is restated at five more spec sites, and none of them becomes false under the widening, because each states the setup-command exit as a sufficient cause rather than an exhaustive one — EVIDENCE: `spec/15_external-api-surface.md:625,646,647,650,720` and `spec/07_session-lifecycle.md:208`. The one site that did become false, `spec/06_warm-pod-model.md:290`, is the one SPEC-5 replaces. A later round should not re-derive this sweep.

### [spec-recheck.1.review-docs-alignment.1]
DECISION: empty findings list — BECAUSE the only spec-lane delta since spec-r23 is one added rationale paragraph in SPEC-3 (`0081_...spec-changes.md:664-674`) recording the ten-second graceful window as a shipped figure, and both of its citations verify — ALTERNATIVES: filing the missing `docs/reference/metrics.md` / runbook companions for SPEC-6's two new counters, and the missing narrative-operator-doc cause for the orphaned-tokened-entry failure mode; both rejected because their only remedy is a docs edit, which this spec-lane loop may not land.
FACT: the delta paragraph's two citations are exact. `pkg/adapter/holdstate.go:201` is `closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`, and `git log -1 3997f502b` gives `2026-08-22 Terminate every started session when the coordinator hold times out`. — EVIDENCE: pkg/adapter/holdstate.go:201
FACT: §10.1 states no numeric grace for the hold-timeout termination, only "initiates graceful session termination", so the staged §5.2 ten-second sentence is a first statement rather than a second copy of one. — EVIDENCE: spec/10_gateway-internals.md:58
USEFUL [standing context, "The §5.2 'graceful window of ten seconds' … is the shipped constant"]: it named the figure, the file and CODE-6's re-scoping before I read the delta, so verifying the new paragraph cost two commands instead of a hunt through pkg/adapter.
WATCHOUT: under the spec-lane scope this lens is nearly inert. Every classic docs-alignment remedy (a metrics.md row, a runbook cause, a DOCS-n mirror) lands in the non-spec staging, so the only in-scope class left is an accepted failure mode whose observable outcome lands in NO staged spec text. I walked the nine Edge-cases bullets against the staged blocks and each resolves to staged §4.7.1 rules 1-15, the §5.2 disposition table, or the §5.2 residue paragraph. — EVIDENCE: 0081_..._.spec-changes.md:70-216

### [spec-recheck.1.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens on this recheck — BECAUSE the whole delta since the `spec-r23` snapshot inside `.spec-changes.md` is a single new commentary paragraph (:664-:674) recording the provenance of the ten-second graceful window the §5.2 reclaim-hold paragraph fixes; it adds no identifier, no field, no metric, no flag, no yaml key and no anchor, so it opens no new edit site. Re-swept the staging's full identifier set (`bind_attempt`, `unconditional_teardown`, `SLOT_BIND_ATTEMPT_SUPERSEDED`, `SLOT_BIND_ALREADY_STARTED`, `lenny_slot_compensation_superseded_total`, `lenny_slot_shutdown_untokened_entry_total`) across `spec/`, `docs/`, `schemas/` and `charts/`: zero pre-existing hits for any of them, so no surface collides or goes stale. ALTERNATIVES rejected: (a) a §11.3 timeouts-table row for the new ten-second figure, see the FACT below; (b) the new §16.1 adapter row's deferral clause not matching the four shipped adapter rows' wording, which is phrasing rather than a wrong surface.

FACT: the new paragraph's three tree claims all check out. `context.WithTimeout(context.Background(), 10*time.Second)` is at `pkg/adapter/holdstate.go:201` inside `onHoldTimeout`'s pass 2 — EVIDENCE: pkg/adapter/holdstate.go:201. Commit `3997f502b` is "Terminate every started session when the coordinator hold times out", dated 2026-08-22 — EVIDENCE: `git log -1 3997f502b`. The staged §5.2 sentence that names the window is at `.spec-changes.md:635`.

FACT: `spec/11_policy-and-controls.md:200-225` (§11.3 Timeouts and Cancellation) carries hard-coded, non-configurable figures too (`| CoordinatorFence RPC timeout | 5s | (hard-coded; not configurable in v1) | No |`, spec/11:216), so "not configurable" is not why the new ten-second window is absent from it. I still judged an added row below the bar: the table's preamble makes no exhaustiveness claim and the table does not become WRONG, only shorter than it could be. A later edit-site lens that rediscovers §11.3 should stop here rather than file it. EVIDENCE: spec/11_policy-and-controls.md:199-225.

FACT: the ten-second figure does not contradict any sibling spec figure. `spec/11_policy-and-controls.md:264` already states "SIGTERM to agent, wait up to 10s, then SIGKILL" for the §11.4 revoke `Shutdown` path, and `spec/10_gateway-internals.md:58` (the §10.1 hold-timeout termination) states no close window at all, so the staged sentence fills a gap rather than overriding a stated figure. EVIDENCE: spec/11_policy-and-controls.md:264; spec/10_gateway-internals.md:58.

FACT: `ShutdownRequest` carries `deadline_ms = 3` and no separate grace field — EVIDENCE: schemas/lenny-adapter.proto:1612. The §5.2 sentence's "the graceful window the reclaiming `Shutdown` carries when it carries one, by that request's own deadline when it carries none" reads onto `deadline_ms` set vs. unset plus the gRPC context deadline. I did not file it: it is a mechanism reading, not an edit site, and no carrier goes stale either way.

USEFUL [spec.22.review-edit-sites.1]: its ALTERNATIVES list (the `spec/07:72` `scrubPolicy` act enumeration as pre-existing divergence, and the §15.1 endpoint-precondition rows as sufficient-rather-than-exhaustive) killed two candidates before I spent a sweep on either.

### [spec-recheck.1.review-fresh.1]

DECISION: returned an EMPTY findings list — BECAUSE the only spec-lane delta since r23 is one
commentary paragraph in SPEC-3 (the ten-second graceful-window provenance, spec-changes.md:664-674),
every factual claim in it verifies against the tree, and a full re-sweep of the other staged blocks
turned up nothing at the bar — ALTERNATIVES: I considered filing the new paragraph under (g) as
commentary restating the staged three-case close-bound predicate ("the other two take the reclaiming
`Shutdown`'s carried grace or that request's own deadline"). Rejected: (g) exempts a site that
"summarises in a clause, or gives the rationale for a rule", and the paragraph's whole subject is the
figure's provenance. It is a summary clause, not a second full statement.

FACT: the delta paragraph's four checkable claims all hold. `context.WithTimeout(context.Background(),
10*time.Second)` is at exactly pkg/adapter/holdstate.go:201 (the line, not the comment above it);
commit 3997f502b is dated 2026-08-22 with subject "Terminate every started session when the
coordinator hold times out"; CODE-6 re-scopes the pass-shared context to a per-member one
(non-spec-changes.md:1395-1396, 3778-3786); `.claude/rules/code-best-practices.md` conditions the
override rule on "A default not fixed by the spec". — EVIDENCE: pkg/adapter/holdstate.go:201

FACT: every `reads, verbatim:` anchor in the staging still matches the tree exactly. Re-verified all
of them this round: spec/04:157 (§4.1), :674 (DemoteSDK), :686 (Shutdown), :692
(ReportSessionScrub), :854 (§4.7.9 step 5), :409/:415/:416 (§4.6.1); spec/05:545 (Slot cleanup
bullet, all three anchored sentences on one physical line), :561 (replacement trigger);
spec/06:80 (projection prose), :95, :148, :152, :155 (fence entries), :290 (client visibility);
spec/07:23 (atomicity paragraph, with the `(executionMode, isolationProfile, scrubPolicy summary)`
continuation line immediately after it at :24), :210, :213, :214, :414; spec/12:481, :494;
spec/15:1136 (SETUP_COMMAND_FAILED), :1469 (SDK-warm demotion contract), :1471 (§15.4.1 heading);
spec/29:711 (§29.4 step 13). No anchor has drifted.

FACT: the staged §4.7 `Shutdown` row's graceful-shutdown-signal condition is exactly what the tree
runs. `Server.Shutdown` computes `boundRemains` inside the same `s.mu` section as
`deregisterSlotLocked` and calls `drainViaLifecycle` only under `if !boundRemains`, before
`Runtime.Close`; `drainViaLifecycle` sends `Lifecycle.Terminate(deadlineMs, drainReason(reason))`,
which is the CH-RUNTIMEOPS `terminate` frame §29.4 step 13 traces. So the staged §29.4 sentence
attaches the right condition to the right frame. — EVIDENCE: pkg/adapter/session.go:238-259, 299-307

FACT: `mid_session` exists today ONLY on `FinalizeWorkspaceRequest` (schemas/lenny-adapter.proto:724);
`PrepareWorkspaceRequest` has none. The standing trap "Only `PrepareWorkspaceRequest` gains the field"
is therefore consistent with the §4.7.1 carriage table listing both as carrying it — the table states
the post-edit state. A lens reading the trap as "the table is wrong about FinalizeWorkspace" is
misreading it. — EVIDENCE: schemas/lenny-adapter.proto:706-724

FACT: `PrepareWorkspace` is the only client-streaming RPC on the contract
(`rpc PrepareWorkspace(stream PrepareWorkspaceRequest)`, schemas/lenny-adapter.proto:41), so rule 9's
scoping to it is exhaustive. — EVIDENCE: schemas/lenny-adapter.proto:41-215

FACT: the two shipped-behaviour claims under the §5.2 action-list replacement both hold.
`slotlayout.RemoveTree` removes `p.CredentialsDir` among its four directories and returns the first
error only; `deregisterSlotLocked` cancels every armed expiry timer before deleting the map entry.
— EVIDENCE: pkg/adapter/slotlayout/tree.go:58-70; pkg/adapter/slotsession.go:174-189

FACT: every cross-file anchor the staged blocks use resolves to a real heading (checked 4.6.1, 4.6.3,
4.7, 4.7.1, 4.7.9, 4.9, 5.2, 6.1, 6.2, 7.1, 7.2, 7.3, 7.4, 10.1, 15.1, 15.4, 15.4.2, 15.4.3). No
staged link is dangling.

FACT: §5.2's `**Session count limit:**` bullet (spec/05:488) states the evaluation point for both a
single-session pool and a concurrent non-`vm-restart` pool, so SPEC-3's two §12.6 read-clause
citations of it resolve to a bullet that actually answers them. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488

FACT: docs mirrors of the edited spec surfaces are all claimed by a deliverable. `sessions_served`
appears in no `docs/` page (only in schemas/lenny-adapter.proto:314, 453, 471, which SCHEMA-1 owns);
`docs/reference/glossary.md:371` mentions leaked slots but states no trigger, so it does not become
wrong. state-machines.md:236-237, 247-248, 251 is DOCS-1's; adapter-contract.md:64, 75, 81 is DOCS-2's.
I found no unclaimed surface.

### [spec-recheck.1.review-kubernetes.1]

DECISION: returned EMPTY for the Kubernetes-idiom lens on the spec-recheck.1 staging — BECAUSE the
whole delta since the r23 snapshot is a single commentary paragraph in the SPEC-3 §5.2 block
justifying the ten-second graceful window (spec-changes.md:664-674), which touches no Kubernetes
surface at all: no CRD field, no status subresource, no finalizer, no admission webhook, no
controller work-queue, no watch. I re-read the whole staging section by section anyway; the only
K8s-bearing blocks remain SPEC-4 (§4.6.1 / §6.2 occupancy projection) and the §4.7
`ReportSessionScrub` row's `lenny.dev/drain-request` pointer, both of which are unchanged from
round 22 and both of which re-verify clean against the tree — ALTERNATIVES: filing the SPEC-4
parenthetical "the controller's own last level, which it may read back because it is the sole
writer of that field" as a two-manager / read-back hazard, rejected on two independent grounds
re-derived this pass (see FACTs below); filing the retirement clause's dependence on the pod
projecting `claimed` at the claim DELETE, rejected because it is already carried as a pre-existing
UNVERIFIED whose remedy is code (review-log.md:784) and so is out of this loop's scope.

FACT: the sole-writer claim SPEC-4 stages is exact at the field-manager level, and the delta did not
disturb it. Both arms of the WarmPoolController that write `Sandbox.status.phase` — the occupancy
projection and the Sandbox-to-Pod reconciler — patch under the single
`ownership.WarmPoolController` field manager, and the gateway writes no `Sandbox.status`. There is
no `ForceOwnership` anywhere in the package and no second manager on the field.
EVIDENCE: pkg/controller/warmpool/occupancy.go:268, pkg/controller/warmpool/pod_reconciler.go:691,
pkg/controller/warmpool/controller.go:752,852, pkg/controller/warmpool/gc.go:466,
pkg/controller/warmpool/occupancy.go:150-158, spec/04_system-components.md:409.

FACT: the delta's one factual citation re-verifies. `onHoldTimeout`'s pass-2 close context is
`context.WithTimeout(context.Background(), 10*time.Second)` at exactly the cited line, and commit
`3997f502b` is "Terminate every started session when the coordinator hold times out".
EVIDENCE: pkg/adapter/holdstate.go:201.

USEFUL [spec.22.review-kubernetes.1]: its statement that the K8s candidate space on this staging is
exhausted held; this pass found nothing it had not already ruled on.
USEFUL [review-log.md:260]: the standing line that `OccupancyReconciler` does `For(&Sandbox{})` and
maps claim events through `claimToSandbox` is what closes the read-back convergence question
without re-deriving it; still true at occupancy.go:281-291,297-300.

### [spec-recheck.1.review-mechanism.1]

DECISION: filed exactly one finding, the `running`-boundary contradiction between staged §6.2's
`**Pre-`running` slot cleanup.**` paragraph (`spec-changes.md:989`) and staged §4.7.1 rule 8
(`spec-changes.md:1039`) — BECAUSE §6.2 keys the boundary on the HANDOFF ("the pod's shared runtime
process has been given the session") while rule 8 asserts, of a window it reaches only after that
handoff (it "takes the session back off the shared runtime process"), that "the slot never reached
`running`". ALTERNATIVES rejected: (a) filing the new 10s-provenance commentary paragraph
(`spec-changes.md:664-674`) under bar (g) as commentary restating the three-case bound — the caller
directive bars adding rationale to SPEC-n commentary, but the paragraph is provenance for a constant
rather than a second rule home, and the caller's prune rounds predate it; (b) filing the
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` bullet timeout (`spec/05:545`) against the
reclaim-hold paragraph's new close-bound sentence as a double bound — the two bound nested scopes
(whole cleanup vs. the runtime close alone), so it is not a contradiction.

FACT: the delta this recheck exists for is ONE added paragraph, the 10s-window provenance block in
SPEC-3 commentary. `diff -u` of spec-changes.md against the spec-r23 snapshot returns a single hunk
at `spec-changes.md:664-674`. Every claim in it verifies: `context.WithTimeout(context.Background(),
10*time.Second)` is at `pkg/adapter/holdstate.go:201`, and `git show -s 3997f502b` is
`2026-08-22 ... Terminate every started session when the coordinator hold times out`.

FACT: the give/record split in the tree is TWO statements, not one, and the window between them is
real. `pkg/adapter/session.go:156` is `s.Runtime.Start(ctx, sessionID)` and
`pkg/adapter/session.go:163` is `s.noteRuntimeStarted(sessionID)`; `resume.go:140`/`:144` are the same
pair. `pkg/adapter/runtimegeneration.go:21-23` spells the distinction out: noteRuntimeStarted
"records that sessionID **has been given** to the pod's one shared runtime process. It runs
immediately after a successful start." So the handoff is `Runtime.Start`'s return and the record is
`runtimeLive`. `st.started` is a THIRD flag, set in `claimSessionSlot` BEFORE `Runtime.Start`, and it
is what the staged §4.7 `Shutdown` row's runtime-teardown precondition ("a session whose start the
adapter has admitted") keys on. Three flags, three predicates; do not collapse any two.

FACT: the shipped `Shutdown` handler already implements the staged graceful-shutdown-signal condition.
`pkg/adapter/session.go:259-261` gates `drainViaLifecycle` on `!boundRemains`, with a comment that is
almost word for word the staged §4.7 row's clause. The staged row records shipped behaviour there, so
a lens should not file the pre-binding co-tenant (an entry created but not credential-bound is not
`bound`, so the signal can go out under it) as new: it is pre-existing and the Edge-cases bullet
`**The graceful-shutdown signal on a co-tenanted pod.**` records the class.

FACT: `ConfigureWorkspace` is issued from `Launch`, at `pkg/gateway/podlifecycle/podsession/binder.go:1009`,
i.e. AFTER the tokened Prepare/Finalize/RunSetup/AssignCredentials sequence. So the untokened-entry
residue rule 13 records is not reached on the ordinary SDK-warm bind: PrepareWorkspace creates the
tokened entry first and ConfigureWorkspace only resolves it. I spent time on "does every SDK-warm
bind produce an untokened entry, making every compensating reclaim answer `superseded`?" — it does
not. Do not re-derive.

WATCHOUT: the §4.7 `Shutdown` row's own wording is the cleanest evidence that the handoff and the
admission are distinct moments — "taken at the moment of admission rather than at the moment the
session reaches the runtime, so a start still in flight is torn down rather than skipped"
(`spec-changes.md:273`). Any fix to the `running` boundary has to leave that clause true.
EVIDENCE: proposals/0081_*/...spec-changes.md:273

USEFUL [DECISION: the refused `StartSession` reports no outcome]: the standing-context line at
review-log.md:163 and the `[f1.human-decisions]` entry at review-log.md:1611-1626 both settle that
rule 8's refusal files NO cleanup-outcome report. My finding does not disturb that disposition; it
says the GROUND rule 8 gives for it ("because the slot never reached `running`") is the sentence
that is false against staged §6.2, and the fix is to that ground clause or to §6.2's boundary phrase,
not to the no-report answer.

### [spec-recheck.1.review-performance.1]
FACT: The delta in this recheck is one added commentary paragraph in the spec staging (the ten-second-window provenance paragraph). Its two verifiable claims check out: `context.WithTimeout(context.Background(), 10*time.Second)` is at pkg/adapter/holdstate.go:201 and commit 3997f502b is "Terminate every started session when the coordinator hold times out". — EVIDENCE: pkg/adapter/holdstate.go:201
FACT: §5.2's per-slot cleanup timeout `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` lives in the `**Slot cleanup:**` bullet under the heading `**Slot failure and cleanup (maxConcurrentSessions > 1).**`, and the CRD floor (`cleanupTimeoutSeconds >= maxConcurrentSessions x 5`) permits exactly 5s per slot. The staged reclaim-hold paragraph fixes the hold-timeout close at ten seconds, which exceeds that floor. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545
FACT: The per-slot cleanup timeout formula is spec-only today. `CleanupTimeout` in the tree bounds only the whole-pod scrub's `cleanupCommands` phase, not any per-slot cleanup. — EVIDENCE: pkg/adapter/scrub/scrub.go:128-130, pkg/adapter/podscrub.go:128
FACT: The non-spec staging already derives the compensating `Shutdown`'s graceful window from that same formula ("pins half of it as the adapter's graceful window"), so the Shutdown arm of the staged sentence is consistent; only the ten-second hold-timeout arm is not. — EVIDENCE: 0081...non-spec-changes.md:880-889
DECISION: I did NOT file the gateway-crash-strands-an-entry case, the N x 10s serialization of the §10.1 pass, or disk residue accumulation. BECAUSE the first is an explicitly accepted failure mode with two named out-of-scope recoveries; the second is bounded in practice (the tree comment at pkg/adapter/holdstate.go:196-200 records that a non-last close returns without touching the child, so at most one close consumes grace) and per-member scoping is a reliability improvement; the third is swept by the whole-pod scrub on a recycling pod and by pod termination otherwise. ALTERNATIVES: filing them would re-litigate settled design.
FACT: No new etcd status write, CRD watch, informer cache, Redis key or Postgres row is created by this staging. `sessions_served` writes strictly DECREASE (the report is now conditioned on a `Shutdown` reclaiming a runtime-given slot), and both new §16.1 counters reuse the established `pool` / `k8s_pod_name` label pair that the neighbouring slot metrics already carry. — EVIDENCE: spec/16_observability.md:14-15, :297

### [spec-recheck.1.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability lens on spec-recheck round 1 — BECAUSE the whole delta in `spec-changes.md` since the r23 snapshot is one commentary paragraph (the ten-second graceful window provenance, now at spec-changes.md:664-673), every citation in it verifies, and a full re-sweep of the staged blocks under crash / restart / redelivery turned up nothing the staging does not already own or record as an accepted failure mode — ALTERNATIVES: filing the "hold held for the life of the pod has no reclaimer" and "gateway crash between abandon and reclaim strands a tokened entry" dresses; both are refuted by the staging itself (the §5.2 disposition table's hold column plus the residue paragraph for the first, the Edge-cases bullet at spec-changes.md:177-188 for the second, which names the two out-of-scope recoveries).

FACT: the new commentary's three citations all hold. `context.WithTimeout(context.Background(), 10*time.Second)` is at `pkg/adapter/holdstate.go:201` exactly; `git show -s 3997f502b` is "Terminate every started session when the coordinator hold times out", dated 2026-08-22; the shared-context rationale is in the code comment at `pkg/adapter/holdstate.go:196-200`, and CODE-6's re-scope to a per-member context is staged at `non-spec-changes.md:2817-2824`. — EVIDENCE: pkg/adapter/holdstate.go:196-201

FACT: the staged reclaim-hold sentence bounds the close in exactly three cases, and three is the complete set, not an omission. The SDK demotion closes BEFORE it deregisters, so it opens no hold (its own disposition-table row says so), and `releaseSessionSlot`, the failed-start performer, closes no runtime at all, so neither needs a bound. — EVIDENCE: spec-changes.md:630, spec-changes.md:635

USEFUL [standing 97]: "The §5.2 'graceful window of ten seconds' … is the shipped constant in `onHoldTimeout` … CODE-6 re-scopes it" saved the whole re-derivation of the delta paragraph; the added commentary is that standing line written into the proposal.

USEFUL [standing 259, 260]: the at-most-once `ReportSessionScrub` and the replay-idempotent compensating `Shutdown` (rule 11 answers `absent`, rule 15 makes it a clean exit) close the two dresses this lens would otherwise have to re-derive against the §12.6 `sessions_served` re-key. Do not re-derive them.

FACT: the §4.9 timer residue is covered for the "no cleanup runs" row as well as for the failed-act rows. The residue paragraph's "fails or does not run" quantifier plus its "a cleanup that did not deregister the entry also leaves the registry entry and its armed [Section 4.9] lease-expiry timers" answers the armed-timer-fires-into-a-dead-session dress on the "Pre-`running` slot no cleanup reclaims" row. — EVIDENCE: spec-changes.md:631, spec-changes.md:633

### [spec-recheck.1.review-security.1]

FACT: The whole delta this recheck exists for is one commentary paragraph in the spec staging (the ten-second graceful-window provenance note). Its citations verify: `context.WithTimeout(context.Background(), 10*time.Second)` is at pkg/adapter/holdstate.go:201, and commit 3997f502b is dated 2026-08-22. — EVIDENCE: pkg/adapter/holdstate.go:201

FINDING: Disposition-table row 3 (spec-changes.md:625) reports `released` with the clean-exit flag Set when the runtime close succeeds and a later act fails, and SPEC-3 deletes the current §5.2 universal "If cleanup fails, the slot is leaked" (spec/05_runtime-registry-and-pool-model.md:545). Because the staged action list (spec-changes.md:547) now names the credential directory removal and the §4.9 timer cancellation as cleanup acts, a failed credential-directory or workspace removal becomes an outcome nothing surfaces: not `leaked`, so not counted toward the ceil(maxConcurrentSessions/2) unhealthy threshold (spec/06_warm-pod-model.md:160), not in `lenny_adapter_leaked_slots`, no drain. Filed under the security lens as regression of an established residual-state control.

FACT: The tree already behaves as row 3 states — pkg/adapter/session.go:272 is `_ = removeSlotTree(st)` (error discarded) and pkg/adapter/sessionscrubreporter.go:39-44 keys the outcome on `closeErr` alone. So the staging records the tree. What the staging does NOT do is record the withdrawal as a decision or as a row under `## Defects in the shipped tree that this proposal does not stage` (summary.md:650), the way it records five other shipped-tree divergences. — EVIDENCE: pkg/adapter/session.go:272

WATCHOUT: The caller directive says the disposition table's six surviving columns are "unchanged and settled" and bars re-keying a cell for RESIDUE. The finding above is about the `leaked`/report columns rather than residue, but a fixer should read the directive before touching the table; the cheaper remedy may be a decision entry rather than a cell change.

FACT (checked clean, do not re-run): rule 13's entry-carries-no-token arm fails closed (answers `superseded`, removes nothing) with its reasoning stated; §7.1's acknowledged-clean predicate quantifies over every outcome and is explicitly fail-closed against a non-conforming adapter; nothing staged touches §10.3 zero-RBAC, §13.2 egress, §13.1 pod security, or admission-webhook purity. — EVIDENCE: spec-changes.md:1057, spec-changes.md:377

UNVERIFIED: whether a leftover `/run/lenny/slots/{sessionId}/credentials.json` on a concurrent pod is reachable by a later session's agent code through the shared runtime process. If it is not, the row-3 finding weakens to an observability gap. Someone with the §5.2/§13.1 filesystem-isolation picture should settle it.

### [spec-recheck.1.review-single-source.1]

DECISION: empty findings list — BECAUSE the only delta to the spec staging since the r23 snapshot is one added commentary paragraph in SPEC-3 (spec-changes.md:664-672, the provenance/no-override note on the ten-second graceful window), and it introduces no second stating site of any rule; every rule inventory I walked still has exactly one stating home — ALTERNATIVES: filing the new paragraph's clause "the other two take the reclaiming `Shutdown`'s carried grace or that request's own deadline" as a copy of the staged reclaim-hold sentence (spec-changes.md:635). Rejected: it names the sentence it summarises ("the three cases the sentence states"), it is the rationale for why the figure is recorded rather than minted and why no flag is staged, and the lens exempts a site that cites or gives the reason for a rule.

FACT: the new paragraph's citations all hold. `context.WithTimeout(context.Background(), 10*time.Second)` is at exactly `pkg/adapter/holdstate.go:201`, and commit `3997f502b` is dated 2026-08-22 ("Terminate every started session when the coordinator hold times out") — EVIDENCE: pkg/adapter/holdstate.go:201

FACT: no other spec section states a grace figure for the §10.1 hold-timeout termination, so the staged §5.2 sentence is the only site. `grep -rn "hold timeout\|coordinatorHoldTimeout" spec/*.md` returns §10.1 (120s hold expiry), spec/11:214 and spec/17:925 (the same 120s in the tunables tables), and §28's CH-FENCE row — none of them carries a close-grace figure — EVIDENCE: spec/10_gateway-internals.md:58, spec/11_policy-and-controls.md:214

WATCHOUT: two near-duplications look like (g) hits and are settled, not findings. (1) The §4.7.1 carriage table's `PrepareWorkspace`/`FinalizeWorkspace` cells ("non-empty when the request is not marked `mid_session`, empty when it is") look like a copy of rule 1, but the table states carriage and rule 1 states the refusal; neither is implementable from the other. (2) The §5.2 disposition table's "Cleanup-outcome report" column encodes the scrub-model biconditional row by row, but the caller's round-15 prune directive records the table's six surviving columns as settled — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:1013 (carriage table), :639 (report column)

USEFUL [DECISION: the cleanup has TWO completion terms, each defined once]: the standing-context entry let me confirm in one pass that `completed` (§5.2 reclaim-hold paragraph) and `acknowledged clean` (§7.1 paragraph) are two predicates with one home each rather than one predicate stated twice, which is the shape this lens would otherwise have spent a full sweep on.

### [spec-recheck.2.fix-G1.1]

DECISION: Closed the running-boundary contradiction by deleting the five words "is owed no runtime close and" from the Design paragraph "**The `running` boundary sits where the adapter records the session on the shared runtime process.**", leaving "so a slot the adapter never recorded is owed no cleanup-outcome report." — BECAUSE the runtime teardown's precondition has one home, the staged §4.7 `Shutdown` row, which keys it on admission of the starting RPC rather than on the record, so a start still in flight is torn down rather than skipped; the Design section is a choice record that cites rule homes, and commentary restating a staged rule is fixed by deletion. — ALTERNATIVES: rewriting the clause to state the correct rule (rejected: grows a second full statement of the §4.7 row's precondition); re-keying the §4.7 `Shutdown` row on the record so the clause becomes true (rejected: leaves an unclosed runtime session on the shared process for a start in flight, reverses the stated asymmetry, and cascades into rule 8, the §5.2 disposition table's pre-`running` rows, and CODE-1's `st.started` predicate); deleting the whole paragraph (rejected: it is the only site carrying the ground for SPEC-4).

FACT: "Recorded" and "admitted" are two distinct instants in this staging, and the two cleanup obligations are keyed on different ones. The runtime teardown is keyed on admission of the starting RPC; the cleanup-outcome report is keyed on the record, which is the `running` boundary. Rule 8 has the adapter put the session on the shared runtime process, then resolve and confirm, then record, so a start in flight is unrecorded while its session is already on the process. — EVIDENCE: spec-changes.md §4.7 `Shutdown` row ("taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped"); spec-changes.md §4.7.1 rule 8 ("It takes the session back off the shared runtime process"); pkg/adapter/session.go:163 calls `s.noteRuntimeStarted(sessionID)` after the start; pkg/adapter/runtimegeneration.go:26-33 shows that record is the `s.runtimeLive` write under `s.mu`.

WATCHOUT: Any sentence of the form "a slot the adapter never recorded is owed no X" is only true for X = the cleanup-outcome report. Extending it to the runtime close is the error this round removed, and it reads as a natural generalisation of the `running` boundary, so a later round is likely to rewrite it back. The two obligations do not share a predicate. — EVIDENCE: spec-changes.md, the Design paragraph "**The `running` boundary sits where the adapter records the session on the shared runtime process.**" and the §4.7 `Shutdown` row.

FACT: This round's fix is confined to that one Design clause. A grep of every file in the proposal directory for "owed no runtime close" and "never recorded is owed" returned exactly one site, and the §5.2 disposition table, its residue paragraph, §4.7.1 rule 8, and CODE-1's split between `st.started` (teardown) and `runtimeLive` (report) all already state the correct rule. No deliverable id was added, removed, merged, split, or resequenced, so the summary's deliverable index and the implementation checklist are unaffected, and nothing in non-spec-changes.md was falsified.

### [spec-recheck.2.fix-design-G1.1]
DECISION: fix the Design "running boundary" paragraph by DELETING the five words "is owed no runtime close and", leaving "...so a slot the adapter never recorded is owed no cleanup-outcome report." — BECAUSE the runtime teardown's precondition is stated once in the staged §4.7 `Shutdown` row (admission, not arrival on the process), and the Design section is a choice record that cites rule homes rather than a second statement of them; deleting the wrong half is a reduction and adds no text. ALTERNATIVES: (a) rewrite the clause to say a start still in flight IS torn down — rejected, that restates the §4.7 row in the Design section, which the caller directive forbids (commentary is not a rule home); (b) change the §4.7 row's precondition from admission to the record — rejected, it would let a start in flight leave an unclosed runtime session and contradicts the stated over/under-approximation asymmetry at spec-changes.md:16-18; (c) delete the whole paragraph — rejected, it is the only home of the "no state is added / existing terminals stay authoritative" ground for SPEC-4.
FACT: "recorded" (the `s.runtimeLive` write under `s.mu`, pkg/adapter/runtimegeneration.go:26-33, reached from pkg/adapter/session.go:163 `noteRuntimeStarted` AFTER the start returns) and "admitted" are two different instants; anything keyed on the later instant cannot discharge an obligation the staging keys on the earlier one. EVIDENCE: proposals/0081_*/...spec-changes.md:273 (teardown precondition = admission), :1052 (rule 8 orders put-on-process → confirm → record).
WATCHOUT: the cleanup-outcome report and the runtime teardown have DIFFERENT predicates and the Design paragraph is about the boundary the report follows only. A future edit that pairs them in one sentence reintroduces this defect. CODE-1's doc comment already splits them correctly (`st.started` for the teardown, `runtimeLive` for the report) at non-spec-changes.md:529-576; keep that split. EVIDENCE: proposals/0081_*/...spec-changes.md:34-35

### [spec-recheck.2.review-mechanism.1]

DECISION: filed exactly one finding, against the Design paragraph the round-1 fix rewrote — BECAUSE the rewrite moved the `running` boundary to "the adapter records the session on the shared runtime process" and then appended a consequence clause, "so a slot the adapter never recorded is owed no runtime close and no cleanup-outcome report", whose first half contradicts the staged §4.7 `Shutdown` row's runtime-teardown precondition (admission, "so a start still in flight is torn down rather than skipped") and the adjacent Design paragraph's own "the teardown over-approximates toward closing" — ALTERNATIVES: rejected filing the fence-pointer replacement of `receiving_uploads ──→ running` (a workable design choice, and docs/reference/state-machines.md:235 is now listed in DOCS-1, so no edit site is missed); rejected filing pkg/sandbox/slotstate/slotstate.go:100's `→ running` doc comment (code lane, out of this loop's scope).

FACT: the reworded boundary checks out against the tree. `noteRuntimeStarted` takes `s.mu` and writes `s.runtimeLive`, so "the record is the one event a later request can read under the registry lock" is accurate. EVIDENCE: pkg/adapter/runtimegeneration.go:26-33.

FACT: the three sites the boundary rewrite had to reach all agree after it. Rule 8's "records nothing ... because the slot never reached `running`" (spec-changes.md:1052), the §5.2 biconditional (spec-changes.md:569) and the §6.2 pre-`running` paragraph (spec-changes.md:1002) now key on the same event. The pre-`running` disposition rows still key on "every act", which correctly includes the runtime close that the admission-keyed teardown performs on an in-flight start.

WATCHOUT: "admitted the start" and "recorded the session on the runtime process" are two different instants and the staging deliberately keys two different rules on them (teardown on the first, `running` and the report on the second). Any sentence that collapses them is the defect this family keeps producing. EVIDENCE: spec-changes.md:273 vs spec-changes.md:1002.

USEFUL [review-log-archive.md:54023]: the earlier OPEN that predicted the exact remedy this round applied ("a later round that wants full consistency would make the `receiving_uploads ──→ running` annotation a `(see the paragraph below)` pointer") let me confirm the fence change was the intended reduction rather than a new invention, and saved re-deriving it.

### [spec-recheck.3.review-mechanism.1]

FACT: The whole delta for this round is one 3-line hunk in spec-changes.md, the Design
"**The `running` boundary sits where the adapter records the session...**" paragraph, which
dropped "no runtime close and" from its conclusion. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:32-36
FACT: The edit is consistent with the staged §4.7 `Shutdown` row, which owes a runtime teardown
to any session "whose start the adapter has admitted", pre-`running` included. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:273
FACT: The surviving conclusion ("owed no cleanup-outcome report") agrees with staged rule 8, which
says the adapter "reports no cleanup outcome for the slot, because the slot never reached `running`",
and with the staged §6.2 pre-`running` paragraph's boundary ("A slot reaches `running` when the
adapter has recorded the pod's shared runtime process as holding the session"). — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1052, :1002
FACT: Step-2 sweep for stale parallel sites found none: `grep -n "never recorded|not recorded|no close|closes nothing|nothing to close"` over spec-changes.md and summary.md returns only the edited line 35; the "runtime close" sweep over the whole proposal directory returns only disposition-table rows and non-spec text that the §4.7 row already governs. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:35, :624-630
DECISION: Returned an empty findings list — BECAUSE the single hunk is the fix for a finding already
recorded as closed, it is internally coherent, and no other site restated the removed clause —
ALTERNATIVES: filing the residual "takes the session back off that process" clause as now-orphaned
rationale, rejected because the clause still grounds the boundary choice and the caller directive bars
commentary edits that add or reword rationale.

### [spec-recheck.4.fix-G1.1]

DECISION: the inserted §6.2 fence entry `receiving_uploads ──→ slot_cleanup` now carries the one-line pointer `(see the pre-`running` slot cleanup paragraph below)`, the same pointer its sibling `receiving_uploads ──→ running` entry already carries — BECAUSE the pre-`running` slot cleanup paragraph (spec-changes.md, SPEC-4 prose-after-fence block) is already the declared single home of both `slot_cleanup` edge conditions and of the `running` boundary, and the r23 delta had to re-word the boundary at both sites in one hunk pair, which is the drift a second full statement guarantees — ALTERNATIVES: re-synchronise the two wordings (rejected, that is the defect restated as a fix); shorten the fence trigger to a partial one (rejected, a third weaker statement that drops the start-still-in-flight case); delete the paragraph and let the fence entry be the home (rejected, the paragraph also carries the §4.7.9 step-5 stage enumeration, the §5.2 pointer and the no-admission-rule clause, and the sibling entry points at it); retarget the pointer at §5.2 (rejected, §5.2 states who performs the cleanup and its disposition, never the edge condition).

FACT: all four §6.2 fence entries SPEC-4 touches now carry a pointer and state no trigger, so the block's commentary sentence "Each entry carries a pointer and states no trigger" reads true of the insert without edit. No rationale sentence was added anywhere. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:976 (the commentary), :934 (the reduced insert).

FACT: the §6.2 per-slot fence gate compares edge presence and block membership only, never trigger text, so reducing the insert's annotation trips no gate. This re-confirms review-log.md:232 and :288 against the reduced form. EVIDENCE: review-log.md:232.

WATCHOUT: two other sites state this edge trigger in full and are NOT copies to be reduced. non-spec-changes.md:791 is the CODE-3 Go doc comment, which a code block must carry as code, and non-spec-changes.md:2416 is the DOCS-1 `docs/reference/state-machines.md` table row, which the documentation rules bar from carrying a spec pointer. Neither was falsified by this fix and neither was touched. A later single-source lens that files them is filing a licensed restatement. EVIDENCE: non-spec-changes.md:791, :2416.

WATCHOUT: the CODE-3 doc-comment trigger at non-spec-changes.md:791 reads "abandoned before the slot reaches running" and omits the "or fails" and "a start still in flight included" cases the spec paragraph states. That is a non-spec-lane defect, not one this fix caused and not one a spec lens may repair. EVIDENCE: non-spec-changes.md:791 against spec-changes.md:1000 (the pre-`running` slot cleanup paragraph).

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: nothing new. Step S5's enumeration is already recorded as stale by review-log.md:975, and this fix changes no count of fence edits and adds no deliverable. The standing deferral covers it unchanged.

### [spec-recheck.4.fix-design-G1.1]
DECISION: fix the duplicated pre-`running` cleanup trigger by REDUCING the inserted §6.2 fence entry to the same pointer its sibling already carries — `receiving_uploads ──→ slot_cleanup    (see the pre-`running` slot cleanup paragraph below)` — leaving the paragraph at spec-changes.md:1002 the single stating site, and re-wording only the "Spec files touched" bookkeeping line at spec-changes.md:1284-1288 so "one edge added" records that the added edge carries that pointer — BECAUSE the fence-edit block already declares "Each entry carries a pointer and states no trigger" (spec-changes.md:978) and the paragraph is already declared the home of both `slot_cleanup` edges (:1005-1006); the reduction makes all four touched entries uniform and removes text instead of adding it. ALTERNATIVES: re-synchronise the two wordings (rejected: leaves two full statements of one rule, which is what the finding is); shorten the fence trigger to a partial one (rejected: a partial trigger is a third, weaker statement that a reader implements from); delete the paragraph and keep the fence trigger (rejected: the paragraph also carries the `running` boundary and the §5.2 pointer, which no fence entry can hold, and the sibling `→ running` entry already points at it); add a rationale sentence to the fence commentary covering the insert (rejected as hair and barred by the caller directive — commentary is not a rule home, and :978 already reads true of the insert).
FACT: the §6.2 per-slot fence gate compares edge PRESENCE and block membership only, never trigger text, so converting the inserted entry's annotation to a pointer trips no tier-11 gate. — EVIDENCE: review-log.md:232, review-log.md:288; target fence spec/06_warm-pod-model.md:149-155.
FACT: the commentary sentence "Each entry carries a pointer and states no trigger" (spec-changes.md:978) needs no edit under this fix; it becomes true of four entries instead of three, and the §5.2 rationale that follows it only ever justified the two §5.2 pointers. — EVIDENCE: spec-changes.md:974-986.
WATCHOUT: the two pointers in this fence now have two different targets (two at §5.2, two at the pre-`running` paragraph). That is correct, not an inconsistency: §5.2 owns the disposition of the cleanup, the paragraph owns the pre-`running` boundary and the edge conditions. A later lens that "unifies" all four onto §5.2 would put the edge condition back in a section that does not state it. — EVIDENCE: spec-changes.md:949, :967, :976, :1002.
UNVERIFIED: DOCS-1's own-voice trigger cell for the new `receiving_uploads → slot_cleanup` row in docs/reference/state-machines.md is unaffected by this fix (a reader-facing page is the one licensed restatement), but nobody re-read that cell this round against the paragraph's current wording.

### [spec-recheck.4.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens on the
spec-changes staging — BECAUSE every "reads, verbatim" anchor in the staging resolves, uniquely,
against the current tree, and every artifact a staged edit references either pre-exists or is
landed by an earlier spec step — ALTERNATIVES: filing the §16.1-row-before-code gate, the
§5.2 hold paragraph's broad "create or resolve a registry entry" wording, and the §4.6.1 bullet
anchored by its opening words rather than verbatim; each was checked and each fails the bar
(see FACTs below).

FACT: I re-verified EVERY verbatim anchor in the staging against the tree this round. All resolve,
all uniquely. Do not re-run this sweep unless the staging changes.
  EVIDENCE: spec/04_system-components.md:157 (§4.1 third sentence), :674 (`DemoteSDK` row), :686
  (`Shutdown` row), :692 (`ReportSessionScrub` row), :688 and :695 (the §4.7.1 insertion window),
  :854 (§4.7.9 step 5), :409 / :415 / :416 (§4.6.1 enumeration and the two claim-deletion bullets);
  spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`), :545 (action list, reporting
  sentence, leaked-outcome sentence, all three on one line), :561 (whole-pod trigger parenthetical),
  :488 (`**Session count limit:**`, the bullet §12.6's replacement names);
  spec/06_warm-pod-model.md:80 (the three projection-prose clauses), :95 (the Occupancy-projection
  `claimed ──→ draining` entry), :148 / :152 / :155 (the three fence annotations), :150 (the
  per-slot heading line), :156-:158 (the pre-`running` paragraph's insertion window), :234
  (mid-resume cancel clause), :290 (`**Client visibility:**` clause);
  spec/07_session-lifecycle.md:23 (atomicity paragraph), :210 / :213 / :214 (§7.2 preamble, step 2,
  step 3), :414 (§7.3 list tail);
  spec/12_storage-architecture.md:481 (prose write and read clauses), :494 (DDL comment);
  spec/15_external-api-surface.md:1136 (all four `SETUP_COMMAND_FAILED` sentences), :1469-:1471
  (§15.4 insertion window);
  spec/16_observability.md:14 (the session-slot failure row the new rows sit beside);
  spec/29_communication-scenarios.md:704-711 (§29.4 step 13).

FACT: the §7.1 reclaim paragraph lands INSIDE a markdown code fence. The fence opens at
spec/07_session-lifecycle.md:5 and the atomicity paragraph the new one follows sits at :23, between
the step-8 line (:22) and its continuation line (:24). The existing paragraph already carries
markdown links inside that fence, so the new one's links are precedented and this is not a defect.
  EVIDENCE: spec/07_session-lifecycle.md:5, :22, :23, :24

FACT: the adapter metric gate runs CODE → catalogs, never catalogs → code, so SPEC-6 landing a
§16.1 row at S6 with no `pkg/adapter/metrics.go` registration (which CODE-9/S10 lands) fails
nothing. `spec161Metrics` is a hand-transcribed Go slice reconciled both ways against
`MetricCatalog()` alone and reads no spec file, so it is likewise untouched by a spec-only row.
A future round should not file the S6-before-S10 ordering as a gate-state defect.
  EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:81-116 (iterates
  `registeredAdapterMetrics`); pkg/observability/metrics/catalog_test.go:15, :188-:212

FACT: the staged §5.2 ten-second figure citation is accurate to the character.
`context.WithTimeout(context.Background(), 10*time.Second)` is the pass-2 close context.
  EVIDENCE: pkg/adapter/holdstate.go:201; proposal spec-changes.md:664-672

WATCHOUT: `claimed ──→ draining` occurs FIVE times in spec/06's fence (lines 95, 114, 135, 137,
139). SPEC-4's edit is scoped by "In the fenced `Occupancy projection` block", which selects :95
alone, and it supplies only the replacement text with no verbatim anchor block. That is
determinate, and a later round should not file it as an ambiguous anchor.
  EVIDENCE: spec/06_warm-pod-model.md:95, :114, :135, :137, :139

WATCHOUT: §4.6.1's first projection bullet, "`idle` for a pod in a warm-inventory phase with no
claim", is NOT among SPEC-4's edit targets and does not need to be. The re-keyed :416 bullet keys
on the pod projecting `claimed`, which is not a warm-inventory phase, so the two do not overlap.
Do not file the surviving bullet as an unreduced third statement of the claim-existence half.
  EVIDENCE: spec/04_system-components.md:411 (the bullet), :416 (the bullet SPEC-4 replaces)

UNVERIFIED: the staged §5.2 reclaim-hold sentence "While the identifier is held the adapter admits
no request that would create or resolve a registry entry under it" is broader on its face than the
tree, where revoke, rotate, extend and `CoordinatorFence` resolve through read-only
`slotStateLocked`/`boundSlotState` and would therefore be swept in by a literal reading. The
standing Settled line "The reclaim hold cannot refuse a mandatory credential control" records the
tree side but not whether the SPEC text needs narrowing. I did not file it: it is a mechanism
question rather than an applicability one, and the spec sentence's next sentence scopes the
creating requests to §4.7.1's enumeration. The mechanism or security lens should settle whether
the "or resolve" half needs a carve-out for the §4.9 mandatory controls.
  EVIDENCE: proposal spec-changes.md, the `**Slot-identifier reclaim hold.**` paragraph in the
  SPEC-3 §5.2 append; review-log.md `## Standing context`, the Settled line beginning "The reclaim
  hold cannot refuse a mandatory credential control."

### [spec-recheck.4.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens — BECAUSE I re-verified every verbatim "reads, verbatim:" block, every anchor, and every code/commit citation in the staging against the tree and all of them check out — ALTERNATIVES: filing the two soft spots below; both fail the bar (one is commentary scope, one is a defensible section attribution).

FACT: every verbatim anchor block in the staging still matches the tree byte for byte as of 2026-09-21. Re-verified this round: §4.1 third sentence (spec/04_system-components.md:157); §4.7 `Shutdown` row (:686); §4.7 `DemoteSDK` row (:674); §4.7 `ReportSessionScrub` row (:692); §4.6.1 projection lead-in and both claim-deletion bullets (:409, :415, :416); §5.2 `**Scrub model.**` (spec/05:453), `**Slot cleanup:**` action list / reporting sentence / leaked sentence (all on the single line :545), `**Whole-pod replacement trigger:**` parenthetical (:561); §12.6 prose sentence + read clause (spec/12:481) and DDL comment (:494); §6.2 fence entries `receiving_uploads ──→ running` (spec/06:152-153), `slot_cleanup ──→ released` (:155), `slot_cleanup ──→ leaked` (:148), `claimed ──→ draining` in the Occupancy projection block (:95), projection prose clauses (:80), mid-resume cancel clause (:234), Client visibility clause (:290); §7.1 atomicity paragraph (spec/07:23), §7.2 preamble/step 2/step 3 (:210, :213, :214), §7.3 list item 4 (:414); §4.7.9 step 5 (spec/04:854); §15.1 `SETUP_COMMAND_FAILED` all four sentences (spec/15:1136); §29.4 step 13 (spec/29:703-711). Do not re-run this sweep; re-run only the blocks a later fix touches.

FACT: the delta's new code citation is exact. `pkg/adapter/holdstate.go:201` is `closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`, and `git log 3997f502b` gives 2026-08-22 "Terminate every started session when the coordinator hold times out". The CODE-6 re-scoping claim the same paragraph makes is backed by non-spec-changes.md:3795-3800 ("each member mints its own ten-second close context inside `terminateHeldSession`").

FACT: insertion points in the delta resolve. The §6.2 per-slot sub-state fence closes at spec/06_warm-pod-model.md:156 and `**`reserved` hold semantics.**` opens at :158, so "immediately after the fenced block closes and before" is a real, unambiguous slot, and the new `receiving_uploads ──→ running` pointer "(see the pre-`running` slot cleanup paragraph below)" names a paragraph that does land below it and does state the boundary.

FACT: the §12.6 write-trigger re-key is correct against the reconciler. `ScrubReporter.RecordSessionScrub` calls `IncrementSessionsServed` at `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-459`, so the increment is on the report and a release that files none performs none. The gateway side is authoritative here; `agent_pod_state` is a mirror only for the WarmPoolController-written columns (spec/12:481 names `sessions_served` as the exception).

FACT: the tier-11 gate the SPEC-3 §4.7 block invokes asserts exactly what the block claims. `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67-75` requires BOTH the spec row and `docs/reference/adapter-contract.md`'s row to contain the literal `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."`, and it locates the spec row with `lineContaining` over `"| \`ReportSessionScrub\` |"` (:46-49), so the row must stay ONE physical line. The staged replacement keeps the sentence word for word.

FACT: the carrier/non-carrier split in the SPEC-3 table is accurate against the docs tree. Carriers (they assert the report): `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33` (both "...and the adapter reports its outcome to the gateway"), `docs/reference/adapter-contract.md:81` and its `Shutdown` row at :75. Non-carriers (cleanup only, no report): `docs/operator-guide/multi-tenancy.md:72`, `docs/runtime-author-guide/index.md:186`, `docs/runtime-author-guide/lifecycle.md:69`, `docs/client-guide/session-lifecycle.md:416`, spec/06:24, spec/07:72. Note the proposal writes `multi-tenancy.md` unqualified; the file is `docs/operator-guide/multi-tenancy.md`, not under `docs/reference/`.

WATCHOUT: the staged §4.7 `Shutdown` row cites "[Section 15.4.2] graceful-shutdown signal", but §15.4.2 (spec/15_external-api-surface.md:1686-1704) is the RPC Lifecycle State Machine and only describes the `DRAINING` state; the frame itself, with the "Graceful shutdown signal" gloss and the `type`/`deadlineMs`/`reason` field set that carries no session, is specified at spec/28_communication-channels.md:1082. I judged this NOT a finding (the DRAINING row does state the adapter signalling the agent to stop, and the pod-global/names-no-session property is confirmed by the §28.5.3 field set), but a later lens that reads it as an attribution error should know the §28.5.3 anchor is the stronger one before spending a finding on it. — EVIDENCE: spec/15_external-api-surface.md:1702; spec/28_communication-channels.md:1082

WATCHOUT: after this round's delta, THREE fence entries are replaced in the SPEC-4 §6.2 fence block, but the commentary after them still reads "Each entry carries a pointer and states no trigger. The §5.2 disposition table gives the cleanups that traverse either edge different reports..." — "either edge" was written when only the two `slot_cleanup` edges were replaced, and the §5.2 rationale does not apply to the new `receiving_uploads ──→ running` pointer, which points at the §6.2 paragraph instead. This is commentary, not a staged rule, and the caller directive bars adding rationale, so it is not a finding; do not file it, and if it is ever fixed the fix is a deletion. — EVIDENCE: spec-changes.md:978-985

MISTAKE: nothing new this round. The three "already found and fixed" items from earlier rounds are visibly repaired in the current text: the Design paragraph no longer says a never-recorded slot "is owed no runtime close" (spec-changes.md:32-37), which is what made it consistent with the staged §4.7 row's "a start still in flight is torn down rather than skipped" (:273).

UNVERIFIED: I did not re-derive the seven entry-creating RPCs, the SCHEMA-1 field numbers, or the `mid_session` carriage direction; the standing context records all three as settled and re-derived by many shards. A later lens that needs them should take them from the log rather than from the tree.

### [spec-recheck.4.review-client-surface.1]

DECISION: EMPTY findings list for the client-facing surface lens on spec round 23's staging — BECAUSE the whole delta since the last empty run of this lens (round 22) is the `running`-boundary rewording plus one new fence-annotation replacement, and the only client-facing representation either touches (`docs/reference/state-machines.md:235`, the `receiving_uploads` → `running` row) is already staged in DOCS-1 and named in spec-changes.md's "Spec files touched" list — ALTERNATIVES: filing `docs/operator-guide/troubleshooting.md:41` as an unstaged carrier (see DEFERRED below), rejected because its only remedy is a docs edit, which this loop may not land.

FACT: the `receiving_uploads → running` trigger has exactly TWO carriers tree-wide, and neither is a diagram. `spec/06_warm-pod-model.md:152-153` and `docs/reference/state-machines.md:235`. `grep -rln "slot_cleanup\|slot_assigned" docs/` returns `docs/reference/state-machines.md` alone, so no SVG under `docs/assets/diagrams/` carries the per-slot sub-state machine. A third hit, `pkg/sandbox/slotstate/slotstate.go:35` ("the task has been dispatched to the runtime with the slotId"), is a Go comment and a code-lane site. — EVIDENCE: spec/06_warm-pod-model.md:152, docs/reference/state-machines.md:235, pkg/sandbox/slotstate/slotstate.go:35

FACT: the tier-11 gate over the per-slot sub-state machine asserts EDGE STRINGS and section scoping, never trigger text, so replacing an annotation with a pointer cannot break it. `generalSlotEdges` is a list of four `X ──→ Y` substrings checked with `strings.Contains` against the §6.2 general block, and the doc half asserts only that the page's section names the five state identifiers and is not scoped to `maxConcurrentSessions`. The new `receiving_uploads ──→ slot_cleanup` edge is not in `generalSlotEdges`, so the "scoped block must not contain a general edge" assertion does not fire on it either. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-37, :55-72, :100-112

FACT: every `reads, verbatim:` block in spec-changes.md resolves to exactly one file under `spec/`. Checked mechanically: `re.finditer(r'verbatim:\s*\n\n```\n(.*?)\n```\n', props, re.S)` over the staging, each block counted against `glob('spec/*.md')` — 26 blocks, each exactly one hit, no misses and no ambiguities. This is cheaper than the three-command sweep the ledger records and covers the anchor half of it. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md (whole file), spec/*.md

USEFUL [Settled, round 11/22]: "No OpenAPI document and no language SDK mirrors any edited surface." Re-verified a third time and it still saves a full sweep. `SETUP_COMMAND_FAILED`, `setup_command_failed`, `ShutdownResponse` and `exited_cleanly` all return zero hits in `pkg/gateway/externalapi/openapi/openapi.json` and under `sdks/`; the only `slot` hits under `sdks/client/` are three parallel doc comments about upload addressing (`client.ts:326`, `client.py:327`, `client.go:266`), untouched by this proposal. Note the lens prompt names `pkg/gateway/openapi/openapi.json`, which does not exist; the real path is `pkg/gateway/externalapi/openapi/openapi.json`.

FACT: the spec writes an adapter `ErrorCode` WITHOUT the `ERROR_CODE_` prefix and the proto writes it WITH one, and that is the shipped convention rather than a drift. `PROTOCOL_VERSION_INCOMPATIBLE` is the precedent, spelled bare in both the spec and the published doc and prefixed in the proto. So staged §4.7.1's `SLOT_BIND_ATTEMPT_SUPERSEDED` / `SLOT_BIND_ALREADY_STARTED` against SCHEMA-1's `ERROR_CODE_SLOT_BIND_*` is not a half-renamed vocabulary. — EVIDENCE: spec/15_external-api-surface.md:1699, docs/reference/adapter-contract.md:448, schemas/lenny-adapter.proto:585

FACT: `PrepareWorkspaceRequest` does NOT carry `mid_session` in the shipped proto; only `FinalizeWorkspaceRequest` does (field 4). The staged §4.7.1 carriage table marks `mid_session` "carried" on both, which is a new field SCHEMA-1 adds at `PrepareWorkspaceRequest` field 6, and rule 9 (the first-frame rule) depends on it. A lens reading the table against the tree alone will think the table is wrong; it is the proposal adding the field. — EVIDENCE: schemas/lenny-adapter.proto:682-696 (no `mid_session`), :713-724 (`FinalizeWorkspaceRequest.mid_session = 4`), non-spec-changes.md:2317-2318 (the two `PrepareWorkspaceRequest` rows)

DEFERRED [docs/operator-guide/troubleshooting.md:41]: the row `| Setup command failures | \`reason: setup_command_failed\` | Check runtime setup commands, increase \`setupPolicy.timeoutSeconds\` |` keys its whole diagnosis and remedy on the setup command having run. SPEC-5 widens `SETUP_COMMAND_FAILED` to a second cause, the started-session refusal at the setup-command request, which runs no setup command and produces no setup output, so the remedy column is wrong for that cause. What is true instead: `reason: setup_command_failed` now covers either a non-zero or timed-out setup command or a refused setup-command request, and only the first has output to inspect. DOCS-3 stages the same correction for `docs/reference/error-catalog.md:129` and stops there; this page is in no edit list. Marginal — it is operator-guide triage prose rather than a contract — so it may be closed by deciding it needs nothing.

OPEN: does `docs/operator-guide/observability.md` owe rows for the two SPEC-6 counters? CODE-9 stages `docs/reference/metrics.md` only, and `observability.md` carries its own metric table (`observability.md:67` is a row for `lenny_warmpool_warmup_failure_total`). Nobody has traced whether that page is a full mirror of §16.1 or a curated subset; if it is curated, it owes nothing. A docs-lane question, not a spec-lane one.

### [spec-recheck.4.review-docs-alignment.1]
DECISION: empty findings list — BECAUSE the entire delta since the `spec-recheck-r2-prefix` snapshot is one 3-line hunk in `.spec-changes.md` (the Design paragraph "**The `running` boundary sits where the adapter records the session on the shared runtime process.**" losing "is owed no runtime close and"), which is proposal commentary about an already-staged rule: it changes no identifier, no default, no error code, no metric, no lifecycle step, so it mirrors into no `docs/` surface. — ALTERNATIVES: filing `docs/reference/metrics.md` / runbook companions for SPEC-6's two new counters, and the missing narrative-operator-doc cause for the orphaned-tokened-entry failure mode (gateway crash between abandoning an attempt and sending the reclaim); both rejected on scope, their only remedy being a docs edit this spec-lane loop may not land, exactly as `[spec-recheck.1.review-docs-alignment.1]` rejected them.
FACT: the docs carrier sweep for the withdrawn "reports on every session release" universal is complete and the SPEC-3 carrier table's three `docs/` rows are exactly the hits. Only `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33` carry the reporting clause ("and the adapter reports its outcome to the gateway"); `docs/operator-guide/multi-tenancy.md:72` carries the same sentence WITHOUT that clause, which is why the carrier table's exclusion list is right to name it a non-carrier. — EVIDENCE: docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33; docs/operator-guide/multi-tenancy.md:72
FACT: `docs/reference/adapter-contract.md:84` (`**Scrub responsibilities.**`) says "Your runtime exits at each session end ... then reports through these RPCs" and is NOT listed in DOCS-2's four edits. I judged it below the bar and did not file it: it is keyed on a session END (a session that ran, hence a slot that reached `running`), so the biconditional does not falsify it, and its remedy would be a docs edit regardless. A later docs-lane lens that rediscovers it should weigh it there, not here. — EVIDENCE: docs/reference/adapter-contract.md:84
FACT: `docs/reference/state-machines.md:248` ("Served-session count reaches `recycle.maxSessionsPerPod` on a session release") survives SPEC-3's §12.6 re-key untouched. The re-key moves only the WRITE trigger onto the cleanup-outcome report; the per-release EVALUATION point on a concurrent non-`vm-restart` pool is unchanged, so the row is not a stale carrier and DOCS-1 rightly does not list it. — EVIDENCE: docs/reference/state-machines.md:248; spec-changes.md §12.6 block (read clause re-keyed to §5.2's **Session count limit:** bullet)
USEFUL [spec-recheck.1.review-docs-alignment.1]: its WATCHOUT ("under the spec-lane scope this lens is nearly inert; the only in-scope class left is an accepted failure mode whose observable outcome lands in NO staged spec text") plus its walk of the nine Edge-cases bullets meant I re-verified rather than re-derived, and it named the same two out-of-scope alternatives I reached independently.
WATCHOUT: no `docs/` page mirrors §5.2's `**Slot cleanup:**` ACTION LIST, so SPEC-3's two added acts (the credential directory removal and the §4.9 timer cancellation) open no docs edit site. A lens looking for one will find `docs/reference/adapter-contract.md:84` and `docs/runtime-author-guide/lifecycle.md:390`, both of which describe the WHOLE-POD scrub's acts rather than the per-slot list. — EVIDENCE: docs/runtime-author-guide/lifecycle.md:390; docs/reference/adapter-contract.md:84

### [spec-recheck.4.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens on this recheck — BECAUSE the entire delta since the `spec-recheck-r2-prefix` snapshot is one clause deletion in the Design running-boundary paragraph (`.spec-changes.md:32-37`, "no runtime close and" removed), which adds, changes and removes no identifier and therefore opens no edit site. I then re-swept the whole staging's identifier set and every companion surface listed below and found nothing that becomes wrong and is unlisted. ALTERNATIVES rejected: (a) the "clean-exit flag on the `Shutdown` response" being used normatively in §7.1, §4.7.1 rule 15 and the §5.2 table while no spec text defines it — `exited_cleanly` is a SHIPPED `ShutdownResponse` field (schemas/lenny-adapter.proto:1666) and the spec has never named it, so this is a pre-existing spec silence the proposal leans on rather than a surface the edits falsify; (b) the new §16.1 adapter row's short deferral clause, already refuted at [spec-recheck.1.review-edit-sites.1].

FACT: the SPEC-3 carrier table's completeness claim SURVIVES a looser grep than round 12 ran, which closes the round-22 UNVERIFIED. I ran `grep -rniE "report[s]?( its| the| each)?[a-z ]{0,30}(on|at|per|each|every) (a )?(session )?release|on every session release|at each session release|per release|at session release|on each release|reports? the outcome"` over `spec/ docs/ schemas/ charts/`. Every hit carrying the withdrawn universal is already a table row: spec/05:453, spec/05:545, spec/04:692, spec/12:481, spec/12:494, docs/reference/adapter-contract.md:81, docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33, schemas/lenny-adapter.proto:308-309, :437, :452. The only new hits are correct non-carriers: docs/operator-guide/multi-tenancy.md:72 (states the cleanup runs, not the report), spec/07:72 `scrubPolicy` row (same), docs/runtime-author-guide/lifecycle.md:390 (whole-pod scrub), docs/reference/state-machines.md:248 (drain stamping, not the report), and release-cadence prose in spec/16:580, spec/25:4725, spec/26:237, docs/testing/index.md:26-27. EVIDENCE: the eleven carrier lines above; table at `.spec-changes.md` SPEC-3 §5.2 block.

FACT: every verbatim anchor the staging quotes matches the tree byte for byte, re-checked this round: spec/04:157 (§4.1), :674 (`DemoteSDK`), :686 (`Shutdown`), :692 (`ReportSessionScrub`), :854 (§4.7.9 step 5), spec/05:453, :545, :561, spec/06:148, :151-155 (fence, incl. the two-line `receiving_uploads ──→ running` entry), :234 (`resuming → cancelled` clause), spec/07:210, :213, :214, :414, spec/12:481, :494 (DDL comment leading whitespace verified with `cat -A`). EVIDENCE: those files at those lines.

FACT: every section anchor the staged blocks link to resolves to a real heading — 10.1 Horizontal Scaling, 4.6.1, 4.6.3, 4.7, 4.7.1, 4.7.9, 4.9, 5.2, 6.1, 6.2, 7.1, 7.2, 7.3, 7.4, 12.6, 15.1, 15.4, 15.4.2, 16.1. EVIDENCE: `grep -n "^#\+ <n>"` over the seven spec files.

FACT: `spec/04_system-components.md` §4.9 states the direct-mode lease-expiry timer's ARMING and FIRING (spec/04:1169) and nowhere states its cancellation, so SPEC-3's new `**Slot cleanup:**` act ("cancels the §4.9 direct-delivery-mode lease-expiry timers armed for that session") creates a single home rather than a second copy. Do not file it under the restated-rule rule. EVIDENCE: spec/04_system-components.md:1169; no `cancel`+timer hit in §4.9.

FACT: no diagram under `docs/assets/diagrams/` mentions `slot_cleanup`, `slot_assigned`, `Shutdown`, `ReportSessionScrub` or `sessions_served`, so the §6.2 per-slot fence edits and the §4.7/§12.6 re-keys open no SVG or ASCII-fallback edit site. `receiving_uploads` appears in `pod-warm-path.svg:26` and `sdk-warm-path.svg:29`, but as a SESSION state on the session state machine, not a per-slot sub-state. EVIDENCE: `grep -l` over docs/assets/diagrams/*.svg.

FACT: `docs/reference/adapter-contract.md` documents the gateway→adapter RPCs as a one-line-per-RPC table (lines 59-75) with NO request field tables anywhere, so adding `bind_attempt`/`mid_session`/`unconditional_teardown` to six requests opens no field-table edit site on that page; DOCS-2's four edits plus the bind-attempt paragraph are sufficient. `PrepareWorkspaceRequest`, `AssignCredentialsRequest` and `RunSetupRequest` appear in no file under `docs/` or `spec/`. EVIDENCE: docs/reference/adapter-contract.md:51-101; empty grep.

USEFUL [spec-recheck.1.review-edit-sites.1]: its identifier sweep (zero pre-existing hits for the six new names across spec/, docs/, schemas/, charts/) and its §11.3 stop-here FACT saved me two sweeps; I re-ran the identifier sweep and confirm it still returns zero.

USEFUL [spec.18.review-edit-sites.1] and [spec.22.review-edit-sites.1]: their refuted-candidate lists (§15.1 endpoint-precondition rows, `spec/07:208`, the `scrubPolicy` act enumeration) killed three candidates before I spent a read on any.

### [spec-recheck.4.review-fresh.1]

DECISION: returned an EMPTY findings list for the fresh-holistic lens on spec round 23 — BECAUSE the only delta since the last converged sweep is one Design-paragraph hunk (deleting "no runtime close and" from the `running`-boundary choice paragraph), the fix is correct and complete, and an independent re-derivation of the staged blocks against spec/, schemas/ and docs/ turned up nothing that meets the bar — ALTERNATIVES: filing the `schemas/lenny-adapter.proto:256-257` `GatewayControl` service comment as a missing SPEC-3 carrier row (see UNVERIFIED below); rejected as not clearly false and as a non-spec-lane remedy.

FACT: the Design delta is exactly one hunk and it is correct. Old text asserted "a slot the adapter never recorded is owed no runtime close and no cleanup-outcome report", which contradicted the staged §4.7 `Shutdown` row's runtime-teardown precondition (admission of the starting RPC, not the recording). The live text drops the runtime-close half and keeps only the report half, which agrees with the §5.2 scrub-model biconditional — EVIDENCE: spec-changes.md:32-37; §4.7 row spec-changes.md:273; §5.2 biconditional spec-changes.md:569.

FACT: every verbatim anchor SPEC-1 through SPEC-6 quotes still matches the tree byte for byte, re-checked this round on the expensive ones — EVIDENCE: spec/04:157 (§4.1 third sentence), spec/04:673 (`ConfigureWorkspace` idempotence, which rule 6 cites), spec/04:686 (`Shutdown` row opening), spec/04:674 (`DemoteSDK`), spec/04:692 (`ReportSessionScrub`), spec/04:415-417 (§4.6.1 claim-deletion bullets and the deleted one-session-only sentence), spec/05:453/:545/:561, spec/06:80/:148-157, spec/07:210/:213/:214, spec/12:481/:494.

FACT: `spec/06_warm-pod-model.md:148-157` is the general per-slot sub-state group; the `slot_assigned ──→ receiving_uploads` trigger is "workspace materialization begins for this slot", which is what makes the staged §6.2 pre-`running` paragraph's "every earlier stage of the §4.7.9 step-5 bind sequence ... leave the slot in `receiving_uploads`" true and keeps the connect-stage abandon in `slot_assigned`, where the proposal deliberately adds no edge — EVIDENCE: spec/06:149; spec-changes.md:989-994, :1002.

UNVERIFIED: `schemas/lenny-adapter.proto:256-257` (the `GatewayControl` service doc comment) says the service "carries the §5.2 per-slot and whole-pod scrub reports (ReportSessionScrub, ReportPodScrub) the adapter emits on release." It is a third proto site touching the report trigger, and the SPEC-3 carrier table lists only `:308-310` and `:451-452` for that file. I did NOT file it: the sentence lacks the "on every session release" universal the table's own exclusion criterion keys on, and it is already loose about `ReportPodScrub` (emitted at the occupancy-zero boundary, not at a release), so it reads as a service blurb rather than a statement of the trigger. A later docs-alignment or schema lens should decide whether SCHEMA-1 should re-key it anyway — EVIDENCE: schemas/lenny-adapter.proto:256-257; carrier table spec-changes.md:598-599; exclusion criterion spec-changes.md:582-586.

USEFUL [round 22 standing-context entry on markdown anchors]: the "every markdown anchor written in a staged block resolves" line saved a full slug sweep; I spot-checked five of the costly ones (§10.1 `#101-horizontal-scaling` at spec/10:3, §4.9 at spec/04:1099, §7.4 at spec/07:438, §4.7.1 at spec/04:659, §4.6.1 at spec/04:338) and all resolve.

USEFUL [the Traps block on the disposition table's residue column and the §15.4 rewrites]: it pre-empted two dresses I had started drafting (a per-row residue gap on table row 5, and a §15.4 completeness gap). Both are recorded as closed by deletion; do not re-derive.

### [spec-recheck.4.review-kubernetes.1]

DECISION: empty findings list for the Kubernetes-idiom lens on spec round recheck.4 — BECAUSE the only staged block that touches a CRD surface is SPEC-4 (spec/04 §4.6.1 occupancy-projection bullets, spec/06 §6.2 fence + prose), and it checks out on every idiom the lens owns: single-writer field manager, status-as-observed-state, level-triggered projection off an owned watch, no controller on a synchronous hot path — ALTERNATIVES: filing the new "phase the pod currently projects" projection input as a status-read-back anti-pattern, rejected because the tree already does exactly that and §4.6.3 makes the read legal.

FACT: the delta since spec-recheck-r2-prefix is a single hunk — the Design section's running-boundary paragraph drops "is owed no runtime close and", leaving "a slot the adapter never recorded is owed no cleanup-outcome report" — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:32-37. Nothing under this lens is affected by it.

FACT: `Sandbox.status.phase` has exactly one SSA field manager, `lenny-warm-pool-controller` (`ownership.WarmPoolController`), across every writer inside the WPC — the occupancy reconciler, the pod reconciler, the GC and the main controller all patch status under it — EVIDENCE: pkg/controller/warmpool/occupancy.go:268, pkg/controller/warmpool/pod_reconciler.go:691, pkg/controller/warmpool/controller.go:752, pkg/controller/warmpool/gc.go:466. So the staged §4.6.1 clause "the controller's own last level, which it may read back because it is the sole writer of that field" is true at both the spec level (spec/04_system-components.md:618, `Sandbox` `status.*` → WarmPoolController, "Sole writer of phase and conditions") and the code level. Do not spend a round re-deriving this: the two-writers reading of occupancy.go's `ok=false` branch (comment at pkg/controller/warmpool/occupancy.go:49-55, "never fights the warm-fill writer") is about two RECONCILERS, not two field managers.

FACT: `ProjectOccupancyPhase` genuinely takes the pod's live phase as an input (`occupancy.Current`, pkg/controller/warmpool/occupancy.go:33-34) and switches on it in the no-claim branch, so SPEC-4's re-key of the two claim-deletion bullets onto "the phase the pod projects at the claim DELETE" matches the tree, including `claimed` + no claim → `Draining` on a pool of either recycle setting — EVIDENCE: pkg/controller/warmpool/occupancy.go:76-79 and the no-claim branch at :110-125.

WATCHOUT: the staged §4.6.1 input enumeration keeps "the pool's `sessionPolicy`" while `recycle` is nested under `sessionPolicy` (spec/05_runtime-registry-and-pool-model.md:155-156) and `ProjectOccupancyPhase` reads no pool config at all. I did NOT file this: the clause is pre-existing text the staging explicitly leaves alone ("The rest of that sentence is unchanged"), surviving bullets (the `vm-restart` carve-out) still key on pool config, and the remedy would be an addition rather than a reduction. A later lens that wants to file it needs to show a surviving §4.6.1 bullet that reads no pool config anywhere.

FACT: every other staged block routes its state through Postgres (`agent_pod_state.sessions_served`), Redis, the pod annotation `lenny.dev/drain-request`, or the adapter's in-process registry — no staged text has the adapter or the gateway writing `Sandbox.status`, and the gateway's `delete` on `SandboxClaim` and `patch` on Pods that the failed-bind path uses are both already granted — EVIDENCE: spec/04_system-components.md:625 (gateway SA grants: "`get`/`patch` on `Pods` ... `create`/`get`/`delete` on `SandboxClaim`"), proposals/0081_*/0081_*.spec-changes.md:128-133.

### [spec-recheck.4.review-mechanism.1]

DECISION: returned an EMPTY findings list for the end-to-end mechanism lens on spec round (recheck) 4 — BECAUSE the only delta since the last converged spec-lane sweep is one clause deletion in the Design section ("is owed no runtime close and" struck from the `running`-boundary paragraph, spec-changes.md:32-37), and after re-tracing every staged flow end to end I found no predicate drift, unreachable trigger, bypassable gate, or granularity mismatch that meets the bar — ALTERNATIVES: I considered filing two candidates and rejected both, recorded below.

FACT: every verbatim anchor SPEC-1 through SPEC-6 quotes still matches the tree byte for byte as of today. Re-verified: spec/04_system-components.md:157 (§4.1 third sentence), :674 (`DemoteSDK` row), :686 (`Shutdown` row), :692 (`ReportSessionScrub` row), :854 (§4.7.9 step 5), :409-420 (§4.6.1 projection bullets); spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`), :488 (`Session count limit:` bullet, which §12.6's replacement read clause cites and which does exist), :545 (`Slot cleanup:` action list, reporting sentence, leaked-outcome sentence), :561 (`Whole-pod replacement trigger:` parenthetical); spec/06_warm-pod-model.md:93-96 and :146-155 (fence entries), :234 (mid-resume cancel bullet), :290 (`Client visibility:`); spec/07_session-lifecycle.md:23 (atomicity paragraph, and it does sit inside the fenced flow listing with the `(executionMode, isolationProfile, scrubPolicy summary)` continuation line directly below at :24), :210/:213/:214 (§7.2), :414 (§7.3 list item 4); spec/15_external-api-surface.md:1136 (`SETUP_COMMAND_FAILED` row, all four replaced sentences present verbatim on one line), :1469 (`SDK-warm demotion contract:`) with :1471 the next heading; spec/29_communication-scenarios.md:704-711 (step 13) and :586-591 (Preconditions). No anchor re-verification is owed by a later round unless the tree moves.

FACT: the §5.2 residue paragraph's claim that the whole-pod scrub "ends the slot's workspace tree and credential file" is exact against spec/05_runtime-registry-and-pool-model.md:461 (step 0 removes every `/run/lenny/slots/{sessionId}/credentials.json`) and :471 line 2 (`rm -rf /workspace/slots/*`). Do not file it as unsupported.

FACT: `ProjectOccupancyPhase` is exactly as SPEC-4 describes it — `pkg/controller/warmpool/occupancy.go:128-140`, `state.Reserved` with no claim returns `Idle`, `state.Claimed` with no claim returns `Draining`, and the doc comment at :57-63 states the "phase the pod sits in at the claim DELETE" rule the staged bullets adopt. SPEC-4's whole re-key rests on this and it holds.

MISTAKE (mine, avoided): I nearly filed that the §4.7 `Shutdown` row's graceful-shutdown signal, cited to §15.4.2, names a frame §29.4 step 13 calls `terminate` while §15.4.2/§15.4.3 call it `shutdown` (spec/15_external-api-surface.md:1780, :2017 vs spec/29_communication-scenarios.md:704-708). That two-name discrepancy predates this proposal and neither staged block introduces or depends on it. Not a finding.

WATCHOUT: a tempting but weak finding on this staging is that after SPEC-3 the gateway can enter `leaked` from the `Shutdown` response's clean-exit flag alone (table rows 4 and 5 file no report, and "a Section 7.1 reclaim the adapter does not answer enters it"), while §6.2's untouched `**`leaked` slot semantics.**` paragraph (spec/06_warm-pod-model.md, the paragraph after the fence) says the persistent leaked count "is the `lenny_adapter_leaked_slots` gauge" that the ADAPTER exposes — a slot the adapter never learns is leaked cannot appear in an adapter-sourced gauge. I did not file it: the gateway already marks a slot `leaked` without the adapter on the shipped reservation-release-failure path (the proposal says so itself at spec-changes.md:991-993), so the divergence is pre-existing rather than introduced, and the §6.2 paragraph states no trigger. EVIDENCE: spec-changes.md:633 ("A slot enters `leaked` on the gateway's reading of the report or of the response"), spec-changes.md:991-993, spec/06_warm-pod-model.md `**`leaked` slot semantics.**`. If a later round wants this, it needs to show the equivalence clause was true before the edits and is false after.

FACT (mechanism traces that came back clean, so a later mechanism lens need not redo them): rule 2's hold reaches non-entry-creating RPCs through the `**Admission.**` preamble's closing clause ("apart from the reclaim hold of rule 2"), so the §7.4 upload refusal has a rule to hang on; `Shutdown` is explicitly outside the hold, so a compensation is never deadlocked by the hold its predecessor opened; rule 8's re-resolve happens inside an already-admitted request and is its own critical-section step, so the hold does not block it; `ConfigureWorkspace` is governed by rules 1-9 AND starts a session, and rule 8's "including one that carries no token of its own, for which the token compared is the one the entry carried at admission" makes the empty-token entry compare equal to itself; the disposition table's asymmetry (a tree-removal failure sets clean-exit false on a pre-`running` row but not on a `running` row) is the proposal's declared keying at spec-changes.md:640-643 and is deliberate, not drift.

### [spec-recheck.4.review-performance.1]
DECISION: returned EMPTY, a fourth consecutive empty verdict for this lens — BECAUSE the whole delta since the last converged review is a three-line edit inside the Design section's running-boundary paragraph, deleting the clause "no runtime close and" so the sentence now reads "a slot the adapter never recorded is owed no cleanup-outcome report" (spec-changes.md:32-37). That edit removes text; it creates no write, no watch, no lock scope and no failure path, so it cannot move any quantity this lens measures — ALTERNATIVES: re-deriving the tier arithmetic over SPEC-3/SPEC-6, rejected because the standing context already records the result and nothing in the delta perturbs an input.
FACT: the delta this round is 16 diff lines total against the spec-recheck-r2-prefix snapshot, all of them in spec-changes.md's Design section — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:32-37
USEFUL [The staging adds NO per-session or per-request write to etcd, Postgres or Redis and net-REDUCES Postgres writes]: this standing line plus [The staged registry critical section puts NO slow act under the one registry lock] and [ProjectOccupancyPhase reading back the pod's own projected phase creates NO reconcile write loop] are the three quantitative results this lens owns. Together they cover the etcd write-amplification, lock-serialization and reconcile-pressure questions, so a future performance pass on an unchanged mechanism needs only to confirm the delta touches none of their inputs rather than re-run the arithmetic. Confirmed again this round against the SPEC-3 §12.6 block (spec-changes.md:772-830, the report-keyed increment strictly reduces write frequency versus the release-keyed one) and the SPEC-6 counters (spec-changes.md:1210-1222, two counters labeled `pool`/`k8s_pod_name`, bounded cardinality, one of them not scraped at all today).
WATCHOUT: the SPEC-6 adapter counter `lenny_slot_shutdown_untokened_entry_total` is staged as not scraped until the adapter metrics endpoint is wired — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1219-1220. Do not file its absence from a scrape budget as a capacity finding; the deferral is stated and carries a claim-register row per N4.

### [spec-recheck.4.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability lens on spec round 24 (spec-recheck 4) — BECAUSE the only spec-lane delta since r23 is the `running`-boundary redesign (boundary moved from "the runtime has been given the session" to "the adapter has recorded the session on the shared runtime process"), and I traced every recovery path it touches without finding a defect that meets the bar — ALTERNATIVES: I built and then dropped one candidate (rule 8's unconditional take-back racing a successor, below), on reachability grounds.

FACT: the boundary redesign makes the reclaim/record race well-defined rather than worse. The `Shutdown` decision (rules 11-14) and the start's resolve-confirm-record are BOTH members of the registry critical section, so they serialize: either the `Shutdown` wins and rule 8's confirmation fails, or the record wins and the slot is `running` when the `Shutdown` decides. There is no straddling window left. EVIDENCE: spec-changes.md:1039 ("On a `Shutdown`, the step is the decision of rules 11 through 14 and the deregistration that decision selects"; "On a request that starts a session, the step is the resolve, the confirmation and the recording ... that rule 8 states").

FACT: the boundary move does NOT orphan the runtime teardown for an in-flight start. The §4.7 `Shutdown` row keys the runtime teardown on the ADMISSION of the start, not on the record ("taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped"), while the disposition table keys the report and the clean-exit flag on `running`. The two predicates are deliberately different and both rows are quantified over "an act", so the runtime close is an act of a pre-`running` cleanup too. Any finding claiming a pre-`running` slot is owed no runtime close dies here — that half was already deleted from the Design paragraph this round. EVIDENCE: spec-changes.md:273 (the `Shutdown` row), :626-627 (the two pre-`running` table rows), :32-37 (the corrected Design paragraph).

WATCHOUT: a "permanently wedged identifier, no reclaimer" finding against the `Held for the life of the pod` rows looks strong and is not. The reclaimer is the windowed-failure replacement trigger: every refusal rule 2 returns is accounted as an ordinary transient slot failure by the gateway, `accountSlotFailure` covers the retry path, the `BindReservedSlot` branch and `resumeOnPod`, and `ceil(maxConcurrentSessions/2)` drains the pod. EVIDENCE: spec-changes.md:635 ("a bind refused this way is accounted by the gateway as an ordinary transient slot failure"), :86-92 (the accepted-failure bullet stating the same arithmetic).

WATCHOUT: the reclaim's idempotence under redelivery is already sound and is not worth a round. A replayed compensating `Shutdown` carrying the same token meets no entry and takes rule 11 (`absent`), which removes nothing, performs neither teardown, and reports a clean exit under rule 15, so a duplicate reclaim is a no-op that is still acknowledged clean. EVIDENCE: spec-changes.md:1060 (rule 11), :1064 (rule 15).

OPEN (dropped as below the bar, recorded so the next reliability agent does not re-derive it from scratch): rule 8's remedy — "It takes the session back off the shared runtime process" — is addressed by the session identifier, which `SlotID == SessionID` makes shared by every attempt, and it is stated OUTSIDE the registry critical section (the critical section lists only resolve, confirmation and recording). So in principle a lagging attempt A, stalled between `Runtime.Start` returning and the record, whose confirmation fails because a successor B now owns the entry, would close a session identifier B is running. I did not file it: reaching it needs A to be descheduled across an entire reclaim, cleanup, hold release and fresh bind sequence, the reclaiming `Shutdown` has already torn A's runtime down on the admission precondition so the take-back is belt-and-braces on every reachable ordering, and the area (rule 8, the take-back, the successor orderings) is exactly what the Edge-cases bullet `**A start that races the reclaim.**` already records. EVIDENCE: spec-changes.md:1052 (rule 8), :1039 (the critical section's membership, which excludes the take-back), :135-147 (the racing-start bullet).

FACT: the new ten-second-window commentary added this round checks out verbatim. `context.WithTimeout(context.Background(), 10*time.Second)` is at pkg/adapter/holdstate.go:201, shared by pass 2 across members, with the comment stating the non-last-close argument the commentary reuses. EVIDENCE: pkg/adapter/holdstate.go:197-205.

### [spec-recheck.4.review-security.1]

DECISION: returned an EMPTY findings list for the security lens on spec round 23 — BECAUSE the
round-23 delta (`diff` against `scratchpad/cp-snap/0081-opt2/spec-r23`) contains no change to a
control, a trust boundary, or a bound: it is (a) the `running`-boundary re-key from "the pod's
shared runtime process was given" to "reached `running` ([§6.2])" at four sites (Design :31-36,
scrub-model biconditional :569, disposition-table rows 1-3 :623-625, table commentary :642), which
is the same predicate under §6.2's own new definition at :1002 ("the adapter has recorded the pod's
shared runtime process as holding the session") and therefore still `runtimeLive`; (b) the deletion
of "is owed no runtime close and" from the Design running-boundary paragraph, which REMOVES the
only staged sentence that weakened the fail-closed teardown direction; (c) a new fence-annotation
replacement for `receiving_uploads ──→ running`, which drops a trigger and states no control; and
(d) one new commentary paragraph on the ten-second window. ALTERNATIVES: I considered filing the
adapter-side `lenny_slot_shutdown_untokened_entry_total` deferral as an unsurfaced degraded state,
and rejected it because the same residue is surfaced gateway-side by
`lenny_slot_compensation_superseded_total`: rule 13 (spec-changes.md:1062) answers `superseded` on
BOTH the different-token arm and the untokened-entry arm, and the gateway counter is keyed on the
`superseded` answer (SPEC-6 row, :1216).

FACT: the teardown direction the whole proposal rests on is stated once and is fail-closed in the
right direction: the runtime teardown's precondition is "the adapter's admission of that RPC, taken
at the moment of admission rather than at the moment the session reaches the runtime, so a start
still in flight is torn down rather than skipped", while the cleanup-outcome report is keyed on the
LATER `running` boundary. A lens tempted to file the two as inconsistent should stop: the asymmetry
is the recorded choice (over-approximate teardown, under-approximate counting). — EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:271 (§4.7 row) and :569 (scrub-model biconditional);
Design :16-19.

FACT: every mandatory gate the staging adds fails closed, re-derived independently this round.
Rule 1 and rule 10 answer `INVALID_ARGUMENT` on an unpaired field set and change nothing
(spec-changes.md:1045, :1059); rule 13's untokened arm answers `superseded` and removes nothing,
stated as fails-closed in the rule's own text (:1062); rule 2's hold refuses rather than admits;
and the `leaked` disposition is decided on the GATEWAY's reading of the report or the response
(:633), so no security bound in this staging is sourced from a pod self-report. — EVIDENCE:
spec-changes.md:1045,:1059,:1062,:633.

FACT: the ten-second commentary paragraph's three citations all check out.
`pkg/adapter/holdstate.go:201` is `closeCtx, cancel := context.WithTimeout(context.Background(),
10*time.Second)`; `git log -1 3997f502b` is "2026-08-22 Terminate every started session when the
coordinator hold times out"; and `.claude/rules/code-best-practices.md` does say "A default not
fixed by the spec must be overridable by a flag or config value". Nothing to file under (a). —
EVIDENCE: pkg/adapter/holdstate.go:201; .claude/rules/code-best-practices.md (Configuration and
compatibility).

FACT: the new `receiving_uploads ──→ running` fence replacement strands no gate and no unstaged
carrier. `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:34` pins the bare
edge string `"receiving_uploads ──→ running"`, which survives an annotation-only replacement;
`docs/reference/state-machines.md:235` is staged by DOCS-1 (non-spec-changes.md:2419); and
`pkg/sandbox/slotstate/slotstate.go:100`'s mirroring doc comment is staged by CODE-3
(non-spec-changes.md:785, files-touched :3812). An earlier round rejected this very reword on the
ground that it "strands pkg/sandbox/slotstate's `Running` doc comment"; that ground is now
discharged. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:34;
pkg/sandbox/slotstate/slotstate.go:100; non-spec-changes.md:785,:2419,:3812.

USEFUL [Settled, "ROUND 22'S TWELVE LENSES ALL RETURNED EMPTY … the security and kubernetes
candidate spaces are declared exhausted by their own lenses"]: it let me scope this pass to the
delta plus a re-derivation of the fail-closed gates rather than a fresh full sweep, and the
re-derivation agreed. The security candidate space stays exhausted after round 23.

### [spec-recheck.4.review-single-source.1]

DECISION: filed exactly one finding, on the §6.2 fence — BECAUSE the delta reduced the sibling
`receiving_uploads ──→ running` entry to a pointer at the pre-`running` paragraph
(spec-changes.md:949) while the newly inserted `receiving_uploads ──→ slot_cleanup` entry
(spec-changes.md:934-936) still spells out the whole edge condition that same paragraph states
(spec-changes.md:1002) — ALTERNATIVES: rejected three near-misses, each recorded below.

FACT: the two completion predicates each have exactly one definition site and no third one.
`acknowledged clean` is defined only in the staged §7.1 paragraph (spec-changes.md:377);
`completed` only in the §5.2 reclaim-hold paragraph (spec-changes.md:635). A full-file grep for
`acknowledged clean|clean exit|clean-exit|completed` returns no other defining site — every other
hit is a citation, a table column header, or a scenario narration.
EVIDENCE: proposals/0081_.../0081_....spec-changes.md:377,635,1064

FACT: the fence in spec/06 is NOT uniformly pointer-ized after the proposal applies: the
`running ──→ slot_cleanup` entry keeps its shipped `(session completes or fails)` trigger and the
proposal does not touch it (spec/06_warm-pod-model.md:154). So "the fence carries only pointers"
is not the ground for the finding; the ground is that the inserted entry's trigger and the
paragraph directly below the fence state one rule twice.
EVIDENCE: spec/06_warm-pod-model.md:149-155

WATCHOUT: three sites look like (g) copies and are not. (1) Rule 8 restates the one-report rule
but attributes it to §5.2 in the same clause ("because the slot never reached `running` and
[Section 5.2] files at most one cleanup-outcome report per session release") — that is
name-plus-cite, which the lens definition excludes. (2) The §16.1 `lenny_slot_compensation_superseded_total`
row restates rule 15's meaning of `superseded` almost word for word, but a metric catalog row has
to say what it counts and the row cites §4.7.1. (3) The §4.1 replacement's scrub clause is
described by its own commentary as restating rule 10, but rule 10 states a refusal and the clause
states the scrub trigger; neither is derivable from the other alone.
EVIDENCE: spec-changes.md:1057 (rule 8), :1216 (metric row), :230 and :243-246 (§4.1 clause and its commentary)

UNVERIFIED: the SPEC-3 commentary paragraph added in this delta (spec-changes.md:664-675) restates
the three arms of the staged close-bound sentence ("The figure bounds one of the three cases the
sentence states ... the other two take the reclaiming `Shutdown`'s carried grace or that request's
own deadline"). I judged it rationale/provenance rather than a copy and did not file it. A later
prune pass should decide whether the caller's round-4 directive ("Add no rationale sentence to ...
any SPEC-n commentary") retires it. EVIDENCE: spec-changes.md:635 (the rule), :670-672 (the restatement)

### [non-spec-recheck.1.fix.1]

The previous fixer opened no pass subsection, so this shard carries the correction.

- DECISION: corrected the one surviving "fail-closed row" to "fail-closed arm" at
  spec-changes.md:1301, in the `spec/16_observability.md` carrier bullet. CODE-1 contains no table,
  so "row" named an artifact the proposal no longer holds, while the four sibling statements of the
  same fact (non-spec-changes.md:244, :303, :322, :2057) name the `shutdownReclaimOutcome` arm. The
  word is the whole edit: no staged spec text, no deliverable and no §16.1 row content moved.
  EVIDENCE: `grep -n "fail-closed row\|fail-closed arm"` over the proposal directory now returns the
  four non-spec sites and the archive alone.

### [non-spec-recheck.1.fix-G1.1]

DECISION: deleted the "For DOCS-3" Testing sentence claiming the deliverable lands beside SPEC-5 in the same step, adding nothing in its place — BECAUSE the implementation checklist is the one home of step order and lane (S1 spec carries SPEC-5, S7 docs carries DOCS-3 with "Depends on: S1, S2, S4, S5"), and a Testing-section sentence about when a deliverable lands is commentary on a rule another file owns — ALTERNATIVES: rewriting it as "lands in the docs step, which depends on SPEC-5's step" (rejected: plants a second copy of the checklist's dependency edge that drifts the next time steps are renumbered); merging S1 and S7 so the sentence becomes true (rejected: a step carries exactly one lane, so a step naming a spec and a docs deliverable has no handler).
DECISION: reduced the non-spec Edge-cases bullet "An attempt cannot fence itself" to a fourth pointer entry in the list that already reduces its three siblings — BECAUSE non-spec-changes.md's Edge-cases lead-in declares the spec-changes Edge-cases section the single home of the accepted failure modes of the contract, and the bullet was the whole of spec-changes.md's "An attempt that recreates its own entry after an unconditional teardown removed it" written a second time (same condition, same consequence, same two closures) — ALTERNATIVES: keeping a code-only tail on FinalizeWorkspace/allowCreate (rejected: those nouns illustrate the spec bullet's own "a request that may legitimately be an attempt's first entry-creating RPC", and the tier-1 pin is already in the orderings table); deleting the spec-changes bullet instead (rejected: reverses a settled home); a cross-reference sentence leaving both full statements (rejected: two full statements plus a link is still the copy that drifts).
FACT: the pointer entries in the non-spec Edge-cases list carry the spec-changes heading verbatim in bold after a colon, so `grep` on a spec-changes heading finds every citing site. Keep that form when adding one. EVIDENCE: 0081_..._.non-spec-changes.md Edge-cases pointer list, entries for "A retry that meets the reclaim hold spends an attempt on it" and "A compensation lost to a gateway crash leaves an entry no attempt can use".
FACT: no site outside the review log mentioned either corrected text; `grep -rn "fence itself\|beside SPEC-5"` over the proposal directory now returns nothing outside the log, so neither fix cascaded into the summary, the checklist or the problem statement. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/.
WATCHOUT: DOCS-2's landing sentence in the same file is phrased "DOCS-2 lands beside DOCS-1, after SPEC-1, SPEC-3 and SPEC-5 have landed the contract it mirrors". DOCS-1, DOCS-2 and DOCS-3 are all in S7, so "beside DOCS-1" is true and "after SPEC-5" is true. Do not file it as the same defect. EVIDENCE: implementation-checklist.md S7 line naming DOCS-1, DOCS-2, DOCS-3.

### [non-spec-recheck.1.fix-G2.1]
DECISION: closed the DOCS-2 link-form finding by DELETING the `:393` precedent sentence outright, leaving the paragraph to end at the `.claude/rules/doc-content.md` citation — BECAUSE the rule the sentence appealed to is already stated and cited in the sentence before it, and the caller directive fixes commentary that props up a rule stated elsewhere by deletion rather than by editing it into agreement — ALTERNATIVES: rewriting the clause to state the divergence explicitly (adds a rationale sentence to commentary this run is shrinking, and no DOCS-2 reader needs the divergence); staging a fix to `docs/reference/adapter-contract.md:393` so the precedent becomes true (out of scope, unfiled, and the tree is not this proposal's to edit).
FACT: `docs/reference/adapter-contract.md` has exactly one absolute specification link, at line 393, and its form is the MIRROR IMAGE of DOCS-2's staged links: the number rides in the link text (`Spec §15.4 -- Translation Fidelity Matrix`) and is absent from the anchor (`#translation-fidelity-matrix`). DOCS-2's two staged links carry the heading in the text and the number only in the anchor. `grep -n "blob/main/spec" docs/reference/adapter-contract.md` returns line 393 alone — EVIDENCE: docs/reference/adapter-contract.md:393
WATCHOUT: line 393 is a pre-existing `doc-content.md` violation (a section number in published prose) and it is NOT licence. Do not "align" DOCS-2's staged links with it by moving `§4.7.1` or `§15.4` into the link text; that is the tempting local edit and it is the thing the rule forbids. The archive already records this trap twice — EVIDENCE: review-log-archive.md:18496, :50981
FACT: the false attribution had exactly one site in the live proposal directory. `grep -rn '`:393`\|already uses for a specification'` over the proposal directory, excluding the archive, returns only the deleted sentence, so no summary, checklist, problem-statement or spec-changes text needed a matching edit — EVIDENCE: non-spec-changes.md, DOCS-2 prose block preceding the `Shutdown` row replacement

### [non-spec-recheck.1.fix-G3.1]
FACT: the sentence boundary inside the shipped `ReportSessionScrub` RPC comment falls MID-LINE. `schemas/lenny-adapter.proto:310` reads "  // `maxConcurrentSessions > 1` and recycling cases alike. The outcome is", so the opening sentence and the outcome sentence share a physical line. Any SCHEMA-1 before-block quoting whole proto lines must give those three trailing words to one block or the other. — EVIDENCE: schemas/lenny-adapter.proto:308-313
DECISION: gave "The outcome is" to the OPENING-sentence block, by extending its last quoted line to the whole of proto line 310, and reworded its intro to say the block is the opening sentence plus the words that open the next sentence on the same physical line. The outcome-sentence block's intro changed from "the sentence that reads, verbatim" to "the text that reads, verbatim". Neither replacement text changed. — BECAUSE every quoted before-block in SCHEMA-1 reproduces whole physical proto lines, and this keeps that true with no rewrap. — ALTERNATIVES: prepending "The outcome is" to the RELEASED/LEAKED block (rejected: forces a rewrap away from the file's real line breaks, reintroducing a non-verbatim block, which is the defect itself); rewriting the replacement to "// The outcome is RELEASED or LEAKED, on the terms §5.2 states for the cleanup." (rejected: diff with no gain, reopens settled wording); reflowing the whole comment as one before/after block (rejected: enlarges the quoted surface over sentences the paragraph leaves unedited).
WATCHOUT: when SCHEMA-1's ReportSessionScrub replacements are applied in file order, the surviving comment reads "... and for no other release." then "// RELEASED and LEAKED are the outcomes §5.2 states for the cleanup." A later round that shortens either replacement must re-check that the join still reads as two sentences. — EVIDENCE: non-spec-changes.md SCHEMA-1, "**The scrub-outcome and report-trigger comments.**"
FACT: no other file in the proposal directory (summary, checklist, problem statement, spec-changes) quotes this sentence or its boundary; `grep -rn "recycling cases alike\|The outcome is"` over the directory excluding the review log returns only the edited block and one unrelated compensation sentence. The spec-changes carrier table names the comments by identity rather than by literal text, so it stays true. — EVIDENCE: non-spec-changes.md:968, :2267
USEFUL [archive:47252-47254]: an earlier round had already spotted this orphan and it was not closed then; this round closed it.

### [non-spec-recheck.1.fix-G4.1]

DECISION: Re-keyed the retired glosses in `pkg/sandbox/slotstate/slotstate.go` onto `see §6.2 "Pre-running slot cleanup"` and `see §5.2` pointers rather than onto DOCS-1's reader-facing sentences — BECAUSE a Go comment may cite the spec (`// spec:` is the project's own form), so the citation form is available and a restatement would be a second full statement of a rule the staging just consolidated into §5.2 and §6.2; DOCS-1's wording exists only because a reader-facing page may not carry a spec citation. ALTERNATIVES: (a) copy DOCS-1's sentences into the Go comments, rejected as a second rule statement; (b) add a separate CODE-n deliverable for slothealth, rejected because it renumbers the CODE series for two one-line deletions that share the same cause and land in the same step; (c) delete the edge-list comment entirely, rejected because it is the only place a reader of that package learns which spec section owns the edge set.

FACT: `pkg/gateway/runtime/slothealth/slothealth.go` states the withdrawn cleanup-timeout trigger twice, in the `event` doc comment (`:33`) and in `RecordLeak` (`:110`), and the file's other comments (package doc, `DefaultWindow`, `Tracker`) state persistence rather than a trigger, so they are untouched — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:25-38, :109-121.

FACT: CODE-3's own new edge falsifies the `SlotCleanup` constant doc, which calls it "the post-execution cleanup sub-state (task completed or failed, per-slot cleanup runs)". A finding pass that checks only what SPEC-3 and SPEC-4 withdraw will miss it — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:38-40.

WATCHOUT: `OccupiesSlot` in the same file quotes a §6.2 sentence verbatim about a leaked slot remaining counted in `active_slots`. SPEC-4 leaves the `**leaked slot semantics.**` paragraph alone, so that quote stays valid; do not sweep it with the trigger glosses — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:80-87.

WATCHOUT: the staged §6.2 fence uses the in-section form `(see the pre-`running` slot cleanup paragraph below)`, which does not carry across into a Go file. The Go comments therefore name the section and the heading instead. A later round comparing the two texts character by character will read that as a divergence; it is not — EVIDENCE: 0081...spec-changes.md SPEC-4 §6.2 fence edits.

### [non-spec-recheck.1.fix-G5.1]
DECISION: closed the G5 finding by DELETING CODE-5's restatement of the hold's duration and citing SPEC-3's §5.2 `**Slot-identifier reclaim hold.**` paragraph plus the disposition table instead — BECAUSE commentary is not a rule home, and the defect was a paraphrase that had already drifted (it quoted §5.2's bound on the cleanup's runtime CLOSE as if it bounded the HOLD) — ALTERNATIVES: the reviewer's literal suggested sentence, rejected because it installs a second full statement of hold duration in code-deliverable commentary and reduces §5.2's three-arm close bound to two arms; deleting the whole closing paragraph, rejected because the decision not to stage an in-gateway wait loop lives nowhere else; moving the rationale into §5.2, rejected because it is a pkg/gateway staging decision rather than platform behaviour.
FACT: §5.2's reclaim-hold paragraph bounds the CLEANUP'S CLOSE with three arms (the `Shutdown`'s carried graceful window, that request's own deadline, and a ten-second window for the §10.1 hold-timeout termination, which runs under no request). It states NO bound on the hold itself; the hold ends only on a cleanup that completed, and four disposition-table rows answer "Held for the life of the pod" — EVIDENCE: 0081_....spec-changes.md:635 and the table rows at :624,:625,:627,:629.
WATCHOUT: the accepted-failure-mode bullet in the spec-changes Edge-cases section says a gateway-side wait "holds the client's request open for the same window", where "the same window" resolves through the preceding sentence "How long the hold lasts is stated per cleanup in the hold column of SPEC-3's §5.2 disposition table". That is a pointer rather than a bound, so it is not the same defect and it was deliberately left alone; do not re-key it, and do not add a duration clause there — EVIDENCE: 0081_....spec-changes.md:124-127.
FACT: no cascade existed. `grep -n "bounded by the graceful window\|gateway-side wait\|wait-and-retry"` over the four non-log proposal files returns only the edited paragraph, the §5.2 close-bound sentence and the Edge-cases bullet above. The summary, implementation checklist and problem statement carry no statement of hold duration bounds, so none was edited, and no deliverable was added, removed, merged, split or resequenced. The staged spec-changes file was NOT edited.

### [non-spec-recheck.1.fix-G6.1]
DECISION: Re-keyed the tier-1 `Shutdown` test bullet onto the arms of `shutdownReclaimOutcome` BY RULE NUMBER (rule 11's no-entry arm on each request form, rule 12, rule 13's two arms, rule 14) rather than onto a row set — BECAUSE staged §4.7.1 rules 11 to 15 are the cascade's one home, and keying on rule numbers survives edits to CODE-1's Go text — ALTERNATIVES: reintroducing a rule table inside CODE-1 (a second full statement of the cascade, and the restructure deleted it); keying on the Go case expressions (pins coverage to an implementation detail); deleting the bullet and relying on CONF-1 (CONF-1 is tier 3 and tier 10 against the contract and cannot observe the on-disk survival assertions this bullet owns).
DECISION: Closed the "single exception" contradiction by citation rather than by recount: the doc-comment bullet now reads "Name the returns that bypass it, the ones the `answerShutdown` paragraph above enumerates" — BECAUSE the count drifted only because the exception set was written twice, and the enumeration stays solely at the `answerShutdown` rationale paragraph — ALTERNATIVES: "Name the two exceptions" (correct today, re-creates the duplicate that produced the finding); deleting the bullet (it is the instruction for the shipped Go doc comment); changing the enumeration to agree with "single" (the tree refutes it: `pkg/adapter/session.go:228-231` returns on an empty session id before the recycle scrub at `:288-290`).
FACT: CODE-1 contains no markdown table; the Shutdown cascade is `shutdownReclaimOutcome`, a switch whose arms map one to one onto rules 11 through 14, with the mapping stated in prose just above the function — EVIDENCE: 0081_..._.non-spec-changes.md:212-262.
FACT: The old six-row table's two `absent` rows collapse into rule 11's single `!ok` arm, so "six rows" and the arm set never corresponded; any later text that counts either is wrong — EVIDENCE: 0081_..._.non-spec-changes.md:246-262 against `git show 14f82426c:proposals/0081_.../0081_....non-spec-changes.md` lines 226-234.
WATCHOUT: "row" is still correct in two places in non-spec-changes.md and must not be swept. The tier-1 bullet "The two-field precondition, four rows" names the four request-field combinations, and the metrics deliverable's "untokened-entry row under that page's `## Adapter metrics` table" names a docs table row — EVIDENCE: 0081_..._.non-spec-changes.md:2921 and :2051.
DEFERRED [spec-changes.md]: the staged carrier list still says the §16.1 rows are for the counters "the compensation's caller and the adapter's fail-closed row emit". The vestigial word is "row"; what is true is that the adapter counter is emitted from `answerShutdown`'s fail-closed ARM. Not landed because the supplied design scoped the sweep to the four non-spec sites and this loop prefers leaving the converged spec-changes file alone — EVIDENCE: 0081_..._.spec-changes.md:1301.

### [non-spec-recheck.1.fix-G7.1]
DECISION: Closed the summary's `stageWorkspace` bullet by reduction — replaced its first two sentences with one correctly scoped sentence naming the two pre-send arms and citing CODE-4, and deleted the `absent`/rule-11/transient clause outright — BECAUSE that outcome mapping already has a home in CODE-4's rationale and its test bullet, so re-scoping it in the summary would install a second full statement. ALTERNATIVES: apply the reviewer's literal suggestion and keep the following outcome sentence (rejected: keeps commentary as a rule home); delete the bullet entirely (rejected: the "exactly one `Shutdown`" test caution is stated nowhere else); edit the three sibling sites to match (rejected: all three are already correctly scoped and stay true).
FACT: `stageWorkspace` sends `PrepareWorkspace` as its last statement and returns that call's error as its own, so with uploads present its failure can be a post-send failure and the entry exists. Its only pre-send failure arms are the source rewrite and the blob-store path. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1283 (signature), :1303 (nil blob store), :1327 (`cl.PrepareWorkspace`).
WATCHOUT: do not "fix" this by restoring the older qualifier "on an upload-free plan". `stageWorkspace` cannot fail on an upload-free plan: the rewrite returns the plan unchanged, the blob-fetch loop runs only for `uploadFileRefs`, and the `PrepareWorkspace` send is guarded by `len(uploads) > 0`. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1284-1332.
FACT: the three sibling statements of this fact are correctly scoped and need no matching edit — the staged spec Edge-cases bullet, CODE-4's rationale, and the CODE-4/CODE-5 test bullet. EVIDENCE: spec-changes.md:74-80, non-spec-changes.md:1132-1137, non-spec-changes.md:3141-3145.

### [non-spec-recheck.1.fix-design-G1.1]

DECISION: DOCS-3's co-location sentence at non-spec-changes.md:3513 is DELETED outright rather than rewritten into agreement with the checklist — BECAUSE the implementation checklist is the one home of the execution sequence (S1 spec lane, S7 docs lane, S7 "Depends on: S1, S2, S4, S5"), so a Testing-section sentence stating when a deliverable lands is commentary on a rule another file owns; the preceding two sentences already carry the whole reason no gate is added, and the paragraph is complete without it — ALTERNATIVES: the reviewer's suggested replacement ("lands in the docs step, which depends on SPEC-5's step ...") was rejected, because it makes the Testing section a second statement of the checklist's dependency edge, which drifts the next time the step numbering moves.

DECISION: the "An attempt cannot fence itself" bullet at non-spec-changes.md:3569-3575 is deleted and replaced by a fourth entry in the pointer list at :3560-3567, in the same form as the three siblings (short description, colon, the bold heading as spec-changes.md writes it: **An attempt that recreates its own entry after an unconditional teardown removed it**) — BECAUSE :3556-3558 already declares the spec-changes Edge-cases section the single home of the accepted failure modes, and the bullet is the whole of spec-changes.md:167-176 restated; nothing in it is code-only (`FinalizeWorkspace` and `allowCreate` are illustrations of the spec bullet's "a request that may legitimately be an attempt's first entry-creating RPC") — ALTERNATIVES: keeping a reduced code-only tail naming the tier-1 case was rejected, because the tier-1 orderings table row at :3017 already carries that pin.

FACT: the pointer list at non-spec-changes.md:3560-3567 states no count of the cases it holds, so adding a fourth entry falsifies nothing — EVIDENCE: proposals/0081_*/*.non-spec-changes.md:3556-3567

FACT: the code-only bullets that follow (leaked entry / MCP arming at :3576, rolled-back start at :3580) are genuine code cases and stay — EVIDENCE: proposals/0081_*/*.non-spec-changes.md:3576-3590

WATCHOUT: DOCS-2's landing sentence at non-spec-changes.md:2519 is already phrased as "after SPEC-1, SPEC-3 and SPEC-5 have landed" and is true; do not sweep it while fixing :3513 — EVIDENCE: proposals/0081_*/*.non-spec-changes.md:2519

### [non-spec-recheck.1.fix-design-G2.1]
DECISION: DOCS-2's precedent clause at non-spec-changes.md:2548-2549 is deleted, not rewritten — BECAUSE the doc-content.md citation already carried in the preceding sentence is the whole justification the staged links need, and the appeal to `:393` is both false and (per caller directive) commentary that is not a rule home — ALTERNATIVES: rewrite the clause to state the divergence from :393 explicitly (rejected: adds a rationale sentence to commentary the directive says to shrink, and the divergence matters to nobody reading the deliverable); keep the clause and change the shipped line 393 to match (rejected: out of DOCS-2's scope, and no finding was filed against that line).
FACT: docs/reference/adapter-contract.md:393 is the page's ONLY absolute spec link, and its form is the mirror image of the staged ones: `§15.4` in the link text, no number in the anchor `#translation-fidelity-matrix`. Verified by `grep -n "blob/main/spec" docs/reference/adapter-contract.md` returning line 393 alone. — EVIDENCE: docs/reference/adapter-contract.md:393
WATCHOUT: the staged links themselves (non-spec-changes.md:2581, anchors `#471-role-and-gateway-rpc-contract` and `#154-runtime-adapter-specification`) are correct and must not be touched; only the attribution clause is wrong. A fixer that "aligns the staged links with the precedent" would write `§4.7.1`/`§15.4` into published link text, which doc-content.md forbids. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md:2581


### [non-spec-recheck.1.fix-design-G3.1]
DECISION: Close the "The outcome is" orphan by extending the OPENING-sentence quote (non-spec-changes.md:2264-2266) so its last line is the whole of proto line 310, "  // `maxConcurrentSessions > 1` and recycling cases alike. The outcome is", and reword its intro at :2262 so it no longer claims to be the opening sentence alone; the existing replacement at :2272-2273 then consumes the orphan unchanged. Also reword the intro at :2226 from "the sentence that reads, verbatim" to "the text that reads, verbatim", since that block is now the sentence's remainder. — BECAUSE both staged blocks are quoted as whole physical comment lines, and only this direction keeps that property: proto:310 ends with the three orphan words, so extending the FIRST quote needs no rewrap. — ALTERNATIVES: extend the SECOND quote (:2229) to begin "The outcome is RELEASED when ...", which the finding suggests; rejected because the quote is line-based and prepending three words forces a rewrap of :2229-2230 away from the file's actual line breaks (or an ellipsis), which is exactly the kind of non-verbatim block that produced this finding. Also rejected: changing the replacement text to "// The outcome is RELEASED or LEAKED, on the terms §5.2 states for the cleanup." — the existing replacement already reads, and rewriting it is diff for nothing.
FACT: schemas/lenny-adapter.proto:308-313 wraps the ReportSessionScrub comment so that the sentence boundary falls MID-LINE at :310 ("... recycling cases alike. The outcome is"). Any staged quote/replacement pair over that comment that is keyed to sentences rather than to whole lines will orphan or duplicate words. — EVIDENCE: schemas/lenny-adapter.proto:310
WATCHOUT: the two other replacements in the same paragraph (SESSION_SCRUB_OUTCOME_RELEASED at :2240-2247, ReportSessionScrubRequest at :2276-2288) already match whole lines exactly. Do not touch them. — EVIDENCE: proposals/0081_.../...non-spec-changes.md:2240
MISTAKE: an earlier round already flagged this same orphan (review-log-archive.md:47252-47254) and it was not closed; the cost is one repeated finding.

### [non-spec-recheck.1.fix-design-G4.1]
DECISION: CODE-3 widens to cover the whole comment block it already opens plus `pkg/gateway/runtime/slothealth/slothealth.go`, and every retired gloss is re-keyed to a POINTER (`see §6.2 "Pre-running slot cleanup"`, `see §5.2`) mirroring the staged fence's own reduction, never to DOCS-1's reader-facing wording — BECAUSE code may cite the spec (`// spec:` in code-best-practices) while a docs page may not, so DOCS-1's sentence is the one licensed restatement and a copy of it in Go comments would be a second full statement of a rule whose home is §5.2/§6.2 — ALTERNATIVES: re-key the Go comments onto DOCS-1's wording (rejected: second statement); a new CODE-n for slothealth (rejected: renumbering plus splitting one cause across two deliverables); leave slothealth alone (rejected: the withdrawn timeout predicate is stated twice in that file).
FACT: `pkg/sandbox/slotstate/slotstate.go` states the §6.2 fence TWICE — the per-constant docs at :30-48 and the `ValidTransitions()` edge list at :95-104. A fix that touches only the edge list leaves the constant docs asserting the same retired triggers. EVIDENCE: pkg/sandbox/slotstate/slotstate.go:33-47, :95-104
FACT: CODE-3's own new edge falsifies the `SlotCleanup` constant gloss "(task completed or failed, per-slot cleanup runs)", which is not in the finding's list. EVIDENCE: pkg/sandbox/slotstate/slotstate.go:38-40
WATCHOUT: three glosses in that edge list stay verbatim (`slot_assigned → receiving_uploads`, `running → slot_cleanup`, `running → failed`); SPEC-4 replaces only three fence entries, so re-keying the other three would desynchronise the comment from the post-edit fence. EVIDENCE: 0081_...spec-changes.md:930-975
WATCHOUT: `OccupiesSlot`'s quoted §6.2 leaked-semantics sentence, the slothealth package doc, `DefaultWindow` and the `Tracker` docs state PERSISTENCE, not the entry trigger, and stay true; only slothealth.go:33 and :110 state the withdrawn cleanup-timeout trigger. EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:5-11, :26-29, :33, :110
FACT: CODE-3's scope is restated in four places, all of which move with the widening: non-spec-changes.md:785-797 (the deliverable), :3812 (files touched), implementation-checklist.md:31 (S8), summary.md:1032 (deliverable roster). EVIDENCE: grep "CODE-3" across proposals/0081_*/


### [non-spec-recheck.1.fix-design-G5.1]
DECISION: CODE-5's closing paragraph loses its duration clause entirely and cites §5.2 instead of restating any bound — BECAUSE the reviewer's own suggested wording still restates §5.2's three-way close bound plus the life-of-the-pod arm inside CODE-5, which is a second full statement of a rule whose one home is the staged §5.2 reclaim-hold paragraph and its disposition table; the conclusion (no in-gateway wait) needs only "the hold's duration is not request-scoped", which a citation carries — ALTERNATIVES: (a) apply the reviewer's sentence verbatim, rejected as a second rule home; (b) make CODE-5 agree by copying the life-of-the-pod arm, rejected same reason; (c) delete the whole closing paragraph, rejected because the "why no gateway-side wait is staged" rationale lives nowhere else and rationale is what a code-lane deliverable alone knows.
FACT: the erroneous sentence is a mis-attribution, not a disagreement about the mechanism: the three-way bound it quotes is §5.2's bound on the CLEANUP'S RUNTIME CLOSE, and §5.2 states a third arm CODE-5 omits (a ten-second graceful window for the §10.1 hold-timeout termination, which runs under no request) — EVIDENCE: 0081_*.spec-changes.md:635 (reclaim-hold paragraph, "The cleanup's close of the session on the pod's shared runtime process is bounded by ... and, for the Section 10.1 hold-timeout termination, which runs under no request, by a graceful window of ten seconds."); the erroneous site is 0081_*.non-spec-changes.md:1318-1320.
WATCHOUT: a fixer that "completes" CODE-5's bound by adding the missing ten-second arm has made the duplication worse. The correct direction is fewer words at that site, not more.
FACT: no other site in the staging carries the wrong framing. summary.md:~287, CODE-6 and the spec file's Edge-cases section all state the life-of-the-pod arm correctly, and the tier-7a hold test asserts admission ordering during a parked cleanup rather than a total duration, so it stays true. review-log-archive.md carries the wrong framing historically and is immutable.
### [non-spec-recheck.1.fix-design-G5.1]
DECISION: Fix CODE-5's closing paragraph (non-spec-changes.md:1318-1320) by DELETING its restatement of the hold's duration and citing §5.2's reclaim-hold paragraph instead, rather than by rewriting the restatement into a correct three-arm one — BECAUSE the §5.2 **Slot-identifier reclaim hold.** paragraph plus the disposition table is the single home of hold duration (spec-changes.md:616-635), and commentary in a code deliverable cites the home and adds only what it alone knows (here: that no in-gateway wait is staged, and why). ALTERNATIVES: (a) the reviewer's literal suggested_fix, which writes a correct but full second statement of both duration arms into CODE-5 — rejected as a second rule home and exactly the accretion the caller directive bans; (b) delete the whole paragraph — rejected, the decision not to stage a gateway-side wait is CODE-5's own and is stated nowhere else.
FACT: the erroneous "bounded by the graceful window ... plus the removal of the slot's directories" clause in CODE-5 is §5.2's bound on the CLEANUP'S RUNTIME CLOSE, not on the hold; §5.2 also gives the hold-timeout termination a third arm (a ten-second graceful window, no request), which CODE-5's two-way paraphrase already dropped — a second reason not to paraphrase it at all. EVIDENCE: spec-changes.md:635.
FACT: no other site in the proposal carries the wrong request-scoped-bound framing; summary.md:287-288, CODE-6 and the spec-changes Edge-cases section all state the life-of-the-pod arm correctly, so the fix is one paragraph and cascades nowhere. review-log-archive.md carries the old framing but is an immutable historical record.
WATCHOUT: do not touch the §5.2 staged block while closing this; the duration statement there is the correct one and is settled.

### [non-spec-recheck.1.fix-design-G6.1]
FACT: CODE-1 (non-spec-changes.md:161-605) contains NO markdown table; `awk 'NR>=161&&NR<=605 && /^\|/'` returns nothing. The Shutdown cascade is `shutdownReclaimOutcome` with five switch arms mapped onto §4.7.1 rules 11-14 at :216-222 and :246-262. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:246-262
FACT: the deleted six-row table survives only in history (`git show 14f82426c:proposals/0081_.../...non-spec-changes.md` around line 226). The tier-1 bullet at :2886-2888 is the only site left pointing at it; `grep -n "six-row\|one case per row"` returns exactly that one line. — EVIDENCE: non-spec-changes.md:2886
FACT: vestigial "row" vocabulary from that table survives at :303, :322 (Go comments, "the fail-closed row"), :2015 (metrics table cell, "incremented from CODE-1's fail-closed row") and :2888 ("The untokened-entry row"). Each now denotes an ARM of `shutdownReclaimOutcome`. The first tier-1 bullet's "four rows" at :2881 is a different thing (the two-field truth table) and must NOT be swept. — EVIDENCE: non-spec-changes.md:2881
DECISION: re-key the tier-1 bullet onto the arms by RULE NUMBER (rule 11's no-entry arm on each request form, rule 12, rule 13's two arms, rule 14) and keep the assertion tail as-is — BECAUSE the cascade's home is staged §4.7.1 rules 11-15 and a test list is one case per rule asserting what the rule gives — ALTERNATIVES: reintroducing a rule table in CODE-1 so the citation resolves (rejected: a second full statement of the cascade, the exact defect the renumbering restructure removed); listing the arms by their Go `case` expressions (rejected: pins the test list to an implementation detail rather than to the rule home).
DECISION: for the "single exception" contradiction at :597-598, DELETE the enumeration in the doc-comment bullet and point at the deliverable's own `answerShutdown` sentence at :501-505 instead of restating a count — BECAUSE the exception set then has one home and cannot drift a second time; the count "single" drifted precisely because the set was written twice — ALTERNATIVES: the reviewer's literal fix ("two exceptions", naming both) — correct today, rejected as the primary because it re-creates the duplicate statement that produced this finding; deleting the whole bullet (rejected: it is the instruction for the shipped Go doc comment, and the scrub's placement outside both teardowns is stated nowhere else in the doc-comment list).
WATCHOUT: the empty-session-identifier rejection is a TREE fact, not a numbered rule — `Shutdown` returns on an empty session id before anything else, so a request carrying a recycle disposition and `unconditional_teardown` with an empty id starts no whole-pod scrub. Any future "single exception" phrasing is wrong. — EVIDENCE: pkg/adapter/session.go:228-231 vs :288-290
WATCHOUT: review-log.md carries a DEFERRED note about `answerShutdown`'s "one return" wording; that phrasing no longer exists anywhere in non-spec-changes.md (the sentence now reads "The two returns that do not go through it"). It is a stale TODO, not a live site. — EVIDENCE: non-spec-changes.md:501-505

### [non-spec-recheck.1.fix-design-G7.1]
DECISION: fix the single summary.md Decisions bullet (lines 267-270) by re-scoping its first sentence to "a `stageWorkspace` failure raised before `PrepareWorkspace` is sent, through a source-rewrite error or a blob-store failure", citing CODE-4 for the consequence and deleting the `absent`/rule-11/transient restatement, keeping only the unique test-design caution ("a test asserting exactly one `Shutdown` on that branch would pin over-sending as contract") — BECAUSE the rule's home is CODE-4's rationale (non-spec-changes.md:1091-1093) and its test case (:3098-3101), both already correctly scoped; the summary is a citing site — ALTERNATIVES: delete the bullet outright (rejected: the "do not pin exactly one Shutdown" caution is stated nowhere else; the per-stage table at :3094-3097 names only the four post-send stages and the workspace case at :3098-3101 asserts only that the compensation is still sent); edit the CODE-4 siblings (rejected: they are already true).
WATCHOUT: do NOT restore the older qualifier "on an upload-free plan". `stageWorkspace` cannot fail on an upload-free plan at all: `rewriteExtractedSources` returns the plan unchanged, the blob-fetch loop is entered only for `uploadFileRefs`, and `PrepareWorkspace` is the last statement — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1284-1332; review-log-archive.md:50840
FACT: the reachable pre-send failure arms of `stageWorkspace` are exactly (a) a `rewriteExtractedSources` error, (b) `b.Blobs == nil` with an original `uploadFile` ref, (c) ParseURI/Get/ReadAll on the blob store. With uploads present and those passing, the function's error IS `PrepareWorkspace`'s error, after the stream reached the adapter — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1295-1332

### [non-spec-recheck.1.review-applicability.1]

FACT: the delta this lane owes is `git diff 14f82426c -- <proposal dir>` (14f82426c is the
"baseline before open-decisions firing 8 (post-non-spec-recheck)" commit, 2026-09-17, the last
point this lane converged). The cp-snap `spec-recheck-r4-prefix` snapshot is a SPEC-loop
snapshot only and shows a two-hunk diff; it is NOT the non-spec delta. Using it hides ~970
changed lines in non-spec-changes.md. EVIDENCE: git log --oneline -- .../non-spec-changes.md

FACT: checklist is clean on the mechanical sweeps. Every deliverable (SPEC-1..6, SCHEMA-1,
CODE-1..9, CONF-1, DOCS-1..4) appears in a step; every Depends-on names an earlier existing
step; no box is ticked; lanes are one per step; spec steps S1-S6 lead. CODE-1/CODE-4/CODE-6 are
each named by more than one step, which is a deliberate phase split the step text explains.
EVIDENCE: .../implementation-checklist.md:16-62

FACT: every SCHEMA-1 field number is free in the tree as it stands, verified by
`awk "/^message X \{/,/^\}/" schemas/lenny-adapter.proto`. All nine check out, including
`ResumeRequest` 16 (6 and 15 are `reserved`). EVIDENCE: schemas/lenny-adapter.proto, messages
PrepareWorkspaceRequest/FinalizeWorkspaceRequest/RunSetupRequest/AssignCredentialsRequest/
ResumeRequest/ShutdownRequest/ShutdownResponse

FACT: every DOCS anchor and line citation in the delta resolves.
docs/reference/state-machines.md:138 (pod state machine paragraph), :235, :237, :251;
docs/reference/adapter-contract.md:53, :64, :81, :393; docs/reference/error-catalog.md:129;
docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 both end
in ", and the adapter reports its outcome to the gateway". Quoted cells match word for word.

FACT: DOCS-4's gate claim holds. `TestPerSlotCleanupStatedOnEverySessionModeRow` reads only the
residual-state TABLE rows via `residualStateTable`, never the prose sentence DOCS-4 deletes from,
so the deletion turns nothing red. EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:439-476

FACT: both staged `ReportSessionScrub` row replacements (spec §4.7 and adapter-contract.md)
retain "The request is session-scoped: it is addressed by the identifier of the released session
and names no slot." word for word, which is exactly what
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` compares across the two
carriers. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-75

WATCHOUT: a "verbatim" quote in the SCHEMA-1 comment block can start mid-source-line. The
`ReportSessionScrub` RPC comment's outcome sentence begins with "The outcome is" at the END of
schemas/lenny-adapter.proto:310, and neither of the two staged replacements that straddle that
line includes those three words. Check every proto/doc verbatim block against the whole source
LINE, not just the sentence the proposal names. EVIDENCE: schemas/lenny-adapter.proto:308-313
vs non-spec-changes.md:2229-2231 and :2264-2266

WATCHOUT: two deliverables state their own step placement in prose and both disagree with the
checklist. DOCS-3 says it "lands beside SPEC-5 in the same step" (non-spec-changes.md:3513) but
SPEC-5 is S1 (spec lane) and DOCS-3 is S7 (docs lane); a single step may not carry both lanes.
DOCS-4 says it "lands beside DOCS-2" (:2673-2674) but the checklist deliberately gives it S23
and says why. When a step is renumbered or added, sweep the deliverable prose for "lands beside"
and "in the same step". EVIDENCE: implementation-checklist.md S1/S7/S23

UNVERIFIED: the checklist gives S23 tiers "0, 11" while DOCS-4 itself declares "Tiers: 0." and
stages no tier-11 work. I judged this immaterial (running the docs tier costs nothing and breaks
nothing) and did not file it. A later round that wants the tier lists exact should settle it.

### [non-spec-recheck.1.review-citations.1]

FACT: the non-spec staging's code citations are, on a near-exhaustive sweep, accurate. I verified roughly 170 distinct `file:line` claims across `pkg/adapter`, `pkg/gateway`, `cmd/`, `schemas/`, `docs/`, `spec/`, `tests/` and `migrations/` and found no false attribution of behaviour to a component and no line drift that changes a meaning. Do not re-run the whole sweep. — EVIDENCE: spot anchors that all held exactly: pkg/adapter/session.go:163,:259-261,:279,:283-291; pkg/adapter/slotsession.go:84-86,:109-116,:174,:214-220,:238-260,:398-407; pkg/adapter/slot.go:105,:140,:183-206; pkg/adapter/staging.go:181,:239,:209-214; pkg/adapter/holdstate.go:189-205,:249,:254; pkg/adapter/socketruntime.go:156-161,:398-417,:435-467; pkg/gateway/podlifecycle/podsession/binder.go:865-869,:948-954,:997-1000,:1072-1082,:1200,:1225,:1248,:1256; slotbinder.go:210-224,:230-254,:265,:287,:294,:303-304,:353-361,:449-473,:502,:528-545; slotfailure.go:41-48,:88-100; sessionserver/start.go:87,:208-214,:236-249,:312-320,:2594-2605,:2634,:2834,:3648-3681,:4041; adapterclient/client.go:333-341,:807,:813,:860.

FACT: the `tests/spec-map.json` registration claims in `## Files touched` are exact. slotbinder_test.go = {4.6.3,4.7,5.2,6.2,7.1} whole-file plus 4.1 via the `pkg/gateway/...` directory entry; client_test.go = the twelve listed plus 4.1; slotsession_test.go = {4.9,5.2,6.1,6.4,15.4} plus 4.7 via `pkg/adapter/...`; sdkwarm_test.go = {4.7.9,6.1}; holdstate_test.go = the nine listed plus 4.7; concurrent_workspace_test.go = {5.2,6.4,28.5.3} whole-file plus 15.1 via `tests/tier4_integration/...`; recycle_scrub_path_test.go has NO whole-file entry, only per-case, plus 15.1 via the directory entry. — EVIDENCE: tests/spec-map.json `sections`, directory entries `pkg/gateway/...` under 4.1, `pkg/adapter/...` under 4.7, `tests/tier4_integration/...` under 15.1.

FACT: the summary's claim-register arithmetic is exact as of today. tests/claim-map.json holds 76 claims: 32 WIRED, 24 UNWIRED, 20 ABSENT, which is what the 0080 impact row states before SCHEMA-1's three rows. — EVIDENCE: tests/claim-map.json (`claims` array, 76 entries).

WATCHOUT: SCHEMA-1's four proto comment replacements are SENTENCE-scoped quotes, not physical-line quotes, and the sentence boundaries do not line up with the line breaks. `schemas/lenny-adapter.proto:310` ends with "The outcome is", which begins the second sentence; the first replacement's quote stops at "alike." and the second's starts at "RELEASED when". Applying both literally orphans "The outcome is". Check any further proto-comment replacement against the sentence, not the line. — EVIDENCE: schemas/lenny-adapter.proto:308-313.

WATCHOUT: `docs/reference/adapter-contract.md:393` is the page's ONLY spec reference, and its link text is "Spec §15.4 -- Translation Fidelity Matrix", i.e. it does carry a section number. A claim that the page's precedent link form omits the number is false; there is no second link to fall back on. — EVIDENCE: docs/reference/adapter-contract.md:393; `grep -n "blob/main/spec" docs/reference/adapter-contract.md` returns that line alone.

FACT: `pkg/gateway/sessionserver/start_test.go` is 189 lines and contains no `ShutdownRequest{` literal, no `slotBinder` implementation, no `ReleaseSlot*` call and no `SlotBindError` reference, so neither CODE-5's interface change nor the `UnconditionalTeardown: true` grep sweep reaches it. — EVIDENCE: pkg/gateway/sessionserver/start_test.go (whole file); `grep -c "ShutdownRequest{"` returns 0.

USEFUL [review-log `### Deferred`]: the DEFERRED list against non-spec-changes.md is mostly DISCHARGED in the current text. Verified applied: CODE-9's "verbatim" adapter-deferral sentence (now "records the same scrape deferral"), the resume-path `fakeAssigner.released` bullet, CODE-4's `upload_to_session.go` Targets row, the CODE-6 `ConfigureWorkspace` idempotent-repeat arm (the claim now reports the token), the logging/one-return wording, the scrub-outcome comment count, CONF-1's reclaim-hold attribution (now §5.2), and DOCS-3's age claim. The ONE half left unapplied is `start_test.go` in `## Files touched`, filed this round.

MISTAKE (earlier fixer, half-applied): the DEFERRED entry naming both `upload_to_session.go` (missing) and `start_test.go` (listed with no edit) was closed on its first half only. When a DEFERRED names two sites, check both.

### [non-spec-recheck.1.review-client-surface.1]

FACT: the client-facing parallel surfaces are CLEAN for this proposal and I re-derived it, so a later
client-surface lens can skip the sweep. `pkg/gateway/externalapi/openapi/openapi.json` (the only
openapi.json outside testdata) enumerates NO §15.1 error code — `grep -o "SETUP_COMMAND_FAILED\|
STARTING_FAILED\|RESUME_FAILED\|SLOT_FAILED\|WARM_POOL_EXHAUSTED"` returns nothing — so SPEC-5/DOCS-3's
`SETUP_COMMAND_FAILED` widening owes no OpenAPI, MCP-tool-schema or SDK mirror. `grep -rn
"ERROR_CODE_\|ErrorCode" docs/` returns nothing, so the two new adapter codes owe no doc row.
`grep -rln "bind_attempt\|ShutdownRequest\|SlotReclaim" sdks/` returns nothing: no client or runtime
SDK derives from `schemas/lenny-adapter.proto`. No CRD, chart value or JSONL schema is touched.
EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json; sdks/; docs/reference/error-catalog.md:157

FACT: SCHEMA-1's nine field numbers and its five verbatim proto-comment quotes all check out against
`schemas/lenny-adapter.proto` as it stands (PrepareWorkspaceRequest 1-3 used + 4 reserved;
FinalizeWorkspaceRequest mid_session=4, 5 reserved; RunSetupRequest 1-3 + 4 reserved;
AssignCredentialsRequest 1-2 + 3 reserved; ResumeRequest 1-5,7-14 used, 6 and 15 reserved;
ShutdownRequest 1-3,5,6 used, 4 reserved; ShutdownResponse 1-2). Do not re-derive.
EVIDENCE: schemas/lenny-adapter.proto:184-214, :308-319, :436-474

FACT: CODE-5's new `codes.Aborted` arm in `isTransientPodClaimError` makes
`docs/reference/error-catalog.md:157` ("the session row stays in `awaiting_client_action`") true for the
new refusals rather than false, so RESUME_FAILED owes no doc edit. EVIDENCE:
docs/reference/error-catalog.md:157

WATCHOUT: the SPEC-3 carrier table defers TWELVE rows to "the non-spec loop" (spec-changes.md:602-613)
and the non-spec staging carries NONE of them — not in a deliverable, not in `## Testing`, not in
`## Files touched on application (non-spec)` (non-spec-changes.md:3666-3889). Only SCHEMA-1's two proto
comments, DOCS-2, DOCS-4 and CODE-1's `Server.SessionScrubReporter` comment (non-spec-changes.md:183)
are discharged. Two of the twelve turn tier 11 RED on application:
`tests/tier11_docs/spec_28_register_writers_test.go:99-101` pins the §12.6 prose byte-exactly and
`tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173-174` pins the read clause
SPEC-3 deletes. SPEC-3's "`grep -rn sessions_served tests/`" instruction (spec-changes.md:825-829)
reaches those two and reaches NONE of the other ten, which live in `pkg/` and `migrations/`.
EVIDENCE: spec-changes.md:602-613; non-spec-changes.md:3666-3889

FACT: the ten uncovered carriers all still carry the universal today, verified by grep:
pkg/adapter/sessionscrubreporter.go:13; pkg/adapter/gatewaycontrol/scrubreport.go:13,:72;
pkg/agentpodstate/agentpodstate.go:60,:128; pkg/gateway/session/recycle/scrubreporter_seams.go:245;
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472;
tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122;
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-458.

DEFERRED [proposals/.../0081_....spec-changes.md, DOCS-2's precedent claim]: non-spec-changes.md:2549
says the two staged links "follow the form this page already uses for a specification reference
(`:393`), which names the heading and carries no number in its own text". The precedent link text at
docs/reference/adapter-contract.md:393 reads "Spec §15.4 -- Translation Fidelity Matrix", which does
carry the number in its own text (its anchor, `#translation-fidelity-matrix`, is the numberless part).
The staged links are themselves correct; only the rationale clause is false. Not filed: the remedy is
deleting a rationale clause, and the caller's prune directive bars adding or editing commentary.

USEFUL [DECISION: every carrier of the withdrawn ... universal has ONE home, the carrier table]: the
standing-context line giving the 5/7/12 split is what let me check the twelve against the non-spec
staging in one pass instead of re-running the carrier grep.

### [non-spec-recheck.1.review-docs-alignment.1]

FACT: the non-spec lane's last review was at `14f82426c` (2026-09-17), so the docs delta for this
recheck is `git diff 14f82426c -- ...non-spec-changes.md` (~1487 diff lines). The docs hunks are
`@@ -2324,25 +2403,95 @@` (DOCS-1 grows from two edits to five), `@@ -2374,38 +2524,61 @@`
(DOCS-2 grows from two rows to three rows plus a new `**Bind attempt token.**` paragraph),
`@@ -2417,20 +2590,19 @@` and `@@ -2460,14 +2632,17 @@` (DOCS-3 rationale rewrite) and
`@@ -2487,6 +2662,17 @@` (DOCS-4 added as a section; it had existed only in the summary index,
the files-touched list and the tier-11 block). EVIDENCE: /tmp of `git diff 14f82426c`;
non-spec-changes.md:2406, :2517, :2587, :2665

FACT: every "currently reads" quote in DOCS-1, DOCS-3 and DOCS-4 is byte-exact against the tree,
and every cited line number resolves. Verified: state-machines.md:138 (pod state machine
paragraph), :235 (`receiving_uploads`→`running` row), :237 (`slot_cleanup`→`released` row), :251
(`slot_cleanup -> leaked` clause); adapter-contract.md:10, :18, :53, :64, :75, :81;
error-catalog.md:129 (all five quoted sentences plus the remedy cell); execution-modes.md:68 and
security-principles.md:33 (the clause ", and the adapter reports its outcome to the gateway"
verbatim on both). Do not re-verify these. EVIDENCE: docs/reference/state-machines.md:138,235,237,251

FACT: the one cited line that is NOT what the proposal says it is is
`docs/reference/adapter-contract.md:393` — the page's only spec link, whose link TEXT is
`[Spec §15.4 -- Translation Fidelity Matrix]`, i.e. it carries the section number in its text and
not in its anchor. DOCS-2 claims the opposite. Filed. EVIDENCE:
docs/reference/adapter-contract.md:393; non-spec-changes.md:2548-2549

FACT: the staged §4.7.1 carriage table puts `StartSession` and `ConfigureWorkspace` at
"not carried" for `bind_attempt`, and a `mid_session` `PrepareWorkspace`/`FinalizeWorkspace`
carries it empty, while rule 4 lets a tokenless request CREATE an entry. Any prose that says the
token rides "the requests through which an attempt creates or resolves the entry" is therefore
false. The new DOCS-2 paragraph says exactly that. Filed. EVIDENCE:
spec-changes.md:1023-1031 (carriage table), spec-changes.md rule 4; non-spec-changes.md:2581

MISTAKE: the round-4 "already found and fixed" list records "Implementation checklist step S7
still enumerates DOCS-1 without the new `receiving_uploads` → `running` trigger replacement" as
FIXED. It is not fixed. It was recorded as a DEFERRED in the review log (review-log.md:975, which
also names step S5's stale "Two fence edits"), because the spec lane could not edit the
checklist. This lane can. Filed the S7 half. The S5 half is still unclosed and is spec-lane
bookkeeping. EVIDENCE: implementation-checklist.md:25 (S5), :29 (S7);
review-log.md:975

FACT (negative results, so a later docs lens does not repeat them):
- `grep -rn "Shutdown" docs/` outside `adapter-contract.md:75` returns only JSONL `shutdown`-frame
  hits (`getting-started/concepts.md:134`, `runtime-author-guide/testing.md:28,220`), which are a
  different mechanism.
- `grep -rn "leaked" docs/` returns `state-machines.md:247,248,251`, `adapter-contract.md:81`,
  `glossary.md:371` and one unrelated runbook hit. `glossary.md:371` ("leaked and failed slots are
  retained until the pod terminates and still count against pod occupancy") stays true under the
  staged disposition table, because a row whose hold is "Held for the life of the pod" but whose
  `leaked` cell is "Not entered" has already released its occupancy.
- The projection prose exists on exactly one docs page (`state-machines.md:138`); every other
  `recycle.enabled: false` hit in `docs/` is a mode description, not a projection statement.
- `lenny_slot_failure_total`'s `error_type` value domain is enumerated in neither
  `spec/16_observability.md:14` nor `docs/reference/metrics.md:166`, so CODE-9's new
  `slotFailureWorkspaceFinalize` stage constant falsifies no docs row.
- CODE-5 guards the §7.3 re-attach accounting on a non-empty slot id, which is the concurrent pool
  only, so `metrics.md:166`'s "`maxConcurrentSessions > 1`" scope stays true.
- The proposal adds no alert, so no `docs/runbooks/` companion is owed.
- The four new `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` substrings and the
  `**Bind attempt token.**` page-level assertion all match the staged text; the shipped gate's four
  substrings all survive; `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`'s
  required opener + rule string is byte-identical on both staged carriers. EVIDENCE:
  tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307,
  tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-76

WATCHOUT: `adapter-contract.md:84` (`**Scrub responsibilities.**`, "the adapter runs the credential
purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") reads like a
sixth carrier of the withdrawn report universal. It names neither `ReportSessionScrub` nor
"per-slot cleanup", so it is outside both sweeps the SPEC-3 carrier table was built from, and it
is scoped to "each session end", which is a slot that reached `running`. I worked it up and did
not file it. A later lens that wants it owes an argument that "each session end" reaches a
pre-`running` slot.

USEFUL [Settled, "The SPEC-3 carrier table's docs coverage is COMPLETE over the whole `docs/`
tree"]: the two greps it records (`per-slot cleanup` and `ReportSessionScrub` over `docs/`) still
return exactly the sites it names, and re-running them cost two minutes instead of a full sweep.

### [non-spec-recheck.1.review-edit-sites.1]

FACT: SPEC-3's carrier table (spec-changes.md:588-613) is the single home of the withdrawn
"reports on every session release" universal. Eleven of its rows read "Deferred to the non-spec
loop, code-lane comment re-key". THIS loop is that lane, and the non-spec staging discharges
exactly one of them (`pkg/adapter/server.go`'s `SessionScrubReporter` field comment, CODE-1 at
non-spec-changes.md:182-184). Every other deferred carrier is absent from both the deliverables
and `## Files touched on application (non-spec)`.
— EVIDENCE: spec-changes.md:602-613; non-spec-changes.md:3666-3889;
  pkg/adapter/sessionscrubreporter.go:13; pkg/adapter/gatewaycontrol/scrubreport.go:72;
  pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472;
  pkg/gateway/session/recycle/scrubreporter_seams.go:245; pkg/agentpodstate/agentpodstate.go:60,:128

FACT: the two tier-11 rows in that same table (`spec_28_register_writers_test.go`,
`concurrent_slot_lifecycle_doc_reconciliation_test.go`) ARE discharged, by SPEC-3's §12.6
instruction to run `grep -rn sessions_served tests/` and re-key/delete what it finds. Both files
contain the literal `sessions_served`, so the grep reaches them. Do not file those two.
— EVIDENCE: spec-changes.md:825-829; tests/tier11_docs/spec_28_register_writers_test.go:99;
  tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170

FACT: `pkg/sandbox/slotstate/slotstate.go:98-104` carries a Go doc-comment transcription of §6.2's
per-slot fence, including `slot_cleanup → leaked (cleanup timeout exceeded)` and
`receiving_uploads → running (workspace ready, task dispatched)`. CODE-3 opens that comment but
only appends one line. `pkg/gateway/runtime/slothealth/slothealth.go:33` carries the same retired
timeout trigger.
— EVIDENCE: pkg/sandbox/slotstate/slotstate.go:100,:104; non-spec-changes.md:787-792

FACT (gates that are fine, do not re-derive):
  - `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` needs the literal
    "The request is session-scoped: it is addressed by the identifier of the released session and
    names no slot." on ONE line in both §4.7's row and adapter-contract.md's row. Both staged rows
    carry it verbatim. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:68
  - `TestRecycleTriggerConsistentAcrossSpec47And52_F5215` reads the §4.7 `Shutdown` row for
    "recycle disposition", "ReportPodScrub", `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`,
    "does not block the response on the scrub" and the §5.2 link. SPEC-1 replaces only the row's
    FIRST sentence, and every one of those substrings lives in the surviving tail.
    EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:73-100; spec/04_system-components.md:686
  - `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs "end-of-session teardown",
    "recycle disposition", "ReportSessionScrub", "ReportPodScrub"; DOCS-2's staged row carries all four.
    EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:301-306
  - `generalSlotEdges` positive/negative loops stay green with the new edge added.
    EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-74
  - `docs/operator-guide/multi-tenancy.md:72` carries the per-slot-cleanup sentence WITHOUT the
    reporting clause, which is why DOCS-4 correctly leaves it alone.

FACT: the two `k8s_pod_name` questions are settled and must not be refiled. §16.1.1's "Used on"
column is descriptive prose no gate reads (review-log.md:957). And no adapter-registered metric in
`pkg/adapter/metrics.go` carries a pod label today, but `Server.podID` exists
(pkg/adapter/server.go:196,:380), so SPEC-6's `k8s_pod_name` on the adapter series is fillable and
is not a design defect.

WATCHOUT: `slotFailure*` stage constants live in `pkg/gateway/podlifecycle/podsession/binder.go:290-298`,
not in `slotfailure.go`, which the non-spec files-touched list names as the home of the new
`slotFailureWorkspaceFinalize`. I judged this below the bar (the constant compiles anywhere), but a
later attribution lens may want it.
— EVIDENCE: non-spec-changes.md:3814-3815; pkg/gateway/podlifecycle/podsession/binder.go:290

MISTAKE: the round that fixed "S7 enumerates DOCS-1 without the `receiving_uploads` → `running`
trigger replacement" added the `slot_cleanup` → `released` and `slot_cleanup -> leaked` items to S7
and left the named one out. The fix was applied to its siblings rather than to the item the finding
named. Re-filed here.
— EVIDENCE: implementation-checklist.md:29

### [non-spec-recheck.1.review-fresh.1]

DECISION: filed six findings, all reachable by reading the non-spec file + summary against the spec-changes carrier table and the tree — BECAUSE the non-spec lane has not run since 2026-09-17 while SPEC-3's carrier table and the Design/CODE-1 rule-renumbering pass both moved under it — ALTERNATIVES: rejected filing on the §4.1 scrub sentence ("a request that passed the teardown-pairing rule") against the empty-session-id rejection, because "passed" plausibly excludes a request never evaluated by rule 10, and the remedy would be a spec edit.

FACT: `pkg/adapter/session.go:228-231` rejects an empty session id BEFORE anything else in `Shutdown`, so the staged handler has TWO returns that bypass `answerShutdown` and therefore the whole-pod recycle scrub, not one. EVIDENCE: pkg/adapter/session.go:228-231

FACT: `stageWorkspace` DOES send `PrepareWorkspace`, at `pkg/gateway/podlifecycle/podsession/binder.go:1326`, and returns that RPC's error, so "a stageWorkspace failure sends no RPC" is false in general. Only the source-rewrite and blob-store arms return before the send (`:1290`, `:1303-1318`). CODE-4 gets this right with its parenthetical; the summary does not. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1322-1330

FACT: SPEC-3's carrier table (spec-changes.md:602-613) defers ~9 carriers of the withdrawn "reports on every session release" universal "to the non-spec loop", and the non-spec file's Targets lists and `## Files touched on application (non-spec)` carry none of them. The live strings are at pkg/adapter/gatewaycontrol/scrubreport.go:13,:72; pkg/adapter/sessionscrubreporter.go:13; pkg/gateway/session/recycle/scrubreporter_seams.go:245; pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:67,:196,:472; pkg/agentpodstate/agentpodstate.go:60,:128; tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8. EVIDENCE: spec-changes.md:602-613

WATCHOUT: `schemas/lenny-adapter.proto:257` ("carries the §5.2 per-slot and whole-pod scrub reports ... the adapter emits on release") is a near-carrier the table does not list. I did NOT file it: it says "on release", not "on every release", and `ReportPodScrub` is already not a release-time report, so the phrase is loose rather than a universal. A later round should decide once and record the decision rather than re-deriving it. EVIDENCE: schemas/lenny-adapter.proto:257

FACT: `docs/reference/adapter-contract.md:393` is the page's ONLY spec reference, and its link text is "[Spec §15.4 -- Translation Fidelity Matrix]" — it does carry a section number, while its anchor (`#translation-fidelity-matrix`) does not. The staged DOCS-2 links are the mirror image (no number in the text, numbers in the anchors). Whichever half of "form" is meant, the DOCS-2 rationale sentence is false. EVIDENCE: docs/reference/adapter-contract.md:393

FACT: verified clean this pass, do not re-derive: docs/reference/state-machines.md:138,:235,:237,:251 verbatim quotes all match; docs/reference/adapter-contract.md:10,:18,:53,:64,:75,:81 all match; the two DOCS-4 sentences at docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 end exactly as the deliverable quotes; every SCHEMA-1 proto comment quote matches verbatim (schemas/lenny-adapter.proto:308-313,:440-443,:451-452); `spec/28_communication-channels.md:162-163`, `tests/tier0_static/claim_register_test.go:46` and `gateway-runtime-comms-remediation.md:980` all say what SCHEMA-1's claim-register paragraph says they say; `slotFailureSetup = "setup"` (binder.go:291); `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the exact opener `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` and both staged rows keep it; `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`'s four substrings all survive the staged `Shutdown` row.

MISTAKE: the rule-renumbering fix pass deleted CODE-1's six-row `| Request | Entry | Answer | Removes |` table but left the tier-1 case list pointing at "one case per row of CODE-1's table" (non-spec-changes.md:2886), and left "Name the single exception" (`:597`) disagreeing with the "two returns" sentence it had just rewritten (`:501-503`). Both are one-line repairs; both cost this round a finding.

OPEN: the pass that reduced the non-spec Edge-cases section to pointers converted three bullets and left **An attempt cannot fence itself** (non-spec-changes.md:3569) as a second full statement of spec-changes.md:167's **An attempt that recreates its own entry after an unconditional teardown removed it**. Whether that bullet is "a case that concerns code alone" is the question; on its text it is not.

### [non-spec-recheck.1.review-kubernetes.1]

DECISION: EMPTY findings list for the Kubernetes-idiom lens on the FULL staging (spec-changes +
non-spec-changes + checklist + summary), the first time this lens has read the non-spec lane since
2026-09-17 — BECAUSE every Kubernetes surface the staging touches is either unchanged from the
spec-lane rounds this lens already cleared (SPEC-4's §4.6.1/§6.2 occupancy projection, the §4.7
`ReportSessionScrub` row's `lenny.dev/drain-request` pointer) or is a gateway-side write that
§4.6.3 already grants (`SandboxClaim` DELETE via `podclaim.DeleteClaim`, the Pod annotation stamp
via `Binder.DrainSandbox`). No staged text writes a CRD status subresource from a non-owning
component, adds a finalizer, adds a webhook, uses a CRD as a message bus, or puts a controller
reconcile on a synchronous request path — ALTERNATIVES rejected: (a) filing CODE-8's refusal arm
(which skips `failPhase` and therefore skips `b.drain`) as leaving the per-pod `SandboxClaim`
behind for the §4.6.1 orphan GC, rejected because on a `SLOT_BIND_ATTEMPT_SUPERSEDED` refusal
another attempt of the SAME session legitimately owns that claim and deleting it would retire a
healthy pod, which is the deliverable's stated point and what the staged tier-2 case asserts;
(b) re-filing the status read-back in SPEC-4's projection input enumeration, already rejected twice
by this lens on the ground that `ProjectOccupancyPhase` genuinely takes `occupancy.Current`;
(c) re-filing the §16.1.1 "Used on" cell, retired as an Open in compaction pass 27.

FACT: the non-spec lane's Kubernetes-bearing citations all re-verify at the cited lines.
`failPhase` at binder.go:1072, its unconditional `b.drain` at :1079, `drain` = `podclaim.DeleteClaim`
at :1200-1201; the occupancy-zero dispositions at slotclaimer.go:845-847 (sibling retained),
:850-878 (`bound → recycling` + `OnRecycling` + signal) and :881-885 (DELETE retires);
`ProjectOccupancyPhase`'s no-claim branch at occupancy.go:128-143; `claimToSandbox` at :281-291.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072,1079,1200;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845,850,881;
pkg/controller/warmpool/occupancy.go:128,281.

FACT: the CODE-9 metric wiring citations are exact and the new gateway series' `k8s_pod_name` label
follows the shipped `lenny_slot_failure_total` precedent line for line: `Binder.SlotFailure` field
at binder.go:137, `recordSlotFailure` at slotbinder.go:353-360, the hook wiring at
metricsbackfill.go:149, `IncSlotFailure` at gatewaymetrics.go:1175, the collector at
gatewaymetrics_credential.go:185-193, and both `slotFailureWorkspacePrep` sites at
slotbinder.go:287,294. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:137;
pkg/gateway/podlifecycle/podsession/slotbinder.go:287,294,353;
cmd/lenny-gateway/metricsbackfill.go:149.

FACT: DOCS-1's four quoted state-machines.md strings are verbatim in the shipped page — the
`receiving_uploads`/`running` row at :235, the `slot_cleanup`/`released` row at :237, the
`slot_cleanup -> leaked` clause at :251, and the pod-state projection sentence's input enumeration
at :138 — and the tier-11 gate that reads that page asserts only state NAMES
(`generalSlotEdges` and a `requireAllContain` over backticked state names), never a trigger cell,
so none of DOCS-1's four trigger/clause replacements turns a shipped gate red.
EVIDENCE: docs/reference/state-machines.md:138,235,237,251;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-36,102-121.

WATCHOUT: the prompt's "already found and fixed" list names "Implementation checklist step S7 still
enumerates DOCS-1 without the new `receiving_uploads` → `running` trigger replacement" as FIXED. It
is not. Current S7 enumerates the new row, the `slot_cleanup` → `released` trigger, the leaked
clause and the pod-state paragraph, and omits the `receiving_uploads` → `running` trigger cell that
DOCS-1 and the files-touched list both carry. S5 is stale in the same way: it says "Two fence
edits" while SPEC-4's §6.2 fence work is now one added edge plus three annotation replacements plus
the `claimed ──→ draining` trigger pointer. I did NOT file either, because the live DEFERRED at
review-log.md:975 already records both and the prompt bars re-litigating the S7 half.
EVIDENCE: implementation-checklist.md S5 and S7 lines; non-spec-changes.md DOCS-1 block and the
`docs/reference/state-machines.md` bullet of the files-touched list; review-log.md:975.

USEFUL [spec-recheck.4.review-kubernetes.1]: its FACT that `Sandbox.status.phase` has exactly one
SSA field manager across every WPC writer, and its warning that the `ok=false` branch comment is
about two RECONCILERS rather than two field managers, is what let this pass clear SPEC-4 and DOCS-1
without re-deriving the ownership question. Still true.
USEFUL [spec-recheck.1.review-kubernetes.1]: its statement that the K8s candidate space on the SPEC
lane is exhausted held; extending the read to the whole non-spec lane added no new candidate.

### [non-spec-recheck.1.review-mechanism.1]

DECISION: filed three findings, all with their remedy inside the non-spec staging — BECAUSE the spec staging re-verified clean under this lens (the §5.2 disposition table, the §5.2 reclaim-hold paragraph, the §4.7.1 cascades and the §6.2 pre-`running` paragraph compose without predicate drift; CODE-1's `shutdownReclaimOutcome` arms map one-to-one onto rules 11-14, the rule-10 XOR is correct in both directions, the three CODE-1 predicates (`started`/`live`/`completed`) are each keyed as §5.2 and §4.7 require, and the two-decision re-evaluation on the removing arm is sound) — ALTERNATIVES: rejected filing the narrower trigger text in CODE-3's new edge-list comment as its own finding (folded into the stale-gloss finding, same edit site, same fix round); rejected filing the vestigial "row"/"fail-closed row" vocabulary in CODE-1's code comments (wording).

FACT: `pkg/sandbox/slotstate/slotstate.go` carries the retired §6.2 glosses at FIVE sites, not two, and CODE-3 opens that file for a sixth — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:36-37 (`Running` "task has been dispatched to the runtime"), :41-42 (`Released` "workspace was removed, processes killed, and slotId released"), :44-45 (`Leaked` "whose cleanup timed out"), :100 (`receiving_uploads → running (workspace ready, task dispatched)`), :103 (`slot_cleanup → released (slot reclaimed)`), :104 (`slot_cleanup → leaked (cleanup timeout exceeded)`). `pkg/gateway/runtime/slothealth/slothealth.go:33` is the sixth carrier of "cleanup timeout exceeded" and is in no edit list at all.

FACT: CODE-1 no longer contains a table. The six-row `| Request | Entry | Answer | Removes |` table was deleted by the restructure; `git show 14f82426c:...non-spec-changes.md | sed -n '226,234p'` is the last version that had it — EVIDENCE: non-spec-changes.md:2886 still says "one case per row of CODE-1's table".

WATCHOUT: `Client.shutdown` at pkg/gateway/runtime/adapterclient/client.go:813 is the builder behind `Shutdown` (:807) and `ShutdownRecycle` (:860) ONLY. `ShutdownReclaim` is a third, separate method that must NOT route through it, or it would inherit `unconditional_teardown = true` and violate rule 10. The staging says "Both exported forms build the request through one unexported builder", which reads as if it covered all three — EVIDENCE: non-spec-changes.md:871-875, pkg/gateway/runtime/adapterclient/client.go:807-824, :860-867.

FACT: every `binder.go`/`slotbinder.go` line citation in CODE-1's clause-three comment and CODE-4's non-compensating-caller table re-verifies exactly: :1994 is `b.shutdownAdapter(ctx, result, true)` inside `Release`'s recycle branch, :2037 is `ShutdownRecycle`, :2043 is the plain `Shutdown`, slotbinder.go:542 is `result.Adapter.Shutdown`, :574 is `ShutdownRecycle` — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1994,:2037,:2043; slotbinder.go:542,:574. Do not re-derive.

CORRECTS [orchestrator prompt's "already found and fixed" list]: "Implementation checklist step S7 still enumerates DOCS-1 without the new `receiving_uploads` → `running` trigger replacement" is NOT fixed in the current text. checklist S7 (implementation-checklist.md:29) still lists only the new row, the released-row trigger, the leaked clause and the pod-state paragraph, while DOCS-1 also replaces the `receiving_uploads` → `running` trigger cell (non-spec-changes.md:2406, :2427-2437). The checklist has not been touched since `72ac374e3`. It stands recorded at review-log.md:975 as a DEFERRED alongside the same falsehood in S5. I did not file it because the prompt barred re-litigating it; the pass between loops must close it from the DEFERRED rather than wait for a lens.

UNVERIFIED: whether the DEFERRED entries at review-log.md:836-877 against `non-spec-changes.md` are ALL discharged. I checked and found discharged: the CODE-4 `Targets:` `upload_to_session.go` entry (now at :805-807), the CODE-6 `ConfigureWorkspace` idempotent-repeat double-lock entry (now an `allowStarted` field on the resolve descriptor, :1564-1573), the CODE-9 "verbatim" entry (:2003-2006), the `fakeAssigner.released` resume-test entry (:3144-3148), the tier-7a rule-8 gap (:3320-3356 plus the fifth arm at :3437-3452), and the CONF-1 reclaim-hold attribution (:2148-2152, now §5.2). I did NOT check the DOCS-3 retryable-fallback age-claim entry, the logging-and-count-wording entry, or the scrub-outcome comment-count entry. Whoever runs the next non-spec lens should sweep those three.

### [non-spec-recheck.1.review-performance.1]

DECISION: returned an EMPTY findings list for the performance / scalability / failure-mode-reliability lens — BECAUSE the whole non-spec-lane delta since the last converged non-spec review (commit 14f82426c, 2026-09-17) is 967 diff lines of which every substantive hunk is terminology, citation re-keying onto the numbered §4.7.1 rules, doc-surface additions and test-case restructuring; none of it creates a write, a watch, a lock scope, a hot key or a failure path, so none of it perturbs an input to the three quantitative results this lens owns. ALTERNATIVES considered and rejected: (1) re-filing the coordinator-handoff/preemption trigger for the lost-compensation bullet (standing Open entry, already FILED once at [spec.20.review-performance.1] with no fix following; its only remedy is a spec edit, and this loop prefers a non-spec remedy); (2) filing `Server.slotGuards` unbounded growth on a bounded-cohort pool (see FACT below — magnitude is negligible); (3) filing the §10.1.4 per-member close budget as an N x 10s serialization regression (explicitly refuted earlier in this run).

FACT: locating the non-spec delta. The snapshot at `scratchpad/cp-snap/0081-opt2/spec-recheck-r4-prefix` differs from the live proposal in `spec-changes.md` ALONE, so it is useless for the non-spec lane. The usable baseline is the last `post-non-spec-recheck` commit: `git diff 14f82426c HEAD -- proposals/0081_*/0081_*.non-spec-changes.md`. — EVIDENCE: `git log --oneline -20 -- proposals/0081_*/0081_*.non-spec-changes.md` names 14f82426c "baseline before open-decisions firing 8 (post-non-spec-recheck) on 2026-09-17".

FACT: the only delta hunks with any capacity or reliability content, all checked clean. (a) CODE-4 gains `pkg/gateway/sessionserver/upload_to_session.go` as a target; that file at :128 and :134 is the only §7.4 mid-session caller in the gateway, and the only other `PrepareWorkspace`/`FinalizeWorkspace` callers are `binder.go:1327,:921` and `slotbinder.go:291` on the bind path, so rule 1's carriage partition is total over the tree. (b) the resume-path lease assertion is corrected to a new `fakeAssigner` `Release(leaseID)` field; `Binder.Resume` mints no credential lease (its body has no `assignCredentials` call), so "releases no gateway-side credential lease" is not a lease leak. (c) the `stageWorkspace` test re-description verifies: with `b.Blobs == nil` and an original `uploadFile` ref the function returns at `pkg/gateway/podlifecycle/podsession/binder.go:1305` before the `cl.PrepareWorkspace` at `:1327`. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1305,:1327; pkg/gateway/sessionserver/upload_to_session.go:128,:134.

FACT (magnitude, so nobody re-derives it): `Server.slotGuards`'s stated bound, "one channel per distinct session the pod has served, and `recycle.maxSessionsPerPod` retires the pod at that count" (non-spec-changes.md, CODE-6 *Lifetime.*), covers the default and recycling pools but NOT the "Bounded cohort" configuration (`maxConcurrentSessions: N`, `recycle.enabled: false`, spec/05_runtime-registry-and-pool-model.md:428), where `maxSessionsPerPod` is not required and the spec does not say whether a freed slot may take a new session inside one occupancy episode. I did not file it: a map entry is a session-id string plus a capacity-one channel, on the order of 200 bytes, so even a pod churning 8 slots for 24h holds tens of kilobytes, far below anything this lens measures. The same argument covers `s.reclaiming` (standing Open entry "Does `s.reclaiming` grow without bound"), which is one string key per failed cleanup on the same pod. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:428; spec/05:511 (`maxSessionsPerPod` required only when `recycle.enabled: true`).

USEFUL [The staging adds NO per-session or per-request write to etcd, Postgres or Redis and net-REDUCES Postgres writes] and [non-spec-recheck-2.4.review-performance.1]: the top-tier write math (compensation fires once per FAILED bind; Tier 3 claim rate ~30/s at a 99.5% creation SLO puts it at <=~0.15/s, one `Shutdown` on an already-open connection each) is still exact and nothing in this delta touches an input to it. A future performance pass on an unchanged mechanism needs only to confirm the delta touches none of those inputs.

WATCHOUT: the non-spec Edge-cases section no longer states the hold's cost accounting; three bullets are now pointers into the spec-changes file's accepted-failure-modes section. I verified the pointers resolve and carry the content: "A retry that meets the reclaim hold spends an attempt on it" (spec-changes.md:107) does carry the caller list and the per-caller cost, and "A bind abandoned at the connect stage" (spec-changes.md:189) carries the no-reclaim-owed statement. One detail the deleted bullet carried is now stated nowhere: that no metric distinguishes a hold refusal from any other transient slot failure. That is an observability gap rather than a defect, and the bar excludes it — do not file it. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:107,:189; non-spec-changes.md:3557-3568.

### [non-spec-recheck.1.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability/fault-tolerance lens over the whole non-spec staging plus the summary — BECAUSE every recovery path I traced (compensation, reclaim hold, per-slot guard, start-confirmation rollback, §10.1.4 hold termination, whole-pod scrub) either bounds its own failure or is named in an accepted-failure-mode bullet with a stated bound — ALTERNATIVES: I worked up and then dropped four candidates, each recorded below so the next reliability pass does not re-derive them.

FACT: the non-spec lane's delta since its last convergence (2026-09-17) is `git diff 14f82426c..HEAD -- <non-spec-changes.md>` and `<summary.md>`; the spec-lane snapshot at scratchpad/cp-snap/0081-opt2/spec-recheck-r4-prefix differs ONLY in spec-changes.md, so it is useless for locating the non-spec delta. — EVIDENCE: `git log --format='%h %ad %s' --date=short -- proposals/0081_.../` (14f82426c is the 2026-09-17 post-non-spec-recheck baseline)

FACT: `Runtime.Close` does NOT abort on a cancelled context in any of the three implementations, so CODE-2's rollback issuing `s.Runtime.Close(ctx, sessionID)` on the (typically already-cancelled) inbound context still performs the close. `resolveShutdownGrace` falls back to the implementation default when the deadline has passed. — EVIDENCE: pkg/adapter/embedded.go:188 (`Close(_ context.Context, ...)`), pkg/adapter/mcpruntime.go:266 and :312-324, pkg/adapter/socketruntime.go:435-467. CANDIDATE DROPPED: "the rollback's close is issued on a dead context, so the abandoned runtime session survives" is refuted by these three bodies. Do not file it.

FACT: `deregisterStartedSessions` — §10.1.4 pass 1 — lives at pkg/adapter/slotsession.go:354-395, NOT in holdstate.go. The proposal cites `pkg/adapter/holdstate.go:189-205` for it (non-spec-changes.md, CODE-6 "The reclaim hold has one predicate and two test points"); that range is only the call site inside `onHoldTimeout` (holdstate.go:190). The claim made about pass 1 (one `s.mu` hold across every member, no guard) is TRUE of the slotsession.go body. I judged this below the bar (the call site is inside the cited range and the claim is accurate), but it is a citations-lens lead if that lens wants it. — EVIDENCE: pkg/adapter/slotsession.go:375 `func (s *Server) deregisterStartedSessions() []heldSession`; pkg/adapter/holdstate.go:188-205.

FACT: the shipped §10.1.4 discards this proposal replaces are at pkg/adapter/holdstate.go:249 (`_ = s.Runtime.Close(ctx, m.sessionID)`) and :254 (`_ = removeSlotTree(m.state)`), and the shared close context is minted at :201 with its explaining comment at :196-200. Every line number CODE-6 cites for that site checks out.

FACT: `stageWorkspace` returns before `PrepareWorkspace` on a rewrite error and on a blob-store error; the `cl.PrepareWorkspace` send is the last statement and runs only when `len(uploads) > 0`. CODE-4's amended parenthetical ("a source-rewrite error or a blob-store failure, which returns before `PrepareWorkspace` is sent") is exact. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1283-1332.

FACT: `UnhealthyThreshold` is `int((maxConcurrent+1)/2)` with a clamp at 1, the leak count is persistent and released only by `Forget`, the failure count ages out of a rolling five-minute window. Every arithmetic claim CODE-5, the summary's decision 34 and the accepted-failure-mode bullets make against these is correct. — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:98-125, :215-220.

FACT: `Server.SessionScrubReporter`'s field comment does say "on every session release", so CODE-1's new re-key target on pkg/adapter/server.go exists. — EVIDENCE: pkg/adapter/server.go:168-177.

FACT: the SDK-warm `noteRuntimeStarted` call is on the `fresh` arm only, and the non-fresh arm is the idempotent repeat of a session already recorded, so CODE-1's `live := removed && s.runtimeHoldsLocked(sessionID)` cannot silently withhold `ReportSessionScrub` (and therefore `sessions_served`) at an ordinary SDK-warm session end. CANDIDATE DROPPED. — EVIDENCE: pkg/adapter/sdkwarm.go:217, :255-262.

WATCHOUT: the three bullets the delta deleted from the non-spec "Edge cases and accepted failure modes" section are now pointers into the spec-changes file by bold title. All three titles resolve: spec-changes.md:107 ("A retry that meets the reclaim hold spends an attempt on it"), :177 ("A compensation lost to a gateway crash..."), :189 ("A bind abandoned at the connect stage"). Do not file the pointers as dangling; I checked them.

FACT: defer order in CODE-1's removing arm is correct as the text claims. `defer unlockSlot()` is registered before the `completed`-gated `defer release()`, so LIFO runs the hold release first and the guard unlock last, and `answerShutdown` (which starts the async whole-pod scrub, pkg/adapter/podscrub.go:40-42) runs before both because it is evaluated in the return expression. The scrub is a goroutine, so no deadlock against the still-held guard or `s.mu`. CANDIDATE DROPPED: "the scrub starts while the hold and guard are still held" is open decision 35's subject and is not mine to file.

UNVERIFIED: whether an entry stamped with a dead token on the EXCLUSIVE path can now strand the pod's `SandboxClaim` forever. CODE-8 makes `Binder.Prepare`'s refusal arm skip `failPhase`, so the drain that deletes the claim (binder.go:1079, :1200-1202) does not run; on the concurrent-`/finalize` case that is correct (the winner owns the claim), but on the gateway-crash residue case nothing obviously reclaims it. I could not establish a reachable ordering that produces it without also producing the drain, and the proposal records the neighbouring case ("A pod whose drain failed keeps a dead attempt's token... It is the reaper's subject"). A later Kubernetes-lens or reliability pass could settle whether WarmPoolController's orphaned-claim GC reaches it.

### [non-spec-recheck.1.review-security.1]

DECISION: filed exactly ONE finding, on open-decision 35's factual ground — BECAUSE the staged
CODE-1 handler runs `s.removeSlotTreeVia(st)` (non-spec-changes.md:424) and only then reaches
`return answerShutdown(...)` (:490), whose body runs `s.startPodScrub(rc)` (:327), so on the
removing arm the slot's tree removal has RETURNED before the whole-pod scrub starts; the decision
entry (summary.md:636-638) tells the human the scrub starts "while ... the slot's tree removal is
still running". ALTERNATIVES: I did not file on how the decision is framed or on whether it should
be open (the caller bars both); the finding is only on the false premise, whose remedy is a
deletion of the false clause.

FACT: every non-compensating `Shutdown` caller in the tree is exactly the five the CODE-4 table
names, and `Client.Shutdown`/`Client.ShutdownRecycle` both route through the one unexported
builder, so the `unconditional_teardown` statement genuinely cannot be forgotten by a caller.
Re-derived with `grep -rn "\.Shutdown(\|ShutdownRecycle(" --include=*.go pkg/gateway cmd/`.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807,:813,:860;
pkg/gateway/podlifecycle/podsession/slotbinder.go:542,:574;
pkg/gateway/podlifecycle/podsession/binder.go:2037,:2043; cmd/lenny-gateway/user_revocation.go:129.

FACT: CODE-8's security argument checks out line for line against the tree. `failPhase`'s
`leaseAssigned` gates `releaseCredentials` alone and `b.drain` sits outside that block;
`Binder.Launch`'s closure passes the literal `true`; `Binder.Prepare` declares `leaseAssigned`
false and sets it only AFTER the `assignCredentials` call site, so `failPhase` releases nothing on
that arm today. Skipping `failPhase` on `SLOT_BIND_ALREADY_STARTED` is therefore the fail-closed
direction: the session-keyed `credassign.Service.ReleaseSession` walk would strip the live
session's own §4.9 leases. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082,
:865-869, :949, :954, :997-1000.

FACT: the §7.4 mid-session admission guard the Design section rests on is real and is
tenant-scoped: `s.store.Get(ctx, tenantID, id)` then `s.podRegistry.Get(row.ID)`, and a nil bind
answers `TARGET_NOT_READY` before any adapter RPC. `FinalizeWorkspace` already carries a
mid-session argument; `PrepareWorkspace` does not, which is why SCHEMA-1 adds the field.
EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:72,:75,:112-121,:127,:133.

FACT: SCHEMA-1's four "verbatim" proto comment quotes are verbatim in the tree, and the
`SessionScrubOutcome` enum comment it declares unedited ("the §5.2 per-slot cleanup the adapter
runs on every session release") states when the CLEANUP runs, not when the REPORT is filed, so
SPEC-3's withdrawn reporting universal does not reach it. EVIDENCE:
schemas/lenny-adapter.proto:308-311,:436-442,:451.

FACT: DOCS-4's clause deletion lands cleanly on the security page. `docs/operator-guide/
security-principles.md:33` is a standalone sentence whose tail is exactly the clause DOCS-4
deletes, and the acknowledgment-gate paragraph below it (recycle / maxConcurrentSessions /
scrubProfile, each fail-closed) carries no dependency on the deleted clause. EVIDENCE:
docs/operator-guide/security-principles.md:33-39; docs/reference/execution-modes.md:68.

USEFUL [spec-recheck.4.review-security.1]: its statement that no security bound in this staging is
sourced from a pod self-report saved me re-deriving the `leaked` decision path; I confirmed the
non-spec side agrees (`sbe.Leaked = cerr != nil || !cleanly` is computed gateway-side at the
compensation's call site and treats an unrecognized outcome on the same two fields rather than on
a fail-open default arm). EVIDENCE: non-spec-changes.md:948-960.

UNVERIFIED: whether a recycle-carrying `Shutdown` answering `ABSENT` can start the whole-pod scrub
while a DIFFERENT slot's `releaseSessionSlot` or §10.1.4 termination is still inside
`removeSlotTree` in another goroutine. That is a real ordering question and may be what
decision 35 meant to ask; I did not chase it because it is outside the arm the entry describes.
A mechanism or reliability lens should settle it.

### [non-spec-recheck.1.review-single-source.1]

DECISION: filed two findings, both on text the non-spec lane has never reviewed — BECAUSE the
non-spec lane's last pass was 2026-09-17 (commit `14f82426c`) and the delta since then rewrote
CONF-1, the tier-3/tier-10 test sections, DOCS-1, DOCS-2, SCHEMA-1's comment block, the Design
section and the summary's accepted-failure-modes block — ALTERNATIVES: rejected four near-misses,
each recorded below.

FACT: the delta this lane owes is `git diff 14f82426c HEAD -- <non-spec-changes.md>` (1487 diff
lines) and the same over `summary.md` (875). The snapshot at
`scratchpad/cp-snap/0081-opt2/spec-recheck-r4-prefix` is the SPEC lane's baseline and shows only a
single §6.2 hunk; it is useless for this lane. Use the git range, not the snapshot.
EVIDENCE: proposals/0081_.../*.non-spec-changes.md, git 14f82426c

FACT: prune 4's claim (commit `a6abb507b`) that the summary's block carries "the two residues
stated nowhere else" is false for BOTH residues. Residue 1 is spec-changes.md:94-127 (two
bullets); residue 2 is non-spec-changes.md:3576-3581 verbatim on the same phrase, and the summary's
own 0071 impact row at summary.md:1017 already records that the MCP-arming symptom "stands under
`## Defects in the shipped tree that this proposal does not stage` ... in the accepted failure
modes of the non-spec changes, and in the problem statement". Four sites.
EVIDENCE: summary.md:419-428, :1017; non-spec-changes.md:3576-3581; spec-changes.md:94,107

WATCHOUT: four candidates look like (g) copies and are not. (1) The Testing tier-3 "behavioural
cases" list (non-spec-changes.md:3212-3245) re-enumerates CONF-1's nineteen cases, but each bullet
is a name plus a drive note and nothing is implementable from it alone; the tier-10 sibling
(:3248-3252) is already a pure pointer. (2) DOCS-2's `Shutdown` row (:2555) restates rule 15's
outcome meanings verbatim against the staged §4.7 row (spec-changes.md:273) which explicitly
declines to; that is the one licensed reader-vocabulary restatement. (3) The checklist's per-step
paragraphs and summary.md:1023-1049's per-deliverable index share whole sentences verbatim; they
are two indexes of one deliverable set, excluded by the lens. (4) The DOCS-1 table row and the
CODE-3 Go comment are both licensed carriers per [spec-recheck.1.review-single-source.1] — what I
filed is their DISAGREEMENT, not their existence.
EVIDENCE: non-spec-changes.md:3212-3252, :2555, :791, :2416; spec-changes.md:273

DEFERRED [implementation-checklist.md, S7]: S7 enumerates DOCS-1 as "gains the row matching the new
§6.2 edge, its `slot_cleanup` → `released` row takes its trigger replacement, the page's
`slot_cleanup -> leaked` clause takes its replacement, and the pod state machine paragraph ..."
and still does NOT name the `receiving_uploads` → `running` trigger-cell replacement that DOCS-1
stages (non-spec-changes.md:2419-2431) and that `## Files touched on application` does list. My
prompt's already-found-and-fixed list carries exactly this title, so I did not re-file it; the fix
round added the other three items and dropped the one the finding named. Verified at HEAD:
implementation-checklist.md:29. Someone should close it in the checklist.

MISTAKE: the round that "fixed" the S7 enumeration finding rewrote S7 without adding the item the
finding named. Cost: the defect is now masked behind a do-not-relitigate entry.

### [non-spec-recheck.1.review-test-coverage.1]

DECISION: filed exactly one finding, that no listed adapter case drives a `Shutdown` whose
`Runtime.Close` returns an error on a `running` slot — BECAUSE that is disposition-table row 2
(spec-changes.md:624), the only row that produces a `leaked` cleanup-outcome report, and the
Testing section pins only the tree-failure half of the same decision (non-spec-changes.md:2780-2788)
while the close-failure case it does list is scoped to the §10.1.4 hold termination
(:2789-2795), a different site with a different completion predicate — ALTERNATIVES: rejected
filing (i) CODE-7 having no tier-3 case although checklist S11 names tier 3 (the decode is pure
logic; the wire half is the descriptor gate), (ii) the three new DOCS-1 trigger cells taking no
tier-11 gate (the matching spec fence entries are now bare pointers, so no cross-carrier
substring gate is constructible), (iii) the `superseded` arm not being driven with a
`RecycleScrub` (one exit helper, absent+reclaimed already pin it), (iv) the four-row
precondition case not asserting the whole-pod scrub does not run (nice-to-have).

FACT: `probeRuntime.Close` in `pkg/adapter/slotsession_test.go:49-55` always returns nil and has
no error hook, so the CODE-1/CODE-2 fixture list ("Reuse the internal fixtures that already
exist … `probeRuntime`", non-spec-changes.md:2851-2855) cannot drive a failed runtime close as it
stands; the missing case needs a one-field fixture extension the same way `Server.removeSlotTreeFn`
was minted for the tree error. — EVIDENCE: pkg/adapter/slotsession_test.go:49-55

FACT: the disposition table's nine rows map onto listed cases as follows, derived once so a later
round need not: row 1 = "Bound and started" (:2909); row 3 = "A cleanup whose tree removal fails
keeps the hold", `running` arm (:2780); rows 4/5 = "`exited_cleanly` on a reclaim the runtime
never held" (:2955) and "The clean arms answer true" (:2980); row 6 = the park-release arm of "The
hold refuses admission until the teardown returns having completed" (:2760ff); row 7 = "A cleanup
whose runtime close fails keeps the hold" at the §10.1.4 site (:2789); rows 8 and 9 are negative
statements of shipped behaviour with no case and need none. Row 2 is the only row with no case.
— EVIDENCE: spec-changes.md:620-628; non-spec-changes.md:2760-2795, :2909, :2955, :2980

WATCHOUT: row 7 for `releaseSessionSlot` (an act failing after deregistration on a release outside
a `Shutdown`) is not drivable at all: `removeSlotTreeVia` is staged as a seam on the `Shutdown`
path alone and "its two other callers … keep calling it directly"
(non-spec-changes.md:~520-527). Do not file that as a missing test; it is a stated limit of the
seam, not an omission.

DEFERRED [pkg/sandbox/slotstate/slotstate.go]: the const doc comments at :35 ("dispatched to the
runtime with the slotId"), :41-42 ("workspace was removed, processes killed, and slotId
released") and :45 ("cleanup timed out"), plus the `ValidTransitions` gloss lines at :100 and
:104, carry exactly the three statements SPEC-4/DOCS-1 retire; CODE-3 stages only the added edge
line (non-spec-changes.md:785-796) and files-touched says "`ValidTransitions()` and its doc
comment" (:3812). `pkg/gateway/runtime/slothealth/slothealth.go:33` carries the retired
"cleanup timeout exceeded" gloss as a further site. This restates the live DEFERRED at
review-log.md:821-826 and CORRECTS the FACT at review-log.md:1665-1670, which asserts
`slotstate.go:100`'s `→ running` gloss "is staged by CODE-3": CODE-3's staged text adds a line
and replaces none, so that ground is not discharged.

CORRECTS [review-log.md:1665-1670, "the new `receiving_uploads ──→ running` fence replacement
strands no gate and no unstaged carrier"]: the `pkg/sandbox/slotstate/slotstate.go:100` half is
wrong — see the DEFERRED above. The `docs/reference/state-machines.md:235` half checks out
(DOCS-1 now stages that row, non-spec-changes.md:2416-2429).

USEFUL [Settled, "The completion predicate differs by site, deliberately, and all three are
stated."]: it is what turned "the close-failure case is listed" into "the close-failure case is
listed for a different site", which is the whole of the finding.

### [spec-recheck-2.1.review-applicability.1]

DECISION: returned EMPTY — BECAUSE I re-verified every staged spec anchor against the tree and every one matched exactly once, every markdown anchor resolved to a live heading, and every shipped gate that reads a replaced line survives or is disposed of in the same step — ALTERNATIVES: filing the §6.2 `claimed ──→ draining` single-block edit as an unresolvable anchor; rejected, see the FACT below.

FACT: the spec-changes delta since the r5 snapshot is ONE WORD ("row"→"arm" in the Spec-files-touched §16.1 bullet). Everything else in `diff -ru -x '*.review-log*.md'` is in non-spec-changes.md, summary.md and the checklist, which this loop's scope excludes. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1301

FACT: every "reads, verbatim" anchor in the staged spec edits was confirmed present EXACTLY ONCE by `grep -cF` this round: spec/04 (§4.1 sentence, §4.7 `Shutdown` row, §4.7 `DemoteSDK` row, §4.7 `ReportSessionScrub` row, §4.7.9 step 5), spec/05 (action list, `**Scrub model.**` opener, reporting sentence, leaked-outcome sentence, whole-pod-trigger parenthetical), spec/06 (mid-resume clause, Client-visibility clause, the three fence annotations, the three projection-prose clauses), spec/07 (§7.2 preamble, step 2 tail, step 3, §7.3 item 4, the atomicity paragraph), spec/12 (prose write clause, read clause, DDL comment), spec/15 (the four `SETUP_COMMAND_FAILED` sentences). Do not re-derive; re-run only the anchors a later fix touches. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,545,561

FACT: the §6.2 `claimed ──→ draining` edit (spec-changes.md:852-859) is the ONE staged replacement that gives a single code block with no "reads, verbatim" leg. It is still deterministic: the tree's text (spec/06_warm-pod-model.md:95-97, "claim deleted on a pod / with recycle.enabled: false") differs from the block, the block carries the "see §4.6.1" pointer the deliverable's stated intent and the Spec-files-touched entry both describe, and the `Occupancy projection` group holds exactly one `claimed ──→ draining` (the other three sit in the `Recycle edges` and concurrent-occupancy groups). A future lens that "finds" this should not file it. — EVIDENCE: spec/06_warm-pod-model.md:95, proposals/0081_.../0081_....spec-changes.md:855-859, :1280-1282

FACT: the four shipped gates that read a line SPEC-1/SPEC-3/SPEC-4 replace all survive or are disposed of. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts only the addressing sentence, which the §4.7 row replacement keeps word for word (tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,67). `TestRecycleTriggerConsistentAcrossSpec47And52_F5215` needs "recycle disposition", `ReportPodScrub`, `podId`, `cleanupCommands`, `cleanupTimeoutSeconds`, "does not block the response on the scrub" and the §5.2 link in the §4.7 `Shutdown` row — all of them live in the row remainder SPEC-1 leaves unchanged (tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-101). `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` matches edge strings only, never the annotations SPEC-4 rewrites (tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-36,54-72). `podStateGatewayWrittenSentence` DOES break on the §12.6 write-clause re-key, and SPEC-3 disposes of it by name plus the `grep -rn sessions_served tests/` sweep in the same step (tests/tier11_docs/spec_28_register_writers_test.go:99-101, spec-changes.md:603,825-829).

FACT: `grep -rn sessions_served tests/ --include=*.go` returns exactly four files — concurrent_slot_lifecycle, spec_28_register_writers, spec_28_index_rows (a slugify unit case, not a §12.6 assertion) and three non-tier-11 files. The SPEC-3 sweep reaches both tier-11 carriers. This refutes, again, the "the grep matches neither gate" line of attack. — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170, tests/tier11_docs/spec_28_register_writers_test.go:99

FACT: every markdown anchor the staged spec text emits resolves. Checked live: `#47-runtime-adapter`, `#471-role-and-gateway-rpc-contract`, `#461-warm-pool-controller-pod-lifecycle`, `#463-crd-field-ownership-and-write-boundaries`, `#479-startup-sequence-for-type-agent-runtimes`, `#49-credential-leasing-service`, `#52-pool-configuration-and-execution-modes`, `#61-what-a-pre-warmed-pod-looks-like`, `#62-pod-state-machine`, `#71-normal-flow`, `#73-retry-and-resume`, `#74-upload-safety`, `#101-horizontal-scaling`, `#151-rest-api`, `#154-runtime-adapter-specification`, `#1542-rpc-lifecycle-state-machine`, `#1543-runtime-integration-levels`.

FACT: the two multi-block insertion points are clean and unambiguous. §4.7.1's block goes between the `*Adapter → Gateway RPCs:*` table (spec/04_system-components.md:688) and `#### 4.7.2` (:695), with nothing between but the two table rows. §15.4's two blocks go between `**SDK-warm demotion contract:**` (spec/15_external-api-surface.md:1469) and `#### 15.4.1` (:1471). §7.1's paragraph goes after the atomicity paragraph (spec/07_session-lifecycle.md:23) and before the `(executionMode, isolationProfile, scrubPolicy summary)` continuation line (:24), and the proposal's description of that fenced listing is accurate.

FACT: §12.6's replaced read clause cites "the **Session count limit:** bullet", which exists at spec/05_runtime-registry-and-pool-model.md:488, and the trailing §5.2 link the proposal says resolves it is on the same physical line (spec/12_storage-architecture.md:481).

FACT: every checklist box is unchecked and S1 discloses its three forward references to S4 on its own line, so the class-1 sweep found nothing to file. — EVIDENCE: proposals/0081_.../0081_....implementation-checklist.md:17

UNVERIFIED: I checked gate breakage only for gates that read a line the staged spec text REPLACES. I did not enumerate gates that would newly fail because of text the staging ADDS (for example a §4.7.1 block whose new table row count or new bold-lead paragraphs some structural spec gate counts). Nothing in `tests/tier11_docs` looked like such a counter, but a later edit-sites or fresh lens could confirm it.

### [spec-recheck-2.1.review-citations.1]

DECISION: returned EMPTY. — BECAUSE every verbatim anchor, every spec-section attribution and
every code/test citation in the spec staging re-verified clean this round. — ALTERNATIVES:
filing the §28.4 `ABSENT`/`coordination_generation` precedent sentence, rejected because the
log records it chased and judged decorative twice ([spec.16.review-citations.1],
[spec.19.review-citations.1]).

FACT: the delta in spec-changes.md this round is ONE WORD, line 1301 `row`→`arm` in the
`spec/16_observability.md` bullet of "Spec files touched". Everything else in the
spec-recheck-r5 diff is in non-spec-changes.md, the checklist and the summary. Confirm with
`diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/spec-recheck-r5 proposals/0081_*/`
before spending a sweep. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:1301

FACT: full verbatim-anchor sweep, all 23 blocks confirmed byte-exact at these lines.
spec/04_system-components.md:157 (§4.1 third sentence), :686 (§4.7 `Shutdown` row opening),
:674 (`DemoteSDK` row), :692 (`ReportSessionScrub` row), :854 (§4.7.9 step 5),
:409/:415/:416 (§4.6.1 projection sentence and the two claim-deletion bullets).
spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**` opening), :545 (the action
list, the reporting sentence and the leaked-outcome sentence all on one line), :561
(`**Whole-pod replacement trigger:**` parenthetical). spec/06_warm-pod-model.md:80 (the three
projection-prose clauses), :95 (`claimed ──→ draining`), :148 (`slot_cleanup ──→ leaked`),
:152-153 (`receiving_uploads ──→ running`), :155 (`slot_cleanup ──→ released`), :234 (resuming
cancel clause), :290 (`**Client visibility:**` clause). spec/07_session-lifecycle.md:23
(atomicity paragraph tail), :210 (§7.2 preamble premise), :213 (step 2 tail), :214 (step 3),
:414 (§7.3 list item 4). spec/12_storage-architecture.md:481 (prose write and read clauses),
:494 (DDL comment). spec/15_external-api-surface.md:1136 (all four `SETUP_COMMAND_FAILED`
sentences), :1469 (`**SDK-warm demotion contract:**`).

FACT: every insertion point resolves. §4.7.1's `*Adapter → Gateway RPCs:*` table ends at
spec/04:693 and `#### 4.7.2` is :695; §15.4.1 is spec/15:1471, directly after the SDK-warm
demotion paragraph at :1469; §7.1's atomicity paragraph at spec/07:23 is followed at :24 by the
`(executionMode, isolationProfile, scrubPolicy summary)` continuation line the staging names.
All linked anchors exist: `#471-role-and-gateway-rpc-contract`, `#479-...`, `#461-...`,
`#463-...`, `#49-credential-leasing-service`, `#154-runtime-adapter-specification`,
`#1542-rpc-lifecycle-state-machine`, `#52-pool-configuration-and-execution-modes`,
`#101-horizontal-scaling`, `#74-upload-safety`.

FACT: the SPEC-3 carrier table's carrier / not-a-carrier split is discriminating, not sloppy,
and one file sits on both sides correctly. `pkg/adapter/gatewaycontrol/scrubreport.go:12-14`
(`SessionScrubOutcome`) says only that the CLEANUP runs on every release, so it is listed as
not a carrier; `:71-72` (`Client.ReportSessionScrub`) says the adapter REPORTS on every
release, so it is a carrier row. Do not "fix" the apparent double listing.

FACT: the staged §4.7 `Shutdown` row's graceful-shutdown-signal condition is shipped behaviour,
word for word. `pkg/adapter/session.go:249-258` carries the same reasoning ("The signal is
pod-global and names no session, so it goes out only when the deregistration left the registry
holding no bound entry"), gated on `!boundRemains` at :259. §29.4 step 13
(spec/29_communication-scenarios.md:703-711) is that signal's trace step (`drainViaLifecycle`,
`pkg/adapter/session.go:294+`), so the appended sentence lands on the right step.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-77) asserts
two things, and the staging satisfies both: each carrier's row is ONE physical line found by
`lineContaining("| \`ReportSessionScrub\` |")`, and each contains the exact string
`"The request is session-scoped: it is addressed by the identifier of the released session and
names no slot."` (the `opener` const at :67). SPEC-3 preserves that sentence verbatim.

WATCHOUT: `RemoveTree` lives in `pkg/adapter/slotlayout/tree.go:58`, NOT `pkg/sandbox/slotlayout`.
`slotstate` is the one under `pkg/sandbox`. A grep on the wrong root returns nothing and reads
as an absent symbol. — EVIDENCE: pkg/adapter/slotlayout/tree.go:58

FACT: `tests/claim-map.json` holds 76 claims, 32 WIRED / 24 UNWIRED / 20 ABSENT, and every
`coordination_generation` fence row is UNWIRED with `deferral_id: "R16"`. This is the standing
`### Open` question at review-log.md:808 and the WATCHOUT at :1081; it has now been re-derived a
third time. Promote the WATCHOUT or close the Open entry, but stop paying for the re-derivation.

USEFUL [review-log.md:1081 WATCHOUT on the ABSENT/coordination_generation precedent sentence]:
saved a filing that two prior citation rounds had already lost.
USEFUL [Traps: "**MISTAKE, THREE rounds choosing an outcome predicate for the
`slot_cleanup ──→ released` fence annotation**"]: kept me from reading the bare `(see §5.2)`
pointers as an underspecified fence entry.

### [spec-recheck-2.1.review-client-surface.1]

DECISION: returned an EMPTY findings list for the client-facing-surface lens on the spec staging — BECAUSE the only delta in `spec-changes.md` since the last converged sweep is a one-word "row"→"arm" edit at the `Spec files touched` §16 bullet, and a fresh sweep of every client-facing spec surface the staging touches (§4.1 Request Message Scope, §4.7 `Shutdown`/`DemoteSDK`/`ReportSessionScrub` rows, §4.7.1 carriage table + rules, §15.1 `SETUP_COMMAND_FAILED` row, §15.4 two blocks, §6.2 client-visibility clause, §29.4 step 13) found every verbatim quote exact and every parallel representation either staged or genuinely unaffected — ALTERNATIVES: filing the `docs/api/internal.md` gRPC-status table (it lists no `ABORTED`/`INVALID_ARGUMENT` row and both new refusals use them) and the proto `ErrorCode` catalog comment; both rejected, their remedies are docs/schema-lane edits that this loop may not land.

FACT: the wire field numbers the staging picks are all the next free slot in the shipped proto, verified message by message: `PrepareWorkspaceRequest` 4 reserved so 5/6 free (schemas/lenny-adapter.proto:682-696), `FinalizeWorkspaceRequest` 5 reserved so 6 free and it ALREADY carries `mid_session = 4` (:706-731, which is why SCHEMA-1 adds `mid_session` only to `PrepareWorkspaceRequest` and the §4.7.1 carriage table's "carried" cell for `FinalizeWorkspace` is still right), `RunSetupRequest` 4 reserved so 5 free (:846-857), `AssignCredentialsRequest` 3 reserved so 4 free (:1021-1031), `ShutdownRequest` tops out at 6 so 7/8 free (:1609-1636), `ResumeRequest` tops out at 14 with 15 reserved so 16 free (:1405-1412 region). — EVIDENCE: schemas/lenny-adapter.proto:682-731, :846-857, :1021-1031, :1609-1636

FACT: no client SDK and no OpenAPI surface carries anything this proposal changes. `grep -rln "ShutdownRequest|SlotReclaim|bind_attempt|ERROR_CODE_SLOT" sdks/ examples/` returns nothing (the adapter proto is gateway↔adapter only; `examples/runtimes/echo/` that spec/15:1466 names does not exist in the tree). `pkg/gateway/externalapi/openapi/openapi.json` — note the path, NOT `pkg/gateway/openapi/openapi.json` as the lens brief states — enumerates no REST error codes at all (its only error enum is `category`, :32) and models `retryPolicy.nonRetryableFailures` as an open `array of string` (:162), so the §15.1 `SETUP_COMMAND_FAILED` rewrite owes it no edit. — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:32,162

FACT: the only doc carrier of the §15.1 `SETUP_COMMAND_FAILED` row is `docs/reference/error-catalog.md:129`; `docs/api/rest.md`, `docs/client-guide/sdk-examples/curl.md` and `docs/client-guide/session-lifecycle.md` match only on `setup-output`, not on the code. DOCS-3 is therefore the complete docs-side mirror. — EVIDENCE: docs/reference/error-catalog.md:129

FACT: the staged §4.7 `Shutdown` row's claim that the §15.4.2 graceful-shutdown signal "is pod-global and names no session" is true on the wire: the JSONL `shutdown` frame's required set is `type`, `reason`, `deadline_ms` with no `sessionId`. Nothing in spec/15 or spec/28 states the signal is emitted unconditionally at every session end, so the row's new co-tenant condition falsifies no other spec site; §29.4 step 13 was the one that did and the staging edits it. — EVIDENCE: schemas/lenny-adapter-jsonl.schema.json `$defs.shutdown`; spec/28_communication-channels.md:618-624; spec/15_external-api-surface.md:1686-1705

WATCHOUT: `docs/api/internal.md:485-497` carries a gateway↔adapter gRPC status-code table ("When used") that lists neither `ABORTED` nor `INVALID_ARGUMENT`, which rules 1, 2, 5, 8 and 10 all newly answer on, and the same page embeds hand-transcribed proto messages (`StartSessionRequest` at :108-117, `DemoteSDKRequest` at :265-282). It is in no edit list I could find. Do not file it in the spec lane — the remedy is a docs edit — but the non-spec loop should decide whether DOCS-n takes it. — EVIDENCE: docs/api/internal.md:487-497

DEFERRED [docs/api/internal.md]: the page's gRPC status-code table (`docs/api/internal.md:487-497`) becomes incomplete once the staging lands: it presents itself as the catalog of gateway↔adapter statuses and omits `ABORTED` (rules 2, 5, 8 and the §15.4 reclaim-hold conformance block) and `INVALID_ARGUMENT` (rules 1 and 10). What is true instead: an adapter on this contract also answers `ABORTED` for a superseded bind attempt, a held slot identifier and a failed start confirmation, and `INVALID_ARGUMENT` for a request failing the pairing rule or the teardown-pairing rule. No proposal deliverable names this file.

USEFUL [The adapter `ErrorCode` enum has no spec-side enumeration] (review-log.md:237) and the matching WATCHOUT at review-log.md:1106: saved me from filing the proto's "The catalog below mirrors spec §15.1" comment (schemas/lenny-adapter.proto:556) as a spec-lane surface gap for the two new `SLOT_BIND_*` codes. Confirmed independently: `grep -rn "ERROR_CODE_" docs/ spec/ --include=*.md` returns nothing, so the enum has no reader-facing mirror anywhere, and `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27` already has no §15.1 row.

### [spec-recheck-2.1.review-docs-alignment.1]
DECISION: returned zero findings — BECAUSE the spec-changes delta since spec-recheck-r5 is one word ("row"→"arm" at spec-changes.md:1301, the §16.1 files-touched line), and the docs-mirror index at the tail of spec-changes.md resolves every edited spec surface to a named docs deliverable — ALTERNATIVES: filing the self-recreation accepted failure mode as undocumented in landing spec text; rejected because the caller's directive fixed the spec-changes Edge-cases section as its single home and the remedy would add text against a prune directive
FACT: the docs carriers of the withdrawn "reports on every session release" universal are exactly three, and all three are staged: docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 (DOCS-4) and docs/reference/adapter-contract.md:75,:81 (DOCS-2). docs/operator-guide/multi-tenancy.md:72 states only that the cleanup RUNS at each release, with no report clause, so it is correctly excluded from SPEC-3's carrier table — EVIDENCE: docs/operator-guide/multi-tenancy.md:72
FACT: no docs page enumerates the per-slot cleanup ACTION list, so SPEC-3's two added acts (credential directory removal, §4.9 timer cancellation) need no docs mirror. The only near miss is docs/runtime-author-guide/lifecycle.md:37, which describes `/workspace/slots/` at `idle` and states no cleanup acts — EVIDENCE: docs/runtime-author-guide/lifecycle.md:37
FACT: the only docs mirror of §4.6.1's claim-deletion bullets is docs/reference/state-machines.md:138 (DOCS-1 owns it). docs/getting-started/architecture.md:237 names the projection's inputs generically and states no claim-deletion rule, so it does not become wrong — EVIDENCE: docs/reference/state-machines.md:138, docs/getting-started/architecture.md:237
FACT: docs/reference/state-machines.md:248 ("Served-session count reaches `recycle.maxSessionsPerPod` on a session release") survives SPEC-3 unchanged. SPEC-3 re-keys the §12.6 INCREMENT trigger onto the report and leaves the per-release EVALUATION point alone, and :248 states the evaluation — EVIDENCE: docs/reference/state-machines.md:248, spec-changes.md §12.6 block
FACT: `maxSlotRetries` is `const maxSlotRetries = 1`, so the Edge-cases bullet's "defaults to one retry" checks out — EVIDENCE: pkg/gateway/sessionserver/start.go:2720
WATCHOUT: docs/reference/metrics.md is staged under CODE-9, not under a DOCS-n id. A lens that greps the DOCS deliverables alone will file a false "metrics doc missing" finding — EVIDENCE: non-spec-changes.md:2006

### [spec-recheck-2.1.review-edit-sites.1]
DECISION: returned EMPTY for the edit-sites lens on the spec staging — BECAUSE the delta in
`*.spec-changes.md` since the last converged sweep is one word (`row` → `arm` at
spec-changes.md:1301, inside the `spec/16_observability.md` entry of "Spec files touched"), and
a fresh identifier-by-identifier sweep of spec/, docs/, schemas/ and charts/ turned up no spec
surface that becomes wrong and is missing from the staged lists — ALTERNATIVES: filing the two
marginal sites in FACT/DEFERRED below, rejected because both remedies land in non-spec files,
which this loop may not edit.
FACT: the three spec carriers of the retired `leaked` trigger ("cleanup timeout exceeded") are
exactly spec/05_runtime-registry-and-pool-model.md:545 (Slot cleanup bullet), :561 (Whole-pod
replacement trigger parenthetical) and spec/06_warm-pod-model.md:148 (fence entry); all three are
staged. spec/06:160 (`leaked` slot semantics) states no trigger and correctly stays untouched.
No fourth spec site exists — EVIDENCE: `grep -rn "cleanup timeout" spec/` returns only 05:545 and
05:561; `grep -rn "leaked" spec/` outside 05/06 returns only 15:594, 29:1512 and 04:692, none of
which states the trigger.
FACT: the spec carriers of the withdrawn "reports on every session release" universal are exactly
spec/04:692, spec/05:453, spec/05:545, spec/12:481 and spec/12:494 (DDL comment); SPEC-3's carrier
table stages all five. docs/operator-guide/multi-tenancy.md:72 carries the cleanup clause WITHOUT
the report clause, so the carrier table is right to carve it out, while
docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 do carry the
report clause and are both in the table under DOCS-4 — EVIDENCE: `grep -rn "at each session
release\|every session release" spec/ docs/ schemas/`.
FACT: §4.7.1 ALREADY EXISTS in the tree as "Role and Gateway RPC Contract"
(spec/04_system-components.md:659), and both RPC tables (:686, :692) sit inside it, so SPEC-5's
"§ 4.7.1 (after the ... RPC tables)" is an append inside an existing subsection rather than a new
numbered subsection. No renumbering of §4.7.2 through §4.7.11 is implied and none is needed —
EVIDENCE: spec/04_system-components.md:659, :695.
FACT: the §15.4 insertion anchor is real and unambiguous — the `**SDK-warm demotion contract:**`
paragraph is the last block before `#### 15.4.1` — EVIDENCE: spec/15_external-api-surface.md:1469
(paragraph) and :1471 (heading).
DEFERRED [schemas/lenny-adapter.proto]: SCHEMA-1 says the `SESSION_SCRUB_OUTCOME_LEAKED` comment
"already states its own case as a resource that could not be reclaimed and is unedited"
(non-spec-changes.md, the scrub-outcome comment block). Under SPEC-3's disposition table row 3 (the
runtime close succeeds and a later act fails) the adapter reports `released` even though a resource
was not reclaimed, so the shipped comment at schemas/lenny-adapter.proto:445-448 ("a resource could
not be reclaimed at the session release") now over-states when LEAKED is reported. What is true: a
cleanup reports LEAKED on the terms §5.2's disposition table states, which for a slot that reached
`running` is a failed runtime close. Remedy is one more verbatim replacement inside SCHEMA-1; it is
not a spec-lane fix.
UNVERIFIED: schemas/lenny-adapter.proto:256-257 ("carries the §5.2 per-slot and whole-pod scrub
reports (ReportSessionScrub, ReportPodScrub) the adapter emits on release") is a `GatewayControl`
service doc comment that is arguably a fifth proto carrier of the report universal and is in no
edit list and in no SPEC-3 carrier-table row. I judged it a clause-level gloss ("on release", not
"on every session release") and did not file it. The non-spec loop should decide.

### [spec-recheck-2.1.review-fresh.1]
DECISION: returned an EMPTY findings list for the fresh-holistic lens — BECAUSE the spec-lane delta since snapshot `spec-recheck-r5` is one word in spec-changes.md ("fail-closed row" → "fail-closed arm", :1301), and a full independent re-derivation of every staged anchor, link and mechanism turned up nothing meeting the bar — ALTERNATIVES: filing the narrowed §15.1 exclusion sentence (SPEC-5 drops the "any other setup-window failure stays the retryable fallback" universal for requests other than the setup-command request) — rejected because the row's own opening sentence already defines the code and the DIRECTIVE prefers reductions; filing the §5.2 reclaim-hold clause "A request that resolves an entry without creating one is refused on the same terms" as sweeping in mandatory credential controls — rejected because the hold opens at deregistration, so no entry exists to resolve, and the standing-context entry already scopes this to a re-check only if a control moves onto `ensureSlotStateLocked`.
FACT: every "reads, verbatim" anchor in the staged spec edits still matches the tree byte-for-byte — EVIDENCE: spec/04_system-components.md:157, :415, :416, :674, :686, :692, :854; spec/05_runtime-registry-and-pool-model.md:453, :488, :545, :561; spec/06_warm-pod-model.md:80, :95, :148, :152, :155, :234, :290; spec/07_session-lifecycle.md:23, :210, :213, :214, :414; spec/12_storage-architecture.md:481; spec/15_external-api-surface.md:1136; spec/29_communication-scenarios.md:704-711
FACT: all 21 distinct markdown anchors used in the staged blocks resolve to real headings — EVIDENCE: spec/04_system-components.md:338,606,657,659,848,1099; spec/05:365; spec/06:3,78; spec/07:3,378,438; spec/10:3; spec/15:614,1458,1686,1707
FACT: the §6.2 fence-annotation reductions do not break the shipped tier-11 per-slot gate, which matches on the edge arrows alone and never on the parenthetical — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37, :56-58
FACT: SPEC-3's `**Whole-pod replacement trigger:**` parenthetical replacement leaves both substrings that gate reads intact ("counted within a rolling 5-minute window", "counted persistently for as long as the slots remain leaked" both sit earlier in the same bullet) — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561 vs tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92-95
FACT: the withdrawn "report on every session release" universal has exactly the docs carriers the SPEC-3 table names; `docs/operator-guide/multi-tenancy.md:72` genuinely carries no reporting clause and is correctly listed as a non-carrier — EVIDENCE: docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33, docs/operator-guide/multi-tenancy.md:72, docs/reference/adapter-contract.md:81
FACT: `slotlayout.RemoveTree` does remove `/run/lenny/slots/{sessionId}` and `deregisterSlotLocked` does cancel every armed §4.9 timer, so SPEC-3's two added action-list acts record shipped behaviour — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-70, pkg/adapter/slotsession.go:174-181
FACT: the ten-second window and its provenance check out exactly as the SPEC-3 commentary states — EVIDENCE: pkg/adapter/holdstate.go:197-202; `git show -s 3997f502b` = 2026-08-22
WATCHOUT: the §5.2 disposition table's `leaked` column and §7.1's acknowledged-clean predicate disagree on the surface for a cleanup that completes cleanly but whose response is lost (row 1 says "Not entered"; §7.1 leaks an unanswered reclaim). The paragraph after the table reconciles them explicitly ("A slot enters `leaked` on the gateway's reading of the report or of the response, so a §7.1 reclaim the adapter does not answer enters it, and its hold ends on the terms of the row the cleanup on the pod met"). Do not file this; the sentence is doing the work — EVIDENCE: spec-changes.md, paragraph immediately after the disposition table
USEFUL [Already found and fixed / Already refuted lists in the prompt]: the refuted entry on SPEC-3's carrier table saved me re-deriving that `grep -rn sessions_served tests/` reaches `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-176`, which is the one gate SPEC-3's §12.6 read-clause deletion actually turns red.

### [spec-recheck-2.1.review-kubernetes.1]

DECISION: EMPTY findings list for the Kubernetes-idiom lens on the spec-recheck-2.1 staging — BECAUSE the delta in the spec-changes file since the r5 snapshot is one word ("row" -> "arm") in the SPEC-6 §16.1 files-touched bullet (spec-changes.md:1301), which touches no Kubernetes surface; and the only CRD-bearing staged block, SPEC-4, re-verified clean on every idiom the lens owns — ALTERNATIVES: filing the staged §4.6.1 "phase the pod currently projects (the controller's own last level, which it may read back because it is the sole writer of that field)" input as a status-read-back anti-pattern, rejected because `OccupancyReconciler` does `For(&lennyv1.Sandbox{})` (pkg/controller/warmpool/occupancy.go:307), so the read-back is level-triggered off its own owned watch, and the sole-writer claim is already normative spec text ("the gateway does not write `Sandbox.status`", spec/04_system-components.md:409; "The gateway holds no `sandboxes/status` grant", :632).

FACT: the staged §4.7 `ReportSessionScrub` replacement row (spec-changes.md:762) preserves the `lenny.dev/drain-request` clause verbatim from the shipped row (spec/04_system-components.md:692). That annotation is a gateway write to a POD annotation, not to a CRD status subresource, and §4.6.3's RBAC paragraph grants the gateway `get`/`patch` on Pods for exactly it (spec/04_system-components.md:632). A lens tempted to file it as a gateway-writes-controller-state anti-pattern should stop there: the WarmPoolController owns the resulting phase transition (:418). — EVIDENCE: spec/04_system-components.md:418, :632, :692

FACT: this staging creates no new etcd write, no finalizer, no webhook, no CRD watch and no controller on a synchronous request path. The whole mechanism (bind_attempt token, the two adapter error codes, the reclaim hold, the §5.2 disposition table) lives on the gateway-to-adapter gRPC surface and in the adapter's in-process registry. The CRD surface is reached only indirectly, through the occupancy projection SPEC-4 re-keys. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:830-905

USEFUL [spec.22.review-kubernetes.1], [spec-recheck.1.review-kubernetes.1], [spec-recheck.4.review-kubernetes.1], [non-spec-recheck.1.review-kubernetes.1]: four prior Kubernetes-lens verdicts on this staging, all EMPTY, with the SPEC-4 idiom checks already itemised. They let this pass be a delta check plus a re-verification of the two sole-writer citations rather than a re-derivation of the projection.

OPEN: the standing `UNVERIFIED` on whether an entry stamped with a dead token on the EXCLUSIVE path can strand a pod's `SandboxClaim` (review-log.md:2243) remains unsettled by this pass. Its remedy, if any, is in CODE-8, which is non-spec-lane text, so this loop cannot close it even if it were a finding. A reliability or Kubernetes lens in the non-spec loop should settle whether WarmPoolController's orphaned-claim GC reaches the case.

### [spec-recheck-2.1.review-mechanism.1]

DECISION: returned an EMPTY findings list — BECAUSE the spec-changes delta since the r5 snapshot is one word (`row`→`arm` at spec-changes.md:1301, the residue of the already-fixed "row"→"arm" sweep), and a full re-trace of the staged mechanism found no break — ALTERNATIVES: filing the §4.1 scrub clause as a restatement of rule 10 (rejected: it links §4.7.1 and is a clause-level summary, which the (g) carve-out exempts, and it is the single-source lens's ground anyway); filing disposition-table row 5's `Entered` cell against non-compensating unconditional-`Shutdown` callers that never read the clean-exit flag (rejected: speculative, code-lane, and the table already keys `leaked` on "the gateway's reading of the report or of the response").

FACT: the §4.7.1 admission cascade is TOTAL and drift-free when traced request by request. Walked all five carriage-table request classes plus the §7.4 marked form against rules 1–7: a `mid_session` request with an empty `bind_attempt` resolving an entry passes rule 1 (both invalid arms need either "not marked mid_session" or a non-empty token), is not rule 3 (it resolves one), is excluded from rule 4 and rule 6 by the `mid_session` conjunct, misses rule 5 on the empty token, and lands on rule 7. No request falls off the end. — EVIDENCE: spec-changes.md:1043-1049

FACT: the hold column of the §5.2 disposition table matches the `completed` definition on all nine rows, and the clean-exit column matches rule 15 on all of them (rows 6–9 read "No `Shutdown` performs the cleanup", and rule 15 only quantifies over `reclaimed`/`superseded`/`absent`). Rows 4–5 are consistent with "every act that cleanup owes the slot" because a pre-`running` slot is owed no runtime close. — EVIDENCE: spec-changes.md:621-635, :1062

FACT: SPEC-4's §4.6.1 re-key is a COMPLETE partition of what the projection owns, which is why the §6.2 no-claim clause can be reduced to a pointer. `ProjectOccupancyPhase`'s no-claim switch has exactly two owning arms, `state.Reserved → Idle` and `state.Claimed → Draining`, and `default: return "", false` leaves every warm-fill and terminal phase to the Sandbox-to-Pod reconciler. A claim deleted while the pod projects `sdk_connecting` is therefore not a gap in the re-keyed bullets; it is a phase the projection does not own. — EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143

FACT: the §4.7 staged row's "[Section 15.4.2] graceful-shutdown signal" is grounded in the tree, not invented. The shipped handler comment reads "drainViaLifecycle sends the §15.4.2 DRAINING-state graceful-shutdown" and the gate above it is `if !boundRemains { s.drainViaLifecycle(...) }`, so the row's condition and its §15.4.2 anchor both mirror shipped code, even though the literal phrase "graceful-shutdown signal" appears nowhere in spec/15. Do not file the anchor as a false citation on a grep miss. — EVIDENCE: pkg/adapter/session.go:259-260, :294-299; spec/15_external-api-surface.md:1702

FACT: the `slot_assigned ──→ receiving_uploads` fence trigger is "workspace materialization begins for this slot", NOT "an upload frame arrived". The new `receiving_uploads → slot_cleanup` edge is therefore reachable on an upload-free plan, whose first adapter RPC is `FinalizeWorkspace`. An unreachable-trigger finding against that edge dies here. — EVIDENCE: spec/06_warm-pod-model.md:151

FACT: every verbatim anchor SPEC-3 and SPEC-4 quote out of spec/05 and spec/06 still matches byte for byte at this revision: the `**Slot cleanup:**` action list, its reporting sentence and its leaked-outcome sentence all sit on spec/05:545; the `**Whole-pod replacement trigger:**` parenthetical on :561; the `**Scrub model.**` opening on :453, which is 89 lines ABOVE the `maxConcurrentSessions > 1` heading at :542, confirming the concurrency-independence argument the proposal rests the reporting-rule placement on. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453, :542, :545, :561

USEFUL [Traps]: the trap "MISTAKE, FIVE edit passes in four rounds on ONE disposition-table column, the residue column" plus the caller directive on the deleted seventh column saved a full pass: two candidate observations I formed while tracing rows 3 and 7 were both residue-detail questions the post-table paragraph already answers, and both would have been the sixth pass on that column.

### [spec-recheck-2.1.review-performance.1]
DECISION: empty findings list — BECAUSE the spec-changes delta since the last converged sweep (spec-recheck-r5 snapshot) is a single word inside commentary ("fail-closed row" -> "fail-closed arm", spec-changes.md:1301), and nothing in that swap touches a rate, a budget, a store, or a failure path — ALTERNATIVES: re-deriving the whole staging under the capacity lens, which I did at the anchors below and which produced nothing above the bar.
FACT: this proposal adds NO store-backed state. The bind attempt token lives in gateway process memory per attempt and in the adapter's in-process registry entry; the reclaim hold is an in-process slot-identifier hold. No new etcd write, no new informer/watch, no new Redis key, no new Postgres column. The only Postgres write-rate change is a REDUCTION: SPEC-3 makes the `sessions_served` increment conditional on a cleanup-outcome report, and the report becomes a biconditional on "a `Shutdown` reclaiming a slot that reached `running`", so releases outside a `Shutdown` (SDK demotion, §10.1 hold-timeout termination, failed-start handler) stop writing — EVIDENCE: spec-changes.md:566-573 (scrub-model replacement), :782-800 (§12.6 write-trigger re-key).
FACT: the two new §16.1 counters introduce no new label dimension. `lenny_slot_compensation_superseded_total` is labeled `pool`,`k8s_pod_name` and `lenny_slot_shutdown_untokened_entry_total` by `k8s_pod_name` alone; both labels are already carried by shipped session-slot series — EVIDENCE: spec/16_observability.md:14-15 (`lenny_slot_failure_total`, `lenny_slot_pod_replacement_total`, same labels), :297 (the `k8s_pod_name` attribute row). Cardinality is per-pod, bounded by the pod inventory, not per-session or per-attempt.
FACT: §12.4 durable-fallback obligation is not engaged by this staging. No value the design relies on is Redis-backed or Postgres-backed and new; the staging explicitly leaves §10.1's coordinator handoff untouched and says why ("the bind attempt token is not a coordination generation") — EVIDENCE: spec-changes.md:1230-1232.
WATCHOUT: the capacity-lens argument that looks most promising and is NOT a finding is the "held for the life of the pod" cell on disposition-table rows 2, 3, 5 and 7 (spec-changes.md:623-631): a permanently held slot identifier that on rows 3, 5 and 7 does not enter `leaked` and so feeds no whole-pod replacement trigger. That is the design choice the SPEC-3 leaked-disposition finding already raised and the material skeptic refuted as a preference between workable designs; the transient refusal the hold returns is accounted as an ordinary windowed slot failure (spec-changes.md:635), which is the drain path. Do not re-file it without new evidence of a rate or budget it breaks.
WATCHOUT: the gateway-crash residue ("A compensation lost to a gateway crash leaves an entry no attempt can use", spec-changes.md ~:182-194) does state it is "strictly worse than what an entry-scoped fence left". That reads like a reliability-lens finding but is a recorded accepted failure mode in the spec file's own Edge-cases section, with two named recoveries each deferred to its own finding. The mechanism is settled; it is not filable under this lens.

### [spec-recheck-2.1.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability lens on the spec staging — BECAUSE the only delta inside `0081_....spec-changes.md` since the last converged spec review is one word of commentary (`row` → `arm` at spec-changes.md:1301, in the `spec/16_observability.md` entry of `## Spec files touched`), and a full re-read of the staged blocks (§4.7 rows, §7.1 reclaim paragraph, §7.2 steps 2-3, §5.2 scrub-model append + disposition table + reclaim-hold paragraph, §4.6.1/§6.2 projection re-keys, §6.2 fence and pre-`running` paragraph, §4.7.1 rules 1-15, §15.4's two blocks) surfaced no crash/restart/failover defect above the bar — ALTERNATIVES: filing the "life-of-the-pod hold with no gateway-visible signal" (table rows 3, 5, 7) as an unreclaimed-resource finding, which I rejected as a close variant of the already-refuted SPEC-3 leaked-withdrawal finding and of the barred capacity family.

FACT: a life-of-the-pod reclaim hold costs the pod NO slot capacity. The adapter's registry map is keyed by the session identifier (`s.slots[sessionID]`, pkg/adapter/slot.go:159, `slots map[string]*slotState` at pkg/adapter/server.go:367), and the hold opens AT the deregistration, so occupancy is already released when the hold begins. The only cost of a permanent hold is that that one session cannot re-bind on that pod. EVIDENCE: pkg/adapter/slot.go:159; pkg/adapter/server.go:367; spec-changes.md:~700 (`**Slot-identifier reclaim hold.**`, "from the deregistration of the slot's registry entry, which opens the hold in the same step").

USEFUL [Traps: "Do NOT re-derive the capacity family"] and USEFUL [refuted entry: "SPEC-3 withdraws the `leaked` disposition for every cleanup failure other than the runtime close"]: together these two, plus the FACT above, are what killed the one candidate this lens had. A future reliability shard that reaches the same candidate should stop at the capacity fact rather than re-deriving the drain-trigger argument.

WATCHOUT: the reclaim hold answers `ABORTED` (transient, §15.4) for a condition that on rows 3, 5 and 7 of the §5.2 disposition table never clears. That reads like a fail-transient-on-permanent defect, but it is already carried as an Open entry for a human ("Does a permanently held slot identifier have to be answered as permanent?", review-log.md:761) and the retry budget bounds the stampede (`maxSlotRetries` default 1). Do not file it as a new finding. EVIDENCE: proposals/0081_.../0081_....review-log.md:761; spec-changes.md Edge-cases bullet "A retry that meets the reclaim hold spends an attempt on it".

### [spec-recheck-2.1.review-security.1]

DECISION: Returned an EMPTY findings list for the security lens on spec-recheck round 1 (2.1) — BECAUSE the spec-changes delta since the `spec-recheck-r5` snapshot is a single word (`row` → `arm` at spec-changes.md:1301), so no staged spec block changed, and a full re-sweep of the security-relevant staged blocks (§5.2 scrub model + disposition table + reclaim hold, §4.7.1 rules 1-15, §4.7 `Shutdown`/`DemoteSDK` rows, §4.1, §4.6.1/§6.2 occupancy re-key, §15.4, §16.1) found no control regression and no bound sourced from an attacker-influenceable self-report — ALTERNATIVES: filing the `sessions_served` narrowing, the credential-residue-plus-cancelled-timer combination, and the §4.6.1 `reserved → idle` widening; each was derived, checked against the tree and spec, and dropped (see FACTs below).

FACT: `maxSessionsPerPod` is an explicitly security-motivated reuse bound ("the deployer must make an explicit choice based on the workload's sensitivity and the residual state vectors enumerated above") — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488. SPEC-3's biconditional narrows the `ReportSessionScrub` trigger (and hence the `sessions_served` increment) to "a `Shutdown` that reclaims a slot that reached `running`", so the §10.1 hold-timeout termination and the SDK demotion no longer advance it. NOT a finding: the hold-timeout case is whole-pod connection loss, where "the whole-pod replacement trigger ... also fires immediately on total connection loss regardless of the per-slot failure or leak count" (spec/10_gateway-internals.md:62) and the adapter exits (spec/10:58), so the pod is never reused; and the shipped handlers already file nothing on those paths, so the staging conforms spec to code rather than relaxing a live bound.

FACT: the two claims SPEC-3 makes about shipped adapter behaviour for the added cleanup acts both hold. `slotlayout.RemoveTree` iterates `{slotRoot, Sessions, Artifacts, CredentialsDir}` — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69. `deregisterSlotLocked` cancels every armed provider expiry timer before deleting the entry, with the rationale comment "an armed timer left behind fires AUTH_EXPIRED against a session that has already ended" — EVIDENCE: pkg/adapter/slotsession.go:174-181. The staged §5.2 action list's `[Section 4.9]` link is also correct: the direct-delivery MUST-arm-a-timer rule sits at spec/04_system-components.md:1169, inside `### 4.9 Credential Leasing Service` (heading at spec/04:1099).

WATCHOUT: the combination "cleanup cancels the §4.9 expiry timers AND a later act fails, so `credentials.json` survives with no timer to delete it" looks like a fail-open credential-residue finding, and on a `maxConcurrentSessions > 1` pod every co-tenant slot can read it (spec/13_security-model.md:30). It is NOT a finding here: the cancel-then-remove ordering is shipped (deregisterSlotLocked runs at deregistration, RemoveTree after), the proposal adds no new ordering obligation, the staged residue paragraph already says a failed act leaves what it would have removed, and cross-slot credential readability is covered by the `acknowledgeProcessLevelIsolation` gate (spec/05:517). Do not re-file it without evidence that the proposal CHANGES the ordering or the arming.

FACT: SPEC-4's §4.6.1 re-key makes the `claimed` arm STRICTER, not weaker — the deleted sentence was "the projection returns a pod from `claimed` to `idle` only on a recycling pool", and the replacement sends every claim deleted while the pod projects `claimed` to `draining`/`terminated` "on a pool of either recycle setting, because such a pod is unscrubbed". The `reserved → idle` bullet drops the `recycle.enabled: true` qualifier, but a `reserved` pod has never had a session bound, so no residual state is reused; and `ProjectOccupancyPhase` already behaves that way (pkg/controller/warmpool/occupancy.go).

FACT: the §7.1 acknowledged-clean predicate is deliberately fail-closed against a non-conforming adapter ("Quantifying over every outcome fails closed against an adapter that answers otherwise") and rule 13's entry-carries-no-token arm answers `superseded` explicitly "so that the rule fails closed" (spec-changes.md, §4.7.1 rule 13). Rule 10 makes the destructive `unconditional_teardown` the form a caller names rather than the default. There is no fail-open default anywhere in the teardown cascade.

UNVERIFIED: the §11.4 revoke fan-out is a spec-stated `Shutdown` caller (spec/11_policy-and-controls.md:270) with no implementation in the tree — the only gateway `Adapter.Shutdown` call sites are pkg/gateway/podlifecycle/podsession/slotbinder.go:542 and binder.go:2043. If that fan-out is ever built and omits `unconditional_teardown`, rule 10 turns a credential revocation into an `INVALID_ARGUMENT` that tears nothing down. Not a defect in this staging (CODE-4 covers every caller that exists), but whoever implements the revoke fan-out should be pointed at rule 10. Owner: the implementor of §11.4, or a later proposal.

### [spec-recheck-2.1.review-single-source.1]

DECISION: returned an EMPTY findings list for the single-source lens on the spec staging — BECAUSE the delta in `*.spec-changes.md` since snapshot `spec-recheck-r5` is one word ("row" -> "arm" in the SPEC-6 line of `## Spec files touched`, spec-changes.md:1301), and a full re-walk of the staged blocks found each rule with exactly one stating site — ALTERNATIVES: filing the §16.1 superseded-counter gloss (see WATCHOUT below), rejected as a clause-level summary that cites its home.

FACT: the whole spec-lane delta of this recheck round is one word. `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/spec-recheck-r5 proposals/0081_*/` shows spec-changes.md changing only at line 1301; every other hunk is in the checklist, the non-spec staging (CODE-3's new slothealth/slotstate comment re-keys, the DOCS-3 co-step sentence deletion, the reclaim-hold rationale rewrite, the Shutdown test bullet re-keyed onto `shutdownReclaimOutcome` arms) or the summary. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1301

WATCHOUT: the SPEC-6 §16.1 row for `lenny_slot_compensation_superseded_total` glosses `superseded` in wording close to rule 15's definition ("the adapter holds an entry for the session that the compensation is not addressed to, so the reclaim released nothing and the slot is not leaked"), while rule 15 reads "means the adapter holds an entry for the session that the request is not addressed to ... and the reclaim released nothing". I judged it below the bar: it is a metric-catalog gloss of what the counter counts, it carries "See §4.7.1", and it agrees with rule 15 and with §7.1's acknowledged-clean predicate today. A future lens re-deriving this should not file it without a DISAGREEMENT between the two texts. — EVIDENCE: spec-changes.md:1217 vs spec-changes.md:1062

FACT: the potential second home of "absent/superseded report a clean exit" is a `so`-clause inside SPEC-2's §7.1 acknowledged-clean definition ("so a reclaim answered `superseded` or `absent` is acknowledged clean"), whose normative home is rule 15. It reads as the illustrative consequence the wide-form decision (open decision 23) deliberately keeps, not a second statement, and the commentary right below the paragraph says so. Not filed. — EVIDENCE: spec-changes.md:319 (staged §7.1 block) and spec-changes.md:1062 (rule 15)

FACT: re-verified the two anchors the §12.6 replacement points at. `**Session count limit:**` exists as a §5.2 bullet and does own the evaluation point. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488

FACT: existing §16.1 catalog rows are short parenthetical glosses (e.g. `lenny_slot_failure_total`), so the two staged rows are longer than the section's convention but no rule in them lacks a citation. — EVIDENCE: spec/16_observability.md:14

### [non-spec-recheck-2.1.fix.1] — corrections appended to round 1's fix pass, not a new pass

- CORRECTS the bind-field sweep paragraph the round-1 fixer wrote into non-spec-changes.md.
  Its clause "no in-tree literal is a mid-session one today, because the §7.4 pair in
  `pkg/gateway/sessionserver/upload_to_session.go` has no test that constructs it" is false
  against the tree and makes the sweep instruction wrong for the one literal it misses.
  `TestFinalizeWorkspaceMidSessionOverlaysAndSignals_spec_7_4_433` builds
  `&adapterv1.FinalizeWorkspaceRequest{SessionId: ..., MidSession: true, WorkspacePlan: ...}`
  (pkg/adapter/files_updated_test.go:77-86), the test's own doc comment calls it the
  adapter-side end-to-end of a §7.4 mid-session upload (:48-52), and `mid_session` is already
  a shipped field (schemas/lenny-adapter.proto:713,:724). The paragraph now names that literal,
  states that rule 1 admits it unchanged with no token, and says the sweep adds a token to
  every other literal the grep names. Without the correction an implementor would stamp a
  `BindAttempt` onto a `MidSession: true` request, which rule 1's pairing predicate answers
  `INVALID_ARGUMENT`, turning a passing tier-1 test red for the reason the paragraph exists to
  prevent.
- FACT: the mid-session literal is the only one. `grep -rn "MidSession: *true" --include=*_test.go
  pkg/ tests/` returns exactly pkg/adapter/files_updated_test.go:80. — EVIDENCE:
  pkg/adapter/files_updated_test.go:80
- FACT: the implementation-checklist S14 wording already covered both arms correctly ("a
  non-empty `BindAttempt`, or `MidSession: true` and no token where the literal stands for a
  §7.4 mid-session call"), so only the non-spec-changes sentence was defective and the
  checklist is untouched.
- CORRECTS the CONF-1 reclaim-hold bullet's lead-in, which still scoped the hold to "every
  entry point that resolves a slot identifier", the reading round 1's G1 fix withdrew at its
  four sibling sites (the Design section, the `ensureSlotStateLocked` doc comment, and the two
  summary.md statements). Under the narrowed staging the hold reaches the seven requests
  §4.7.1's admission rules govern, and the resolvers that are not admission requests
  (`Checkpoint` through `checkpointRootsForSession` at pkg/adapter/slot.go:185 and
  pkg/adapter/checkpoint.go:94, the credential controls at pkg/adapter/credentials.go:121 and
  pkg/adapter/slotcreds.go:70,:105,:125,:252, the manifest reader at
  pkg/adapter/manifest.go:380, the tracing-context reader at pkg/adapter/tracingcontext.go:47)
  are outside it. The lead-in also contradicted the bullet's own enumeration two sentences
  later, which names exactly the seven. Corrected to "across the seven requests §4.7.1's
  admission rules govern"; the rest of the bullet stands verbatim.
- No spec-changes.md edit was needed for either correction. The staged spec text already
  carries the narrowed scope (spec-changes.md:635 and the deleted reclaim-hold clause at
  :1038), so both fixes are non-spec-lane only.

### [non-spec-recheck-2.1.fix-G1.1]
DECISION: closed the "rule 2's hold reaches every resolver" finding by REDUCTION in the spec lane rather than by widening the code — BECAUSE the wide predicate is unimplementable and unwanted: the hold opens in the same critical-section step that deletes the entry (spec-changes.md §4.7.1 registry critical section), so a non-admission resolver during a hold already misses, and refusing revoke/rotate/extend transiently would be a fail-open (review-log.md standing context, "The reclaim hold cannot refuse a mandatory credential control") — ALTERNATIVES: testing `s.reclaiming` inside `slotStateLocked` (rejected: it would refuse `Shutdown`, `releaseSessionSlot` and `terminateHeldSession`, which resolve through that function, and the branch is unreachable anyway); deleting §5.2's resolve sentence outright (rejected: the §7.4 mid-session upload resolves without creating and must take the transient refusal rather than rule 3's permanent `FAILED_PRECONDITION`); adding a CONF-1 row asserting `ABORTED` from `Checkpoint` (rejected: certifies the fail-open).
DECISION: edited the staged spec file, which this loop prefers not to touch — BECAUSE the finding's normative half lives there and the wording corrections alone would not close it. Two edits: the `, apart from the reclaim hold of rule 2` clause deleted from §4.7.1's `**Admission.**` preamble, and §5.2's three hold-scope sentences replaced by one that scopes the hold to the requests §4.7.1 enumerates as governed by its admission rules while keeping the create case, the resolve case and the §7.4 example.
FACT: the coverage phrase had FOUR copies, not the three the design listed. The design named non-spec-changes.md:26-29, non-spec-changes.md:1528 and summary.md:119; summary.md:48 carries a fourth in different words ("its callers cover every RPC that can reach an entry"), which is wider still and false for the same reason — EVIDENCE: proposals/0081_.../0081_....summary.md:48. All four now read "the seven requests §4.7.1's admission rules govern".
WATCHOUT: CODE-6's "one predicate and two test points" paragraph and CONF-1's rule-2 case are CORRECT as staged under the narrowed scope and were deliberately not edited. A later round that reads them as under-scoped is reading the withdrawn wide predicate — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:1730-1745, :2094-2100.
CORRECTS [standing context, "Do NOT put the reclaim hold back inside the admission cascade as rule 1"]: its parenthetical "(a `Checkpoint` resolve is refused)" no longer describes the staging. Rule 2 still cites §5.2 and §4.7.1 still restates none of the scope, so the entry's own guidance stands, but the scope is now the seven admission requests and a `Checkpoint` under a hold answers not-found on its own path.

### [non-spec-recheck-2.1.fix-G2.1]

DECISION: widened CODE-3 to carry every in-tree doc comment that states the withdrawn
cleanup-timeout ground for `leaked`, rather than splitting the gateway-side carriers onto
CODE-9/S10 or deferring them into SPEC-3's carrier table — BECAUSE one withdrawn trigger has
one retirement, and the deliverable boundary follows the rule rather than the file —
ALTERNATIVES: CODE-9/S10 for `gatewaymetrics_credential.go` (splits one rule across two
commits); SPEC-3 carrier-table rows (that table is scoped to the `sessions_served` re-keying
and would become a general deferral dump); rewriting the comments onto §5.2 pointers (those
pointers belong to the edge list that transcribes the §6.2 fence, and §6.2 still states what a
leaked slot holds, so the existing citation stays).

FACT: the carriers of the retired ground are found by `grep -rn "cleanup tim" pkg/ --include=*.go
| grep -v _test.go`. Beyond the two CODE-3 already opened they are
pkg/sandbox/slotstate/registry.go:93 (`MarkLeaked`),
pkg/gateway/sessionserver/sessionserver.go:417 (`slotLeakGauge`) and :1665 (`SlotLeakGauge`),
and pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:71 (`adapterLeakedSlots`)
and :219 (the gauge's construction comment). The gauge's `Help` string
("Concurrent-workspace leaked slots per pod awaiting pod termination (§6.2).") names no
trigger and needs no edit — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:224

WATCHOUT: the bind-field sweep's grep MUST carry the `adapterv1.` qualifier. The unqualified
pattern `FinalizeWorkspaceRequest{\|...\|ResumeRequest{` returns hits on
`tokensv1.AssignCredentialsRequest` and `podsession.ResumeRequest`, which are other services'
messages with no `bind_attempt` field; staging the unqualified form would tell the implementor
to add a field that does not exist — EVIDENCE: pkg/tokenservice/grpc_test.go:47,
pkg/gateway/podlifecycle/podsession/binder_test.go:492

FACT: `grep -rn "BindAttempt" --include=*_test.go pkg/ tests/` returns nothing today, so every
in-tree bind-sequence request literal is in the sweep. The qualified grep spans pkg/adapter and
tests at tiers 3, 4, 7a, 8, 9 and 10, which is why S14's tier list gained 4 and 8 — EVIDENCE:
tests/tier4_integration/credential_lifecycle_test.go:196,
tests/tier8_chaos/credential_rotation_ceiling_test.go:60

WATCHOUT: `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` is now in BOTH mandatory-field
sweeps. `TestShutdownDrainRacesAnIncomingSession_spec_6_4` holds `ShutdownRequest` literals at
:332, :373 and :453 and bare `FinalizeWorkspaceRequest` literals at :327 and :460, so the
summary's 0078 impact row can no longer describe it as a one-field edit. The same holds for
`tests/tier4_integration/concurrent_delegation_proxy_test.go`, whose `AssignCredentialsRequest`
at :194 sits beside the `ShutdownRequest` at :424. Both rows were corrected in this pass —
EVIDENCE: tests/tier7a_load_local/shutdown_drain_gate_race_test.go:327,:460;
tests/tier4_integration/concurrent_delegation_proxy_test.go:194

DECISION: dropped the design's literal counts ("46 literals over 21 files") from the staged text
— BECAUSE the round's standing prohibition on writing a count of sites or files outranks the
design's wording, and the grep already defines the set — ALTERNATIVES: staging the counts, which
go stale the first time a test is added and become a later finding.

FACT: no in-tree test constructs the §7.4 mid-session pair, so no literal in the sweep takes
`MidSession: true`. The pair exists only in production code at
pkg/gateway/sessionserver/upload_to_session.go, and no `*_test.go` under pkg/gateway/sessionserver
builds an `adapterv1` bind-sequence request.

### [non-spec-recheck-2.1.fix-G3.1]

DECISION: SCHEMA-1's `SESSION_SCRUB_OUTCOME_LEAKED` proto comment is now edited on exactly the terms its `RELEASED` sibling takes, naming the outcome and deferring the condition to §5.2, with the drain-ledger sentence and the `spec:` line untouched — BECAUSE the staged §5.2 disposition table keys `leaked` on the runtime close failing and reports `released` when the close succeeds and a later act fails, so "a resource could not be reclaimed at the session release" names a set the table splits across both outcomes, which is the mirror image of the ground SCHEMA-1 already gives for replacing the `RELEASED` clause — ALTERNATIVES: restating the new condition in the proto (rejected: a second home for a rule the table owns); deleting the comment body down to the `spec:` line (rejected: leaves the two siblings in different forms and drops the drain-ledger fact, which is stated nowhere else on this surface); also deleting the drain-ledger sentence (rejected: SPEC-3 does not falsify it; §4.6.3 still governs it); noting the discrepancy in commentary only (rejected: commentary is not a rule home and the published wire contract is what §15.4 hands third-party adapter authors).

WATCHOUT: the first draft of this fix added a rationale paragraph after the replacement pair restating why the old condition clause is false. That is exactly the commentary-restates-a-staged-rule class the round-18 prune deleted, and it was removed before the edit landed. The lead-in paragraph already carries the ground; do not re-add a per-comment rationale here — EVIDENCE: non-spec-changes.md, SCHEMA-1 `**The scrub-outcome and report-trigger comments.**` block.

FACT: the shipped `SessionScrubOutcome` enum header comment reads "the result of the §5.2 per-slot cleanup the adapter runs on every session release". That universal is about the CLEANUP, which still runs on every session release; SPEC-3 withdraws the universal on the REPORT only. It is deliberately unedited and is not a finding — EVIDENCE: schemas/lenny-adapter.proto:436-437.

FACT: SCHEMA-1's scope is restated in two places outside the deliverable, and both named only `released` until this fix. Any later change to SCHEMA-1's comment set must visit both — EVIDENCE: summary.md Deliverable index SCHEMA-1 bullet; spec-changes.md non-spec-deliverable summary sentence for `schemas/lenny-adapter.proto`.

FACT: the implementation checklist's S9 enumerates enum values, fields and the closed-field-set gate and never enumerates the comment replacements, so a change to the comment set needs no checklist edit. The "Files touched on application (non-spec)" entry for the proto is likewise generic ("the scrub-outcome and report-trigger comment replacements SCHEMA-1 states") — EVIDENCE: implementation-checklist.md S9; non-spec-changes.md "Files touched on application (non-spec)", the `schemas/lenny-adapter.proto` bullet.

DEFERRED [pkg/proto/adapter/v1/lenny-adapter.pb.go]: the regenerated copy of the LEAKED comment carries the falsified condition clause forward. No proposal edit is owed, because the checklist already has `make generate-proto` land the regenerated package in the same commit as the proto edit; recorded so a later reader does not file it as an unstaged site.


### [non-spec-recheck-2.1.fix-design-G1.1]

DECISION: the hold's scope is NARROWED to the §4.7.1-enumerated requests at its one home (§5.2's `**Slot-identifier reclaim hold.**` paragraph, spec-changes.md:635) and the trailing clause ", apart from the reclaim hold of rule 2" is deleted from the §4.7.1 `**Admission.**` preamble (spec-changes.md:1039) — BECAUSE the wide predicate is unimplementable and unwanted: the hold opens in the SAME critical-section step as the deregistration, so during a hold there is no entry to resolve, and every non-admission resolver (`Checkpoint`, `RotateCredentials`, `ExtendCredentialLease`, `RevokeCredentials`, manifest, tracing-context) already answers not-found; the only thing the wide clause buys is a different error code on requests that must not be refused transiently at all — ALTERNATIVES: (a) the finding's primary remedy, testing `s.reclaiming` inside `slotStateLocked`, rejected on three counts below; (b) deleting §5.2's "resolves an entry without creating one" sentence outright, rejected because it is load-bearing for the §7.4 mid-session upload, which resolves and cannot create and would otherwise take rule 3's permanent `FAILED_PRECONDITION` instead of the transient refusal; (c) adding a CONF-1 row driving `Checkpoint` under a hold, rejected with (a).

WATCHOUT: do NOT put the reclaim-hold test inside `slotStateLocked`. The staged `Shutdown` handler resolves through it twice, before and after taking the guard, so the test would refuse the reclaim its own pass opened and contradict the destructive-section paragraph; `releaseSessionSlot` and `terminateHeldSession` resolve the same way. — EVIDENCE: non-spec-changes.md:336,351; non-spec-changes.md:1702; pkg/adapter/slotsession.go:277,299

WATCHOUT: a hold test in `slotStateLocked` is also a FAIL-OPEN on the credential controls (revoke, rotate, extend) and is unreachable by construction, because `reclaiming[id]` is set in the same step that deletes `s.slots[id]`, so the lookup after it always misses anyway. Two separate reasons, either one sufficient. — EVIDENCE: review-log.md:113; spec-changes.md:1037 (registry critical section, "On every release ... the step is the deregistration of the entry and the opening of the hold")

FACT: the §4.7.1 `**Admission.**` partition is by RPC NAME, not by request instance — "Rules 1 through 9 govern every request ... that can create a slot registry entry: PrepareWorkspace, ..." followed by "Every OTHER RPC on this contract". A mid-session `PrepareWorkspace`/`FinalizeWorkspace` is therefore inside the enumeration (rule 3 exists precisely for it), so the §7.4 upload refusal hangs on rule 2 with no trailing clause. — EVIDENCE: spec-changes.md:1039, rule 3 at :1045

CORRECTS [review-log.md:1594]: that FACT says rule 2 reaches non-entry-creating RPCs "through the `**Admission.**` preamble's closing clause, so the §7.4 upload refusal has a rule to hang on". The clause is not what carries the §7.4 upload: a mid-session upload is one of the seven enumerated RPCs and is already governed. The clause carries only `Attach`, `Interrupt`, `Checkpoint`, `ReportUsage` and the credential controls, which the code neither refuses nor should refuse.

FACT: `slotStateLocked` (pkg/adapter/slot.go:130) is a bare map lookup and is a genuinely different function from `ensureSlotStateLocked` (:105). Eleven production call sites in the tree, plus the two the staged `Shutdown` adds; `ensureSlotStateLocked` has three. — EVIDENCE: pkg/adapter/{slot.go:185,credentials.go:121,slotcreds.go:70,105,125,252,slotsession.go:277,299,manifest.go:380,tracingcontext.go:47}

DEFERRED [spec-changes.md]: if this non-spec loop may not write the spec file, the two spec edits above are the half that actually closes the finding and must be carried into the next spec pass. What is false today: §4.7.1:1039's clause ", apart from the reclaim hold of rule 2" and §5.2:635's unqualified "admits no request that would create or resolve a registry entry" / "A request that resolves an entry without creating one is refused on the same terms". What is true instead: the hold refuses exactly the requests §4.7.1 enumerates as governed by its admission rules, whether they would create an entry or resolve one, the §7.4 mid-session upload among them. The non-spec wording corrections (below) are true-making on their own and may land alone; the finding is not closed until the spec half lands.

DEFERRED [summary.md]: summary.md:119 carries the same false scope phrase as Design :27-29 and the `ensureSlotStateLocked` doc comment :1528 — "Its production callers cover every RPC that can create or resolve an entry". True instead: they cover the seven requests §4.7.1's admission rules govern. Correct it in the same edit if the lane allows; otherwise it is the last stale copy of this phrase.

UNVERIFIED: §5.2:635 says the hold "refuses a §7.4 mid-session upload STILL IN FLIGHT when the cleanup opens the hold", while CODE-6 states the opposite for an upload already past its resolve (non-spec-changes.md:1687-1690: a `FinalizeWorkspace` inside `MaterializeWithPolicy` "is not being admitted and re-enters no resolve"). Pre-existing and independent of this fix; a later round should decide whether "still in flight" means "not yet admitted". Nobody has checked it.

### [non-spec-recheck-2.1.fix-design-G2.1]

DECISION: the retired `leaked` trigger sweep stays ONE deliverable (CODE-3), extended to all five
remaining carriers (`pkg/sandbox/slotstate/registry.go:93`, `pkg/gateway/sessionserver/sessionserver.go:419`
and `:1667`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:72` and `:220`) with the
reduction CODE-3 already applies to `slothealth.go`: delete the retired ground clause, keep the consequence
clause, keep the existing `§6.2` citation, add no `§5.2` pointer to a Go doc comment — BECAUSE one withdrawn
trigger has one retirement, and the `§5.2` pointers CODE-3 writes belong to the edge list that transcribes the
§6.2 fence, not to prose comments — ALTERNATIVES: routing the two gateway files through CODE-9/S10 because
CODE-9 already opens `gatewaymetrics_credential.go` (rejected: splits one retirement across two deliverables);
deferring the gateway half into SPEC-3's carrier table (rejected: that table is scoped to the `sessions_served`
re-keying, and the remedy adds text where the finding's own remedy is a deletion).

DECISION: the bind-field sweep is recorded by EXTENDING the existing scope-accounting paragraph
(non-spec-changes.md:3027-3036), not by a second paragraph — BECAUSE the two mandatory-field breaks are one
class with one silent failure mode, and a second paragraph is the start of two drifting enumerations.

FACT: the finding's grep for the five bind-sequence request literals is OVER-BROAD. `grep -rn
"FinalizeWorkspaceRequest{\|..." --include=*_test.go pkg/ tests/` returns 66 hits over 25 files, but 20 of them
are `tokensv1.AssignCredentialsRequest` (`pkg/tokenservice/grpc_test.go`,
`tests/tier2_component/controllers/tokenservice_test.go`,
`tests/tier10_conformance/token_service_unavailability_guard_conformance_test.go`) or `podsession.ResumeRequest`
(`pkg/gateway/podlifecycle/podsession/binder_test.go:492`, `resume_slot_reservation_test.go:52`), which are
different messages on different services. The correct set is the `adapterv1.`-qualified one: 46 literals over 21
files. Pattern to stage:
`grep -rn "adapterv1\.\(FinalizeWorkspaceRequest\|RunSetupRequest\|AssignCredentialsRequest\|ResumeRequest\|PrepareWorkspaceRequest\){" --include=*_test.go pkg/ tests/`
EVIDENCE: pkg/tokenservice/grpc_test.go:47; pkg/gateway/podlifecycle/podsession/binder_test.go:492.

FACT: the shipped `ShutdownRequest{` grep the proposal already stages has no such hazard — all 64 hits are
`adapterv1.ShutdownRequest{`. EVIDENCE: `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/ | grep -o
"[a-z0-9]*\.ShutdownRequest{" | sort | uniq -c`.

FACT: no in-tree test literal is a mid-session case. `grep -rn "BindAttempt" --include=*_test.go pkg/ tests/`
returns zero, and `pkg/gateway/sessionserver/upload_to_session_test.go` (the only §7.4 test) constructs no
`adapterv1` request literal. So every swept literal takes a non-empty `BindAttempt` and none takes
`MidSession: true`.

FACT: the 21 swept files span tiers 1, 3, 4, 7a, 8, 9 and 10. S14's tier list ("0, 1, 2, 3, 7a, 9, 10") is short
by 4 and 8. EVIDENCE: tests/tier4_integration/credential_lifecycle_test.go:196;
tests/tier8_chaos/credential_rotation_ceiling_test.go:60.

WATCHOUT: `tests/tier7a_load_local/shutdown_drain_gate_race_test.go` is hit by BOTH sweeps. Its two named tests
carry four `adapterv1.ShutdownRequest{}` literals AND two bare `adapterv1.FinalizeWorkspaceRequest{}` literals
(`:327`, `:460`), so the summary's 0078 impact row ("take that one-field edit at their four `ShutdownRequest`
literals ... and must keep passing", summary.md:1012) is falsified by the bind-field sweep and must gain the
second edit in the same commit. EVIDENCE: tests/tier7a_load_local/shutdown_drain_gate_race_test.go:327,:460;
summary.md:1012.

WATCHOUT: the only `docs/` carrier of the retired trigger is `docs/reference/state-machines.md:251`, which DOCS-1
already owns. Do not add a docs edit to CODE-3, and do not raise S8 above tiers 0 and 1: the five new carriers
are doc comments no gate reads. EVIDENCE: `grep -rn "cleanup tim" docs/ tests/`.

### [non-spec-recheck-2.1.fix-design-G3.1]
DECISION: SCHEMA-1 edits the `SESSION_SCRUB_OUTCOME_LEAKED` comment on the same terms as its `RELEASED` sibling — the condition clause "a resource could not be reclaimed at the session release" becomes "the cleanup reported leaked, on the terms §5.2 states", with the drain-ledger sentence and the `spec:` line kept verbatim — BECAUSE the staged §5.2 disposition table makes `leaked` the report of the runtime close failing alone (spec-changes.md:624-625), so a tree-removal failure is a resource not reclaimed that reports `released`; the comment's condition clause is falsified in the mirror image of the `RELEASED` one SCHEMA-1 already replaces — ALTERNATIVES: restating the new condition ("the runtime close failed") in the proto (rejected: a second full statement of a rule whose one home is the §5.2 table, the pattern the RELEASED replacement exists to remove); deleting the enum comment body down to the `spec:` line (rejected: the RELEASED sibling's settled wording names the outcome, and a bare citation would diverge from it); deleting the drain-ledger sentence in the same edit (rejected: SPEC-3 does not falsify it, so it is an unreviewed extra edit).
WATCHOUT: the `SessionScrubOutcome` enum header comment says the cleanup "the adapter runs on every session release" (schemas/lenny-adapter.proto:436-437) and looks like the withdrawn universal, but SPEC-3 withdraws the universal on the REPORT, not on the cleanup; the cleanup still runs on every session release. SCHEMA-1 lists it as deliberately unedited (non-spec-changes.md:2331-2333). Do not edit it. EVIDENCE: proposals/0081_*/0081_*.non-spec-changes.md:2331-2333
FACT: SCHEMA-1's scope is restated in exactly two places outside its own block, both naming only the `RELEASED` implication: summary.md:1028 (Deliverable index bullet) and spec-changes.md:1315-1318 (non-spec-deliverable summary). Both undercount once the LEAKED comment is edited. EVIDENCE: proposals/0081_*/0081_*.summary.md:1028; proposals/0081_*/0081_*.spec-changes.md:1317
WATCHOUT: the block's lead-in at non-spec-changes.md:2262-2265 counts the edited comments as "both comments" / "Both comments"; a third edited comment makes that wrong. Rewrite it count-free ("these comments"), never as "all three": doc-style.md bars stated counts. EVIDENCE: proposals/0081_*/0081_*.non-spec-changes.md:2262-2265
OPEN: whether the LEAKED comment's drain-ledger sentence ("The gateway feeds the outcome into the unhealthy-threshold ledger behind the lenny.dev/drain-request annotation") is itself a restatement of a rule whose home is §4.6.3/§5.2 and should be reduced to a citation. Not falsified by SPEC-3, so it was left standing here; a later round can file it as its own finding.

### [non-spec-recheck-2.1.review-applicability.1]

FACT: this round had NO delta. `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 proposals/0081_*/` returns empty, so the r2 snapshot is byte-identical to the current staging and the "read the delta first" instruction had nothing to point at. The last real delta is the r1-prefix diff (CODE-3 widened to `slothealth.go`, the "row"→"arm" sweep, two deleted step-placement sentences, the CODE-5 hold-bound rewrite). EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs the proposal directory.

FACT (the finding this round filed): the staged `validateBindFields` makes `bind_attempt` MANDATORY on every non-mid-session `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials` and `Resume` (non-spec-changes.md:1497-1500, :1505-1507), and the proposal's only in-tree literal sweep is the `ShutdownRequest` one (non-spec-changes.md:3027-3036). `grep -rn "BindAttempt" --include=*_test.go pkg/ tests/` returns ZERO hits today, while the five bind-sequence request types appear in 66 test literals across 25 files (pkg/adapter/staging_test.go:49, credentials_test.go:46, slot_test.go:96, resume_test.go, files_updated_test.go, credredact_test.go, rotationgate_test.go, tracing_*_test.go, tests/tier4_integration/concurrent_workspace_test.go:171, credential_lifecycle_test.go:196, tests/tier8_chaos/credential_rotation_ceiling_test.go:60, tests/tier9_security/credential_rotation_cotenant_inflight_gate_test.go:47, …). None of those files is in the Files-touched list except for the unrelated `ReleaseSlotForTest` ctx edit. Same silent failure mode the ShutdownRequest paragraph names: proto3 scalars, compiles clean, tiers go red with no build error.

WATCHOUT: the `ShutdownRequest` sweep paragraph reads as if it were the whole wire-break accounting for this proposal. It is not. There are TWO mandatory-field breaks: rule 10 on `Shutdown` (swept) and rule 1 on the five bind-sequence RPCs (not swept). Do not read the presence of the first as coverage of the second. EVIDENCE: non-spec-changes.md:3027-3036 vs :1497-1507.

FACT: CODE-3's four re-keyed anchors all resolve verbatim in the tree — pkg/sandbox/slotstate/slotstate.go:34,:38,:41,:44 (constant docs) and :99-104 (edge list), pkg/gateway/runtime/slothealth/slothealth.go:33 and :110. The CODE-3 pointer text `(see §6.2 "Pre-running slot cleanup")` matches SPEC-4's staged fence pointer `(see the pre-`running` slot cleanup paragraph below)` in target, not in wording; the difference is deliberate (a Go comment cannot say "below").

FACT: SCHEMA-1's claim "this is the only closed field set over the messages SCHEMA-1 opens" checks out. `assertFieldSet` exists only in tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:259; checkpoint_stream_wire_test.go:151's `Fields().Len() != 6` is over `CheckpointStart`, which SCHEMA-1 does not open; tests/tier3_contract/adapter_session_address/session_address_wire_test.go holds open sets (retired-field and address presence), not closed ones.

FACT: neither tier-0 claim-register gate checks that a row's `surface` paths exist. tests/tier0_static/claim_register_test.go validates status/anchor/deferral/surface-is-not-a-bare-line only; claim_register_proto_agreement_test.go only fires when `surface` contains `schemas/lenny-adapter.proto` AND the claim text matches `^Message.field`, which none of SCHEMA-1's three rows does. So landing the ABSENT row at S9 while its named tier-3/tier-10 surfaces do not exist until S22 turns nothing red.

UNVERIFIED: SCHEMA-1's body says all THREE claim rows land in the S9 commit ("Three rows, added to the `EXPLICIT` list … in the same commit"), CONF-1 says the `ABSENT` row "lands with it" (the tier-10 file, S22), and checklist S9 names "the two `WIRED` rows". I did not file it: the gates above tolerate either placement and the refuted list already carries two near-identical step-placement bookkeeping refutations (DOCS-4/"lands beside DOCS-2", S9/"omits the four proto comment replacements"). A round that wants the placement exact should settle it in ONE home.

USEFUL [non-spec-recheck.1.review-applicability.1]: its checklist sweep (every deliverable in a step, every Depends-on earlier and existing, no ticked box, one lane per step, spec steps leading) still holds after the r1 fixes; I re-ran it and changed nothing, so a later round can spend its attention elsewhere.

### [non-spec-recheck-2.1.review-citations.1]
DECISION: returned an EMPTY findings list for the citation lens on this recheck — BECAUSE every concrete citation I could resolve verified, including the whole delta — ALTERNATIVES: filing the `SessionScrubOutcome` enum-comment question and CODE-3's "faithful transcription" wording, both rejected below as non-defects.
FACT: the r2 snapshot is IDENTICAL to the current proposal. `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 proposals/0081_.../` returns NOTHING. The real delta for this round is against `non-spec-recheck-r1-prefix`: checklist S8, CODE-3's expansion onto slotstate.go constant docs + slothealth.go, CODE-5's reclaim-hold rationale rewrite, the DOCS-2 `:393` grounding deletion, the DOCS-3 co-step sentence deletion, the SCHEMA-1 "text that reads, verbatim" widening, the tier-1 Shutdown bullet re-key onto `shutdownReclaimOutcome` arms, the Edge-cases self-recreation bullet reduced to a pointer, the row→arm sweep, and two summary bullets. — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry
FACT: a mechanical citation sweep is cheap and worth redoing. Extracting every `path:line` from non-spec-changes.md + summary.md yields 261 distinct citations; 213 resolve to a unique tree file and every one of those points at what the proposal says it does. The ~20 unresolved are bare basenames (`slot.go:105`, `sdkwarm.go:261`, `server.go:111-114`) that are unambiguous in their deliverable's header context. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:3848
FACT: CODE-3's four verbatim constant-doc quotes are exact against the tree — `Running` at pkg/sandbox/slotstate/slotstate.go:34-36, `SlotCleanup` :38-40, `Released` :41-43, `Leaked` :44-47 — and slothealth.go states the retired cleanup-timeout trigger in exactly the two places CODE-3 names (`event` doc :33, `RecordLeak` doc :110-112); `grep -n timeout pkg/gateway/runtime/slothealth/` returns those two lines and nothing else, so the deliverable's "twice" is right. — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:33
FACT: SCHEMA-1's four proto quotes are verbatim (schemas/lenny-adapter.proto:308-315, :440-443, :451-452) and every "Basis" cell of the field-number table matches the declared/reserved numbers in each message. — EVIDENCE: schemas/lenny-adapter.proto:311
WATCHOUT: the `SessionScrubOutcome` ENUM comment (schemas/lenny-adapter.proto:435-436) reads "the result of the §5.2 per-slot cleanup the adapter runs on every session release" — near-identical wording to the RPC opening sentence SCHEMA-1 DOES edit, and SCHEMA-1 explicitly leaves it, saying the unedited comments state "no trigger for the report". This looks like an unstaged carrier but is not: the withdrawn universal is about the REPORT, and the enum comment's "on every session release" attaches to the CLEANUP, which still runs on every release. Do not file it. — EVIDENCE: schemas/lenny-adapter.proto:435
WATCHOUT: CODE-3 says the three untouched slotstate.go glosses "stay verbatim, so the comment remains a faithful transcription of the fence". They are paraphrases, not transcriptions: the code says "workspace materialization begins" / "task completes or fails" / "non-retryable error" where spec/06_warm-pod-model.md:151,:154,:147 say "...for this slot" / "session completes or fails" / "non-retryable error: OOM, workspace validation, policy rejection". Nothing becomes wrong when SPEC-4 lands, so it is wording, not a finding. — EVIDENCE: spec/06_warm-pod-model.md:151
FACT: slotstate.go's `OccupiesSlot` quotes §6.2 as "remains counted in active_slots" while spec/06_warm-pod-model.md:160 says "remains counted in the pod's Redis slot-counter occupancy". Pre-existing drift; SPEC-4 leaves the `leaked` semantics paragraph untouched, so this proposal neither creates nor is obliged to close it. — EVIDENCE: spec/06_warm-pod-model.md:160
FACT: the two tier-11 gates DOCS-2 reasons about behave as it says. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only the four substrings, all of which the staged row keeps (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307), and `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts the literal "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." in BOTH the staged §4.7 row (spec-changes.md:762) and the staged docs row. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67-74
FACT: the corrected summary `stageWorkspace` bullet is right. `rewriteExtractedSources` errors (binder.go:1290-1293) and the blob-store errors (:1303-1318) all return before the `PrepareWorkspace` send at :1327, and that send is guarded by `len(uploads) > 0`. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1327
FACT: every reader-facing anchor DOCS-1/2/3 cites is on the line it names: state-machines.md:138 (pod-state paragraph), :235, :237, :251; adapter-contract.md:10, :53, :64, :75, :81; error-catalog.md:129 carries all four quoted `SETUP_COMMAND_FAILED` sentences and the remedy cell verbatim. — EVIDENCE: docs/reference/error-catalog.md:129

### [non-spec-recheck-2.1.review-client-surface.1]
DECISION: returned an empty findings list for the client-facing-surface lens — BECAUSE every client-facing parallel I could name is either untouched or staged with its mirror, and each mirror's quoted source text verified verbatim against the tree — ALTERNATIVES: filing the `Sandbox.status.phase` CRD godoc gap and the `SessionScrubOutcome` enum-comment gap, both rejected below.
FACT: the nine field numbers SCHEMA-1 claims are all free, re-verified by reading the message blocks: `PrepareWorkspaceRequest` 1-3 + `reserved 4`; `FinalizeWorkspaceRequest` 1-4 (`mid_session` is 4) + `reserved 5`; `RunSetupRequest` 1-3 + `reserved 4`; `AssignCredentialsRequest` 1-2 + `reserved 3`; `ResumeRequest` 1-5, 7-14, `reserved 6` and `reserved 15`; `ShutdownRequest` 1-3, `reserved 4`, 5 `recycle`, 6 `coordination_generation`; `ShutdownResponse` 1-2 — EVIDENCE: schemas/lenny-adapter.proto:464,694,730,855,1411,1210 and the message bodies around each.
FACT: the adapter `ErrorCode` enum has NO mirror in any language SDK. `sdks/runtime/{go,python,typescript}` and `sdks/client/{go,python,typescript}` contain zero references to `lenny-adapter`, `adapter/v1`, `ShutdownRequest` or `SlotReclaim`; `schemas/buf.gen.yaml` emits only into `../pkg/proto`. So SCHEMA-1's two new codes and the new enum need no SDK edit — EVIDENCE: schemas/buf.gen.yaml:16-25; `grep -rln "lenny-adapter\|adapter/v1\|SlotReclaim\|ShutdownRequest" sdks/` returns nothing.
FACT: `pkg/gateway/externalapi/openapi/openapi.json` does not name `SETUP_COMMAND_FAILED` at all (0 hits), so SPEC-5/DOCS-3's §15.1 row edits have no OpenAPI, MCP-tool-schema or client-SDK parallel to mirror — EVIDENCE: `grep -c SETUP_COMMAND pkg/gateway/externalapi/openapi/openapi.json` = 0.
FACT: DOCS-3's and SPEC-5's client-visible envelope claim is true in code. Any `RunSetup` error is wrapped as `SetupCommandFailure` (pkg/gateway/podlifecycle/podsession/slotbinder.go:299-304, binder.go:933-942), and `writeSetupCommandError` splits on `status.Code(setupFail.Cause) == codes.FailedPrecondition` → 422 `SETUP_COMMAND_FAILED`, everything else (including `Aborted`) → 503 retryable fallback (pkg/gateway/sessionserver/start.go:237-249). So `SLOT_BIND_ALREADY_STARTED` (FailedPrecondition) lands on 422 and `SLOT_BIND_ATTEMPT_SUPERSEDED` (Aborted) on the retryable fallback, exactly as staged.
FACT: all five DOCS-3 quoted sentences and both DOCS-4 sentences are verbatim in the tree — EVIDENCE: docs/reference/error-catalog.md:129; docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33. So are DOCS-1's four quotes (docs/reference/state-machines.md:138,235,237,251) and DOCS-2's three row quotes (docs/reference/adapter-contract.md:64,75,81).
FACT: every tier-11 substring the staging relies on survives its staged rewrite. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" (basic_level_echo_stamp_doc_reconciliation_test.go:302-307) — all in the staged `Shutdown` row; the five substrings the staging ADDS ("no other bound session", "a session whose start the adapter has admitted", "either the bind attempt", "reclaimed", "absent", plus page-level "**Bind attempt token.**") are all present too. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the exact opener `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` on BOTH carriers (session_scrub_report_addressing_doc_reconciliation_test.go:69-77); staged spec-changes.md:762 and the staged DOCS-2 row both keep it word for word. `TestPerSlotCleanupStatedOnEverySessionModeRow` reads only the residual-state tables of execution-modes.md and multi-tenancy.md (basic_level_echo_stamp_doc_reconciliation_test.go:439-451,466-474), never the prose sentence DOCS-4 deletes, so DOCS-4's "no gate reads either sentence" holds.
FACT: `scripts/check-markdown-links.sh` shells out to `markdown-link-check` and skips silently when the tool is absent; it does not validate GitHub blob-URL fragments. So DOCS-2's two `https://github.com/.../spec/...#471-role-and-gateway-rpc-contract` anchors are ungated. Both anchors are nonetheless correct GitHub slugs for `#### 4.7.1 Role and Gateway RPC Contract` (spec/04_system-components.md:659) and `### 15.4 Runtime Adapter Specification` (spec/15_external-api-surface.md:1458). Note the page's one shipped precedent at docs/reference/adapter-contract.md:393 uses an unnumbered fragment (`#translation-fidelity-matrix`), so the two forms differ; that is style, not a broken link.
DECISION: did NOT file the `Sandbox.status.phase` doc-comment gap — BECAUSE SPEC-4 adds "the phase the pod currently projects" to §4.6.1's projection-input enumeration (spec-changes.md:903) and pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118 (copied into charts/lenny/crds/lenny.dev_sandboxes.yaml:407 and pkg/embedded/crds/lenny.dev_sandboxes.yaml:407) carries the same enumeration without it, BUT that comment already omits "disposition" and is a loose paraphrase that was incomplete before this proposal, so its absence makes neither the applied spec nor the implementation wrong — ALTERNATIVES: filing it under (d); an earlier round already recorded it as DEFERRED with the verdict "record only" (review-log.md:864-868), and the authoring source is the Go doc comment plus `make generate`, never the two generated YAMLs.
DECISION: did NOT file the `SessionScrubOutcome` enum comment (schemas/lenny-adapter.proto:436-437, "the result of the §5.2 per-slot cleanup the adapter runs on every session release") as a missed SCHEMA-1 edit site — BECAUSE the universal SPEC-3 withdraws is the REPORT universal, and the staged §5.2 scrub-model replacement (spec-changes.md:569) explicitly PRESERVES "a per-slot cleanup runs on every session release". The enum comment states the cleanup's trigger, not the report's, so it stays true; SCHEMA-1 says so in terms (non-spec-changes.md, "none states a trigger for the report").
WATCHOUT: the next agent tempted to file "SCHEMA-1 leaves a fourth `on every session release` in the proto" should stop at the sentence above. The cleanup universal survives; only the report universal is withdrawn.
FACT: `adapterclient` has exactly the callers CODE-4 names. `grep` for `.Shutdown(ctx|.ShutdownRecycle(|.PrepareWorkspace(|.FinalizeWorkspace(|.RunSetup(|.AssignCredentials(|.Resume(ctx` over non-test `pkg/` and `cmd/` returns only pkg/gateway/podlifecycle/podsession/{binder.go,slotbinder.go} and pkg/gateway/sessionserver/upload_to_session.go:128,134, all of which are in CODE-4's target list. `pkg/embedded/localcli/session.go:396`'s `FinalizeWorkspace` is the REST client SDK, a different type, and is unaffected.

### [non-spec-recheck-2.1.review-docs-alignment.1]
DECISION: Returned an empty findings list for the docs-alignment lens — BECAUSE every docs/ surface that mirrors a behaviour this proposal changes is in a staged edit list, and each staged docs edit checks out verbatim against the tree and against the post-change spec — ALTERNATIVES: I considered filing on docs/operator-guide/troubleshooting.md:41 (`reason: setup_command_failed`) as a new cause of an existing failure narrative, and on docs/reference/adapter-contract.md:84 ("Scrub responsibilities") as an unlisted carrier of the withdrawn report universal; both refuted below.
FACT: docs/operator-guide/troubleshooting.md:41's `reason: setup_command_failed` row is NOT the REST envelope's `details.reason`. Its section is warm-pool exhaustion and the surrounding block reads `rate(lenny_warmpool_warmup_failure_total[5m]) by (reason)`, so the label is a warmup-failure reason. The started-session refusal SPEC-5/DOCS-3 add to `SETUP_COMMAND_FAILED` never produces a warmup failure, so that row needs no new cause. — EVIDENCE: docs/operator-guide/troubleshooting.md:33-41
FACT: `SETUP_COMMAND_FAILED` appears in exactly one docs page, docs/reference/error-catalog.md:129, which DOCS-3 stages. docs/client-guide/error-handling.md carries a partial code catalog that does not include it, so DOCS-3's single-page scope is complete. — EVIDENCE: docs/reference/error-catalog.md:129; docs/client-guide/error-handling.md:60-140
FACT: the only docs mirror of §4.6.1's occupancy projection is docs/reference/state-machines.md:138; nothing else in docs/ states "level-triggered projection" or the claim-deletion clauses, so DOCS-1's pod-state-machine paragraph is the whole of that mirroring. — EVIDENCE: docs/reference/state-machines.md:138
FACT: DOCS-1's three cited line numbers all resolve: :235 `receiving_uploads`→`running`, :237 `slot_cleanup`→`released`, :251 the `slot_cleanup -> leaked` clause. — EVIDENCE: docs/reference/state-machines.md:235,237,251
FACT: DOCS-4's "no gate reads either sentence" holds. The three tier-11 tests that open docs/operator-guide/security-principles.md or docs/reference/execution-modes.md read other anchors: `TestPerSlotCleanupStatedOnEverySessionModeRow` scopes itself to the residual-state table via `residualStateTable`, `TestVMRestartDocs*` read the `scrubProfile` rows, `TestCredentialLeaseDocs*` read the credential-lease statements. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:438-480,505-520; tests/tier11_docs/vm_restart_reprovision_doc_reconciliation_test.go:53-57,112-125; tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go:105-140
FACT: the substrings the Testing section adds to `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` ("no other bound session", "a session whose start the adapter has admitted", "either the bind attempt", "reclaimed", "absent", "**Bind attempt token.**") are each present verbatim in DOCS-2's staged row/paragraph, and the four shipped substrings survive it. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310; non-spec-changes.md DOCS-2 staged `Shutdown` row
FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the exact opener "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." on BOTH the staged §4.7 row and the staged DOCS-2 row; both carry it word for word. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-77; spec-changes.md SPEC-3 §4.7 block; non-spec-changes.md DOCS-2 `ReportSessionScrub` row
WATCHOUT: docs/api/internal.md documents an OLDER adapter service (StartSession, StopSession, Attach, Checkpoint, UploadFiles, DemoteSDK) with no `Shutdown`, no `PrepareWorkspace` and no `ErrorCode` enum. It is NOT a mirror of schemas/lenny-adapter.proto, so SCHEMA-1's nine fields and two enum values create no edit site there. Its `StopSession` naming is pre-existing drift unrelated to 0081. — EVIDENCE: docs/api/internal.md:79-99,265-290,497
WATCHOUT: docs/reference/adapter-contract.md:84 ("the adapter runs the credential purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") looks like an unlisted carrier of the report universal SPEC-3 withdraws, but it is scoped to the ordinary session end on a recycling pod, which is exactly the case that still files a report. Not a carrier. — EVIDENCE: docs/reference/adapter-contract.md:84
UNVERIFIED: DOCS-2's `DemoteSDK` row states the registry drop unconditionally while staged §4.7 says "the entry for the session the pod holds if it holds one". I judged the omitted conditional below the bar on a one-line orientation row; a later reviewer who disagrees should weigh it as docs precision rather than contradiction. — EVIDENCE: non-spec-changes.md DOCS-2 `DemoteSDK` row vs spec-changes.md SPEC-1 §4.7 `DemoteSDK` block

### [non-spec-recheck-2.1.review-edit-sites.1]
FACT: the withdrawn `leaked` trigger ("cleanup timeout exceeded" / "whose cleanup timed out") has SEVEN Go carriers in the tree, not the three CODE-3 now names. The complete grep is `grep -rn "cleanup timeout exceeded\|cleanup timed out" pkg/`. — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:44, :104 (staged); pkg/gateway/runtime/slothealth/slothealth.go:33, :110-111 (staged); pkg/sandbox/slotstate/registry.go:93 (UNSTAGED); pkg/gateway/sessionserver/sessionserver.go:419, :1667 (UNSTAGED); pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:72, :220 (UNSTAGED)
WATCHOUT: the round-N fix for "CODE-3 leaves the retired `leaked` trigger standing" swept only the two files the finding named. `registry.go` is in the SAME package as `slotstate.go` and CODE-3's own closing sentence enumerates what stays in `slotstate.go` and in `slothealth.go` without ever opening the sibling file. — EVIDENCE: non-spec-changes.md:832-834
FACT: the tier-11 fence gate `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:33-36` matches the §6.2 fence entries by EDGE PREFIX only (`"receiving_uploads ──→ running"`), so SPEC-4's trigger-cell replacements to `(see §5.2)` do not break it, and the added `receiving_uploads ──→ slot_cleanup` edge is not in `generalSlotEdges` so the scoped-block exclusion at :69-73 does not fire either. I verified this so a later round need not. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-74
FACT: `tests/tier11_docs/adapter_metric_catalog_test.go:92-116` admits a new `pkg/adapter/metrics.go` metric only when it reaches BOTH `docs/reference/metrics.md` and the §16.1 catalog, or reaches the reference plus a `specCatalogPending` entry. SPEC-6 stages the §16.1 row for `lenny_slot_shutdown_untokened_entry_total` and CODE-9 stages the reference row, so CODE-9's claim that the shipped gate is "not edited" holds. Checked; not a finding. — EVIDENCE: spec-changes.md:1218, non-spec-changes.md:2053-2055
FACT: the two new `ErrorCode` values have exactly one carrier, `schemas/lenny-adapter.proto:555-585`. No spec or docs page enumerates the adapter `ErrorCode` enum, so DOCS-3's "the published error catalog takes no new row" is correct. Checked; not a finding. — EVIDENCE: grep of ERROR_CODE_ over docs/ schemas/ returns only the proto
FACT: the summary's corrected `stageWorkspace` sentence is accurate: `rewriteExtractedSources` and the blob-store fetch both return before the `PrepareWorkspace` call at pkg/gateway/podlifecycle/podsession/binder.go:1325. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1290-1330, summary.md:267-270

### [non-spec-recheck-2.1.review-fresh.1]
DECISION: Returned an empty findings list for the fresh-holistic lens on this recheck — BECAUSE every item in the delta verified against the tree: CODE-3's four slotstate.go constant-doc quotes and two slothealth.go quotes are verbatim; SPEC-4 retires exactly the three fence glosses CODE-3 re-keys; the `stageWorkspace` summary correction is true of the tree; the Testing bullet's five `shutdownReclaimOutcome` arms match the staged switch one-to-one; the reduced Edge-cases bullet's pointer resolves to a real spec-changes bullet — ALTERNATIVES: filed nothing on the unswept in-code §6.2 trigger carriers (see UNVERIFIED below), because that is a close variant of an already-refuted finding class.
FACT: every field number SCHEMA-1 mints is free in the shipped proto. `PrepareWorkspaceRequest` uses 1-3 + `reserved 4`; `FinalizeWorkspaceRequest` 1-4 + `reserved 5`; `RunSetupRequest` 1-3 + `reserved 4`; `AssignCredentialsRequest` 1-2 + `reserved 3`; `ResumeRequest` up to 14 with 6 and 15 reserved; `ShutdownRequest` 1-3 with 4,5,6 taken/reserved; `ShutdownResponse` 1-2. — EVIDENCE: schemas/lenny-adapter.proto, messages at the `reserved "slot_id"` markers.
FACT: the summary's claim-register arithmetic is exact. `tests/claim-map.json` holds 76 claims: 32 WIRED, 24 UNWIRED, 20 ABSENT. — EVIDENCE: tests/claim-map.json (`python3 -c "import json,collections; d=json.load(open('tests/claim-map.json')); print(len(d['claims']), collections.Counter(x['status'] for x in d['claims']))"`)
FACT: nothing under tests/ pins any of the slotstate.go or slothealth.go comment strings CODE-3 rewrites; `grep -rn "workspace ready\|slot reclaimed\|cleanup timeout exceeded\|torn down cleanly\|processes killed" tests/ --include=*.go` returns nothing, so the re-key breaks no gate. — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:99-104
FACT: SCHEMA-1's first `ReportSessionScrub` replacement block (the RELEASED/LEAKED sentence) also ends mid-physical-line, at "not be reclaimed." with "The gateway resolves the pod from pod_id, increments" following on the same line. Unlike the "The outcome is" case an earlier round fixed, this one is a clean sentence boundary and the quoted text is still a verbatim contiguous substring, so it is mechanically replaceable and is NOT a defect. Do not re-file it. — EVIDENCE: schemas/lenny-adapter.proto:311-313
UNVERIFIED: after SPEC-4 lands, five further in-tree Go comments still attribute the retired "cleanup timed out" leaked trigger to §6.2 and are in no edit list: pkg/sandbox/slotstate/registry.go:93 (`MarkLeaked`, "(spec §6.2)"), pkg/gateway/sessionserver/sessionserver.go:419 and :1667, pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:72 and :220. CODE-3 sweeps only slotstate.go and slothealth.go and claims no exhaustiveness. I did not file it: criterion (d) enumerates spec/docs/schemas/charts surfaces and not Go comments, and the material skeptic has already refuted this exact class twice (the CODE-3 gloss finding and the SPEC-3 carrier-table finding, both as in-code documentation polish). A human, or the fix round, can fold the five into CODE-3's list cheaply if it wants the sweep complete. — EVIDENCE: pkg/sandbox/slotstate/registry.go:93
FACT: the Go edge-list glosses CODE-3 leaves alone are not word-for-word the §6.2 fence entries ("task dispatched"/"task completes or fails" in Go against "session dispatched"/"session completes or fails" in spec/06_warm-pod-model.md:151-154), so CODE-3's sentence calling the comment "a faithful transcription of the fence" is loose. Pre-existing synonym drift, commentary only, not worth a finding. — EVIDENCE: spec/06_warm-pod-model.md:151-154 vs pkg/sandbox/slotstate/slotstate.go:99-104
USEFUL [the caller directive's "TWO PREDICATES, TWO TERMS"]: it let me check the delta's new CODE-5 sentence ("where the cleanup does not complete it does not end before the pod does") in one read rather than re-deriving the hold semantics; the sentence uses the pod-side term correctly.

### [non-spec-recheck-2.1.review-kubernetes.1]
DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on the non-spec recheck — BECAUSE the whole staged mechanism (bind_attempt token, the two adapter refusal codes, the reclaim hold, the Shutdown cascade) lives in the adapter's in-process registry and on gRPC between gateway and adapter; nothing staged writes a CRD status, adds a finalizer, uses etcd as a bus, moves a controller onto a synchronous request path, or touches an admission webhook — ALTERNATIVES: I considered filing on CODE-8 (a typed refusal now skips failPhase, so the per-pod SandboxClaim survives), on the DOCS-1 pod-state-machine rewrite (it adds the controller's own prior Sandbox.status.phase as a projection input), and on the tier-2 envtest case; each checks out against the tree and against spec/04 §4.6.1, so none met the bar.
FACT: the snapshot chain's LAST link is byte-identical to the live proposal: `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 proposals/0081_*/` returns nothing. The real delta for this round is against `non-spec-recheck-r1-prefix` — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs the live directory
FACT: DOCS-1's replacement "A pod in a warm-inventory phase with no claim projects `idle`" is NOT invented wording and is not a drift from the controller — it is §4.6.1's own first occupancy bullet, quoted verbatim, and SPEC-4 leaves that bullet untouched — EVIDENCE: spec/04_system-components.md:411 "- `idle` for a pod in a warm-inventory phase with no claim."; proposal non-spec-changes.md:2538
WATCHOUT: `ProjectOccupancyPhase`'s no-claim arm returns `("", false)` for every phase but `Reserved` and `Claimed`, so a warming/idle pod with no claim gets NO projection from the occupancy reconciler. That makes §4.6.1's first bullet loose against the code. It is PRE-EXISTING, unstaged and harmless (the warm-fill arm owns those phases); do not spend a finding on it — EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143
FACT: CODE-8's full call-site sweep re-verifies clean at the exact lines it cites: `Prepare`'s `reclaim` closure at binder.go:866-869 with `leaseAssigned` declared :865 and set :954, call sites :883 (DemoteSDK), :918 (stageWorkspace), :923 (FinalizeWorkspace), :935 (RunSetup), :950 (assignCredentials); `Launch`'s closure at :997-1000 passing literal `true` at :998, sites :1010, :1021, :1032. `failPhase` is :1072-1082 and `drain` is a bare `podclaim.DeleteClaim` at :1200-1202 — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:865-1032, :1072-1082, :1200-1202
FACT: the fix-stage summary rewrite of the `stageWorkspace` bullet is correct against the tree: `rewriteExtractedSources` (:1290-1293) and every blob-store arm (:1303-1318) return before the `PrepareWorkspace` call at :1327 — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1283-1332
FACT: CODE-3's newly staged comment quotes are verbatim in the tree. slotstate.go carries the cleanup-timeout trigger once (:104) plus the four constant docs (:34-46); slothealth.go carries it exactly twice, in the `event` doc (:33) and in `RecordLeak` (:110-111), which is what CODE-3 claims — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:33-46, :99-104; pkg/gateway/runtime/slothealth/slothealth.go:33, :110-111
USEFUL [round-22 standing line on §4.6.3 sole-writer / field manager]: "§4.6.3 gives `Sandbox.status.*` to the WarmPoolController as sole writer, and both writing reconcilers share one field manager" plus the `OccupancyReconciler` self-watch line closed the two strongest dresses this lens had (two-managers-racing-one-field; lagging read-back) without re-derivation. Keep both; they are the whole reason this lens is cheap on 0081.

### [non-spec-recheck-2.1.review-mechanism.1]

FACT: the r2 snapshot (scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2) is byte-identical to the live proposal; `diff -ru` returns nothing. The only usable delta this round is against non-spec-recheck-r1-prefix. — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs proposals/0081_.../
FACT: every proto field number SCHEMA-1 claims free is genuinely free. Verified by `awk` over each message in schemas/lenny-adapter.proto: Prepare 1-3+reserved 4; Finalize 1-4 (mid_session=4)+reserved 5; RunSetup 1-3+reserved 4; AssignCredentials 1-2+reserved 3; Resume 1-5,7-14+reserved 6,15; Shutdown 1-3,5,6+reserved 4; ShutdownResponse 1-2. Do not re-derive. — EVIDENCE: schemas/lenny-adapter.proto
FACT: CODE-3's four verbatim constant-doc quotes and its two slothealth quotes all check out exactly. — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:34-47,:99-104; pkg/gateway/runtime/slothealth/slothealth.go:33-34,:110-112
FACT: the retired `cleanup timeout exceeded` trigger has exactly five carriers in the tracked tree, all now staged: slothealth.go:33, slothealth.go:111, slotstate.go:104, spec/05:561 (SPEC-3), spec/06:148 (SPEC-4). No missing site. — EVIDENCE: grep -rn "cleanup timeout exceeded|cleanup timeout was exceeded"
FACT: `status.Code` in grpc v1.80.0 resolves through wrappers via `errors.As`, so CODE-5's new Aborted arm does fire through `*SlotBindError`. — EVIDENCE: $(go env GOMODCACHE)/google.golang.org/grpc@v1.80.0/status/status.go:96,:113,:139
FACT: only one production `adapterv1.ShutdownRequest{}` literal exists (pkg/gateway/runtime/adapterclient/client.go:814), so CODE-4's "field set in a single place" claim holds. — EVIDENCE: grep -rn 'ShutdownRequest{' --include=*.go pkg/ cmd/ tests/ sdks/ | grep -v _test
WATCHOUT: `ensureSlotStateLocked` is NOT the adapter's only entry resolver. `slotStateLocked` is called directly at slotcreds.go:70,:105,:125,:252, credentials.go:121, slot.go:185 (checkpointRootsForSession), manifest.go:380 and tracingcontext.go:47. Any claim that a gate "at the one resolve chokepoint" reaches every request that resolves an entry must be checked against those sites. — EVIDENCE: pkg/adapter/slotcreds.go:70; pkg/adapter/slot.go:185
DECISION: filed the reclaim-hold-scope finding against the non-spec lane (CODE-6/CONF-1) rather than against §4.7.1 — BECAUSE the caller directive prefers a remedy in the non-spec staging, and the spec sentence is the converged lane — ALTERNATIVES: filing it as a SPEC-5 narrowing, which I named as the alternative inside the finding rather than as the primary fix.
OPEN: if the human answers open decision 33 (observability on the residues) by wiring the adapter scrape target, N4's deferred metric half and the §16.1 adapter row both move; nobody has checked whether that changes CODE-9's staged rows.

### [non-spec-recheck-2.1.review-performance.1]

DECISION: returned an EMPTY findings list for the performance / scalability / failure-mode-reliability lens — BECAUSE the delta this round exists for is byte-empty against the r2 snapshot and, against `non-spec-recheck-r1-prefix`, is six hunks of which none touches a rate, a store, a lock scope, a watch or a failure path: two `row`→`arm` terminology swaps (non-spec-changes.md:303,:322,:2057; spec-changes.md:1301), CODE-1's "single exception"→"the returns that bypass it" correction (non-spec-changes.md:597-598), CODE-3's expansion onto four `slotstate.go` constant doc comments and two `slothealth.go` doc comments (non-spec-changes.md:785-838, all comment text, no behaviour), the CODE-5 hold-bound paragraph rewrite (non-spec-changes.md:1360-1366), the `Shutdown` tier-1 test bullet re-cut from "six-row rule table" to one case per `shutdownReclaimOutcome` arm (non-spec-changes.md:2927-2933), the Edge-cases self-recreation bullet reduced to a pointer (non-spec-changes.md:3610-3611), DOCS-3's "lands beside SPEC-5" deletion, and two summary corrections. ALTERNATIVES rejected: (1) re-filing the §10.1.4 per-member close context as an N×10s serialization regression (explicitly refuted earlier in this run: a non-last close on a shared runtime returns without touching the child); (2) re-filing the life-of-the-pod hold on disposition rows 2/3/5/7 (barred capacity family, and the hold costs no slot capacity); (3) re-filing the queued-attempt compensation FIFO amplification (already a standing FILED Open entry with a spec-only remedy).

FACT: the CODE-5 hold-bound paragraph was CORRECTED by this delta, in my lens's direction. The old text claimed the hold "is bounded by the graceful window the reclaiming request carries ... plus the removal of the slot's directories"; the new text (non-spec-changes.md:1360-1366) says "where the cleanup does not complete it does not end before the pod does" and that a gateway-side wait "would hold the client's request open for an interval nothing bounds". That now agrees with the settled life-of-the-pod arm of the §5.2 reclaim-hold paragraph, so the one latent unboundedness claim a performance lens would have chased is gone. — EVIDENCE: non-spec-changes.md:1360-1366; spec-changes.md ~:700 (`**Slot-identifier reclaim hold.**`).

FACT: no new store-backed state anywhere in the non-spec staging. `grep -n -i "redis\|postgres\|etcd\|informer" non-spec-changes.md` returns exactly three hits, all of them reads of shipped behaviour: slotbinder.go:543 freeing the slot's existing Redis reservation (:459), and the tier-4 fixture's miniredis plus the `leaked=true` non-decrement it asserts (:3354, :3359). §12.4's durable-fallback obligation stays unengaged. — EVIDENCE: non-spec-changes.md:459,:3354,:3359.

USEFUL [The staging adds NO per-session or per-request write to etcd, Postgres or Redis and net-REDUCES Postgres writes] and USEFUL [non-spec-recheck-2.x performance shard at review-log.md:2207-2215]: the three quantitative results (no new etcd/Postgres/Redis write, no slow act under the registry lock, no reconcile write loop from `ProjectOccupancyPhase`) plus the top-tier compensation-rate math (≤~0.15/s at Tier 3's ~30 claims/s and a 99.5% creation SLO) are still exact. Confirming the delta touches none of their inputs took one diff and one grep; re-deriving them would have cost the whole round. A future performance pass on an unchanged mechanism should do the same and stop.

### [non-spec-recheck-2.1.review-reliability.1]

DECISION: EMPTY findings list for the reliability lens on the whole staging (spec-changes + non-spec-changes + summary + checklist read as one document). BECAUSE every recovery-path candidate I worked up is already a recorded `### Open`, a recorded Trap, or an accepted failure mode the staging states in its single home. ALTERNATIVES considered and dropped, each with the reason: (1) "a cleanup that fails outside a `Shutdown` holds the identifier for the life of the pod, the gateway is answered `absent`/clean, `ReleaseSlotReservation(leaked=false)` frees occupancy, and the session becomes permanently unbindable on a pod that looks healthy" — this is the standing Trap that names it as the freshest-looking reliability candidate and routes it to the position-2 entry reaper; (2) "`s.reclaiming` grows unbounded over a long-lived recycling pod" — standing Open; (3) "the compensating `Shutdown` can spend its whole budget on `Resume`'s guard" — stated as an accepted failure mode in the non-spec Edge-cases section with its bound and its residue; (4) "the §10.1.4 pass's single guard-acquisition context can expire and leave later members removing unguarded" — stated as an accepted failure mode, and the fall-through disposition is the one CODE-6 specifies; (5) "Redis reset loses the withheld slot-counter decrement" — capacity family, barred.

FACT: the two-context split CODE-6 stages for `onHoldTimeout`/`terminateHeldSession` checks out against the tree. `onHoldTimeout` today mints ONE `context.WithTimeout(context.Background(), 10*time.Second)` and passes it to every `terminateHeldSession` call, where it bounds `emitFinalUsage` AND `Runtime.Close`; `removeSlotTree` takes no context at all. EVIDENCE: pkg/adapter/holdstate.go:196-205 (the shared context and the pass-2 loop), :237 (`s.emitFinalUsage(ctx, m.sessionID)`), :248-250 (`_ = s.Runtime.Close(ctx, m.sessionID)`), :254 (`_ = removeSlotTree(m.state)`). So the staged per-member close context genuinely fixes a real starvation (one member's guard wait spending a later member's final usage report), and it is not a restatement of shipped behaviour.

FACT: every non-test `&adapterv1.ShutdownRequest{` in the tree is still exactly one, behind one unexported builder, so CODE-4's "no caller edit is owed for `unconditional_teardown`" holds and rule 10 cannot silently kill the whole-pod recycle scrub. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:813-824 (`func (c *Client) shutdown`, the single literal at :814), :807-809 (`Shutdown` delegating to it), :860-867 (`ShutdownRecycle` delegating to it). Re-verified because a miss here would be the one way rule 10 turns a mandatory scrub into `INVALID_ARGUMENT`; it is clean.

FACT: `stageWorkspace` has five error returns that precede the `PrepareWorkspace` send and one that is the send's own error, which is exactly what the corrected summary bullet and CODE-4 now say. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1291-1319 (the five pre-send returns, including the `b.Blobs == nil` one at :1303-1304) and :1323-1329 (the send and its error return). The round-N fix that narrowed the summary claim to "raised before `PrepareWorkspace` is sent, through a source-rewrite error or a blob-store failure" is correct against the tree; do not re-open it in either direction.

FACT: CODE-3's four verbatim quotations into `pkg/sandbox/slotstate/slotstate.go` and its two into `pkg/gateway/runtime/slothealth/slothealth.go` all match the tree byte for byte. EVIDENCE: slotstate.go:35-37 (`Running`), :38-40 (`SlotCleanup`), :41-43 (`Released`), :44-47 (`Leaked`), :95-104 (the edge-list glosses); slothealth.go:33-34 (the `event` gloss) and :110-112 (the `RecordLeak` gloss). The delta's newest text is anchored correctly.

WATCHOUT: the disposition table's row 3 (running slot, close succeeds, another act fails) sets the clean-exit flag while holding the identifier for the life of the pod, and CODE-1's `exited_cleanly = closeErr == nil && (live || treeErr == nil)` plus `completed = closeErr == nil && treeErr == nil` implement exactly that split. A reliability reviewer reaching for "a failed tree removal is reported clean" is filing against the recorded two-predicate decision and against the most expensive recorded MISTAKE on this proposal (the `errors.Join(closeErr, treeErr)` re-key). EVIDENCE: spec-changes.md:625 (row 3), non-spec-changes.md:435 and :490.

UNVERIFIED: whether `terminateHeldSession` can be skipped for a member whose hold pass 1 already opened, leaving that member's `release` closure never run. I traced `onHoldTimeout` and found no early return after pass 1 (pkg/adapter/holdstate.go:188-205), so the only skip is a panic out of an earlier member's termination, which takes the whole process down anyway under `RestartPolicy: Never`. Nobody should spend a round on it unless a later staging adds a per-member `continue`.

### [non-spec-recheck-2.1.review-security.1]

DECISION: returned an EMPTY findings list for the security lens on non-spec recheck round 2.1 — BECAUSE the delta this round exists for is empty (`diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 proposals/0081_.../` exits 0, byte-identical), and the delta since the last security pass of this lane (`non-spec-recheck-r1-prefix`) is comment/word-level: CODE-3's slotstate/slothealth doc-comment re-keys, four `row`→`arm` word swaps, the CODE-5 reclaim-hold rationale rewrite, the DOCS-2 link-form sentence deletion, two SCHEMA-1 verbatim-quote corrections, the Shutdown test bullet re-keyed onto `shutdownReclaimOutcome` arms, the summary's `stageWorkspace` bullet and the Edge-cases pointer compression. None of it touches a control, a predicate or a bound — ALTERNATIVES: derived and dropped four dresses, recorded below.

FACT: the round-2.1 delta is literally nothing. `diff -rq -x '*.review-log*.md' /home/ec2-user/lenny/scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 /home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry` returns exit 0 with no output. A later round should diff against `non-spec-recheck-r1-prefix` to see this lane's real delta.

FACT: the hold's refusal surface re-verifies clean against the tree. `ensureSlotStateLocked` has exactly three production call sites (`pkg/adapter/slot.go:143` ensureSlotPaths, `pkg/adapter/slotcreds.go:26` assignCredentialsSlot, `pkg/adapter/slotsession.go:75` claimSessionSlotUnderLock); every credential control that must not be refused — revoke, rotate, extend, the coordinator fence — resolves through the read-only `slotStateLocked`/`boundSlotState` pair instead (`pkg/adapter/credentials.go:121`, `pkg/adapter/slotcreds.go:70,:105,:125,:252`, `pkg/adapter/coordination.go:116,:254`). So CODE-6's `errSlotReclaimInProgress`, which sits at the top of `ensureSlotStateLocked`, cannot refuse a mandatory credential control. This confirms the standing-context claim rather than resting on it.

FACT: the `unconditional_teardown` statement is genuinely unforgettable by a caller. `cmd/lenny-gateway/user_revocation.go:129` calls `bind.Adapter.Shutdown`, which is `Client.Shutdown` at `pkg/gateway/runtime/adapterclient/client.go:807`, which delegates to the single unexported builder `Client.shutdown` at `:813`; `Client.ShutdownRecycle` at `:860` delegates to the same builder. Setting the field inside that builder therefore covers the §11.4 full-revoke fan-out and every future teardown caller, which is what keeps rule 10 from turning a credential revocation into an `INVALID_ARGUMENT`. Re-derived, not carried.

FACT: DOCS-4's clause deletion is safe on both pages and leaves a grammatical sentence. `docs/operator-guide/security-principles.md:33` and `docs/reference/execution-modes.md:68` each end the target sentence with ", and the adapter reports its outcome to the gateway"; on execution-modes.md the paragraph continues with two further sentences that carry no dependency on the deleted clause, and on security-principles.md the fail-closed acknowledgment paragraph below (`:35-39`) is independent of it.

WATCHOUT: `materializeSlot`'s `releaseAttemptCredentials(minted)` runs on the `SLOT_BIND_ALREADY_STARTED` refusal arm too, and there is a reachable interleaving where one provider's `AssignCredentials` was already admitted before a late `StartSession` started the session, so this attempt's already-written lease is released while the started session's `credentials.json` still holds its material. It is NOT a finding: releasing a lease revokes it (fail-closed), the slot identifier is the session identifier so no cross-session or cross-tenant reach exists, and the tier-9 third arm pins that a failed attempt's release strips none of a successor's leases (non-spec-changes.md:3519-3525). Do not file it without evidence of a cross-session reach.

WATCHOUT: CODE-1's removing arm proceeds destructively when `lockSlotGuard` fails to acquire within the request context (non-spec-changes.md:~340, the `slot_guard_not_acquired` warn). That reads as a bypassable mandatory gate, but removing unguarded is the shipped behaviour of every deregister-then-destroy site today, so the guard is a new and strictly-additive control rather than one this proposal weakens, and the entry-level decision is still made under `s.mu` after the acquisition attempt, so a successor's entry is never deregistered on a stale comparison. "Merely less strict than it could be" applies.

USEFUL [spec-recheck-2.1.review-security.1]: its FACT that `maxSessionsPerPod` is the explicitly security-motivated reuse bound (spec/05:488) and that SPEC-3's narrowing of the `sessions_served` increment conforms spec to shipped handler behaviour rather than relaxing a live bound saved me re-deriving the whole `sessions_served` path on the non-spec side; I confirmed the non-spec staging adds no second source for that count (`RecordSessionScrub` increments before it branches on the leak flag and holds no dedup, non-spec-changes.md:~440 citing scrubreport_server.go:451-481), so the one-report rule stays an adapter-side invariant with no gateway-side fallback — which is a durability question for §12.4 only if the count ever moved to Redis, and it has not.

USEFUL [non-spec-recheck.1.review-security.1]: its FACT list on CODE-8's `failPhase` gating and on the §7.4 tenant-scoped mid-session guard is accurate and let me skip re-deriving both; I re-checked only the `Client.shutdown` builder claim, which also holds.

### [non-spec-recheck-2.1.review-single-source.1]

FACT: the two snapshot dirs differ. `diff -ru` against `scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2` is BYTE-EMPTY; the real delta for this round is against `non-spec-recheck-r1-prefix`. Use the r1-prefix snapshot. — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs proposals/0081_*/ (empty diff)

FACT: the retired `leaked` trigger ("cleanup timeout exceeded") has SEVEN in-tree Go carriers, not four. CODE-3 now re-keys four (slotstate.go:44, :104; slothealth.go:33, :111). Three more are in no edit list: `pkg/sandbox/slotstate/registry.go:93` (`MarkLeaked`, same package CODE-3 opens), `pkg/gateway/sessionserver/sessionserver.go:419` and `:1667`, and `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:72` and `:220` (five sites over three files). Command: `grep -rn "cleanup tim" pkg/ --include=*.go | grep -v _test.go`. Spec and docs carriers ARE covered (spec/05:545 and :561 by SPEC-3, spec/06:148 by SPEC-4, state-machines.md:251 by DOCS-1); docs/reference/metrics.md carries no such sentence. — EVIDENCE: pkg/sandbox/slotstate/registry.go:93, pkg/gateway/sessionserver/sessionserver.go:419

FACT: the proposal's own ground for treating a timeout-keyed `leaked` statement as a defect is spec-changes.md:741 — "The table's `leaked` column states the grounds, and the parenthetical's timeout is none of them, so it names a cause as though it were the predicate." That sentence is the argument a future round should cite when closing the remaining carriers. — EVIDENCE: spec-changes.md:729-746

WATCHOUT: do NOT re-file the `sbe.Leaked = cerr != nil || !cleanly` double-staging (non-spec-changes.md:989-1000 and :1060-1072). It is one code site shown twice, once as an excerpt with its own comment and once inside the `materializeSlot` wrapper with a differently-worded comment. The rule is identical in both; only the comment prose differs, and this loop has twice refuted comment-wording findings as polish. I considered and dropped it. — EVIDENCE: non-spec-changes.md:998-1000, :1066-1072

FACT: the corrected summary bullet on `stageWorkspace` is accurate against the tree. `Binder.stageWorkspace` has five error returns before the `PrepareWorkspace` send (rewrite error, nil `Blobs`, `ParseURI`, `Blobs.Get`, `io.ReadAll`) and a sixth return that IS the send's own error, so "raised before `PrepareWorkspace` is sent, through a source-rewrite error or a blob-store failure" is exactly right. Do not re-open. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1283-1330

FACT: CODE-3's four constant-doc quotes and its two slothealth quotes are verbatim in the tree, and SPEC-4 does leave exactly the three glosses CODE-3 says it leaves (`slot_assigned → receiving_uploads`, `running → slot_cleanup`, `running → failed`). Verified this pass; do not re-derive. — EVIDENCE: pkg/sandbox/slotstate/slotstate.go:34-47, :99-104; spec-changes.md:929-975

DECISION: filed exactly one finding (the incomplete `leaked`-trigger reduction) — BECAUSE the single-source sweep over CONF-1, the tier-3 case list, the checklist, DOCS-1/2/3/4, SCHEMA-1 and the Edge-cases sections found every other rule reached by number-and-name reference rather than restated. ALTERNATIVES: rejected filing on SCHEMA-1's `ERROR_CODE_SLOT_BIND_*` proto comments (they state rules 5/6/13 conditions in full) because the deliverable justifies the proto as the third-party implementor's first read and this lens has passed over them in three prior sweeps; rejected the `sbe.Leaked` double-staging above.

### [non-spec-recheck-2.1.review-test-coverage.1]
DECISION: Returned zero findings for the test-coverage lens — BECAUSE every staged behavior I could enumerate (CODE-1 through CODE-9, SCHEMA-1, CONF-1, DOCS-1 through DOCS-4, SPEC-1 through SPEC-6) maps onto a concrete named case in the Testing section at a tier that matches the surface it touches, and the round's delta only re-keyed one bullet and added comment-only edits — ALTERNATIVES: rejected filing the `cmd/lenny-gateway/metricsbackfill.go` hook-wiring gap (see WATCHOUT below); rejected filing an absent test for SPEC-2's §7.2 mid-resume step-3 reclaim (see FACT below).
FACT: the snapshot at scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 is BYTE-IDENTICAL to the current proposal (diff -ru returns nothing). The real delta for this round is against non-spec-recheck-r1-prefix, which is the other snapshot the prompt names. Use that one. — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-recheck-r2 vs proposals/0081_.../
FACT: the Testing bullet re-keyed this round, `**`Shutdown`'s teardown cascade, one case per arm of `shutdownReclaimOutcome`**`, enumerates exactly the five switch arms the staged function has (`!ok`, `unconditional`, `bindAttempt == ""`, `bindAttempt != attempt`, default), which map one-to-one onto §4.7.1 rules 11-14; rule 10 is the preceding four-row precondition bullet and rule 15 is asserted through the outcome assertion. The enumeration is complete. — EVIDENCE: non-spec-changes.md:2927-2931 against non-spec-changes.md:249-262 and spec-changes.md:1057-1062
FACT: the scope-accounting sweep's tier list ("tiers 1, 2, 3, 4, 7a, 9 and 10") is exact. `grep -rln "ShutdownRequest{" --include=*_test.go pkg/ tests/` returns files in pkg/adapter (tier 1), tests/tier2_component/warmlayout, tier3_contract (3 dirs), tier4_integration (2), tier7a_load_local (6), tier9_security (3) and tier10_conformance (1). No tier-5 or tier-8 file constructs one, so no tier is missing from that list. — EVIDENCE: non-spec-changes.md:3033-3037
FACT: the Testing bullet "**The pre-`PrepareWorkspace` workspace failure, separately**" is accurate against the tree: with `b.Blobs == nil` and an original `uploadFile` ref, `stageWorkspace` returns "plan has upload source(s) but the binder has no blob store" before the `cl.PrepareWorkspace` call further down the function. The summary's reworded bullet ("raised before `PrepareWorkspace` is sent, through a source-rewrite error or a blob-store failure") now agrees with it. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1305-1307 (nil-Blobs return) vs :1324-1329 (the PrepareWorkspace send)
WATCHOUT: CODE-9 names `cmd/lenny-gateway/metricsbackfill.go:149` as the hook's only production wiring site and states outright that "the tier-1 case below sets the hook directly and so cannot observe the omission", then lists no test for the wiring. This LOOKS like a clean (f) finding. It is not worth filing: no sibling hook in that same block (`SlotFailure`, `SlotConflict`, `Rehydration`, `ClaimAccepted`, `SDKDemotion`) has a wiring test anywhere in the tree (`grep -rln "podBinder\." cmd/lenny-gateway/*_test.go` returns nothing), so a wiring test here is coverage beyond what the changed behavior requires and refutes as nice-to-have. — EVIDENCE: non-spec-changes.md:2028-2034; cmd/lenny-gateway/metricsbackfill.go:141-161
FACT: SPEC-2's §7.2 step-3 replacement ("Reclaim the pod-side state and release the replacement pod") adds no untested behavior. The mid-resume terminal collapse is a REST write plus a `coordination_generation` bump (`pkg/gateway/sessionserver/sessionserver.go:2950-2957`, `:2990-3005`); the in-flight re-attach then fails its next RPC on the §4.2 fence, which is the failed-`Resume` path CODE-4 stages and the Testing section's "**The resume path**" bullet pins. Do not file the absent §7.2-named test. — EVIDENCE: non-spec-changes.md:3186-3197; spec-changes.md:444-450

### [redesign.6.fix.1]

DECISION: the `running` boundary is defined ONCE, at the record step of the staged §4.7.1 registry critical-section paragraph, which now reads "On a request that starts a session, the step is the resolve, the confirmation and the record that rule 8 states; the record is the adapter's recording of the pod's shared runtime process as holding the session, and the slot reaches [Section 6.2] `running` at that record." Staged §6.2's pre-`running` paragraph cites it ("A slot is pre-`running` until the record step of the [§4.7.1] registry critical section, and reaches `running` at it") and every other site in the five files carries only `running` / pre-`running`. SPEC-4's fence commentary states one policy, "Every per-slot fence entry this proposal edits carries a pointer at the paragraph or section that owns its edge, and none carries a trigger", and all four edited entries sit under it. — BECAUSE the record is the adapter's act inside the critical section rule 8 already orders as resolve → confirm → record, so the atomicity paragraph is where a reader of any citing site lands anyway, and one home ends the three-vocabulary drift the round-4 introspection traced across five files. — ALTERNATIVES: home in §6.2's pre-`running` paragraph with §4.7.1 citing it (rejected: §6.2 is a per-slot state fence and prose that owns edges, and it would have the adapter's own act defined in a section the adapter contract then cites backwards; rule 8 and the critical section already name the record, so §6.2 would be the second statement); keep both statements and word them identically (rejected: two statements is the defect the introspection named).

FACT: sites rewritten. spec-changes.md: the Design `running`-boundary paragraph (heading, "off the shared runtime process", Owner line); the §4.7 `Shutdown` row ("rather than when the slot reaches `running` ([Section 4.7.1])" in place of "at the moment the session reaches the runtime"); SPEC-2 commentary "starts the session on its own" in place of "reaches the runtime start"; the SPEC-4 fence-policy sentence; the §6.2 paragraph's first sentence; the commentary after it ("cites the `running` boundary's home in §4.7.1"); the §4.7.1 critical-section record step (the home); the "Spec files touched" §6.2 line ("citing §4.7.1 for the `running` boundary"). summary.md: the Goals residue-class clause ("where the start-confirmation rule refuses, take the abandoned session back off the pod's shared runtime process"); the SPEC-4 bullet ("cites §4.7.1's record step"); the SPEC-5 bullet ("whose record step is the `running` boundary"). non-spec-changes.md: CODE-1 `exited_cleanly` rationale (two clauses, "§4.7.1 `running` boundary", "a slot that did not reach `running`", "a session whose slot reached `running`"); CODE-2's staged `noteRuntimeStarted` doc comment ("records the pod's one shared runtime process as holding sessionID, the record at which the slot reaches running"); CODE-3's `Running` constant doc comment ("the sub-state a slot enters when the adapter records the pod's shared runtime process as holding the session" in place of "the dispatched sub-state"); DOCS-1's commentary "in the terms the staged §4.7.1 record step retires"; the tier-7a `Resume` arm ("an entry whose slot reached `running`"); the accepted-failure bullet ("before the slot reached `running`"). implementation-checklist.md: S5 (fence policy and the §4.7.1 citation, count word dropped), S7 (the `receiving_uploads` → `running` row replacement), S17 (both clauses onto "a slot that reached `running`" / "a slot that did not reach `running`"). Verified agreeing with the home without edit: DOCS-1's `receiving_uploads` → `running` row ("the adapter records the pod's shared runtime process as holding the session") and `receiving_uploads` → `slot_cleanup` row, DOCS-2's `Shutdown` row ("for a slot that reached `running`"), CODE-3's two `(see §6.2 "Pre-running slot cleanup")` glosses (transcriptions of the fence pointers). EXEMPT list after the exit grep: empty. problem-statement.md was not edited and carries no hit. Staged rule 8's "takes the session back off the shared runtime process" (names the undo rather than the boundary) and non-spec-changes.md's "before `noteRuntimeStarted` runs" (names the function) carry none of the banned phrases and stand. — EVIDENCE: `grep -n 'given the session\|was given\|runtime is given\|has been given\|reaches the runtime'` over spec-changes.md, summary.md, non-spec-changes.md, implementation-checklist.md and problem-statement.md returns nothing.

CORRECTS [spec-recheck.1.fix-G1.1] WATCHOUT: summary.md's residue-class clause "in the class where the runtime was given it" is no longer a legitimate survivor; it is rewritten onto the start-confirmation rule's refusal. CORRECTS [spec-recheck.1.fix-G1.1] OPEN: the §4.7 `Shutdown` row no longer says "the moment the session reaches the runtime"; it contrasts admission with the slot reaching `running` and cites §4.7.1, so there is no third boundary term to confirm. CORRECTS [spec-recheck.1.fix-G1.1] DEFERRED and [spec-recheck.1.fix-design-G1.1] WATCHOUT: the checklist S5/S7 wording and the accepted-failure bullet are rewritten; the nine-carrier list is superseded by the grep in `### Settled`. CORRECTS [spec-recheck.1.fix-G1.1] DECISION and [spec-recheck.1.fix-design-G1.1] DECISION: the boundary is no longer "stated once in staged §6.2"; §6.2 cites §4.7.1. CORRECTS [spec-recheck.1.review-mechanism.1] WATCHOUT: the §4.7 `Shutdown` row's clause quoted there is reworded; its truth (admission and the record are distinct instants) holds under the new wording. CORRECTS `### Settled` "The pod's shared runtime process has been given is bound vocabulary": that phrase is retired everywhere in the proposal, and the Settled line is replaced by the one-home entry. CORRECTS `### Traps` "SEVEN rounds on one boundary ... Fix that block only as ONE whole rewrite": the whole rewrite is done; the Traps entry is rewritten to bar reopening it a facet at a time.

WATCHOUT: the `running` boundary is defined in the §4.7.1 record step only. The five banned phrases ("given the session", "was given", "runtime is given", "has been given", "reaches the runtime") are never reintroduced anywhere in spec-changes.md, summary.md, non-spec-changes.md or implementation-checklist.md; a lens that wants to restate the boundary at a citing site writes `running` and cites §4.7.1, and a finding that a citing site "does not define" `running` is answered by the home. Rules 1 through 15 of the staged §4.7.1 block are byte-identical to HEAD; only the critical-section paragraph's record-step sentence changed.
