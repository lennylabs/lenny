# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass 26, 2026-09-20). Read the whole ledger. Pass 25 had already lifted spec
rounds 2 through 10, so this pass lifted what the ledger gained since: `[redesign.2.fix.1]`'s
carrier-table decision and its carrier-sweep WATCHOUT, the two `f1.open-decisions` blocks (whose
decision-21 resolution `[redesign.2.fix.1]` withdrew), and the four round-11 lenses. Added 21 Settled
lines, 5 Traps and 1 Open entry, and folded the round-11 re-verifications of the anchor sweep, the
OpenAPI/SDK sweep and the §5.2 gate-redirect hazard into the entries that already carried them rather
than repeating them. Honoured both `CORRECTS` in `[redesign.2.fix.1]`: neither names a live standing-context
claim that is now false (the §12.6 read-clause reversal and open decision 21 are already recorded as
round 10 and the human's), so nothing was rewritten or deleted for them. Deleted no trap and dropped
no `OPEN`, `UNVERIFIED` or `DEFERRED`. Did NOT reach the 472-line target: this section is roughly 640
lines, for the reasons pass 25 recorded, and this pass adds to it rather than cutting. Nothing that
mattered was dropped to make the number.

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
- **"Completed", "the cleanup does not complete" and "the `leaked` predicate" are THREE different
  things.** Close succeeds and directory removal fails: not complete, identifier held for the pod's life,
  reports `released`, NOT `leaked`. §7.1's "did not complete" means unanswered or no clean exit. `leaked`
  is entered on the GATEWAY's reading; "does not complete" is never its trigger.
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
  reclassify the untokened `superseded` as a reclaim that "did not complete".** It may be a live
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
- **MISTAKE nearly filed, round 11: SPEC-4's rationale puts quotation marks around a sentence that is
  NOT in `occupancy.go`.** It writes "the §6.2 state machine encodes the recycle-versus-one-session
  distinction in the phase the pod sits in at the claim DELETE"; the file says "The phase the pod sits
  in at the DELETE selects the edge, and the §6.2 state machine constrains it". Same rule, different
  words, judged below the bar on the `ProjectOccupancyPhase` precedent. Do not spend a round on it.
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
- **Does "takes the session back off the shared runtime process" hold for every runtime implementation?** — UNVERIFIED: `InProcessRuntime.Close` and `MCPRuntime.Close` ignore the session identifier, so rule 8's undo can tear down co-tenants.
- **Can a coalesced reconcile lose the claim-DELETE retirement?** — UNVERIFIED [spec.7.review-kubernetes.1, spec.10.review-kubernetes.1]: a CREATE, `bound` patch and DELETE inside one window leaves `("", false)` and an idle unscrubbed pod holding the residue. Pre-existing; remedy is code.
- **Does a gateway replica's §10.1.7 preStop drain discharge the §7.1 reclaim obligation?** — UNVERIFIED [spec.10.review-reliability.1]: the Edge-cases bullet covers a crash, and a graceful drain is distinguishable.
- **Does the gateway double-book a leak for a runtime-given slot whose close fails?** — UNVERIFIED [spec.7.review-reliability.1]: row 2 gives both a `leaked` report and an unclean response into one `slothealth.Tracker` with no per-session dedup. Check `MarkLeaked` idempotency.
- **Does withholding the report change the `lenny_pod_session_reuse_count` histogram and the `mode_factor`?** — UNVERIFIED [spec.7.review-performance.1]: unknown whether the observation shares `RecordSessionScrub`'s call site.
- **Can a `PrepareWorkspace` frame on an already-admitted call write into a tree `removeSlotTree` is deleting?** — UNVERIFIED [spec.10.review-mechanism.1]: rule 2 does not re-admit a frame; reachability turns on whether the handler honours the cancelled server context.
- **Do the two SPEC-6 series belong in §16.1.1's "Used on" column?** — UNVERIFIED [spec.10.review-client-surface.1]: the column is prose-descriptive and no gate reads it.
- **Does `podStateGatewayWrittenSentence` keep its trailing `ReportPodScrub` clause after the re-key?** — UNVERIFIED [spec.9.review-fresh.1]: the implementor should keep the full sentence rather than truncate the constant.
- **Should §6.2's projection preamble at spec/06:80 keep enumerating the inputs at all?** — OPEN [spec.10.review-edit-sites.1]: SPEC-4 adds a fourth input to §4.6.1's enumeration and not to §6.2's; the remedy is a reduction ending that clause at the §4.6.1 citation.
- **Has the "which teardown it asks for" vocabulary leaked into the DOCS-2 `adapter-contract.md` mirror?** — UNVERIFIED [spec.5.review-mechanism.1]: only `spec-changes.md` was reviewed; grep the whole directory before declaring that fix complete.
- **Is the ten-second graceful window an operator-tunable constant?** — OPEN [spec.10.review-single-source.1]: §10.1 states no close window and the code only comments "best-effort", so the figure is minted here; `code-best-practices.md` requires a non-spec default to be overridable.
- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** — OPEN, for a human [spec.4.fix-design-G3.1]: the same refusal is non-retryable at one stage and retryable at another, and nobody tested it against a client.
- **Do `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` and the session-scrub addressing gate live in the same file?** — UNVERIFIED [spec.4.fix-G2.1]: DOCS-2's `## Testing` names a third file. Nothing depends on it yet.

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
- DEFERRED [implementation-checklist.md, S2 final sentence]: it says §29.4's session-end step 13
  "restates" the graceful-shutdown signal's co-tenancy condition. Round 18 trimmed the staged
  sentence to a citation, so the step now cites §4.7 and states nothing about the condition itself.
  Replace the sentence with the summary's wording. Body in `[spec.18.fix.1]`.
- DEFERRED [implementation-checklist.md, S15, S16, S19, S22]: the checklist predates the
  restructure. S15 and S16 undercount their edits and S16 must key the hold release on completion; the
  rest are untraced against the current staging.

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
- Retired in compaction pass 26: nothing. Round 11's four lenses closed no Open, no UNVERIFIED and no Deferred entry, and the two `CORRECTS` in `[redesign.2.fix.1]` name claims this section already states in their corrected form (the §12.6 read clause as round 10's pointer at §5.2, open decision 21 as the human's), so no entry was rewritten or moved out.
- Retired by earlier passes (1 through 24, `prune.1.fix.1`), bodies in the archive: the `coordination_generation` fence question; the resume-path exclusion; the slothealth-ledger resume question; §29.4 scoping; the tier-7a drain-gate items; Redis rehydration of leaked occupancy (own proposal); `releaseCredentials` and user-source leases; the reclaim-deadline margin; the spec/18 edit question; a fourth `SlotReason`; every bind-epoch, epoch-latch, `bind_epoch` field-number, and `ExcludePod(s)` entry; the fourteen-step checklist; the §10.1.4 and `DemoteSDK` report carve-outs; the "one return-to-pool edge" question; whether `superseded`, a pairing-rule failure, or `absent` still runs the whole-pod scrub.

## Ledger

### [spec.12.fix-G1.1]
DECISION: closed the §15.4 domain-narrowing finding by deleting the four words "to a request that carries it" from the staged `**Bind attempt token contract:**` sentence, leaving "...the numbered rules an adapter applies, and that statement is normative for a third-party adapter." — BECAUSE the domain of rules 1-15 has one home, §4.7.1's `**Admission.**` preamble (seven entry-creating RPCs) plus rule 10's Shutdown cascade, and §15.4 is a pointer plus a conformance criterion that states no domain — ALTERNATIVES: naming the domain in §15.4 (rejected: a second home, and it re-adds a sentence to a block the caller fixed by reduction); widening rule 8 in §4.7.1 (rejected: rule 8 is already correct, "required of every request that starts a session, including one that carries no token of its own"); narrowing §4.7.1's admission domain to the token-carrying requests (rejected: drops `StartSession` and `ConfigureWorkspace` out of the rules and breaks the start-versus-reclaim safety argument).
FACT: the narrowing phrase existed at exactly one site in the whole proposal directory, and `bind_attempt` has no footprint anywhere under spec/, schemas/ or docs/, so the deletion cascaded nowhere. — EVIDENCE: `grep -rn "to a request that carries" proposals/0081_*/` returned only spec-changes.md:1107
WATCHOUT: the §15.4 block is now in its fifth-and-corrected form and remains exactly two sentences. Every earlier round that touched it added words and was reversed. A finding about it is closed by removing words from it or from §4.7.1, never by adding a clause to §15.4. — EVIDENCE: spec-changes.md, the `**Bind attempt token contract:**` block under "SPEC-5 · spec/15_external-api-surface.md § 15.4"
MISTAKE: commit 9c59dabec, the hand redesign that reduced §15.4 to two sentences, rewrote "the numbered rules an adapter applies to it" into "...to a request that carries it" while shortening the block. The narrowing cost a round: it excluded `StartSession` and `ConfigureWorkspace`, which carry no token, and so excluded the very requests rule 8 exists for.

### [spec.12.fix-G2.1]
DECISION: closed the finding by deleting "The whole-pod scrub or " from the last column of exactly the two disposition-table rows whose `leaked` column reads `Entered` (the runtime-close-fails row and the pre-`running`/act-fails row), so each reads "Pod termination ends the slot's directories and all else" — BECAUSE a slot that enters `leaked` keeps pod occupancy above zero and the whole-pod scrub only runs at occupancy zero, so on those two rows the named scrub is unreachable — ALTERNATIVES: a footnote or a qualification on the staged `**Scrub model.**` opening sentence (a second statement of the scrub trigger, whose home is that sentence and spec/05 Lenny scrub procedure); the shorter "Pod termination ends all of it" (drops the word "directories" that the column's other rows use to mark what the scrub reaches); sweeping all rows carrying the phrase (the `Not entered` rows free occupancy, so the scrub is reachable and the phrase is true there).
FACT: `leaked` slots are retained until pod termination and still count against pod occupancy — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:395, and the session-count decoupling at spec/05_runtime-registry-and-pool-model.md:488 ("a persistently `leaked` slot can hold total occupancy above zero indefinitely"); the whole-pod scrub's occupancy-zero trigger is spec/05_runtime-registry-and-pool-model.md:459.
WATCHOUT: the phrase "The whole-pod scrub or pod termination ends the slot's directories" still stands, correctly, on the disposition-table rows whose `leaked` column reads `Not entered`. A future sweep on the phrase must read the `leaked` column before deleting — EVIDENCE: the staged `**Scrub model.**` append in 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md, disposition table.
UNVERIFIED: whether an adapter registry entry that stands with nothing reported (the SDK-demotion-close-failure row and the "no cleanup reclaims" row, both `leaked` = Not entered) leaves gateway-side occupancy above zero the way a `leaked` slot does, which would make the whole-pod scrub unreachable on those rows too. Nothing in spec/05, spec/06 or pkg/gateway asserts it either way; a later round or the non-spec lane should settle it. It is pre-existing and not caused by this edit.


### [spec.12.fix-design-G1.1]
DECISION: Fix finding 0 by deleting exactly the four words "to a request that carries it" from the staged §15.4 `**Bind attempt token contract:**` sentence (spec-changes.md:1107), leaving "...the registry critical section, and the numbered rules an adapter applies, and that statement is normative for a third-party adapter." — BECAUSE the domain of the rules belongs to §4.7.1's `**Admission.**` preamble (spec-changes.md:1044, seven RPCs incl. the two token-less ones) and rule 10's `Shutdown` cascade; §15.4 must state no domain of its own, and the caller directive fixes a §15.4 finding by removing words, never adding a sentence — ALTERNATIVES: (a) rewrite the clause to enumerate the domain in §15.4 — rejected, it duplicates §4.7.1's preamble and gives the rule a second home; (b) leave the narrowing and instead widen rule 8's own text — rejected, rule 8 is already correct at :1055 and the defect is only in the pointer; (c) narrow §4.7.1 to match §15.4 — rejected, it would break the start-versus-reclaim safety argument at :150-152.
FACT: The buggy phrase occurs exactly once in the whole proposal directory; no other carrier (DOCS-2, CONF-1, claim-register rows, summary) repeats the narrowing, so the deletion has no cascade. — EVIDENCE: grep "an adapter applies" over proposals/0081_.../ returns only spec-changes.md:1107
FACT: `bind_attempt` has zero footprint in the current tree (spec/, docs/reference/, schemas/), so nothing outside the proposal can be falsified by the edit. — EVIDENCE: grep -rn "bind_attempt" spec/ docs/ schemas/ (no hits)
WATCHOUT: The §15.4 block must stay exactly two sentences after the edit. The tempting follow-on ("...applies to every request that section governs") reintroduces a domain statement in the wrong home and is the fifth-form regression the caller directive warns about. — EVIDENCE: proposals/0081_.../...spec-changes.md:1107

### [spec.12.fix-design-G2.1]
DECISION: apply the reviewer's reduction verbatim in two cells only — delete "The whole-pod scrub or " from the last column of spec-changes.md:650 and :653, leaving "Pod termination ends the slot's directories and all else" — BECAUSE those are exactly the two rows whose `leaked` column reads `Entered`, and a `leaked` slot pins pod occupancy above zero (spec/05_runtime-registry-and-pool-model.md:395, :488; spec/06_warm-pod-model.md:160), while the whole-pod scrub runs only at occupancy zero (spec/05:459; staged opening sentence at spec-changes.md:595) — ALTERNATIVES: (a) rewording the cells to "Pod termination ends all of it" (shorter, but drops the "directories" term the column's other rows key on, so the table stops reading as one enumeration); (b) adding a footnote or exception sentence under the table explaining when the scrub is reachable (hair: a conditional added to avoid restating a rule); (c) adding a `leaked`-aware clause to the staged `**Scrub model.**` opening sentence (wrong home: the opening sentence states the scrub's trigger once and §5.2:488 already states the decoupling).
FACT: the exact cell string "The whole-pod scrub or pod termination ends the slot's directories" occurs nowhere in the repository outside this one staged table (rows 650, 651, 653, 655, 656, 657). No cascade: §6.2, §7.1 and the `**Slot cleanup:**` bullet cite the table rather than restating the cell — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:645-657
FACT: the Settled line "the whole-pod scrub ends directories only" in the review log's standing context is about what the scrub does wherever it is named; it is NOT falsified by two rows ceasing to name the scrub — EVIDENCE: proposals/0081_*/0081_*.review-log.md, Settled entry "DECISION: where the old sites disagreed, the table takes these answers."
WATCHOUT: do not touch the four `Not entered` rows that carry the same phrase (:651, :655, :656, :657). On those the reported disposition frees gateway-side occupancy, so the occupancy-zero scrub stays reachable and the phrase is correct — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:651,655,656,657
UNVERIFIED: row :657 ("Pre-`running` slot no cleanup reclaims", `leaked` = Not entered) and row :656 (SDK demotion whose runtime close fails, registry entry stands) both leave an adapter-side entry with nothing reported to the gateway. Whether the gateway's occupancy counter is nonetheless decremented on those paths — and therefore whether the scrub is genuinely reachable there — is not stated anywhere I could find; spec/06:160 documents occupancy counting for `leaked` specifically. Someone should derive it from pkg/gateway/podlifecycle/podclaim/slotclaimer.go before a later round either widens this fix or files a second finding. It is independent of this two-cell reduction.

### [spec.12.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens on spec round 12 — BECAUSE I re-ran the whole class list (forward reference, underspecified target, relocation-that-loses-content, gate state, unresolvable anchor) end to end against the tree and every check passed — ALTERNATIVES: filing the SPEC-4 `claimed ──→ draining` single-block edit as an ambiguous instruction (rejected: the block is unambiguously the replacement, it is the only zero-hit block of its kind, and the standing context already records it as such); filing the §4.7.1 carriage table's `PrepareWorkspace`/`mid_session` row as false against the proto (rejected: SCHEMA-1 adds `bool mid_session` field 6 to `PrepareWorkspaceRequest`, non-spec-changes.md:2301); filing the §6.2 no-claim clause deletion as content loss (rejected: §4.6.1's untouched first bullet absorbs it, as the standing context records).

FACT: every anchor, every heading and every markdown link target in the staged spec edits re-verifies clean in round 12. Anchors: §4.1 sentence spec/04_system-components.md:157; §4.7 `Shutdown` row :686; `DemoteSDK` row :674; `ReportSessionScrub` row :692; §4.7.1 insertion point between :693 and :695; §4.7.9 step 5 :853; §4.6.1 enumeration :409 (unique) and bullets :415, :416; §5.2 scrub model :453, `Slot cleanup:` bullet :545, whole-pod trigger :561; §6.2 projection prose :80, fence entries :95, :148, :152, :155, insertion point before :158, cancel bullet :234; §7.1 atomicity paragraph :23 with continuation :24; §7.2 preamble :210, step 2 :213, step 3 :214; §7.3 list end :414; §12.6 :481 and :494; §15.1 row :1136; §15.4 insertion between :1469 and :1471; §29.4 step 13 ends at the `§15.4.3 … §28.5.3).` sentence. All 21 distinct link anchors used in staged blocks already resolve in the corpus. EVIDENCE: spec/04_system-components.md:157,674,686,692; spec/06_warm-pod-model.md:80,95,148,152,155,234

FACT: the two new citations a fresh reader would doubt both hold. `/run/lenny/slots/{sessionId}/credentials.json` removal is shipped (`slotlayout.RemoveTree` iterates `p.CredentialsDir`) and the timer cancellation is shipped (`deregisterSlotLocked` loops `st.timers`); §4.9's direct-delivery-mode expiry timer is real. EVIDENCE: pkg/adapter/slotlayout/tree.go:58-68; pkg/adapter/slotsession.go:174-189; spec/04_system-components.md:1169

FACT: `ShutdownResponse.exited_cleanly` is a shipped field, so the §5.2 table's "Clean-exit flag on the `Shutdown` response" column and §7.1's clean-exit predicate both resolve to something an implementor can find. EVIDENCE: schemas/lenny-adapter.proto:1665-1668

USEFUL [the verbatim-anchor sweep entry in the standing context]: it named exactly which blocks are expected to be zero-hit (the replacement texts, the two new fence entries, the SPEC-4 `claimed ──→ draining` replacement and the §16.1 rows), which is what let me stop treating the single-block SPEC-4 edit as an ambiguity rather than a known form.

### [spec.12.review-citations.1]

DECISION: filed exactly one finding, the §15.1 finalize-precondition-note attribution in the SPEC-5
commentary — BECAUSE every other concrete citation in spec-changes.md re-verified against the tree
this round, including the whole carrier table (new since the round-11 sweep) —
ALTERNATIVES: filing the `claimed ──→ draining` SPEC-4 block as an unmarked replacement-without-original
(rejected: standing-context entry on the anchor sweep already records it as a known zero-hit
replacement block, and the entry it names is unambiguous), and filing the §4.6.1 closing-sentence
quote that renders the spec's `[Section 6.2](...)` link as `§6.2` (rejected: the whole bullet is
replaced and the meaning is unchanged).

FACT: the SPEC-3 carrier table's 24 rows ALL re-verify. Every named file exists and every named
comment/site carries the "on every session release" / "at each session release" wording the row
attributes to it. Verified with one grep over the row set: `grep -rn "session release" <the 24 paths>`.
The four "not a carrier" sites also check out: spec/06:24 and spec/07:72 state only that the cleanup
runs, and `pkg/adapter/gatewaycontrol/scrubreport.go:13` (the `SessionScrubOutcome` comment) is
distinct from `:72` (the `Client.ReportSessionScrub` comment the table lists).
EVIDENCE: docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33,
docs/reference/adapter-contract.md:81, schemas/lenny-adapter.proto:309,437,445,452,
pkg/adapter/server.go:169, pkg/adapter/sessionscrubreporter.go:13,
pkg/adapter/gatewaycontrol/scrubreport.go:13,72,
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:38,67,196,220,
pkg/gateway/session/recycle/scrubreporter_seams.go:245, pkg/agentpodstate/agentpodstate.go:60,128,
migrations/0167_runtime_definitions_execution_mode_service.up.sql:102,
tests/tier11_docs/spec_28_register_writers_test.go:101,
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173,
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6,21,
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:453 (the `// diagnosis:` on
`TestPerSlotCleanupStatedOnEverySessionModeRow` at :462),
tests/tier4_integration/concurrent_delegation_proxy_test.go:49,122,
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go:422,444,455.

USEFUL [the verbatim-anchor sweep is DONE ...]: the python "extract every fenced block, count
occurrences across glob('spec/*.md')" recipe re-ran clean in one command. All 29 quoted-original
blocks hit exactly once; the zero-hit blocks are exactly the replacements plus the two new fence
entries, the SPEC-4 `claimed ──→ draining` replacement and the two §16.1 rows. Re-running it costs
about ten seconds and it is the cheapest way to clear the whole anchor class.

FACT: the inline (non-fenced) quotes in SPEC-4 need their own check, because the fenced-block sweep
does not see them. All of them verify except one, and that one verifies by design: the proposal's
"which then reads \"and a terminal claim disposition (`released` or `failed`) projects `draining`
and then `terminated`\"" is the POST-deletion text, not a quote of the current spec.
EVIDENCE: spec/06_warm-pod-model.md:80 (the full pre-deletion clause), spec/04_system-components.md:414-415.

WATCHOUT: `§12.6` is headed "Interface Design", not "agent_pod_state table schema". The
`sessions_served` prose and DDL sit at spec/12_storage-architecture.md:481 and :494, inside §12.6
(which runs 369-766). The proposal's "§12.6 (`agent_pod_state` table schema)" is a bolded
sub-block name inside the section rather than the heading; do not file it as a wrong section.
EVIDENCE: spec/12_storage-architecture.md:369,481,494.

FACT: `§5.2`'s `**Session count limit:**` bullet really does own BOTH halves of the evaluation point
the §12.6 read clause gave up (single-session pool at the recycle disposition, and the per-release
drain on a concurrent non-vm-restart pool), so the round-10 reduction to a pointer loses nothing.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488.

FACT: `ScrubReporter` (capital S) is a real concrete type in the leasecontrol package and its
`RecordSessionScrub` is the increment site; the interface beside it is `ScrubReportService`. The
proposal's "`ScrubReporter.RecordSessionScrub` (`.../scrubreport_server.go`) increments
`sessions_served`" is exact, not a near-miss on the interface name.
EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:378,441,451.

### [spec.12.review-client-surface.1]

DECISION: returned EMPTY for the client-facing surface lens on the round-12 staging (spec-changes.md as of commit 9c59dabec) — BECAUSE every client-facing spec surface the staging touches re-verified clean and every parallel representation outside spec/ is already named by a deliverable — ALTERNATIVES: filing the docs/proto/SDK mirrors as (d) missing-edit-site findings, rejected because the loop scope restricts findings to ones whose fix lands in spec-changes.md, and each mirror is already staged (DOCS-2/3/4, SCHEMA-1, CODE-9).

FACT: every verbatim anchor the staging quotes for a client-facing section re-verifies word for word. §15.1 `SETUP_COMMAND_FAILED` row (all four replaced sentences) — EVIDENCE: spec/15_external-api-surface.md:1136. §4.7 `Shutdown`/`DemoteSDK`/`ReportSessionScrub` rows — EVIDENCE: spec/04_system-components.md:686, :674, :692. §5.2 anchors — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453, :545, :561. §7.2 preamble/step 3, §7.3 list item 4, §4.7.9 step 5, §6.2 cancel-mid-resume clause — EVIDENCE: spec/07_session-lifecycle.md:210,214,414; spec/04_system-components.md:854; spec/06_warm-pod-model.md:234.

FACT: the OpenAPI document the lens brief names as `pkg/gateway/openapi/openapi.json` does not exist at that path; the served document is `pkg/gateway/externalapi/openapi/openapi.json`, and it contains NO error-code strings at all (no `SETUP_COMMAND_FAILED`, no `SESSION_CREATION_FAILED`, no `STARTING_FAILED`). Widening a §15.1 catalog row therefore owes no OpenAPI edit. — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json (4301 lines, grep for the four codes returns nothing).

FACT: no file under `sdks/` references `ShutdownRequest`, `lenny-adapter`, `ReportSessionScrub` or `slot_reclaim`, so the adapter-wire changes reach no language SDK. This re-confirms standing-context entry "No OpenAPI document and no language SDK mirrors any edited surface" for the post-redesign staging. — EVIDENCE: `grep -rln` over sdks/ returns empty.

FACT: `mid_session` exists on ONE proto message today, `FinalizeWorkspaceRequest` field 4; the §4.7.1 carriage table also puts it on `PrepareWorkspace`, which is a new field SCHEMA-1 adds. No spec/ or docs/ site names the `mid_session` wire field at all (the spec speaks only of `capabilities.midSessionUpload` and §7.4 prose), so adding it to a second message falsifies no prose site. — EVIDENCE: schemas/lenny-adapter.proto:713-724; grep of spec/ for `mid_session` returns nothing.

FACT: the §4.7.1 carriage table's RPC names all exist in the §4.7 tables, and `PrepareWorkspace` is the only client-streaming RPC on the contract, so rule 9's first-frame scoping names the right RPC. — EVIDENCE: schemas/lenny-adapter.proto:41 (`rpc PrepareWorkspace(stream ...)`), :48, :55, :62, :72, :89, :151; spec/04_system-components.md:667-686.

FACT: the `SESSION_SCRUB_OUTCOME_RELEASED` proto comment says the slot's runtime, credential timers and directory tree "were torn down cleanly", which the staged §5.2 disposition table row "runtime close succeeds and any other act fails → report `released`" makes false. It is NOT a spec-lane finding: SCHEMA-1's own heading already claims "the scrub-outcome and report-trigger comments", and the enum's `runs on every session release` half is correctly excluded by the carrier table's exclusion paragraph. A later loop should confirm SCHEMA-1's body actually replaces the RELEASED value comment and not only the RPC/Request comments. — EVIDENCE: schemas/lenny-adapter.proto:436-448; spec-changes.md:651; spec-changes.md:608-612.

WATCHOUT: the §15.1 row edit looks like it should ripple to the four other §15.1 sentences that map a deterministic non-zero setup command to `SETUP_COMMAND_FAILED`. It does not. Each states a sufficient condition ("a deterministic non-zero setup command surfaces as ..."), which the widening leaves true, so none is a missed edit site. — EVIDENCE: spec/15_external-api-surface.md:625, :646, :647, :650, :720.

WATCHOUT: spec §15.1's error catalog table has four columns and no remedy column; only `docs/reference/error-catalog.md` has one. A reviewer who assumes the two tables are column-parallel will invent a missing spec remedy edit. DOCS-3 owns the remedy cell. — EVIDENCE: spec/15_external-api-surface.md:1136 vs docs/reference/error-catalog.md:129.

UNVERIFIED: whether `docs/runtime-author-guide/` needs a mirror of §15.4's two new published blocks (bind attempt token contract, slot-identifier reclaim hold). No deliverable names that directory; only `docs/reference/adapter-contract.md` (DOCS-2) is staged. The remedy is a docs edit, so it is the non-spec loop's to settle, not this one's.

### [spec.12.review-docs-alignment.1]

DECISION: filed exactly the two accepted-residue findings (gateway-crash-stranded entry, self-recreated
entry after an unconditional teardown) and nothing else — BECAUSE the mechanical check still reproduces
zero: `awk 'NR>=243' spec-changes.md | grep -n "crash\|recreat\|empty workspace\|unstartable\|stranded"`
returns only the two §15.1 `SETUP_COMMAND_FAILED` exclusion sentences, so neither residue has landing
spec text, while rule 13 carries the untokened-entry residue's in spec-changes.md:1065 — ALTERNATIVES:
refiling the three review-log DEFERRED docs corrections (troubleshooting.md `setup_command_failed`,
gateway-replica-failure.md benign-crash narrative, DOCS-3's age claim), rejected because each remedy is
a docs or non-spec file this loop may not edit and the between-loops pass owns them; filing
`docs/reference/adapter-contract.md:84` ("Scrub responsibilities") as a 25th carrier-table row, rejected
below.

CORRECTS [spec.11.review-docs-alignment.1]: its ground for dropping the two accepted residues — "rejected
on the standing-context entry that their remedy was ONE SENTENCE EACH in spec-changes.md's Edge-cases
section" — misreads that entry. The Edge-cases section of spec-changes.md is the proposal's own reasoning
and lands nothing in `spec/`; the standing-context trap names the Edge-cases section as WHERE the two
residues are currently recorded, and the remedy it prescribes is one sentence each of LANDING text, which
is what rule 13 already gives the third residue. Round 11 therefore returned empty on a misreading, and
the residues are unfixed after five filings.

FACT: `docs/reference/adapter-contract.md:84` is NOT a carrier of the withdrawn "reports on every session
release" universal, although it reads like one at a glance. Its action list (credential purge, deployer
`cleanupCommands`, the scrub) is the WHOLE-POD scrub's list, reported via `ReportPodScrub`, which this
proposal does not condition; "at each session end" attaches to "Your runtime exits", not to "reports".
The carrier table's 24 rows stand. EVIDENCE: docs/reference/adapter-contract.md:84;
docs/reference/glossary.md:302 (same three-item list glossed as the whole-pod scrub)

FACT: the whole staged docs-mirror closing paragraph re-verifies clean against the tree this round, on
every page it names: `docs/reference/state-machines.md:138` (projection prose with the two claim-deletion
clauses), `:237` (`slot_cleanup → released` trigger), `:251` (`slot_cleanup -> leaked` cleanup-timeout
clause), `docs/reference/adapter-contract.md:64` `DemoteSDK`, `:75` `Shutdown`, `:81`
`ReportSessionScrub`, `docs/reference/execution-modes.md:68`, `docs/operator-guide/security-principles.md:33`,
`docs/reference/error-catalog.md:129` (four sentences plus the remedy cell). Nothing on those pages is
left describing superseded behaviour by a deliverable that does not name it.

USEFUL [spec.7.review-docs-alignment.1]: its verbatim page-by-page check of the closing docs-mirror
paragraph is still accurate and saved re-deriving every page; only the line numbers needed re-confirming.

### [spec.12.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site-completeness lens on round 12 — BECAUSE every identifier the staging adds, changes or retires was swept against `spec/`, `docs/`, `schemas/`, `charts/`, `migrations/` and `tests/`, and every surface that goes wrong is already either a staged spec edit, a named non-spec deliverable (DOCS-1..4, SCHEMA-1, CODE-1/9), or a row in the SPEC-3 carrier table — ALTERNATIVES considered and rejected: (a) §16.1.1's `k8s_pod_name` "Used on" column, which names "slot failure and replacement metrics" and not the two SPEC-6 series — the column is prose-descriptive, no gate reads it, and it is already an UNVERIFIED in the standing context; (b) `docs/reference/adapter-contract.md:84` `**Scrub responsibilities.**` ("the adapter runs the credential purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") as a missing carrier-table row — that sentence describes the whole-pod scrub (`ReportPodScrub`), not the per-slot report, so the withdrawn universal is not carried there; (c) the spec-changes.md DOCS-1 summary sentence ("what §4.6.1's re-keyed claim-deletion bullets state"), which reads as omitting the unqualified no-claim clause — non-spec DOCS-1 in fact replaces all four clauses, so the summary is loose rather than wrong.

FACT: the SPEC-3 carrier table is COMPLETE against a whole-tree sweep. `grep -rn "on every session release\|at each session release\|on each session release\|at every session release" pkg/ tests/ migrations/ schemas/ spec/ docs/` returns exactly the table's 24 carriers plus the sites the table's own exclusion paragraph names (spec/06:24 §6.1, spec/07:72 §7.1 `scrubPolicy` row, `schemas/lenny-adapter.proto:437` + its generated copy `pkg/proto/adapter/v1/lenny-adapter.pb.go:42`, `pkg/adapter/gatewaycontrol/scrubreport.go:13`, `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:473`). Each excluded site states only that the CLEANUP runs on every release, never the report. Do not re-run this sweep — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:589-616

FACT: the two carrier-table attributions I spot-checked are exact. `pkg/adapter/server.go:168-176` ("emits the §5.2 per-slot cleanup outcome to the gateway on every session release via ReportSessionScrub") and `pkg/adapter/sessionscrubreporter.go:12-21` ("emits the §5.2 per-slot cleanup outcome to the gateway on every session release") both carry the withdrawn universal verbatim, as the table's CODE-1 and deferred rows say — EVIDENCE: pkg/adapter/server.go:169, pkg/adapter/sessionscrubreporter.go:13

FACT: the retired `leaked` trigger "cleanup timeout exceeded" has exactly FOUR carriers in the tree and both spec ones are staged. `spec/05_runtime-registry-and-pool-model.md:561` (SPEC-3's `**Whole-pod replacement trigger:**` parenthetical) and `spec/06_warm-pod-model.md:148` (SPEC-4's `slot_cleanup ──→ leaked` fence entry); the other two are `pkg/gateway/runtime/slothealth/slothealth.go:33` and `pkg/sandbox/slotstate/slotstate.go:104`, both already Deferred to the code lane. `docs/reference/state-machines.md:251` carries the prose form and is DOCS-1's — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561, spec/06_warm-pod-model.md:148, docs/reference/state-machines.md:251

FACT: "Spec files touched" still matches the staged headings one for one after the prune (`9c23121af`) and the redesign (`9c59dabec`): spec/04 seven edits (§4.1, §4.6.1, §4.7 `Shutdown`, §4.7 `DemoteSDK`, §4.7 `ReportSessionScrub`, §4.7.1, §4.7.9), spec/05 one, spec/06 five, spec/07 three, spec/12 one, spec/15 two, spec/16 one, spec/29 one. Nothing staged is unlisted and nothing listed is unstaged — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1260-1310

FACT: the §12.6 anchors all still match byte for byte after the redesign moved the read-clause REPLACEMENT text (the anchors themselves were not touched): prose write clause and read clause at `spec/12_storage-architecture.md:481`, DDL comment at `:494`. The DDL line's `§` is a real U+00A7 in both the tree and the staged block — EVIDENCE: spec/12_storage-architecture.md:481,494

FACT: `**Session count limit:**` (`spec/05_runtime-registry-and-pool-model.md:488`) sits under `**Pod retirement policy (recycling pools).**` and is NOT scoped to `maxConcurrentSessions > 1`; it states the evaluation point for the single-session pool and the concurrent pool alike. That is what makes SPEC-3's reduction of both §12.6 read clauses to a citation of that bullet lossless. A future round tempted to file "the citation drops the single-session half" dies here — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488

FACT: adding two rows to §16.1 turns no gate red, including the hand-transcribed one. `spec161Metrics` in `pkg/observability/metrics/catalog_test.go:15` is iterated in both directions against `MetricCatalog()` and never against the §16.1 table text, so a §16.1 row with no catalog entry passes. The list goes red only when CODE-9 registers `lenny_slot_compensation_superseded_total` without adding it there — EVIDENCE: pkg/observability/metrics/catalog_test.go:193,204

FACT: no alert rule and no runbook page names any `lenny_slot_*` series. `grep -rn "lenny_slot_" pkg/alerting/ docs/runbooks/ charts/` is empty, so the two new counters owe no `docs/runbooks/` companion and no alert-catalog row; `docs/reference/metrics.md:166-167` is the only reader-facing mirror and CODE-9 owns it — EVIDENCE: docs/reference/metrics.md:166

FACT: §6.1's `preConnect` compatibility table admits `preConnect` only at `maxConcurrentSessions: 1` (`session, maxConcurrentSessions > 1` is "Not supported"), so the staged §4.7 `DemoteSDK` row's "the entry it removes is the registry's single entry" citation of §6.1 is sound — EVIDENCE: spec/06_warm-pod-model.md:68,74

USEFUL [spec.11 lenses / standing context]: the two "MISTAKE nearly filed" entries on the §15.1 endpoint precondition rows and on `docs/reference/configuration.md:91` saved me two filings; `spec/15_external-api-surface.md:625,720` and `spec/25_agent-operability.md:2850,2898` all state the `SETUP_COMMAND_FAILED` cause as sufficient rather than exhaustive, so SPEC-5's widening leaves them true.

### [spec.12.review-fresh.1]
DECISION: returned EMPTY — BECAUSE every candidate my fresh-holistic pass generated resolved clean against the tree, sat on the refuted/standing list, or was rationale-only prose whose defect does not change the applied spec — ALTERNATIVES: filing the two borderline items below (rejected on the "speculative finding is worse than no finding" bar, reasons recorded so a later round need not re-derive them).
FACT: the verbatim-anchor sweep re-ran clean at round 12, post-`9c59dabec`. A python script that pulls every fenced block out of spec-changes.md and counts it across `glob('spec/*.md')` gives exactly one hit for every original-text block and zero for every replacement; the four inline (unfenced) SPEC-4 anchors also hit exactly once. Nothing moved under the redesign — EVIDENCE: spec/04_system-components.md:157,:409,:416,:692; spec/05_runtime-registry-and-pool-model.md:453,:545,:561; spec/06_warm-pod-model.md:80,:148,:155; spec/12_storage-architecture.md:481; spec/15_external-api-surface.md:1469
CORRECTS [standing-context "MISTAKE nearly filed, round 11: SPEC-4's rationale puts quotation marks around a sentence that is NOT in `occupancy.go`"]: the quoted sentence IS in the file, verbatim, in the `ProjectOccupancyPhase` doc comment: "the §6.2 state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim DELETE." The round-11 shard compared it against a DIFFERENT comment 50 lines lower ("The phase the pod sits in at the DELETE selects the edge, and the §6.2 state machine constrains it"), which is a second, separate sentence. Conclusion is unchanged (not a finding) but the reason is now "the citation is exact", not "a loose paraphrase judged below the bar" — EVIDENCE: pkg/controller/warmpool/occupancy.go:57-60 and :112-118
FACT: the §16.1 catalog table is TWO columns (`| <long description> | Counter |`), so SPEC-6's two staged rows match the table's arity; the sibling adapter-side rows sit at spec/16_observability.md:188-189 and the session-slot failure row SPEC-6 names ("beside the session-slot failure count") is real — EVIDENCE: spec/16_observability.md:14,:188
FACT: the SPEC-3 carrier table is complete against a fresh independent grep. A regex over `spec/ docs/ schemas/ migrations/` for a report predicate within 120 chars of "every/each/at a/on a session release" returns exactly six live carriers, all six already rows in the table (spec/05:453, spec/04:692, spec/12:481, docs/reference/adapter-contract.md:81, docs/operator-guide/security-principles.md:33, docs/reference/execution-modes.md:68, schemas/lenny-adapter.proto:452). No seventh carrier exists in those trees — EVIDENCE: 0081...spec-changes.md:614-639
FACT: `docs/reference/state-machines.md`'s pod-transition TABLE is not a missed DOCS-1 site. Its `claimed → draining` row's trigger list names only the recycle-disposition retire reasons and never claim deletion, so SPEC-4's re-key leaves it true; only the page's prose paragraph at :138 carries the claim-deletion partition, and DOCS-1 already owns that paragraph — EVIDENCE: docs/reference/state-machines.md:138,:220
FACT: the two new action-list acts in SPEC-3's first anchor are both shipped, re-verified at the line level. `slotlayout.RemoveTree` removes `p.CredentialsDir` among its four directories, and `deregisterSlotLocked` cancels every armed provider timer before `delete(s.slots, …)`, so the action list adds no obligation — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69; pkg/adapter/slotsession.go:174-189
WATCHOUT: two borderline items I deliberately did NOT file; a later round that wants either owes the argument, not a re-derivation. (1) The §5.2 disposition table's "Clean-exit flag on the `Shutdown` response" column reads "No `Shutdown` performs a cleanup" on the three rows where none does, while §4.7.1 rule 15 says a `superseded` or `absent` response DOES report a clean exit. The column is keyed on the cleanup rather than on the response, so the two are about different objects; filing it owes a reader who is actually misled. (2) The Edge-cases bullet "A retry that meets the reclaim hold spends an attempt on it" opens "The callers that reach the hold are" three callers and then names a fourth (a client retry of the §15.1 start on a create-time-reserved slot) fifteen lines later. It is rationale prose in spec-changes.md that lands in no spec file, so applying the edits is unaffected — EVIDENCE: 0081...spec-changes.md:647-657,:1067; :110-127

### [spec.12.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on the spec staging — BECAUSE the only CRD-touching deliverable is SPEC-4 and every one of its claims re-verified against the tree this round; the rest of the staging (SPEC-1/2/3/5/6) is adapter-internal registry state and gRPC, touches no apiserver object, adds no finalizer, no status write, no admission-webhook behaviour, and puts no reconcile on a synchronous request path — ALTERNATIVES: re-filing the §5.2 last-row conditionality hazard (already UNVERIFIED at review-log.md:1516 and declined by this same lens in round 4) and the §4.6.1 first bullet's warm-inventory/no-claim clause (pre-existing, untouched by the staging, same hazard family); both rejected.

FACT (re-verified this round, so a later lens need not redo it): §4.6.3 gives `Sandbox` `status.*` to the WarmPoolController as "Sole writer of phase and conditions", so SPEC-4's read-back parenthetical is sound — EVIDENCE: spec/04_system-components.md:618.

FACT: no `ForceOwnership` anywhere in `pkg/controller/warmpool/`, and every status patch in that package — both reconciler arms, the GC, and the occupancy projection — uses `client.FieldOwner(string(ownership.WarmPoolController))`, so the two-managers-racing-one-field dress has no purchase — EVIDENCE: pkg/controller/warmpool/occupancy.go:268, controller.go:752,:852, pod_reconciler.go:345,:691, gc.go:466.

FACT: `failPhase` really does reach a claim DELETE rather than a terminal-disposition patch (`failPhase` → `drain` → `podclaim.DeleteClaim`), which is what SPEC-3's §5.2 last row and the §139-145 edge-case bullet assert — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082 and :1200-1202.

FACT: `ProjectOccupancyPhase`'s no-claim arm switches on `o.Current` with `Reserved → Idle`, `Claimed → Draining`, and `("", false)` for everything else, so SPEC-4's re-keyed §4.6.1 bullets transcribe the controller. The re-keying is convergent on an owned watch (`claimed`+no claim writes `draining`; the re-triggered reconcile reads `draining`+no claim and declines), so there is no status-write-triggers-reconcile loop — EVIDENCE: pkg/controller/warmpool/occupancy.go:126-143.

USEFUL [spec.4.review-kubernetes.1]: its convergence argument and its FACT list were exactly right and saved this round the whole derivation; the only work left was confirming nothing in commits 9c23121af and 9c59dabec (§15.4 reduction, SPEC-3 carrier table, disposition-row merges, line-citation removal) reaches a Kubernetes surface, which none of them does.

WATCHOUT: the §6.2 fence carries TWO `claimed ──→ draining` entries — one in the `Occupancy projection` group (spec/06_warm-pod-model.md:95-97) and one in the `Recycle edges` group (:115-119). SPEC-4 replaces only the first and says so; a lens grepping for the arrow will hit the wrong one — EVIDENCE: spec/06_warm-pod-model.md:95,:115.

### [spec.12.review-mechanism.1]

DECISION: filed exactly one finding, on the §15.4 `**Bind attempt token contract:**` block's clause
"the numbered rules an adapter applies to a request that carries it" — BECAUSE it narrows the
normative pointer to token-carrying requests while §4.7.1's own domain declaration
(spec-changes.md:1044) puts `StartSession` and `ConfigureWorkspace` (carriage table
spec-changes.md:1034-1035: "not carried") inside rules 1-9, and rule 8 — the one NEW adapter
obligation whose enforcement argument is "§15.4 publishes non-conformance against the rule"
(spec-changes.md:150-152, 1088-1091) — is required "of every request that starts a session,
including one that carries no token of its own". The remedy is a three-word DELETION, which is the
remedy shape this run's directive prefers. ALTERNATIVES: rejected filing it as a §15.4
completeness gap (refuted six times, and the fix would add a sentence); rejected filing the
conformance criterion (it is the settled fifth form and quantifies correctly over requests).

FACT: the §15.4 clause was narrowed by the hand redesign `9c59dabec`. The pre-redesign form read
"the numbered rules an adapter applies to it"; the redesign rewrote it to "to a request that
carries it", making the token-only scope explicit. EVIDENCE: git diff 9c23121af~1..HEAD on
0081_...spec-changes.md, hunk @@ -1047,31 +1100,22 @@.

FACT: `**Session count limit:**` (spec/05_runtime-registry-and-pool-model.md:488) sits under
`**Pod retirement policy (recycling pools).**`, NOT under the `maxConcurrentSessions > 1` heading,
so SPEC-3's §12.6 read-clause pointer at that bullet loses neither the single-session nor the
concurrent case. I checked this specifically because the sibling `**Slot cleanup:**` bullet (:545)
IS concurrency-scoped and the two look alike. EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:488 vs :545.

FACT: the merged disposition table still partitions. The only performer that closes the runtime
BEFORE deregistering is the SDK demotion (own row 8); `releaseSessionSlot` and the hold-timeout
pass both deregister first, so rows 6/7's key "an act fails after the deregistration" is total over
the outside-`Shutdown` cases. EVIDENCE: spec-changes.md:654-656; pkg/adapter/sdkwarm.go
`Server.DemoteSDK`, pkg/adapter/holdstate.go `onHoldTimeout`.

FACT: `SocketRuntimeProcess.Close` IS session-aware (`releaseActiveLocked(sessionID)`, leaves the
shared connection up while a sibling is active) while `MCPRuntime.Close(ctx, _ string)` ignores the
session id entirely. This is the tree half of the standing Open "does 'takes the session back off
the shared runtime process' hold for every runtime implementation". Not filed: the shipped
`Shutdown` already calls the same `Runtime.Close` at an ordinary session end, so rule 8's undo adds
no new co-tenant hazard on the MCP runtime. EVIDENCE: pkg/adapter/socketruntime.go:435-467,
pkg/adapter/mcpruntime.go:266.

WATCHOUT: rules 1-9 do NOT reach `Attach`, `Interrupt`, `Checkpoint` etc. — if they did, rule 4
would have each of them CREATE a registry entry. The only thing holding that closed is the
`**Admission.**` preamble's closing sentence ("Every other RPC ... is outside these rules, apart
from the reclaim hold of rule 2"). Any fix that touches that sentence must keep the carve-out.
EVIDENCE: spec-changes.md:1044, rule 4 at spec-changes.md:1051.

UNVERIFIED: rule 8's untokened arm across a replacement (standing Open). I traced one candidate
interleaving on the SDK-warm path — untokened entry E1 claimed by `ConfigureWorkspace`,
unconditional `Shutdown` removes E1 during `Runtime.Start`, a repeat `ConfigureWorkspace` (rule 6
exempt) creates untokened E2, and rule 8's confirm compares "" to "" and records against E2 — but
could not establish that the gateway ever issues the second `ConfigureWorkspace` inside that
window, so I did not file it. Whoever lands CODE-2/CODE-6 should settle it.

FACT (negative, saved re-derivation): §6.1 does state "SDK-warm mode (`preConnect: true`) is
admitted only in session mode with `maxConcurrentSessions: 1`", so the staged `DemoteSDK` row's
"the entry it removes is the registry's single entry" holds. EVIDENCE:
spec/06_warm-pod-model.md:69.

USEFUL [the standing context's "First command of any round" note]: diffing spec-changes.md across
`9c23121af~1..HEAD` reduced this pass to the ~40 changed hunks plus a whole-file mechanism read,
and it is what surfaced the §15.4 clause. A mechanism lens last ran in round 10, so that range is
exactly its delta.

### [spec.12.review-performance.1]

DECISION: returned EMPTY for the performance / scalability / failure-mode lens on the round-12
staging — BECAUSE the staged spec edits create no net-new control-plane or data-plane write on
any path, and the one write-rate change they do make is a REDUCTION —
ALTERNATIVES: I considered and rejected filing (a) the reclaim hold as an availability
bottleneck, (b) the withheld pre-`running` report as relaxing `maxSessionsPerPod`, (c) SPEC-4's
re-key as new pod churn, (d) `k8s_pod_name` on the two new §16.1 counters as a cardinality
regression, and (e) the gateway-crash stranded-entry residue as a reliability regression against
the shipped design. (a)-(c) and (e) are recorded refuted or accepted in the standing context;
(d) is refuted below on precedent.

FACT: the write-rate arithmetic, done once so a later round need not redo it. The staging adds
ZERO etcd writes: the gateway writes no `Sandbox.status` (§4.6.3 keeps the WarmPoolController
sole writer, and `ProjectOccupancyPhase` SSA-patches only on a change), and SPEC-4 re-keys the
projection onto inputs `ProjectOccupancyPhase` already reads. It adds ZERO Redis operations: the
§7.1 reservation release is the shipped `SlotClaimer.ReleaseSlot` path. Its only Postgres-rate
change is SPEC-3's report biconditional, which REMOVES `sessions_served` increments — one per
pre-`running` cleanup, per SDK demotion, per hold-timeout termination — so the `agent_pod_state`
write rate at the top tier strictly decreases. The one added per-unit-of-work item is the §7.1
compensating `Shutdown`: one extra gateway→adapter RPC per FAILED bind attempt only, bounded by
`maxSlotRetries = 1` (`pkg/gateway/sessionserver/start.go:2720`), on a channel that already
carries five to eight RPCs per successful bind. EVIDENCE: pkg/controller/warmpool/occupancy.go:84-143;
pkg/gateway/podlifecycle/podsession/slotbinder.go:528-560; pkg/gateway/sessionserver/start.go:2720

FACT: the two new §16.1 counters carry no cardinality regression. `lenny_slot_failure_total` and
`lenny_slot_pod_replacement_total` already carry `pool` + `k8s_pod_name`, and §16.1's own
attribute table lists `k8s_pod_name` as in use for "slot failure and replacement metrics", so
`lenny_slot_compensation_superseded_total` reuses an established label set rather than opening a
new per-pod series family. EVIDENCE: spec/16_observability.md:14, :15, :297

FACT: nothing the staging relies on is store-backed, which is why the §12.4 durable-fallback half
of this lens comes back empty. The `bind_attempt` token is minted in gateway process memory before
the attempt's first pod-side RPC and never round-trips through Postgres, Redis or a response; the
reclaim hold is adapter process memory. A Postgres failover or a Redis reset therefore cannot
lose either, and neither fencing mechanism degrades during a coordinator handoff. The cost of that
choice is the gateway-crash stranded entry, which the staging already records as an accepted
failure mode with two named out-of-scope recoveries. EVIDENCE:
proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:1021, :201-212

FACT: `sessions_served` read-clause citation re-verifies. SPEC-3's replacement
("evaluated against `recycle.maxSessionsPerPod` on the terms the **Session count limit:** bullet
states") resolves to a bullet that exists and does state the evaluation point for both pool kinds
plus the `vm-restart` carve-out. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488

USEFUL [Standing context, "Do NOT re-derive the capacity family"]: it named five dresses
(hold refusal against the unhealthy threshold, the §10.1 fence blocking a compensation,
Redis-reset occupancy loss, the fresh-workspace guarantee versus the hold, the §10.1.4
N-identifier hold) that are exactly where this lens lands first. It saved a full re-derivation.
Pair it with "**The hold is keyed on the slot identifier, which IS the session identifier**",
which is the one-line answer to every remaining hold-contention dress: N concurrent cleanups
refuse N distinct identifiers and never each other.

USEFUL [Standing context, "**`UnhealthyThreshold` is `(maxConcurrent+1)/2`...**"]: it confirms the
two quantified claims in the Edge-cases section (one leak trips the threshold at
`maxConcurrentSessions: 2` and does not above it) without re-reading `slothealth`.

WATCHOUT: the hold's unbounded tail is real but is NOT this proposal's to file. The staged §5.2
hold paragraph bounds only the runtime CLOSE (by the request's graceful window or its deadline);
`removeSlotTree` takes no `context.Context`, so the hold outlasts the bounded close by an
unbounded directory removal while the `Shutdown` handler blocks on it. That is shipped and
pre-existing, and the availability cost of a retry meeting the hold is already an accepted failure
mode in the spec file's Edge-cases section. Do not re-file it as a new bottleneck.

### [spec.12.review-reliability.1]

DECISION: filed exactly ONE finding, on the two `leaked`-Entered rows of the §5.2 disposition
table naming the whole-pod scrub as an ender of the slot's directories — BECAUSE a `leaked` slot
pins occupancy above zero for the life of the pod (spec/05:395, :488; spec/06:160;
slotclaimer.go:801,:830) and the whole-pod scrub runs only at occupancy zero (spec/05:453, and the
staged scrub-model sentence in the same appended block), so the cell names a reclaimer that cannot
run in the state the row describes; the remedy is a three-word deletion in two cells.
ALTERNATIVES rejected: (a) rule 8's "takes the session back off the shared runtime process" tearing
down co-tenants — the shipped `Shutdown` runtime teardown has the same width, so it is the
pre-existing-looseness family; (b) the double-book of a leak for row 2 (`leaked` report AND unclean
response into one `slothealth.Tracker`) — `Tracker.RecordLeak` is a bare per-pod increment with no
session dedup (pkg/gateway/runtime/slothealth/slothealth.go:120-123), but the spec says a slot
transitions to `leaked` once, so the defect, if any, is code-lane and out of this loop's scope;
(c) the unbounded `removeSlotTree` holding the identifier — already a recorded FACT and an Open.

FACT: the "leaked blocks the whole-pod scrub" chain is stated in the SHIPPED spec in three places,
so no derivation is needed: a leaked slot is "retained until the pod terminates and still count[s]
against pod occupancy" (spec/05_runtime-registry-and-pool-model.md:395), §5.2's own session-count
bullet says the concurrent drain is "decoupled from the whole-pod scrub because a persistently
`leaked` slot can hold total occupancy above zero indefinitely" (:488), and the code's own doc
comment says "A pod carrying a leaked slot is by definition not at occupancy zero" — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:395,:453,:488; spec/06_warm-pod-model.md:160;
pkg/gateway/podlifecycle/podclaim/slotclaimer.go:801,:830

CORRECTS [archive WATCHOUT at review-log-archive.md:8678, "the whole-pod scrub is NOT a backstop …
I did NOT file it"]: that non-filing was against a SPEC-3 *rationale* sentence about the credential
purge, and its grounds (pre-existing, strictly improving) are right for that sentence. They do not
carry to the staged disposition TABLE, which did not exist then: the table is declared the single
home of every cleanup's disposition and has a dedicated "What ends the state left on the pod"
column, so a cell naming an unreachable mechanism is a positive false statement in the one site
every other section is told to cite. The same applies to the UNVERIFIED at
review-log-archive.md:34398, which asked whether the staged sentence should read "whichever comes
first"; the answer for the two `leaked` rows is simpler, since the scrub never comes at all.

WATCHOUT: rows 3, 6, 7, 8 and 9 of the table carry the SAME "The whole-pod scrub or pod
termination …" cell and are CORRECT, because their `leaked` column reads "Not entered", so
occupancy is released and the scrub is reachable. Only the two rows whose `leaked` column reads
"Entered" (spec-changes.md:650 and :653) are wrong. A fix that sweeps the phrase out of every row
breaks five correct cells. — EVIDENCE:
0081_…spec-changes.md:649-657

FACT (negative, re-verified this round, saves a sweep): `slotlayout.RemoveTree` does remove the
credential directory (`p.CredentialsDir` is one of its four paths) and `deregisterSlotLocked` does
cancel every armed expiry timer before `delete(s.slots, …)`, so SPEC-3's claim that both added
action-list items are shipped behaviour rather than new obligations holds. — EVIDENCE:
pkg/adapter/slotlayout/tree.go:58-69; pkg/adapter/slotsession.go:174-189

FACT: `Tracker.RecordLeak(pod)` is `t.leaked[pod]++` with no session key, and `RecordFailure` is
likewise pod-keyed, so nothing on the gateway side dedups two leak signals for one slot. Row 2 of
the disposition table emits both a `leaked` cleanup-outcome report and an unclean `Shutdown`
response for the same slot. Whoever owns the code lane should check the two call paths converge on
one `MarkLeaked`. — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:99-123

UNVERIFIED: whether the two leak signals of row 2 actually reach `RecordLeak` twice (the gateway
may take the report path and the response path in one place). The code lane should settle it; it is
out of the spec loop's scope either way.

### [spec.12.review-security.1]

DECISION: returned an EMPTY findings list for the security lens on the round-12 staging — BECAUSE every
security-shaped reading I could construct against the staged spec edits either verified true against the
tree or is already a refuted/accepted item the standing context names — ALTERNATIVES: I built and
discarded six candidates, listed below so nobody rebuilds them.

FACT: the two shipped-behaviour citations in the staged §5.2 `**Slot cleanup:**` action-list replacement
are BOTH true, so the added credential-purge and timer-cancel acts are a record rather than a new
obligation. `slotlayout.RemoveTree` removes `p.CredentialsDir` (`/run/lenny/slots/{sessionId}`) as one of
its four best-effort trees and returns only the FIRST error — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-70
(loop over `p.slotRoot(), p.Sessions, p.Artifacts, p.CredentialsDir`, `firstErr`). `deregisterSlotLocked`
cancels every armed timer before `delete(s.slots, …)` — EVIDENCE: pkg/adapter/slotsession.go:174-181. The
timers are §4.9 DIRECT-delivery-mode only, which is what the staged sentence says — EVIDENCE:
pkg/adapter/slotcreds.go:215-222 (`if leaseDeliveryMode(lease) != string(directDeliveryMode) || ms <= 0 {
cancel; return }`).

FACT: the whole safety argument for `mid_session` — "the gateway issues one only for a session the issuing
replica holds a live binding for" (spec-changes.md:1038) — is TRUE in the shipped gateway and does not need
to wait for the implementation note at spec-changes.md:1092-1095 to confirm it. The handler refuses with
`TARGET_NOT_READY` unless `s.podRegistry.Get(row.ID)` returns a bind with a non-nil `Adapter` — EVIDENCE:
pkg/gateway/sessionserver/upload_to_session.go:111-121. A lens that wants to attack the `mid_session`
bypass of rules 5 and 6 has to get past this first, and it does not.

FACT: SPEC-4's re-key is security-POSITIVE and matches the controller exactly, including the "sole writer"
parenthetical it adds to the §4.6.1 input enumeration. `ProjectOccupancyPhase` switches on the pod's
current phase alone for the no-claim arm: `state.Reserved → Idle`, `state.Claimed → Draining`, with the
scrub-before-idle invariant named in the comment — EVIDENCE: pkg/controller/warmpool/occupancy.go:126-140
and its doc comment at :56-62. The sole-writer claim is spec-backed twice: "the gateway does not write
`Sandbox.status`" (spec/04_system-components.md:409) and the §4.6.3 ownership row "`Sandbox` | `status.*` |
WarmPoolController | Sole writer of phase and conditions" (spec/04_system-components.md:618). The re-key
strictly widens `draining` (a claim-less `claimed` pod now retires on a pool of EITHER recycle setting), so
it removes no isolation control; tenant pinning is untouched and persists across `reserved → idle`
(spec/04_system-components.md:625, the label-immutability allowlist paragraph).

FACT: the staged §5.2 commentary's claim that the whole-pod credential purge is unchanged and disk-addressed
is true — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461 ("Remove every per-slot credential file
`/run/lenny/slots/{sessionId}/credentials.json` remaining under `/run/lenny/slots/`", MUST, before any
deployer code) and the step-6 stat-check backstop at :471. So a credential file a per-slot cleanup left
behind is collected at the recycle boundary whether or not a registry entry survives.

FACT: the §12.6 read-clause redirect target exists and states an evaluation point for both pool kinds, so
the `maxSessionsPerPod` reuse bound does not lose its home — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:488 (`**Session count limit:**`, single-session arm, concurrent
non-`vm-restart` arm "on the session release that drives the served-session count to `maxSessionsPerPod`",
`vm-restart` carve-out).

WATCHOUT: the failed-process-group-kill residue is NOT reachable through the disposition table's "any other
act fails" row, and a lens that thinks it is will file a wrong finding. The §5.2 action list names the
process-group kill as a cleanup act, but in the tree the kill lives inside `Runtime.Close`
(pkg/adapter/socketruntime.go:412,463,482; pkg/adapter/mcpruntime.go:148,155,302), so a failed kill surfaces
as `closeErr` and lands on the table's "The runtime close fails" row, which reports `leaked`, withholds the
clean-exit flag, and enters the `leaked` sub-state. The benign residue (tree, credential dir, timers) is
what "any other act fails" covers. The two rows are correctly partitioned; the spec-level act list just
does not tell a reader which act sits inside which.

MISTAKE avoided, and the six candidates I built and killed, so the next security lens can skip them:
(1) "the scrub-model biconditional stops `sessions_served` counting a started session the §10.1 hold-timeout
termination releases, relaxing `maxSessionsPerPod`" — dead, the control was never implemented
(`reportSessionScrub` has one caller, the `Shutdown` handler) and the standing context already lists "the
withheld report relaxing `maxSessionsPerPod`" as a refuted dress. (2) "withdrawing the `leaked` universal
for cleanups outside a `Shutdown` removes a whole-pod-replacement control" — dead, shipped handlers discard
those errors and file nothing; the proposal records the tree, and the standing context bars re-keying the
report to make it otherwise. (3) "a permanently held identifier is invisible: no report, no counter, no
alert" — dead, adapter-local and shipped, and `leaked` occupancy and the hold are deliberately separate
objects. (4) "the untokened-entry orphan holds credentials and is accounted clean" — dead, named as an
accepted failure mode at spec-changes.md:172-190 and listed as a refuted security dress. (5) "the
`bind_attempt` token states no length or entropy floor" — dead, the token is a correctness fence between
gateway and adapter, not an authentication secret; the only party who could guess it is the adapter, which
is the enforcer, so guessing buys nothing, and specifying a length would be over-specification. (6) "rule
12's 'whatever entry the adapter holds' is loose addressing for the destructive arm on a concurrent pod" —
dead, `ShutdownRequest` is session-scoped by §4.1 and rule 11 has already resolved the named session's
entry before rule 12 is reached; this is wording, not a defect.

UNVERIFIED: nothing new. Everything I relied on is cited above with a line number I read this round.

### [spec.12.review-single-source.1]

DECISION: returned EMPTY — BECAUSE every rule I inventoried in the staged spec text has exactly one stating site and every other site cites it by heading or rule number — ALTERNATIVES: I worked up four candidates and dropped each below the bar (each is recorded under WATCHOUT/FACT below so a later round does not re-derive them).

FACT: the delta a single-source lens has to cover in round 12 is exactly commit `9c59dabec` (the §15.4 two-sentence rewrite, the SPEC-3 carrier table, the §12.6 read-clause reduction, the trimmed §15.4 commentary) plus `9c23121af`. Round 11's inventory (standing context, "Round 11 inventoried single-sourcing") covered everything else and still holds against the current file. — EVIDENCE: git show 9c59dabec -- proposals/0081_*/0081_*.spec-changes.md

FACT: a mechanical duplicate check over the staged fenced blocks is cheap and comes back clean. Extract every ``` block from spec-changes.md, split into sentences >60 chars, and count repeats: the only four repeats are verbatim-anchor/replacement pairs (spec-changes.md:292/299, :460/466, :544/550, :771/777), i.e. the "reads verbatim" text and its unchanged tail in the replacement. No staged sentence is written twice. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:292,299

WATCHOUT: four candidates that look like (g) and are not; do not spend a round on them again.
  (1) §7.1's "the fence does not depend on the connection, and a reclaim sent on a fresh connection is fenced exactly as one sent on the original" (spec-changes.md:403) versus §4.7.1's "a caller on a fresh connection holds it exactly as the caller on the original connection did" (spec-changes.md:1023). The §7.1 clause is the consequence of §4.7.1's property stated in a clause, and its job is to mark the connection preference as a preference; the commentary at spec-changes.md:419-423 says so. A clause-level consequence is excluded by the lens's own definition of a stating site.
  (2) The §5.2 disposition table's "Cleanup-outcome report" column versus the `**Scrub model.**` biconditional. The table's lead-in reads "what it reports under this paragraph's opening rule" (spec-changes.md:126), so the column applies the rule rather than restating it, and the column cannot be deleted anyway because it carries `released` versus `leaked`, which the biconditional does not fix.
  (3) "A `Shutdown` is governed by rules 10 through 15 in place of them" appears in the `**Admission.**` preamble and again as "Rules 10 through 15 govern a `Shutdown` in place of the admission rules, applied as an ordered cascade on the same terms." Structural signposting, and the second adds the cascade terms; this is the "redundancy other than the restated rule" the lens excludes.
  (4) The §15.4 hold block's two non-conformance sentences versus the bind-token block's request-quantified criterion. The hold block's own normative content is the `ABORTED` status that rule 2 delegates to it, so the block is not reducible to the criterion, and standing context records §15.4 completeness findings as refuted six times.

FACT: the §4.9 direct-delivery lease-expiry timer rule lives at spec/04_system-components.md:1169 and states only the arming and the `AUTH_EXPIRED` firing. It does not state cancellation at slot release, so SPEC-3's new §5.2 action-list clause ("cancels the [Section 4.9] direct-delivery-mode lease-expiry timers armed for that session") is a first statement rather than a copy of §4.9. — EVIDENCE: spec/04_system-components.md:1169

FACT: §7.4 states nothing about the gateway issuing a mid-session upload only for a session the issuing replica holds a live binding for, so the §4.7.1 mid-session paragraph's safety sentence has no second home in shipped spec. — EVIDENCE: spec/07_session-lifecycle.md, §7.4 grepped for "live binding"/"replica" — no hit

UNVERIFIED: the §4.7.1 carriage table and SCHEMA-1's nine-field proto table state the same carriage facts in two deliverables (standing context records they agree field for field). I did not file it: the proto staging must list the fields it adds, and the remedy would land in non-spec-changes.md, outside this loop's scope. If the non-spec loop wants one home, the reduction is SCHEMA-1 citing the §4.7.1 table for which request carries which field and keeping only the field numbers.

USEFUL [Round 11 inventoried single-sourcing and found one home each]: it named the eleven homes explicitly, which let me spend the round on the delta and on independent spot checks instead of rebuilding the inventory. Keep it.
USEFUL [DECISION: §15.4's `**Bind attempt token contract:**` block is TWO sentences...]: it plus the "MISTAKE, FOUR attempts at ONE §15.4 paragraph" trap is what kept candidate (4) off the report.

### [spec.13.fix.1] — corrections appended to round 13's fix pass, not a new pass

The round-13 fixer re-keyed the disposition table's last column from an ender to a residue. The
post-fix review found four defects in that re-key. Each is corrected below with the smallest edit.

- **DECISION: the staged §7.1 paragraph's pointer at the table is re-keyed to match the column.** The clause "and what ends the state a reclaim that did not complete leaves on the pod" becomes "and what state a reclaim that did not complete leaves on the pod", matching the corrected `**Slot cleanup:**` pointer and `summary.md`'s deliverable index. — BECAUSE the column header now reads "State the cleanup leaves on the pod" and the commentary states the column asserts no ender, so the staged §7.1 sentence would land in `spec/07` pointing a reader at the table for a fact it no longer carries. — EVIDENCE: spec-changes.md, the `**Pod-side reclaim on a failed bind.**` block; the table header and the `**Slot cleanup:**` leaked-outcome replacement in the SPEC-3 §5.2 blocks; summary.md's SPEC-3 entry, "and what state it leaves on the pod".
- **DECISION: the SDK-demotion-close-failure row's residue gains the armed §4.9 lease-expiry timers,** so it reads "The slot's directories, the registry entry, and the armed [Section 4.9] lease-expiry timers". — BECAUSE the timer cancellation happens inside the deregistration step, and on that row the demotion returns before any release, so nothing deregisters and the timers stay armed. The row now matches the "no cleanup reclaims" row, whose reclaim-hold cell states the same condition. — EVIDENCE: `pkg/adapter/sdkwarm.go`, `DemoteSDK`'s error return ahead of `s.releaseSessionSlot(sessionID)`; `pkg/adapter/slotsession.go`, `deregisterSlotLocked` cancelling every armed expiry timer under `s.mu` before deleting the entry.
- **DECISION: the three failed-act rows state the residue as whatever the failed act left,** "Whatever the failed act left: the slot's directories, or processes owned by the slot's process group", on the runtime-close-succeeds row, the pre-`running` `Shutdown` row and the outside-`Shutdown` row. — BECAUSE the act set the same deliverable stages includes the process-group kill, whose failure leaves running processes rather than a directory, and the previous "pod termination ends all else" wording had covered it. The rows are quantified over the act set, so their residue cell is too. — ALTERNATIVES: enumerating every act's residue per row (rejected: it re-keys the rows onto named acts, which the standing context records as settled against); naming only the directories (rejected: under-inclusive against the action list).
- **DECISION: the column commentary is qualified rather than the last row's retirement pointer moved.** It now states that the column asserts nothing about which scrub reaches the residue, and names the last row's §6.2 occupancy-projection pointer as the one place the column names an ender, because that row is the one on which no cleanup runs. — BECAUSE the generalisation as written was false of the table: the last row's cell does assert an ender, and the downstream commentary and `summary.md` both rest on that clause staying in the cell. — ALTERNATIVES: moving the retirement pointer into the note under the table (rejected: the note is not keyed per row, and the pointer belongs to that row's condition).
- **DEFERRED: the implementation checklist's step S4 still describes the deliverable as "when the identifier hold ends and what ends the state left on the pod".** That is the fourth statement of the now-false column description. The checklist is out of bounds for this loop, so the pass between the loops closes it by re-keying the clause to "and what state is left on the pod". — EVIDENCE: the S4 line in `0081_..._.implementation-checklist.md`.
- **FACT: nothing outside the spec staging was falsified by these four edits.** `summary.md`'s SPEC-3 and SPEC-4 entries already read "what state it leaves on the pod" and already rest on the retirement clause, which stands. `non-spec-changes.md`'s only "what ends" site is the reclaim-hold comment in CODE-1, which is about the hold rather than the pod-state column. The problem statement carries no statement of the column. — EVIDENCE: grep for "ends the state" and "leaves on the pod" across the proposal directory.

### [spec.13.fix-G1.1]

DECISION: the SPEC-3 §5.2 disposition table's last column is re-keyed from "What ends the state left on the pod" to "State the cleanup leaves on the pod", every cell now names residue only, and the enders are stated once in the sentence appended to the post-table note ("Pod termination ends every residue the table names, and the whole-pod scrub, on a pod that reaches one under this section's scrub model, ends the slot's directories.") — BECAUSE whether a whole-pod scrub reaches a pod is a function of occupancy and the pool's recycle setting, and the table is keyed on what the cleanup reclaims, who performs it and which act fails, so no cell can carry a true universal about scrub reachability; two rounds of narrowing the cells each became the next round's finding — ALTERNATIVES: the reviewer's literal fix (restore "The whole-pod scrub or " on the two `leaked` rows plus an occupancy clause on the note) was rejected as the same kind of answer one step wider, and still open to the rows 8/9 occupancy question; deleting the column outright was rejected because rows 8 and 9 carry residue stated nowhere else and the last row's §6.2 retirement clause is cited twice; splitting the `leaked` rows by pod concurrency was rejected because it breaks the table's one-row-per-case partition on an axis no other row uses.

WATCHOUT: any future text that decides PER ROW whether the whole-pod scrub runs is the third occurrence of this defect. The table states residue; the scrub model owns the trigger. — EVIDENCE: 0081...spec-changes.md, SPEC-3 §5.2 `**Scrub model.**` append, the note paragraph after the table; spec/05_runtime-registry-and-pool-model.md:453,459

MISTAKE: round 12 removed "The whole-pod scrub or " from the two `leaked` rows on the ground that a `leaked` slot pins occupancy above zero. That ground holds only on a pod serving concurrent sessions, and the table's own note says a pod serving one session has no `leaked` sub-state, so both cells were false on a `maxConcurrentSessions: 1`, `recycle.enabled: true` pod. Cost: one round and this group.

FACT: the disposition table's last column is content staged only in this proposal. A grep for its cell phrasings across spec/, docs/, schemas/, charts/, pkg/ and tests/ returns nothing, so a re-key of the column cascades only inside the proposal. — EVIDENCE: 0081...spec-changes.md, the SPEC-3 §5.2 append

FACT: exactly four sites inside the proposal name the column rather than a row, and all were reconciled in this edit: the Design choice paragraph "Every cleanup's disposition is one table", the table lead-in sentence, the `**Slot cleanup:**` leaked-outcome pointer sentence that replaces the old universal, and the summary's SPEC-3 deliverable-index bullet. The sites at spec-changes.md's accepted-failure-modes list cite the HOLD column and the last row, and are unaffected. non-spec-changes.md cites the row for a slot released outside a `Shutdown` whose act fails, whose residue is unchanged, so nothing there was falsified.

OPEN: the round-12 UNVERIFIED asking whether rows 8 and 9 (registry entry standing, nothing reported) pin the pod's occupancy is now moot for the table, which no longer depends on the answer. It may still matter to SPEC-4's occupancy projection; a later round or the non-spec loop should settle it there if it does.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S4 (line 23) describes the §5.2 disposition table as the single home of ", and what ends the state left on the pod". That clause is now false. What is true: the table's last column states the state each cleanup leaves on the pod, and the sentence appended to the post-table note states, once, that pod termination ends every residue the table names and that the whole-pod scrub, on a pod that reaches one under §5.2's scrub model, ends the slot's directories. The clause should read "and what state it leaves on the pod".

### [spec.13.fix-design-G1.1]
DECISION: the §5.2 disposition table's last column STOPS NAMING ENDERS. Re-key it from "What ends the state left on the pod" to "State the cleanup leaves on the pod", so each of the nine cells names residue only (none / the slot's directories / plus the registry entry / plus the armed §4.9 lease-expiry timers), and state the enders ONCE in the paragraph after the table: pod termination ends all of it, and the whole-pod scrub, when the pod reaches one under this section's scrub model, ends the slot's directories. — BECAUSE the reachability of the whole-pod scrub is a function of pod occupancy, which the table does not key on, so every per-row assertion about it is a universal the table cannot support; the scrub's trigger already has a home in the same section's `**Scrub model.**` sentence (spec/05:453, restated in the staged append) and in the Lenny scrub procedure (spec/05:459). — ALTERNATIVES: the reviewer's suggested fix, restore "The whole-pod scrub or " on rows 2 and 5 and add an occupancy clause to the note at :659 (REJECTED: same kind of answer as round 12 one step wider, and provably still not total — `[spec.12.fix-G2.1]`'s own UNVERIFIED asks whether rows 8 and 9, whose registry entry stands, also pin occupancy; if they do, those cells are the round-14 finding); deleting the column outright (REJECTED: rows 8 and 9's registry entry and armed lease timers are stated nowhere else and SPEC-4's rationale cites the last row); a per-concurrency split of the two `leaked` rows (REJECTED: enumeration, the failure mode this loop is under directive to stop).
WATCHOUT: the last row's trailing sentence, "A pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection", is cited twice outside the table (spec-changes.md:677 and the SPEC-4 rationale at :864). It is a pod-retirement pointer rather than a scrub-reachability claim, so it SURVIVES the re-key in the same cell. Moving it cascades into both citations for no gain. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:657, :677, :864
FACT: the table's line map, stable as of this round: lead-in sentence :645 (its last clause "and what ends the state the cleanup leaves on the pod" must be re-keyed with the header), header :647, separator :648, the nine rows :649 through :657, the `leaked`-scoping note :659, the rationale paragraph :675-679. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:645-679
MISTAKE: rounds 12 and 13 both spent a round on the same two cells because each answer kept the claim and narrowed it. Round 12 deleted the scrub from two cells; round 13 found the deletion false on a `maxConcurrentSessions: 1`, `recycle.enabled: true` pod, where no `leaked` sub-state exists and the ending session's recycle-disposition `Shutdown` runs the scrub. Any further edit that decides, per row, whether the scrub runs is the third occurrence.
EVIDENCE for the round-13 half: spec/05_runtime-registry-and-pool-model.md:426 (pod reuse at concurrency 1), :453, :459; spec-changes.md:659 ("A pod serving one session has no `leaked` sub-state").
UNVERIFIED [carried, still open]: whether a standing adapter registry entry with nothing reported (rows 8 and 9) holds gateway-side occupancy above zero the way a `leaked` slot does. Under this design the table no longer depends on the answer, which is the point; the non-spec lane should still settle it. — originally `[spec.12.fix-G2.1]`
CORRECTS [spec.12.fix-G2.1]: its DECISION and its WATCHOUT ("the phrase still stands, correctly, on the rows whose `leaked` column reads `Not entered`; a future sweep must read the `leaked` column before deleting") are superseded. After the re-key no row names an ender, so there is no phrase to sweep and no `leaked`-column read to perform. Its FACT about leaked occupancy stands.

### [spec.13.review-mechanism.1]

DECISION: filed exactly one finding, on the round-12 fix to the two `leaked` rows of the §5.2 disposition table — BECAUSE the fix removed the whole-pod scrub as an ender on the ground that a `leaked` slot pins occupancy above zero, which is true only on a concurrent pod, while the note directly under the table says a pod serving one session has no `leaked` sub-state — ALTERNATIVES: leaving it, rejected because on a one-session recycling pod the very `Shutdown` whose cleanup failed carries the recycle disposition and runs the whole-pod scrub, so the cell is false in that case.

FACT: the whole-pod scrub is triggered by the recycle disposition carried on the ending session's own `Shutdown`, so on a one-session recycling pod the scrub runs on the same request whose per-slot cleanup failed — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:459 ("The gateway triggers the whole-pod scrub by sending the adapter the `Shutdown` RPC ... for the ending session carrying the **recycle disposition**"), and the staged §4.7 row at spec-changes.md:258 ("runs the whole-pod scrub when the recycle disposition is set ... whatever outcome that request answers").

FACT: the occupancy pin that makes the whole-pod scrub unreachable is a property of the `leaked` sub-state alone — EVIDENCE: spec/06_warm-pod-model.md:160 ("A slot in the `leaked` state remains counted in the pod's Redis slot-counter occupancy"), spec/05_runtime-registry-and-pool-model.md:488 ("a persistently `leaked` slot can hold total occupancy above zero indefinitely"). The slot-identifier reclaim hold does NOT pin occupancy: it is adapter-internal and invisible to the gateway except as refusals, which is why rows 3 and 7 keep the scrub as an ender while holding for the life of the pod.

WATCHOUT: when a fix removes a disjunct from a disposition-table cell on a reachability argument, check the cell against the concurrency-scoping note under the table (spec-changes.md:659). The rows are not scoped by concurrency; only the `leaked` column is.

FACT: the round-12 §15.4 edit (dropping "to a request that carries it") leaves every other §15.4 site consistent — spec-changes.md:1077, :1090, :1101-1104 and rule 2 at :1049 all cite the section whole or cite it for the hold status only. Nothing stale was left by that hunk.

### [spec.13.review-reliability.1]
DECISION: returned an EMPTY findings list for the reliability/fault-tolerance lens on spec round 13 — BECAUSE the round-12 diff is two hunks only (the two `leaked` disposition-table cells and the four-word deletion in the §15.4 pointer), both are reductions, and each re-verifies correct against the tree; no other site in the proposal carries the text either hunk changed — ALTERNATIVES: filing the two corrected cells as leaving a leaked slot with no reclaimer (rejected: `maxPodUptimeSeconds` is level-triggered by the WarmPoolController from the pod's `CreationTimestamp` "regardless of session activity", spec/05_runtime-registry-and-pool-model.md:489, so pod termination is a real terminal for a slot whose occupancy never reaches zero, and the leaked-slot-until-pod-termination rule is shipped text at spec/05:545).
FACT: the UNVERIFIED that `[spec.12.fix-G2.1]` and `[spec.12.fix-design-G2.1]` both left open — whether the two `Not entered` rows that leave an adapter-side entry standing (:656 SDK-demotion close failure, :657 pre-`running` slot no cleanup reclaims) also pin gateway occupancy above zero, which would make their "whole-pod scrub" ender unreachable too — resolves NO, so those four rows' phrasing is correct and needs no second sweep. Gateway occupancy is the gateway's own `active_slots` counter, decremented by `ReleaseSlotReservation` unconditionally on every bind-failure path, independently of anything the adapter's registry holds. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:172,215-217 (release on the failure path), binder.go:1708-1714 ("ReleaseSlotReservation decrements unconditionally"), pkg/gateway/sessionserver/start.go:3234 ("decrements the pod's active_slots"); the adapter-side entry is never an input to that counter.
FACT: the exclusive-pod arm of row :657 has a second, independent reclaimer in the tree: `failPhase` drains the pod after releasing the lease, so the pod does not sit on the abandoned pre-`running` slot. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082
USEFUL [spec.12.fix-design-G2.1]: its WATCHOUT ("do not touch the four `Not entered` rows that carry the same phrase") named exactly the rows I would otherwise have re-derived from scratch, and its cell-level line numbers let me check the partition in one read.

### [spec.14.review-mechanism.1]

FACT: the round-13 fix re-keyed the §5.2 disposition table's last column from "What ends the state left on the pod" to "State the cleanup leaves on the pod", and moved the enders into one trailing sentence of the paragraph after the table ("Pod termination ends every residue the table names, and the whole-pod scrub, on a pod that reaches one under this section's scrub model, ends the slot's directories."). — EVIDENCE: spec-changes.md:647, spec-changes.md:659
FACT: the §4.9 lease-expiry timers the table names as residue are ADAPTER-LOCAL, so "pod termination ends every residue" holds for them. — EVIDENCE: pkg/adapter/credexpiry.go:12-23 ("a local timer for each direct-mode lease's expiresAt")
FACT: the whole-pod scrub is purely on-disk and asynchronous, so the trailing sentence's directories-only claim about it is right. — EVIDENCE: pkg/adapter/podscrub.go:38-48
DECISION: I did NOT file §7.1's surviving clause "the same table states what becomes of the slot state that attempt left on the pod" (spec-changes.md:403) — BECAUSE the only attempts that obligation does not reach and that leave pod-side state are the exclusive-pod ones, and the table's last row DOES state their ending (the §6.2 occupancy-projection retirement, spec-changes.md:657). — ALTERNATIVES: filing it as a stale-pointer twin of the clause the same round fixed earlier in the sentence; rejected as refutable on that row.
WATCHOUT: the re-key left two COMMENTARY sites describing the column as an ender column. Both are filed this round: spec-changes.md:1015-1016 ("What each pod exit ends is a column of the §5.2 disposition table") and the because-clause at spec-changes.md:674-676 ("that row is the one on which no cleanup runs at all"), which row 8 (spec-changes.md:656, "runs no cleanup") falsifies. — EVIDENCE: spec-changes.md:656, spec-changes.md:674, spec-changes.md:1015
UNVERIFIED: whether the trailing enders sentence belongs in the applied §5.2 prose at all, given the scrub model already states which residue the scrub reaches; a later lens could test it as a duplication (g) candidate. Nobody has.

### [spec.15.fix.1] — corrections appended to round 15's fix pass, not a new pass

The round-15 fixer re-keyed three residue cells of the SPEC-3 §5.2 disposition table. The
post-fix review found one defect in that re-key. It is corrected below with the smallest edit.

- **DECISION: row 8's session clause is bound to the arm that can produce it.** The cell now reads "The slot's directories, the registry entry, the armed [Section 4.9](…) lease-expiry timers, and, for a slot the pod's shared runtime process was given, the session the failed close left open in it". — BECAUSE the row is keyed "Slot of either kind that the SDK demotion would release", and the staged §6.2 pre-`running` paragraph defines a slot as reaching `running` only when the shared runtime process has been given the session, so on the pre-`running` arm the flat conjunct asserted a residue the mechanism cannot produce. The arm is reachable: an entry stands bound-not-started before the start is admitted (`pkg/adapter/slot.go`, the `started` field's comment), `anyRegisteredSession` returns whatever single entry stands without regard to `started` (`pkg/adapter/sdkwarm.go`), and `ShutdownDemoteSDK` reaches `DemoteSDK` on SIGTERM while a `ConfigureWorkspace` has claimed the slot and not yet reached `noteRuntimeStarted`. — ALTERNATIVES: splitting row 8 by slot kind (rejected: it re-keys one row on an axis the other outside-`Shutdown` rows do not use, and the other columns are identical on both arms); dropping the clause (rejected: the residue is real on the given-to-runtime arm and is stated nowhere else, the table being the single home of every cleanup's disposition).
- **FACT: nothing outside the spec staging was falsified by the edit.** The cell's phrasings appear nowhere else in the proposal directory or the tree; `summary.md`'s SPEC-3 entry states the column by its header only, and `non-spec-changes.md` carries no statement of row 8's residue. — EVIDENCE: grep for "failed close left open" and "would release" across the proposal directory and across spec/, docs/, schemas/, charts/, pkg/ and tests/.
- **WATCHOUT: rows 2, 7 and 8 all name a session a failed runtime close left open, and each is scoped differently** (row 2 is keyed to a slot the runtime was given, row 7 hedges with "or", row 8 now carries the explicit qualifier). A future round that flattens any of the three onto one wording reintroduces this defect class. — EVIDENCE: spec-changes.md, the SPEC-3 §5.2 disposition table, rows 2, 7 and 8.

### [spec.15.fix-G1.1]

DECISION: the §5.2 disposition table now states the failed-runtime-close residue with ONE phrase, "the session the failed close left open in the pod's shared runtime process", in all three rows that can reach it (the `Shutdown` runtime-given row, the released-outside-a-`Shutdown` row, the SDK-demotion row) — BECAUSE the residue is one mechanism at three performers and a second vocabulary would be a second rule — ALTERNATIVES: making the staged tree removal conditional on `closeErr == nil` so row 2's old cell became true (rejected: it inverts shipped behaviour and moves the hold's completion predicate); splitting row 7 per performer (rejected: breaks the one-row-per-case partition); the finding's conditional clause "where the failed act was the runtime close" (rejected: hair inside a list of plain disjuncts); qualifying row 8 with "for a slot the runtime was given" (rejected: `sw.DemoteSDK` closes the pre-connected SDK process itself, so the residue stands on a slot of either kind).

FACT: `Server.Shutdown` calls `removeSlotTree(st)` AFTER `Runtime.Close` in the same `if bound` block with NO branch on `closeErr`, and discards the removal error, so a failed close does not preserve the slot's directories — EVIDENCE: pkg/adapter/session.go:262-271.

FACT: the §10.1 hold-timeout termination deregisters in pass 1 and closes in pass 2, so a close that fails there leaves a live session with the entry already gone; `releaseSessionSlot` reaches no `Runtime.Close` at all (its whole body is `deregisterSlot`, `removeSlotTree`, `cancelPodMCPIfRuntimeIdle`) — EVIDENCE: pkg/adapter/holdstate.go:190 and :248-251; pkg/adapter/slotsession.go:214-220.

WATCHOUT: the disposition table's last column is the ONLY carrier of any residue phrase in the whole corpus. A grep of every distinctive residue phrase across the proposal directory, spec/, docs/, schemas/ and charts/ returns the table rows and nothing else, so a residue fix is a one-file, one-table edit and the commentary sentences below the table (including "Pod termination ends every residue the table names") enumerate nothing and need no follow-up — EVIDENCE: 0081_..._.spec-changes.md:647-666.

WATCHOUT: a residue cell keyed on a NAMED failing act may assert only what that act leaves; everything else on the row is a hedge. Row 2 asserted the directories survive because the round-13 re-key turned an ender column into a residue column and the cell was carried across unchanged. When a column's meaning is re-keyed, re-read every cell against the new header rather than only the cells the re-key touched.

### [spec.15.fix-design-G1.1]

DECISION: the three residue cells (rows 2, 7, 8 of the §5.2 disposition table) are fixed in ONE edit that
uses ONE phrase for the runtime-close residue, "the session the failed close left open in the pod's shared
runtime process" (row 2's existing wording), and row 2 additionally drops its unconditional directory claim
onto the hedged form rows 3/5/7 already use — BECAUSE all three rows read one mechanism at three performers
and any per-row phrasing divergence is the next round's finding; the hedged form is already the table's
established wording for a residue quantified over which act failed — ALTERNATIVES: (a) a per-act residue
mapping sentence under the table ("a failed removal leaves the directories, a failed kill leaves processes,
a failed close leaves the session"), so each failed-act cell could shrink to "Whatever the failed act left"
— REJECTED, it is a second enumeration of the cleanup's acts, which the standing-context trap "Do NOT
enumerate the cleanup's acts anywhere but §5.2's `**Slot cleanup:**` bullet" forbids; (b) giving rows 3 and
5 the same widened disjunct so all four failed-act rows read identically — REJECTED, row 3's key states the
close SUCCEEDED, so the disjunct contradicts its own key; (c) finding 1's literal wording, "or, where the
failed act was the runtime close, the session it left open" — REJECTED as hair, a conditional clause inside
a list whose other members are plain disjuncts; the plain disjunct says the same thing.

WATCHOUT: the tempting local fix on row 2 is to make the tree removal conditional on the close, i.e. to
"repair" the code so the cell becomes true. `Server.Shutdown` removes the tree unconditionally after the
close inside the same `if bound` block and discards the removal error, and staged CODE-1 preserves that
order (`treeErr` guarded on `removed` alone). Making removal conditional would also change the hold's
completion predicate. The cell is the defect. — EVIDENCE: pkg/adapter/session.go:262-271

FACT: the hold-timeout termination is the ONLY non-`Shutdown` performer that can fail a runtime close after
deregistering, which is why row 7 needs the close residue: `onHoldTimeout` deregisters in pass 1 and
`terminateHeldSession` closes in pass 2, discarding the error; `releaseSessionSlot` calls no `Runtime.Close`
at all; `Server.DemoteSDK` closes BEFORE it deregisters and returns on failure, which is row 8's case. —
EVIDENCE: pkg/adapter/holdstate.go:190,203-205,248-251; pkg/adapter/slotsession.go:214-220;
pkg/adapter/sdkwarm.go:280-282,296-298

FACT: the residue cells' phrasings exist nowhere but the six table rows. A grep of every distinctive cell
phrase across proposals/0081_*/ (excluding the review log), spec/ and docs/ returns only
spec-changes.md:650,651,653,655,656,657, so a cell rewrite cascades nowhere. Confirms and re-verifies
`[spec.13...]`'s FACT about the column. — EVIDENCE: spec-changes.md:647-657

UNVERIFIED: whether row 5 (pre-`running` slot reclaimed by a `Shutdown`, an act fails) can also produce the
runtime-close residue. `Shutdown` runs the close whenever the deregistration removed a bound entry
(`bound := removed && st.sessionID != ""`, pkg/adapter/session.go:238), which does not test `started`, so a
pre-`running` slot's cleanup does reach `Runtime.Close`; what a failed close leaves for a session that never
started is not established. Left out of this round's edit deliberately. A later round or the mechanism lens
should settle it before widening row 5.

### [spec.15.review-applicability.1]

DECISION: Returned an empty findings list for the applicability-and-sequencing lens on spec round 15 — BECAUSE every staged anchor resolves uniquely against the current tree, every cross-reference anchor slug exists, both legs of every relocation are staged, and the two existing tier-11 gates the staging would break carry recorded dispositions — ALTERNATIVES: I considered filing (a) the SPEC-4 §6.2 fence trigger-list edit, which gives only the replacement block with no "reads, verbatim" quote of the current text, and (b) the disposition table's new last-column header "State the cleanup leaves on the pod" against rows 8 and 9 whose second column says no cleanup runs. Both are below the bar: (a) the target is unique and the new value is stated, (b) column 2's header "How the cleanup ends" already tolerates a "No cleanup runs" cell, so the convention is established.

FACT: All 25 fenced "reads, verbatim:" anchors in spec-changes.md match their target spec file exactly once each (checked mechanically with a byte-exact substring count). The 18 non-fenced anchor phrases also resolve uniquely, with one exception below. EVIDENCE: proposals/.../0081...spec-changes.md:252,292,323,437,460,472,501,520,544,567,589,715,732,753,777,803,815,830,963,975,1139,1151,1163,1175,1209

WATCHOUT: The §4.6.1 closing sentence SPEC-4 deletes is quoted in prose as "The one-session-only invariant of §6.2 is the..." but the tree writes it "The one-session-only invariant of [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) is the...". A byte-exact grep for the proposal's spelling returns nothing. It is a paraphrase in a parenthetical, not a "reads, verbatim" block, and the bullet it lives on is uniquely identified, so it is not a finding — but do not treat that grep miss as evidence of a stale anchor. EVIDENCE: spec/04_system-components.md:416

FACT: All 21 markdown link targets in the staged spec text resolve — cross-file paths exist and every `#slug` matches a heading in the file the staged text lands in (checked for `#47-runtime-adapter`, `#471-role-and-gateway-rpc-contract`, `#463-crd-field-ownership-and-write-boundaries` in spec/04; `#71-normal-flow`, `#73-retry-and-resume` in spec/07). EVIDENCE: spec/04_system-components.md:659,695; spec/07_session-lifecycle.md:3

FACT: The §7.1 reclaim paragraph lands INSIDE spec/07's §7.1 fenced flow listing (fence opens at spec/07_session-lifecycle.md:5, closes at :54). Its markdown links will not render as links. This is the shipped precedent: the atomicity paragraph it is inserted after already carries `[§6.2](...)` and `[§15.1](...)` inside the same fence. Not a defect; do not re-file it. EVIDENCE: spec/07_session-lifecycle.md:23

FACT: `spec161Metrics` in pkg/observability/metrics/catalog_test.go is a hand-transcribed Go slice, not parsed from spec/16. Adding a §16.1 row therefore breaks neither `TestMetricCatalogHasNoUnspecifiedMetrics` nor its converse; and `tests/tier11_docs/adapter_metric_catalog_test.go` only walks registered metrics outward, so a §16.1 row with no code counter is inert to it. SPEC-6's claim on this point is correct. EVIDENCE: pkg/observability/metrics/catalog_test.go:15,193,202; tests/tier11_docs/adapter_metric_catalog_test.go:80

FACT: The only existing tier-11 gate the staged spec edits hard-fail is `TestPerReleaseSessionCountDrainAgrees_F5231`, whose spec/12 assertion requires one of two `sessions_served` read-clause spellings that SPEC-3's §12.6 replacements both delete. It IS dispositioned, in the SPEC-3 carrier table. Two neighbouring gates survive the edits untouched: `TestLeakedSlotCountingLifetimeAgrees_F5231` reads only the `failed`/`leaked` counting-lifetime substrings of the §5.2 whole-pod-replacement-trigger line, which the `leaked` parenthetical replacement leaves intact, and `TestVMRestartRewarmLegScopedToStandardAndInPlace_F5232` reads only the `recycling`-claim clauses of the §6.2 projection sentence, none of which SPEC-4's three clause deletions touch. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173-176,92-96; tests/tier11_docs/vm_restart_reprovision_consistency_test.go:182-188; proposals/.../0081...spec-changes.md:71

FACT: `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` checks the per-slot fence by edge prefix only (`"slot_cleanup ──→ released"`), never by the parenthetical, so SPEC-4's replacement of both annotations with `(see §5.2)` passes it, and the added `receiving_uploads ──→ slot_cleanup` edge is not in its list. EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-37,53-58

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` pins the opener `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` word for word on both the §4.7 row and its doc mirror. SPEC-3's `ReportSessionScrub` replacement reproduces that sentence unchanged, so the gate holds. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,67-72

FACT: `FinalizeWorkspaceRequest` already carries `bool mid_session = 4` in the shipped proto, so the §4.7.1 carriage table's `FinalizeWorkspace | mid_session | carried` row needs no schema addition and SCHEMA-1's omission of it is correct rather than a gap. EVIDENCE: schemas/lenny-adapter.proto:713,724

FACT: All four spec sites that name `ReportSessionScrub` are staged (§4.7 row, §5.2 scrub-model, §5.2 `**Slot cleanup:**`, §12.6). The three spec sites that state only that a per-slot cleanup runs at each release, and so stay true under the withdrawn universal, are spec/06:24, spec/07:72 and spec/05:457 — I read all three and none asserts a report. EVIDENCE: spec/06_warm-pod-model.md:24; spec/07_session-lifecycle.md:72; spec/05_runtime-registry-and-pool-model.md:457

UNVERIFIED: SPEC-6 lands the two §16.1 metric rows on a spec step whose tier list includes 11, while the matching `docs/reference/metrics.md` rows land later under CODE-9. I found no gate that reconciles §16.1 rows outward to metrics.md, so I believe the intermediate tree is green, but I did not run tier 11. A checklist-lane reviewer should confirm before relying on it.

### [spec.15.review-citations.1]

FACT: every fenced verbatim anchor in spec-changes.md still hits EXACTLY ONCE across `spec/*.md` (re-run in round 15 with the extract-and-count python script); the zero-hit blocks are exactly the replacement texts, the two new fence entries, the SPEC-4 `claimed ──→ draining` replacement and the two §16.1 rows. — EVIDENCE: proposals/.../spec-changes.md:251,291,322,436,459,471,500,519,543,566,588,714,731,752,776,802,814,829,962,974,1138,1150,1162,1174,1208
FACT: every backticked code symbol in spec-changes.md re-verifies in the tree (28 distinct): `Binder.ReleaseSlot` (pkg/gateway/podlifecycle/podsession/slotbinder.go:528), `Client.ReportSessionScrub` (pkg/adapter/gatewaycontrol/scrubreport.go:78), `ScrubReporter.RecordSessionScrub` (pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451), `failPhase` (binder.go:1072), `maxSlotRetries` (pkg/gateway/sessionserver/start.go:2720), `terminateHeldSession` (pkg/adapter/holdstate.go:229), `slotlayout.RemoveTree` (pkg/adapter/slotlayout/tree.go:58), `deregisterSlotLocked` (pkg/adapter/slotsession.go:174), `lineContaining` (tests/tier11_docs/backup_status_enum_test.go:48). — EVIDENCE: as listed
CORRECTS [standing context, "MISTAKE nearly filed, round 11: SPEC-4's rationale puts quotation marks around a sentence that is NOT in `occupancy.go`"]: the sentence IS in the file, verbatim, in the `ProjectOccupancyPhase` doc comment: "the §6.2 state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim DELETE". The round-11 shard read the second, differently-worded statement inside the function body instead. The quotation is sound; stop re-checking it. — EVIDENCE: pkg/controller/warmpool/occupancy.go:57-59 (quote) and :116-117 (the body's other wording)
FACT: the SPEC-3 carrier table's 24 rows all resolve — every named file exists and every named comment really carries the withdrawn universal. Spot-verified: pkg/adapter/server.go:168-170 ("on every session release via ReportSessionScrub"), tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-7 and :20-21, tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-456, pkg/agentpodstate/agentpodstate.go:60,:128, pkg/gateway/mcpfabric/.../scrubreport_server.go:67,:196,:472, pkg/adapter/gatewaycontrol/scrubreport.go:72, tests/tier4_integration/concurrent_delegation_proxy_test.go:49,:122. The non-carrier exclusion for `pkg/adapter/gatewaycontrol/scrubreport.go:13` (the `SessionScrubOutcome` comment) is correct: :13 states the CLEANUP runs on every release, :72 states the REPORT does. A future sweep must keep that line distinction.
WATCHOUT: the §4.7 `DemoteSDK` row's claim that "[§6.1] admits `preConnect` only at `maxConcurrentSessions: 1`" is TRUE and sits at spec/06_warm-pod-model.md:69, inside §6.1 (§6.2 starts at :78). Do not file it as a wrong-section citation on the strength of the line being near the §6.2 boundary. — EVIDENCE: spec/06_warm-pod-model.md:69, :78
FILED: the disposition table's row 2 (runtime close fails) names the slot's directories as residue, but `Server.Shutdown` runs `removeSlotTree` AFTER the close and unconditionally of `closeErr`, so on the ordinary sub-case the directories are gone. Rows 3, 5 and 7 use the hedged "Whatever the failed act left"; row 2 is the only failed-act row that hard-codes a residue. — EVIDENCE: spec-changes.md:650 vs pkg/adapter/session.go:262-271
FACT: round 15's citation lens found nothing else. §4.1's Request Message Scope derivation, the §29.4 preconditions/step-10/step-12/step-13 claims, the §7.1 atomicity-paragraph placement inside the fenced flow, the §12.6 trailing §5.2 link, the §5.2 step-0 credential purge and recycle-lifecycle assumption, the §5.2 `Max retries`/`Fresh workspace guarantee`/`Session count limit` bullets, §4.6.1's two claim-deletion bullets and its opening input enumeration, §6.2's `sdk_connecting ──→ failed` "see §6.1" precedent, and all seven named tier-11/tier-3 gate test names each re-verify. — EVIDENCE: spec/04:149-157,409-416; spec/05:455,461,471,488,545,555,556,561; spec/06:69,90; spec/07:23-24; spec/12:481,494; spec/29:586-588,+step 10/12/13

### [spec.15.review-client-surface.1]

DECISION: returned EMPTY for the client-facing-surface lens in spec round 15 — BECAUSE every externally-consumed contract the staged spec edits touch re-verified against the tree, and the round-14 diff (the §5.2 disposition table's last column re-keyed from "what ends the state" to "state the cleanup leaves", plus the three pointer sentences in §7.1, §6.2 and the SPEC-3 commentary) touches no client-facing representation at all. ALTERNATIVES: filing the §15.4 SDK-warm-demotion-contract paragraph as a missed edit site for the `DemoteSDK` registry removal SPEC-1 adds to the §4.7 row (rejected: §4.7 is normative for adapter authors, the removal is shipped behaviour, and the fix would add text to §15.4, which the caller directive bars); filing §7.4's `upload_to_session` surface for the new rule-2/rule-3 refusals of an in-flight mid-session upload (rejected: the refusal reaches the client under the existing upstream-error envelope, and the case is already an accepted failure mode).

FACT: the §15.1 `SETUP_COMMAND_FAILED` mapping really does hold in code end to end, so SPEC-5's widening of that row is truthful. Every `RunSetup` error is wrapped as a `SetupCommandFailure` (pkg/gateway/podlifecycle/podsession/slotbinder.go:304 and binder.go:938), and `writeSetupCommandError` branches on `status.Code(setupFail.Cause) == codes.FailedPrecondition` alone to emit the 422 `SETUP_COMMAND_FAILED` and everything else to the retryable 503 fallback. — EVIDENCE: pkg/gateway/sessionserver/start.go:239-248

FACT: all four §15.1 verbatim quotes SPEC-5 replaces, and the §6.2 `**Client visibility:**` clause SPEC-5 replaces, are byte-exact against the tree as it stands. — EVIDENCE: spec/15_external-api-surface.md:1136 (the whole `SETUP_COMMAND_FAILED` row, carrying all four sentences on one physical line), spec/06_warm-pod-model.md:290

FACT: the three SPEC-1 verbatim anchors re-verify byte-exact too. — EVIDENCE: spec/04_system-components.md:157 (§4.1 third sentence), :674 (`DemoteSDK` row), :686 (`Shutdown` row)

FACT [re-verification of an entry the standing context already carries, so treat this as a USEFUL rather than new]: no OpenAPI document and no language SDK mirrors any surface this proposal edits. `SETUP_COMMAND_FAILED`/`setup_command_failed` appears under `pkg/` only in `pkg/gateway/externalapi/errorclassify/` and `pkg/gateway/sessionserver/start.go`, and nowhere in `pkg/gateway/externalapi/openapi/openapi.json` or under `sdks/`. Separately, `ShutdownRequest`, `slot_reclaim`, `SlotReclaim`, `ReportSessionScrub` and `unconditional_teardown` return ZERO hits across `sdks/`, `docs/api/`, `docs/client-guide/` and `docs/runtime-author-guide/`, so the adapter wire contract has no client-SDK or client-doc parallel to keep in step. — EVIDENCE: `grep -rln` over those trees returns nothing; the only openapi.json in the repo outside specshift testdata is pkg/gateway/externalapi/openapi/openapi.json

USEFUL [standing context, "No OpenAPI document and no language SDK mirrors any edited surface"]: saved a full sweep; I re-ran it rather than trusting it (standing hazard) and it holds in round 15.

FACT: the adapter `ErrorCode` enum still has exactly one spec-side mention of any value, so minting 28 and 29 owes no spec table edit and the "Spec sections deliberately untouched" claim about §15.1 taking no new row is sound. `grep -n "ERROR_CODE_\|PROTOCOL_VERSION_INCOMPATIBLE\|ErrorCode" spec/*.md` returns only the §15.2.1 taxonomy reference and the §15.4.2 `INIT` row. — EVIDENCE: spec/15_external-api-surface.md:552, :1699

FACT: the runtime-facing graceful-shutdown frame is defined as a frame and nowhere asserts it is sent at every session end, so the §4.7 `Shutdown` row's new co-tenancy condition falsifies no runtime-facing schema or §28 text; §29.4 step 13 was the only trace site and SPEC-1 edits it. — EVIDENCE: schemas/lenny-adapter-jsonl.schema.json:109-119 ("Inbound graceful-shutdown signal"), spec/28_communication-channels.md:617-623, :904

FACT: §15.4.2 carries no `Shutdown` or teardown row that the new `reclaimed`/`superseded`/`absent` outcome would have to be mirrored into; its table is the five-state INIT→TERMINATED machine and nothing else. — EVIDENCE: spec/15_external-api-surface.md:1696-1704

FACT: every field number SCHEMA-1 claims free really is free in the messages the §4.7.1 carriage table names, and `FinalizeWorkspaceRequest.mid_session` already exists as field 4, which is why only `PrepareWorkspaceRequest` gains a `mid_session`. The carriage table's request set (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `Resume`, `ConfigureWorkspace`, `Shutdown`) matches the `service Adapter` RPC list exactly. — EVIDENCE: schemas/lenny-adapter.proto:32-241 (service block), :682-696, :706-731, :846-857, :1021-1030

WATCHOUT: `migrations/0167_runtime_definitions_execution_mode_service.up.sql:102-104` states the same `sessions_served` write trigger §12.6 is re-keying ("incremented at each session release (ReportSessionScrub)"), so it goes false when SPEC-3 lands. Do NOT file it: it is already row 13 of the SPEC-3 carrier table, dispositioned to the non-spec loop. — EVIDENCE: spec-changes.md:628

UNVERIFIED: whether `docs/reference/adapter-contract.md` needs a mirror of the new §4.7.1 numbered-rule block (beyond DOCS-2's four row/paragraph edits) for the tier-11 adapter-contract reconciliation gates. I did not chase it because any remedy lands in docs/, which this loop may not edit. The docs-alignment lens or the non-spec loop should settle it.

### [spec.15.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens at spec round 15 — BECAUSE the lens's natural findings (a docs/ page left describing superseded behaviour, a missing docs mirror) have their remedy in `docs/`, and this loop's scope bars a finding "whose only remedy is a code, test, or docs change". The only in-scope residue of this lens is an accepted/deferred failure mode needing landing SPEC text, and every remaining candidate in the Edge-cases section has already been filed and refuted on materiality — ALTERNATIVES: filing the architecture.md projection-enumeration gap and the §15.4 reclaim-hold docs mirror gap, both rejected as docs-only remedies.

FACT: the docs-mirror paragraph at the end of spec-changes.md ("Each reader-facing reference page that mirrors these sections moves with the section it mirrors") was re-verified line by line and every claim in it is true of the tree: `docs/reference/adapter-contract.md` `Shutdown` row exists, `DemoteSDK` row and `ReportSessionScrub` row exist; `docs/reference/state-machines.md` carries the `slot_cleanup | released` row and the `slot_cleanup -> leaked` clause; `docs/reference/execution-modes.md` and `docs/operator-guide/security-principles.md` each carry the reporting clause DOCS-4 removes. — EVIDENCE: docs/reference/state-machines.md:237,251; docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33

FACT: SPEC-5's four verbatim quotes out of the §15.1 `SETUP_COMMAND_FAILED` row all match the shipped row exactly, and its docs mirror carries the same four sentences on one line. — EVIDENCE: spec/15_external-api-surface.md:1136; docs/reference/error-catalog.md:129

FACT: spec/15 has no enumerated adapter `ErrorCode` table, so the two new adapter codes take no §15 row; `PROTOCOL_VERSION_INCOMPATIBLE` appears only in the §15.4.2 `INIT` lifecycle row, exactly as the "Spec sections deliberately untouched" entry claims. — EVIDENCE: spec/15_external-api-surface.md:1699

FACT: §16.1's metric catalog table is two columns (description-in-name, Type), which is the form SPEC-6's two staged rows use. The sibling `lenny_slot_failure_total` row is the anchor SPEC-6 names. — EVIDENCE: spec/16_observability.md:14

DEFERRED [docs/getting-started/architecture.md]: line 237 enumerates the occupancy projection's inputs as "claim existence, binding state, and disposition". SPEC-4 adds "the phase the pod currently projects" as an input to §4.6.1's enumeration and DOCS-1 adds that same input to `docs/reference/state-machines.md`'s pod-state-machine paragraph only, so architecture.md's enumeration is left short one input. Not filed: docs-only remedy, out of this loop's scope, and the page is a getting-started conceptual page where the enumeration may legitimately stay at concept depth. The docs loop should decide.

DEFERRED [docs/reference/adapter-contract.md]: SPEC-5 adds TWO blocks to §15.4 (the bind attempt token contract and the slot-identifier reclaim-hold contract). DOCS-2 mirrors only the token, and states its ground for leaving the cascade in §4.7.1 (a runtime author issues none of the governed requests). The reclaim hold's `ABORTED` refusal is an adapter-author obligation of the same kind and DOCS-2 gives it no sentence and no explicit exclusion rationale. Not filed: docs-only remedy. The docs loop should either mirror it or record why it is excluded alongside the cascade.

UNVERIFIED: whether `docs/reference/metrics.md`'s `## Adapter metrics` table uses the same column set as the gateway table CODE-9 adds `lenny_slot_compensation_superseded_total` to; CODE-9 asserts sibling phrasing ("outside the default scrape set") but I did not open that table. The non-spec loop's docs reviewer should check.

### [spec.15.review-edit-sites.1]

DECISION: returned EMPTY for the edit-site lens in round 15 — BECAUSE every identifier the staging adds, changes or retires re-swept clean against `spec/`, `docs/`, `schemas/`, `charts/` and `docs/assets/diagrams/`, and every surface the sweep found is already in a staged edit, in the SPEC-3 carrier table, in a named docs/schema deliverable, or in a recorded Deferred entry — ALTERNATIVES: filing the §16.1.1 "Used on" column (see below) and the §15.4 SDK-warm demotion contract paragraph (which now has a registry-removal obligation via the SPEC-1 `DemoteSDK` row but describes only teardown/timeout/post-state/`UNIMPLEMENTED`); both rejected as descriptive prose whose omission makes nothing wrong.

FACT: the round-13/14 re-key of the disposition table's last column (header `What ends the state left on the pod` → `State the cleanup leaves on the pod`) has exactly THREE citing sites and all three moved with it: staged §7.1 (`what state a reclaim that did not complete leaves on the pod`), the staged §5.2 leaked-outcome pointer (`what state each leaves on the pod`), and the Design choice paragraph. The only stale "ender" phrasings left are proposal rationale, not staged text, and both are already refuted — EVIDENCE: spec-changes.md:38, :403, :675, :738, :1015

FACT: the withdrawn "reports on every session release" universal re-swept independently with `grep -rni "ReportSessionScrub"` and `grep -rni "cleanup outcome"` over `spec/ docs/ schemas/ charts/`. Every hit is a carrier-table row or a site the table's lead-in excludes. `docs/runtime-author-guide/lifecycle.md:390` ("then reports the outcome") looks like a carrier and is NOT one: it describes the whole-pod scrub / `ReportPodScrub`, not the per-slot report. Do not add it as a row — EVIDENCE: docs/runtime-author-guide/lifecycle.md:390, spec-changes.md:614-639

FACT: the claim-deletion projection rule has exactly five carriers and SPEC-4 + DOCS-1 cover four of them; the fifth is the recorded Deferred on `sandbox_types.go` — EVIDENCE: spec/04_system-components.md:415, :416; spec/06_warm-pod-model.md:80, :95-97; docs/reference/state-machines.md:138; spec-changes.md:1324-1327

FACT: §5.2's per-slot cleanup ACTION LIST has exactly one spec carrier and one docs carrier. `grep -rn "process group" spec/ docs/ schemas/ charts/` returns only spec/05:545 (SPEC-3's first anchor); the docs mirror is the `slot_cleanup | released` row of `docs/reference/state-machines.md:237`, which DOCS-1 replaces. No third site — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545, docs/reference/state-machines.md:237

FACT: no diagram is affected. `grep -rln "Shutdown\|slot_cleanup\|ReportSessionScrub" docs/assets/diagrams/` returns nothing, so the §6.2 fence edge and the `Shutdown` row changes owe no SVG or ASCII-fallback edit — EVIDENCE: docs/assets/diagrams/ (empty grep)

FACT: `schemas/` holds one affected file. Of the eleven entries under `schemas/`, only `lenny-adapter.proto` matches `sessions_served|slot_cleanup|leaked`; `lenny-adapter-jsonl.schema.json`, `workspaceplan-v1.json` and the rest are untouched by this staging — EVIDENCE: schemas/ listing

FACT: the two new §16.1 rows pass the §16.1.1 label gate. §16.1.1 requires only that a label NAME appear in its table; `pool` and `k8s_pod_name` both do. Its "Used on" column is descriptive prose no gate reads, so the two new series not being named there is not an edit site — EVIDENCE: spec/16_observability.md:289-296, spec-changes.md:1232-1233. This closes the standing `OPEN`/`UNVERIFIED` "Do the two SPEC-6 series belong in §16.1.1's 'Used on' column?" in the negative.

USEFUL [spec.10.review-edit-sites.1 and the standing-context anchor/carrier entries]: the "every anchor hits byte for byte and exactly once" and "Spec files touched is complete" entries let this pass skip a full anchor re-verification and spend its budget on the carrier and identifier sweeps instead. Both re-spot-checked on the anchors the round-13/14 fix touched (§5.2 table, §7.1 block) and both still hold.

WATCHOUT: §12.6's replacement read clause cites §5.2's `**Session count limit:**` bullet, which sits under `**Pod retirement policy (recycling pools).**`, NOT under a concurrency-scoped heading. A future round tempted to file "the pointer lands in a `maxConcurrentSessions > 1` block" (the trap the `**Slot cleanup:**` bullet sets) is wrong here — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488, spec-changes.md:821

### [spec.15.review-fresh.1]

DECISION: filed exactly two findings, both against the round-13 re-key of the §5.2 disposition
table's last column, and nothing else — BECAUSE the re-key turned an ender column into a residue
column, and a residue cell is an EXISTENCE assertion where an ender cell was not, so each cell has
to be re-checked against what the shipped handler actually leaves behind; two cells do not survive
that check — ALTERNATIVES: filing the "completed" collision between §7.1 ("a reclaim whose answer
reports a clean exit completed") and the §5.2 hold paragraph ("completed = every act returned
without error"), which diverge on table row 3 (clean-exit Set, hold held for life): rejected,
the standing-context trap records these as three deliberately different predicates and the hold
column resolves the case in the table itself.

FACT: `Server.Shutdown` runs `removeSlotTree(st)` UNCONDITIONALLY after `Runtime.Close`, whatever
the close returned, and discards its error; the staged CODE-1 handler keeps that order and only
binds the error as `treeErr` under `if removed`. So on the "runtime close fails" row the slot's
directories are gone unless the removal ALSO failed. — EVIDENCE: pkg/adapter/session.go:262-271
(`closeErr = s.Runtime.Close(...)` then `_ = removeSlotTree(st)`);
0081_..._.non-spec-changes.md:407-431 (`treeErr = s.removeSlotTreeVia(st)` inside `if removed`,
no `closeErr` guard).

FACT: two paths outside a `Shutdown` close the shared runtime process and can fail there, and both
discard the error, so the session stays open on the pod. `terminateHeldSession` does
`_ = s.Runtime.Close(ctx, m.sessionID)` and then `s.noteRuntimeClosed` unconditionally;
`Server.DemoteSDK` returns `codes.Internal` out of `sw.DemoteSDK` before it touches the registry.
`releaseSessionSlot`, the third performer on those rows, closes no runtime at all. — EVIDENCE:
pkg/adapter/holdstate.go:248-254; pkg/adapter/sdkwarm.go:280-282, :289-298.

WATCHOUT: the residue column's three "Whatever the failed act left: the slot's directories, or
processes owned by the slot's process group" cells (rows 3, 5, 7) enumerate only DISK and PROCESS
residue. On every row whose failing act can be the runtime close (rows 2, 7, 8), the residue also
includes the session the failed close left open in the pod's shared runtime process. Row 2 names
it; rows 7 and 8 do not. A future sweep of that column must ask, per row, whether the runtime
close is among the acts the row's key admits. — EVIDENCE:
0081_..._.spec-changes.md:650, :655, :656

FACT: the runtime close is an act of the cleanup for the table's purposes even though §5.2's
`**Slot cleanup:**` action list does not name it: rows 2 and 3 split on it explicitly, and the
staged hold paragraph bounds "the cleanup's close of the session on the pod's shared runtime
process". Do not argue a row's residue from the action list alone. — EVIDENCE:
0081_..._.spec-changes.md:650-651, :661

USEFUL [spec.13.fix.1]: its four-bullet record of what the re-key changed, cell by cell, is what
let me check the column in one pass instead of re-deriving the nine rows. Its third bullet
(the three failed-act rows quantified over the act set) is exactly where the runtime-close
omission entered, because the quantified phrasing enumerates only disk and process residue.

USEFUL [spec.14.review-mechanism.1]: its FACT that the §4.9 timers are adapter-local, and its
record that both ENDER-commentary sites were already filed, kept me off two dead candidates.

### [spec.15.review-kubernetes.1]

DECISION: the Kubernetes-idiom lens returns EMPTY on the round-14 staging — BECAUSE every
k8s-facing surface the staging touches (SPEC-4's §4.6.1 projection re-key and §6.2 fence,
SPEC-3's §4.7 `ReportSessionScrub` row, SPEC-6's §16.1 rows) was re-derived against the tree
this round and each one holds — ALTERNATIVES: filing the §4.6.1 status read-back as a
level-triggered violation, and filing the disposition table's last-row retirement clause as a
claim-DELETE coalescing race; both were derived and then killed by the evidence below.

FACT: the §4.6.1 read-back parenthetical is safe because the OccupancyReconciler WATCHES ITS
OWN OBJECT, not merely because it is the sole writer. `SetupWithManager` does
`For(&lennyv1.Sandbox{})` plus `Watches(&lennyv1.SandboxClaim{}, ...)`, and `Reconcile` returns
a bare `ctrl.Result{}` with no requeue. So the hazard is real in principle — the claim DELETE
arrives, `sb.Status.Phase` is read from the manager cache, and a cache that has not yet caught
up with the controller's own earlier `claimed` write falls into the `default: return "", false`
arm and drains nothing — but the controller's own status patch produces a Sandbox update event
that re-enqueues, and the next pass sees `HasClaim=false, Current=Claimed` and drains. A future
finding of the form "the projection reads its own lagging output and can miss the drain" is
answered by the `For(&Sandbox{})` watch, and only by it; if a later change narrows that watch
(a predicate, a `GenerationChangedPredicate`, an `Owns` in its place) the hazard becomes live.
EVIDENCE: pkg/controller/warmpool/occupancy.go:196-206 (the read of `sb.Status.Phase` into
`occupancy.Current`, and `return ctrl.Result{}, nil` on `!ok`), occupancy.go:122-140
(the no-claim switch, `default: return "", false`), occupancy.go:280-289 (`SetupWithManager`).

FACT: the disposition table's last-row clause "A pod serving one session whose claim the failed
bind deletes retires under the §6.2 occupancy projection" needs the pod to project `claimed` at
the DELETE, and it does, with a whole client HTTP round trip of slack. The claim is created and
patched `bound` at `/create`; the entry-creating RPCs that produce the residue the row describes
run inside `Binder.Prepare`, which is called at `/finalize`; `failPhase` then deletes the claim
with no disposition write. A "claim created and deleted inside one reconcile window, so the
projection never observes `claimed`" finding does not survive that gap.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1083 (`failPhase` = `releaseCredentials`
+ `drain`), binder.go:1200-1202 (`drain` is a bare `podclaim.DeleteClaim`),
pkg/gateway/podlifecycle/podclaim/bindingstate.go:68-72 (the `bound` status patch).

FACT: the staging assigns no new Kubernetes actor anything. A grep of spec-changes.md for
`Sandbox`, `SandboxClaim`, `apiserver`, `RBAC`, `finalizer` and `webhook` returns only the two
`ReportSessionScrub` row variants (both carrying the shipped `lenny.dev/drain-request`
annotation clause and its §4.6.3 pointer, unchanged word for word between them) and the §6.2
fence trigger. No staged sentence puts the gateway on `sandboxes/status`, gives the agent pod an
apiserver path, makes a status field an RPC inbox, or adds a finalizer. The coordination this
proposal adds lives on the gRPC wire and in adapter memory, which is the idiomatic side of the
CRD-as-coordination line. EVIDENCE: spec/04_system-components.md:632 ("The gateway holds no
`sandboxes/status` grant"), spec-changes.md:777 and :783 (the row before and after).

USEFUL [Settled: "§4.6.3 gives `Sandbox.status.*` to the WarmPoolController as sole writer, and
both writing reconcilers share one field manager"]: saved re-deriving the field-manager
question. Worth extending with what this round found: the sole-writer fact answers
ForceOwnership/two-managers findings, and the `For(&Sandbox{})` self-watch (above) is the
separate fact that answers convergence findings against the read-back. They are different
objections and the standing context only carried the first.

FACT: `ProjectOccupancyPhase`'s real input set is {HasClaim, Binding, RewarmStarted, Current},
and §4.6.1's enumeration sentence — before and after SPEC-4's edit — names `sessionPolicy` and
omits the rewarm stamp. The omission is pre-existing and unstaged, and `sessionPolicy` survives
on `scrubProfile` and `recycle.maxPodUptimeSeconds`, so neither is a defect in this staging.
Do not file the mismatch as an enumeration gap. EVIDENCE: pkg/controller/warmpool/occupancy.go:33-45
(the `occupancy` struct), spec/04_system-components.md:409 (the shipped enumeration).

### [spec.15.review-mechanism.1]

FACT: the §5.2 disposition table's OUTSIDE-`Shutdown` rows (6 and 7) are the only rows whose
performer set includes a cleanup that closes the runtime, and the table's residue column does not
say so. `terminateHeldSession` (pkg/adapter/holdstate.go:248-250) calls `s.Runtime.Close` and
discards the error, after `deregisterStartedSessions` already deregistered in pass 1
(pkg/adapter/holdstate.go:190). `releaseSessionSlot` (pkg/adapter/slotsession.go:214-220) reaches
no `Runtime.Close`, and `Server.DemoteSDK` (pkg/adapter/sdkwarm.go:280-298) closes BEFORE it
deregisters, so the §10.1 hold-timeout termination is the ONE performer in rows 6/7 that can fail a
runtime close after the deregistration. — EVIDENCE: pkg/adapter/holdstate.go:248, spec-changes.md:655

FACT: the defect has two horns and both land on row 7. If the runtime close counts among "the acts"
(which rows 1-3 assume, since row 3 reads "The runtime close succeeds and any other act fails"),
row 7 fires and its residue cell omits the session left open in the shared runtime process. If the
close does NOT count as a cleanup act (the §5.2 `**Slot cleanup:**` action list never names it),
the hold-timeout close failure falls to row 6, whose residue cell reads "No state is left", which
is flatly false. Present both horns to a fixer; a one-clause residue addition to row 7 closes it
either way. — EVIDENCE: spec-changes.md:654-655, spec/05_runtime-registry-and-pool-model.md:545

WATCHOUT: the row-2 residue ("the session the failed close left open in the pod's shared runtime
process") was ADDED in the r13→r14 fix. That fix re-keyed the whole last column from "what ends the
state" to "what state is left" and repaired the `Shutdown` rows only; it did not sweep the
outside-`Shutdown` rows for the same residue class. Whenever a column is re-keyed on this table,
re-derive every row's cell from the performer set in its KEY column rather than editing the rows
the finding named. — EVIDENCE: spec-changes.md:650 vs :655

FACT: re-verified clean under the mechanism lens this round, so a later round need not redo them.
§4.1/§4.7 `Shutdown`/§4.7 `DemoteSDK` verbatim anchors (spec/04_system-components.md:157, the
Gateway→Adapter table's two rows); §5.2 anchors at :453, :545, :561; §12.6 anchors at :481, :494;
§7.2 anchors at :210, :213, :214; §6.2 fence at :146-155 and projection prose at :80;
`ProjectOccupancyPhase`'s no-claim arm (pkg/controller/warmpool/occupancy.go:126-140) matching
SPEC-4; the `!boundRemains` graceful-shutdown gate (pkg/adapter/session.go:238-260); and
spec/06_warm-pod-model.md:69 backing the `DemoteSDK` row's "preConnect only at
maxConcurrentSessions: 1". — EVIDENCE: spec/06_warm-pod-model.md:69

FACT: the §5.2 hold paragraph's three-way bound on "the cleanup's close" is COMPLETE, and it looks
incomplete. It names the `Shutdown`'s graceful window, that request's own deadline, and ten seconds
for the hold-timeout pass, and omits the SDK demotion and the failed-start handler only because
neither of those cleanups closes a runtime at all (`releaseSessionSlot` closes nothing; the
demotion's close is `sw.DemoteSDK`, outside the cleanup). Do not file the omission.
— EVIDENCE: pkg/adapter/slotsession.go:214, pkg/adapter/sdkwarm.go:280

UNVERIFIED: the staged §5.2 biconditional's ground clause, "that count records the sessions that
process has been given", is not exactly true: a runtime-given session released by the §10.1
hold-timeout termination files no report and is not counted. The undercount is SHIPPED
(`reportSessionScrub` has one caller), so it is not a behaviour change, and the clause is a
"because" rather than a characterisation. Judged below the bar this round; a later round that wants
it owes an argument that the clause reads as a definition of the counter.

### [spec.15.review-performance.1]

DECISION: returned an EMPTY findings list for the performance/scalability/failure-mode lens at spec round 15 — BECAUSE every write-rate, serialization and failover question I could quantify came out neutral or favourable, and the two candidates that looked like reliability regressions are both explicitly owned by the proposal's accepted-failure-modes section and by settled-mechanism directives — ALTERNATIVES: filing the gateway-crash-stranded-entry regression (barred: the mechanism is settled and the only remedy is an alternative design; a related filing was already refuted on materiality) and filing the unanswered-reclaim-enters-`leaked` pool-churn risk (rejected: the proposal names pod retirement through the §5.2 threshold as the intended bound, spec-changes.md:82-84, so it is a preference between workable designs).

FACT: the staging is write-rate NEUTRAL-TO-NEGATIVE on every store the lens covers, and this is the arithmetic a future perf lens can reuse rather than redo. (1) Postgres: SPEC-3 re-keys the `sessions_served` increment from "each session release" onto "each cleanup-outcome report" (spec-changes.md:806-812, :828-836), and the report biconditional withholds the report on every pre-`running` release and on every release outside a `Shutdown` (spec-changes.md:594), so the increment count per pod is a strict subset of shipped. No new Postgres column, row or index. (2) etcd/apiserver: nothing in the staging adds a status write, an annotation write or a net-new informer watch; SPEC-4's §4.6.1 re-key changes the projection's INPUT (the phase the pod currently projects) and not its write frequency, and `ProjectOccupancyPhase` is a pure function over an already-assembled `occupancy` value (pkg/controller/warmpool/occupancy.go:84). (3) Redis: untouched. (4) The token and the reclaim hold are adapter-process in-memory only and cross no store.

FACT: the new metric rows introduce no cardinality growth, because `k8s_pod_name` and `pool` are the labels §16.1 already uses on the neighbouring slot series — EVIDENCE: spec/16_observability.md:14 (`lenny_slot_failure_total`, labeled by `error_type`, `pool`, `k8s_pod_name`) and :15, against spec-changes.md:1231-1232. Do not file a label-cardinality finding on SPEC-6.

FACT: the reclaim hold is keyed on the SLOT IDENTIFIER, which §5.2 fixes as the session's own identifier (spec/05_runtime-registry-and-pool-model.md:395, "the gateway mints one identifier at claim time, and that single value is both the session's identifier and its slot's identifier"). A hold "held for the life of the pod" therefore blocks exactly one session id and is not a pod-wide or pool-wide bottleneck; a later session on the same pod carries a different identifier and never meets it. A lens that reads "held for the life of the pod" as capacity loss is misreading it — the staging says so explicitly at spec-changes.md:668 ("The identifier hold and the `leaked` occupancy are separate objects, which is why they are separate columns").

FACT: the registry critical section holds the lock across NO I/O. Each of its four steps (spec-changes.md:1048) is in-memory: resolve/create/stamp plus rules 2-7; resolve/confirm/record for a start; the rules-11-to-14 decision plus its deregistration; deregistration plus hold opening. The slow work (`Runtime.Start`, `Runtime.Close`, `removeSlotTree`) all sits outside it, and the settled DECISION has `Shutdown` acquire the per-slot guard only on the destroying arm. There is no per-pod serialization bottleneck to file here.

WATCHOUT: the `leaked` column is NOT the occupancy column, and §5.2 shipped text makes it tempting to conflate them. spec/05_runtime-registry-and-pool-model.md:395 says leaked slots "are retained until the pod terminates and still count against pod occupancy", and :488 says a persistently `leaked` slot "can hold total occupancy above zero indefinitely". Those are true of the `leaked` column only; the rows whose hold is "Held for the life of the pod" with `leaked` "Not entered" (the outside-a-`Shutdown` failed-act row, spec-changes.md:656) pin no occupancy, so the whole-pod scrub can still reach that pod. An earlier round already burned a finding on the inverse confusion.

UNVERIFIED: whether an orphaned process group left by a cleanup that fails OUTSIDE a `Shutdown` is ever reaped short of pod termination. `pkg/adapter/podscrub.go` is purely on-disk (standing context, re-confirmed by its enumeration of `/workspace/slots` and `/run/lenny/slots`), so the whole-pod scrub ends the directories but not the processes, and the table's row for that case reports nothing to the gateway, so no threshold ever retires the pod on that ground. I did NOT file it: the staging here only aligns spec to shipped code behaviour (the handlers discard errors, spec-changes.md:677-681), so no behaviour changes and the remedy would be a code change outside this loop. A future code-lane reviewer should decide whether accumulating orphan process groups on a long-lived recycling pod needs a signal.

### [spec.15.review-reliability.1]

FACT: the round-14 re-key of the §5.2 disposition table's last column (header now "State the
cleanup leaves on the pod") is the ONLY delta since the r13 snapshot; every other hunk in the
diff is the same phrase swap propagated to §7.1, the §5.2 lead-in, the `**Slot cleanup:**`
leaked-outcome replacement, the Design paragraph and summary.md. — EVIDENCE:
proposals/0081_*/0081_*.spec-changes.md:645-659
FACT: the re-key left the residue cells in TWO shapes. Rows 3, 5 and 7 use the hedged
"Whatever the failed act left: ..."; row 2 and row 8 were written out as unhedged conjunctions.
Both unhedged cells are wrong, in opposite directions, and both are filed this round. — EVIDENCE:
spec-changes.md:650, :656
FACT: `Server.Shutdown` runs `removeSlotTree(st)` UNCONDITIONALLY after `Runtime.Close`, inside
the same `if bound` block, and discards its error; a failed close therefore does not leave the
slot's directories behind. CODE-1's staged handler keeps that ordering and only captures the
error as `treeErr`. — EVIDENCE: pkg/adapter/session.go:262-271; non-spec-changes.md:407-432
FACT: `Server.DemoteSDK` returns before deregistering when `sw.DemoteSDK` fails, so on the
SIGTERM `ShutdownDemoteSDK` path a slot the runtime was given keeps a LIVE session in the
pre-connected SDK process. Row 8's residue cell names the directories, the entry and the §4.9
timers and omits that session, which is exactly the omission round 14 repaired in row 2. —
EVIDENCE: pkg/adapter/sdkwarm.go:69-107; spec-changes.md:656
WATCHOUT: `MCPRuntime.Close(ctx, _ string)` ignores the session identifier while
`SocketRuntimeProcess.Close` is sibling-safe, so rule 8's "takes the session back off the shared
runtime process" can tear down co-tenants on an mcp runtime. NOT filed: the shipped `Shutdown`
already calls the same `Runtime.Close`, so it is pre-existing looseness this proposal restates.
It is the same claim the standing-context Open entry records. — EVIDENCE:
pkg/adapter/mcpruntime.go:266; pkg/adapter/socketruntime.go:435-455
DECISION: nothing filed on the hold's life-of-the-pod terminal, the 10s hold-timeout window, the
absent reclaim deadline, the gateway-crash stranded entry or the coordinator-handoff question —
BECAUSE each is already a standing Open, an accepted failure mode in the spec file's Edge-cases
section, or a refuted filing — ALTERNATIVES: refiling them, rejected as closed variants.

### [spec.15.review-security.1]
DECISION: returned EMPTY. — BECAUSE every security-bearing surface the staged spec edits touch either preserves the shipped control or documents shipped behaviour, and the three candidates I developed each died on evidence (below) — ALTERNATIVES: filing the credential-residue-without-expiry-timer case, the `sessions_served` narrowing, and the SPEC-4 scrub-before-idle re-key; each is refuted in the entries below.

FACT: the scrub-before-idle invariant survives SPEC-4 intact, and this is the load-bearing security check on that deliverable. `ProjectOccupancyPhase`'s no-claim arm returns `Idle` only from `state.Reserved` and `Draining` from `state.Claimed`; a `recycling` claim projects `claimed` (spec/04:412), so a claim deleted mid-scrub drains rather than returning an unscrubbed pod to inventory. The staged §4.6.1 bullets ("deleted while the pod projects `reserved`" → idle, "deleted while the pod projects `claimed`" → draining, "because such a pod is unscrubbed") match the function and its comment exactly. — EVIDENCE: pkg/controller/warmpool/occupancy.go:123-141; spec/04_system-components.md:411-416; spec-changes.md:929-943

FACT: a cleanup whose credential-directory removal fails leaves `/run/lenny/slots/{sessionId}/credentials.json` on the pod with its §4.9 expiry timers ALREADY CANCELLED, because `deregisterSlotLocked` cancels every armed timer under `s.mu` and cannot fail, while `slotlayout.RemoveTree` is best-effort over four directories and returns only the first error. On disposition-table row 3 (runtime-given, close succeeds, another act fails) the gateway still sees `released` + clean exit, so nothing drains the pod. NOT A FINDING: both halves are shipped, the staged action list only writes them down, and the whole-pod scrub step 0 purges the file from disk at the next occupancy-zero recycle boundary (reachable on that row, whose `leaked` cell is `Not entered`). — EVIDENCE: pkg/adapter/slotsession.go:174-189; pkg/adapter/slotlayout/tree.go:58-69; spec/05_runtime-registry-and-pool-model.md:461; spec-changes.md:573, :651

FACT: §4.9's direct-delivery expiry-timer contract and §5.2 scrub step 0 both verify word for word against the staged citations. §4.9 at spec/04:1169 requires the adapter to arm a local timer per lease in direct delivery mode and to delete the credential file on fire; step 0 at spec/05:461 is the pre-`cleanupCommands` credential purge. The staged §5.2 action-list replacement and its commentary cite both correctly. — EVIDENCE: spec/04_system-components.md:1169; spec/05_runtime-registry-and-pool-model.md:459-461; spec-changes.md:573-581

FACT: the shipped `Shutdown` handler already gates the whole teardown on `bound := removed && st.sessionID != ""`, so staged rule 11 (no entry → `absent`, neither teardown) is the shipped behaviour and not a new fail-open against a §11.4 revoke that arrives after the entry is gone. — EVIDENCE: pkg/adapter/session.go:238-243

WATCHOUT: the mid-session exemption is the one place in the staged cascade where BOTH fences are bypassed by design. A request marked `mid_session` carries an empty `bind_attempt`, so rule 5 cannot fire (it needs both tokens non-empty), and rule 6 exempts it explicitly. Its whole safety argument is gateway-side admission ("the gateway issues one only for a session the issuing replica holds a live binding for"). A future lens reading rules 5 and 6 in isolation will think the marker is a bypass; it is bounded, and the bound lives outside the adapter. — EVIDENCE: spec-changes.md:1044, :1058-1059

WATCHOUT: do not re-derive the security family. The refuted dresses are already in the standing context (`superseded`/`absent` as pod self-reports, a compromised adapter refusing teardowns, a late `StartSession` orphan holding credentials, the withheld report relaxing `maxSessionsPerPod`). The one live security question on this surface is pre-existing and already an Open: whether the adapter's gateway-facing gRPC server is reachable from the agent container. Every "an in-pod attacker sends X" dress collapses into that Open, and it is not this proposal's. — EVIDENCE: review-log.md `### Open`, "Is the adapter's gateway-facing gRPC server reachable from the agent container"

USEFUL [spec.12.fix-G2.1]: its `leaked`-pins-occupancy reasoning is the key that also settles the round-13 re-key of the last column. The new post-table sentence hedges the scrub with "on a pod that reaches one under this section's scrub model", so the residue column asserts no unreachable ender on the two `Entered` rows. I checked that specifically and it holds.

### [spec.15.review-single-source.1]

DECISION: returned EMPTY — BECAUSE the round-13→15 delta is confined to the §5.2 disposition table's last column (re-keyed from "What ends the state left on the pod" to "State the cleanup leaves on the pod"), its cells, one new post-table sentence, one new commentary paragraph, and the two pointer sentences that name the column, and none of that creates a second stating site for any rule; the rest of the inventory still matches `[spec.12.review-single-source.1]`'s eleven homes — ALTERNATIVES: four candidates worked up and dropped below the bar, recorded under WATCHOUT so round 16+ does not re-derive them.

FACT: the r13→r15 delta is exactly one `diff -ru` hunk set on spec-changes.md (Design's "Every cleanup's disposition is one table" paragraph, the §7.1 fenced paragraph's closing table-pointer clause, the §5.2 table lead-in, header and five cells, the new post-table sentence, the new last-column commentary paragraph, and the `**Slot cleanup:**` leaked-outcome replacement) plus the mirrored SPEC-3 line in summary.md. Everything else is byte-identical to the r13 snapshot. — EVIDENCE: diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/spec-r13-prefix proposals/0081_*/

FACT: the new post-table sentence "Pod termination ends every residue the table names, and the whole-pod scrub, on a pod that reaches one under this section's scrub model, ends the slot's directories." is NOT a copy of §5.2's scrub procedure. Steps 0, 2 and 6 state what the scrub removes as an executable list; the new sentence summarises the consequence for the table's residues and explicitly defers to "this section's scrub model". A reader cannot implement the scrub from it. Nor is it a copy of §6.2's `**`leaked` slot semantics.**` paragraph, whose "a `leaked` slot persists until pod termination" is the ground for the persistent-versus-windowed counting rule rather than a statement about residue. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:459-471, spec/06_warm-pod-model.md:160

FACT: the §5.2 slot-cleanup action list's two added acts have no second home in shipped spec. §6.1's per-session credential lease paragraph states lease REVOCATION at session end, not the adapter's file removal or timer cancellation, and §4.9's direct-delivery row states only arming and `AUTH_EXPIRED` firing. — EVIDENCE: spec/06_warm-pod-model.md:26, spec/04_system-components.md:1169

FACT: §7.4 states nothing about which adapter RPCs a mid-session upload reuses, so §4.7.1's mid-session paragraph is a first statement rather than a copy. Re-verified this round by reading §7.4 whole. — EVIDENCE: spec/07_session-lifecycle.md:438-476

FACT: §15.4's `**SDK-warm demotion contract:**` paragraph and §6.1's two demotion paragraphs state no registry removal, so the staged §4.7 `DemoteSDK` row really is the single spec home of it. — EVIDENCE: spec/15_external-api-surface.md:1469, spec/06_warm-pod-model.md:32,38

WATCHOUT: four candidates that look like (g) this round and are not.
  (1) Rule 15's `superseded` gloss restates rule 13's condition ("either because a different attempt owns it or because the entry carries no token"). Rule 15 is the home of what each OUTCOME VALUE means and cites "answered by rule 13"; rule 13 is the home of the condition. The standing-context trap "every spelling of the `superseded` gloss (rules 13 and 15, the §16.1 row, the docs mirror) moves in one edit" records this partition as settled.
  (2) The two-field rationale is written twice in proposal prose: the Design paragraph "**`Shutdown` names its teardown in two fields.**" and the SPEC-1 commentary under the §4.7 `Shutdown` row, nearly word for word. Rule 10 itself is stated once. A duplicated RATIONALE is "redundancy other than the restated rule", which the lens excludes. Note that standing context's ground for keeping Design ("the two-field rationale lives nowhere else") is loose on this point; do not use it as evidence that the commentary copy is absent.
  (3) The §4.7 `Shutdown` row's "Every request states which teardown it asks for, by carrying either a non-empty `bind_attempt` ... or `unconditional_teardown`" versus rule 10. The row states neither the exclusivity nor the `INVALID_ARGUMENT` answer and names §4.7.1 as the authority in the next sentence, so rule 10 is not implementable from the row.
  (4) The §5.2 reclaim-hold paragraph's completion predicate versus the table's "Slot-identifier reclaim hold" column. The paragraph states the rule, the column applies it per row — the same relationship round 12 recorded for the report column.

UNVERIFIED: the §5.2 reclaim-hold paragraph does not itself state the "held for the life of the pod" terminal; only the table's hold column does. The SPEC-3 commentary nonetheless says "The paragraph states a terminal for a cleanup that does not complete". That is non-landing commentary describing staged text inaccurately, and it is single-source-clean either way, so I did not file it. A mechanism or citations lens should decide whether it is worth one word.

USEFUL [spec.12.review-single-source.1]: its eleven-home inventory and its mechanical fenced-block repeat check let this round be a delta review plus targeted spec/ spot checks instead of a rebuild. Keep it; it has now paid for itself twice.
USEFUL [A residue-disposition cell is changed in the §5.2 table and NOWHERE ELSE...]: the trap is what told me the re-keyed last column is by construction the single home, so the four sites that name it (§7.1, the `**Slot cleanup:**` bullet, §6.2's pre-`running` paragraph, the `**Whole-pod replacement trigger:**` parenthetical) needed only a pointer check rather than a content comparison.

### [f1.cleanup.1]

FACT: the summary file already carried exactly the eight required sections, in the required order, and the `## Summary` container already held `**Problem statement.**`, `**What changes.**`, `**Decisions.**` and `**Watch out for.**` in that order and no prose of its own. No section was added, removed, reordered or renamed, and no block was relocated. — EVIDENCE: headings at summary.md lines 1, 3, 289, 313, 471, 595, 929, 944; labelled parts at 5, 21, 101, 224.

FACT: this firing's items required no summary edit. Items 20 and 21 were refuted at both the gate and the apply, so nothing was staged for them and both remain listed under `## Open decisions for human to make` with the identifiers 20 and 21. Item 27 and every `marker:unscoped` item were `no-edit-needed`, and the summary already carried each as written: item 27 as decision entry 27, and the unscoped markers as entries of `## Defects in the shipped tree that this proposal does not stage`.

FACT: the `## Open decisions for human to make` preamble is true of the entries the section now carries. Entries 21 and 27 each carry a recommendation with its ground, its alternatives and a stated confidence; entry 20 carries the question and its ground with no recommendation. The review log carries both `### Settled` and `### Retired` lists the preamble points at. No preamble correction was owed, because this pass falsified nothing.

FACT: the section carries no `### Retired` block or equivalent, and no meta-list of staged items with a proposed disposition. There was nothing to check against the staged changes and nothing to drop.

WATCHOUT: `**Accepted failure modes.**` sits as a labelled block at the end of `## Non-goals` (summary.md line 416) rather than under a section of its own. It was left in place: its subject, residues this change deliberately does not close, is covered by `## Non-goals`, and moving it to `## Defects in the shipped tree that this proposal does not stage` would re-file design residues as confirmed shipped-tree defects, which is an adjudication a format pass may not make. A later pass that wants it moved should move it as a decision rather than as a format correction.

WATCHOUT: `## Defects in the shipped tree that this proposal does not stage` has a stray blank line between the entry ending at summary.md line 854 and the entry beginning at line 856, which renders the bullet list loose from that point on. It is cosmetic and was left alone under this pass's change-nothing-else rule.

### [prune.3.fix.1]

DECISION: the SPEC-3 §5.2 disposition table's seventh column, "State the cleanup leaves on the pod", is DELETED from the header and from every row, together with the lead-in clause "and what state it leaves on the pod", and the residue is stated ONCE in the paragraph after the table, replacing the enders sentence that paragraph carried: a cleanup act that fails or does not run leaves on the pod what it would have removed or ended, the acts being those the `**Slot cleanup:**` bullet states and the close of the session on the pod's shared runtime process; a cleanup that did not deregister the entry also leaves the registry entry and its armed §4.9 lease-expiry timers; pod termination ends all of it, the whole-pod scrub, on a pod that reaches one under the section's scrub model, ends the slot's directories; and the last row's retirement clause (a pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection) moves into the same paragraph. The two commentary sites that justified the column are deleted (the SPEC-3 "The last column records a choice ... the only disposition the row has to give" sentences and the SPEC-4 §6.2 "What each pod exit ends is a column of the §5.2 disposition table rather than a statement here" sentence, the latter replaced by nothing). The other six columns and the nine rows are byte-identical. — BECAUSE the column told no adapter or gateway to do anything, no code deliverable read it, and its content was derivable from the action list plus which acts ran; it cost five edit passes in rounds 12 through 15 (introspection round 15, verdict prune), each fix becoming the next finding. — ALTERNATIVES: re-keying the column whole, tried in round 13 (`[spec.13.fix-G1.1]`) and broken within two rounds, because per-row residue must stay consistent with the action list and with a tree where `slotlayout.RemoveTree` returns one error over four directories and no per-slot process-group kill exists, so no cell could be both true of the tree and total over the act set; keeping the column and re-keying rows 3 and 5, whose "processes owned by the slot's process group" residue names an act with no tree act (rejected: the sixth re-key of the same column).

DECISION: the residue rule is worded "fails OR DOES NOT RUN" and its retirement clause carries the one-session qualifier. — BECAUSE the rows where the tree removal never ran (the SDK-demotion-close-fails row and the no-cleanup row) are otherwise uncovered, and a concurrent pod's residue ends at termination or the whole-pod scrub rather than at retirement. — ALTERNATIVES: "a failed act" alone (rejected: leaves rows 8 and 9 without a residue statement); an unqualified retirement clause (rejected: false on a concurrent pod).

DECISION: the rule names the runtime close as an act beside the action list. — BECAUSE the `**Slot cleanup:**` action list does not name the close, while the table's key column ("The runtime close fails") and the reclaim-hold paragraph ("The cleanup's close of the session") both treat it as one of the cleanup's acts, so a rule keyed on the action list alone would not cover the session a failed close leaves open, which `[spec.15.review-mechanism.1]` recorded as the two-horned defect on row 7. — ALTERNATIVES: adding the close to the action list (rejected: a spec edit to shipped text beyond the prune's scope, and the close is not a per-slot act on a pre-`running` slot).

FACT: sites re-pointed in the same edit: the Design paragraph "Every cleanup's disposition is one table" (spec-changes.md), the §7.1 `**Pod-side reclaim on a failed bind.**` block's two clauses (now "the scrub-model paragraph that carries the table states what a reclaim that did not complete leaves on the pod and what ends it" and "the same paragraph states what becomes of the slot state that attempt left on the pod"), the `**Slot cleanup:**` leaked-outcome pointer replacement, the SPEC-3 commentary "The last row's retirement clause rests on" (now "The scrub-model paragraph's retirement clause"), the SPEC-4 rationale "what the last row of SPEC-3's §5.2 disposition table relies on" (now "the retirement clause of SPEC-3's §5.2 scrub-model paragraph"), summary.md's SPEC-3 and SPEC-4 deliverable entries, and checklist step S4's one clause. — EVIDENCE: grep for "leaves on the pod", "State the cleanup", "last column", "seventh column", "left on the pod" and "what ends the state" across the proposal directory returns only ledger history and the new rule.

WATCHOUT: a finding that a cleanup's residue is unstated, wrong, or unscoped is answered by the one post-table sentence or not at all. No per-row residue may be reintroduced: not as a column, not as a per-row clause, not as a footnote keyed on a row, and not as a per-act mapping sentence under the table. File a residue finding only if the sentence is false against the `**Slot cleanup:**` action list or the tree.

CORRECTS [spec.13.fix.1]: its first DECISION (the §7.1 pointer re-keyed to "what state a reclaim that did not complete leaves on the pod"), its fourth DECISION (the column commentary qualified rather than the retirement pointer moved) and its DEFERRED on checklist S4 are superseded: the pointer now names the scrub-model paragraph, the commentary is deleted, the retirement pointer is moved, and the S4 clause is re-keyed onto the paragraph, which discharges the DEFERRED. Its FACT that nothing outside the spec staging was falsified still holds for the edits it describes.
CORRECTS [spec.13.fix-G1.1]: its DECISION to re-key the column and its rejection of "deleting the column outright" (on the ground that rows 8 and 9 carry residue stated nowhere else and the retirement clause is cited twice) are superseded; both grounds are now met by the post-table sentence, which carries the entry-and-timers residue and the retirement clause once. Its FACT about the four column-naming sites and its DEFERRED on S4 are superseded as above. Its WATCHOUT (no per-row scrub decision) stands and is widened by this entry's WATCHOUT.
CORRECTS [spec.13.fix-design-G1.1]: its DECISION and its WATCHOUT that the retirement clause "SURVIVES the re-key in the same cell" and that moving it "cascades into both citations for no gain" are superseded; the clause moved and both citations were re-pointed in this edit. Its FACT line map of the table is stale (the table now has six columns and the rationale paragraph lost its column sentences).
CORRECTS [spec.14.review-mechanism.1]: its WATCHOUT on the two commentary sites is discharged (both deleted); its DECISION not to file the §7.1 "the same table states what becomes of the slot state" clause on the ground that the last row states the ending is moot (the clause now names the paragraph); its UNVERIFIED on whether the trailing enders sentence belongs in §5.2 is closed as moot, since that sentence is now the one residue rule.
CORRECTS [spec.15.fix.1], [spec.15.fix-G1.1], [spec.15.fix-design-G1.1]: their DECISIONs on the wording of rows 2, 7 and 8's residue cells, the WATCHOUT that rows 2, 7 and 8 must keep three scopings, the WATCHOUT that the column is the only carrier of any residue phrase, and the FACT that the residue phrasings exist only in the six table rows are superseded: the cells no longer exist and the one post-table sentence is the only residue carrier. `[spec.15.fix-design-G1.1]`'s UNVERIFIED on row 5's runtime-close residue is moot for the table; whether a pre-`running` slot's failed close leaves anything is answered by the rule's "what it would have removed or ended" without a per-row statement. Their tree FACTs (`Server.Shutdown` removes the tree unconditionally after the close; the hold-timeout termination is the one outside-`Shutdown` performer that closes after deregistering) stand.
CORRECTS [spec.15.review-mechanism.1]: its first FACT, that the table's residue column does not say rows 6 and 7 can close a runtime, and its second FACT's two horns are moot; the rule names the close as an act, so a failed close on any performer leaves the session open under the one sentence. Its WATCHOUT on re-deriving every cell on a re-key has no object.
CORRECTS [spec.15.review-edit-sites.1]: its FACT that the column has exactly three citing sites, all moved with the re-key, is superseded by this entry's FACT listing the sites re-pointed on deletion.
CORRECTS [spec.15.review-single-source.1]: its DECISION's ground that the round-13 to 15 delta was "confined to the §5.2 disposition table's last column" and its USEFUL note that "the re-keyed last column is by construction the single home" now read against a table with no such column; the single home of the residue is the post-table sentence.
CORRECTS [spec.15.review-reliability.1]: its FACT that the residue cells were left in two shapes has no object.
CORRECTS [spec.15.review-performance.1]: its UNVERIFIED on orphaned process groups still stands as a code-lane question, but its ground "the table's row for that case reports nothing" now reads on the report column alone; the residue it names is what the post-table rule states.

### [spec.16.fix-G1.1]

DECISION: closed the round-16 G1 finding by DELETING the tail ", so the pod-warm bind sequence that follows an SDK demotion does not meet it" from the staged §5.2 `**Slot-identifier reclaim hold.**` paragraph (spec-changes.md), and by conditioning DOCS-2's `DemoteSDK` row on the same predicate ("When that cleanup completes, the next bind sequence on the pod creates a fresh entry and stamps it with that attempt's own token.") — BECAUSE the §5.2 disposition table already carries both arms for a release outside a `Shutdown` (cleanup returns clean: hold ends when the cleanup returns; an act fails after the deregistration: held for the life of the pod), so the deleted clause stated no rule the table lacks and stated it wrongly — ALTERNATIVES: deleting the whole sentence (rejected: the surviving half is the only statement that the hold ends before the requesting RPC answers, which the SPEC-1 §4.7 `DemoteSDK` row and the DOCS-2 row both rest on, and the table gives no answer-time bound); rewriting the tail conditionally (rejected: a second full statement of rule 2's failing arm, which the table owns); adding a disposition-table row or per-row clause (rejected: the table's partition is settled and every reachable case already maps to one row); deleting the DOCS-2 row's second sentence (rejected: it removes the reader-facing consequence the row exists to carry and forces an edit to the DOCS-2 commentary).

WATCHOUT: the DOCS-2 `DemoteSDK` row's fresh-entry sentence is a licensed docs restatement (doc-content.md bars it from citing a spec section), so it is the one site in the proposal that can restate a spec consequence in its own words. That makes it the site a spec-lane retraction leaves stale. Whenever a §5.2 hold or §4.7.1 rule-4 predicate changes, check it. EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md:2538

FACT: the SDK demotion's failing-cleanup arm is genuinely reachable, so no universal about "a demotion always leaves no entry" is safe. `Server.DemoteSDK` calls `releaseSessionSlot`, which deregisters and then DISCARDS `removeSlotTree`'s error, and `slotlayout.RemoveTree` returns the first `os.RemoveAll` error over four directories. EVIDENCE: pkg/adapter/sdkwarm.go:296-301; pkg/adapter/slotsession.go:214-220; pkg/adapter/slotlayout/tree.go:58-69

USEFUL [prune.3.fix.1]: the standing-context line recording that the residue column was deleted and that per-row hold disposition is the table's job settled this finding's remedy immediately: the fix is a deletion at the prose site rather than any change to the table.

### [spec.16.fix-G2.1]

DECISION: closed the G2 finding by DELETING the closing sentence of the Edge-cases bind-sequence-refusal bullet ("It makes one replacement §15.1 does not need, because the page states the retryable-fallback exclusion keyed on the cause class where §15.1 keys it on the gRPC code."), leaving the bullet ending at "... the setup-output remedy qualified to the cause that ran a command." — BECAUSE the claim is false against the current staging: SPEC-5 replaces §15.1's exclusion sentence (spec-changes.md, SPEC-5 §15.1 block, "In the same row, replace the exclusion sentence"), so DOCS-3's fallback replacement mirrors a §15.1 replacement rather than standing alone. — ALTERNATIVES: (a) rewrite the sentence to say DOCS-3 mirrors every §15.1 replacement including the exclusion sentence — rejected, that is a second full statement of what `## Spec files touched` already enumerates; (b) keep it and qualify it with why the page's wording differs — rejected, that rationale has its home in the DOCS-3 deliverable commentary ("its reader is a REST client with neither the gRPC codes nor the specification"); (c) edit DOCS-3 or SPEC-5 to remove the asserted divergence — rejected, there is no divergence, the defect was confined to the Edge-cases prose.

FACT: the enumeration of DOCS-3's `error-catalog.md` replacements has exactly one home in the spec staging, the `## Spec files touched` paragraph's closing entry ("`docs/reference/error-catalog.md` takes a sentence replacement mirroring each of SPEC-5's §15.1 replacements, the retryable-fallback sentence among them, and a replaced remedy cell"). The Edge-cases bullet is not a second carrier and must not become one again. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md, `## Spec files touched`, the `docs/reference/error-catalog.md` entry.

CORRECTS [review-log-archive.md:35062, the FACT that the edge-case bullet's closing clause is "true against the page"]: that entry was true when written, before SPEC-5 acquired the §15.1 exclusion-sentence replacement. It is now false as a statement about the bullet, and the bullet it defended has been deleted. Do not restore the clause on the strength of that archived FACT.

USEFUL [review-log-archive.md:54374, MISTAKE (nearly filed)]: an earlier round had already identified that this sentence's stated reason describes the wrong replacement (the extra one is the remedy cell, since §15.1's catalog table has no remedy column) and judged it below the bar as a rationale slip. It saved me re-deriving the remedy-cell point; the sentence has now been removed outright, so both readings of its defect are closed.

WATCHOUT: `implementation-checklist.md` still states that §15.1's "exclusion sentence is keyed on the gRPC code and stands unedited", which is stale against the current SPEC-5 staging. That drift predates this finding and is independent of it, and the checklist is out of bounds for a lens; the reconciliation pass rebuilds it. — EVIDENCE: proposals/0081_.../0081_....implementation-checklist.md, the §15.1 line near the top of the SPEC-5 step.

FACT: no other file needed repair for this edit. It removed prose only, added, removed, merged, split and resequenced no deliverable, and falsified no statement in summary.md, problem-statement.md or non-spec-changes.md. The non-spec DOCS-3 deliverable already states the mirroring correctly ("This deliverable makes one sentence replacement for each of SPEC-5's §15.1 replacements, plus one remedy-cell replacement") and was left untouched.

### [spec.16.fix-design-G1.1]

DECISION: close finding 1 by deleting only the tail clause "so the pod-warm bind sequence that follows an SDK demotion does not meet it" from the staged reclaim-hold sentence (spec-changes.md:663), keeping "A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers when that cleanup completes." — BECAUSE the surviving half carries a fact the §5.2 disposition table does not: the hold ends before the requesting RPC answers (the §4.7 `DemoteSDK` row and the DOCS-2 row both rest on that timing). — ALTERNATIVES: deleting the whole sentence as a restatement of the table's "Ends when the cleanup returns" rows (rejected: the table gives no answer-time bound, and the two rows that depend on it would be left ungrounded); rewriting the tail conditionally ("and the pod-warm bind that follows an SDK demotion meets the hold only when that cleanup failed") (rejected: a second statement of rule 2's outcome, already given by disposition rows spec-changes.md:655-657).

DECISION: the DOCS-2 `DemoteSDK` row (non-spec-changes.md:2538) is IN SCOPE and is corrected in the same edit, by conditioning its second sentence: "When that cleanup completes, the next bind sequence on the pod creates a fresh entry and stamps it with that attempt's own token." — BECAUSE it is the same retracted universal in the one carrier a reader sees, and doc-content.md bars it from citing the rule, so the licensed docs restatement must not over-claim. — ALTERNATIVES: deleting the sentence (rejected: the DOCS-2 commentary at non-spec-changes.md:2505-2523 states the row exists to carry rule 4's fresh-entry consequence, so deleting forces a commentary edit and loses reader content); adding the failing arm to the docs row (rejected: that restates the reclaim hold in a page that cannot cite it — hair).

FACT: no tier-11 gate reads the `DemoteSDK` row. `grep -rn DemoteSDK tests/` returns only claim-map.json:353 and two fixture comments; the `lineContaining` gates cited in DOCS-2 are for the `Shutdown` and `ReportSessionScrub` rows. — EVIDENCE: tests/claim-map.json:353, tests/testinfra/fixtures/fixtures.go:41

FACT: the failing arm is the TREE-REMOVAL failure, not the runtime-close failure. `Server.DemoteSDK` returns before any deregistration when the close fails (pkg/adapter/sdkwarm.go:296-301, its own disposition row at spec-changes.md:658), while `releaseSessionSlot` deregisters and then discards `removeSlotTree`'s error (pkg/adapter/slotsession.go:214-220), which is disposition row spec-changes.md:657, "Held for the life of the pod". Do not conflate the two rows when reasoning about this sentence.

WATCHOUT: spec-changes.md:334-337 (SPEC-1 commentary) reads "§4.7.1 rule 4 ..., under which the bind that follows a demotion creates a fresh entry stamped with its attempt's token, only because the demotion left no entry for that identifier." That is a statement of what rule 4 gives and why the row matters, not a claim about the cascade's outcome (rule 2 preempts rule 4 on the failing arm). It is rationale, it was inspected by this round's verifiers and left standing, and it is NOT part of this edit. A later lens that files it should be answered this way rather than by adding a conditional there. — EVIDENCE: proposals/0081_*/*.spec-changes.md:334-337

WATCHOUT: spec/05_runtime-registry-and-pool-model.md carries no "reclaim hold" text; the whole mechanism is staged only in this proposal, so there is no landed tree site to keep in step with either edit.

### [spec.16.fix-design-G2.1]
DECISION: delete the trailing sentence of the Edge-cases bullet ("It makes one replacement §15.1 does not need, because the page states the retryable-fallback exclusion keyed on the cause class where §15.1 keys it on the gRPC code.", spec-changes.md:241-243) and change nothing else — BECAUSE SPEC-5 does replace §15.1's exclusion sentence (spec-changes.md:1167-1177), so the claim is false; the enumeration of DOCS-3's replacements has one home at "## Spec files touched" (spec-changes.md:1331-1332) and the rationale for the page's own voice has one home in DOCS-3 (non-spec-changes.md:2577-2581) — ALTERNATIVES: rewriting the sentence to say DOCS-3 mirrors all four replacements (rejected: a second full statement of what :1331-1332 already enumerates, and hair on a bullet whose subject is the refusal envelope); editing the DOCS-3 deliverable (rejected: DOCS-3 is already correct).
FACT: the bullet's preceding sentence ("DOCS-3 mirrors those same replacements into the `SETUP_COMMAND_FAILED` row ...") is self-contained; deleting the trailing sentence leaves the bullet grammatical and complete — EVIDENCE: spec-changes.md:238-243
DEFERRED [implementation-checklist.md]: implementation-checklist.md:17 still says "The row's exclusion sentence is keyed on the gRPC code and stands unedited". That is false against the current SPEC-5 staging, which replaces the exclusion sentence. It was already false before this fix and is not caused by it; the caller's directive says the checklist is stale by design and the reconciliation pass between the loops rebuilds it.

### [spec.16.review-citations.1]

DECISION: filed exactly one finding, the Edge-cases claim that DOCS-3 "makes one replacement §15.1 does not need" — BECAUSE it is round-7 residue (introduced in 096b39656, before SPEC-5 gained its fourth §15.1 replacement) and now contradicts spec-changes.md's own "Spec files touched" paragraph and non-spec DOCS-3's heading — ALTERNATIVES: filing the "position 2 of the gateway-runtime-comms remediation plan" reference (rejected, see WATCHOUT); filing the §28.4 ABSENT-row precedent (rejected, see WATCHOUT).

FACT: the whole citation surface of spec-changes.md re-verifies clean as of 72ac374e3, re-run from scratch this round. All 24 verbatim anchor blocks that are meant to exist hit exactly once across `spec/*.md`; all 78 markdown links resolve to a real heading, and every same-file `](#...)` sits inside an edit to the file that owns the heading. Every backticked file path exists. Cheap reproductions: the fenced-block counter python snippet in Settled #189, and a slug-based anchor checker over `spec/*.md` headings. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:254,294,325,439,462,474,503,522,546,569,591,711,728,749,773,799,811,826,959,971,1134,1146,1158,1170,1204

FACT: the SPEC-3 carrier table's 24 rows all re-verify: every named file exists and carries the claimed "on/at every session release" comment, including the three distinct comments in scrubreport_server.go (:66 handler, :195 SessionCountRetirer, :441 RecordSessionScrub), the two `// spec:` annotations in the tier-4 file (:48, :120) and `podStateGatewayWrittenSentence` (tests/tier11_docs/spec_28_register_writers_test.go:99). Do not re-derive this table. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:66,195,441

WATCHOUT: "staged at position 2 of the gateway-runtime-comms remediation plan" (spec-changes.md:188, and two parallels in summary.md:260,455) looks like a false citation and is NOT filable on the evidence available. `gateway-runtime-comms-remediation.md` has steps R1-R25 and waves, no "position" concept, and its step 2 is the spec modularization (R2); the problem statement itself says "The gateway-runtime-comms remediation programme's step list carries no step touching the adapter slot registry's failed-bind compensation". But "position" in this proposal's own vocabulary means a QUEUE position (problem-statement.md:318-320, "Files a later position also rewrites"), which makes the claim a forward-looking planning statement rather than a citation of existing text. A later round that wants it owes a reading that distinguishes plan step from queue position. — EVIDENCE: gateway-runtime-comms-remediation.md:143,476,728; proposals/0081_*/0081_*.problem-statement.md:318,326

WATCHOUT: spec-changes.md:1119-1120 says the §28.4 `ABSENT` row follows "the rows the `coordination_generation` fence already carries". Every `coordination_generation` row in `tests/claim-map.json` is `UNWIRED`, not `ABSENT`. Judged below the bar because the register does hold a generation-fence ABSENT row ("In-flight RPC cancellation on a generation gap") and the sentence names the status it wants explicitly, so the precedent clause is decorative. Do not file it without showing an implementor would write the wrong status. — EVIDENCE: tests/claim-map.json (grep `coordination_generation`); spec/28_communication-channels.md:164-165

USEFUL [the standing context's Settled #189 and #209]: both held on re-verification after the three hand restructures (9c23121af, 9c59dabec, 72ac374e3). Re-running the two mechanical sweeps cost about ten minutes and is the right first move for this lens; the yield is in the prose commentary, not in the anchors.

FACT: no text in spec-changes.md still references the deleted disposition-table residue column (grep for "residue column", "State the cleanup leaves", "seven columns" is empty), so `[prune.3.fix.1]` swept its own file clean.

### [spec.16.review-fresh.1]

DECISION: returned EMPTY on the round-16 staging — BECAUSE I re-ran the mechanical checks that cover
the whole file (anchor uniqueness, link resolution, insertion points) and then spot-checked the six
areas the hand edits touched since round 11 (`9c23121af`, `9c59dabec`, `72ac374e3` and the two
commits after it), and every candidate I worked up died on the tree or on a standing-context trap —
ALTERNATIVES worked up and dropped: (a) the post-table residue rule attributing the §4.9 timer
cancellation both to the `**Slot cleanup:**` action list and to the deregistration clause — not a
contradiction, the second clause only covers the case where no deregistration happened, and the
prune's WATCHOUT bars any per-row residue remedy; (b) the residue rule's whole-pod-scrub clause
against `[spec.12.fix-G2.1]`'s finding that the scrub is unreachable on a `leaked` row — the
qualifier "on a pod that reaches one under this section's scrub model" already carries that
exclusion, which is why the prune worded it so; (c) the §15.1 retryability replacement's gloss
"another start already holds the slot identifier" — a gloss on rule 6, and the split it rests on is
open decision territory; (d) re-filing the round-11 graceful-shutdown triple (see OPEN below).

FACT: the whole-block anchor sweep re-runs clean on the current file. 64 fenced blocks; every
"reads, verbatim" original hits `spec/*.md` EXACTLY ONCE, and the zero-hit set is exactly the
replacement texts, the two new fence entries, the SPEC-4 `claimed ──→ draining` replacement and the
two §16.1 rows — no new zero-hit block appeared after the three hand edits. Every markdown anchor in
a staged block resolves against a real heading slug. The two scripts are ~20 lines each (extract
fenced blocks, count occurrences / slug every heading and match `](file.md#anchor)`); re-run them as
the first command of a round rather than re-deriving. — EVIDENCE:
proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md (all 64 blocks)

FACT: §7.1's staged reclaim paragraph lands INSIDE a markdown code fence. spec/07_session-lifecycle.md
opens a bare ``` at :5 and closes it at :54, so the shipped atomicity paragraph at :23 and the line
continuing it at :24 are both inside it, and the new paragraph's `[§15.1](...)` links will render
literally. This is shipped style (the atomicity paragraph already carries a `[§6.2](...)` link inside
the same fence), so it is not a finding — but do not "fix" it by moving the paragraph out of the
fence, which would break the step-8/continuation anchoring the staging depends on. — EVIDENCE:
spec/07_session-lifecycle.md:5, :23-24, :54

FACT: `requireLine(s47, "| `Shutdown` |")` in tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65
is safe against the new §4.7.1 block although the staged carriage table contains a row literally
beginning "| `Shutdown` | pairs with `unconditional_teardown` under rule 10 |". `specSection` slices
from "### 4.7 " and `lineContaining` returns the FIRST match, and the Gateway → Adapter RPC row at
spec/04_system-components.md:686 precedes the §4.7.1 insertion point (between :693 and :695). A
future edit that moves the carriage table above the RPC table would turn that gate red silently. —
EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:57-65, :74-100;
spec/04_system-components.md:686, :688-695

FACT: the post-table residue rule's whole-pod-scrub clause is true of the scrub as spec/05 states it.
Step 0 removes every `/run/lenny/slots/{sessionId}/credentials.json` and step 2 removes every
per-slot tree under `/workspace/slots/`; step 6's stat-check is the scrub's own failure predicate
rather than a refusal to remove. So "ends the slot's workspace tree and credential file" holds. —
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:461, :467, :471

FACT: the `DemoteSDK` row's ground that §6.1 admits `preConnect` only at `maxConcurrentSessions: 1`
is stated in prose and in a compatibility table, so the row's "the entry it removes is the registry's
single entry" is sound. — EVIDENCE: spec/06_warm-pod-model.md:69, :75

OPEN: the round-11 FILED item "The graceful-shutdown-signal condition is stated at three sites" is
still live and still unfixed — the §4.7 `Shutdown` row states the condition, the §29.4 step-13 append
restates the co-tenant half of it ("an end that leaves a bound co-tenant writes no frame") while
citing the row, and the Edge-cases bullet restates it with no citation. Rounds 12 and 15's
single-source lenses both returned EMPTY without naming it, and no `[spec.1x.fix*]` entry discharges
it. I did not re-file it: it is already in `### Open` as FILED, and re-filing costs two verifiers
without closing anything this loop is converging. Whoever runs the pre-certification sweep of
`### Open` for FILED items should either land the reduction (delete the restating clause from the
§29.4 append and cite the row from the Edge-cases bullet) or record it as refused. — EVIDENCE:
review-log.md:582; spec-changes.md, the §29.4 step-13 staged sentence and the Edge-cases
`**The graceful-shutdown signal on a co-tenanted pod.**` bullet

USEFUL [The verbatim-anchor sweep is DONE and was re-run independently in rounds 2, 4, 5, 7, 10 and 11]:
its description of the cheap python form and of which blocks are legitimately zero-hit is what let a
fresh lens clear the whole anchor class in two commands instead of a pass.
USEFUL [MISTAKE, FIVE edit passes in four rounds on ONE disposition-table column]: it killed candidate
(a) above before I spent a round deriving a per-row remedy the prune has already barred.

### [spec.16.review-mechanism.1]

DECISION: returned EMPTY under the end-to-end mechanism lens — BECAUSE every flow I traced
(concurrent bind via `materializeSlot`, create-time-reserved via `BindReservedSlot`, exclusive
`Prepare`@/finalize + `Launch`@/start, `Binder.Resume`, SDK-warm demote-then-bind, the
occupancy-zero two-`Shutdown` release, the reclaim-vs-start race, the mid-session upload) resolves
under rules 1-15 plus the §5.2 hold and disposition table with no unreachable trigger, no bypassable
gate, no wrong default and no predicate drift I could substantiate — ALTERNATIVES: I considered and
declined four candidates, each recorded below so a later round does not re-spend them.

FACT: the §4.7.1 rationale "a start may be issued by a later stage of the same binding than the stage
that created the entry" is SOUND, and the case it names is the EXCLUSIVE pool, not the concurrent one.
`Binder.Prepare` sends PrepareWorkspace/FinalizeWorkspace/RunSetup/AssignCredentials at /finalize and
`Binder.Launch` sends only StartSession-or-ConfigureWorkspace at /start — EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:843-960 (Prepare's four RPCs), :976-1000 (Launch's
reconnect + start). So the two starts carrying no token is what keeps the exclusive path working;
had `StartSession` carried a token it would be attempt 2's and would mismatch attempt 1's stamp under
rule 5. On a CONCURRENT pool the whole step-5 sequence is one attempt inside `materializeSlot`, so the
rationale's case does not arise there. Do not "correct" that sentence toward the concurrent path.

FACT: `connectSlot`'s only pod-side RPC before the workspace stages is `NegotiateVersion`, and
`NegotiateVersionRequest` carries NO session identifier — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:464 (`cl.NegotiateVersion`),
schemas/lenny-adapter.proto:1706-1714 (two repeated-string fields, no `session_id`). This is what makes
§7.1's "first pod-side RPC **for the session**" and the Edge-cases bullet "A bind abandoned at the
connect stage ... no reclaim is owed" agree. A round that reads §7.1 as triggered by the dial/handshake
and files the connect-stage bullet as a contradiction is wrong; the qualifier "for the session" is
load-bearing and must not be dropped in any future reduction of that phrase.

FACT: the `DemoteSDK` row's three claims all re-verify. The close precedes the deregistration
(`sw.DemoteSDK` returns `codes.Internal` before any release), the release names the registry's single
entry via `anyRegisteredSession()`, and its cleanup is synchronous inside the handler before it answers
— EVIDENCE: pkg/adapter/sdkwarm.go:280-302, :309-316. §6.1's "preConnect admitted only at
maxConcurrentSessions: 1" re-verifies at spec/06_warm-pod-model.md:69 and :75.

FACT: SPEC-4's re-key matches `ProjectOccupancyPhase` exactly on both claim-deletion arms —
`state.Reserved` with no claim → `Idle`, `state.Claimed` with no claim → `Draining`, every other
current phase → `("", false)` — EVIDENCE: pkg/controller/warmpool/occupancy.go:138-151. All four
SPEC-3/SPEC-5/SPEC-2 verbatim anchors I spot-checked hit byte for byte:
spec/05:453, :488, :545, :561; spec/12:481, :494; spec/07:210, :213, :214, :414;
spec/06:234; spec/04:157, :686, :692, :854; spec/29:711.

WATCHOUT: the §5.2 post-table paragraph's sentence "A slot enters `leaked` on the gateway's reading of
the report or of the response, so a §7.1 reclaim the adapter does not answer enters it as a reclaim
whose act fails does" READS as contradicting the table's row-1 `leaked` cell ("Not entered") for a
cleanup that ran clean on the pod but whose answer was lost. It is not one: the table keys on what
happened on the pod and the sentence declares the column gateway-side. I nearly filed this and stopped.
A future round that wants it owes an argument the qualifying sentence does not already make, and the
remedy could only be a deletion, never a new row or a per-row clause (`[prune.3.fix.1]` bars both).

WATCHOUT: the pre-`running` cleanup trigger is phrased at three sites — §5.2's append ("after the slot
enters `receiving_uploads` and before it reaches `running`"), the §6.2 fence entry's parenthetical
("before the runtime has been given the session, a start still in flight included") and the §6.2
`**Pre-`running` slot cleanup.**` paragraph. I checked all three for drift and they agree. I declined to
file it as a rule (g) duplication because the three are at different granularities (who performs / the
edge exists / where the boundary sits) and because the trap on that boundary records seven rounds spent
rewriting it, with "reintroducing a classification, list or trigger in §6.2" named as the repeat defect.
A round that does want the reduction should delete the fence parenthetical, not re-word the paragraph.

UNVERIFIED: whether a `Shutdown` whose cleanup ends at disposition-table row 3 (runtime close clean,
another act fails → reports `released`, clean-exit Set, identifier held for the pod's life) can strand a
CLIENT RETRY of the same session on the same pod indefinitely. The hold is keyed on the slot identifier,
which is the session identifier, so a NEW session is unaffected, but a §7.3 resume or a client retry of
the same session onto that pod meets a hold nothing will release. I judged it below the bar because the
retry is answered `ABORTED`, spends its last attempt under `maxSlotRetries = 1` and lands elsewhere,
which the accepted-failure bullet on the reclaim hold already covers. Whoever owns the entry-reaper
finding at remediation position 2 should confirm that reading.

### [spec.16.review-reliability.1]

DECISION: filed exactly one finding, the unconditional "so the pod-warm bind sequence that follows an SDK demotion does not meet it" tail of the staged §5.2 `**Slot-identifier reclaim hold.**` paragraph (spec-changes.md:663), against disposition-table row 7 (:657, "Held for the life of the pod") — BECAUSE the antecedent is conditional ("when that cleanup completes") and the conclusion is not, and `releaseSessionSlot` discards `removeSlotTree`'s error, so the failing arm is reachable on the demotion path — ALTERNATIVES: adding a row or a per-row clause (barred by the caller directive); re-wording the table (settled); leaving it (rejected: an implementor reading the normative sentence would write a conformance assertion that the demotion path never meets the hold). The remedy is a DELETION of the "so ..." tail; the general rule and the table already cover the case.

FACT: `Server.DemoteSDK` closes the runtime via `sw.DemoteSDK` and returns `codes.Internal` before touching the registry; on success it calls `noteRuntimeClosed` then `releaseSessionSlot`, and `releaseSessionSlot` is `deregisterSlot` → `_ = removeSlotTree(st)` → `cancelPodMCPIfRuntimeIdle`, with the tree error DISCARDED. So the demotion's cleanup can fail silently after the deregistration. — EVIDENCE: pkg/adapter/sdkwarm.go:280-302; pkg/adapter/slotsession.go:214-220

FACT: `slotlayout.RemoveTree` loops over slotRoot, Sessions, Artifacts, CredentialsDir and returns only the FIRST error; an already-absent tree is nil. Re-verified for this round. — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69

FACT: `onHoldTimeout` pass 2 shares ONE 10s context across every member, which is where §5.2's staged "graceful window of ten seconds" comes from; the figure re-verifies. — EVIDENCE: pkg/adapter/holdstate.go:201

UNVERIFIED: whether a `Runtime.Close` that ignores its `sessionID` argument (`MCPRuntime.Close(ctx, _ string)`, `InProcessRuntime.Close`) makes rule 8's "takes the session back off the shared runtime process" tear down co-tenants. I did not file it: the shipped `Shutdown` teardown uses the same primitive at every co-tenanted session end, so the exposure predates the staging. Whoever wants it owes the argument that rule 8 widens it. — EVIDENCE: pkg/adapter/mcpruntime.go:266, pkg/adapter/embedded.go:188

MISTAKE (avoided): I nearly filed that the scrub-model biconditional's rationale ("that count records the sessions that process has been given") is falsified by the §10.1 hold-timeout termination, which releases a runtime-given slot and files no report. It is shipped behaviour (`terminateHeldSession` "reports no §5.2 session scrub", holdstate.go:216-220), so the staging records rather than creates it.

USEFUL [standing context, Traps]: the "loose summary, not a false citation" precedent (`ProjectOccupancyPhase`) and the residue-column MISTAKE entry together cut four candidates before I spent a read on them.

### [spec.17.review-citations.1]
FACT: Round 17's spec-lane diff against the r16 snapshot is 35 lines over two files, both deletions closing the two named earlier findings: spec-changes.md drops the DOCS-3 "makes one replacement §15.1 does not need" sentence from the setup-window Edge-cases bullet, and drops ", so the pod-warm bind sequence that follows an SDK demotion does not meet it" from the `**Slot-identifier reclaim hold.**` paragraph. non-spec-changes.md gains "When that cleanup completes," on the `DemoteSDK` row (the fixer's propagation, non-spec lane). — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:238, :661
FACT: the step-2 sweep for sites left stale by those deletions returns nothing. The only other spec-lane sites naming the demotion/hold interaction are the SPEC-1 `DemoteSDK` commentary, which states the pre-replacement row counterfactually ("had nothing in the applied specification to close it"), and the disposition table's own demotion rows; both stand. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:334-341, :654-656
FACT: re-verified the two repository citations the `DemoteSDK` block rests on. The §4.7 row quoted "verbatim" matches spec/04_system-components.md:674 exactly, and "§6.1 admits `preConnect` only at `maxConcurrentSessions: 1`" matches spec/06_warm-pod-model.md:69 ("SDK-warm mode (`preConnect: true`) is admitted only in session mode with `maxConcurrentSessions: 1`"), which sits inside §6.1 (§6.2 begins at :80). — EVIDENCE: spec/04_system-components.md:674, spec/06_warm-pod-model.md:69
FACT: the surviving DOCS-3 description is self-consistent after the deletion. SPEC-5's §15.1 block stages four sentence replacements (opening, retryability, closing/remedy, exclusion) and the summary's DOCS-3 entry mirrors "each of SPEC-5's §15.1 replacements, the retryable-fallback sentence among them, and a replaced remedy cell"; the non-spec DOCS-3 heading says "four sentence replacements and a replaced remedy cell". No count or set mismatch. — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:1120-1178, :1329-1333; 0081_*.non-spec-changes.md:2563
DECISION: returned no findings for the citation lens this round — BECAUSE both hunks remove text and add no citation, and every claim they leave reachable verifies against the tree — ALTERNATIVES: filing the SPEC-1 counterfactual sentence at :340 as a stale claim, rejected because it describes the row as it stands before the replacement, which is still accurate.

### [spec.17.review-reliability.1]

DECISION: returned no findings this round — BECAUSE the round-16→17 diff is three deletions/wording
changes only (spec-changes.md Edge-cases bullet loses the "DOCS-3 makes one replacement §15.1 does
not need" sentence; the §5.2 `**Slot-identifier reclaim hold.**` paragraph loses the trailing
"so the pod-warm bind sequence that follows an SDK demotion does not meet it" clause; non-spec
`DemoteSDK` row gains "When that cleanup completes,"), and each deletion removes a claim rather
than adding one. — ALTERNATIVES: filing the demotion-failure "held for the life of the pod" outcome
as an unreclaimed resource was rejected: it is the settled DECISION recorded in standing context,
and the hold is keyed on the session's slot identifier, so a pod returned to pod-warm state binds
later sessions under different identifiers.

FACT: after the round-16 deletion, the reclaim-hold paragraph's last substantive sentence is the
general one ("A release that runs its cleanup inside the RPC that requested it ends the hold
before that RPC answers when that cleanup completes."), and the SDK-demotion conclusion is now
derived, not stated. The two sites that rest on it are still coherent: the SPEC-1 `DemoteSDK`
row commentary cites it as "the §5.2 slot-identifier reclaim hold's sentence on a release that
runs its cleanup inside its own RPC" — EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:334, :661

FACT: step-2 sweep for the deleted clause found no stale restatement. `grep -rn "inside the RPC\|
inside its own RPC\|inside the call\|inside this request"` over the proposal directory returns
five sites (spec-changes :329, :334, :661; non-spec :2511, :2535/:2538; summary :964) and all
agree with the reduced wording. `pod-warm bind` survives only at spec-changes :340 (a statement
about the spec BEFORE the edit) and :604 (re-bind counting), neither of which asserts the deleted
conclusion. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:340, :604

### [spec.18.fix.1] — corrections appended to round 18's fix pass, not a new pass

The round-18 fixer trimmed the staged §29.4 step-13 sentence and updated the summary's parallel
but wrote no shard of its own, so this subsection is the round-18 fix pass's shard and carries
both its record and the post-fix correction. The post-fix review found one defect, the stale
parallel in the implementation checklist, and that file is out of bounds for this lane.

FACT: the round-18 spec-lane edit is three hunks over two files. The staged §29.4 step-13 sentence
loses its trailing clause and now reads, in full, "On a pod serving concurrent sessions this step
occurs only under the condition the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown`
row states for the graceful-shutdown signal."; the Edge-cases `**The graceful-shutdown signal on a
co-tenanted pod.**` bullet becomes a citation of the §4.7 row plus the reason the condition is
keyed on the binding; §15.1's replacement commentary drops the workspace-materialization example.
The summary's SPEC-1 index entry was re-keyed in the same pass. — EVIDENCE:
proposals/0081_.../0081_....spec-changes.md:221-223, :363, :1182-1184; summary.md:946

DEFERRED [implementation-checklist.md:19, step S2 (SPEC-1), final sentence]: it still reads
"§29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and
cites §4.7." The trim makes that false, and it disagrees with both the staged sentence and the
summary's index entry. The checklist is the instruction an implementer executes, so the stale
line directs S2 at text the staging does not contain. Replace that final sentence with the
summary's current wording, "§29.4's session-end step 13 cites §4.7 for the graceful-shutdown
signal's co-tenancy condition and states nothing about it itself." No other clause of the S2 line
changes, and no deliverable was added, removed, merged, split or resequenced. The design for this
group listed the checklist edit as in scope, and this lane's HARD CONSTRAINT names the
implementation checklist out of bounds, so the correction is recorded here for the reconciliation
pass rather than made. — EVIDENCE:
proposals/0081_.../0081_....implementation-checklist.md:19; spec-changes.md:363; summary.md:946

CORRECTS [review-log-archive.md:45133 and :45174]: both record the same S2 DEFERRED with the
replacement text "…and states only that the step does not occur when the end leaves a bound
co-tenant." Round 18 deleted that clause from the staged sentence, so applying either archived
replacement would restore a claim the staging no longer makes. The replacement text to apply is
the one in the DEFERRED above.

FACT: no other parallel of the trimmed clause survives in the staged files. `grep -n "bound
co-tenant"` returns nothing in spec-changes.md, summary.md, problem-statement.md or the
implementation checklist, and its two hits in non-spec-changes.md are the tier-1 fixture's
account of the shipped `boundRemains` gate rather than restatements of the staged sentence. —
EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:591, :2880

### [spec.18.fix-G1.1]

DECISION: closed the SPEC-5 §15.1 rationale finding by deleting the workspace half of the counter-example clause, leaving "a credential-assignment failure at `/finalize` surfaces as `CREDENTIAL_POOL_EXHAUSTED`, per the §15.1 finalize precondition note." — BECAUSE the §15.1 /finalize precondition row names only `CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND` and `SETUP_COMMAND_FAILED` and gives no workspace-materialization envelope, and "workspace validation error" is §5.2/§6.2 pod-failure vocabulary with no occurrence in spec/15 — ALTERNATIVES: re-attributing the workspace half to §6.2/§5.2 (rejected: those are pod-failure reasons rather than REST envelopes); naming `SESSION_CREATION_FAILED` instead (rejected: that is the retryable fallback the clause argues the stage does not select). The staged replacement block was not touched, so DOCS-3's mirrored error-catalog sentences stay in sync.

FACT: spec/15 defines no code named for a workspace-validation failure. `grep -rn "workspace.validation" spec/` returns spec/05_runtime-registry-and-pool-model.md:559 (`reason: workspace_validation`), spec/06_warm-pod-model.md:147,169,170, and spec/07_session-lifecycle.md:389 (`workspace_validation_failed`), and nothing in spec/15. A materialization failure outside a more specific code reaches the client as `SESSION_CREATION_FAILED`, TRANSIENT/503 — EVIDENCE: spec/15_external-api-surface.md:646, spec/15_external-api-surface.md:1131.

OPEN: spec/06_warm-pod-model.md:290 makes the same false attribution in the shipped spec ("returning the workspace-validation, setup-command, or credential error per the §15.1 finalize precondition note"). It is a pre-existing spec defect this proposal did not introduce, and SPEC-5's §6.2 deliverable deliberately leaves that bullet's finalize sentence standing. A later round or a separate proposal should decide whether to correct it.

WATCHOUT: the identical erroneous wording also sits at review-log-archive.md:52579 inside a DECISION entry. It is the audit record of why the exclusion was re-keyed and it stands unedited; do not file it as a live site — EVIDENCE: review-log-archive.md:52579.

### [spec.18.fix-G2.1]

DECISION: closed the triple statement of the graceful-shutdown signal's co-tenant condition by deletion only, leaving the §4.7 `Shutdown` row as the single home — BECAUSE the row already owns both teardowns and their preconditions and §5.2, §7.1 and §15.4 already point there; the staged §29.4 sentence's first clause already discharges what the step owes — ALTERNATIVES: moving the home into §29.4 step 13 (a scenario section would become normative for a component contract and four citations would need edits); keeping the complement clause and softening the commentary (preserves two normative statements, which is the drift the discipline exists to prevent); deleting the Edge-cases bullet outright (it is the only statement of why the gate is the binding rather than `started`, and summary.md's rejected-alternative record depends on it).
FACT: the staged §29.4 step-13 sentence is still a single appended sentence after the trim, so the files-touched line "§29.4 session-end step 13 (one sentence appended)" stays true — EVIDENCE: 0081_...spec-changes.md, `spec/29_communication-scenarios.md` bullet of the files-touched list.
FACT: the live §29.4 step 13 still ends "([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3)." so the staged anchor sentence is unchanged — EVIDENCE: spec/29_communication-scenarios.md:704-711.
FACT: the non-spec-changes sites that mention a bound co-tenant describe shipped code (`boundRemains`, the `session.go` early return) rather than which spec text states the condition, so the trim falsified nothing there — EVIDENCE: non-spec-changes.md:586-591, :2880.
DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S2's final sentence reads "§29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7." That is now false. What is true: §29.4's session-end step 13 cites the §4.7 `Shutdown` row for the graceful-shutdown signal's co-tenancy condition and restates none of it. The design for this group marked that line in-scope; the HARD CONSTRAINT puts the checklist out of bounds, so it is recorded here instead.
WATCHOUT: the commentary under the staged §29.4 block ("The condition stays in the §4.7 `Shutdown` row … so a later refinement of the condition lands in one place") is now true of the text it describes. Do not edit it to describe two statement sites, and do not re-add a complement clause to the staged sentence for reader convenience — EVIDENCE: spec-changes.md, the paragraph after the SPEC-1 §29.4 fenced sentence.
CORRECTS [spec.11.review-single-source.1]: the round-11 FILED item, recorded as still live at round 17, is now closed. Only the §4.7 `Shutdown` row states the condition; §29.4 and the Edge-cases bullet cite it.

### [spec.18.fix-design-G1.1]
DECISION: SPEC-5's §15.1 rationale clause loses its workspace half; it becomes "...a credential-assignment failure at `/finalize` surfaces as `CREDENTIAL_POOL_EXHAUSTED`, per the §15.1 finalize precondition note." — BECAUSE the note (spec/15_external-api-surface.md, the `/finalize` precondition row) names only `CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND` and `SETUP_COMMAND_FAILED`, and §15.1 defines no "workspace-validation" code at all; the credential half alone carries the rationale. — ALTERNATIVES: (a) re-attribute the workspace half to §6.2's non-retryable vocabulary — rejected, that vocabulary is a pod-failure reason rather than a REST envelope, so it cannot be the envelope "that stage selects"; (b) replace the workspace half with `SESSION_CREATION_FAILED` — rejected, that IS the retryable fallback the sentence argues against, so it would refute the clause; (c) also stage a fix to spec/06:290 — rejected as scope growth, recorded as OPEN below.
FACT: spec/06_warm-pod-model.md:290 (the §6.2 Client visibility bullet) contains the same false attribution the proposal's rationale echoes: "returning the workspace-validation, setup-command, or credential error per the §15.1 finalize precondition note." That sentence is almost certainly where the proposal's clause came from. — EVIDENCE: spec/06_warm-pod-model.md:290
OPEN: spec/06:290's finalize sentence attributes a workspace-validation envelope to the §15.1 finalize precondition note, which states none. SPEC-5's §6.2 deliverable edits a different clause of that same bullet (the `/start` setup-command clause) and explicitly leaves "its finalize ... sentences" standing. Whether to extend SPEC-5 to correct that finalize sentence too is a scope question for a human or a later round; it is a pre-existing spec defect, not one this proposal introduces.
WATCHOUT: the staged replacement text of SPEC-5 §15.1 needs no change, and DOCS-3's mirrored error-catalog sentences are unaffected. Only the rationale prose between the replacement block and the "After the replacement..." sentence changes. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1177-1188

### [spec.18.fix-design-G2.1]
DECISION: One home for the graceful-shutdown signal's co-tenant condition — the §4.7 `Shutdown` row (spec-changes.md:299), unchanged. Trim the staged §29.4 step-13 sentence to its citation clause only (delete "; an end that leaves a bound co-tenant writes no frame and this step does not occur"), and replace the Edge-cases bullet's stating sentence (spec-changes.md:222-223, "A pod that keeps a bound co-tenant sends no signal...") with a citation, keeping only the bullet's own ground (why the gate is the binding and not `started`). BECAUSE the reduction removes two statements and adds no text, and it makes the commentary at spec-changes.md:366-368 true of the text it describes. ALTERNATIVES: (a) move the home into §29.4 and reduce §4.7 — rejected, §4.7 owns both teardowns and their preconditions and §5.2/§7.1/§15.4 already point there; (b) keep the complement clause and soften the commentary — rejected as hair: it preserves the drift the commentary disclaims.
WATCHOUT: two dependent descriptions must change in the SAME edit or they assert withdrawn text — summary.md:946 (SPEC-1 deliverable index: drop "and states only that the step does not occur when the end leaves a bound co-tenant") and implementation-checklist.md:19 ("restates ... and cites §4.7" → cites only). EVIDENCE: proposals/0081_.../0081_....summary.md:946, .../0081_....implementation-checklist.md:19
FACT: the Edge-cases bullet's closing sentence itself says "This is the shipped behaviour, restated so the two teardowns do not read as sharing one precondition" — the word "restated" is part of what the trim falsifies, so the bullet's last sentence is inside the edit, not adjacent to it. EVIDENCE: proposals/0081_.../0081_....spec-changes.md:223-224
FACT: spec-changes.md:1308 ("§29.4 session-end step 13 (one sentence appended)") stays true after the trim — the sentence is still one sentence. No other proposal file describes the §29.4 anchor. EVIDENCE: grep of the proposal dir for "step 13"/"29.4" outside the review log

### [spec.18.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens on the spec staging — BECAUSE every staged anchor quote resolves exactly once in the current tree, every anchor/link target exists, every relocation has both legs staged, and every gate I could reach either survives the edits or has its disposition recorded in the staging — ALTERNATIVES: filing the §6.2-vs-§4.6.1 projection-input-enumeration divergence (rejected: SPEC-4 reasons about it explicitly and the choice is defensible, so it is a preference between workable designs rather than an inapplicable edit); filing the §12.6 read-clause gate as an undisposed gate (rejected: the SPEC-3 §12.6 commentary and the SPEC-3 carrier table both record it).

FACT: every verbatim "reads, verbatim:" anchor in spec-changes.md was checked mechanically and each matches exactly one spec file, once. The cheap way to redo this: extract all fenced blocks with `re.findall(r'\n```\n(.*?)\n```\n', txt, re.S)` and substring-test each against every `spec/*.md`. The "current text" blocks return count 1 and the replacement blocks return 0; any current-text block returning 0 or >1 is a class-5 anchor defect. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md (blocks 0,2,4,8,10,12,14,16,18,20,22,25,27,29,31,33,35,37,45,47,53,55,57,59,61 all count 1)

FACT: the three §16.1/metric gates do NOT read the spec catalog into a completeness check that a spec-only step can break. `spec161Metrics` is a hand-transcribed Go slice in pkg/observability/metrics/catalog_test.go:15, reconciled against `MetricCatalog()` in both directions and never against spec/16 text, and tests/tier11_docs/adapter_metric_catalog_test.go:81 walks only metrics registered in pkg/adapter/metrics.go. So SPEC-6 landing alone (S6 before S10) breaks nothing. — EVIDENCE: pkg/observability/metrics/catalog_test.go:186-211; tests/tier11_docs/adapter_metric_catalog_test.go:85-116

FACT: the two tier-11 gates that read the §4.7 `Shutdown` row and the §4.7 `ReportSessionScrub` row both survive SPEC-1 and SPEC-3 unchanged. `TestRecycleScrubTriggerConsistency` scopes with `requireLine(s47, "| `Shutdown` |")` and requires only "recycle disposition", "ReportPodScrub", "does not block the response on the scrub", and the three `podId`/`cleanupCommands`/`cleanupTimeoutSeconds` params — all of them in the unedited tail of the row. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the exact string "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." which the SPEC-3 replacement carries word for word. — EVIDENCE: tests/tier11_docs/recycle_scrub_trigger_consistency_test.go:65-100; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,44-64

FACT: `TestVmRestartReprovisionProjectionConsistency` anchors on the §6.2 projection line via `requireLine(s62, "a `recycling` claim projects `claimed` until its whole-pod scrub reports successful")` and requires five substrings, none of which SPEC-4 deletes; SPEC-4 removes only the three claim-existence clauses from that same line. The gate therefore stays green. — EVIDENCE: tests/tier11_docs/vm_restart_reprovision_consistency_test.go:181-189; spec/06_warm-pod-model.md:80

FACT: `specSectionRPCRows` in tests/tier11_docs/spec_47_rpc_row_naming_test.go:44 harvests every `| \`Name\` |` row from the WHOLE of `### 4.7 `, so the SPEC-5 §4.7.1 "Which fields each request carries" table's rows enter that set. It only loosens the gate (the assertion is one-directional, Go comment -> row), so it is not a defect, but a future table added under §4.7 with a backticked first cell will silently widen this set. — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:33-53,136-151

FACT: the §5.2 `**Slot cleanup:**` bullet and the `**Whole-pod replacement trigger:**` bullet both sit under `**Slot failure and cleanup (maxConcurrentSessions > 1).**`, while `**Scrub model.**` sits far above it under no concurrency-scoped heading. The staging's claim about the scoping boundary is true against the tree. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453 (Scrub model), :545 (Slot cleanup bullet, one physical line carrying the action list, the reporting sentence, the timeout, the CRD rule and the leaked sentence), :561 (Whole-pod replacement trigger)

WATCHOUT: `spec/12_storage-architecture.md` §12.6 is titled "Interface Design", not "agent_pod_state table schema"; the `sessions_served` prose and DDL live at :481 and :494 inside it. A lens that greps for a §12.6 heading matching the proposal's parenthetical will conclude the citation is false. It is not: the parenthetical names the bolded paragraph, not the heading. — EVIDENCE: spec/12_storage-architecture.md:369,481,494

WATCHOUT: `[Section 15.4.2](...#1542-rpc-lifecycle-state-machine)` in the staged §4.7 `Shutdown` row looks like a thin citation — §15.4.2's DRAINING row says only "Graceful shutdown requested. The adapter finishes the current exchange and signals the agent to stop." It is nevertheless the tree's own citation for this signal: `drainViaLifecycle`'s doc comment cites "the §15.4.2 DRAINING-state graceful-shutdown signal". Do not file it. — EVIDENCE: spec/15_external-api-surface.md:1702; pkg/adapter/session.go:294-299

FACT: all 15 cross-file anchors and all 9 same-file anchors in the staged blocks resolve to live headings, including `#479-startup-sequence-for-type-agent-runtimes`, `#1542-rpc-lifecycle-state-machine`, `#49-credential-leasing-service` and `#74-upload-safety`. The staged blocks introduce no new spec heading, so the anchor-redirect map needs no entry. — EVIDENCE: spec/04_system-components.md:848,1099; spec/15_external-api-surface.md:1686; spec/07_session-lifecycle.md:438

FACT: every box in the implementation checklist is unchecked and every `Depends-on` names an earlier step; the six spec steps S1-S6 form the leading block. Checked against the caller's instruction that checklist drift is not filable this loop, so this is recorded rather than reported. — EVIDENCE: proposals/0081_.../0081_....implementation-checklist.md (S1-S22)

### [spec.18.review-citations.1]

FACT: The verbatim-anchor sweep re-ran clean in round 18. A python pass extracting every fenced block from spec-changes.md and counting it across `glob('spec/*.md')` gives exactly one hit for every "reads, verbatim" original and zero for every replacement, the two new fence entries, the §4.6.1 bullet replacements, the §6.2 `claimed ──→ draining` replacement and the §16.1 rows. — EVIDENCE: spec/04_system-components.md:157,:686,:481(spec/12),:416; spec/05_runtime-registry-and-pool-model.md:453,:545,:561; spec/06_warm-pod-model.md:80,:94,:145,:147,:156,:234,:290; spec/07_session-lifecycle.md:23,:210,:213,:214,:414; spec/15_external-api-surface.md:1136 region; spec/12_storage-architecture.md:481,:494
CORRECTS [standing context, "MISTAKE nearly filed, round 11: SPEC-4's rationale puts quotation marks around a sentence that is NOT in `occupancy.go`"]: the sentence IS in the file, word for word, in the `ProjectOccupancyPhase` doc comment: "the §6.2 state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim DELETE". The entry confused it with the different sentence in the no-claim arm's inline comment ("The phase the pod sits in at the DELETE selects the edge, and the §6.2 state machine constrains it"). The citation is sound for the reason the entry gives, but not for the reason it states. — EVIDENCE: pkg/controller/warmpool/occupancy.go:57-59 and :121-124
FACT: every code/file/symbol citation in spec-changes.md re-verifies in round 18, including the full 24-row SPEC-3 carrier table: every named file exists and carries the comment claimed, and the three sites the table deliberately excludes as non-carriers (`SessionScrubOutcome` in the proto and in scrubreport.go, `multi-tenancy.md`) do state only that the cleanup runs. — EVIDENCE: pkg/adapter/gatewaycontrol/scrubreport.go:12-14 (non-carrier) vs :71-72 (carrier); docs/operator-guide/multi-tenancy.md:72 (no reporting clause) vs docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 (carriers); schemas/lenny-adapter.proto:308-310,:451-452
FACT: `spec/06_warm-pod-model.md:69` is inside §6.1 and states "SDK-warm mode (`preConnect: true`) is admitted only in session mode with `maxConcurrentSessions: 1`", so the staged `DemoteSDK` row's §6.1 citation for the single-entry argument is sound. §6.1 spans lines 3-77. — EVIDENCE: spec/06_warm-pod-model.md:3,:69,:78
WATCHOUT: §15.1 defines NO "workspace-validation error" code and its `/finalize` precondition row names only `CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND` and `SETUP_COMMAND_FAILED`. "workspace validation error" is §6.2 vocabulary. A §15.1 materialization failure outside a more specific code is the RETRYABLE `SESSION_CREATION_FAILED`, which is the opposite of what a "that stage selects its own envelope" argument needs. — EVIDENCE: spec/15_external-api-surface.md:646, :518, :1010, :1057; spec/06_warm-pod-model.md:169-170
FACT: the §4.7 `Shutdown` row as shipped contains no `superseded` at all, so the SPEC-1 rationale sentence "the restatement it carried had already drifted: it stated `superseded` ..." describes an earlier STAGED draft of the replacement rather than anything in spec/04. Judged below the bar in round 18 (retrospective rationale, no effect on the applied spec); a later round that wants it should treat it as a deletion. — EVIDENCE: spec/04_system-components.md:686

### [spec.18.review-client-surface.1]

DECISION: returned EMPTY for the client-facing-surface lens in spec round 18 — BECAUSE the
round-16→18 delta is three sentence-level edits with no client-surface content (a `DemoteSDK`
docs-row clause, the removal of the "one replacement §15.1 does not need" Edge-cases clause, and
the removal of the SDK-demotion trailing clause from the §5.2 reclaim-hold paragraph), and an
independent re-sweep of every parallel representation came back clean —
ALTERNATIVES: filing the §15.4.2 graceful-shutdown citation (standing context line 188 forbids it,
a round was already spent), filing the §15.1 endpoint rows at
`spec/15_external-api-surface.md:625,646,647,650,720` that restate "a deterministic non-zero setup
command surfaces as the non-retryable `SETUP_COMMAND_FAILED`" (each states a sufficient condition
rather than the total mapping, so SPEC-5's widening leaves them true), and filing the
AssignCredentials-stage envelope for a `SLOT_BIND_ALREADY_STARTED` refusal (the remedy is gateway
envelope selection, which the proposal declares out of scope and whose fix is code-lane).

FACT: `PrepareWorkspaceRequest` has NO `mid_session` field today; only
`FinalizeWorkspaceRequest` has one, at field 4 — EVIDENCE: schemas/lenny-adapter.proto:682-695
(PrepareWorkspaceRequest, fields 1-3 plus `reserved 4`), schemas/lenny-adapter.proto:713-724.
The staged §4.7.1 carriage table's `PrepareWorkspace | mid_session | carried` row is therefore a
NEW field, and SCHEMA-1 adds it at `PrepareWorkspaceRequest` field 6. Do not file the carriage
table row as a false claim about the shipped proto; it is a staged addition.

FACT: the CH-RUNTIMEOPS `terminate` frame carries only `type`, `deadlineMs`, `reason` and names no
session, so the staged §4.7 `Shutdown` row's "the signal is pod-global and names no session" is
sound on the wire schema, not just on the spec prose — EVIDENCE:
schemas/runtime-ops-events.schema.json:174-185; spec/28_communication-channels.md:1082.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` compares only the
ADDRESSING sentence and its fixed opener `"The request is session-scoped: it is "`, not the row's
first clause — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33
and :66-74. SPEC-3 re-keys the row's opening clause and DOCS-2 re-keys the doc row's opening clause
to different wording, and the gate does not care. An earlier reading of the proposal's phrase
"asserts that the specification row and its reader-facing mirror open the rule identically" as
meaning the row opening is wrong; "the rule" is the addressing rule.

USEFUL [standing context line 191]: "No OpenAPI document and no language SDK mirrors any edited
surface" saved a full sweep and re-verifies in round 18. `SETUP_COMMAND_FAILED` /
`setup_command_failed` appear only in `pkg/gateway/externalapi/errorclassify/` and in no file under
`sdks/` or in `pkg/gateway/externalapi/openapi/openapi.json`. Note the path: the openapi document is
`pkg/gateway/externalapi/openapi/openapi.json`, NOT `pkg/gateway/openapi/openapi.json` as the
orchestrator prompt spells it; the latter does not exist.

USEFUL [standing context line 216]: "The adapter `ErrorCode` enum has no spec-side enumeration" is
correct — the only spec mention of any enum value is `PROTOCOL_VERSION_INCOMPATIBLE` in the §15.4.2
`INIT` row (spec/15_external-api-surface.md:1699). Minting codes 28 and 29 owes no spec table edit,
so there is no missing client-surface spec site for the two new adapter codes.

FACT: every verbatim anchor SPEC-5 and SPEC-3 quote on the client-facing rows re-verifies word for
word — EVIDENCE: spec/15_external-api-surface.md:1136 (all four `SETUP_COMMAND_FAILED` sentences),
spec/06_warm-pod-model.md:290 (the `**Client visibility:**` clause),
spec/04_system-components.md:686 (`Shutdown` row opening), :692 (`ReportSessionScrub` row).
§15.4's insertion point is real: the `**SDK-warm demotion contract:**` paragraph is
spec/15_external-api-surface.md:1469 and `#### 15.4.1` is :1471.

FACT: the withdrawn "reports on every session release" universal has exactly four spec-side carriers
and all four are staged — EVIDENCE: `grep -rn ReportSessionScrub spec/` returns
spec/05_runtime-registry-and-pool-model.md:453 and :545, spec/04_system-components.md:692, plus the
spec/12 `sessions_served` clauses; spec/06 §6.1 and spec/07 §7.1's `scrubPolicy` row mention the
cleanup but not the report (spec/07_session-lifecycle.md:72, spec/06_warm-pod-model.md:133). The
carrier table's spec half is complete.

WATCHOUT: `spec/16_observability.md` §16.1's metric catalog table has exactly TWO columns
(description-with-inline-labels, then type) — EVIDENCE: spec/16_observability.md:14-15. SPEC-6's two
staged rows match that arity. A future fixer adding a third column to those rows breaks the table.

### [spec.18.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the docs-alignment lens at spec round 18 — BECAUSE the
only in-scope residue of this lens (an accepted/deferred failure mode needing LANDING spec text) is the
same two Edge-cases bullets that have now been filed and refuted repeatedly, and every other candidate
this lens generates has its remedy in `docs/` or `non-spec-changes.md`, which this loop's scope bars —
ALTERNATIVES: (1) refiling the gateway-crash-stranded-entry and self-recreated-entry residues (rejected:
`[spec.12.review-docs-alignment.1]` filed exactly these, no fix group landed for them in round 12/13,
and `[spec.15.review-docs-alignment.1]` and `[spec.15.review-performance.1]` both record them as
"filed and refuted on materiality"; a sixth filing costs two verifiers and closes nothing);
(2) filing `schemas/lenny-adapter.proto:257` as a 25th carrier-table row (rejected, see FACT below);
(3) refiling the three review-log DEFERRED docs corrections (troubleshooting.md, gateway-replica-failure.md,
adapter-contract.md) — each remedy is a docs file this loop may not edit.

FACT: the round-12 mechanical check still reproduces zero at round 18. `awk 'NR>=243' spec-changes.md |
grep -n "crash\|recreat\|empty workspace\|unstartable\|stranded"` returns only the two §15.1
`SETUP_COMMAND_FAILED` exclusion sentences (spec-changes.md staged blocks, the SPEC-5 §15.1 exclusion
replacement). Neither of those two residues has landing spec text; rule 13 still carries the third
(untokened-entry) residue's. Anyone re-deriving this should read `[spec.12.review-docs-alignment.1]`
and `[spec.15.review-docs-alignment.1]` first rather than re-running the grep.

FACT: `schemas/lenny-adapter.proto:257` (the `service GatewayControl` doc comment, "carries the §5.2
per-slot and whole-pod scrub reports (ReportSessionScrub, ReportPodScrub) the adapter emits on release")
is the one carrier candidate the SPEC-3 carrier table's 24 rows do not list, and I judged it BELOW the
bar. It groups the per-slot report with `ReportPodScrub`, which is emitted at occupancy zero rather than
on every release, so "on release" there is a collective time-of-release framing rather than the universal
the table withdraws, and it stays true after SPEC-3. This is the same disposition round 12 reached for
`docs/reference/adapter-contract.md:84`. — EVIDENCE: schemas/lenny-adapter.proto:250-261 versus the
explicit universals at :309-310 ("runs on every session release") and :451-452 ("reports on every
session release"), both of which ARE carrier-table rows.

FACT: the carrier table's exclusion paragraph already names the `SessionScrubOutcome` enum comment, and
that comment (schemas/lenny-adapter.proto:436-437) indeed states only that the CLEANUP runs on every
release, so it is correctly excluded. `ReportSessionScrubResponse`'s comment (:469-473) asserts no
trigger. The proto surface therefore needs no new row.

FACT: the docs-mirror closing paragraph of `## Spec files touched` still re-verifies clean against the
tree at round 18, on every page it names: docs/reference/state-machines.md:247,248,251;
docs/reference/adapter-contract.md:64,75,81; docs/reference/execution-modes.md:68;
docs/operator-guide/security-principles.md:33; docs/reference/error-catalog.md (the
`SETUP_COMMAND_FAILED` row). `docs/operator-guide/multi-tenancy.md:72` still carries the cleanup
sentence WITHOUT the reporting clause, so it stays true and owes no DOCS-4 edit.

WATCHOUT: the round-16 fix conditioned the DOCS-2 `DemoteSDK` row ("When that cleanup completes, the
next bind sequence ...") while the matching tail was DELETED from the staged §5.2 reclaim-hold
paragraph. The docs row is now the only site in the proposal stating that consequence, which is
deliberate (a licensed docs restatement) rather than drift. Do not file the asymmetry.
— EVIDENCE: non-spec-changes.md:2538; spec-changes.md, the `**Slot-identifier reclaim hold.**` block,
last two sentences; review-log `[spec.16.fix-G1.1]`.

USEFUL [spec.12.review-docs-alignment.1]: its page-by-page verification of the closing docs-mirror
paragraph and its rejection of `adapter-contract.md:84` saved this round a full docs sweep; only line
numbers needed re-confirming, and all held.

DEFERRED [docs/operator-guide/troubleshooting.md, the `setup_command_failed` row]: still true at round
18 that no deliverable in the proposal opens this page (`grep -n troubleshooting.md` over the proposal
directory returns nothing), while SPEC-5 widens `SETUP_COMMAND_FAILED` to a cause that ran no setup
command. The row's remedies are wrong for that cause. The remedy is a docs deliverable, so the pass
between the loops owns it.

### [spec.18.review-edit-sites.1]

FACT: The SPEC-3 carrier table for the withdrawn universal ("the adapter reports on every session
release") is complete over `spec/`, `docs/`, `schemas/` and the generated pb.go copies, and over
`pkg/gateway/**`, `pkg/adapter/sessionscrubreporter.go`, `pkg/adapter/gatewaycontrol/scrubreport.go`
and `pkg/agentpodstate/agentpodstate.go`. It is NOT complete over two further Go sites, which I
verified by `grep -rn "ReportSessionScrub"` plus `grep -rn "every session release|each session
release|every slot release"` across the tree.
EVIDENCE: pkg/adapter/gatewaylink.go:71; pkg/controller/sandbox/podspec/podspec.go:168, :591, :906

FACT: `docs/operator-guide/multi-tenancy.md:72` is correctly excluded from the carrier table — its
per-slot cleanup sentence states only that the cleanup runs at each release and never mentions the
report, unlike the otherwise word-identical sentences in `docs/reference/execution-modes.md:68` and
`docs/operator-guide/security-principles.md:33`, which do and are carriers.
EVIDENCE: docs/operator-guide/multi-tenancy.md:72 vs docs/reference/execution-modes.md:68

FACT: `pkg/controller/sandbox/podspec/podspec.go:687` mentions `ReportSessionScrub` but carries no
universal over releases ("it emits ReportSessionScrub and ReportPodScrub keyed on the pod identity"),
so it is not a carrier. Do not add it as a row. The three sibling POD_NAME comments at :168, :591 and
:906 all say "reports each per-slot cleanup outcome via ReportSessionScrub" and are.
EVIDENCE: pkg/controller/sandbox/podspec/podspec.go:687 vs :168

FACT: Every verbatim anchor the staged edits quote resolves, at these lines on 2026-09-21:
spec/04:149 (§4.1 Request Message Scope, the third sentence), spec/04:686 (`Shutdown` row),
spec/04:676 (`DemoteSDK` row), spec/04:692 (`ReportSessionScrub` row), spec/04:659/:665/:688/:695
(the §4.7.1 insertion point), spec/05:453/:545/:561/:567, spec/06:80/:105/:148/:151/:157/:234/:290,
spec/07:23/:210/:213/:214/:414, spec/12:481/:494, spec/15:1136, spec/29:703-710. I re-read each.

FACT: The §4.6.1 re-key matches the controller. `ProjectOccupancyPhase` with no claim returns
`Idle` only from `Reserved` and `Draining` only from `Claimed`, and `("", false)` from every other
phase, so the two re-keyed bullets and the deleted "returns a pod from `claimed` to `idle` only on a
recycling pool" sentence are right. A claim deleted while the pod projects `sdk_connecting` or a
warm-fill phase is projected by the Sandbox-to-Pod arm, not this one, which is why §4.6.1's bullet
list needs no third claim-deletion bullet. Do not file one.
EVIDENCE: pkg/controller/warmpool/occupancy.go:84-144, esp. :128-143

FACT: The §28.4 claim register lives in `tests/claim-map.json`, not in `spec/28`. SPEC-5's promise of
an `ABSENT` row for the missing third-party-adapter harness therefore needs no spec/28 edit, and
spec/28's absence from "Spec files touched" is correct. SCHEMA-1 already lists the register file and
`scripts/seed-claim-register.py`.
EVIDENCE: spec/28_communication-channels.md:161-174

FACT: `docs/reference/metrics.md` carries no generation header and is authored by hand, so CODE-9
editing it directly is right rather than a generated-artifact edit. No `charts/`, `pkg/alerting/rules`
or `docs/runbooks/` surface mentions `lenny_slot_*` or `sessions_served`, so the two new §16.1
counters pull in no alert or runbook edit site.
EVIDENCE: docs/reference/metrics.md:1-20, :166-167; empty grep over charts/ pkg/alerting/ docs/runbooks/

WATCHOUT: `mid_session` exists today only on `FinalizeWorkspaceRequest` (field 4). The §4.7.1
carriage table's `PrepareWorkspace` row is not a false citation only because SCHEMA-1 adds
`bool mid_session = 6` to `PrepareWorkspaceRequest`. If SCHEMA-1's field list is ever trimmed, that
table row becomes a spec-lane defect.
EVIDENCE: schemas/lenny-adapter.proto:682-696, :724; non-spec-changes.md:2301

WATCHOUT: `spec/05:555` ("The retry is always assigned to a **new slot** on the same pod") reads like
a contradiction of the proposal's "names the same slot identifier", and is not: spec/05:395 fixes the
slot identifier as the session identifier, so a retry of the same session on a new slot reuses the
identifier. Two rounds could burn a finding here.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:395, :555

UNVERIFIED: §15.1's `SETUP_COMMAND_FAILED` row keeps the untouched sentence "Surfaced wherever the
gateway runs setup commands: ..." while SPEC-5 adds a cause under which no setup command runs. I
judged this below the bar (the clause locates the four endpoints, which are unchanged, and the fix
would add text). A later lens that disagrees should say so rather than re-derive it.
EVIDENCE: spec/15_external-api-surface.md:1136; spec-changes.md:1153-1163

### [spec.18.review-fresh.1]

DECISION: filed ONE finding, the §29.4 step-13 restatement of the §4.7 `Shutdown` row's
graceful-shutdown-signal precondition — BECAUSE the staged sentence's second clause ("an end that
leaves a bound co-tenant writes no frame") writes out the home predicate's complement, and the
block's own commentary claims the step "states only whether it occurs" — ALTERNATIVES: rejected
filing the Edge-cases bullet of the same triple (it is a record/rationale, not a staged spec
site), and rejected re-keying the §4.7 row (the row is the settled home).

USEFUL [Settled #169]: "The round-5 single-source shard filed FOUR groups ... The graceful-shutdown
triple it also recorded was NOT among them, so that one is unfiled rather than refuted." That line
is what sent me straight to the one live duplication; it is worth keeping until the triple is
reduced.

FACT: the §29.4 clause has been in the staging since `spec-r1` of this run — `grep -c "an end that
leaves a bound co-tenant writes no frame" scratchpad/cp-snap/0081-opt2/*/0081_*.spec-changes.md`
returns 1 for every snapshot — so it is pre-existing rather than fixer-introduced.

FACT: every verbatim anchor in spec-changes.md re-verified again this round against the live tree
(§4.1:157, §4.7 rows :674/:686/:692, §5.2 :453/:488/:545/:561, §12.6 :481, §6.2 :80/:95/:148/:155/
:234/:290, §7.1:23, §7.2 :210/:213/:214, §7.3:414, §15.1:1136, §15.4:1469, §29.4 step 13 at
spec/29_communication-scenarios.md:704-711, §16.1 two-column table at spec/16_observability.md:14).
Nothing has drifted. Do not spend another round on the anchor sweep.

FACT: SPEC-4's §6.2 `claimed ──→ draining` edit is the ONE staged edit given as a single fenced
block with no "reads, verbatim" anchor beside it. It is the REPLACEMENT (live text at
spec/06_warm-pod-model.md:95-97 reads "claim deleted on a pod / with recycle.enabled: false"). I
judged this not a finding because the anchor names the fenced `Occupancy projection` block, which
disambiguates it from the three other `claimed ──→ draining` entries at :114, :135, :137. A later
agent reading it as an anchor rather than a replacement will waste a round.

UNVERIFIED: `schemas/lenny-adapter.proto:257`, the `service GatewayControl` doc comment, says the
scrub reports are ones "the adapter emits on release". I considered it a missing row of SPEC-3's
carrier table and DROPPED it: the clause also covers `ReportPodScrub`, which is not emitted on
release at all, so the phrase was already loose before this proposal and is a restrictive
identifier rather than the withdrawn universal. Someone should settle it once rather than
re-deriving it; the carrier table claims to be the single complete home.

UNVERIFIED: `RunSetup` has a THIRD deterministic `FailedPrecondition` producer the widened §15.1
`SETUP_COMMAND_FAILED` row does not name — "adapter is not configured with a workspace root"
(`pkg/adapter/staging.go:345-349`), which `writeSetupCommandError` maps to 422 exactly like the
other two. I did not file it: the shipped row was equally non-total before SPEC-5, so it is
pre-existing, but SPEC-5's commentary sentence "After the replacement the row is total over
failures of the setup-command request" is strictly false against the tree.

FACT: a `SLOT_BIND_ALREADY_STARTED` refusal (rule 6, `FailedPrecondition`) classifies as
`SlotReasonPolicyRejection` at every stage except the workspace stages, where
`SlotBindError.Reason()` forces `SlotReasonTransient`
(`pkg/gateway/podlifecycle/podsession/slotfailure.go:85-100`). So §5.2's three non-retryable
categories need no new member — the "deliberately untouched" entry's conclusion holds even though
its stated ground (the hold's refusal is transient) does not address rule 6. Do not file the
category list; do not file the `policy_rejection` mislabel without new evidence that a client
contract depends on it.

### [spec.18.review-kubernetes.1]
DECISION: returned EMPTY for the Kubernetes-idiom lens on the round-18 staging — BECAUSE the whole K8s surface this proposal touches is SPEC-4 (§4.6.1 bullets, §6.2 projection prose and fence) plus the two `ReportSessionScrub` row variants, and every one of them re-verifies against the controller; the new mechanism (bind attempt token, reclaim hold, two teardowns) lives entirely on the gateway↔adapter gRPC path and writes no CRD, no status subresource, no annotation and no finalizer — ALTERNATIVES: filing the self-referential projection input (rejected: shipped, and already carried as an Open entry), filing §4.6.3's Notes cell "a level-triggered projection of `SandboxClaim` state" as an unstaged site after SPEC-4 adds the pod's own phase as an input (rejected: the cell is a gloss that cites §4.6.1 for the rule, so it does not become false), filing the surviving "the pool's `sessionPolicy`" input in §4.6.1's enumeration (rejected: `ProjectOccupancyPhase` never reads it, but that is pre-existing text SPEC-4 leaves alone and the proposal asserts nothing about it).
FACT: `OccupancyReconciler` reads `Current: state.State(sb.Status.Phase)` from the manager cache and SSA-patches the projected phase back under the single `lenny-warm-pool-controller` field manager, and its own doc comment states that the Sandbox-to-Pod reconciler writes the warm-fill legs under that same manager, "so the §4.6.3 sole-writer invariant holds across the two arms". The staged §4.6.1 parenthetical "(the controller's own last level, which it may read back because it is the sole writer of that field)" is therefore exact, including its implicit two-arm case. — EVIDENCE: pkg/controller/warmpool/occupancy.go:145-156, :198-203, :206-210; spec/04_system-components.md §4.6.3 ownership table row `Sandbox` / `status.*`
FACT: the two re-keyed §4.6.1 claim-deletion bullets are a faithful transcription of the code's no-claim arm and cover no unreachable case. `case state.Reserved: return Idle` and `case state.Claimed: return Draining`, with every other current phase falling to `("", false)`. The dropped "under its limits" qualifier on the `reserved` bullet loses nothing: a pod over its retirement limits takes the `released` disposition and drains before it can reach `reserved`. — EVIDENCE: pkg/controller/warmpool/occupancy.go:126-142
FACT: the exclusive-pool edge-case bullet's premise holds in the tree. The claim is patched `bound` at acquisition (`podclaim.WriteBoundStatus` from `binder.go:1906`) well before `Binder.Prepare`, and `failPhase` → `drain` is a bare `podclaim.DeleteClaim` writing no disposition, so the DELETE does land while the claim's binding state is `bound` and the pod projects `claimed`. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082, :1200-1202, :1906
USEFUL [Settled: "§4.6.3 gives `Sandbox.status.*` to the WarmPoolController as sole writer..."]: it named the exact two claims a K8s lens has to test on SPEC-4 and saved re-deriving the field-manager question from scratch; I re-verified both rather than trusting it, and both hold.
USEFUL [Open: "Can a coalesced reconcile lose the claim-DELETE retirement?"]: this is the one genuine K8s-idiom hazard in the design (a projection whose input is its own last output is not recoverable from a single level when a CREATE, `bound` patch and DELETE coalesce inside one watch window). It is already recorded, pre-existing in `ProjectOccupancyPhase`, and its remedy is code, so it is correctly not a spec-lane finding. A future K8s lens should read that entry first and not re-derive it; three rounds of this lens have now landed on it.

### [spec.18.review-mechanism.1]

DECISION: returned EMPTY under the end-to-end-mechanism lens in round 18 — BECAUSE every flow I traced
origin-to-effect (bind → failure → §7.1 reclaim → §4.7.1 rules 10-15 → §5.2 cleanup/hold/report → §6.2
sub-state → §12.6 counter) closed without a gate a write path bypasses, an unreachable trigger, or a
default contradicting stated behaviour — ALTERNATIVES: five candidates were worked up and each dropped
below the bar (listed below), so none was filed.

FACT: the round-17→18 delta is TWO hunks only, both harmless: the Edge-cases DOCS-3 bullet lost its
"makes one replacement §15.1 does not need" tail, and the §5.2 reclaim-hold paragraph lost its trailing
"so the pod-warm bind sequence that follows an SDK demotion does not meet it." The demotion's
hold-closure is still carried, by the surviving "A release that runs its cleanup inside the RPC that
requested it ends the hold before that RPC answers when that cleanup completes" plus the §4.7
`DemoteSDK` row. — EVIDENCE: spec-changes.md:238-241, :661, :329

FACT: every verbatim anchor SPEC-1/2/3/4/5 quotes still matches the tree byte for byte at round 18.
Re-checked directly: spec/04:157 (§4.1 third sentence), spec/04:674 (`DemoteSDK` row), spec/04:686
(`Shutdown` row), spec/04:692 (`ReportSessionScrub` row), spec/04:854 (§4.7.9 step 5), spec/05:453
(`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**`), spec/05:561 (`**Whole-pod replacement
trigger:**`), spec/06:82 (projection prose clauses), spec/06:148/151-155 (per-slot fence), spec/06:234
(mid-resume cancel bullet), spec/06:290 (`**Client visibility:**`), spec/07:23 (atomicity paragraph
end), spec/07:210/213/214 (§7.2 preamble and steps 2-3), spec/07:414 (§7.3 list item 4), spec/12:481
and :494 (§12.6 prose and DDL), spec/15:1136 (`SETUP_COMMAND_FAILED` row), spec/29:704-711 (step 13).
No re-sweep is owed unless a fix moves one.

FACT: `ProjectOccupancyPhase` returns `("", false)` for a claim deleted while the pod projects
`sdk_connecting` (the recycle re-warm leg), so SPEC-4's re-keyed §4.6.1 bullets leaving that input
unstated matches the controller; the OLD text ("a claim deleted on a recycling pod under its limits
projects `idle`") was the wrong one. Do not file the silence as a projection gap. — EVIDENCE:
pkg/controller/warmpool/occupancy.go:104-140

WATCHOUT: five mechanism candidates that look like findings and are not. (1) The §5.2 disposition
table has no key-column row for a RUNTIME-GIVEN slot that no cleanup reclaims, which the late-start
residue produces; row 9's BODY is right for it and the case is recorded in Edge-cases, and the table's
lead-in quantifies over cleanups rather than over slots, so this is not a partition break. (2) The
§5.2 append's "It also runs on a slot whose bind is abandoned or fails ... before it reaches `running`"
reads universal against row 9's "No cleanup runs"; the wording is deliberate and was chosen to keep
the withheld case OUTSIDE the "session release" vocabulary the docs mirrors assert over. (3) "Completed"
carries two staged senses — §7.1's (answered, clean exit) and §5.2's (every act returned without error)
— and row 3 satisfies one and not the other; the log already records this as three deliberately
distinct predicates. (4) §15.1's untouched "Surfaced wherever the gateway runs setup commands" lead-in
sits beside a new cause under which no setup command runs; the endpoint enumeration it introduces stays
correct, so it is a framing seam rather than a false claim. (5) Rule 2 is worded "Applied to a request
rule 1 admits" while the `**Admission.**` preamble puts non-creating RPCs outside rules 1-9 "apart from
the reclaim hold of rule 2", leaving rule 1's verdict undefined for them; "admits" reads as "does not
refuse" and the entry is deregistered during the hold anyway. — EVIDENCE: spec-changes.md:645, :657,
:661, :403, :1048, :1043; spec/15_external-api-surface.md:1136

USEFUL [the Traps section, "The §5.2 disposition table's row keys are performer-scoped"]: it is what
made me read the key column before judging candidates 1 and 2, and it is why both were dropped rather
than filed. USEFUL [Settled, "The three attempt kinds do not map onto three code paths"] and
[Settled, "`st.started` precedes `Runtime.Start`, and it has THREE setters"]: together they close the
"the runtime teardown precondition and the report predicate are the same predicate" dress in one read.

### [spec.18.review-performance.1]

DECISION: Returned an empty findings list for the performance / scalability / failure-mode lens on spec round 18 — BECAUSE the staged spec edits are write-neutral-to-negative on every shared store and introduce no new serialization point. ALTERNATIVES: I worked up and then dropped three candidate findings (pod-churn amplification from the widened `leaked`, the withdrawn "any cleanup failure leaks" universal, and the shared 10s hold-timeout grace window); each is recorded below with the evidence that refuted it, so a later round does not re-derive them.

FACT: The proposal creates NO new control-plane write. The reclaim is a gateway→adapter gRPC only; `ReportSessionScrub` is strictly *conditioned* (fewer Postgres `sessions_served` writes, not more); both new §16.1 counters are in-process; the SPEC-4 occupancy re-key is a spec-to-code correction with no controller change. Nothing lands on etcd, so the §4.6.1 ~800 writes/s Tier-3 estimate and the ~120 QPS post-dedup ceiling are untouched. EVIDENCE: spec/04_system-components.md:469-475 (tier write table), spec-changes.md:595 (conditioned report), pkg/controller/warmpool/occupancy.go:128-143.

FACT: `ProjectOccupancyPhase` already switches on the pod's current phase alone — `state.Reserved` with no claim → `Idle`, `state.Claimed` with no claim → `Draining` — so SPEC-4's re-key of §4.6.1's two claim-deletion bullets and §6.2's projection prose adds no pod churn. A reviewer tempted to file "the re-key retires pods a recycling pool used to reuse" should read the function first. EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143, and the function's own comment at :113-127 states the rule the edits adopt.

FACT (refutes a candidate finding): the shipped adapter derives the per-slot cleanup outcome from the runtime `closeErr` ALONE — `sessionScrubOutcome(closeErr)` returns `leaked` iff the close failed. A workspace-tree or credential-directory removal failure never produced `leaked` in the tree. So the §5.2 disposition table's row "runtime close succeeds and any other act fails → `released`, clean-exit Set, `leaked` Not entered", and the fourth anchor's withdrawal of the shipped sentence "If cleanup fails, the slot is leaked", record the tree rather than degrade it. The shipped §5.2 sentence was already false against the code. Do NOT file this as a reliability regression against the whole-pod replacement threshold. EVIDENCE: pkg/adapter/sessionscrubreporter.go:39-44; the withdrawn sentence at spec/05_runtime-registry-and-pool-model.md (the `**Slot cleanup:**` bullet), quoted verbatim at spec-changes.md:726.

FACT: The §10.1 hold-timeout termination's "graceful window of ten seconds" staged at spec-changes.md:661 is real in the tree, but the 10s context is ONE `closeCtx` shared across every member of the hold set, not one per session. It is still a true upper bound for any single session's close, so the staged sentence is not false; the code's own comment explains that only the last member's close consumes the grace. Do not spend a finding on this granularity. EVIDENCE: pkg/adapter/holdstate.go:199-205, :248-250.

FACT: `sessions_served` feeds two capacity controls, and both survive the SPEC-3 conditioning. (1) `ScrubReporter.RecordSessionScrub` increments the counter and then calls `RetireOnSessionCount` with the atomic post-increment value, so the §5.2 **Session count limit:** evaluation point stays "on each report" and §12.6's replacement read clause citing that bullet is accurate. (2) `mode_factor` for the PoolScalingController comes from `lenny_pod_session_reuse_count`, which is observed at pod RETIREMENT from the served-session count (`ObserveSessionReuseCount`), not per release, so conditioning the report cannot skew the scaling formula. EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go:214-222; pkg/controller/poolscaling/controller.go:1227; spec/05_runtime-registry-and-pool-model.md:488.

FACT: The registry critical section is a short bookkeeping step in every one of its four forms — the cleanup acts, the runtime close and the runtime handoff all sit OUTSIDE the lock, which is precisely what the slot-identifier reclaim hold exists to cover. There is no pod-wide lock held across I/O, so `maxConcurrentSessions` does not become a contention axis. EVIDENCE: spec-changes.md:1041 (each step enumerated), spec-changes.md:661 (hold covers the post-deregistration acts).

FACT: The gateway-side slot capacity gate is the Redis key `lenny:pod:{pod_id}:active_slots`, released by the §7.1 paragraph's "releases the slot reservation afterwards"; the adapter's reclaim hold is keyed on the slot identifier, which equals the session identifier, so a hold held "for the life of the pod" blocks only retries of that one session and never the pod's free concurrency. A Redis reset rehydrates the counter from `SessionStore.GetActiveSlotsByPod`, which the hold does not interact with. EVIDENCE: spec/12_storage-architecture.md:191; spec-changes.md:403, :661.

WATCHOUT: `lenny_slot_compensation_superseded_total` and `lenny_slot_shutdown_untokened_entry_total` use only `pool` and `k8s_pod_name`, both registered in §16.1.1's single-source-of-truth attribute table, and neither uses a forbidden label (`session_id` is correctly absent). Cardinality matches the shipped `lenny_slot_failure_total` / `lenny_slot_pod_replacement_total` precedent. The only thing arguably stale is the descriptive "Used on" cell for `k8s_pod_name`, which enumerates metric families; I judged that below the bar and did not file it. EVIDENCE: spec/16_observability.md:14-15, :297, :312.

UNVERIFIED: whether the *rate* of unanswered reclaims at Tier 3 is high enough that the staged rule "a §7.1 reclaim the adapter does not answer enters `leaked` as a reclaim whose act fails does" (spec-changes.md:659) meaningfully accelerates whole-pod replacement at `maxConcurrentSessions: 2`, where `ceil(2/2) = 1` leaked slot retires the pod (spec/05_runtime-registry-and-pool-model.md:561). The spec publishes no bind-failure rate and no reclaim-timeout budget to multiply against, so the math the lens asks for cannot be stated. It is the fail-closed direction, which is why I did not file it. A load-tier owner with real Tier-3 bind-failure numbers should close this.

### [spec.18.review-reliability.1]

DECISION: returned EMPTY for the reliability lens on the spec staging — BECAUSE every recovery
path the staging adds or changes traces cleanly under crash, restart, redelivery and store
failover, and each candidate I raised died on a tree fact or on an entry the standing context
already records as settled, refuted or reserved. ALTERNATIVES: I considered filing (a) rule 8's
"takes the session back off the shared runtime process" against `MCPRuntime.Close`/
`InProcessRuntime.Close` ignoring the session identifier; (b) the hold paragraph's close-window
enumeration leaving a `Shutdown` with neither `deadline_ms` nor a ctx deadline unbounded; (c) the
untokened-orphan entry having no reclaimer short of pod retirement; (d) `ReportSessionScrub`
double-counting `sessions_served` under redelivery. Each is refuted below.

FACT: rule 8's undo is NOT a new co-tenant hazard. `SocketRuntimeProcess.Close` is session-aware
and returns early while a sibling slot is active — EVIDENCE: pkg/adapter/socketruntime.go:435-444
("if !p.releaseActiveLocked(sessionID) { ... return nil }"). `MCPRuntime.Close(ctx, _ string)`
(pkg/adapter/mcpruntime.go:266) and `InProcessRuntime.Close` (pkg/adapter/embedded.go:188) do
ignore the identifier, but the shipped `Shutdown` teardown already calls the same method on the
same runtimes, so rule 8's undo adds no arm those runtimes did not already have. Not a finding;
the Open entry "Does 'takes the session back off the shared runtime process' hold for every
runtime implementation?" can be closed for the NEW-behaviour reading and left open only as a
pre-existing question.

FACT: `ReportSessionScrub` is at-most-once on the wire, so the gateway's missing dedup cannot
double-count under redelivery — EVIDENCE: pkg/adapter/gatewaycontrol/scrubreport.go:70-77, whose
doc comment states "A transport or gateway failure is returned as a wrapped error"; the client
performs no retry. This kills the "at-least-once delivery whose consumer has no dedup key" dress
against SPEC-3's re-key of §12.6 onto the report and against the §4.7 row's "on each such report".

FACT: the compensating `Shutdown` is idempotent under replay by construction. A second copy
naming the same token meets no entry and is answered `absent` by rule 11, which rule 15 makes a
clean-exit success — EVIDENCE: spec-changes.md:1062 (rule 11) and :1066 (rule 15). A transport
retry of the reclaim therefore cannot double-tear-down. This is a strength of the staged design
and is worth not re-deriving.

FACT: `Server.DemoteSDK` runs its release synchronously before answering, so the staged §4.7
`DemoteSDK` row's "the adapter runs [the cleanup] inside this request, before it answers" and the
hold paragraph's "A release that runs its cleanup inside the RPC that requested it ends the hold
before that RPC answers when that cleanup completes" are both sound — EVIDENCE:
pkg/adapter/sdkwarm.go:296-302 (`anyRegisteredSession` → `noteRuntimeClosed` → `releaseSessionSlot`
before the `return &adapterv1.DemoteSDKResponse{...}`), with the failure arm returning at :280-282
before any deregistration.

WATCHOUT: the round-17 fix deleted the categorical "so the pod-warm bind sequence that follows an
SDK demotion does not meet it" from the hold paragraph, and the ONLY two surviving "pod-warm bind
sequence" phrases are both safe (a historical clause in the `DemoteSDK` commentary and the
double-count sentence in the scrub-model commentary) — EVIDENCE:
0081_...spec-changes.md:340 and :604. A later round re-deriving the demotion/hold interaction
should grep those two lines first rather than re-reading the paragraph.

UNVERIFIED: whether `s.reclaiming` grows without bound over a long-lived recycling pod, one entry
per cleanup whose act failed ("Held for the life of the pod", spec-changes.md:651,:653,:655). The
adapter process survives pod reuse, so the map is never swept. This is a code-lane concern, not a
spec defect, and the non-spec loop should price it when it builds `Server.reclaiming`.

### [spec.18.review-security.1]

FACT: `spec/04_system-components.md:1169` is the normative statement of the direct-mode
lease-expiry timer, and it is an ENFORCEMENT mechanism, not bookkeeping: "the adapter MUST set a
local timer for each credential lease's `expiresAt` ... the adapter deletes the credential file
(`/run/lenny/slots/{sessionId}/credentials.json`) ... This ensures that a long-lived
`anthropic_direct` key cannot be used by the runtime beyond the lease boundary". `spec/04:1466`
restates it: "the adapter expiry timer is the enforced lease deadline". EVIDENCE:
spec/04_system-components.md:1169, spec/04_system-components.md:1466

FACT: no spec section anywhere states that the per-slot cleanup cancels those timers. `grep -rn
"expiry timer" spec/*.md` returns §4.9's two statements, a §4.7 `ExtendCredentialLease` row, §28.5
and §29 flow lines, and nothing in §5.2. SPEC-3's action-list replacement is the FIRST spec text
to assert the cancellation, so the interaction below is introduced by this staging rather than
pre-existing in the spec. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545,
spec-changes.md:573

FACT: in the tree the cancellation is unconditional and precedes the removal that can fail.
`deregisterSlotLocked` cancels every armed timer under `s.mu` and cannot fail
(pkg/adapter/slotsession.go:174-188); both cleanup arms then call `_ = removeSlotTree(st)`,
discarding the error (pkg/adapter/slotsession.go:213-218 for `releaseSessionSlot`,
pkg/adapter/session.go:272 inside `Shutdown`); `slotlayout.RemoveTree` removes `p.CredentialsDir`
as one of four best-effort `os.RemoveAll` calls and returns only the first error
(pkg/adapter/slotlayout/tree.go:58-69). So "timers cancelled, credential file survives" is the
shipped ordering, not a hypothetical. EVIDENCE: pkg/adapter/slotsession.go:174

DECISION: filed ONE finding, the §4.9 timer/credential-file fail-open in SPEC-3's action list.
BECAUSE the staged text newly asserts the disarming of a mandatory §4.9 control in the same
sentence that asserts the removal of the artifact that control protects, and the residue
paragraph then permits the removal to fail while the deregistration (and so the cancellation)
has already happened. ALTERNATIVES rejected, each verified and refuted rather than guessed:
(1) the withdrawal of "If cleanup fails, the slot is leaked" relaxing the whole-pod replacement
threshold — refuted, because §5.2 scrub step 6 stat-checks `/workspace/slots/`, `/tmp`,
`/dev/shm` and `/run/lenny/slots/` from disk and fails the scrub if a per-slot credential file
survives, so an independent disk-addressed backstop retires the pod at the recycle boundary
(spec/05:461, spec/05:471); this is also a recorded DECISION (`CODE-1's cleanup-outcome report
stays keyed on closeErr ALONE`). (2) the hold refusing a mandatory credential control —
refuted again: revoke/rotate/extend/`CoordinatorFence` resolve through
`slotStateLocked`/`boundSlotState`, and during a hold the entry is already deregistered, so the
resolve is vacuous either way. (3) `Shutdown` rule 10 breaking the §11.4 revoke — refuted:
§11.4 step 2's `Shutdown` is built at the single non-test call site and CODE-4 sets
`unconditional_teardown` there, and rule 12 covers it. (4) SPEC-4's deletion of §4.6.1's
one-session-only sentence — refuted: `ProjectOccupancyPhase` never returns `Idle` from
`Claimed` on any pool (pkg/controller/warmpool/occupancy.go:134-140), so the replacement bullet
is a tightening. (5) token entropy under-specified (no minimum length) — below the bar: forging
a token buys an in-pod attacker nothing it cannot already do by sending
`unconditional_teardown` on the same localhost channel.

FACT: `spec/05_runtime-registry-and-pool-model.md:517` is the sentence that makes a residual
credential file matter on a concurrent pod: each slot's
`/run/lenny/slots/{sessionId}/credentials.json` is readable by every slot's agent process via
the shared `lenny-cred-readers` supplementary group. A future security lens looking for blast
radius of a surviving credential file should start there. EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:517

USEFUL [MISTAKE nearly filed, the security family]: the standing-context entry listing the four
refuted security dresses (`superseded`/`absent` as pod self-reports, a compromised adapter
refusing every teardown, a late `StartSession` orphan holding credentials, the withheld report
relaxing `maxSessionsPerPod`) saved a full round of work. The bar it states, "the lie must make
the gateway do something new", is the right filter and I reused it.

WATCHOUT: the disposition table's row "runtime close succeeds and any other act fails" reports
`released` with the clean-exit flag SET and `leaked` Not entered, so the gateway learns nothing
about a failed credential-file removal. Any future lens arguing "the gateway will notice" is
wrong; the only backstop is the disk-addressed whole-pod scrub at the next occupancy-zero
boundary, which a long-lived co-tenant can defer indefinitely. EVIDENCE:
0081_...spec-changes.md:641 (table row three), spec/05_runtime-registry-and-pool-model.md:461

### [spec.18.review-single-source.1]

DECISION: filed exactly ONE finding, the round-11 graceful-shutdown triple, and returned nothing
else — BECAUSE the round-17 fresh shard `[spec.17.review-fresh.1]` explicitly left it live and
unfixed and routed it to "the pre-certification sweep of `### Open` for FILED items", and this loop
retires a lens once it returns nothing: if the single-source lens returns empty again the item
never gets a home. Rounds 12 and 15's single-source lenses both returned EMPTY without naming it,
which is how it survived seven rounds. — ALTERNATIVES worked up and dropped, each recorded so the
next round does not re-spend them: (a) the §16.1 `superseded` metric row restating rule 15's gloss
— barred by the standing-context trap that enumerates rules 13/15, the §16.1 row and the docs
mirror as the four spellings that move in one edit, i.e. already accepted; (b) the Edge-cases
"abandoned attempt's start … re-creates the entry" bullet restating rule 13's untokened arm without
citing it — a clause-level summary inside an accepted-failure record, and the standing context
already assigns rule 13 the untokened residue and the bullet the self-recreated residue; (c) the
carriage table's `mid_session`/`bind_attempt` pairing cells against rule 1 — settled decision, the
table is the single home of carriage and rule 1 owns the refusal, two different rules; (d) the
§15.4 hold block's two "does not conform" sentences against the bind-attempt block's conformance
universal — the standing context says do not touch that block; (e) rule 8's "at most one
cleanup-outcome report" clause against the §5.2 one-report sentence — rule 8 CITES §5.2 and the
standing context says do not delete the §5.2 sentence because rule 8 and §7.1 cite it.

FACT: the residue rule after the §5.2 disposition table has exactly ONE stating site. `grep -n
"would have removed\|leaves on the pod"` over spec-changes.md returns the post-table paragraph plus
three pointer sites (§7.1's paragraph, the `**Slot cleanup:**` leaked-sentence replacement, the
Design choice record), all of which cite it and state no residue. The prune held. Do not re-audit
this class. — EVIDENCE: spec-changes.md:659 (home), :403, :732, :40

FACT: the §12.6 read-clause citation target is real and states the evaluation point, so SPEC-3's
reduction lands on a live anchor. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488
("**Session count limit:** … the pod transitions to `draining` on the session release that drives
the served-session count to `maxSessionsPerPod`")

FACT: the §5.2 `**Slot cleanup:**` action-list additions duplicate nothing in spec/. spec/05's only
other `/run/lenny/slots/{sessionId}/credentials.json` statements are whole-pod (the recycle
lifecycle paragraph, scrub step 0, the scrub verify step, and the §13.1 group-read paragraph), and
spec/04 §4.9 states the direct-mode timer's ARMING and its `AUTH_EXPIRED` firing but never its
cancellation on slot release, so the cancellation clause is a new single statement rather than a
copy. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455, :461, :471, :517;
spec/04_system-components.md:1169, :1466

WATCHOUT: the round-16 edit deleted the §5.2 hold paragraph's trailing clause "so the pod-warm bind
sequence that follows an SDK demotion does not meet it" and moved that consequence into the
non-spec `DemoteSDK` docs row ("When that cleanup completes, the next bind sequence …"). That is
the correct direction (one general rule in §5.2, the instance in the mirror), so do not read the
missing clause as a gap and do not put it back. — EVIDENCE: diff of
scratchpad/cp-snap/0081-opt2/spec-r16-prefix against the live directory; spec-changes.md:661;
non-spec-changes.md:2538

USEFUL [OPEN: the round-11 FILED item "The graceful-shutdown-signal condition is stated at three
sites"]: `[spec.17.review-fresh.1]`'s OPEN entry named the three sites, said no `[spec.1x.fix*]`
entry discharges it, and named the exact reduction. That is the entire finding; I verified the
three sites and filed it rather than re-deriving.
USEFUL [MISTAKE, FIVE edit passes in four rounds on ONE disposition-table column]: killed candidate
(b)-adjacent residue work before I spent a pass on it.

### [f1.cleanup.2]

FACT: the summary file already carried exactly the eight required sections, in the required order, and needed no edit. `## Summary` holds `**Problem statement.**`, `**What changes.**`, `**Decisions.**` and `**Watch out for.**` in that order and carries no prose of its own; `## Deliverable index` stands last, line for line as the reconciliation pass left it. Nothing was added, removed, renamed, reordered or relocated. — EVIDENCE: headings at summary.md lines 1, 3, 289, 313, 471, 595, 929, 944; labelled parts at 5, 21, 101, 224; `git status` and `git diff HEAD` over the proposal directory are both empty against `992081205`.

FACT: no item of this firing owed a summary edit. Items 20 and 21 were refuted at both the gate and the apply, so nothing was staged for either and both remain listed with their own identifiers. Item 27 and every `marker:unscoped` item came back `no-edit-needed`, and the summary already carried each as written: 27 as decision entry 27, and each unscoped marker as an entry of `## Defects in the shipped tree that this proposal does not stage`. All nineteen markers were matched against that section entry by entry; none is missing and none is unaccounted for.

FACT: the `## Open decisions for human to make` preamble is true of the three entries the section now carries, so no preamble correction was owed. Entries 21 and 27 each carry a recommendation with its ground, its alternatives and a stated confidence; entry 20 carries the question and its ground alone. The `### Settled` and `### Retired` lists the preamble points at both exist in this log.

FACT: the section carries no `### Retired` block or equivalent, and no meta-list of staged items with a proposed disposition, so there was nothing to check against the staged changes and nothing to drop.

FACT: this log's layout differs from the phase's description of it. There is no top-level `## Retired`; `### Retired` is a subsection of `## Standing context` (line 684) and `## Ledger` is the last section of the file. Appending at the end of the file therefore lands at the end of `## Ledger` and buries nothing. A later firing should splice the same way rather than hunting for a `## Retired` heading.

WATCHOUT: the two cosmetic observations `[f1.cleanup.1]` recorded both still stand, unchanged, and were again left alone. `**Accepted failure modes.**` sits as a labelled block at the end of `## Non-goals` (summary.md:416); moving it under `## Defects in the shipped tree that this proposal does not stage` would re-file design residues as confirmed shipped-tree defects, which is an adjudication rather than a format correction. And a stray blank line between summary.md:854 and :856 renders the defects bullet list loose from that point on. Neither is a section-list violation; a pass that wants either changed should take it as a decision.

### [prune.4.fix.1]

DECISION: spec-changes.md's `## Edge cases and accepted failure modes` loses two whole bullets, `**A bind that fails inside its first entry-creating RPC is fenced.**` and `**A compensation still on the wire when a retry reaches the same slot is fenced.**`, and the create-time-reserved bullet loses its closing rationale sentences from "In every ordering" through "by the caller". Nothing replaces them. — BECAUSE each narrated a CLOSED window or the withdrawn response-carried fence and fixed no behaviour; the surviving home of the behaviour is the staged §4.7.1 block (rules 4 and 5 and the stamp-once paragraph), and the reason the token is caller-minted has its one home in the Design paragraph `**The token is minted by the caller, once per attempt, and the first writer owns the entry.**`. The section now carries accepted failure modes only, which is its title. — ALTERNATIVES: another sweep round (rejected by the introspection verdict: the echo class was being found at a flat rate per sweep because the duplicate carriers kept supplying it).

DECISION: summary.md `**Accepted failure modes.**` loses the two-residues-closed paragraph, the four bullets that copied the spec file's Edge-cases bullets, and the position-2 routing paragraph, replaced by one sentence pointing at spec-changes.md's `## Edge cases and accepted failure modes` section as the single carrier. The two priced residues (the refused retry's bounded window and the entry that outlives its attempt costing the pod its MCP arming and unaddressed-frame path) are KEPT, because a grep of spec-changes.md for "cheaper than", "MCP arming", "unaddressed-frame", "burns one attempt" and "remaining life" returns nothing; they appear nowhere else. — BECAUSE the summary states no disposition of its own and names no failure mode the spec file does not list. Three summary references that read "the accepted failure modes below" or "under the accepted failure modes" are re-pointed at the spec-changes file's Edge-cases section (the untokened-entry note in `**Decisions.**`, the reaper reference under the MCP arming defect, and the same-ground reference under the rollback defect); no text was restored.

DECISION: the SPEC-5 §15.1 rationale (the paragraph after the exclusion-sentence replacement) is cut to the one reason for the edit: the started-session refusal is a second deterministic `FailedPrecondition` producer at the setup-command request, and keying the exclusion on the code alone would sweep other stages' refusals into the fallback. The `CREDENTIAL_POOL_EXHAUSTED` counter-example clause and the "after the replacement the row is total" sentence are deleted, and the paragraph cites the Edge-cases bullet `**A bind-sequence refusal reaches the client under the envelope its stage already selects.**` as the home of which envelope each stage's refusal reaches. The rationale names no client-facing code for any request other than the setup-command request. — BECAUSE the stage-by-stage mapping had two homes and the rationale's copy was the one review kept repairing.

DECISION: the `### Open` entry "The graceful-shutdown-signal condition is stated at three sites", CLOSED by round 18 with its body in `[spec.18.fix.1]`, moves to `### Retired` as a one-line entry. It was the only closed item still listed as open.

CORRECTS `[spec.18.fix-design-G1.1]`: its DECISION that the §15.1 rationale clause "becomes '...a credential-assignment failure at `/finalize` surfaces as `CREDENTIAL_POOL_EXHAUSTED`...'" described the text until this pass; the clause is now deleted whole, and the rationale names no code for a non-setup-command request.

CORRECTS `[spec.16.review-citations.1]` WATCHOUT: "two parallels in summary.md:260,455" is now one parallel; the summary.md:455 position-2 routing paragraph is deleted, and summary.md:260 (the §7.1 connection-sentence decision) still stands.

CORRECTS the Settled entry `**DECISION: §7.1 keeps only the consequence...**`: its clause naming the Edge-cases fenced-bind bullet as a citing form now carries the note that this pass deleted the bullet; the minting timing and the response-independence live in §4.7.1's token block alone, with no third site.

FACT: after the edits, a grep of spec-changes.md, summary.md, non-spec-changes.md, review-log.md, implementation-checklist.md and problem-statement.md for "first entry-creating RPC is fenced", "still on the wire", "In every ordering", "closes two residues", "Further residues stand", "CREDENTIAL_POOL_EXHAUSTED" and "finalize precondition note" returns no live-text site. The surviving "position 2" sites (spec-changes.md's untokened-entry bullet, summary.md's §7.1 connection-sentence decision, and the review-log's Traps and Open entries) each name the remediation plan rather than the deleted summary paragraph. non-spec-changes.md's four "recorded among the accepted failure modes" references each resolve to a surviving Edge-cases bullet, so the file was not touched. — EVIDENCE: `grep -n` over the proposal directory excluding the archive.

WATCHOUT: accepted failure modes are stated in spec-changes.md's `## Edge cases and accepted failure modes` section ONLY and are summarised nowhere. The summary's `**Accepted failure modes.**` block is a one-sentence pointer plus the two priced residues; do not re-grow it, and do not add a bullet to non-spec-changes.md's same-named section for a contract-level case.
