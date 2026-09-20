# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass 25, 2026-09-20). Lifted the durable residue of the whole ledger below,
spec rounds 2 through 10 plus the two `f1.open-decisions` passes and `prune.1.fix.1`: 33 new Settled
lines, 18 new Traps, 13 new Open entries, 9 new Deferred entries. Retired two Open entries a
`CORRECTS` closed (whether §15.4 states a rule of its own; whether its conformance criterion
over-reaches) and two Deferred entries the staging discharged (SCHEMA-1's two proto report-trigger
sentences, DOCS-2's `adapter-contract.md` `ReportSessionScrub` row). Deleted no trap. Did NOT reach
the 472-line target: this section is roughly 640 lines, because Traps absorbed eighteen new dead ends,
eight of which cost a round each, and Deferred must carry each correction whole for the pass that
applies it. Nothing that mattered was dropped to make the number.

A hard compaction on 2026-09-20 moved the prior standing context whole, all 2,308 lines, into
`0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md` under the heading
`## Standing context as of 2026-09-20, before hard compaction`.
A claim absent from this section is one this log no longer carries. Recover an old entry from the archive by its bold subject.

How to use this section. Entries are cited by their bold subject. The current staging is: a caller-minted
per-attempt `bind_attempt` token stamped once in `ensureSlotStateLocked` under `s.mu`;
`unconditional_teardown`; `SLOT_BIND_ATTEMPT_SUPERSEDED` and `SLOT_BIND_ALREADY_STARTED`; §4.7.1 rules
1-9 (admission) and 10-15 (`Shutdown`), stated once; the §4.7.1 registry critical-section paragraph as
the one atomicity home; the §5.2 disposition table as the one home of every residue cell; Design as a
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
- **DECISION: the §15.4 conformance criterion is fixed by DELETION plus ONE scope sentence, never by a per-rule quantifier.** The answer enumeration ("the gRPC status, the `ErrorCode` and the `Shutdown` outcome ... except in two places") is gone; the criterion reads "answers as it states", and the lead-in states what the rules govern and that a request's own work is §4.7's, per RPC.
- **The two "exceptions" the deleted §15.4 clause named were never exceptions.** Rule 2 cites §15.4 for its status in its own text, and rule 15 sits inside §4.7.1.
- **DECISION: rule 7 is one sentence, "Any other request is admitted."** Its no-overwrite clause is carried by the stamp-once paragraph. The clause is what makes the admission cascade total, which the §15.4 criterion rests on; do not re-add a token clause.
- **DECISION: §7.1 keeps only the consequence, "A compensation therefore always names one."** The minting timing and the response-independence live in §4.7.1's token block alone; the same pair was written a third time in the Edge-cases fenced-bind bullet and was swept to a citing form in the same edit.
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
- **The verbatim-anchor sweep is DONE and was re-run independently in rounds 2, 4, 5, 7 and 10; every anchor hits byte for byte.** Re-check only an anchor a later fix moves. The cheap form is a script that extracts every fenced block and substring-matches it across `spec/`.
- **The §4.7 row's "[§15.4.2] graceful-shutdown signal" citation is sound although §15.4.2 never uses the phrase.** `drainViaLifecycle`'s own doc comment established the convention. A round was spent on this; do not file it.
- **`PrepareWorkspace` is the only STREAMING RPC among the seven entry-creating RPCs, and its request is a FLAT message** with `session_id` on every frame and no `oneof`. That is what makes rule 9's first-frame form sufficient and leaves §4.1's stream-envelope paragraph and its tier-0 gate untouched.
- **`docs/api/internal.md` is wholesale pre-existing drift** (`StopSession`, `UploadFiles`, a `clean_exit` field, no `Shutdown`), so nothing this proposal stages newly falsifies it.
- **No OpenAPI document and no language SDK mirrors any edited surface.** `SETUP_COMMAND_FAILED` appears in neither `pkg/gateway/externalapi/openapi/openapi.json` nor `sdks/`; the runtime SDKs carry JSONL and CH-RUNTIMEOPS types only.
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

### Traps

- **A residue-disposition cell is changed in the §5.2 table and NOWHERE ELSE, and atomicity in `**The
  registry critical section.**` and nowhere else.** A finding that §7.1, §6.2, the hold paragraph, the
  `**Slot cleanup:**` bullet, §15.4 or Design "does not say" a report, `leaked`, hold or pod-exit fact is
  answered by the table. An uncovered case is ONE TABLE ROW.
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
- **MISTAKE, THREE attempts at ONE §15.4 paragraph.** A per-rule wire table, then a rule-quantified
  criterion unsatisfiable when a request meets rules 5 and 6 at once. §15.4 carries no rule number,
  condition or selection predicate; rule 2 alone delegates its status there. §15.4 completeness findings
  are refuted six times.
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
  cleanup reclaims** (the abandoned attempt's late untokened `StartSession`). The table's residue scope
  is explicitly the pre-`running` case, §6.2 and §7.1 cite it with that scope, and the running orphan
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
  near-misses.

### Open

- **Can the reclaim-hold refusal reach `Binder.Prepare`/`Binder.Launch` and drain the pod?** — OPEN: `errSlotReclaimInProgress` is a bare `codes.Aborted` matching neither CODE-7 sentinel, so CODE-8's guard falls through to `failPhase` and drains.
- **Does a permanently held slot identifier have to be answered as permanent?** — OPEN: a life-of-the-pod hold repeats `ABORTED`, which §15.4 publishes as retryable; the entry reaper is routed to remediation position 2.
- **Does rule 8's untokened arm compare equal across a replacement?** — UNVERIFIED: two untokened entries under one identifier compare "" to ""; no reachable interleaving shown. Check the SDK-warm `ConfigureWorkspace` path.
- **Which client envelope does a rule-6 `FAILED_PRECONDITION` at a non-setup-command bind request reach?** — UNVERIFIED: `SlotBindError.Reason()` may render non-retryable 422 `SLOT_FAILED`; rule 6 also makes a repeat `StartSession`'s `Unavailable` permanent.
- **Is the staged §5.2 ten-second graceful window the right figure, and does it want an operator override?** — OPEN: §10.1 states no window, runtime graces differ, and CODE-6 makes it per-member.
- **Does the whole-pod scrub racing the per-slot cleanup on the removing arm want an ordering statement?** — OPEN: `answerShutdown` fires asynchronous `startPodScrub` while the hold is held and the tree removal runs.
- **Does the process-group kill fall inside the "every act that cleanup owes the slot" completion predicate?** — UNVERIFIED: nobody traced where the kill runs or whether its failure is observable.
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** — OPEN: a third-party harness cannot force a failed removal, and the recycle-carrying `Shutdown` scrub has no case for want of a `ReportPodScrub` observer.
- **Do `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33` need a DOCS-4?** — OPEN: each says the adapter reports every release, which the §5.2 biconditional falsifies; no deliverable opens either.
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
- **Is "the record of which sessions the pod's shared runtime process holds" the right scope vocabulary in the §15.4 criterion,** or does rule 8's start confirmation fold under "the slot registry" wholesale? — UNVERIFIED [spec.10.fix-design-G2.1].
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
- DEFERRED [pkg/agentpodstate/agentpodstate.go, migrations/0167_*.up.sql, the tier-11 scrub-report
  addressing gate's header]: each keys `sessions_served` or the report on "each session release".
  SPEC-3's §12.6 re-key falsifies that, and no deliverable names any of the three.
- DEFERRED [schemas/lenny-adapter.proto, `Shutdown` RPC comment and `ErrorCode` enum header]: the RPC
  comment predates the two-teardown split, and "The catalog below mirrors spec §15.1" is false for
  codes 27, 28, and 29. SCHEMA-1 stages the `ReportSessionScrub` comments only.
- DEFERRED [docs/reference/execution-modes.md, docs/operator-guide/security-principles.md]: each ends a
  sentence with "and the adapter reports its outcome to the gateway", the universal SPEC-3 withdraws.
  The repair deletes that clause. No DOCS deliverable opens either page.
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
- DEFERRED [non-spec-changes.md, the two tier-11 gate files]: neither appears in a deliverable or in
  `## Files touched on application (non-spec)`. `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`
  owes DELETION of its spec/12 substring block (after the read-clause reduction §12.6 states no
  evaluation point for it to compare; its §5.2, §6.2 and state-machines.md checks are untouched), and
  `tests/tier11_docs/spec_28_register_writers_test.go` owes a re-key of `podStateGatewayWrittenSentence`
  from "incremented at each session release (`ReportSessionScrub`)" onto "incremented on each
  cleanup-outcome report (`ReportSessionScrub`)", the rest of the sentence unchanged. One code-lane
  deliverable should carry both, landing in the same step that applies SPEC-3.
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
- DEFERRED [implementation-checklist.md, S2, S15, S16, S19, S22]: the checklist predates the
  restructure. S15 and S16 undercount their edits and S16 must key the hold release on completion; the
  rest are untraced against the current staging.

### Retired

Bodies are in the archive file. This list names subjects only.

- Open, answered by the current text or recorded in the summary's open decisions or unstaged-defects list: socket runtime on a recycling pool; mid-start `Shutdown` bricks the pod; §4.1's retired vocabulary; `receiving_uploads` on an upload-free plan; §5.2's bullet at concurrency 1; `terminate` frame reason value and `deadlineMs`; whose view of "running" §7.1 means; does §7.3 owe a release step; the other `resuming` bullets; concurrent `/start` serialization; retry re-incrementing the Redis reservation; `maxSessionsPerPod` counting a bind that reached `RunSetup` (decision 21); `ProjectOccupancyPhase` no-claim arm and claim-observed generation (decision 27); pre-`running` residue's return-to-pool exit; one-session replacement pod terminal; gateway `leaked` versus adapter `leaked`; §5.2 and §7.1 both stating the `leaked` disposition; hold's "create or resolve" predicate width; rule 10's pre-registry timing; does a bind attempt span `/finalize` to `/start`; headline over-states what the token closes; two residues living only in reasoning; one-session-pod residue bullet; §4.7 `ReportSessionScrub` row edit; `adapter-contract.md:81` row; three docs sentences against the withheld report (two pages survive as the DOCS-4 item); §10.1.4 termination owing a report; CODE-4 session-keyed credential release; second `AssignCredentials` double-count; MCP surface a retry inherits; successor inheriting armed timers; is tier 8 reached; guard-acquisition expiry tests; rule 8 and deregister-and-hold CONF-1 cases; §7.4 mid-session rule; `metrics.md` untokened row; hold refusal accounting on the resume path; hold bounded at concurrency 1; §16.1 superseded row qualifier; §15.4 "stated once" claim; §15.1 exclusion narrowing; §6.2 Client-visibility inclusion clause; §6.2 fence versus prose on `leaked` at concurrency 1; spec/05 versus spec/06 pod retirement wording; `StartSession` row states no refusal.
- Open, mechanism or wording no longer staged: `/finalize` Gap-2 no-re-dial window (epochless form); which step routes `Shutdown` through `reclaimSlotLocked` (old S9/S10); CONF-1 lettered clauses (a)-(j) and clause (j) buildability; tier-9 epoch arms; the "spends one of the attempts" clause; who owns `slot_cleanup`'s start instant; SPEC-3's exclusive-pod arm; exclusive-pod abandonment disposition; "Seven residues survive" count; nine-versus-eleven adapter file lists; `spec-changes.md:98-102` blob-store clause; deleted `## Revision history`; summary host for accepted failure modes; round-8 unlanded findings; lens-retirement questions; tier-7a park placement; late-reclaim arm `leaked=false` observer.
- Open, still unclosed and cut for budget (implementor-level or pre-existing; re-derive from the archive when one matters): §29 incomplete-enumeration rule; §29.10 shared list; concurrent resume occupancy and orphan GC reach; §15.1 retry reaching a draining pod and `ClaimSlot` pass 1; resume-path `error_type`; compensation budget pin and `budget/2` margin, `SocketRuntimeProcess.Close` grace, and override; lease hold bound under §4.9; `SLOT_FAILED` undocumented; `configuration.md:99`; `/sessions/` and `/artifacts/` in the action list and §6.4; timer cancellation conditioned on credential removal; handler writing after `removeSlotTree`; hold spanning the scrub report; non-`Shutdown` hold-retention seam; CODE-2 idempotency sentence and `Resume` confirmation reacher; `ensureSlotStateLocked` whole-of-the-rule comment; zero-frame `PrepareWorkspace` senders; `slotResolveError` preserving `ABORTED`; coordinator handoff fencing the reclaim; §28.3 `LNK-POD-GRPC` multiplicity; N8 in proposal rationale; `superseded` producers; `recordSetupCommandFailed` audit; spec/07:208 and §15.1 precondition rows and "fresh pod" sentence; `state-machines.md` released-row actions; DOCS-2 tier-11 pins, annotation widening and audience clause; `metrics.md` gateway row placement; `Client.Shutdown` doc comment; CODE-1 leak-accounting paragraph; `SlotReclaim` hook arity; `slotFailureWorkspaceFinalize`; CODE-4 `Targets:` list and deliverable heading file lists; checklist tier digits; unused `noteCompensationOutcome` at S10; test-file lists (`binder_test.go`, `start_test.go`, `manifest_fields_test.go`, tier-3 file names, `slot` basename credit, `client_test.go` §15.4 credit); §11.4 fan-out test; setup-envelope tier-3 case; `holdstate_test.go` fake runtime; `fakeSDKWarmRuntime` workspace base; `terminal_reclaim_internal_test.go`; 0079 overlap row; summary index third anchor and `summary.md:141` attribution; `Resume` stall leak rate; re-placement onto a held pod.
- Deferred entries dropped in the 2026-09-20 hard compaction (bodies in the archive): proto `ReportSessionScrub`/`SessionScrubOutcome` comments (discharged, SCHEMA-1); spec/06 `slot_cleanup → released` annotation and its docs mirror (discharged, SPEC-4 and DOCS-1); error-catalog "nothing, deliberately" (mechanism gone, no-retry alternative); docs four sites that stay true (negative record); archive WATCHOUT re-point and ledger "fresh token" sites (log-only targets); CONF-1 per-frame epoch case, precedence note, property count, short-four-cases (discharged, CONF-1 is one case per rule with rule 9); every "RETIRED IN PASS 22" checklist and non-spec entry (discharged); DOCS-3 missing, DOCS-3 index line, summary "four replacements" (discharged, SPEC-5 now stages four); DOCS-1 `state-machines.md` projection clause (discharged); DOCS-2 `ReportSessionScrub` row (discharged); registered-but-unbound credential claim (discharged); §5.2 action list four trees (carried in summary defects); CODE-5 `Targets:` `isTransientPodClaimError`, CODE-6 heading file list, `noteCompensationOutcome` inverted clause, CODE-9 `catalog.go` path, SPEC-6 preamble count, summary CODE-9 bullet, summary "both RPCs", summary SPEC-2/SPEC-3 bullets, summary "Watch out for" connection bullet, summary Decisions file list, "Spec files touched" `Shutdown`-row description, §15.4 table dependants (discharged); SCHEMA-1 gate-text quote and tier-1 wire-rule five-RPC wording (mechanism gone or cosmetic); checklist frame-predicate and S10 negatives (negative records); accepted-residue two-item enumeration (discharged by the disposition table); duplicates of the `Client.Shutdown`, `Binder.drain`, CODE-4 Targets, CODE-9 verbatim, tier-7a, and checklist S1/S15 entries (duplicate).
- Retired in compaction pass 25, closed by a `CORRECTS` or discharged by the staging: the Open "Does §15.4 state a rule of its own after all?" (round 5 reduced the block's closing sentence to a pointer, leaving rule 2's `ABORTED` status, which the lead-in declares); the Open "Does the staged §15.4 conformance criterion over-reach?" (round 10 deleted the answer enumeration and added the scope sentence, and the related UNVERIFIED entries about "except in two places" no longer describe the staged text); the Deferred on SCHEMA-1's two proto report-trigger sentences (absorbed into SCHEMA-1 in round 10); the Deferred on DOCS-2's `adapter-contract.md` `ReportSessionScrub` row (added as DOCS-2's fourth edit in round 4). Carried unsettled for a later pass: `prune.1.fix.1` corrects a Settled entry, "the 'what can meet the reclaim hold' finding was closed by prose at three sites", saying the non-spec accepted-failure-mode bullet is no longer one of the three; that entry is in neither this section nor the live text, so the correction is recorded here verbatim rather than applied.
- Retired by earlier passes (1 through 24, `prune.1.fix.1`), bodies in the archive: the `coordination_generation` fence question; the resume-path exclusion; the slothealth-ledger resume question; §29.4 scoping; the tier-7a drain-gate items; Redis rehydration of leaked occupancy (own proposal); `releaseCredentials` and user-source leases; the reclaim-deadline margin; the spec/18 edit question; a fourth `SlotReason`; every bind-epoch, epoch-latch, `bind_epoch` field-number, and `ExcludePod(s)` entry; the fourteen-step checklist; the §10.1.4 and `DemoteSDK` report carve-outs; the "one return-to-pool edge" question; whether `superseded`, a pairing-rule failure, or `absent` still runs the whole-pod scrub.

## Ledger

### [spec.2.fix-G1.1]
DECISION: SPEC-3's fourth §5.2 anchor (the `**Slot cleanup:**` bullet's leaked-outcome sentence) is now a pointer only at the §5.2 disposition table, with the universal "If cleanup fails, the slot is leaked ..." clause deleted rather than qualified — BECAUSE the table the sentence cites gives `Not entered` for a runtime-given slot whose runtime close succeeds and whose directory removal fails, and for every cleanup failing outside a `Shutdown`, so the retained universal contradicted its own citation inside one bullet — ALTERNATIVES: qualifying the universal in place (rejected: a second full statement of the `leaked` predicate in the section that holds its home, and it goes stale on any table-cell change); changing the table's `leaked` cells so the universal becomes true (rejected: the cells were verified against the tree during the restructure and rest on the SDK-demotion early return and the pre-`running` release outside a `Shutdown`); adding a qualifier at §6.2 or the Scrub-model paragraph (rejected: adds text at a citing site to repair a defect whose remedy is a deletion).
WATCHOUT: the shipped bullet keeps a `See **`leaked` slot semantics** ([Section 6.2](...))` sentence immediately after the replaced one, and it is NOT staged. A future edit that deletes or rewrites the leaked-outcome sentence must leave that pointer standing, because the staged commentary relies on it for what a leaked slot holds — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545
FACT: the three staged §5.2/§6.2 sites that once restated the leaked-cleanup universal are now all pointer-only: the `**Slot cleanup:**` leaked-outcome sentence, the `**Whole-pod replacement trigger:**` parenthetical, and the §6.2 `slot_cleanup ──→ leaked` fence entry. A finding about when a cleanup enters `leaked` is a table-cell change in the §5.2 `**Scrub model.**` append and nowhere else — EVIDENCE: 0081...spec-changes.md:699-712, :719, :923


### [spec.2.fix-design-G1.1]
DECISION: SPEC-3's fourth §5.2 anchor (spec-changes.md:699-706) becomes a pointer only — delete the retained "If cleanup fails, the slot is leaked: the pod continues but the slot is not reclaimed until pod termination." clause and keep one sentence citing the §5.2 disposition table — BECAUSE the retained universal is falsified by the table's own `leaked` column (spec-changes.md:618 `Not entered` for a clean close whose directory removal fails; :620 and :623 `Not entered, because nothing carries the outcome to the gateway`), so applying the staged text would put both the universal and its refutation in one bullet. ALTERNATIVES: (a) qualify the universal ("if cleanup fails on a runtime-given slot reclaimed by a Shutdown...") — rejected, it is a second full statement of the predicate in the same section as its home; (b) change the table to match the universal — rejected, the table is the settled home and the caller directive forbids re-expanding citing sites with disposition text.
FACT: this round already converted the two sibling §5.2/§6.2 restatements to pure pointers (spec-changes.md:719, :923); the fourth anchor was simply missed, so the fix is a reduction at one site with no cascade. EVIDENCE: proposals/0081_.../...spec-changes.md:719,:923
WATCHOUT: the shipped bullet's NEXT sentence ("See **`leaked` slot semantics** ([Section 6.2]...) for the full specification of leaked slot behavior: leaked slots remain counted...") is a separate, unstaged sentence and stays. Do not delete or rewrite it while deleting the universal. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545
WATCHOUT: the commentary at spec-changes.md:704-706 describes the staged text as "becomes a citation", which is false of the text as staged. It must be updated in the same edit or the next reviewer files the same finding against the commentary.

### [spec.2.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens on round 2 — BECAUSE every anchor the round-1 fix groups added quotes the tree verbatim and resolves uniquely, and the three corrected code citations now land on the right lines — ALTERNATIVES: filing the §4.1 commentary's "the requests the carriage table in §4.7.1 names" as a false reference for `unconditional_teardown` (the table has only `bind_attempt` and `mid_session` columns); rejected because the table's `Shutdown` row cell reads "pairs with `unconditional_teardown` under rule 10", the sentence is proposal commentary rather than staged spec text, and §4.1 stages nothing about either field, so no applied-spec text becomes wrong.

FACT: all four new verbatim anchors this round quote the tree exactly and each is unique in its file. §5.2 `**Whole-pod replacement trigger:**` `leaked` parenthetical — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561. §4.7 `ReportSessionScrub` row (the only occurrence in spec/, and one physical line) — EVIDENCE: spec/04_system-components.md:692. §6.2 fence `slot_cleanup ──→ leaked`, 4-space indent, em dash — EVIDENCE: spec/06_warm-pod-model.md:148. §4.6.1 projection preamble phrase — EVIDENCE: spec/04_system-components.md:409. §6.2 projection preamble phrase "the claim's binding state and disposition, and `sessionPolicy`" — EVIDENCE: spec/06_warm-pod-model.md:80.

FACT: the three corrected code citations are right. `st.started = true` before `Runtime.Start` — EVIDENCE: pkg/adapter/slotsession.go:88. The hold-timeout pass's started-flag selection — EVIDENCE: pkg/adapter/slotsession.go:379-382. The hold-timeout pass deregisters first — EVIDENCE: pkg/adapter/holdstate.go:190 (`members := s.deregisterStartedSessions()`).

FACT: the tier-11 gate the §4.7 row edit reasons about asserts only (a) both carriers contain "addressed by the identifier of the released session and names no slot" and (b) both contain the literal opener "The request is session-scoped: it is " + that string + ".". It compares nothing else in the row, so the opening-clause re-key and the added "on each such report" are safe — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,46-49,67-75.

FACT: §12.6's prose sentence does carry the "([§4.7](...))" pointer the new §4.7 sub-section's rationale rests on, immediately after "respectively", and the staged quote stops before it, so the pointer survives — EVIDENCE: spec/12_storage-architecture.md:481.

FACT: `ProjectOccupancyPhase` really does switch on `o.Current` (the pod's own last-written phase), which is what the new §4.6.1/§6.2 projection-input addition asserts, and the WarmPoolController is the sole writer of that field — EVIDENCE: pkg/controller/warmpool/occupancy.go:128-140 and spec/04_system-components.md:618.

FACT: `mid_session` exists today only on `FinalizeWorkspaceRequest` (field 4); `PrepareWorkspaceRequest` has none, so §15.4's "the mid-session marker to `PrepareWorkspace`" is exact rather than an omission — EVIDENCE: schemas/lenny-adapter.proto:713-724.

DEFERRED [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md:81` is the reader-facing `ReportSessionScrub` row and it states the universal SPEC-3 withdraws — "Report the per-slot cleanup outcome (`released` or `leaked`) at each session release, on a pod of any concurrency and any recycle setting." DOCS-2's scope is stated as "the `Shutdown` row, the `DemoteSDK` row, and one bind-attempt paragraph" (non-spec-changes.md:2453) and the files-touched entry repeats that set (non-spec-changes.md:3782), so this row is in no edit list. What is true instead after SPEC-3: the report is filed only for a cleanup a `Shutdown` performs to reclaim a slot the pod's shared runtime process was given. The fix is a DOCS-2 row replacement in the page's own voice, keeping the addressing sentence word for word because the tier-11 gate above pins it on both carriers. Out of scope for the spec loop; the docs loop owns it.

WATCHOUT: do not re-derive the §5.2 `**Slot cleanup:**` leaked-outcome sentence as a second statement of the `leaked` predicate. It still opens "If cleanup fails, the slot is leaked" and only then points at the table, which is broader than the table's rows. That disposition was chosen by the round-1 fix and is on the already-fixed list; re-filing it burns two verifiers — EVIDENCE: spec-changes.md:701.

WATCHOUT: the two rationales for the same withdrawal disagree in wording. The SPEC-3 §5.2 whole-pod-trigger rationale says the parenthetical's timeout "is none of them" (spec-changes.md:720), while DOCS-1 says "A timeout is one cause of a failed cleanup rather than the condition" (non-spec-changes.md, the `slot_cleanup -> leaked` clause). Both are rationale rather than staged text and neither changes an applied surface, so it is below the bar; note it only so the next agent does not spend the round on it.

### [spec.2.review-client-surface.1]

DECISION: returned an empty findings list for the client-facing-surface lens in round 2 — BECAUSE every client-visible surface the round-2 hunks touch is complete on the spec side, and the two incomplete parallels I found both have their remedy in `non-spec-changes.md`, which this loop may not edit — ALTERNATIVES: filing the two DEFERRED items below as (d) missing-edit-site findings; rejected on the loop's explicit scope rule that a finding whose only remedy is a docs or schema change costs two verifiers and cannot be closed here.

FACT: the spec side of the `ReportSessionScrub` reporting rule is now complete. Only four spec sites name that RPC's trigger and all four are staged: `spec/05_runtime-registry-and-pool-model.md:453` (Scrub model opening sentence), `:545` (Slot cleanup bullet reporting sentence), `spec/04_system-components.md:692` (the §4.7 Adapter → Gateway row, new this round), and `spec/12_storage-architecture.md:481` (`sessions_served` write trigger). — EVIDENCE: `grep -rn ReportSessionScrub spec/` returns exactly those four.

FACT: the tier-11 gate that holds the §4.7 `ReportSessionScrub` row to its reader-facing mirror asserts only the addressing sentence, through the literal opener `"The request is session-scoped: it is "` plus `"addressed by the identifier of the released session and names no slot."`, on a single physical line found by `lineContaining`. The staged replacement keeps that sentence word for word, so the row edit does not turn the gate red — and equally, the gate cannot catch the reporting-trigger divergence the DEFERRED items below name. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:57,:67-75.

DEFERRED [0081...non-spec-changes.md, SCHEMA-1]: SCHEMA-1 replaces only the RELEASED/LEAKED effect sentences in `schemas/lenny-adapter.proto`. Two comments on that same wire surface still state the trigger as universal, which SPEC-3's §5.2 biconditional (report iff the cleanup is one a `Shutdown` performs to reclaim a slot the shared runtime process was given) falsifies: `schemas/lenny-adapter.proto:308-309` "reports the outcome of the per-slot cleanup the adapter runs on every session release (§5.2), across the `maxConcurrentSessions > 1` and recycling cases alike", and `:451-452` "carries the §5.2 per-slot cleanup outcome the adapter reports on every session release". What is true instead: the adapter reports only for a `Shutdown`-performed cleanup of a runtime-given slot. Both should take a `§5.2`-citing replacement in SCHEMA-1, in the same commit as the §4.7 row.

DEFERRED [0081...non-spec-changes.md, DOCS-2]: DOCS-2's edit list covers the `Shutdown` row (`docs/reference/adapter-contract.md:75`), the `DemoteSDK` row (`:64`) and one added bind-attempt paragraph. It does not cover the `ReportSessionScrub` row at `:81`, which reads "Report the per-slot cleanup outcome (`released` or `leaked`) at each session release, on a pod of any concurrency and any recycle setting." That is the reader-facing mirror of the §4.7 row SPEC-3 now re-keys, and it states the universal §5.2 withdraws. The spec-lane text that goes false with it is the closing line of `## Spec files touched`, "Each reader-facing reference page that mirrors these sections moves with the section it mirrors" (spec-changes.md, last line). Fix: add the `:81` row to DOCS-2, keeping the addressing sentence verbatim so the tier-11 gate's opener still matches.

DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go]: SPEC-4 adds "the phase the pod currently projects" to the projection-input enumeration in §4.6.1 and §6.2. The operator-visible CRD description carries the same enumeration without it — "computes it as a level-triggered projection of per-pod SandboxClaim existence, the claim's binding state, and the pool's sessionPolicy (§4.6.3)" (pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118, rendered into charts/lenny/crds/lenny.dev_sandboxes.yaml:405-411 and pkg/embedded/crds/lenny.dev_sandboxes.yaml:407). It already omits "disposition", so it is a summary rather than a second home, and the remedy is a Go doc-comment edit plus `make generate` rather than a hand edit of either YAML. Weak; record only.

WATCHOUT: `pkg/sandbox/slotstate/slotstate.go:104` and `pkg/gateway/runtime/slothealth/slothealth.go:33` both carry the retired "cleanup timeout exceeded" gloss for `slot_cleanup → leaked` in Go doc comments. Round 1 found the three prose homes (spec/06:148, spec/05:561, docs/reference/state-machines.md:251) and round 2 staged all three; these two code comments are a fourth and fifth and are in no edit list. Code lane owns them.

FACT: the code citations the round-2 hunks corrected all check out on the tree: `pkg/adapter/holdstate.go:190` is `members := s.deregisterStartedSessions()` (the hold-timeout pass deregistering first), and `pkg/adapter/slotsession.go:379-382` is the `for id, st := range s.slots { if st.started {` selection, with `:88` the `st.started = true` that precedes `Runtime.Start`.

### [spec.2.review-docs-alignment.2]

DECISION: returned an EMPTY findings list for the documentation-alignment lens on round 2's spec
staging — BECAUSE every docs surface this round's new spec text falsifies is either already
staged or has a remedy that lands outside this loop's file — ALTERNATIVES: filing the
`docs/reference/adapter-contract.md:81` intra-page contradiction (declined: standing-context OPEN
entry says the remedy is a DOCS-2 edit, so no spec lane can close it) and filing the
`schemas/lenny-adapter.proto:436-437,:451-453` "on every session release" comments (declined:
same, SCHEMA-1 is the non-spec lane, and the standing dead end on the "session release" framing
applies).

FACT: the round-2 spec hunks that touch a docs-mirrored surface are exactly four, and three are
already staged in DOCS-1: §6.2 fence `slot_cleanup ──→ leaked` annotation →
`docs/reference/state-machines.md:251`; §4.6.1 and §6.2 projection-input enumeration →
`docs/reference/state-machines.md:138`; §5.2 `**Whole-pod replacement trigger:**` parenthetical →
no docs mirror states the timeout predicate (`docs/reference/state-machines.md:247` states only
the fail/leak counting lifetimes). The fourth, the new §4.7 `ReportSessionScrub` row, mirrors to
`docs/reference/adapter-contract.md:81`, which is unstaged and out of this loop's scope.
— EVIDENCE: docs/reference/state-machines.md:138,:247,:251; docs/reference/adapter-contract.md:81

FACT: `grep -rn "level-triggered projection" spec/ docs/` returns exactly three sites —
spec/04_system-components.md:409, spec/06_warm-pod-model.md:80, docs/reference/state-machines.md:138
— so SPEC-4's projection-input addition has no fourth carrier. `grep -rn "cleanup timeout" spec/
docs/ schemas/ charts/` returns spec/05:545, spec/05:561 and spec/06:148 only; the
`docs/reference/state-machines.md:251` clause spells it "the cleanup timeout is exceeded" and is
the only docs carrier. — EVIDENCE: spec/04_system-components.md:409, spec/06_warm-pod-model.md:80

FACT: no shipped tier-11 gate turns red on round 2's spec edits, and I checked the three that
could. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43) asserts only the
addressing sentence and the opener `"The request is session-scoped: it is "`, both of which the
staged §4.7 row keeps word for word. `TestLeakedSlotCountingLifetimeAgrees_F5231`
(tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:76) asserts
"counted within a rolling 5-minute window" and "counted persistently for as long as the slots
remain leaked" on the §5.2 whole-pod-replacement bullet; both sit before the replaced
parenthetical. `TestStateMachinesDocMirrorsThePerSlotSubStateScope`
(tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go) requires the §6.2
concurrent block to keep the `slot_cleanup ──→ leaked` edge string and the docs concurrent
section to keep "`slot_cleanup -> leaked`"; both survive the annotation replacement.
— EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-77

FACT: every code and spec citation the round-2 hunks add or change resolves. holdstate.go:190 is
`members := s.deregisterStartedSessions()` (the hold-timeout pass's deregistration);
slotsession.go:379-382 is the `st.started` selection loop; slotsession.go:88 is `st.started = true`
before the runtime start; the §5.2 whole-pod parenthetical, the §6.2 fence line 148, the §4.7
`ReportSessionScrub` row at spec/04:692, the §4.6.1 preamble at spec/04:409 and the §6.2
projection preamble at spec/06:80 all match the staged "reads, verbatim" blocks byte for byte
(checked with `cat -A` on the fence line, whose `──→` and `—` are multibyte).
— EVIDENCE: pkg/adapter/holdstate.go:190, pkg/adapter/slotsession.go:88,:379-382

FACT: the §4.7 block's rationale "§12.6's re-keyed sentences cite §4.7 as the authority for that
counter" is TRUE and is not in tension with the §12.6 block's "cites §5.2 through the pointer the
DDL comment already carries". The spec/12:481 prose sentence carries BOTH pointers: `([§4.7](...))`
after the "respectively" clause and `([§5.2](...))` after the read-trigger clause. A later lens
that reads only one of the two will think one of the rationales is false. — EVIDENCE:
spec/12_storage-architecture.md:481

USEFUL [Standing context Traps, "Do NOT file `docs/reference/adapter-contract.md:81` ... under
SPEC-3's withheld report" and the OPEN "Does `docs/reference/adapter-contract.md:81`'s
`ReportSessionScrub` row survive the biconditional?"]: together these saved a full finding. The
first blocks the SPEC-3 framing; the second records the sharper intra-page framing AND that its
remedy is DOCS-2, which is what puts it outside a spec-lane loop. A docs lens reaching :81 should
stop at the second entry, not the first, because the first's refutation does not cover the
DOCS-2-created contradiction.

### [spec.2.review-fresh.1]

DECISION: returned an empty findings list for the spec lane this round — BECAUSE every hunk in
`diff spec-r1-prefix` verified against the tree, and the two defects I did derive both land in
files this loop may not edit. ALTERNATIVES: filing the §4.1 "the carriage table names" wording
(the §4.7.1 table has no `unconditional_teardown` column, only a mention inside the `Shutdown`
row's `bind_attempt` cell) — rejected as prose ambiguity rather than a false citation, and it
would have failed the materiality bar.

FACT: every verbatim anchor the round-1 fixer added matches the tree byte for byte. Checked:
spec/05_runtime-registry-and-pool-model.md:561 (`**Whole-pod replacement trigger:**`
parenthetical), spec/04_system-components.md:692 (`ReportSessionScrub` row, and it is inside the
`*Adapter → Gateway RPCs:*` table that opens at :688, so the new SPEC-3 heading is right),
spec/06_warm-pod-model.md:148 (`slot_cleanup ──→ leaked`, four-space indent preserved),
spec/06_warm-pod-model.md:80 and spec/04_system-components.md:409 (the two projection input
enumerations), docs/reference/state-machines.md:138 and :251.

FACT: the corrected code citations are right. `pkg/adapter/holdstate.go:190` is
`members := s.deregisterStartedSessions()` (the hold-timeout pass deregisters first);
`pkg/adapter/slotsession.go:379-382` is the `if st.started` selector inside
`deregisterStartedSessions`; `pkg/adapter/slotsession.go:214-220` is `releaseSessionSlot`, which
both the SDK demotion (`pkg/adapter/sdkwarm.go:236,241,251,298`) and the failed-start handlers
call and which discards both release errors; `pkg/controller/warmpool/occupancy.go:134-140` is
the `case state.Claimed: return state.Draining` arm.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43) pins only the
addressing sentence and the opener `"The request is session-scoped: it is "`, read through
`lineContaining`. The staged §4.7 row keeps both, so the gate holds. It does NOT pin the row's
first clause, so it does not catch the docs drift recorded below.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-75

FACT: `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` asserts only the
presence of edge strings (`:32-38`, `:65`, `:120`), never their parenthetical annotations, so
replacing both `slot_cleanup ──→ released` and `slot_cleanup ──→ leaked` annotations with
`(see §5.2)` turns nothing red.

DEFERRED [non-spec-changes.md, SCHEMA-1]: the shipped proto RPC comment at
`schemas/lenny-adapter.proto:308-310` opens "ReportSessionScrub reports the outcome of the
per-slot cleanup the adapter runs on every session release (§5.2), across the
`maxConcurrentSessions > 1` and recycling cases alike." That is the universal SPEC-3's scrub-model
biconditional withdraws, so it becomes false when the proposal lands. SCHEMA-1 replaces only the
`RELEASED`/`LEAKED` effect sentence (`:311-313`) and the `SESSION_SCRUB_OUTCOME_RELEASED` enum
comment; the first sentence is in no edit list. What is true instead: the report is filed when,
and only when, the cleanup is one a `Shutdown` performs to reclaim a slot the pod's shared runtime
process was given.

DEFERRED [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md:81`'s
`ReportSessionScrub` row reads "Report the per-slot cleanup outcome (`released` or `leaked`) at
each session release, on a pod of any concurrency and any recycle setting. ... The gateway
increments the pod's served-session count and feeds the leak ledger." Both the trigger and the
count clause state the withdrawn universal. DOCS-2 edits only the `Shutdown` and `DemoteSDK` rows
and adds the bind-attempt paragraph, and the `## Files touched on application (non-spec)` entry
for that page lists the same three. What is true instead: the same biconditional as above, and
the count advances on each such report. NOTE: the round-0 "already found and fixed" list carried
"The new §4.7 `ReportSessionScrub` re-key leaves its docs and proto mirrors stating the withdrawn
universal" as FIXED; the spec half landed and both mirror halves did not.

WATCHOUT: `scratchpad/cp-snap/0081-opt2/` now holds `spec-r1-prefix` and `spec-r2`, and `spec-r2`
is byte-identical to the live proposal except the review log. Diff against `spec-r1-prefix`, not
`spec-r2`, or the delta reads as empty.

USEFUL [restructure/compaction pass 24 Standing context]: the three reading rules and the
"bind epoch is withdrawn" note meant I did not chase the `expected_bind_epoch` vocabulary that
still appears in the older ledger entries.

### [spec.2.review-kubernetes.1]

FACT: SPEC-4's new projection input, "the phase the pod currently projects", is NOT a
controller reading back its own status as a command channel; it is what the shipped code
already does. `occupancy.Current` is a struct field documented as "the Sandbox's live
status.phase" and populated from `sb.Status.Phase`, and `ProjectOccupancyPhase` switches on it
in the no-claim arm (Reserved→Idle, Claimed→Draining, default→not owned). The spec prose was
the thing that was incomplete, not the design. Do not file the read-back as a level-triggered
anti-pattern. EVIDENCE: pkg/controller/warmpool/occupancy.go:32-34, :199, :113-144.

FACT: the "sole writer" justification in the staged §4.6.1 preamble is accurate at the
controller level even though TWO reconciler arms write `Sandbox.status.phase`
(OccupancyReconciler and the Sandbox-to-Pod reconciler). Both write under the single
`lenny-warm-pool-controller` field manager, so §4.6.3's sole-writer invariant holds and no
ForceOwnership race exists. EVIDENCE: pkg/controller/warmpool/occupancy.go:146-159,
spec/04_system-components.md:618.

FACT: the partition worry "what answers a claim deleted while the pod projects
`sdk_connecting` / `warming`?" is closed by the code, not by the prose: the no-claim arm
returns ok=false for any Current other than Reserved or Claimed, so the warm-fill writer keeps
the phase. A lens should not file the re-keyed clauses as leaving a projection input
unanswered. EVIDENCE: pkg/controller/warmpool/occupancy.go:141-143.

FACT: the two tier-11 gates that could have been broken by this round's §6.2 and §4.7 edits
both survive them. `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` only asserts edge
NAMES and block membership, so replacing the `slot_cleanup ──→ leaked` annotation with
`(see §5.2)` and adding `receiving_uploads ──→ slot_cleanup` to the general block is fine.
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` only asserts the substring
"addressed by the identifier of the released session and names no slot" on one physical line
of each carrier, which the re-keyed §4.7 row preserves verbatim. EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:55-74,
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,:46-75.

FACT: every "cleanup timeout" statement of the `leaked` predicate in the tree is now in an edit
list. There are exactly three: spec/05:561 (this round's new SPEC-3 anchor), spec/06:148 (this
round's SPEC-4 fence replacement) and docs/reference/state-machines.md:251 (this round's DOCS-1
clause). A later round can re-run `grep -rn "cleanup timeout" docs/ spec/ schemas/ charts/` to
confirm nothing was added.

DEFERRED [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md:81`, the
`ReportSessionScrub` row, is in NO edit list and becomes false once SPEC-3 lands. It reads
"Report the per-slot cleanup outcome (`released` or `leaked`) at each session release, on a pod
of any concurrency and any recycle setting. ... The gateway increments the pod's served-session
count and feeds the leak ledger." That is exactly the universal SPEC-3's §5.2 biconditional
withdraws and the new §4.7 SPEC-3 block re-keys on the specification side. DOCS-2 opens the same
page but only its `Shutdown` row (`:75`), its `DemoteSDK` row and one added paragraph
(non-spec-changes.md:2453, :3782). The tier-11 gate does NOT catch this: it pins only the
addressing sentence, which both rows keep. The fix is a DOCS-2 anchor re-keying the row's
opening clause onto §5.2's terms, keeping the addressing sentence and the row on one physical
line. Out of scope for the spec loop, which is why it is deferred rather than filed.
EVIDENCE: docs/reference/adapter-contract.md:81; spec-changes.md:743 (the new §4.7 row);
non-spec-changes.md:2453,:3782.

USEFUL [restructure/standing context]: the "TWO TABLE FACTS verified against the tree" note in
the brief (SDK demotion close-failure deregisters nothing; `st.started` set before
`Runtime.Start`) matched the tree on re-check, and the round's re-anchored code citations are
all correct now: `pkg/adapter/holdstate.go:190` is `members := s.deregisterStartedSessions()`,
`pkg/adapter/slotsession.go:88` is `st.started = true`, and `:379-382` is the started-flag
selection loop. The previous `:249-254` / `:383-385` citations are gone.

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE every
k8s-touching hunk this round (the §4.6.1/§6.2 projection-input addition, the §4.7
`ReportSessionScrub` re-key, the §5.2 whole-pod-trigger parenthetical) either matches the
shipped controller exactly or carries a clause forward verbatim from the current spec.
ALTERNATIVES: I considered filing the projection input as a status-read-back anti-pattern and
as a stale-informer-cache hazard, and rejected both: the code already reads it, and the
stale-read argument is hypothetical hardening the brief tells me not to file.

### [spec.2.review-mechanism.1]

FACT: the three spec homes of the `leaked` trigger predicate are exactly
spec/05_runtime-registry-and-pool-model.md:545 (the `**Slot cleanup:**` bullet's
"If cleanup fails, the slot is leaked" sentence), spec/05:561 (the
`**Whole-pod replacement trigger:**` bullet's "cleanup timeout exceeded"
parenthetical) and spec/06_warm-pod-model.md:148 (the fence entry). A fourth
mirror is docs/reference/state-machines.md:251. Round 1 converted :561, :148 and
the doc clause to pointers; :545 was converted to a pointer PLUS a retained
universal, which is the one remaining contradiction. — EVIDENCE:
spec-changes.md:701

WATCHOUT: a "citation" replacement that keeps the old universal sentence and
appends a pointer is not a reduction. Grep the replacement block for the
predicate words, not just for the added pointer. — EVIDENCE:
spec-changes.md:699-706

FACT: the tier-11 gate that pins the §4.7 `ReportSessionScrub` row is
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43.
It asserts only the addressing sentence ("The request is session-scoped: it is
addressed by the identifier of the released session and names no slot.") on both
the spec row and docs/reference/adapter-contract.md, through `lineContaining`.
The proposal's claim about this gate (spec-changes.md:745-750) is accurate.

DEFERRED [non-spec-changes.md / docs]: docs/reference/adapter-contract.md:81's
`ReportSessionScrub` row still reads "Report the per-slot cleanup outcome
(`released` or `leaked`) at each session release, on a pod of any concurrency and
any recycle setting. ... The gateway increments the pod's served-session count".
SPEC-3's new §4.7 anchor and §5.2 biconditional withdraw that universal and re-key
the counter onto the report. That row is in no edit list: DOCS-2
(non-spec-changes.md:2452) edits only the `Shutdown` row, the `DemoteSDK` row and
one added paragraph. The tier-11 gate above does not catch it, because the
addressing sentence is unchanged. The docs loop owns the fix.

FACT: `pkg/controller/warmpool/occupancy.go` returns `ok=false` (default arm,
:141-143) for any phase other than `Reserved`/`Claimed`, so a pod already in
`draining` with no claim is not re-projected. That is what makes SPEC-4's
re-keying of the claim-deletion clauses onto "the phase the pod currently
projects" self-consistent across reconciles, and it is why the no-claim clause had
to be narrowed to a warm-inventory phase. Verified: :128 `switch o.Current`,
:129-133 Reserved→Idle, :134-139 Claimed→Draining.

FACT: code citations changed this round all check out — `pkg/adapter/slotsession.go:88`
(`st.started = true`), `:379-382` (the started-flag selection inside
`deregisterStartedSessions`), `:214-220` (`releaseSessionSlot`, which sdkwarm.go:298
calls for the demotion), `pkg/adapter/holdstate.go:190`
(`members := s.deregisterStartedSessions()`), and `pkg/adapter/sdkwarm.go:280-282`
(the demotion returns on a failed `sw.DemoteSDK` before any deregistration).

UNVERIFIED: the orchestrator's brief states "section 15.4 is a table keyed on rule
number". The staged §15.4 insert (spec-changes.md:1053-1060) is two prose blocks
with no table. Nothing in the staged text is wrong because of it, but a later
round should not go looking for that table.

### [spec.2.review-performance.2]

DECISION: returned an EMPTY findings list on the round-1→round-2 delta for the performance /
scalability / failure-mode lens — BECAUSE every hunk in the delta is a reduction (a citation in
place of a restatement), a citation repair, or a re-key that moves spec text TOWARD the shipped
code, and none of them adds a write, a watch, a lock scope, or a failure path. ALTERNATIVES:
four candidates built and dropped; each is recorded below so the next round does not rebuild
them.

FACT: the new §4.7 `ReportSessionScrub` row's re-key ("increments `sessionsServed` ... on each
such report") does NOT relax `recycle.maxSessionsPerPod`, and a future lens can stop checking.
The shipped gateway already increments the counter inside the report handler, not at a session
release — `ScrubReporter.RecordSessionScrub` calls `IncrementSessionsServed` and nowhere else
does. And the shipped ADAPTER already files the report only from the `Shutdown` handler and only
on the `bound` arm, which is exactly the staged biconditional's predicate. So the row's re-key
is spec-catching-up-to-code in both halves, and the count of increments is unchanged.
— EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:441,:457;
pkg/adapter/session.go:238-240 (`bound := removed && st.sessionID != ""`), :279
(`s.reportSessionScrub` inside `if bound`).

FACT: `st.sessionID` is set ONLY in `claimSessionSlotUnderLock`, beside `st.started`, i.e. at
`StartSession`. A pre-`running` slot therefore has `st.sessionID == ""`, so the SHIPPED
`Shutdown` handler takes `bound == false`: it runs no runtime close, removes no tree, files no
report, and answers `ExitedCleanly: true`. This is the baseline the staged pre-`running` rows of
the §5.2 disposition table change, and it is why the widened row ("Pre-`running` slot, reclaimed
by a `Shutdown`") is the fix's intent rather than a regression.
— EVIDENCE: pkg/adapter/slotsession.go:87-88; pkg/adapter/session.go:238-243,:290.

FACT: the two citations this round repaired both check out. `pkg/adapter/holdstate.go:190` is
`members := s.deregisterStartedSessions()`, so "the hold-timeout pass deregisters first" is
right; `pkg/adapter/slotsession.go:379-382` is the `for id, st := range s.slots { if st.started`
selection, so "selects on the started flag" is right. Do not re-verify.

MISTAKE (nearly filed, round 2): I built a failure-mode finding that SPEC-4's new projection
input parenthetical ("the controller's own last level, which it may read back because it is the
sole writer of that field") makes the projection unreliable across a WarmPoolController leader
failover, because a coalesced reconcile past the `reserved` patch plus the claim DELETE reads a
stale `claimed` and drains a pod the shipped clause would have returned to `idle`. It does NOT
meet the bar: `ProjectOccupancyPhase` already switches on `o.Current` and already returns
`Draining` for `Claimed` with no claim, so SPEC-4 transcribes the controller rather than
changing it. This is the SAME hazard already recorded as open decision 27 and as a barred
family. — EVIDENCE: pkg/controller/warmpool/occupancy.go:128-140,:57-59; review-log.md:792,
:1784, :3430, :4498.

FACT: the tier-11 gate the new §4.7 row cites really does read only the addressing sentence and
its opener, so the staged row's unchanged final sentence keeps it green. The gate asserts the
substring "addressed by the identifier of the released session and names no slot" and the
opener "The request is session-scoped: it is " on both the spec row and the
`docs/reference/adapter-contract.md` row; it asserts nothing about the row's first clause or the
`sessionsServed` clause. — EVIDENCE:
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:43-76.

UNVERIFIED: whether `docs/reference/adapter-contract.md`'s own `ReportSessionScrub` row states
the withdrawn "at a session release" universal that the new §4.7 row drops. DOCS-2's staged
edits name only the `Shutdown` and `DemoteSDK` rows. I did not file it: its fix lands in the
non-spec staging, which this loop may not edit, and it is outside my lens. The CODE/docs loop
should check that row.

### [spec.2.review-reliability.1]

DECISION: Returned no findings this round — BECAUSE every reliability-relevant hunk in the r1→r2
diff is a REDUCTION (a restated rule replaced by a citation) and each one preserves the mechanism
it used to state in full; I traced each one to the site that now owns it and the owner states it.
ALTERNATIVES: I considered filing four candidates and rejected each on evidence, listed below so
nobody re-derives them.

FACT: The four reliability candidates I chased and refuted myself, with the evidence that closed
each. (1) "The `**Whole-pod replacement trigger:**` pointer loses the bound on a hung cleanup" —
refuted: the `**Slot cleanup:**` bullet's `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)`
formula is explicitly unchanged (spec-changes.md:667) and the staged hold paragraph bounds the
runtime close by the `Shutdown`'s graceful window, the request deadline, or ten seconds for the
§10.1 path (spec-changes.md:628), so a timed-out act is a failed act and the table's "An act
fails" rows cover it. (2) "The §4.7 `ReportSessionScrub` re-key makes a retried report
double-increment `sessionsServed`" — refuted: the staged scrub-model append states "A session
release produces at most one cleanup-outcome report" (spec-changes.md:610), which is the dedup
anchor. (3) "A permanently held slot identifier is a capacity leak with no reclaimer" — refuted:
the slot identifier IS the session identifier and is never reused across sessions
(spec/05_runtime-registry-and-pool-model.md:395 "the gateway mints one identifier at claim time,
and that single value is both the session's identifier and its slot's identifier"), so a hold
held for the life of the pod blocks only that session's own retries, which the one-retry default
bounds. (4) "The clean-exit flag is undefined for a `Shutdown` that runs no runtime teardown
(a pre-`running` slot)" — refuted: rule 15 defers it to the §5.2 table
(spec-changes.md:1015 "only a response reporting `reclaimed` can report an unclean exit, on the
terms the [Section 5.2] disposition table states") and the two pre-`running` rows set the cell
explicitly (spec-changes.md:619-620).

FACT: The r2 rename of the two pre-`running` table rows from "reclaimed by the pod-side reclaim"
to "reclaimed by a `Shutdown`" is correct and complete under this lens: with rows 621-622 keyed
on "cleaned outside a `Shutdown`", the two pairs now partition every pre-`running` cleanup, and
the unconditional teardown (rule 12) that previously had no row now falls in the first pair.
EVIDENCE: spec-changes.md:619-622.

FACT: Every verbatim anchor the r2 hunks added exists in the tree exactly as quoted. The
whole-pod trigger parenthetical is at spec/05_runtime-registry-and-pool-model.md:561; the §4.7
`ReportSessionScrub` row at spec/04_system-components.md:692; the §6.2 `slot_cleanup ──→ leaked`
fence entry at spec/06_warm-pod-model.md:148; the §4.6.1 projection-list opening phrase at
spec/04_system-components.md:409; the §6.2 projection input phrase at
spec/06_warm-pod-model.md:80; the docs `slot_cleanup -> leaked` clause at
docs/reference/state-machines.md:251.

FACT: The r2 fix of the two drifted code citations lands right. `pkg/adapter/holdstate.go:190` is
`members := s.deregisterStartedSessions()` (pass 1 deregisters first) and
`pkg/adapter/slotsession.go:379-382` is the `st.started` selection loop, with `st.started = true`
at `:88` set before `Runtime.Start`. The tier-11 gate the §4.7 row edit must not break,
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`, asserts only the string
"The request is session-scoped: it is addressed by the identifier of the released session and
names no slot." on both carriers, which the replacement preserves word for word.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:66.

FACT: `ProjectOccupancyPhase` already takes the pod's current phase as an input
(`switch o.Current` at pkg/controller/warmpool/occupancy.go:128), so SPEC-4's addition of "the
phase the pod currently projects" to both projection-input enumerations corrects the spec toward
the code rather than inventing a feedback loop. The re-keyed clauses are also a stable fixpoint
under restart: phase is persisted in etcd and the controller is its sole writer, so after a
leader failover the new leader recomputes the same answer from (no claim, phase=claimed).

WATCHOUT: The adapter's slot registry and the new slot-identifier reclaim hold are both process-
memory, so an adapter container restart drops the hold while the residue it guards (the slot's
directory tree) survives on the pod volume. I did NOT file this: the registry it guards has the
same property in the shipped tree, no spec section states adapter-restart semantics, and the fix
would be crash-durability for a pod-local structure, which is hardening. A later lens that wants
it should raise it as its own proposal rather than as a defect in 0081.

DEFERRED [non-spec-changes.md]: The §4.7 `ReportSessionScrub` re-key (spec-changes.md:728-751)
withdraws the universal "at a session release", but TWO reader-facing mirrors still state it and
neither is in an edit list. `docs/reference/adapter-contract.md:81` reads "Report the per-slot
cleanup outcome (`released` or `leaked`) at each session release, on a pod of any concurrency and
any recycle setting. ... The gateway increments the pod's served-session count and feeds the leak
ledger." — DOCS-2's scope is only the `Shutdown` row, the `DemoteSDK` row and one bind-attempt
paragraph (non-spec-changes.md:2453, :3782). `schemas/lenny-adapter.proto:308-310` reads
"ReportSessionScrub reports the outcome of the per-slot cleanup the adapter runs on every session
release (§5.2), across the `maxConcurrentSessions > 1` and recycling cases alike." — SCHEMA-1
replaces only the RELEASED/LEAKED effect sentence at :311-313 (non-spec-changes.md:2222-2234),
leaving the trigger sentence untouched. What is true instead: a report is filed when, and only
when, the cleanup is one a `Shutdown` performs to reclaim a slot the pod's shared runtime process
was given (spec-changes.md:593). The earlier-rounds "already fixed" list claims this pair was
closed; the proto's OUTCOME comments were fixed and the proto's TRIGGER sentence and the
adapter-contract row were not. Both fixes land in non-spec-changes.md, so this loop may not make
them.

UNVERIFIED: Whether the gateway feeds the unhealthy-threshold ledger for a slot that enters
`leaked` with NO report — the pre-`running` Shutdown-failure row (spec-changes.md:620) reports
`None` and relies on the not-set clean-exit flag (spec-changes.md:626 "A slot enters `leaked` on
the gateway's reading of the report or of the response"), while the §4.7 row still states the
ledger feed on reports alone. I judged this consistent (the §5.2 trigger counts slots in
`leaked`, however they got there) and did not file it. The code lane should confirm that CODE-4's
clean-exit branch actually advances the same ledger `RecordSessionScrub`'s `leaked` arm does; if
it does not, that is a code-loop finding, not a spec one.

### [spec.2.review-single-source.1]

DECISION: return ZERO findings for the spec lane this round — BECAUSE every hunk in the round-1→2 diff is a REDUCTION (a restatement cut to a citation) and I could not find a rule left stated in full at two staged sites, nor a citing site left stale by a rename. ALTERNATIVES: I nearly filed two and dropped both, recorded below so the next lens does not spend a verifier pair on them.

FACT: the round-2 diff's spec-lane reductions, all verified against the tree. §4.1 commentary's request enumeration → cites the new §4.7.1 carriage table; rule 2 and the Admission paragraph lost the duplicated hold scope (§5.2's reclaim-hold paragraph is now the sole scope home); the Shutdown-cascade paragraph lost "Rules 11 through 14 are decided inside the registry critical section" and "Rule 10 is decided on the request's fields alone" (moved into the critical-section paragraph and into rule 10); the Admission cascade paragraph lost "It decides rule 1 before it reads the registry and applies rules 2 through 7 inside the registry critical section"; §15.4's hold block now says "a request the §5.2 hold refuses" instead of re-deriving the scope. After all of it, `grep -n "critical section\|indivisible\|under the registry lock"` over spec-changes.md returns only the one normative paragraph (:990) plus two citations (:628, :1003) and the Design/commentary lines. The atomicity claim "This paragraph is the only statement" is now TRUE. EVIDENCE: spec-changes.md:990,:628,:1003,:992,:1009.

FACT: the fenced-block sweep is still clean after this round's three new anchors. 59 fenced blocks; every "text to replace" block occurs exactly once across spec/*.md and every replacement zero times, with the four known legitimate zeros (SPEC-4's `claimed ──→ draining` fence entry and the §4.6.1 preamble and two bullets, whose targets are named in prose). The three anchors minted this round all resolve byte for byte: spec/05_runtime-registry-and-pool-model.md:561 (`**Whole-pod replacement trigger:**` `leaked` parenthetical), spec/04_system-components.md:692 (`ReportSessionScrub` row), spec/06_warm-pod-model.md:148 (`slot_cleanup ──→ leaked`, four-space indent). Do not re-run this sweep unless spec/ moves.

FACT: the two code citations this round rewrote are correct at HEAD. `pkg/adapter/holdstate.go:190` is `members := s.deregisterStartedSessions()` (pass 1, so the hold-timeout pass does deregister first) and `pkg/adapter/slotsession.go:379-382` is the `if st.started` selection inside `deregisterStartedSessions`. EVIDENCE: pkg/adapter/holdstate.go:189-190; pkg/adapter/slotsession.go:375-382.

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts only the addressing sentence and its opener `"The request is session-scoped: it is "`, on the spec §4.7 row and on the `docs/reference/adapter-contract.md` row, both read through `lineContaining`. SPEC-3's new §4.7 replacement preserves both word for word and keeps the row one physical line, so the gate holds. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:46-49,:67-75.

MISTAKE nearly filed: "§4.1's commentary cites the §4.7.1 carriage table for where `unconditional_teardown` is carried, but the table has no such column and its framing sentence scopes it to `bind_attempt` and `mid_session`." It dies on the table's own `Shutdown` row, whose `bind_attempt` cell reads "pairs with `unconditional_teardown` under rule 10", so the table does name the one request that carries the flag. EVIDENCE: spec-changes.md:257-259 versus :981.

MISTAKE nearly filed: "the SPEC-3 commentary says 'The paragraph states a terminal for a cleanup that does not complete', but the staged reclaim-hold paragraph delegates that to the disposition table's hold column." It dies because the paragraph ends the hold only on completion ("every act that cleanup owes the slot has returned without error"), which entails an unending hold for a cleanup that never completes; the table only names the terminal. EVIDENCE: spec-changes.md:628 versus the table row at :619-624 and the commentary at :670-673.

DEFERRED [non-spec-changes.md]: DOCS-1's new `slot_cleanup -> leaked` replacement clause over-states the §5.2 table. It reads "when the cleanup does not complete and the gateway learns of it, in the outcome the adapter reports, in the answer to the request that asked for the cleanup, or in the absence of an answer to a reclaim the gateway sent" (non-spec-changes.md hunk at the `### DOCS-1` section, quoting `docs/reference/state-machines.md:251`). The disposition table has a row where the cleanup does NOT complete and the slot is NOT `leaked`: "Slot given to the pod's shared runtime process, reclaimed by a `Shutdown` | The runtime close succeeds and a directory removal fails | `released` | Set | Not entered" (spec-changes.md:618). What is true is that the slot enters `leaked` when the RUNTIME CLOSE fails on a runtime-given slot, or when any act fails on a pre-`running` slot reclaimed by a `Shutdown`, or when a §7.1 reclaim goes unanswered. The docs clause needs re-keying on that, not on "does not complete". The spec staging itself is correct here; only the docs mirror is wrong, which is why this is deferred rather than filed.

UNVERIFIED: whether `docs/reference/adapter-contract.md`'s `ReportSessionScrub` row still states the withdrawn universal trigger ("at a session release") after SPEC-3's new §4.7 re-key. The spec lane cannot fix it; the non-spec lane should check DOCS-2's scope covers that row, not only the `Shutdown` and `DemoteSDK` rows.

### [spec.3.review-mechanism.1]

DECISION: Returned no findings this round — BECAUSE the round-2→round-3 diff is a single hunk (spec-changes.md:698-713), the fourth §5.2 anchor's replacement text plus its rationale, and it verifies clean under the end-to-end lens — ALTERNATIVES: I considered filing spec/05:488 ("the session release that drives the served-session count to `maxSessionsPerPod`") as a read-trigger site left stale by SPEC-3's re-keying of `sessions_served` onto the cleanup-outcome report; rejected because the sentence identifies the release *that drives* the count, which is by construction the reporting release, so it is not falsified, and the staged §12.6 commentary explicitly keeps the read triggers as written.
FACT: The diff for this round is one hunk only. `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/spec-r2-prefix proposals/0081.../` is 25 lines total — EVIDENCE: spec-changes.md:698-713
FACT: The fourth-anchor replacement now states no disposition at all; it is a pure pointer at the §5.2 disposition table. The verbatim quote it replaces matches the tree exactly, em dash included — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545 "If cleanup fails, the slot is leaked — the pod continues but the slot is not reclaimed until pod termination."; spec-changes.md:695
FACT: The hunk's two repository claims hold against the staged table: `Not entered` is given for a runtime-given slot whose runtime close succeeds and whose directory removal fails, and for every cleanup that fails outside a `Shutdown` (both post-deregistration rows and the SDK-demotion close-fails row) — EVIDENCE: spec-changes.md table rows at :628-638
FACT: "The `leaked` semantics §6.2 states are unchanged" is true after SPEC-4: SPEC-4 edits only the fence annotations at spec/06:148 and :155; the `**`leaked` slot semantics.**` paragraph is a separate block and is untouched — EVIDENCE: spec/06_warm-pod-model.md:160; spec-changes.md:915-918 ("The `**`leaked` slot semantics.**` paragraph below the fence is untouched")
WATCHOUT: The only site in the proposal still describing the fourth anchor as a sentence that "gains a citation" is the S4 checklist entry (implementation-checklist.md:23), which reads as though the old sentence survives with a citation appended rather than being replaced outright. The spec loop is barred from filing checklist drift, so this must be closed by the reconciliation pass between the loops — EVIDENCE: implementation-checklist.md:23
DEFERRED [0081...implementation-checklist.md]: S4 says "the same bullet's leaked-outcome sentence gains a citation of the disposition table". That is false after this round: the sentence is replaced outright by a pointer and retains none of its own disposition text. True statement: "the same bullet's leaked-outcome sentence is replaced by a pointer at the disposition table, withdrawing the universal it stated." — EVIDENCE: implementation-checklist.md:23 vs spec-changes.md:698-713

### [spec.4.fix-followup.1]

These are corrections to the round-4 fix pass, not a separate round. The pass wrote no shard of its own, so they are recorded here under one heading.

DECISION: SPEC-5's new §15.1 exclusion replacement is re-keyed on the setup-command request rather than made total over the bind sequence — BECAUSE the totalizing form swept a deterministic `FailedPrecondition` the adapter answers at `PrepareWorkspace`, `FinalizeWorkspace` or `AssignCredentials` into the retryable fallback, which contradicts SPEC-5's own preamble ("A refusal arriving at any other bind-sequence request reaches the client under the envelope that stage already selects") and the tree, where `/finalize` surfaces a credential-assignment failure as `CREDENTIAL_POOL_EXHAUSTED` and a workspace-materialization failure as the workspace-validation error. ALTERNATIVES: leaving the totality and narrowing "setup-window failure" to the setup-command request (rejected: the re-key is then vacuous and the stated justification false); assigning the other-stage refusal an envelope in this row (rejected: §15.1's finalize precondition note already owns it, and the row would become a second home). EVIDENCE: spec/15_external-api-surface.md:646; spec/06_warm-pod-model.md:290; spec-changes.md:1082-1083.
FACT: the replacement now reads "Any other failure of the setup-command request (…) is not this code; it stays the retryable … fallback …" plus a second sentence sending a `FailedPrecondition` at any other bind-sequence request to that stage's envelope. The justification below it states totality over failures of the setup-command request and single-home status for that request alone — EVIDENCE: spec-changes.md:1131-1145.
FACT: the preamble's sentence count is replaced by the enumeration, because the deliverable stages four sentence replacements in the row — EVIDENCE: spec-changes.md:1084-1085; parallel at non-spec-changes.md:2533 and the files-touched entry at spec-changes.md:1255.
FACT: DOCS-3's justification no longer claims the page states what the §15.1 exclusion states. It now states the difference: the page enumerates the causes a client can see under this code, and the class §15.1 sends elsewhere is not client-visible under this code — EVIDENCE: non-spec-changes.md:2578-2585.
FACT: the open-decision record's SPEC-4 parenthetical now names §4.6.1 alone, with §6.2 as a site that points at it, matching the reduction at spec-changes.md:798-800 and :830-834 — EVIDENCE: summary.md:557-559.

DEFERRED [0081...implementation-checklist.md]: S1 (`:17`) still instructs the withdrawn §6.2 re-key and still says §15.1's exclusion sentence "stands unedited". Both are false against the staging the same step lands: SPEC-5 replaces the exclusion sentence (spec-changes.md:1122-1132) and reduces §6.2's `**Client visibility:**` restating clause to a pointer at the §15.1 row rather than re-keying it (spec-changes.md:1147-1165). True statement: the row's exclusion sentence is also replaced, re-keyed on the request the adapter answered so it is total over failures of the setup-command request and sends a `FailedPrecondition` at another bind-sequence request to that stage's envelope, and §6.2's restating clause is replaced by a pointer at that row. This loop's write list does not include the checklist.

DEFERRED [0081...implementation-checklist.md]: S5 (`:25`) still describes SPEC-4 as re-keying three sites, including the §6.2 fence trigger and three clauses of §6.2's projection prose. SPEC-4 re-keys §4.6.1's two claim-deletion bullets and reduces both §6.2 sites to a pointer (spec-changes.md:798-800, :817-819, :824-834). True statement: §4.6.1's two claim-deletion bullets are re-keyed on the phase the pod projects at the claim DELETE and its false closing sentence is deleted, while §6.2's `claimed ──→ draining` trigger and the claim-existence clauses of its projection prose become pointers at §4.6.1. This loop's write list does not include the checklist.

DEFERRED [0081...implementation-checklist.md]: S7 (`:29`) omits the `ReportSessionScrub` row DOCS-2 now edits (non-spec-changes.md:2505-2513) and the bind-attempt paragraph. Every other enumeration of DOCS-2's footprint was corrected this round (non-spec-changes.md:2453, :3802-3803; summary.md:918, :945; spec-changes.md:1269-1271). True statement: the adapter-contract reference takes four edits, the `Shutdown` row, the `DemoteSDK` row, the `ReportSessionScrub` row (which points at the page's own `Shutdown` row for which cleanups are reported and reports none for any other release) and one bind-attempt paragraph. This loop's write list does not include the checklist.

### [spec.4.fix-G1.1]
DECISION: Closed the runtime-given `Shutdown` partition gap by widening one condition cell only — spec-changes.md:616 now reads "The runtime close succeeds and any other act fails" in place of "... and a directory removal fails" — BECAUSE the three runtime-given rows then quantify over the whole cleanup act set (close fails | close succeeds and all others succeed | close succeeds and some other fails), which partitions exhaustively and cannot reopen when §5.2's action list gains an act, and it brings the row into agreement with the reclaim-hold paragraph (:628) and the keying commentary (:634-636), both already quantified over every act — ALTERNATIVES: a fourth row naming the process-group kill, the §4.9 timer cancellation and the `slotId` release (rejected: its outcome tuple is byte-identical to :616, so it says nothing new and goes stale on the next act added); re-keying the three rows on the adapter's observable branches `closeErr`/`treeErr` (rejected: re-narrows the spec to the code's current bit width and puts the table back in disagreement with the per-act hold rule, the disagreement family that cost run 0081-opt1 eleven rounds); deleting :616 (rejected: its tuple is distinct, report `released` with the clean-exit flag Set while the hold runs for the life of the pod); a sentence after the table (rejected: repairs a table cell with prose at a citing site).
FACT: On the `Shutdown` path the deregistration precedes every act in the cleanup action list, so the widened condition needs no "after the deregistration" qualifier, unlike the outside-`Shutdown` rows — EVIDENCE: pkg/adapter/session.go:237 (`deregisterSlotLocked` under `s.mu`) before the runtime close at :263 and `removeSlotTree` at :270.
WATCHOUT: spec-changes.md:706 still names "whose directory removal fails" as the `Not entered` instance. That is an instance introduced with "for" and stays true under the widened condition; it is a justification paragraph and rewording it is churn — EVIDENCE: spec-changes.md:704-707.
FACT: `kills any processes owned by the slot's process group` is a real §5.2 obligation with no implementation in the adapter's release path; the only `Kill(-pid)` in the adapter is the setup-command cap — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545, pkg/adapter/workspace/setup.go:215. The widened row makes the spec's disposition total over that act; whether CODE-1 must implement it is a non-spec-lane question.
UNVERIFIED: whether CODE-1's staged cleanup path needs to add the process-group kill and the §4.9 timer cancellation so the widened row has an implementable branch. The next non-spec lane should check, since the spec lane may not author there.

### [spec.4.fix-G2.1]
DECISION: DOCS-2 gains a fourth edit, the `docs/reference/adapter-contract.md` `ReportSessionScrub` row (`:81`), whose opening clause becomes "for the cleanups the `Shutdown` row states the adapter reports, and for no other release" — BECAUSE the page is the licensed reader-facing restatement and may carry no section number, while the staged `Shutdown` row on the SAME page already states which cleanups are reported; a within-page pointer plus the one exclusion clause keeps one home for the rule on one page — ALTERNATIVES: the reviewer's suggested wording, which is a second full statement of the §5.2 biconditional on a page that already has one (rejected); deleting the condition from the row entirely, which leaves nothing excluding the `DemoteSDK` cleanup and the hold-timeout termination (rejected); adding a substring gate over the new pointer clause, which pins one carrier's wording rather than agreement between two (rejected, recorded for a reviewer who asks for regression cover).
FACT: the tier-11 gate pins ONLY the addressing sentence of the `ReportSessionScrub` row and requires the exact opener "The request is session-scoped: it is " on BOTH carriers, and reads each row through `lineContaining`, so the row must stay one physical line and that sentence must stay word for word — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:57,:67-73.
FACT: the three adapter-contract rows DOCS-2 touches are at docs/reference/adapter-contract.md:64 (`DemoteSDK`), :75 (`Shutdown`) and :81 (`ReportSessionScrub`), all three verified against the shipped tree on this pass.
WATCHOUT: `docs/reference/state-machines.md`, `docs/operator-guide/security-principles.md`, `docs/operator-guide/multi-tenancy.md` and `schemas/lenny-adapter.proto` also state the universal SPEC-3 withdraws. The proto is staged under SCHEMA-1; the three docs pages are staged nowhere and are falsified by SPEC-3 independently of DOCS-2. Deliberately left for a later round rather than folded into this group — EVIDENCE: docs/reference/adapter-contract.md:81 was the only one inside this finding's causality.
WATCHOUT: "DOCS-2 makes three edits" at non-spec-changes.md:2468 was a count sitting beside the list it counted, and it is what went stale. It is DELETED rather than re-counted to four. Do not reintroduce a count there — EVIDENCE: non-spec-changes.md:2468.
DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: item S7 (line 29) describes DOCS-2's adapter-contract work as the `Shutdown` row and the `DemoteSDK` row only. That is now false in two ways: DOCS-2 also re-keys the `ReportSessionScrub` row so it points at the `Shutdown` row for which cleanups are reported and reports none for any other release, and it adds the bind-attempt paragraph, which S7 already omitted before this round. The DOCS-1, DOCS-3 and gate sentences on that same physical line are unaffected and must carry through unchanged.
UNVERIFIED: whether the shipped `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` and the session-scrub addressing gate live in the same file. DOCS-2's `## Testing` names `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go` for the former; the latter is in `session_scrub_report_addressing_doc_reconciliation_test.go`. No assertion was added to either by this pass, so nothing depends on it yet; the non-spec lane should confirm before extending.

### [spec.4.fix-G3.1]

DECISION: Applied the supplied design for both G3 findings with one wording: a §6.2 copy of a rule owned elsewhere becomes a pointer at the owner, never a re-keyed second statement. SPEC-4 points §6.2's `claimed ──→ draining` fence trigger and the claim-existence clauses of its projection prose at §4.6.1; SPEC-5 replaces §6.2's `**Client visibility:**` restating clause with a pointer at §15.1's `SETUP_COMMAND_FAILED` row. BECAUSE §4.6.1 and §15.1 were already named the owners and both copies had already drifted. ALTERNATIVES: completing each half-done re-key (rejected: leaves two implementable statements and needs two coordinated edits at the next refinement); reducing §6.2's whole projection sentence (rejected: tier-11-pinned, see the FACT below); flipping the home to §6.2 (rejected: §4.6.1's bullet list is the complete projection, §6.2's is a one-sentence digest).

DECISION: Three §6.2 prose clauses go into the SPEC-4 pointer, not two — the no-claim clause joins the two claim-deletion clauses. BECAUSE reducing only the two named in the finding leaves §6.2's unqualified "a pod with no claim projects `idle`" answering `idle` where §4.6.1 answers `draining`, for a claim deleted while the pod projects `claimed`, which is exactly the input a failed bind produces on a `maxConcurrentSessions: 1`, `recycle.enabled: true` pool. ALTERNATIVES: the reviewer's literal two-clause fix, rejected for that contradiction.

DECISION: SPEC-5 now also replaces §15.1's `SETUP_COMMAND_FAILED` exclusion sentence, which the staging had left unedited. BECAUSE SPEC-5 introduces a second deterministic `FailedPrecondition` producer in the setup window, so an exclusion keyed on the gRPC code alone stops partitioning the space; once §6.2 carries only a pointer, a `FailedPrecondition` answered at a non-setup-command bind request would have had no stated envelope anywhere. The replacement keys the exclusion on the request as well as the code and names `Aborted` among the excluded codes, so the superseded refusal stays excluded and retryable.

FACT: `tests/tier11_docs/vm_restart_reprovision_consistency_test.go:180-188` pins five substrings of §6.2's occupancy-projection sentence by `requireLine` on "a `recycling` claim projects `claimed` until its whole-pod scrub reports successful". Any edit that deletes or splits that sentence fails a shipped gate. The three-clause reduction touches none of the pinned substrings. EVIDENCE: tests/tier11_docs/vm_restart_reprovision_consistency_test.go:183-188

FACT: `docs/reference/state-machines.md:138` is the only live tree mirror of the pod-occupancy projection prose, and it carries the same claim-deletion clauses verbatim. Nothing under `tests/`, `scripts/` or `cmd/` compares it to either spec section. EVIDENCE: docs/reference/state-machines.md:138

WATCHOUT: DOCS-1's mirror source is now §4.6.1's bullets rather than §6.2's prose, and the docs page remains a full restatement rather than a citation, because `doc-content.md` bars spec section numbers in published prose. A later round that "reduces" the docs page to a pointer breaks that rule. EVIDENCE: .claude/rules/doc-content.md, "Verify against the spec before asserting behavior"

WATCHOUT: DOCS-3's page sentence enumerating the retryable causes stands unchanged, because the page's reader has no gRPC codes. What changed is only the justification prose around it, which claimed §15.1's counterpart needed no edit "because it is keyed on the gRPC code". That claim is now false at three sites; two were in my grant and are corrected, the third is the checklist DEFERRED below. EVIDENCE: non-spec-changes.md DOCS-3, the paragraph beginning "The sentence enumerates its causes"

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S1 (line 17) states two things that are now false. "The row's exclusion sentence is keyed on the gRPC code and stands unedited" is false: SPEC-5 now replaces that sentence too, re-keying it on the setup-command request and naming `Aborted` among the excluded codes, so §15.1's row is the single home of the setup-window mapping. "§6.2's pre-attached `**Client visibility:**` bullet has its second clause re-keyed the same way, on the request the adapter answered as well as on the gRPC code" is false: that clause is replaced by a pointer at the §15.1 row and restates no mapping.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S5 (line 25) states that S5 re-keys "the fence's `claimed ──→ draining` trigger list, three clauses of §6.2's projection prose, and both claim-deletion bullets of §4.6.1's occupancy-projection list". What is true instead: §4.6.1's two claim-deletion bullets and its opening input enumeration are re-keyed and its false closing sentence is deleted; §6.2's `claimed ──→ draining` fence trigger and the three claim-existence clauses of its projection prose are reduced to pointers at §4.6.1, and no input is added to §6.2's sentence. The fence edge, the pre-`running` paragraph and the tier deferrals in that item are unchanged.

USEFUL [restructure standing context]: the caller directive's "prefer the reduction over the re-key" is what made both findings one edit rather than two contradicting ones; the same reduction closed a fence trigger, a prose clause set and a client-visibility clause.

### [spec.4.fix-design-G1.1]

DECISION: Close the runtime-given `Shutdown` partition by QUANTIFYING the one remaining act-naming cell, not by adding a row. At spec-changes.md:616 the second column becomes "The runtime close succeeds and any other act fails". No other cell, row, sentence or citing site changes — BECAUSE after that edit the condition column of the whole table is uniformly quantified over the act set ("Every act ...", "The runtime close fails", "... any other act fails", "An act fails"), so the table no longer depends on the MEMBERSHIP of §5.2's cleanup action list and a future act added to that list (SPEC-3 itself adds two) cannot reopen the gap. That is the difference in kind from the two earlier rewrites in this region, which both re-keyed or added a row and left the act naming in place — ALTERNATIVES: a fourth row for the process-group-kill / timer-cancellation / slotId-release failures (rejected: it is the enumeration whose next member is the next round's finding, and its outcome cells would be byte-identical to row :616); re-keying all three runtime-given rows on the observable code branches `closeErr`/`treeErr` (rejected: it re-narrows the table to the two bits the adapter has and puts it back in disagreement with the hold paragraph at :628, which quantifies over every act — that disagreement is the exact family that cost run 0081-opt1 eleven rounds); deleting row :616 (rejected: its outcome tuple differs from both siblings, `released`+Set with the hold held for the life of the pod).

FACT: on the `Shutdown` path the deregistration runs FIRST, before the close and the tree removal, so every act in §5.2's cleanup action list happens after it and "any other act" needs no "after the deregistration" qualifier (the outside-`Shutdown` rows need theirs because the SDK demotion closes before it deregisters). EVIDENCE: pkg/adapter/session.go:238 (`deregisterSlotLocked`), :264 (`Runtime.Close`), :271 (`removeSlotTree`), :291 (`ExitedCleanly: closeErr == nil`).

WATCHOUT: the widened cell states a disposition for cases the shipped adapter cannot OBSERVE. There is no process-group kill on the adapter's release path at all (the only `Kill(-pid)` is the setup-command cap), and the timer cancellation and the `slotId` release return no error; `removeSlotTree` collapses the workspace and credential directories into one `treeErr`. A later lens will be tempted to file "the table states an unobservable case". It is not a defect: the hold paragraph already quantifies over every act, and the code lane's `closeErr`/`treeErr` predicate is a conservative subset of the spec's case space. EVIDENCE: pkg/adapter/workspace/setup.go:215; pkg/adapter/slotlayout/tree.go:58-68; spec/05_runtime-registry-and-pool-model.md:545.

FACT: nothing else in the proposal or the tree restates the narrow "directory removal fails" wording as an EXHAUSTIVE claim, so the edit cascades nowhere. spec-changes.md:706 cites it as one instance ("the table gives `Not entered` for a runtime-given slot whose runtime close succeeds and whose directory removal fails"), which stays true as a member of the widened condition; spec-changes.md:634-636 already says "the hold is keyed on every act on every row"; CODE-1's `removeSlotTreeFn`-failure test case remains an instance of the row. EVIDENCE: `grep -rn "directory removal"` over the proposal directory returns only :616, :651, :706 in spec-changes.md.

MISTAKE: the drafting slip is one group only. The pre-`running` rows (:620-621) and the outside-`Shutdown` rows (:618-619) already use the quantified form; a fixer that "harmonizes" them too will churn correct text and may drop the load-bearing "after the deregistration" clause.

### [spec.4.fix-design-G2.1]

DECISION: the added DOCS-2 edit replaces the `ReportSessionScrub` row's false universal with a WITHIN-PAGE POINTER at the same page's `Shutdown` row plus a one-clause exclusion ("and for no other release"), rather than with the reviewer's suggested second full statement of the §5.2 biconditional — BECAUSE the staged `Shutdown` row (non-spec-changes.md:2489) already states the reporting condition on this page, so the reviewer's wording would put one rule at two sites on one page, which is the exact drift family that stopped run 0081-opt1 — ALTERNATIVES: (a) reviewer's "report none for any other release" full restatement, rejected as a second home; (b) delete the row's condition entirely and rely on the `Shutdown` row alone, rejected because nothing then excludes the `DemoteSDK` and hold-timeout cleanups for a reader who starts at the RPC row.
DECISION: every closed enumeration of DOCS-2's edits is made to name FOUR edits (Shutdown row, DemoteSDK row, ReportSessionScrub row, bind-attempt paragraph), and the "makes three edits" count at non-spec-changes.md:2468 is deleted rather than re-counted — BECAUSE two of the six enumerations (spec-changes.md:1257-1258 and implementation-checklist.md:29) were ALREADY missing the bind-attempt paragraph, so adding only the new row would leave two half-lists to be re-found next round — ALTERNATIVES: turn every site into a bare "DOCS-2's edits" citation, rejected because the sibling clauses for DOCS-1/SCHEMA-1/DOCS-3 in the same sentence and the same index all enumerate, and the files-touched list must enumerate edit sites for the implementer.
FACT: the tier-11 gate `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires BOTH carriers to contain the exact string "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." and reads each row through `lineContaining`, so the doc row must stay ONE physical line and that sentence must stay word for word. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:57,:67-73
FACT: after SPEC-3 the two carriers of the reporting condition CANNOT share a substring: spec/04's staged row defers to "the terms [Section 5.2] states" (spec-changes.md:746) while the docs page may carry no spec section number (.claude/rules/doc-content.md, "Verify against the spec"), so no new agreement gate is possible for the condition; only the addressing sentence is gate-able. EVIDENCE: spec-changes.md:746; docs/reference/adapter-contract.md:81
FACT: the adapter-contract RPC rows sit at `DemoteSDK` :64, `Shutdown` :75, `ReportSessionScrub` :81 on the shipped tree, which is what summary.md:918's 0072 collision row cites. EVIDENCE: docs/reference/adapter-contract.md:64,:75,:81
WATCHOUT: the clause to delete is the whole of "at each session release, on a pod of any concurrency and any recycle setting"; the "any concurrency and any recycle setting" half looks like an independent true statement about the RPC's scope, but it is the part that makes the universal explicit. Nothing in tests/ pins it. EVIDENCE: docs/reference/adapter-contract.md:81
WATCHOUT: docs/reference/state-machines.md, docs/operator-guide/security-principles.md, docs/operator-guide/multi-tenancy.md and schemas/lenny-adapter.proto also carry the SPEC-3-withdrawn universal. The proto is already staged under SCHEMA-1 (spec-changes.md:1253-1256). The three other docs pages are staged nowhere and are a SEPARATE finding for a later round, not part of DOCS-2.

### [spec.4.fix-design-G3.1]

DECISION: SPEC-4 keeps §4.6.1's **Occupancy projection** bullet list as the single home of the claim-existence half of the projection, and §6.2's projection prose surrenders THREE clauses to one pointer, not two — BECAUSE the staging's own rationale shows the no-claim clause ("a pod with no claim projects `idle`") is part of the same partition: if the two claim-deletion clauses become a pointer while the unqualified no-claim clause stays, §6.2 still answers `idle` for a pod whose claim is deleted while it projects `claimed`, which is exactly the contradiction SPEC-4 was invented to remove. §4.6.1 already carries the qualified form at spec/04:411 ("- `idle` for a pod in a warm-inventory phase with no claim."), so the pointer loses nothing. ALTERNATIVES: (a) reduce §6.2's WHOLE projection sentence to a pointer at §4.6.1 — rejected, see the WATCHOUT below; (b) the reviewer's literal fix, reducing only the two claim-deletion clauses — rejected, leaves the no-claim clause contradicting §4.6.1.

WATCHOUT: do NOT delete or rewrite §6.2's projection sentence wholesale. A shipped tier-11 test pins five substrings of that exact sentence (the vm-restart / sdk_connecting re-warm scoping) by `requireLine` on "a `recycling` claim projects `claimed` until its whole-pod scrub reports successful". Reducing the sentence to a pointer fails it. None of the pinned substrings is a clause this fix touches, so the three-clause reduction is safe. EVIDENCE: tests/tier11_docs/vm_restart_reprovision_consistency_test.go:178-188.

WATCHOUT: the reviewer's SPEC-4 fix says to drop "the `sessionPolicy`/projected-phase input re-enumeration" from §6.2's preamble. Drop only the projected-phase ADDITION (i.e. do not add it). `sessionPolicy` must stay: the surviving vm-restart/standard/in-place clauses of the same sentence read the pool mode, and those clauses are pinned by the tier-11 test above. EVIDENCE: spec/06_warm-pod-model.md:80; spec-changes.md:823-827.

DECISION: SPEC-5 drops its §6.2 `**Client visibility:**` edit entirely and instead re-keys §15.1's EXCLUSION sentence, which the staging had explicitly left unedited — BECAUSE the staging adds a second `FailedPrecondition` producer in the setup window, so an exclusion keyed on the gRPC code alone stops being a partition; re-keying it at the row (the home) makes §15.1 total over setup-window failures and lets §6.2's clause collapse to the §15.1 pointer it already half-carries. Net effect: one site edited instead of two, and the half-done re-synchronisation the finding names disappears. EVIDENCE: staging's "left exactly as it stands" at spec-changes.md:1118-1123; live row at spec/15_external-api-surface.md:1136; live §6.2 copy at spec/06_warm-pod-model.md:290. ALTERNATIVES: leave the exclusion keyed on the code and reduce §6.2 anyway — rejected, §15.1 then states no envelope for a `FailedPrecondition` answered at a non-setup-command bind request and §6.2 no longer states one either, so the case has no home at all.

FACT: §4.6.1's projection bullet list is the complete projection (no-claim idle, bound/recycling claimed, sdk_connecting, reserved, claim-deleted-while-reserved idle, terminal-disposition draining/terminated), so a §6.2 pointer at it is total for the claim-existence half. EVIDENCE: spec/04_system-components.md:409-420.

FACT: the pointer-inside-a-fence precedent this fix reuses is `sdk_connecting ──→ failed (sdkConnectTimeoutSeconds watchdog fires — see §6.1)`. EVIDENCE: spec/06_warm-pod-model.md:90; the same deliverable already applies it to the two `slot_cleanup` entries at spec-changes.md:893-948.

FACT: DOCS-1's docs page stays a FULL restatement and is the one licensed mirror, because doc-content.md bars spec section numbers in published prose. Only its SOURCE changes, from §6.2's prose to §4.6.1's bullets. EVIDENCE: non-spec-changes.md:2417,2442; .claude/rules/doc-content.md ("Do not cite spec section numbers ... in published prose").

OPEN: §15.1 now maps a started-session refusal answered at the setup-command request to the non-retryable `SETUP_COMMAND_FAILED`, while the same refusal at any other bind-sequence request falls to the retryable fallback and is recovered with a fresh pod. Both are defensible (a fresh pod has no already-started entry), but a human should confirm the split is wanted rather than an artifact of which row the stage happens to land in. Nobody in this round tested it against a client.

### [spec.4.review-applicability.1]

DECISION: Returned an empty findings list for the applicability/sequencing lens on the spec staging — BECAUSE every verbatim anchor in `spec-changes.md` matches the current tree exactly and uniquely, every markdown anchor the staged text writes matches a real heading and the spelling already used elsewhere in `spec/`, and every code/spec line citation in the staged commentary resolves — ALTERNATIVES: rejected filing the §5.2 disposition-table note ("a §7.1 reclaim the adapter does not answer enters [`leaked`] as a reclaim whose act fails does") against the "Pre-`running` slot no cleanup reclaims" row's `Not entered` cell: the note reads as the gateway-side rule for the unanswered case rather than a contradiction of the pod-side rows, and it is the consistency lens's ground, not this one's.

FACT: Anchor sweep result, all verified this round, all unique in their target file: §4.1 third sentence spec/04_system-components.md:157; §4.7 `Shutdown` row :686; `DemoteSDK` row :674; `ReportSessionScrub` row :692; §4.7.9 step 5 :854; §4.7.1 insertion point between :693 and the `#### 4.7.2` heading at :695; §4.6.1 bullets :415 and :416 and the input sentence :409; §5.2 `**Scrub model.**` :453, `**Slot cleanup:**` bullet (all three anchor sentences on one line) :545, `**Whole-pod replacement trigger:**` :561; §6.2 projection prose :80, fence `claimed ──→ draining` :95 (the only one inside the `Occupancy projection` block; three more live in other groups at :114, :135, :137, :139), `receiving_uploads ──→ running` :152, `slot_cleanup ──→ released` :155, `slot_cleanup ──→ leaked` :148, mid-resume cancel clause :234, `**Client visibility:**` :290, `**`reserved` hold semantics.**` :158; §7.1 atomicity paragraph :23 (inside the untagged flow fence, as the proposal says); §7.2 preamble :210, step 2 :213, step 3 :214; §7.3 list tail :414; §12.6 prose :481 and DDL comment :494; §15.1 `SETUP_COMMAND_FAILED` row :1136 (all three replaced sentences on that one line); §15.4 `**SDK-warm demotion contract:**` :1469 with `#### 15.4.1` at :1471; §29.4 step 13 :704-711 — EVIDENCE: the files above

FACT: The tier-11 gate the staging worries about, `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`, asserts only the addressing sentence and the literal opener `"The request is session-scoped: it is "` — tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:45-78. The staged §4.7 row keeps that sentence word for word, so the gate stays green on the spec edit alone. No other shipped gate reads any string SPEC-1 through SPEC-6 delete: I swept tests/, scripts/, cmd/, pkg/ for "reported by the adapter via", "reports each slot cleanup outcome", "If cleanup fails", "incremented at each session release", "under its limits", "only on a recycling pool", "no runtime was started", "no live workspace", "slot workspace removed, processes killed" and found no gate hit.

FACT: `TestGoCommentsNameOnlyLiveSpec47RPCRows` reads row names out of the whole `### 4.7 ` section with `^\|\s*`([A-Za-z]\w*)`\s*\|`, so the new carriage table SPEC-5 inserts inside §4.7.1 adds names to the ALLOWED set only and cannot fail the gate — EVIDENCE: tests/tier11_docs/spec_47_rpc_row_naming_test.go:33-53,136-153

FACT: `TestAdapterMetricsReachTheDocumentationCatalogs` is driven from the names registered in `pkg/adapter/metrics.go`, not from §16.1 rows, so SPEC-6's §16.1 adapter row landing before the code that registers the series cannot fail it — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:52-120. `k8s_pod_name` on the new adapter row is the §16.1.1 canonical label (spec/16_observability.md:297); the `pod_id` on `lenny_adapter_leaked_slots` is the exception, not the convention.

FACT: Every code citation in the staged §5.2/§6.2/Edge-case commentary checks out at the stated lines: pkg/adapter/sdkwarm.go:280-282 (demote returns before any deregistration) and :296-301 (conditional release); pkg/adapter/slotsession.go:88 (`st.started = true`, set before the runtime start) and :214-220 (`releaseSessionSlot` discards the tree-removal error) and :379-382 (the hold pass selects on `st.started`); pkg/adapter/holdstate.go:190 (`deregisterStartedSessions` runs first) and :215-254 (`_ = s.Runtime.Close`, `_ = removeSlotTree`); pkg/controller/warmpool/occupancy.go:57-59, :128-140, :132-140, :134-140; pkg/gateway/podlifecycle/podsession/binder.go:867, :998, :1072-1082, :1200-1202; schemas/lenny-adapter.proto:1621-1627 and :585; spec/15_external-api-surface.md:1699 (the `INIT` row does name `PROTOCOL_VERSION_INCOMPATIBLE`).

DEFERRED [non-spec-changes.md / DOCS-2]: `docs/reference/adapter-contract.md:81` still opens its `ReportSessionScrub` row "Report the per-slot cleanup outcome (`released` or `leaked`) at each session release, on a pod of any concurrency and any recycle setting." SPEC-3 withdraws exactly that universal (the report is now conditioned on a `Shutdown` reclaiming a slot the shared runtime process was given), and DOCS-2's scope is the `Shutdown` row, the `DemoteSDK` row and one added paragraph (non-spec-changes.md:2453, :2468-2507) — the `ReportSessionScrub` row is in no edit list. The spec-side trailing enumeration in spec-changes.md:1257-1258 also lists only the two rows for DOCS-2. What is true instead: after SPEC-3 the doc row must state the report on §5.2's terms. Not filed as a finding because the remedy is a docs edit and this loop may not close it.

UNVERIFIED: the reclaim hold's refusal (rule 2) is answered on `ABORTED` and carries no `ErrorCode` in the staged §4.7.1/§15.4 text, while rule 5's superseded refusal is `ABORTED` + `SLOT_BIND_ATTEMPT_SUPERSEDED`. The gateway therefore cannot tell a hold refusal from an untyped `Aborted`, which matters to S12 (skip `failPhase` "on either typed refusal") and S20 (`isTransientPodClaimError` gains a `codes.Aborted` arm). Someone on the code loop should confirm a hold refusal does not reach `failPhase` and drain a healthy pod.

### [spec.4.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens on round 4 — BECAUSE every concrete
citation in `spec-changes.md` resolves and says what the proposal claims; I re-derived the whole set
mechanically rather than trusting the standing context — ALTERNATIVES: filing the two marginal items
described below, rejected on the stated bar.

FACT: the fenced-block sweep is cheap and worth re-running each round. A ~25-line python script that
extracts every ``` block from spec-changes.md and counts occurrences across `spec/ docs/ schemas/
charts/` runs in seconds. This round: every "text to replace" block occurs EXACTLY ONCE repo-wide,
every replacement block ZERO times, and the only zero-hit originals are the three whose targets are
named in prose rather than quoted (the §6.2 `Occupancy projection` `claimed ──→ draining` trigger
list, and the two §4.6.1 bullets). No new anchor was minted since round 2.
— EVIDENCE: spec-changes.md:814-820, :868-869, :877-878, :886-887

FACT: the markdown-anchor set also script-verifies clean. Generating GitHub-style slugs from every
`#`-heading in `spec/*.md` and matching every `](file.md#anchor)` in spec-changes.md returns 20/20 OK,
including the five same-file anchors. — EVIDENCE: spec/04_system-components.md:659 (`#471-role-and-gateway-rpc-contract`); spec/15_external-api-surface.md:1458 (`#154-runtime-adapter-specification`)

FACT: every code and spec line citation in spec-changes.md was read at the cited offset and is exact.
The ones that cost the most to check, recorded so nobody re-reads them:
  - `pkg/controller/warmpool/occupancy.go:57-59` carries the quoted comment "the §6.2 state machine
    encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim
    DELETE" word for word; `:128-140` is the `switch o.Current` with `Reserved → Idle` at :133 and
    `Claimed → Draining` at :140, so "`ProjectOccupancyPhase` never returns `idle` from `claimed`"
    (spec-changes.md:859) is true.
  - `pkg/gateway/podlifecycle/podsession/binder.go:867`/`:998` are the two `b.failPhase(...)` calls,
    `:1072-1082` is `failPhase` itself and `:1200-1202` is `drain` = `podclaim.DeleteClaim`. No
    `Shutdown` anywhere on that path.
  - `pkg/adapter/sdkwarm.go:280-282` is the `sw.DemoteSDK(ctx)` error return (before any
    deregistration) and `:296-301` is the `anyRegisteredSession()` guard.
  - `pkg/adapter/holdstate.go:190` is `members := s.deregisterStartedSessions()` (deregisters FIRST,
    unlike the demotion) and `:215-254` is `terminateHeldSession`, whose `_ = s.Runtime.Close` (:247)
    and `_ = removeSlotTree` (:252) discard the errors. The ten-second graceful window the staged §5.2
    reclaim-hold paragraph states is `10*time.Second` at holdstate.go:201.
  - `pkg/adapter/slotsession.go:88` is `st.started = true`, `:214-220` is `releaseSessionSlot`,
    `:379-382` is the `if st.started` selection in `deregisterStartedSessions`.
  - `schemas/lenny-adapter.proto:1621-1627` carries the `recycle` comment quoted in the SPEC-1 §4.1
    commentary; `:585` is `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27` and
    `spec/15_external-api-surface.md:1699` is the `INIT` row naming it.
  - `spec/29_communication-scenarios.md:586-588` ("so the runtime is running"), `:589-591` (the
    interrupt path's additional requirement), `:669-674` (step 10's §15.1-cited precondition
    restatement) and `:697` ("the adapter closes the session runtime") all read as claimed.

FACT: `FinalizeWorkspaceRequest` ALREADY carries `bool mid_session = 4` in the shipped proto. I
briefly took the §15.4 commentary's wire-edit list ("the mid-session marker to `PrepareWorkspace`",
spec-changes.md:1068) to contradict the §4.7.1 carriage table, which gives `mid_session` as "carried"
on BOTH `PrepareWorkspace` and `FinalizeWorkspace`. It does not: only `PrepareWorkspace` needs the new
field. Do not file this. — EVIDENCE: schemas/lenny-adapter.proto:713-724;
non-spec-changes.md:2262-2263

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts, on BOTH
spec/04 §4.7 and docs/reference/adapter-contract.md, the exact string "The request is session-scoped:
it is addressed by the identifier of the released session and names no slot." SPEC-3's replacement row
keeps that sentence byte for byte, so the gate holds. The gate reads each row with `lineContaining`,
which is why the row must stay one physical line. — EVIDENCE:
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:43-75

WATCHOUT: two things I weighed and did NOT file, so the next lens does not spend the round re-deriving
them. (1) The Design section says §4.1's paragraph and the §4.7 `Shutdown` row "both treat the runtime
close and the slot release as one act gated on the binding"; the §4.7 row states the single act but
states no gate (the gate is in §4.1 and in shipped code). It is a characterisation in a choice record,
not a citation of row text, and the bar is not met. (2) After SPEC-5 re-keys §6.2's `**Client
visibility:**` clause onto "a deterministic `FAILED_PRECONDITION` answer to the setup-command
request", the bullet's FIRST clause still names only "a deterministic non-zero setup-command exit" as
what surfaces `SETUP_COMMAND_FAILED`, so the started-session refusal is excluded from the retryable
fallback without the bullet saying where it lands. The bullet cites §15.1, which SPEC-5 makes state
it. That is a gap a reader closes by following the citation, not a contradiction.

USEFUL [Standing context, "The anchor sweep is done, and the LIVE count is FOURTEEN sites"]: the
entry's own instruction — re-run the sweep only if `spec/` moves — is right, but re-running it as a
script costs about a minute and is what let me say "no new anchor since round 2" with evidence rather
than by trusting the entry.

USEFUL [Standing context, "The snapshot diffs have been empty for most rounds"]: `diff -rq` over the
whole `scratchpad/cp-snap/0081-opt2` chain shows `spec-r3` and `spec-r4` byte-identical to the live
proposal; the newest differing snapshot is `spec-r2-prefix`, and the whole delta since it is ONE hunk
(the `**Slot cleanup:**` leaked-outcome replacement at spec-changes.md:698-710). Start every round
with that command.

### [spec.4.review-client-surface.1]

DECISION: returned an EMPTY findings list for the client-surface lens on the spec staging — BECAUSE every client-facing defect I could substantiate has its remedy in a file this loop may not edit (non-spec-changes.md DOCS-2, or the Go/CRD lane). The spec-internal client surfaces the staging touches (§15.1 `SETUP_COMMAND_FAILED` row, §15.4 published adapter contract, §4.7 RPC tables, §4.7.1 carriage table, §16.1 catalog rows, §12.6 DDL, §6.2 `**Client visibility:**`) all verified clean. ALTERNATIVES: filing the two DEFERRED items below as findings; rejected on the explicit scope rule ("a finding whose only remedy is a code, test, or docs change is out of scope here").

DEFERRED [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md:81`, the `ReportSessionScrub` row, is the reader-facing mirror of the §4.7 row SPEC-3 re-keys, and it is in NO edit list. It reads "Report the per-slot cleanup outcome (`released` or `leaked`) **at each session release, on a pod of any concurrency and any recycle setting**. ... The gateway increments the pod's served-session count and feeds the leak ledger." That is exactly the universal SPEC-3's scrub-model biconditional withdraws (spec-changes.md, SPEC-3 second anchor: the report is filed "when, and only when, that cleanup is one a `Shutdown` performs to reclaim a slot the pod's shared runtime process was given"), and the row also keys the served-session increment on the release where the staged §4.7 row keys it on the report. DOCS-2 as staged covers only the `Shutdown` row (`:75`), the `DemoteSDK` row (`:64`) and one added bind-attempt paragraph (non-spec-changes.md:2453, :2468-2507). What is true instead: the doc row must drop "at each session release, on a pod of any concurrency and any recycle setting" and state the report's condition the way the staged §4.7 row does. WATCHOUT for the fixer: the row must stay ONE physical line and its final sentence must keep the exact words "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." — `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts that opener verbatim on BOTH carriers (tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:57-75). That gate does NOT catch the drift above, so nothing turns red on its own.

DEFERRED [non-spec-changes.md, code lane]: SPEC-4 adds "the phase the pod currently projects" to the projection's input enumeration in §4.6.1 (spec/04_system-components.md:409) and in §6.2's prose (spec/06_warm-pod-model.md:80). The same enumeration is carried by the `Sandbox.status.phase` doc comment at `pkg/apis/lenny/v1alpha1/sandbox_types.go:114` ("a level-triggered projection of per-pod SandboxClaim existence, the claim's binding state, and the pool's sessionPolicy (§4.6.3)") and by its two generated copies, `charts/lenny/crds/lenny.dev_sandboxes.yaml:407` and `pkg/embedded/crds/lenny.dev_sandboxes.yaml:407`. None is in an edit list. WEAKENING EVIDENCE, which is why I did not file it even in the following loop's terms: that comment ALREADY omits "and disposition", which §6.2:80 has today, so it is a loose paraphrase rather than a strict mirror and was incomplete before this proposal. If the next loop takes it, the authoring source is the Go doc comment and the two YAMLs are `make generate` output — editing the YAML by hand is itself a defect (`.claude/rules/code-best-practices.md`, generated-code escape hatch).

FACT: `docs/reference/state-machines.md:138` carries the §6.2 projection sentence with the FULL input list ("claim existence, the claim's binding state and disposition, and `sessionPolicy`") plus all three claim-deletion clauses verbatim. DOCS-1 already names it, including "the same projection input added to that sentence's preamble", so it is covered. EVIDENCE: docs/reference/state-machines.md:138; spec-changes.md tail, DOCS-1 paragraph.

FACT: `docs/api/internal.md` is NOT a mirror of the gateway→adapter contract this proposal changes, and a finding against it would be a dead end. Its `RuntimeAdapter` service block lists `StartSession`, `StopSession`, `Attach`, `Checkpoint`, `UploadFiles`, `DemoteSDK` — three of which (`StopSession`, `UploadFiles`, and the flat-string `StartSessionRequest`) exist nowhere in `schemas/lenny-adapter.proto`, and the page carries no `Shutdown` RPC at all. The divergence is wholesale and pre-existing, so nothing this proposal stages newly falsifies it. Its gRPC-status table (:489-497) also lists neither `ABORTED` nor `INVALID_ARGUMENT`, which rules 1, 2, 5, 8 and 10 answer on; same disposition. EVIDENCE: docs/api/internal.md:74-99, :265-285, :489-497.

FACT: `spec/` carries NO adapter `ErrorCode` catalog, so the two minted codes take no spec catalog row anywhere. §15.4.2 mentions `PROTOCOL_VERSION_INCOMPATIBLE` only inline in the `INIT` row of its state table (spec/15_external-api-surface.md:1699) and there is no table of adapter codes to extend. This confirms the standing entry from the other direction: I looked for the catalog and there is none. EVIDENCE: spec/15_external-api-surface.md:1697-1704; `grep -rn "ERROR_CODE_\|ErrorCode" spec/` returns one hit, spec/15:552, which is the REST/MCP `SessionEventError` payload.

FACT: `schemas/lenny-adapter.proto:528-529` and `:555-556` both assert the adapter `ErrorCode` enum "mirrors spec §15.1" / is "the spec-mandated error code from spec §15.1's catalog". That is ALREADY false at HEAD, because `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27` has no §15.1 row, so SCHEMA-1's two deliberately row-less codes do not newly falsify it. Do not file it as a proto mirror gap; if anyone wants it fixed it is a separate pre-existing finding against the proto comment.

UNVERIFIED: whether the staged §15.4 sentence "…together with the gRPC status, the `ErrorCode` and the `Shutdown` outcome each rule answers on, except in two places…" is read distributively or conjunctively. Read conjunctively it is false: §4.7.1 states an `ErrorCode` for rules 5 and 6 only, while rules 1, 3, 8 and 10 each state a gRPC status with no code, and the two named exceptions are both about the STATUS rather than the code. Read distributively ("whichever of these a rule answers on") it is true. I did not file it because the distributive reading is available and the conformance criterion below it is scoped by "it states", so nothing is left unbounded either way. Someone deciding this should decide it once and not re-open it per round. EVIDENCE: spec-changes.md SPEC-5 §15.4 block, first staged paragraph; §4.7.1 rules 1, 3, 5, 6, 8, 10.

FACT (verified this round, do not re-derive): every verbatim anchor the staging quotes for a client surface resolves word for word — §4.1 third sentence (spec/04:157), §4.7 `Shutdown` row opening (spec/04:686), §4.7 `DemoteSDK` row (spec/04:676), §4.7 `ReportSessionScrub` row (spec/04:692), §15.1 `SETUP_COMMAND_FAILED` three sentences (spec/15:1136), §6.2 `**Client visibility:**` clause (spec/06:290), §4.6.1 projection opener and both claim-deletion bullets (spec/04:409,:413,:414), §6.2 fence `claimed ──→ draining` (spec/06:94-96), §6.2 per-slot fence entries (spec/06:148,:156), §12.6 prose and DDL (spec/12:481,:494). The code citations in the SPEC-3 commentary also resolve: sdkwarm.go:280-282, :296-301; slotsession.go:88, :214-220, :379-382; holdstate.go:190; and the ten-second graceful window the reclaim-hold paragraph attributes to the §10.1 hold-timeout termination is holdstate.go:201 (`context.WithTimeout(..., 10*time.Second)`).

FACT: §6.1 does state what the staged `DemoteSDK` row cites it for — "SDK-warm mode (`preConnect: true`) is admitted only in session mode with `maxConcurrentSessions: 1`". EVIDENCE: spec/06_warm-pod-model.md:69.

### [spec.4.review-docs-alignment.1]

DECISION: returned an empty findings list for the docs-alignment lens in spec round 4 — BECAUSE every docs mirror this proposal falsifies is either already staged (DOCS-1/2/3, CODE-9, SCHEMA-1) or already recorded as DEFERRED to the non-spec loop, and this loop's scope bars a finding whose remedy is a docs edit — ALTERNATIVES: filing the three unstaged `ReportSessionScrub`-universal sites anchored on spec-changes.md:1243-1261's mirror enumeration; rejected because review-log.md:1922 already books two of them as DEFERRED [non-spec-changes.md, a new DOCS-4] and review-log.md:2788 books the third as UNVERIFIED for the non-spec lane, so filing costs two verifiers and closes nothing here.

FACT: the three reader-facing sites that still state the universal SPEC-3 withdraws ("the adapter reports the per-slot cleanup outcome at each session release") are `docs/reference/execution-modes.md:68`, `docs/operator-guide/security-principles.md:33`, and the `ReportSessionScrub` row `docs/reference/adapter-contract.md:81`. `docs/operator-guide/multi-tenancy.md:72` carries the same sentence WITHOUT the reporting clause and stays true, so it needs no edit — EVIDENCE: docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33, docs/reference/adapter-contract.md:81, docs/operator-guide/multi-tenancy.md:72

FACT: no shipped gate turns red on those three. `TestPerSlotCleanupStatedOnEverySessionModeRow` asserts only the substring "Per-slot cleanup" in the residual-state rows, and `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts only the addressing sentence, which SPEC-3 keeps word for word. The drift is therefore silent — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:461-476, tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:64-75

FACT: `docs/api/internal.md`'s adapter-proto section is pre-existing drift, not a site this proposal makes wrong. It publishes `StartSession`/`StopSession`/`UploadFiles`/`DemoteSDK` and carries no `Shutdown`, `PrepareWorkspace` or `ErrorCode` enum at all, so SCHEMA-1's new fields and the two new codes have no mirror there to falsify — EVIDENCE: docs/api/internal.md:80-99, :265-284, :488-499

FACT: `docs/reference/state-machines.md:248` ("Served-session count reaches `recycle.maxSessionsPerPod` on a session release") is NOT falsified by SPEC-3's re-key of `sessions_served`. SPEC-3 moves only the WRITE trigger onto the cleanup-outcome report and states explicitly that the read triggers, including "on a concurrent non-vm-restart pool evaluated on each session release", are unchanged. A future round should not file it — EVIDENCE: docs/reference/state-machines.md:248, spec-changes.md §SPEC-3 spec/12 block ("The read triggers are unchanged and stand as written; only the write trigger moves.")

FACT: `docs/reference/glossary.md:371` ("leaked and failed slots are retained until the pod terminates and still count against pod occupancy") survives the §5.2 disposition table unchanged — the table's `leaked` rows all give "Held for the life of the pod" — EVIDENCE: docs/reference/glossary.md:371

WATCHOUT: the whole docs-alignment lens is nearly empty under this loop's spec-only scope. Its two lens-specific categories (an accepted failure mode with no landing spec text; a new operator-facing failure cause) were both already tried and refuted on materiality in earlier rounds, and the ordinary category (a missing docs edit) is out of scope by construction. A future docs-lens agent in a SPEC loop should expect to return empty and should spend its budget verifying that the staged spec text's own docs citations resolve, rather than hunting docs pages.

USEFUL [review-log.md:1922]: the DEFERRED entry naming execution-modes.md:68 and security-principles.md:33 with the exact repair (delete the trailing reporting clause) saved this round from re-filing a known, already-booked defect.

### [spec.4.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site lens in round 4 — BECAUSE every identifier the staging adds, changes or removes was swept against spec/, docs/, schemas/ and charts/ and every surface that goes wrong is already in an edit list (staged, or recorded as a DEFERRED the docs loop owns) — ALTERNATIVES: filing `docs/reference/adapter-contract.md:81`, the §28.5.3 CH-RUNTIMEOPS card's Preconditions field, and the `lenny_slot_shutdown_untokened_entry_total` naming break; each is rejected below with its reason.

FACT: the full `ReportSessionScrub` sweep in spec/ is exactly five sites and all five are staged. `grep -rn ReportSessionScrub spec/` returns spec/05:453 (Scrub model), spec/05:545 (`**Slot cleanup:**` bullet), spec/12:481 (prose) with :494 (DDL comment), and spec/04:692 (§4.7 Adapter → Gateway row). Nothing else in spec/ keys on the report. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,545; spec/12_storage-architecture.md:481,494; spec/04_system-components.md:692

FACT: spec/28:140's `REG-PODSTATE` register row names `sessions_served` but states NO write trigger ("gateway-written reuse counters the recycle disposition evaluates"), so SPEC-3's §12.6 re-key does not reach it and spec/28 needs no edit for the biconditional. Two rounds could burn on this row. EVIDENCE: spec/28_communication-channels.md:140

FACT: spec/05:488 and the spec/06:139-142 fence entry `claimed ──→ draining (served-session count reaches recycle.maxSessionsPerPod on a session release...)` are READ triggers, not write triggers, and §12.6's own read triggers are deliberately retained by SPEC-3 ("The read triggers are unchanged and stand as written; only the write trigger moves", spec-changes.md:787-789). Do not file either against the biconditional. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488; spec/06_warm-pod-model.md:139-142; spec-changes.md:787-789

FACT: every verbatim "text to replace" block I checked this round still matches the tree byte for byte at the line the staging implies — spec/04:157, :674, :686, :692; spec/05:453, :545 (all three sentences), :561; spec/06:80, :95-97, :148, :152-155, :290; spec/12:481, :494; spec/15:1136 (all three sentences), :1469 insertion point. The anchor sweep recorded in the review log's Settled section is still accurate; I re-ran it rather than trusting it and found no drift.

FACT: §16.9 states a closed canonical scrape target set (gateway, controller, token service, `lenny-ops`) that excludes the adapter process, so SPEC-6's adapter-side counter row needs no §16.9 edit. Four shipped `lenny_adapter_*` rows already carry the same deferral and cite §16.9 explicitly (spec/16:186-189); SPEC-6's row states the deferral without the §16.9 citation, which is a consistency nit rather than a defect and I did not file it. EVIDENCE: spec/16_observability.md:186-189,:737

FACT: the "graceful window of ten seconds" the staged §5.2 reclaim-hold paragraph gives the §10.1 hold-timeout termination is the shipped constant, not an invention: `context.WithTimeout(context.Background(), 10*time.Second)` wraps pass 2. EVIDENCE: pkg/adapter/holdstate.go:201

WATCHOUT: the §15.4 commentary says the third-party-harness absence "is recorded as a §28.4 claim-register row with status `ABSENT`", and §28.4 scopes the register to "Every normative statement **this section** makes" (spec/28:163), which reads as §28-only. Before filing that as a false citation, note the row IS staged, in the non-spec lane under SCHEMA-1, which regenerates `tests/claim-map.json` from `scripts/seed-claim-register.py`. The remedy for any complaint here lands outside the spec staging. EVIDENCE: spec-changes.md:1077-1078; spec/28_communication-channels.md:163; non-spec-changes.md:2151,:2309,:3615

WATCHOUT: the §28.5.3 `CH-RUNTIMEOPS` contract card's **Preconditions** field does state per-frame send preconditions (it states one for `credentials_rotated` at spec/28:1096-1099), so the new co-tenancy condition on the `terminate` frame is a plausible-looking edit site. I did not file it: the card states no universal the condition contradicts, the omission alone does not make the card wrong, and the caller directive prefers a reduction over a second statement of a rule §4.7's `Shutdown` row owns. A future round that wants to file it must show the card asserts something now false, not merely that it is silent. EVIDENCE: spec/28_communication-channels.md:1086-1099

WATCHOUT: `lenny_slot_shutdown_untokened_entry_total` is adapter-emitted but does not take the `lenny_adapter_` prefix every other adapter-process metric in §16.1 carries. §16.1.1 governs LABEL naming only and states no metric-name rule, so this is style and out of bounds for this loop. EVIDENCE: spec/16_observability.md:185-189,:289

USEFUL [review log Standing context, the refuted-family index]: the entries barring `docs/reference/adapter-contract.md:81`, `execution-modes.md:68`, `security-principles.md:33` and `multi-tenancy.md:72` saved me from re-filing the "at each session release" family, and the DEFERRED at review-log.md:2344/:2360 already records the one of those four that DID go false (`:81`) with its remedy in DOCS-2. Read those before touching the report rule.

### [spec.4.review-fresh.1]

FACT: Every "reads, verbatim" fenced anchor in spec-changes.md was machine-checked against spec/*.md
this round and every one HITS byte-for-byte; every replacement block is a MISS (i.e. not yet applied),
which is the expected state. Script: extract fenced blocks, substring-match against each spec file.
Every inline (non-fenced) anchor phrase (§6.2 projection prose, §4.6.1 bullets, §15.1 row sentences)
also matches and is UNIQUE in its file. Every markdown link target in every staged block resolves to a
real file and a real heading anchor. Nobody needs to redo this unless the staging changes.
EVIDENCE: spec/04_system-components.md:157,:409,:674,:686,:692; spec/05_runtime-registry-and-pool-model.md:453,:545,:561;
spec/06_warm-pod-model.md:80,:95,:154,:148; spec/07_session-lifecycle.md:23,:52; spec/12_storage-architecture.md:494;
spec/15_external-api-surface.md:1136,:1469

FACT: Every code citation in spec-changes.md verifies at the cited lines:
pkg/controller/warmpool/occupancy.go:57-59 (the "phase at the claim DELETE" comment), :128-140 (the
no-claim switch), :134-140 (never idle from claimed); pkg/adapter/sdkwarm.go:280-282 (close error
returns before deregistration), :296-301 (conditional release); pkg/adapter/slotsession.go:88
(st.started set before Runtime.Start), :214-220 (releaseSessionSlot discards errors), :379-382
(hold-timeout selects on started); pkg/adapter/holdstate.go:190 (pass 1 deregisters first), :215-254
(terminateHeldSession discards every error); pkg/gateway/podlifecycle/podsession/binder.go:867,:998,
:1072-1082,:1200-1202; schemas/lenny-adapter.proto:1621-1627 (the recycle precedent comment).

FACT: `mid_session` ALREADY EXISTS on `FinalizeWorkspaceRequest` at field 4
(schemas/lenny-adapter.proto:724). The §15.4 commentary sentence "adds ... the mid-session marker to
`PrepareWorkspace`" (spec-changes.md:1068) is therefore CORRECT and does NOT contradict the §4.7.1
carriage table, which marks both `PrepareWorkspace` and `FinalizeWorkspace` as carrying it. Do not
file that as a contradiction; I spent a round on it.
EVIDENCE: schemas/lenny-adapter.proto:713-724; spec-changes.md:981,:1068

FACT: `ShutdownResponse.exited_cleanly` already exists (schemas/lenny-adapter.proto:1666), so the
table's "Clean-exit flag on the `Shutdown` response" column names a shipped field. The shipped
`Shutdown` handler keys `ExitedCleanly` on the runtime close alone and discards `removeSlotTree`'s
error (`_ = removeSlotTree(st)`), so table rows 1-3 describe shipped behaviour exactly.
EVIDENCE: pkg/adapter/session.go:263-291

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts only the ADDRESSING
sentence ("The request is session-scoped: it is ...") in both carriers, not the row's opening clause.
The proposal's claim that the gate constrains the row is true but narrower than its wording suggests;
the SPEC-3 re-key of the row's opening clause does not turn that gate red.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76

DEFERRED [non-spec-changes.md DOCS-2 / docs/reference/adapter-contract.md]: `docs/reference/adapter-contract.md:81`
states of `ReportSessionScrub`: "Report the per-slot cleanup outcome (`released` or `leaked`) at each
session release, on a pod of any concurrency and any recycle setting." That is exactly the universal
SPEC-3 withdraws in the §5.2 `**Scrub model.**` opening sentence and in the §4.7 `ReportSessionScrub`
row. After the spec edits land, that doc sentence is FALSE: the report is filed when and only when the
cleanup is one a `Shutdown` performs to reclaim a slot the pod's shared runtime process was given.
DOCS-2 (non-spec-changes.md:2453-2507) edits only the `Shutdown` row, the `DemoteSDK` row and one
added bind-attempt paragraph, and the edit enumeration in spec-changes.md:1257-1258 names only those
two rows. The `ReportSessionScrub` row of that page is in NO edit list. What is true instead: that row
must be re-keyed onto the cleanup-outcome report (the page keeps the addressing sentence word for word,
because the tier-11 gate above compares it against the spec row). Out of scope for the spec loop, so
it is recorded here rather than filed; the pass between the loops should hand it to DOCS-2.

WATCHOUT: The §5.2 disposition table's second column uses TWO different partitions. The pre-`running`
rows and the outside-a-`Shutdown` rows say "An act fails" (exhaustive); the three runtime-given
`Shutdown` rows split into "Every act returns without error" / "The runtime close fails" / "The runtime
close succeeds and a directory removal fails". §5.2's own action list, as SPEC-3 replaces it, contains
acts that are not directory removals (the process-group kill, the §4.9 timer cancellation, the `slotId`
release), so the third row's label does not close the partition. This is the finding I filed; the
remedy is a reduction in that one cell, not a new row.
EVIDENCE: spec-changes.md:571 (action list), :614-616 (the three rows), :628 (hold ends only when every
act returned without error), :634-636 (the commentary's own "the hold is keyed on every act on every row")

UNVERIFIED: §5.2's "kills any processes owned by the slot's process group" has no implementation in the
adapter's release path — the only `syscall.Kill` on a process group is the setup-command cap at
pkg/adapter/workspace/setup.go:215. Whether that pre-existing spec-vs-tree gap matters to this proposal
was not chased; a code-lane reviewer should decide whether CODE-* owes it a slot-cleanup kill.
EVIDENCE: pkg/adapter/workspace/setup.go:202-215; spec/05_runtime-registry-and-pool-model.md:545

UNVERIFIED: §15.4's bind-token contract block says §4.7.1 states "the gRPC status, the `ErrorCode` and
the `Shutdown` outcome each rule answers on, except in two places", excepting only the STATUS for rule 2.
No site states an `ErrorCode` for the rule-2 reclaim-hold refusal: rule 2 delegates the refusal to §5.2
(which names none) and the status to §15.4 (which names `ABORTED` and no code). I judged this below the
bar rather than refuted; a later lens may disagree.
EVIDENCE: spec-changes.md:1000 (rule 2), :1056 (the "except in two places" sentence), :628 (§5.2 hold), :1062

USEFUL [review-log Standing context, the refuted-family index in Traps]: the four refuted entries in the
prompt saved me from re-filing the §7.1 credential-lease omission and the §15.4 "performs the acts it
states and no others" derivation, both of which I independently reached.

### [spec.4.review-kubernetes.1]

DECISION: returning an EMPTY findings list for the Kubernetes-idiom lens on the spec staging — BECAUSE every K8s-touching surface in the staging checked out against the tree: the §4.6.3 ownership table gives `Sandbox` `status.*` to the WarmPoolController as "Sole writer of phase and conditions" (spec/04_system-components.md:618), so SPEC-4's new parenthetical "the controller's own last level, which it may read back because it is the sole writer of that field" is accurate; no finalizer, no SSA ForceOwnership, no second manager, no admission-webhook change, no CRD-as-message-bus write, and no synchronous bind path made to block on a reconcile — ALTERNATIVES: I considered filing the self-referential-projection hazard (see UNVERIFIED below) and declined it as a pre-existing code race that SPEC-4 documents rather than creates.

FACT: `ProjectOccupancyPhase` already reads the Sandbox's own live `status.phase` (`o.Current`) on the no-claim arm, so SPEC-4's re-keying is spec-catching-up-to-code, not a design change. `state.Reserved`+no claim → `Idle`; `state.Claimed`+no claim → `Draining`; every other phase returns `ok=false` and the projection declines to write. EVIDENCE: pkg/controller/warmpool/occupancy.go:128-144; the function's own ground for it is at :57-63.

FACT: the re-keying is convergent and cannot oscillate on an owned watch. `claimed`+no claim → writes `draining`; the re-triggered reconcile reads `draining`+no claim → `ok=false`, no write. Same for `reserved` → `idle` → `ok=false`. A reviewer worrying about a status-write-triggers-reconcile loop can stop here. EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143.

FACT: the pod really does project `claimed` before the bind's adapter RPCs in the ordinary case, so the §5.2 disposition table's last-row retirement clause has a real ground. `podclaim.Claimer.Claim` writes the `bound` binding status immediately after creating the claim, before any adapter RPC. EVIDENCE: pkg/gateway/podlifecycle/podclaim/claimer.go:148 (`writeBoundStatus`).

UNVERIFIED: the §5.2 disposition-table last row ("A pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection", spec-changes.md:624, grounded at :646-648) is UNCONDITIONAL in the table but CONDITIONAL under SPEC-4's re-keyed projection: it holds only where the WarmPoolController has already written `claimed` at the moment of the claim DELETE. Where it has not — WPC leader-election gap (spec/04 states a 25s crash-case failover window), reconcile lag, or a stale informer read of the Sandbox — the DELETE leaves the pod in a warm-inventory phase and the first clause ("a pod in a warm-inventory phase with no claim projects `idle`") returns it to idle inventory UNSCRUBBED, against §5.2's scrub-before-idle invariant. I did NOT file this: the hole is in the shipped controller (occupancy.go:128-143) with or without SPEC-4, the OLD spec text was simply wrong about the code, and the remedy is a code change this loop may not make. Who should check: the code loop, or a separate finding against §4.6.1. EVIDENCE: pkg/controller/warmpool/occupancy.go:141-143; spec-changes.md:624,:646-648,:829-835.

FACT: the claim-deletion-projection sweep is CLOSED and every live site is either staged or listed. `grep -rn "claim deleted on\|claim DELETE\|deleted on a recycling pod\|non-recycling pod" spec/ docs/ schemas/` returns exactly spec/04:409,415,416 (all SPEC-4), spec/06:80,95 (SPEC-4), spec/06:124 (`reserved ──→ idle`, already keyed on phase and correctly declared untouched) and docs/reference/state-machines.md:138 (DOCS-1, listed at spec-changes.md:1246-1253). Do not re-run it.

FACT (checked, deliberately NOT filed as a missed edit site): two further surfaces enumerate the projection's inputs and are absent from every edit list — `charts/lenny/crds/lenny.dev_sandboxes.yaml:404-411` (generated from the `+kubebuilder` markers, so its authoring source is pkg/apis) and docs/getting-started/architecture.md:237, plus the summary clause in the §4.6.3 ownership table at spec/04:618. None states the claim-deletion keying and each already omits an existing input (the CRD text omits "disposition"; :618 omits `sessionPolicy`), so SPEC-4 leaves them incomplete rather than false. A later lens re-deriving these should reach the same call rather than filing them.

FACT: the `lenny-sandboxclaim-guard` webhook reads no phase and gates `CREATE` only (spec/04_system-components.md:405), so adding `Sandbox.status.phase` to the projection's input set cannot reach it. No admission coherence question exists on this staging.

### [spec.4.review-mechanism.1]

FACT: The only delta between the r2 snapshot and the round-4 proposal is one hunk — the §5.2
`**Slot cleanup:**` leaked-outcome replacement and its commentary. `diff -ru -x '*.review-log*.md'`
over the whole directory returns 25 lines.
EVIDENCE: scratchpad/cp-snap/0081-opt2/spec-r2-prefix vs proposals/0081_*/

FACT: Every "text to replace" anchor I re-checked this round matches the tree byte for byte:
spec/04:157, :674 (`DemoteSDK`), :686 (`Shutdown`), :692 (`ReportSessionScrub`), :415, :416,
:409; spec/05:545 (action list, reporting sentence, leaked-outcome sentence), :561; spec/06:80,
:95-97, :148, :155, :290; spec/15:1136 (three sentences); spec/29:711. The §4.6.1 warm-inventory
qualifier bullet (spec/04:410) really does already carry the qualifier the SPEC-4 commentary
claims. Do not re-run the sweep.

FACT: `pkg/controller/warmpool/occupancy.go` citations in SPEC-4 all resolve: the phase switch is
:128-140, the `state.Claimed` arm :134-140, and the "encodes the recycle-versus-one-session
distinction in the phase the pod sits in at the claim DELETE" comment is at :57-59.
EVIDENCE: pkg/controller/warmpool/occupancy.go:58,:129,:134,:140

FACT: `slotsession.go:214-220` is `releaseSessionSlot`, the shared release routine, and the SDK
demotion reaches it at `sdkwarm.go:298`. So SPEC-3's commentary citing :214-220 for "the SDK
demotion discards the cleanup's errors" is accurate, not a mis-attribution. Someone will be
tempted to file it; do not.
EVIDENCE: pkg/adapter/slotsession.go:214-220; pkg/adapter/sdkwarm.go:294-300

FACT: `FinalizeWorkspaceRequest` ALREADY carries `mid_session` (field 4, schemas/lenny-adapter.proto:724).
That is why SPEC-5's wire summary says the wire edit adds "the mid-session marker to
`PrepareWorkspace`" alone while the §4.7.1 carriage table lists both messages as carrying it.
Not a contradiction.
EVIDENCE: schemas/lenny-adapter.proto:713-724; non-spec-changes.md:2260-2261

FACT: The tier-11 gate `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` pins only
the ADDRESSING sentence ("The request is session-scoped: it is addressed by the identifier of the
released session and names no slot.") on both carriers. SPEC-3 keeps that sentence word for word
on the spec side, so the gate stays green even though the doc row's opening clause is left stating
the withdrawn universal. The gate is not the safety net a reader might assume.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:64-74

FINDING 1 (filed): docs/reference/adapter-contract.md:81 — the `ReportSessionScrub` row still reads
"at each session release, on a pod of any concurrency and any recycle setting", which SPEC-3's
biconditional falsifies, and DOCS-2 edits only the `Shutdown` row, the `DemoteSDK` row and one
added paragraph (non-spec-changes.md:2453, :3782). The spec-changes closing enumeration
(spec-changes.md:1257-1258) omits it too. The earlier round's fix for "docs and proto mirrors
stating the withdrawn universal" landed the proto half (SCHEMA-1's two comment replacements,
non-spec-changes.md:2216-2252) and missed this docs row.

FINDING 2 (filed): the §7.3 re-attach's `Resume` is the FIRST pod-side RPC for the session — the
gateway sends no `PrepareWorkspace`/`FinalizeWorkspace` on that path — so the staged §6.2
`**Pre-\`running\` slot cleanup.**` paragraph, whose stage enumeration names only §4.7.9 step 5 and
"a start still in flight", never places a failed resume in `receiving_uploads`, while §7.1's
obligation explicitly binds the reclaim onto that path and rule 4 says `Resume` creates and stamps.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1591-1620; spec-changes.md:401, :610, :955, :1002

UNVERIFIED: §15.4's bind-attempt-token block says §4.7.1 states "the gRPC status, the `ErrorCode`
and the `Shutdown` outcome each rule answers on, except in two places" (spec-changes.md:1056), but
§4.7.1 names an `ErrorCode` only for rules 5 and 6; rules 1, 3 and 8 name a gRPC status and no
`ErrorCode`. I judged this below the bar because the conformance criterion at :1058 is scoped by
"it states". A later lens may disagree; it is a one-clause fix in the §15.4 block if so.

UNVERIFIED: a claim deleted while the pod projects `sdk_connecting` is covered by neither re-keyed
clause in SPEC-4's §6.2 projection prose (the controller returns `("", false)` there,
occupancy.go:141-143). The pre-edit text did not cover it either, so I did not file it as a
partition break, but the SPEC-4 commentary's "keeps the sentence a partition" claim is stronger
than what the re-keyed clauses deliver.

### [spec.4.review-performance.1]

DECISION: returned an EMPTY findings list for the spec staging — BECAUSE every candidate I
derived this round resolved against the tree as either shipped behaviour the staging now merely
describes, or an already-standing OPEN that prior rounds judged not a finding — ALTERNATIVES: I
worked and dropped five candidates, each listed below so nobody re-derives them.

FACT: the disposition table row "runtime close succeeds, a directory removal fails ->
`released`, clean-exit Set, `leaked` Not entered" is EXACTLY the shipped adapter. The outcome is
derived from `closeErr` alone (`sessionScrubOutcome`, pkg/adapter/sessionscrubreporter.go:39-43)
and the tree removal's error is discarded (`_ = removeSlotTree(st)`, pkg/adapter/session.go:272).
The staged table therefore removes NO whole-pod-replacement backstop that the code ever had; the
shipped §5.2 sentence "If cleanup fails, the slot is leaked" was the over-broad half. Do not file
a disk-leak-unbounded finding on that row. — EVIDENCE: pkg/adapter/sessionscrubreporter.go:39-43,
pkg/adapter/session.go:272

FACT: the SPEC-4 occupancy re-key adds no pod churn and no reconcile loop. `ProjectOccupancyPhase`
already switches on the pod's current phase alone with no claim (`state.Reserved` -> `Idle`,
`state.Claimed` -> `Draining`, everything else `ok=false`), and the function's own comment already
states the rule SPEC-4 adopts. Re-reading the projected phase is fixpoint-stable: the second
reconcile after either write falls to the `default` arm and writes nothing, so the "reads its own
status back" input creates no write amplification onto etcd. — EVIDENCE:
pkg/controller/warmpool/occupancy.go:57-59, :112-141

FACT: the adapter enforces NO cap of its own on slot-registry entries (`maxConcurrentSessions`
appears in pkg/adapter only in two comments, neither a bound). So the last disposition-table row
("pre-`running` slot no cleanup reclaims", entry stands to pod termination) cannot starve the
pod's bind capacity even though the gateway has decremented occupancy: adapter-side entry count
and gateway-side occupancy are allowed to diverge without bound and nothing reads the divergence.
— EVIDENCE: pkg/adapter/sdkwarm.go:294, pkg/adapter/slotsession.go:27

FACT: the staged reclaim-hold sentence "for the §10.1 hold-timeout termination ... a graceful
window of ten seconds" is true of the code, but the code shares ONE 10s context across every
member of the hold-timeout batch rather than arming 10s per member. It is still equivalent,
because a non-last close on a shared runtime returns without touching the child, so only the last
member's close consumes the grace — the code says so in place. Do not file the per-member reading
as a false citation. — EVIDENCE: pkg/adapter/holdstate.go:197-205

FACT: fail-closed leak-on-transport-error is ALREADY the shipped gateway rule for a session
release (`leaked = err != nil || !cleanly`), so the staged "a §7.1 reclaim the adapter does not
answer enters `leaked`" extends an existing mechanism to the bind paths rather than introducing
one. A correlated gateway-to-adapter blip already retired pods before this proposal; the proposal
raises the rate. That is the standing OPEN below, not a new finding. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:533-543

WATCHOUT: `k8s_pod_name` on the two new §16.1 counters is NOT a cardinality defect. It is the
established label on the four neighbouring slot/pod metrics in the same catalog and is registered
in §16's attribute table. — EVIDENCE: spec/16_observability.md:11, :14, :15, :128, :297

OPEN: whether §16's attribute-registry row at spec/16_observability.md:297 ("Session-pool scrub
and retirement metrics, slot failure and replacement metrics, and the session reuse histogram")
needs to name the two new counters is an edit-site question outside this lens. A citation or
docs-consistency lens with the tier-11 metric gate open should decide it; I did not file it.

USEFUL [spec.1.review-performance.1]: the corrected note that the `queue` re-entry fires only on
an exhaustion sentinel saved me re-deriving the livelock question for the reclaim-hold refusal.

UNVERIFIED: after a §10.1.4 coordinator-lost hold-timeout termination whose shared 10s budget
expires mid-batch, every remaining member's identifier is "held for the life of the pod" with no
report and no counter naming it. I could not establish that a §7.3 resume ever returns to that
same pod (the resume flow picks a replacement pod), so I did not file it. Someone who can settle
whether any path re-binds a terminated session onto its original pod should close this.

### [spec.4.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability/fault-tolerance lens on the
spec staging — BECAUSE every recovery-correctness hazard I could construct is either already
recorded in `## Edge cases and accepted failure modes`, already bounded by a shipped
mechanism the Standing context records, or owned by another lens (Kubernetes owns the
level-triggered projection re-key; performance owns the capacity math; security owns
fail-closed on the trust boundary). ALTERNATIVES: I considered filing four candidates and
rejected each on the evidence below.

FACT: the hold-timeout termination's graceful window really is ten seconds, and the staged
§5.2 reclaim-hold sentence naming it is accurate. One `context.WithTimeout(..., 10*time.Second)`
is SHARED by every member of the deregistered set, but the code's own comment explains why
that still bounds each cleanup ("a runtime process serving more than one session returns from
a non-last close without touching the child, so only the last member's close consumes the
grace"). Do not file the shared context as a per-cleanup-window contradiction.
EVIDENCE: pkg/adapter/holdstate.go:197-205.

FACT: the SDK demotion's release IS `releaseSessionSlot`, so the staged citation
`pkg/adapter/slotsession.go:214-220` attributed to "the SDK demotion" is correct by
call-through rather than wrong. `DemoteSDK` calls `s.releaseSessionSlot(sessionID)` after
`noteRuntimeClosed`, and returns an error before touching the registry when its own runtime
close fails. EVIDENCE: pkg/adapter/sdkwarm.go:280-282, :297-299; pkg/adapter/slotsession.go:213-219.

WATCHOUT (candidate rejected, do not re-file without new evidence): "a cleanup that fails
OUTSIDE a `Shutdown` holds the identifier for the life of the pod, reports nothing, enters no
`leaked` state, and therefore has no reclaimer, so the session is pinned to a pod that refuses
it transiently forever." It IS bounded: the refusals are ordinary transient slot failures, the
gateway's WINDOWED `RecordFailure` counter (separate from the persistent leak ledger) reaches
`ceil(maxConcurrentSessions/2)` and drains the pod, and `maxSlotRetries` is a hard-coded 1 so
no single request spins. EVIDENCE: spec/06_warm-pod-model.md:160 (the two-lifetime counting
rule); review-log Standing context entries on `UnhealthyThreshold = (maxConcurrent+1)/2` and
`maxSlotRetries` at start.go:2720.

WATCHOUT (candidate rejected): "the reclaim hold gates ADMISSION only, so a client-streaming
`PrepareWorkspace` already in flight when the hold opens keeps writing into the slot tree the
cleanup is removing, defeating §5.2's fresh-workspace guarantee the hold paragraph invokes."
The only genuinely concurrent in-flight writer on this surface is the §7.4 mid-session upload,
which runs against a STARTED session whose teardown is a session end rather than a bind retry,
so there is no successor at that same slot identifier to bind over the residue. I could not
build a reachable successor, so it fails the materiality half of the confirmation test.
EVIDENCE: spec-changes.md:628 (hold paragraph, "admits no request that would create or resolve
a registry entry"); spec-changes.md:989 (the mid-session form is issued only for a session the
replica holds a live binding for).

FACT: rule 15's clean-exit claim for `absent` and `superseded` is satisfied by CODE-1's gate
arithmetic, so §7.1's "did not complete" predicate and the code agree on every table row.
`ExitedCleanly: closeErr == nil && (live || treeErr == nil)` with `live := removed &&
runtimeHoldsLocked` gives true for both no-removal arms (removed false ⇒ live false, and no
`removeSlotTree` runs, so treeErr nil). I re-derived this row by row against the §5.2
disposition table's `Clean-exit flag` column and every cell matches.
EVIDENCE: spec-changes.md:614-624; review-log Standing context entry "CODE-1's response gate is
`live`, not `started`".

FACT: `CoordinatorFenceRequest` is session-scoped (`SessionId session_id = 1`), which matters
because §4.7.1's Admission preamble extends rule 2's hold to "every other RPC on this
contract". A future lens asking whether a held identifier can starve a §10.1 coordinator
handoff should note the hold only ever covers a session whose entry has already been
deregistered, so the fence it would refuse names a session that is already over.
EVIDENCE: schemas/lenny-adapter.proto:1455-1461; spec-changes.md:995.

USEFUL [Standing context]: the entries on `ReleaseSlotReservation` hard-coding
`recycle=false`, on `SlotClaimer.ReleaseSlot(leaked=true)` early-returning before `DeleteClaim`,
on §4.6.1 orphan GC being a real level-triggered backstop for a `bound` claim, and on the
`queue` re-entry firing only on an exhaustion sentinel, between them closed four of my
candidate "abandoned resource with no reclaimer" and "unbounded retry" lines without any tree
reading. The refuted-family index saved the §7.1-credential-lease line specifically.

### [spec.4.review-security.1]

DECISION: returned an empty findings list for the security lens in round 4 — BECAUSE every candidate I built either matched the shipped tree (so the staged edit corrects the spec toward reality rather than regressing a control) or was already refuted by tree evidence — ALTERNATIVES: I built and then killed four candidates, listed below so nobody rebuilds them.

FACT: the gateway already marks a slot `leaked` and writes the `lenny_adapter_leaked_slots` gauge from its OWN side, independently of any adapter report, on a failed `ReleaseSlotReservation`. So the "leaked count is a pod self-report" security argument is dead: the gateway is already an independent measurer and the proposal's new "an unanswered §7.1 reclaim enters `leaked`" only adds a second gateway-measured source. EVIDENCE: pkg/gateway/sessionserver/start.go:2834-2848 (`leaked := slots.MarkLeaked(...)`, `leakGauge(...)`, `health.RecordLeak(...)`).

FACT: the shipped Shutdown handler discards the directory-removal error and derives the reported outcome from `closeErr` alone, so §5.2 table row 3 (runtime close succeeds, directory removal fails → `released`, clean-exit Set, `leaked` Not entered) records the tree rather than relaxing a control. EVIDENCE: pkg/adapter/session.go:270-279 (`_ = removeSlotTree(st)` then `s.reportSessionScrub(ctx, sessionID, closeErr)`); pkg/adapter/sessionscrubreporter.go:39-44 (`sessionScrubOutcome` branches on `closeErr` only).

FACT: the §10.1 hold-timeout path already declines to file a §5.2 scrub report, in the code's own words, so the staged scrub-model biconditional ("only when a `Shutdown` performs it") documents the tree on that arm too. EVIDENCE: pkg/adapter/holdstate.go:215-220 ("the scrub report is the record of a scrub a Shutdown teardown performed, and this path performs none"); pkg/adapter/slotsession.go:213-218 (`releaseSessionSlot` files nothing).

FACT: `ProjectOccupancyPhase` never returns `Idle` from `Claimed` on any pool, so SPEC-4's deletion of §4.6.1's "the projection returns a pod from `claimed` to `idle` only on a recycling pool" sentence removes a false statement and does NOT weaken the one-session-only / scrub-before-idle invariant. The re-keyed bullet ("deleted while the pod projects `claimed`, on a pool of either recycle setting") is strictly more fail-closed. EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143; spec/04_system-components.md:409 ("the gateway does not write `Sandbox.status`") confirms the controller is the sole writer the §4.6.1 input-enumeration edit relies on.

FACT: the new wire fields fail closed on their zero values. `bind_attempt` empty + `unconditional_teardown` false on a `Shutdown` hits rule 10 → `INVALID_ARGUMENT`, no teardown; the same emptiness on a non-`Shutdown` bind request hits rule 1 → `INVALID_ARGUMENT`. A caller that forgets a field gets a refusal, never a destructive teardown. EVIDENCE: spec-changes.md rules 1 and 10 in the staged §4.7.1 block.

WATCHOUT: `StartSession` and `ConfigureWorkspace` do not CARRY `bind_attempt` at all, and rule 1's first clause is scoped to a request "that carries `bind_attempt`". Do not file "rule 1 rejects every `StartSession`" — the wording already excludes them, deliberately. EVIDENCE: the carriage table and rule 1 in the staged §4.7.1 block.

MISTAKE (mine, caught before filing): I nearly filed "the §5.2 disposition table has no row for a RUNTIME-GIVEN slot no cleanup reclaims (the late-`StartSession` residue)". It does not meet the bar: §7.1 points at the table only for (a) the state a reclaim that did NOT complete leaves and (b) attempts the obligation does not reach. The late-`StartSession` residue is neither — that reclaim completed and answered `reclaimed` — so the table is not claimed to cover it and the Edge-cases bullet owns it. Cost: about twenty minutes. Do not rebuild it.

MISTAKE (mine, caught before filing): I nearly filed that §6.2's `**`leaked` slot semantics.**` paragraph (spec/06_warm-pod-model.md:160) becomes wrong because its "equivalently the leaked portion of the pod's Redis slot-counter occupancy" equates an adapter self-report with a gateway measure that the new unanswered-reclaim source makes diverge. Killed by start.go:2834-2848: the gateway already writes that gauge itself on a path the adapter never reports, so the divergence predates the proposal and the paragraph is no more wrong after the edits than before.

UNVERIFIED: whether the §11.4 revoke fan-out (spec/11_policy-and-controls.md:263, :270) actually sets `unconditional_teardown` on the `Shutdown` it sends. Under rule 10 a revoke that omits it is answered `INVALID_ARGUMENT` and kills nothing, which would silently break a mandatory security control. The spec lane is clean here (§11.4 states no fields and the §4.7 `Shutdown` row is the single home of the two-field rule), so this belongs to the CODE loop that follows. Whoever owns that loop should confirm the revoke fan-out and the Redis pub/sub cross-replica re-issue both set the flag.

### [spec.4.review-single-source.1]

FACT: the only delta between snapshot `spec-r2` and the live proposal is ONE hunk, the §5.2
leaked-outcome replacement and its commentary (spec-changes.md:698-710). `spec-r3` and `spec-r4`
are byte-identical to live including the review log. So round 3 produced no staging change and
"read the changed sections hardest" had a two-paragraph target. EVIDENCE: `diff -rq -x '*review-log*'
scratchpad/cp-snap/0081-opt2/spec-r3 proposals/0081_*/` returns nothing.

FACT: the claim-deletion occupancy projection is stated IN FULL at four carriers, three of them
staged by SPEC-4: spec/04_system-components.md:419-420 (§4.6.1 bullets, which the staging itself
names as the owner), spec/06_warm-pod-model.md:80 (§6.2 prose, which already cites §4.6.1 and then
re-enumerates the whole projection), spec/06_warm-pod-model.md:94-97 (the fence trigger list), and
docs/reference/state-machines.md:138. The staging re-synchronises all four rather than reducing
any. Its own text supplies the drift proof: §4.6.1's closing sentence had already gone false
against `pkg/controller/warmpool/occupancy.go:134-140`. EVIDENCE: spec-changes.md:795-799,:854-858.

WATCHOUT: the same SPEC-4 deliverable REDUCES two §6.2 fence entries to `(see §5.2)` using the
`sdk_connecting ──→ failed  (… see §6.1)` precedent (spec/06:90, spec-changes.md:893-948) while
EXPANDING the `claimed ──→ draining` fence entry with a full trigger plus its rationale
(spec-changes.md:815-821). A lens reading only one half concludes the staging has a consistent
reduction policy; it does not.

FACT: §16.1's house style is a long mechanism description plus a `see §X` pointer inside the
metric-name parenthetical (spec/16_observability.md:7-15). A finding that the staged
`lenny_slot_compensation_superseded_total` row restates rule 15's definition of `superseded`
therefore does not clear the bar; the section's own rows all do that.

FACT: §12.6 states the `sessions_served` write trigger twice, in the prose sentence
(spec/12_storage-architecture.md:481, which DOES carry the `[§4.7]` authority pointer the staging
claims for it) and in the DDL comment (:494), and the staging edits both. I did not file it: the
DDL comment is inside the schema code block, and the two sites were judged one artifact rather
than two prose statements. Next lens: if you disagree, the remedy is the DDL comment citing the
prose, not a third re-key.

DECISION: I did not file the `running`-boundary triple (SPEC-4 fence annotation, SPEC-4 prose
paragraph, §5.2 append clause) BECAUSE the Settled entry at review-log.md:73 records it as an
explicit, closed decision that the four sites agree, and re-opening it needs new evidence rather
than a re-derivation. ALTERNATIVES: filing it as (g); rejected as a barred re-litigation.

DECISION: I did not file §15.4's `Shutdown` is not held" clause (spec-changes.md:1062) against
§5.2's `Shutdown` is the one request outside the hold" (:628) BECAUSE the §15.4 half is four words
plus a cited rule-11 consequence, which reads as a mention rather than a stating site. It is the
closest call I left out.

### [f1.open-decisions-apply.id27]

DECISION: item id:27 (the lost claim-DELETE retirement) stays with the human and its summary entry
27 was rewritten to the narrowed form the phase reached. The question is now half (a) alone,
"Should the lost claim-DELETE retirement be opened as its own finding against §4.6.1?", with the
alternative named in the same sentence (file it, or record it in the defects section). The
gateway-side-versus-controller-side analysis moved out of the question into a new closing
paragraph, `What the finding's owner inherits.`, so the entry no longer defers the side choice in
its recommendation and then reopens it two paragraphs later. Identifier 27 is unchanged. Site:
summary.md, `## Open decisions for human to make`, entry 27.

FACT: the entry's old price for the controller-side fix, "a schema field and a migration", was
false against the tree and is corrected. The durable-marker surface exists end to end: the claim
status phase carries the terminal dispositions and `ProjectOccupancyPhase` already drains on
either (`pkg/controller/warmpool/occupancy.go:99-103`), the writer is
`podclaim.WriteDispositionStatus` (`pkg/gateway/podlifecycle/podclaim/bindingstate.go:228-252`),
and the gateway already calls it for the recycle-boundary retirement
(`pkg/gateway/session/recycle/recycleboundary.go:223-228`). Both the shipped §4.6.1 bullet
(spec/04_system-components.md:416) and SPEC-4's replacement of it (spec-changes.md:884) name the
recorded terminal disposition beside the claim delete as a trigger of the same edge, so the route
adds no schema field and no migration. Its cost is restated as an extra status write on the
release path plus a decision about which dispositions a failed bind may record.

FACT: every other citation in entry 27 re-verified against the tree and stands. `observeClaim`
maps NotFound to hasClaim=false (occupancy.go:217), `claimToSandbox` keys every claim event onto
the one owning Sandbox (occupancy.go:284), `DeleteClaim` is an unconditional delete with no
pre-delete step (claimer.go:322-331), the only claim finalizer in the tree is the test hold
(`gc_reclaim_internal_test.go:69`), and `expiredByUptime` (slotclaimer.go:347, called at :433 and
:491) is the sole used-pod guard on the placement scans.

FACT: the item needed no migration out of a staged change file. `## Open decisions for review`
exists in neither spec-changes.md nor non-spec-changes.md, and neither file references this
decision. The `occupancy.go:128-140` cite at spec-changes.md:804 is SPEC-4 rationale rather than a
pointer to the decision, and it was left alone.

FACT: the `## Stage the fix here` alternative's second clause was reworded from "neither is
derivable from the projection alone" to "nothing staged here reads or writes the claim's release
path", because the first clause had become false once the durable-disposition route was shown to
be derivable from surfaces already in the tree. The alternative still loses for the reason that
survives: both candidate fixes sit in components this proposal does not touch.

### [f1.open-decisions-cleanup]

FACT: the summary file already carried exactly the eight required sections in the required order,
so this pass rewrote nothing. The headings are `# Summary: <title>`, `## Summary`, `## Goals`,
`## Non-goals`, `## Open decisions for human to make`, `## Defects in the shipped tree that this
proposal does not stage`, `## Impacts on other proposals` and `## Deliverable index`, at
summary.md:1, :3, :291, :315, :473, :590, :924 and :939. `## Deliverable index` is last and was
not touched.

FACT: `## Summary` carries no prose of its own and holds the four labelled parts in order:
`**Problem statement.**` (:5), `**What changes.**` (:21), `**Decisions.**` (:103) and
`**Watch out for.**` (:226). The two renames this pass owns had already been made, so no part is
labelled `**Fixed decisions.**` or `**What is fixed.**`.

FACT: no `### Retired` block or equivalent stands inside `## Open decisions for human to make`,
and no meta-list of staged items with a per-item disposition stands anywhere in the file. There
was nothing for this pass to drop.

FACT: the three items this firing left with the human are entries 20, 21 and 27, and all three
stand under `## Open decisions for human to make` with their identifiers verbatim. Entry 20 is at
:481, entry 21 at :488 and entry 27 at :526. Entry 27 carries the narrowed form the apply pass
wrote, recorded in `[f1.open-decisions-apply.id27]` above.

FACT: the section's preamble (:475-479) is true of the entries the section now carries. It states
that entries 21 and 27 carry a recommendation with its ground, its alternatives and a confidence
and that entry 20 carries the question and its ground alone, and each of the three entries matches
that description. This pass moved nothing that could falsify it, so it was left as written.

FACT: each of the nineteen out-of-scope markers this firing reported as `no-edit-needed` resolves
to an entry already standing under `## Defects in the shipped tree that this proposal does not
stage`, between :592 and :922. None was promoted or reworded, and the section holds no decisions.

DECISION: `**Accepted failure modes.**` (summary.md:418) was left where it stands, inside
`## Non-goals`. It is a labelled part rather than a heading, the section list constrains labelled
parts only under `## Summary`, and `## Non-goals` is a listed section whose subject covers a
residue this proposal declines to close. ALTERNATIVES: moving it under `## Defects in the shipped
tree that this proposal does not stage`; rejected because that section is scoped to confirmed
defects in the shipped tree, while these residues are properties of the design staged here, and
four sites in the file cite it by name from where it stands.

DECISION: the reference to proposals 0078 and 0079 inside the shipped-tree defect at
summary.md:714-717 was left in place rather than merged into `## Impacts on other proposals`. It
attributes the two halves of BUILD-GAPS finding F-5.2.33 to those proposals, which is a pointer to
who owns an unstaged fix rather than an assertion about either proposal's continued validity, and
it contradicts neither the 0078 row (:931) nor the 0079 row (:933). Verified against
`BUILD-GAPS.md:4132` and the two proposal files. ALTERNATIVES: merging it into the two rows;
rejected because the defect entry loses the evidence that no fix is staged here.

DECISION: the reference to proposal 0080 in the file-collision item of `**Decisions.**`
(summary.md:222-224) was left in place. It is the ground of a fixed decision about how small this
proposal keeps its adapter edits, and the 0080 row (:928) states the same thing in its own terms,
so the two agree and no second row was added.

### [prune.1.fix.1]

DECISION (prune 1, cross-file edge cases): `non-spec-changes.md`'s `## Edge cases and accepted
failure modes` lost four bullets, "A refused retry burns one attempt", "A compensation lost to a
gateway crash leaves the session unstartable on that pod", "What can meet the reclaim hold, and
what it costs" and "The connect stage compensates nothing". A lead paragraph names the spec-file
case that owns each. The spec file's "A retry that meets the reclaim hold spends an attempt on it"
gained what the folded bullet alone carried: the late upload that reaches no adapter, the hold no
reclaim opened, and the per-caller cost. The two tellings of the refused retry had drifted. The
spec file's is right and was kept: `maxSlotRetries` is 1 and `applySlotRetryPolicy`
(`pkg/gateway/sessionserver/start.go`) returns `SlotFailedError` when `attempt == maxSlotRetries`,
so a refused retry is the request's last attempt and is not "placed again". ALTERNATIVES: keeping
both sections in step by hand; rejected because no spec-lane lens reads the non-spec file, which
is how the drift survived four rounds.

DECISION (prune 2, disposition table): the two "released or cleaned outside a `Shutdown`" row
pairs, whose cells were identical in every column but the key, are merged into two rows keyed on a
slot of either kind, with the performers listed once and the failed-start handler scoped to a
pre-`running` slot, because the start handlers (`StartSession`, `Resume` and the SDK-warm start) call
`releaseSessionSlot` only ahead of `noteRuntimeStarted`. The demotion close-failure row stands. The rationale sentence explaining why
the pre-`running` pair repeated the runtime-given pair is deleted. The partition was re-enumerated
and every reachable combination maps to one row: `Shutdown` on a runtime-given slot (every act
returns; the close fails; the close succeeds and another act fails), `Shutdown` on a pre-`running`
slot (every act returns; an act fails), a release outside a `Shutdown` by the demotion, the
hold-timeout termination or the failed-start handler (every act returns; an act fails after the
deregistration), the demotion whose close fails ahead of the deregistration, and no cleanup. The
last column was NOT moved into prose. It takes four values rather than three, the last row's value
carries a retirement clause SPEC-4's rationale cites as "the last row", and three staged sentences
(§5.2 twice, §7.1 once) say the table states what ends the state left on the pod.

DECISION (prune 3, line citations): every shipped-tree and spec line number in
`spec-changes.md` is deleted and the file and symbol names are kept. No blank was left for which
handlers take the identifier hold: a blank staged there was removed on review, because CODE-6
already fixes the sites and the one helper (`reclaimSlotLocked`), a staged tier-1 test asserts
it, and the staged §5.2 paragraph makes the ordering normative, all of which the blanks
convention bars from delegation. The precedent citation for `PROTOCOL_VERSION_INCOMPATIBLE` named
spec/15 line 1699, which is the `INIT` row of §15.4.2's RPC lifecycle state table, and now names
that row.

DECISION (prune 4, `### Open`): the three hold-predicate-width entries are merged into one OPEN
entry carrying all three provenance pointers. The three "does a `Shutdown` that removes nothing
still run the whole-pod scrub" entries are closed and moved to `### Retired` by one clause staged
in SPEC-1's §4.1 sentence: the scrub runs when the recycle disposition is set on a request that
passed the teardown-pairing rule, whatever outcome that request answers. The clause changes no
behaviour. Rule 10 says a refused request "changes nothing", and CODE-1 states that
`answerShutdown` runs the scrub on every outcome and that the `INVALID_ARGUMENT` return "performs
nothing, the scrub included".

DECISION (summary): entry 27's durable-disposition paragraph claimed the coalescing window cannot
defeat a recorded disposition. `observeClaim` in `pkg/controller/warmpool/occupancy.go` reports a
deleted claim as no claim, and `ProjectOccupancyPhase` reads the binding only under `HasClaim`, so
a status write followed at once by the delete coalesces as the create and the delete do. The
paragraph now says so and names the added cost. The question and the recommendation are unchanged.
The section preamble's "their answers are staged in the change files" is replaced by where the
departed entries went.

WATCHOUT: between the two change files, accepted failure modes of the contract live in
`spec-changes.md`'s `## Edge cases and accepted failure modes` only. The section of the same name
in `non-spec-changes.md` carries cases about code alone, and a new contract-level case added there
is the restatement this prune removed.

WATCHOUT: `spec-changes.md` carries no line-number citation. A fix that adds one reintroduces the
class round 1 confirmed as a false citation.

CORRECTS: the Settled entry "DECISION: the 'what can meet the reclaim hold' finding was closed by
prose at three sites" names the non-spec accepted-failure-mode bullet as one site. That bullet is
folded into the spec file's hold case, and CODE-5's and the summary's sites stand.

CORRECTS: `[f1.open-decisions-cleanup]`'s FACT that the open-decisions preamble "is true of the
entries the section now carries" holds for the carried entries only. Its sentence about the
departed entries was false for entries 12, 13, 14, 15, 23, 24 and 25, which edited no change file,
and for 7 and 26, which moved to the defects section.

### [spec.5.fix-G2.1] (follow-up corrections)

FACT: the post-fix defect "the S1 checklist still asserts the §15.4 reclaim-hold block is not
rewritten" is confirmed. `implementation-checklist.md` step S1 reads "The slot-identifier
reclaim-hold block is not rewritten." The staging rewrote that block's closing sentence to
"`Shutdown` is outside this hold, on the terms [Section 5.2] states, and is answered under the
rules [Section 4.7.1] states for it.", and `summary.md`'s SPEC-5 entry states that §15.4
"re-cuts the slot-identifier reclaim-hold block". The checklist is the stale statement of the
three; the summary and the spec staging agree with each other and need no edit. — EVIDENCE:
`spec-changes.md` SPEC-5 `**Slot-identifier reclaim hold:**` block; `summary.md` deliverable
index, SPEC-5; `implementation-checklist.md` step S1

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: replace S1's
sentence "The slot-identifier reclaim-hold block is not rewritten." with "The slot-identifier
reclaim-hold block keeps its `ABORTED` wire obligation for rule 2, and its closing `Shutdown`
sentence becomes a citation of §5.2 and §4.7.1." The checklist is out of bounds for this fixer
as it was for the previous one, so the correction is recorded a second time rather than made.
An implementer who follows S1 as written leaves the block's old closing sentence in place and
re-lands the duplication the round removed, so the pass between the loops should close this
before sign-off. No deliverable is added, removed, merged, split or resequenced by the
correction.

### [spec.5.fix-G1.1]
DECISION: deleted the sentence "The whole-pod scrub ends state only on a pod that reaches one." from the paragraph after the staged §5.2 disposition table, rather than repairing its antecedent — BECAUSE its only readable antecedent was the whole-pod REPLACEMENT trigger of the preceding sentence, and a pod that reaches that trigger is drained and replaced rather than scrubbed; both propositions the sentence could have carried (when the whole-pod scrub runs, and that it is concurrency-independent) are already stated once by the staged `**Scrub model.**` opening replacement three paragraphs above — ALTERNATIVES: the reviewer's suggested replacement naming the occupancy-zero recycle boundary, a bare back-citation, and an in-place pronoun repair; all three rejected because each writes the scrub trigger a second time inside the same subsection that already owns it, which is the duplicate-home defect the round-4 prune removed.
FACT: the staged §5.2 disposition table's last column reads "The whole-pod scrub OR pod termination ends the slot's directories", a disjunction, so those cells are already true on a one-session pod that never recycles and needed no qualifying sentence at all — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md, SPEC-3 disposition table rows
FACT: the whole-pod replacement trigger and the whole-pod scrub are disjoint mechanisms in shipped spec text. The trigger marks the pod unhealthy, drains it and requests a replacement (spec/05_runtime-registry-and-pool-model.md, §5.2 "Whole-pod replacement trigger"); the scrub runs "whenever occupancy reaches zero on a recycling pod before the pod is reused" (spec/05_runtime-registry-and-pool-model.md, §5.2 "Scrub model."). Any staged sentence that conditions one on the other is wrong.
WATCHOUT: the deletion is load-bearing only for the paragraph's readability; nothing cites it. Verified by grepping the whole proposal directory, spec/, docs/, schemas/ and charts/ for "ends state only", "reaches one" and "scrub ends state": the sole live hit was the deleted sentence itself. A later round that feels the paragraph is now missing a one-session clause should re-read the disposition table's last row, which already names the one-session retirement path through the §6.2 occupancy projection.

### [spec.5.fix-G2.1]

DECISION: the §15.4 `**Slot-identifier reclaim hold:**` block's closing sentence is reduced to a pointer, "`Shutdown` is outside this hold, on the terms [Section 5.2] states, and is answered under the rules [Section 4.7.1] states for it." — BECAUSE the withdrawn clause stated two rules with homes elsewhere, the carve-out (§5.2's reclaim-hold paragraph) and the `absent` outcome (rule 11), so an adapter author could implement both from §15.4 alone, and the block's own lead-in declares only two exceptions to "states no rule of its own" — ALTERNATIVES: deleting the sentence outright (leaves a third-party author no answer in the published contract to whether a reclaim is fenced behind its own hold, which is exactly what CONF-1's hold-against-`Shutdown` case drives); widening the lead-in to declare a third exception (answers a duplication finding with an exception clause and makes a pointer section own a rule); moving the carve-out into §15.4 (inverts the settled citation direction from §5.2); restating the hold case inside rule 11 (moves the duplication rather than removing it, and CONF-1 keys one case per rule on rule 11's current statement).

FACT: the block still publishes one thing of its own, the `ABORTED` status a §5.2-refused request is refused with, and rule 2 takes its status from it. The lead-in's two declared exceptions are rule 2's hold status and rules 11 through 14's outcome status from rule 15, so the lead-in needed no edit. — EVIDENCE: spec-changes.md, SPEC-5 `**Bind attempt token contract:**` and `**Slot-identifier reclaim hold:**` blocks

WATCHOUT: the review-log entry "Do NOT file the §15.4 '`Shutdown` is not held' carve-out against the wide hold predicate" quotes wording that no longer exists in the staging. The guidance it carries is still correct on a correctness lens, and it was deliberately left unedited (the design marked it not-a-site), so a later round should read it as an audit record of a refuted filing rather than as a live quotation. — EVIDENCE: review-log.md `### Refuted` ledger, the "`Shutdown` is not held" entry

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S1 · spec asserts "The slot-identifier reclaim-hold block is not rewritten." That is now false. What is true: S1 keeps the block's `ABORTED` wire obligation for rule 2 and reduces its closing `Shutdown` sentence to a citation of §5.2 and §4.7.1. The design for this group instructed editing that line; the hard constraint puts the checklist out of bounds, so it is recorded here instead. No deliverable was added, removed, merged, split or resequenced.

CORRECTS [the Open item "Does §15.4 state a rule of its own after all?"]: resolved in this round rather than left open. §15.4's only own statement is rule 2's `ABORTED` status, which is one of the two exceptions the block's lead-in already declares, so the commentary claim that §15.4 states no rule of its own now holds.

### [spec.5.fix-G3.1]

DECISION: rule 7 is trimmed to "**The admit rule.** Any other request is admitted." — BECAUSE the stamp-once paragraph already states, for every rule in both cascades, that a request resolving an existing entry writes nothing, and rule 4 owns the only write; rule 7's remaining job is to make the admission cascade total, which the §15.4 conformance criterion rests on — ALTERNATIVES: deleting rule 7 entirely (rejected: the cascade stops being total and the §15.4 criterion loses its ground); moving the immutability proposition out of stamp-once into rule 7 (rejected: stamp-once is cascade-wide while rule 7 reaches only the admitted residue, and the rule-4/stamp-once partition is a settled decision); adding a cross-reference from rule 7 to stamp-once (rejected: added text where a deletion suffices).
DECISION: §7.1's mint-timing and response-independence sentence is cut to "A compensation therefore always names one." — BECAUSE §4.7.1's bind attempt token block is the declared owner of the token's minting and its independence of any response, and the sentence immediately before the cut already links §4.7.1, so §7.1 keeps only the consequence its own obligation needs — ALTERNATIVES: the reviewer's longer form re-linking §4.7.1 one sentence after the same link (rejected as redundant); deleting the sentence outright (rejected: a reader of §7.1 alone could then not tell that a token always exists, which is what makes the "may name no other" clause dischargeable); moving the minting deadline into §7.1 (rejected: it splits the token definition across two sections).
FACT: the same two propositions were written a third time in the Edge-cases bullet "**A bind that fails inside its first entry-creating RPC is fenced.**", and that bullet was swept in the same edit to a citing form. A §7.1 fix landed alone would have left a verbatim copy of the deleted sentence one section earlier. EVIDENCE: 0081_...spec-changes.md, Edge cases and accepted failure modes, that bullet.
WATCHOUT: the SPEC-2 commentary paragraph beginning "The paragraph states the connection preference and no correctness rule about it." still says "A caller holds its own attempt token from before its first pod-side RPC, independently of any connection and of any response". It was left standing because it is proposal rationale explaining why the connection is a cost preference, parallel to the Design section's choice record, rather than staged spec text. A later single-source lens may read it as a fourth stating site; the answer is that it is commentary and its ground is what that site alone knows. EVIDENCE: 0081_...spec-changes.md, the SPEC-2 commentary after the staged §7.1 fenced block.
FACT: nothing in non-spec-changes.md was falsified by either trim. The `ensureSlotStateLocked` default-arm comment, the rule-7 unit and conformance test bullets, and the §7.4 upload row all assert outcomes the rule's home plus the stamp-once paragraph jointly give, and they read the same after the trim. EVIDENCE: 0081_...non-spec-changes.md, the admit-rule sites.
FACT: the summary's deliverable index entry for SPEC-2 survives both trims unchanged; it records that §7.1 carries "the rule that the compensation carries the token its own attempt minted", which the paragraph still states. No deliverable was added, removed, merged, split or resequenced, so no implementation-checklist correction is owed from this group.

### [spec.5.fix-G4.1]

DECISION: closed the duplicate accepted failure mode by deleting the second ordering from the `**A start that races the reclaim.**` bullet and replacing it with a one-clause pointer at the `**An abandoned attempt's start whose claim runs after the reclaim completed re-creates the entry.**` bullet — BECAUSE that dedicated bullet is a strict superset (same antecedent, same untokened-entry mechanism, same conclusion, plus the residue-class-three label, the enumeration of what can remove an untokened entry, the two rejected closures staged at position 2 of the remediation plan, and the tier-1 hold-suite pin), so the reduction loses no proposition and leaves one place a later refinement has to touch — ALTERNATIVES: deleting the dedicated bullet instead (would delete live content that exists nowhere else); keeping both and harmonising their wording (two homes for one case is the defect); merging the dedicated bullet's content up into this one (moves a case the disposition text, the metric rows and the review log reach by its own bold subject).

FACT: the replacement clause deliberately avoids the phrase "the bullet below", which is already in use at the end of the `**A compensation still on the wire…**` bullet for the bullet that immediately follows it. From `**A start that races the reclaim.**` the target is two bullets down, so the pointer names the target by its subject instead. — EVIDENCE: spec-changes.md, the closing sentence of the `**A compensation still on the wire when a retry reaches the same slot is fenced.**` bullet.

WATCHOUT: the `**A start that races the reclaim.**` bullet still enumerates three orderings and one of them now resolves only by pointer. A later lens reading the bullet alone may read the pointer as a missing case and re-expand it. It is not missing; the ordering is owned by the dedicated bullet in the same section. — EVIDENCE: spec-changes.md, the `**A start that races the reclaim.**` bullet's second ordering.

FACT: the clause "the slot never reached `running` and no cleanup outcome is reported" survives untouched in the FIRST ordering (the start-confirmation undo) and was never part of the deleted text. Round 3 adjudicated an equivalent sentence true-as-scoped; do not re-file it. — EVIDENCE: review-log-archive.md, the round-3 adjudication entry.

USEFUL [standing context, "**The refusal a later bind meets at an UNTOKENED entry is the STARTED-SESSION rule, never the attempt identity rule.**"]: confirmed that the compensation bullet and the two start-race bullets concern different entries, so no harmonisation between them was owed and the deletion could be local.

No cascades. No §5.2 disposition row, CONF-1 case or §4.7.1 rule was added, removed or renumbered, so no implementation-checklist correction is owed and no DEFERRED line follows from this group. non-spec-changes.md was checked (its three "recorded among the accepted failure modes" sentences concern the credential double-count, the wait-and-retry residue and the failed-`drain` residue, none of them this ordering) and left alone. summary.md and problem-statement.md carry no statement this deletion falsifies.

### [spec.5.fix-design-G1.1]

DECISION: Finding 4 is fixed by DELETING the sentence "The whole-pod scrub ends state only on a pod that reaches one." from the paragraph closing the staged §5.2 disposition table, leaving the three sentences around it untouched — BECAUSE the sentence carries no proposition the block does not already state in its one home: the staged `**Scrub model.**` opening sentence already states both when the whole-pod scrub runs ("whenever occupancy reaches zero on a recycling pod before the pod is reused") and that the scrub is "uniform across session-mode configurations", and the last column's cells are disjunctions ("The whole-pod scrub **or pod termination** ends the slot's directories"), so they are already true on a one-session pod that never recycles. ALTERNATIVES: (1) the reviewer's suggested replacement ("...so it ends state only on a recycling pod that reaches the occupancy-zero recycle boundary...") — rejected, it writes the scrub trigger a second time in the same subsection, which is exactly the duplicate-home defect round 4's prune was called for; (2) replacement by a bare back-citation to the `**Scrub model.**` sentence three paragraphs above in the same subsection — rejected as noise; (3) repairing the pronoun in place ("a pod that reaches the whole-pod scrub") — rejected, it is still a restatement and still adds nothing to the cells.

WATCHOUT: the tempting local edit here is to correct the pronoun's antecedent rather than ask whether the sentence says anything. Both remaining sentences of that paragraph scope the `leaked` COLUMN to concurrent pods; the deleted one tried to scope the LAST column, which needs no scoping because its cells are disjunctions and the scrub's trigger is already stated once above. EVIDENCE: the staged append and the staged `**Scrub model.**` replacement, both in the SPEC-3 §5.2 block of the spec-changes file.

FACT: the spec's whole-pod REPLACEMENT trigger (spec/05_runtime-registry-and-pool-model.md:561) and the whole-pod SCRUB (spec/05_runtime-registry-and-pool-model.md:453) are disjoint events: the trigger marks a pod unhealthy, drains it and replaces it, while the scrub runs at the occupancy-zero recycle boundary on a pod being reused. Any staged sentence that makes one a condition of the other is wrong on its face. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453 and :561.

### [spec.5.fix-design-G2.1]

DECISION: the §15.4 hold block's closing `Shutdown` sentence (spec-changes.md:1060) becomes a pure pointer — "`Shutdown` is outside this hold, on the terms [Section 5.2] states, and is answered under the rules [Section 4.7.1] states for it." — BECAUSE the clause today states two rules whose homes are elsewhere (§5.2's carve-out at :632, rule 11 at :1011) and sits outside the two exceptions its own lead-in enumerates (:1054). ALTERNATIVES: delete the sentence outright (rejected: §15.4's first sentence cites §5.2 only for "the requests it refuses", and an exclusion is not a refusal, so a third-party adapter author loses the answer to whether a reclaim is fenced behind its own hold, which is exactly the non-conformance CONF-1's Shutdown-cascade case drives); move the carve-out's wire status into §15.4 as a third declared exception (rejected: it adds a rule home to a pointer section, the opposite of the single-source rule); widen the lead-in's exception list to cover the carve-out (rejected: hair — an exception clause answering a duplication finding).

FACT: `non-spec-changes.md:2146` (CONF-1 case "The reclaim hold against the `Shutdown` cascade") attributes the interaction to §15.4's reclaim-hold block "rather than by either rule". The pre-pass that listed potentially related sites judged this bullet unaffected; it is not. Once §15.4 only cites, the stating site for the carve-out is §5.2's reclaim-hold paragraph, so the attribution clause must be re-pointed at §5.2 in the same edit. EVIDENCE: non-spec-changes.md:2144-2148; spec-changes.md:632.

WATCHOUT: two proposal-side records assert the block is untouched and both go stale with this fix — implementation-checklist.md:17 ("The slot-identifier reclaim-hold block is not rewritten.") and summary.md:952 ("carries the slot-identifier reclaim-hold block forward"). EVIDENCE: implementation-checklist.md:17; summary.md:952.

USEFUL [review-log.md:1547]: the round-4 shard recorded that it deliberately did NOT file this clause, calling it "the closest call I left out". That is the audit trail for why the finding only surfaces now; it is not a bar on filing, because that shard weighed the clause against §5.2's carve-out on a correctness lens while this finding is single-source.

WATCHOUT: review-log.md:248 ("Do NOT file the §15.4 '`Shutdown` is not held' carve-out against the wide hold predicate") bars a DIFFERENT filing — that the carve-out contradicts the hold's scope. It does not bar the single-source filing, and it stays valid guidance after the fix. Do not delete it. EVIDENCE: review-log.md:248-251.

DECISION: review-log.md:355's live Open item ("Does §15.4 state a rule of its own after all?") is resolved by this fix rather than carried — after it, the block's only own statement is rule 2's `ABORTED` status, which the lead-in at :1054 declares as one of the two exceptions. EVIDENCE: spec-changes.md:1054.

### [spec.5.fix-design-G3.1]
DECISION: the §4.7.1 `**Bind attempt token.**` block keeps BOTH token propositions (mint-before-first-pod-side-RPC at :970, response-independence at :977, immutability in the stamp-once paragraph at :989) and the two restating sites are cut to citations — rule 7 becomes "7. **The admit rule.** Any other request is admitted." and §7.1's sentence becomes the bare consequence "A compensation therefore always names one." — BECAUSE both fixes are reductions of the non-owning site, which is what the caller directive prefers, and the review-log entry "the stamp contract is PARTITIONED between rule 4 and the stamp-once paragraph" already fixes the home. ALTERNATIVES: moving the immutability clause out of stamp-once into rule 7 (rejected: stamp-once is the cascade-wide statement and rule 4/stamp-once partition is settled); adding a second §4.7.1 link inside §7.1's trimmed sentence as the reviewer suggested (rejected: the immediately preceding §7.1 sentence already carries that exact link, so a second is hair).
FACT: trimming rule 7 does not threaten the §15.4 conformance criterion. The criterion rests on the cascade being total, and "Any other request is admitted." is the totality clause; the deleted clause was about what an admitted request does to the token, which stamp-once states for every rule. EVIDENCE: review-log.md:32; spec-changes.md:989
WATCHOUT: the same two propositions are restated a THIRD time, outside both findings, in the Edge-cases bullet "**A bind that fails inside its first entry-creating RPC is fenced.**" — "The attempt token is minted before the attempt's first pod-side RPC and is held by the gateway rather than latched off a response". Landing the §7.1 fix without sweeping it leaves a verbatim copy of the sentence just deleted. EVIDENCE: 0081_...spec-changes.md:134
FACT: the non-spec-changes sites that name the admit rule are a code comment (:1535) and test-case bullets (:2085, :2652, :2950, :3158). None is a normative restatement and all stay true after the trim; do not churn them. EVIDENCE: 0081_...non-spec-changes.md:1535,2085,2652

### [spec.5.fix-design-G4.1]
DECISION: delete the second ordering (the reclaim-precedes-start residue) from the `**A start that races the reclaim.**` bullet, spec-changes.md:152-157, and replace it with a one-clause pointer naming the dedicated bullet by its bold subject; the `**An abandoned attempt's start whose claim runs after the reclaim completed re-creates the entry.**` bullet (spec-changes.md:176-188) stays the single home — BECAUSE the dedicated bullet is a strict superset (it also carries residue class three, which requests can remove the untokened entry, the two rejected closures staged at position 2, and the tier-1 hold-suite pin), so deleting the shorter copy loses nothing — ALTERNATIVES: delete the dedicated bullet and keep the inline ordering (loses all of the above); rewrite both so they "agree" (two homes for one case, which is the finding).
WATCHOUT: the pointer must NOT say "the bullet below" verbatim. That exact phrase is already used at spec-changes.md:175 by the compensation bullet, where the dedicated bullet is the immediately next one; from the races-the-reclaim bullet it is two bullets down, so name it by its bold subject instead. — EVIDENCE: proposals/0081_.../...spec-changes.md:175
FACT: the deletion does not touch the clause the archive's long-standing trap protects. "the slot never reached `running` and no cleanup outcome is reported" now sits inside the FIRST ordering (the start-confirmation undo, spec-changes.md:147-151), not in the deleted second ordering, so the trap "do not touch the closing sentence" is not engaged by this fix. — EVIDENCE: proposals/0081_.../...spec-changes.md:146-151; review-log-archive.md:50787
FACT: nothing outside spec-changes.md states this ordering. CONF-1 has one case per numbered §4.7.1 rule and this accepted failure mode is not a numbered rule; the §5.2 disposition table and §15.4 state only the general rules it falls under; the two metric rows in non-spec-changes.md cite "SPEC-5 records a residue" without restating it. The fix therefore cascades nowhere. — EVIDENCE: proposals/0081_.../...non-spec-changes.md (CONF-1 case list), ...spec-changes.md (§5.2 table, §15.4 blocks)

### [spec.5.review-fresh.1]

DECISION: returning an EMPTY findings list for the fresh-holistic lens on the round-5 spec staging — BECAUSE I re-derived the whole staging against the tree (every "reads, verbatim" anchor, every insertion point, every code citation surviving the hand prune, the disposition table's partition, the two §4.7.1 cascades, and the §4.1/§4.7/§5.2/§6.2/§7.1/§7.2/§12.6/§15.1/§15.4/§16.1/§29.4 targets) and found nothing that would make the applied spec wrong, inconsistent, or unimplementable — ALTERNATIVES: I considered and declined three, each recorded below.

FACT: every "reads, verbatim" anchor in spec-changes.md still hits byte-for-byte AFTER the hand prune (commit 9c23121af), and every insertion point is still valid. Re-verified independently of spec.4.review-fresh.1: spec/04:157 (§4.1 third sentence), :409 and :411-416 (§4.6.1 projection lead-in and the two claim-deletion bullets), :674 (`DemoteSDK` row), :686 (`Shutdown` row), :692 (`ReportSessionScrub` row), :688/:695 (the §4.7.1 insertion gap between the Adapter→Gateway table and `#### 4.7.2`), :854 (§4.7.9 step 5); spec/05:453, :545, :561; spec/06:80, :95-97, :148, :152-155, :156/:158 (the post-fence insertion gap), :234, :290; spec/07:23-24, :210, :213, :214, :414; spec/12:481, :494; spec/15:1136, :1469/:1471 (the §15.4 insertion gap); spec/29:704-711 (step 13). Nobody needs to redo this unless the staging changes.

FACT: §7.1's flow listing is a FENCED code block, spec/07_session-lifecycle.md:5 to :54, and the shipped atomicity paragraph at :23 already sits inside it carrying markdown links that do not render. SPEC-2's new §7.1 paragraph inherits that, which is the shipped convention rather than a defect. Do not file it.
EVIDENCE: spec/07_session-lifecycle.md:5,:23,:24,:54

FACT: the §4.7 `Shutdown` row's citation of "[Section 15.4.2] graceful-shutdown signal" is sound even though spec/15 never uses that phrase. §15.4.2's `DRAINING` row is "Graceful shutdown requested. The adapter finishes the current exchange and signals the agent to stop", and the shipped adapter names the same thing "the §15.4.2 DRAINING-state graceful-shutdown signal" in `drainViaLifecycle`'s own doc comment. I spent a round on this; do not file it as a false citation.
EVIDENCE: spec/15_external-api-surface.md:1686,:1702; pkg/adapter/session.go:294-299

FACT: the disposition table's partition survives the prune's row merge. The two merged "released outside a `Shutdown`" rows are total because the only act that can fail BEFORE the deregistration on a non-`Shutdown` release is the SDK demotion's runtime close, which has its own row: `Server.DemoteSDK` returns `codes.Internal` before it touches the registry, `releaseSessionSlot` is deregister-then-`removeSlotTree`, and the hold-timeout pass deregisters in pass 1 and closes in pass 2. The row-7 key "An act fails after the deregistration" is doing exactly that work; do not "simplify" it to "An act fails".
EVIDENCE: pkg/adapter/sdkwarm.go:279-300; pkg/adapter/slotsession.go:214-220; pkg/adapter/holdstate.go:189-205,:227-252

FACT: the §5.2 action list's two added acts are both inside `deregisterSlotLocked`/`RemoveTree` and neither can fail observably. `deregisterSlotLocked` cancels every armed per-provider expiry timer under `s.mu` and cannot return an error; `slotlayout` owns `/run/lenny/slots/{sessionId}/credentials.json`. So adding them to the action list changes no completion predicate and no table cell.
EVIDENCE: pkg/adapter/slotsession.go:174-188; pkg/adapter/slotlayout/slotlayout.go:100-103; spec/05_runtime-registry-and-pool-model.md:461,:471

FACT: the §5.2 hold paragraph's "graceful window of ten seconds" for the §10.1 hold-timeout termination is the shipped constant, one shared `context.WithTimeout(context.Background(), 10*time.Second)` across pass 2.
EVIDENCE: pkg/adapter/holdstate.go:200-205

DECISION: declined to file that the §5.2 disposition table has no row for a RUNTIME-GIVEN slot no cleanup reclaims (the late-`StartSession` re-created entry), although row 9 covers the pre-`running` twin and the Traps section says "an uncovered case is ONE TABLE ROW" — BECAUSE the archive records that residue's "lands only in reasoning" variant as filed once and then declined, with "the residue bullets are barred from reopening", and the spec-side disposition of an untokened entry is already carried by §4.7.1 rule 13 plus the unconditional-teardown rule — ALTERNATIVES: filing it as a tenth row (rejected: it re-opens a barred item and adds text where the contract already answers).
EVIDENCE: review-log.md:444 ("two residues living only in reasoning", retired); review-log-archive.md:45937; spec-changes.md:1014 (rule 13's "That is a residue this contract records rather than one it closes.")

DECISION: declined to file the "callers that reach the hold are ..." enumeration in the reclaim-hold edge-case bullet as incomplete, although two sentences later the same bullet adds the client's own §15.1 start retry on a create-time-reserved slot — BECAUSE the section is the proposal's own accepted-failure-modes record rather than staged spec text, so the inconsistency makes neither the applied spec nor the implementation wrong.
EVIDENCE: spec-changes.md:110-129

DECISION: declined to file the §15.1 exclusion sentence's "other than a deterministic `FailedPrecondition`" as leaving a non-deterministic `FailedPrecondition` at the setup-command request unmapped — BECAUSE `writeSetupCommandError` branches on the gRPC code alone and the shipped row already carried "deterministic" in its cause sentence, so the looseness is pre-existing and the replacement is total over failures of that request by construction.
EVIDENCE: spec/15_external-api-surface.md:1136; review-log.md:85 ("`writeSetupCommandError` branches on the gRPC code ALONE")

USEFUL [review-log Standing context, "TABLE FACTS" in the caller directive and the Traps index]: the settled entries on `st.started` preceding `Runtime.Start`, the demotion's early return, and the three deregister-then-destroy sites let me confirm the merged table rows in one pass instead of re-deriving the performer set. The "Retired" subject list is what stopped me filing the missing running-slot row.

### [spec.5.review-mechanism.1]

FACT: every verbatim anchor SPEC-1 through SPEC-6 quotes still matches the tree byte for byte as of
2026-09-20. Re-verified: spec/04_system-components.md:157 (§4.1 third sentence), :674 (`DemoteSDK`
row), :686 (`Shutdown` row opening), :692 (`ReportSessionScrub` row), :409-419 (§4.6.1 projection
bullets, including the false closing sentence SPEC-4 deletes);
spec/05_runtime-registry-and-pool-model.md:453 (`**Scrub model.**`), :545 (`**Slot cleanup:**`
action list, reporting sentence, leaked sentence), :561 (replacement-trigger parenthetical);
spec/06_warm-pod-model.md:80 (projection prose clauses), :95-97 (fence `claimed ──→ draining`),
:149 (`slot_cleanup ──→ leaked`), :156 (`slot_cleanup ──→ released`), :234 (mid-resume cancel
clause), :290 (Client visibility clause); spec/07_session-lifecycle.md:23 (atomicity paragraph,
inside the fence that opens at :5 and closes at :54), :210, :213, :214;
spec/12_storage-architecture.md:481, :494; spec/15_external-api-surface.md:1136 (all four
`SETUP_COMMAND_FAILED` sentences), :1458-1470 (§15.4 insertion point);
spec/29_communication-scenarios.md:704-710 (step 13). Every markdown anchor the staged text uses
resolves to a real heading. Do not re-derive this list; spot-check only what a later edit moves.
EVIDENCE: spec/04_system-components.md:686; spec/05_runtime-registry-and-pool-model.md:545

FACT: SPEC-4's claims about `ProjectOccupancyPhase` are exact. `pkg/controller/warmpool/occupancy.go:128-143`
switches on `o.Current` alone in the no-claim branch (`Reserved`→`Idle`, `Claimed`→`Draining`,
everything else `("", false)`), and the doc comment at :57-63 states verbatim the rule the re-keyed
bullets adopt. `deregisterSlotLocked` (pkg/adapter/slotsession.go:174-186) does cancel every armed
§4.9 expiry timer before `delete(s.slots, …)`, so SPEC-3's added action-list clause records shipped
behaviour. `Server.DemoteSDK` (pkg/adapter/sdkwarm.go:274-303) returns `codes.Internal` at :281
before any deregistration and releases inside the RPC at :296-301, which is exactly what the
`DemoteSDK` row and the table's own demotion row say. `resolveShutdownGrace`
(pkg/adapter/mcpruntime.go:312-324) takes the ctx remaining time first, but the `Shutdown` handler
derives that ctx through `contextWithGraceDeadline(ctx, deadline_ms)` (pkg/adapter/session.go:263,
:327-333), so the staged §5.2 "bounded by the graceful window the reclaiming `Shutdown` carries"
sentence is true as a bound and is NOT a defect. §10.1 really does state the hold-timeout
termination (spec/10_gateway-internals.md:58) and scopes it to "every session the adapter has
started", which agrees with the table's pre-`running` performer row.
EVIDENCE: pkg/controller/warmpool/occupancy.go:128

WATCHOUT: the SPEC-4 §6.2 fence edit is the one staged edit that gives a single code block after
"replace the trigger list of the `claimed ──→ draining` entry:" with no separate verbatim-current
quote. The block IS the replacement (it reads "claim deletion — see §4.6.1", which the current
:95-97 text does not). There are four `claimed ──→ draining` entries in §6.2 (:95, :114, :135,
:137, :139); only :95 is inside the `Occupancy projection` block, so the anchor is unambiguous. I
judged this form rather than correctness and did not file it. A future fixer should not "fix" it by
inventing a different replacement.
EVIDENCE: spec/06_warm-pod-model.md:95

DECISION: I filed exactly two findings and deliberately withheld several near-misses — BECAUSE each
of the withheld ones is either recorded in the log's Open/Traps sections or turns on a pronoun that
reads correctly on a charitable pass. ALTERNATIVES rejected, with why: (a) §6.2's new pre-`running`
paragraph enumerates the §4.7.9 step-5 stages and a start in flight but not a failed `Resume`, so a
§7.3 re-attach's sub-state is unstated — withheld because the sub-state machine is a contract model
nothing drives (log: "Production calls only `MarkLeaked`, `MarkReleased` and `ForgetPod`") and the
remedy adds text; (b) the disposition table's lead-in promises "what the `Shutdown` that performs it
answers" while the column delivers only the clean-exit flag — the outcome is rule 15's, so the
lead-in is loose rather than false; (c) §7.2 step 3 states the connection preference flatly where
§7.1 conditions it on the connection still being open — a summary clause, not a copy; (d) §16.1.1's
`k8s_pod_name` "Used on" column does not mention the two SPEC-6 series — descriptive column, no gate
(`grep -rn "16.1.1" tests/tier11_docs pkg/observability/metrics` finds only the `session_id`
forbidden-label gates).

UNVERIFIED: whether the §4.7 `Shutdown` row's "stated as two teardowns" vocabulary has leaked into
the DOCS-2 `adapter-contract.md` mirror in non-spec-changes.md with the same "which teardown it asks
for" clause. I reviewed spec-changes.md only. Whoever fixes finding 1 should grep the whole
proposal directory for "which teardown it asks for" (two hits in spec-changes.md at :303 and :1009)
before declaring the fix complete; the log's standing trap about a design reversal leaving stale
sentences behind applies.

USEFUL [MISTAKE, twice, the same shape: a design reversal leaves behind every sentence a grep on the
new vocabulary misses]: this is what pointed me at grepping the staged normative blocks for the
claim rather than reading the commentary, which is how finding 1 surfaced — the §4.7 row and the
§4.7.1 cascade heading say one thing and the rules two lines below say the opposite.

### [spec.5.review-single-source.1]

FACT: The staged §4.7.1 admission cascade states the no-re-stamp proposition TWICE, in `**The stamp-once rule.**` and in rule 7's trailing clause. — EVIDENCE: spec-changes.md:989 ("a request that resolves an entry the adapter already holds writes nothing") and spec-changes.md:1003 ("leaves that entry's token as it found it, whether the request carried a token or not"). The logged DECISION "the stamp contract is PARTITIONED between rule 4 and the stamp-once paragraph, and neither proposition has two homes" (review-log.md:34) names only rules 4 and the paragraph and does not cover rule 7.
FACT: The Edge-cases section states one accepted failure mode twice. The second ordering of `**A start that races the reclaim.**` (spec-changes.md:152-157) and the whole of `**An abandoned attempt's start whose claim runs after the reclaim completed re-creates the entry.**` (spec-changes.md:176-180) are the same case with the same conclusion, "the pod is left holding an entry and a runtime session no gateway attempt owns". The second bullet is the richer one (it names what ends the entry and why closing it is out of scope), so it is the home.
FACT: The graceful-shutdown-signal condition has three stating sites after the staged edits: the §4.7 `Shutdown` row (spec-changes.md:303, the home), the §29.4 step-13 sentence's second clause (spec-changes.md:367), and the Edge-cases bullet (spec-changes.md:221-225). The §29.4 commentary at spec-changes.md:370-372 asserts "this step states only whether it occurs", which the staged sentence itself falsifies.
WATCHOUT: §15.4's own lead-in enumerates EXACTLY TWO things §15.4 states beyond the pointer (rule 2's status; rule 15's successful status) — spec-changes.md:1054. The hold block's closing `Shutdown` carve-out (spec-changes.md:1060) is a third, outside that enumeration, and it restates both §5.2's carve-out (spec-changes.md:632) and rule 11's outcome (spec-changes.md:1011 region). The Traps entry "Do NOT file the §15.4 '`Shutdown` is not held' carve-out against the wide hold predicate" (review-log.md:248) bars a CORRECTNESS attack on that sentence; it does not bar the duplication reading, which is what the `### Open` entry "Does §15.4 state a rule of its own after all?" (review-log.md:355) already flags unfiled.
FACT: The minting-timing and response-independence propositions are written out in full at both spec-changes.md:970 (§4.7.1, the declared owner per the Design paragraph at spec-changes.md:45-50) and spec-changes.md:407 (§7.1).
DECISION: I did NOT file the §16.1 `superseded` metric-row gloss as a copy of rule 15 — BECAUSE the Traps entry at review-log.md:205-208 records the four spellings as accepted and moving in one edit, and a metric catalog row has to describe its series — ALTERNATIVES: filing it as a (g) reduction; rejected as likely refuted.
DECISION: I did NOT file the Design section's "identity refusal ... applied before the permanent started-session refusal" (spec-changes.md:46-48) as a copy of rule 5's ordering clause — BECAUSE the ordering IS the design choice and the caller's directive defines Design as a choice record that names the choice.
FACT: The SPEC-4 edit to §6.2's `claimed ──→ draining` fence entry (spec-changes.md, "In the fenced `Occupancy projection` block, replace the trigger list") gives the REPLACEMENT block with no "reads, verbatim" anchor, unlike every other edit in the file. The current text is spec/06_warm-pod-model.md:95-97 ("claim deleted on a pod with recycle.enabled: false"). The anchor is still resolvable because only one `claimed ──→ draining` entry sits in the `Occupancy projection` block (the others are at spec/06:114, 135, 137, 139). Not filed under this lens; a later anchor/citation lens may want it.

### [spec.6.review-mechanism.1]

DECISION: returned no findings for round 6 under the end-to-end-mechanism lens — BECAUSE the round-5→6 diff is six small spec-changes hunks, one non-spec pointer retarget and one summary sentence, every one of them a REDUCTION whose deleted proposition survives at its named home; I traced each deletion to that home and found no orphaned premise — ALTERNATIVES: filing the two weakened inferences (see WATCHOUTs) as mechanism gaps; rejected because the propositions they lean on are stated verbatim in the staged §4.7.1 block and the residual complaint is wording.
FACT: rule 7 was cut to "Any other request is admitted." and its deleted clause (an admitted request leaves the resolved entry's token as it found it) is fully carried by the stamp-once paragraph: "an entry's token does not change while the entry lives, and a request that resolves an entry the adapter already holds writes nothing." Rule 8's "the token the entry carried when the request ... was admitted" is therefore still well-defined — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:999 (rule 7), :986 (stamp-once), :1000 (rule 8)
FACT: §7.1's deleted minting sentence has two homes in the staged §4.7.1 token block: "minted once, before the attempt issues its first pod-side RPC" and "a caller that received no response at all still holds the token it minted". The §7.1 remainder "A compensation therefore always names one." is grounded there rather than locally — EVIDENCE: 0081_....spec-changes.md:966, :968, :403
FACT: the §15.4 hold block's removed sentence ("`Shutdown` is not held ... answers `absent` under the no-entry rule") is covered by §5.2's "`Shutdown` is the one request outside the hold, governed by the rule stated for it rather than by this one" plus §4.7.1 rule 11; no third site relied on §15.4 for that carve-out (grep "outside the hold|not held" returns only :628 and :1056) — EVIDENCE: 0081_....spec-changes.md:628, :1056, :1008
WATCHOUT: the §7.1 sentence now reads "...it may name no other. A compensation therefore always names one." The "therefore" no longer follows from the sentence before it in that paragraph; a future reviewer will read it as a broken inference. It is not a finding (the premise lives in §4.7.1) — EVIDENCE: 0081_....spec-changes.md:403
WATCHOUT: the "A start that races the reclaim" bullet now delegates the answer-before-start ordering to the abandoned-attempt bullet, whose condition is "claim runs after the reclaim completed" rather than "after the reclaim's answer". The narrow window between the answer and cleanup completion is covered by rule 2's hold, so nothing is unstated; do not re-file this as a predicate drift without showing a case the hold does not catch — EVIDENCE: 0081_....spec-changes.md:152-155, :174-180, :628

### [spec.6.review-single-source.1]
DECISION: returned no findings for spec round 6 — BECAUSE every hunk in the r5→r6 diff is a REDUCTION (rule 7's trailing no-overwrite clause deleted, §7.1's minting sentence deleted, §5.2's whole-pod-scrub sentence deleted, the §15.4 hold block's last sentence turned into citations of §5.2 and §4.7.1, two Edge-cases bullets turned into pointers), and for each one I verified the surviving home still states the proposition — ALTERNATIVES: filing the §15.4/§5.2 transient-refusal overlap, rejected because §5.2 states "refuses one as a transient condition" with no status and §15.4 states the `ABORTED` status; that is a partition, not a copy.
FACT: the homes the round-6 reductions now lean on, all verified present — minting timing and response-independence: spec-changes.md:966 and :968; no-overwrite on a resolved entry: the stamp-once rule, spec-changes.md:985 ("a request that resolves an entry the adapter already holds writes nothing"); the late-start untokened-entry case the §150 bullet now points at: the bullet "An abandoned attempt's start whose claim runs after the reclaim completed re-creates the entry", spec-changes.md:~173. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:966,985
FACT: rule 7 is now one sentence, "Any other request is admitted." (spec-changes.md:999). Every other site that names rule 7 reaches it by number/name only (non-spec-changes.md:39, :2085, :3158) except a staged Go comment (non-spec-changes.md:1535), which is code lane. Do not re-add a token clause to rule 7. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:999
WATCHOUT: the S1 checklist line still reads "The slot-identifier reclaim-hold block is not rewritten" (implementation-checklist.md:17) although the §15.4 hold block WAS rewritten this round. The orchestrator declares checklist/summary drift out of scope for this loop, so it is not a finding here; the reconciliation pass between the loops must fix it. — EVIDENCE: proposals/0081_.../0081_....implementation-checklist.md:17

### [spec.7.fix-followup.1]

FIXED: the round-7 SPEC-3 §12.6 replacements remove both substrings the shipped tier-11 gate
`TestPerReleaseSessionCountDrainAgrees_F5231` accepts, and no staged deliverable re-keyed it.
Added a disposition paragraph to the SPEC-3 §12 block naming the gate, its two accepted
substrings, the reason only the prose clause matches today (the DDL comment writes
`non-vm-restart` unbackticked, so it matches neither), and the two replacement substrings the
gate is re-keyed onto in the same step that applies SPEC-3. — EVIDENCE:
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:148 (the test),
:172-176 (the only spec/12 read in the file), spec/12_storage-architecture.md:481 (prose, the
matching site), :494 (DDL, matching neither).
DEFERRED: the gate swap itself is not staged. `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`
is absent from non-spec-changes.md, from its "Files touched on application (non-spec)" list, and
from the SPEC-3 checklist step. This lens may not author a staged test change, so the
reconciliation pass between the loops must add the file with its §12 assertion re-keyed onto the
two substrings the spec staging now names, and attach it to the SPEC-3 step so the spec edit and
the gate land together.

### [spec.7.fix-G1.1]

DECISION: re-keyed §12.6's `sessions_served` READ trigger onto the cleanup-outcome report alongside the write trigger, in both carriers (the prose sentence and the DDL comment), as verbatim phrase replacements inside the existing SPEC-3 §12.6 block — BECAUSE the gateway increments and evaluates in one call, so the two triggers cannot diverge — ALTERNATIVES: deleting §12.6's read clause as a reduction (rejected: it silently retires a shipped tier-11 gate, tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go, which exists to hold §12's evaluation point in agreement with §5.2); weakening the write re-key back to the release (rejected: the staged §5.2 scrub-model paragraph conditions the report, so a release that files none performs no increment); a conditional qualifier "each session release that files a cleanup-outcome report" (rejected: it re-derives §5.2's predicate inside §12.6, a second statement of the rule).

DECISION: used the phrase "on each cleanup-outcome report" rather than the reviewer's "on each such report" — BECAUSE the §12.6 prose sentence names both `ReportSessionScrub` and `ReportPodScrub` before the read clause, so "such report" has an ambiguous antecedent; "cleanup-outcome report" is the term the staged §5.2 paragraph defines and the write clause already uses.

FACT: `ScrubReporter.RecordSessionScrub` performs the increment and the `maxSessionsPerPod` evaluation in one call, and the retirer's counter emit is gated on exact equality with the atomic post-increment value, so a second evaluation at an unchanged count double-counts — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go, `RecordSessionScrub` (`IncrementSessionsServed` then `RetireOnSessionCount`, with the exact-equality note in the comment above the increment).

WATCHOUT: the same claim about §12.6 is stated twice in spec-changes.md, once in the SPEC-3 block's closing commentary and once in the `spec/12_storage-architecture.md` bullet of the "Spec files touched" list far below it. A fixer who edits only the block leaves the list describing the edit falsely — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md, the SPEC-3 §12.6 sub-section and the "Spec files touched" spec/12 bullet.

FACT: other per-release sites survive the re-key untouched because each is phrased conditionally and only a report-producing release can advance the count: §5.2's `Session count limit` bullet ("the session release that drives the served-session count to `maxSessionsPerPod`"), spec/16 §16.1's `session_count_limit` reason row, docs/reference/metrics.md's mirror, docs/reference/state-machines.md's `claimed → draining` per-release row, and §28.3's REG-PODSTATE row (roles only, no trigger).

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md]: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go accepts only the two release-keyed substrings, "on a concurrent non-`vm-restart` pool evaluated on each session release" and "on each session release on a concurrent non-`vm-restart` pool", when asserting spec/12 states the per-release evaluation point. Both are removed by the corrected §12.6 text, so the gate fails once SPEC-3 is applied. What is true instead: the gate must accept the report-keyed forms, "on a concurrent non-`vm-restart` pool evaluated on each cleanup-outcome report" and "on each cleanup-outcome report on a concurrent non-`vm-restart` pool". No staged code deliverable touches that file today; this spec loop may not author one.


### [spec.7.fix-design-G1.1]

DECISION: SPEC-3's §12.6 block re-keys the READ clause together with the write clause in both carriers (prose sentence and DDL comment), using the phrase "on each cleanup-outcome report" rather than the reviewer's "on each such report" — BECAUSE the prose sentence names two reports (`ReportSessionScrub` and `ReportPodScrub`) before the read clause, so "such report" has an ambiguous antecedent, while "cleanup-outcome report" is the term the staged §5.2 scrub-model paragraph defines — ALTERNATIVES: (a) delete §12.6's read clause and leave a bare §5.2 pointer, rejected because a shipped tier-11 gate exists precisely to keep §12 stating the evaluation point in agreement with §5.2 and deleting the clause silently retires that gate; (b) leave the read clause and weaken the write re-key, rejected because the code makes them one event.

FACT: the write and the read of `sessions_served` are one call. `ScrubReporter.RecordSessionScrub` does `IncrementSessionsServed` and then `RetireOnSessionCount` with the exact post-increment value, and that is the only evaluation site of the count against `recycle.maxSessionsPerPod` on a concurrent non-`vm-restart` pool. The retirer's emit is gated on exact equality, so re-evaluating on a later release that files no report would re-fire at the same value — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-482 (comment at :453-455 and :461-464).

CORRECTS [review-log.md FACT: "docs/reference/state-machines.md:248 ... is NOT falsified by SPEC-3's re-key"]: the entry's CONCLUSION stands and its REASON does not. `state-machines.md`'s per-release `claimed → draining` row survives because it is phrased conditionally ("Served-session count reaches `recycle.maxSessionsPerPod` on a session release"), and only a report-producing release can advance the count. It does NOT survive "because the read triggers are unchanged": they are not unchanged, and the same log entry quotes that premise from spec-changes.md. Same conditional survival for the §5.2 `Session count limit` bullet, spec/16 §16.1's `session_count_limit` reason row and docs/reference/metrics.md's mirror of it — none of those is a site of this fix.

WATCHOUT: landing the corrected §12.6 text turns a SHIPPED tier-11 gate red. `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-176` requires spec/12 to contain the literal "on a concurrent non-`vm-restart` pool evaluated on each session release" or the reordered "on each session release on a concurrent non-`vm-restart` pool". The fix removes BOTH accepted substrings — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-176.

DEFERRED [non-spec-changes.md]: no deliverable currently edits `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`, and after the §12.6 read-clause re-key its §12 assertion is false: it accepts only the release-keyed phrasings. What is true instead: the gate must accept the report-keyed phrasing ("on a concurrent non-`vm-restart` pool evaluated on each cleanup-outcome report" and its reordered form). The remedy is a two-substring swap in a code-lane deliverable; the spec loop may not write non-spec-changes.md.

FACT: the "Spec files touched" list at the end of spec-changes.md carries a SECOND full statement of the SPEC-3 §12.6 claim, including the false "the read triggers in the same sentences are unchanged" clause. It is far from the SPEC-3 block and is missed by a reader who fixes only the block — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1234 (search string "the read triggers in the same sentences are unchanged").

### [spec.7.review-applicability.1]

DECISION: return an empty findings list for the applicability-and-sequencing lens at spec round 7 — BECAUSE every staged anchor in `.spec-changes.md` matches the tree verbatim, every created artifact's properties are stated, every anchor resolves uniquely, and no existing gate hard-fails from the spec-only edits — ALTERNATIVES: filing the §6.2 `claimed ──→ draining` block as an unresolvable anchor (rejected: determinable, see WATCHOUT below) and re-filing the §15.4 conformance-criterion over-reach (rejected: already `OPEN, FILED` in the review log).

FACT: every "reads, verbatim" quote in the spec staging was re-verified against the tree this round and all match, each uniquely. Map of anchor → current location, for the next agent who need not re-derive it:
- §4.1 third sentence — spec/04_system-components.md:157
- §4.7 `Shutdown` row — spec/04_system-components.md:686; `DemoteSDK` row — :674; `ReportSessionScrub` row — :692
- §4.6.1 projection opening sentence and the two claim-deletion bullets — spec/04_system-components.md:409, :415, :416
- §4.7.1 insertion point (`*Adapter → Gateway RPCs:*` at :688, `#### 4.7.2` at :695)
- §4.7.9 step 5 — spec/04_system-components.md:854
- §5.2 `**Scrub model.**` — spec/05_runtime-registry-and-pool-model.md:453; the `**Slot cleanup:**` bullet (action list, reporting sentence, leaked-outcome sentence, all on ONE physical line) — :545; `**Whole-pod replacement trigger:**` — :561
- §6.2 fence entries — spec/06_warm-pod-model.md:95-97 (`claimed ──→ draining` in the Occupancy-projection group), :148 (`slot_cleanup ──→ leaked`), :150 (general per-slot header), :152-153 (`receiving_uploads ──→ running`), :155 (`slot_cleanup ──→ released`); projection prose — :80; mid-resume cancel bullet — :234; `**Client visibility:**` — :290
- §7.1 atomicity paragraph — spec/07_session-lifecycle.md:23, with the continuation line `(executionMode, …)` at :24; §7.2 preamble :210, step 2 :213, step 3 :214; §7.3 list tail :414
- §12.6 prose :481, DDL comment :494
- §15.1 `SETUP_COMMAND_FAILED` row (all four replaced sentences) — spec/15_external-api-surface.md:1136; §15.4 insertion point (`**SDK-warm demotion contract:**` :1469, `#### 15.4.1` :1471)
- §29.4 step 13 — spec/29_communication-scenarios.md:704-711 (§29.4 spans :575-762, so the `…§28.5.3).` tail is unique inside the section even though the same tail appears at :645, :887, :982)
EVIDENCE: the file:line pairs above.

FACT: every markdown anchor the staged text mints resolves. `#471-role-and-gateway-rpc-contract` (spec/04:659), `#479-…` (:848), `#461-…` (:338), `#463-…` (:606), `#49-credential-leasing-service` (:1099), `#47-runtime-adapter` (:657), `#154-runtime-adapter-specification` (spec/15:1458), `#1542-rpc-lifecycle-state-machine` (spec/15:1686), `#151-rest-api` (spec/15:614), `#52-…` (spec/05:365), `#62-pod-state-machine` (spec/06:78), `#71-normal-flow`/`#72-interactive-session-model`/`#73-retry-and-resume`/`#74-upload-safety` (spec/07:3/:115/:378/:438), `#101-horizontal-scaling` (spec/10:3), `#161-metrics` (spec/16:3). EVIDENCE: `grep -n "^### \|^#### "` over those files.

FACT: the spec-only edits break no shipped gate. Checked and clear:
- `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-73` keys on edge STRINGS only, never on a fence entry's trigger text, so replacing the two `slot_cleanup` annotations with `(see §5.2)` passes; the new `receiving_uploads ──→ slot_cleanup` edge is not in `generalSlotEdges`, so the scoped-block exclusion loop does not trip on it.
- `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92,:155,:173,:222,:298` — the §5.2 phrases it pins (`counted persistently for as long as the slots remain leaked`, `Session count limit`, `Uptime limit`) all sit outside the replaced clauses, and §12's read-trigger alternative `on a concurrent non-\`vm-restart\` pool evaluated on each session release` survives the DDL replacement verbatim.
- `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:67` pins only `The request is session-scoped: it is addressed by the identifier of the released session and names no slot.`, which the staged §4.7 row keeps word for word on one physical line.
- `pkg/observability/metrics/catalog_test.go:15` (`spec161Metrics`) is a hand-transcribed Go list, NOT read from spec/16, so adding §16.1 rows alone fails nothing; and `tests/tier11_docs/adapter_metric_catalog_test.go:92-117` is driven from the names registered in `pkg/adapter/metrics.go`, so a §16.1 row with no registered metric is inert. Both obligations are code-lane.
EVIDENCE: the test files and lines named.

WATCHOUT: the SPEC-4 §6.2 fence edit is the ONE staged edit that breaks the document's "reads, verbatim: … Replace it with: …" convention — it says "replace the trigger list of the `claimed ──→ draining` entry:" and then gives a SINGLE block, which is the NEW text (it carries "claim deletion — see §4.6.1"). I judged it determinable, because the block's content is the pointer the reduction exists to create, the very next paragraph uses a bare block the same way, and the "Spec files touched" entry confirms the direction. Do not file it; but note that spec/06 carries FIVE `claimed ──→ draining` entries (:95, :114, :135, :137, :139) and only the Occupancy-projection one is in scope, so an implementor who ignores the "In the fenced `Occupancy projection` block" qualifier edits the wrong row. EVIDENCE: spec-changes.md:812-819; spec/06_warm-pod-model.md:93-97.

FACT: the `ProjectOccupancyPhase` doc-comment sentence the SPEC-4 rationale quotes ("the §6.2 state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim DELETE") is verbatim but WRAPS ACROSS SOURCE LINES 58-60, so a one-line grep returns nothing and reads as a false citation. Grep a short fragment. EVIDENCE: pkg/controller/warmpool/occupancy.go:57-60.

FACT: §4.6.1's untouched first bullet, "`idle` for a pod in a warm-inventory phase with no claim." (spec/04:411), is what absorbs §6.2's deleted "a pod with no claim projects `idle`" clause, and its "warm-inventory phase" scope is why it does not collide with the re-keyed "deleted while the pod projects `claimed`" bullet. A reviewer checking SPEC-4 for content loss should stop here rather than re-deriving it. EVIDENCE: spec/04_system-components.md:411 against spec-changes.md:821-841.

FACT: `PrepareWorkspaceRequest` is a FLAT client-streaming message carrying `session_id` on every frame, not a `oneof` stream envelope, so §4.1's envelope-classification paragraph (spec/04:153) and its tier-0 gate (:155) are untouched by SCHEMA-1 adding `bind_attempt`/`mid_session` to it, and rule 9's "first frame that carries one" is consistent with that. Do not file a §4.1 envelope finding. EVIDENCE: schemas/lenny-adapter.proto, `message PrepareWorkspaceRequest` (fields 1-3, `reserved 4`); spec/04_system-components.md:151-155.

USEFUL [the standing-context trap "'Completed', 'the cleanup does not complete' and 'the `leaked` predicate' are THREE different things"]: it is what stopped me filing the apparent contradiction between §7.1's "reclaims that did not complete" (row 3 of the §5.2 table answers clean-exit Set, so the reclaim COMPLETED for §7.1) and §5.2's hold predicate (row 3 holds the identifier for the life of the pod, so the CLEANUP did not complete). The two predicates are deliberately distinct and both are stated; a future lens will hit this shape again.

OPEN: the S1 checklist line still reads "The slot-identifier reclaim-hold block is not rewritten" and still promises §15.4 "state[s] the three non-conformances", both falsified by the current §15.4 staging (spec-changes.md:1050-1056). This loop's scope bars filing checklist drift, and the between-loops reconciliation owns it; recording it so it is not lost.

### [spec.7.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens on spec round 7 — BECAUSE every verbatim anchor quote, every spec-section attribution, and every code attribution in spec-changes.md resolved against the tree — ALTERNATIVES: filing the two marginal items below as findings, rejected as wording/consistency, under the bar.

FACT: all 20+ "reads, verbatim:" anchor quotes in spec-changes.md are byte-exact against the current tree. Checked and confirmed: §4.1 ShutdownRequest 3rd sentence — EVIDENCE: spec/04_system-components.md:157; §4.7 `Shutdown` row — spec/04_system-components.md:686; `DemoteSDK` row — spec/04_system-components.md:674; `ReportSessionScrub` row — spec/04_system-components.md:692; §4.7.9 step 5 — spec/04_system-components.md:854; §4.6.1 two claim-deletion bullets + the false closing sentence — spec/04_system-components.md:416-417; §5.2 Scrub model opening — spec/05_runtime-registry-and-pool-model.md:453; §5.2 Slot cleanup action list / reporting sentence / leaked sentence — spec/05_runtime-registry-and-pool-model.md:545; §5.2 whole-pod replacement parenthetical — spec/05_runtime-registry-and-pool-model.md:561; §12.6 prose + DDL — spec/12_storage-architecture.md:481,494; §6.2 fence entries — spec/06_warm-pod-model.md:95,148,152,155; §6.2 projection prose clauses — spec/06_warm-pod-model.md:80; §6.2 Client visibility clause — spec/06_warm-pod-model.md:290; §7.1 atomicity paragraph (inside the fence, lines 5-54) — spec/07_session-lifecycle.md:23; §7.2 preamble/step 2/step 3 — spec/07_session-lifecycle.md:210,213,214; §7.3 list item 4 — spec/07_session-lifecycle.md:414; §6.2 mid-resume cancel clause — spec/06_warm-pod-model.md:234; §15.1 SETUP_COMMAND_FAILED four sentences — spec/15_external-api-surface.md:1136; §29.4 step 13 tail — spec/29_communication-scenarios.md:704-710.

FACT: every markdown anchor the staged text writes is one already used elsewhere in spec/ (`#471-role-and-gateway-rpc-contract`, `#479-startup-sequence-for-type-agent-runtimes`, `#1542-rpc-lifecycle-state-machine`, `#154-runtime-adapter-specification`, `#461-...`, and the twelve common ones). No dangling anchor. Verified by grepping the anchor strings across spec/.

FACT: the code attributions in the staged rationale all hold. `sw.DemoteSDK(ctx)` returns `codes.Internal` before any deregistration — EVIDENCE: pkg/adapter/sdkwarm.go:280-282, release at :296-301. `slotlayout.RemoveTree` removes `p.CredentialsDir` — pkg/adapter/slotlayout/tree.go:58-68. `deregisterSlotLocked` cancels every armed expiry timer — pkg/adapter/slotsession.go:174-182. `failPhase` is releaseCredentials + drain (bare `DeleteClaim`), no adapter RPC — pkg/gateway/podlifecycle/podsession/binder.go:1072-1082, 1200-1202. `maxSlotRetries = 1` — pkg/gateway/sessionserver/start.go:2720. 10s graceful window in the hold-timeout pass, deregistration in pass 1 — pkg/adapter/holdstate.go:189,199-205. `stageWorkspace` fetches blobs and rewrites archive/gitClone before `PrepareWorkspace` — pkg/gateway/podlifecycle/podsession/binder.go:1283-1330. `ProjectOccupancyPhase` comment quoted by SPEC-4 is verbatim — pkg/controller/warmpool/occupancy.go:57-63. The shipped §4.7 Shutdown order (emitFinalUsage → drainViaLifecycle gated on `!boundRemains` → Runtime.Close) matches the staged row — pkg/adapter/session.go:243-266.

FACT: the tier-11 gate the `ReportSessionScrub` edit is written around reads the row with `lineContaining` and asserts the opener `"The request is session-scoped: it is "` + the addressing rule + `"."` on BOTH the spec row and docs/reference/adapter-contract.md. The staged replacement keeps that sentence word for word and stays one physical line, so the gate holds — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76.

WATCHOUT: the SPEC-4 §6.2 fence edit ("replace the trigger list of the `claimed ──→ draining` entry") gives only the REPLACEMENT block and no "reads, verbatim" original, unlike every other edit in the file. It is still resolvable — the `Occupancy projection` fenced block has exactly one such entry — EVIDENCE: spec/06_warm-pod-model.md:95-97. I did not file it; a later lens may want to, but it is a format inconsistency rather than an unresolvable anchor.

WATCHOUT: `§4.7`'s Gateway→Adapter and Adapter→Gateway RPC tables physically live inside `#### 4.7.1` (spec/04_system-components.md:659-694), while the proposal heads those edits "§ 4.7 (Gateway → Adapter RPC table…)". That matches the tree's own citation convention (§5.2, §12.6 and the tier-11 gate all cite the rows as §4.7), so it is not an attribution error. Do not file it.

FACT: the adapter-side metric row SPEC-6 adds labels the series `k8s_pod_name`, which §16.1.1's single-source attribute table sanctions (spec/16_observability.md:296). The neighbouring `lenny_adapter_leaked_slots` gauge is documented in §6.2 with `pod_id`, a pre-existing inconsistency this proposal does not inherit. Not a finding.

USEFUL [Standing context: "The graceful-shutdown-signal gate is shipped verbatim"]: saved me from filing the §4.7 row's `!boundRemains` condition as new behaviour; the code confirms it at pkg/adapter/session.go:260-262.

### [spec.7.review-client-surface.1]

DECISION: returned an EMPTY findings list for the client-facing-surface lens on the spec staging — BECAUSE every externally-consumed representation the staging touches has a home in the staged edits or in a named non-spec deliverable, and I could not substantiate a gap — ALTERNATIVES: I considered and dropped four candidates, each recorded below so the next lens does not re-spend the same effort.

FACT: `mid_session` already exists on `FinalizeWorkspaceRequest` (field 4) and does NOT exist on `PrepareWorkspaceRequest` (field 4 there is `reserved "slot_id"`). The staged §4.7.1 carriage table listing both as carrying it, together with the spec-changes commentary saying the wire edit adds the marker "to `PrepareWorkspace`" only, is therefore CONSISTENT, not a contradiction. I nearly filed this. EVIDENCE: schemas/lenny-adapter.proto:682-695 (Prepare, no marker), :706-731 (`bool mid_session = 4;` at :724).

FACT: `ShutdownResponse` today is `{bool exited_cleanly = 1; int32 exit_code = 2;}` and `exited_cleanly` has NO definition anywhere in spec/, docs/ or a proto comment. The §5.2 disposition table's "Clean-exit flag on the `Shutdown` response" column is therefore the first statement of its meaning and contradicts no source. Do not file it as a repurposed field. EVIDENCE: schemas/lenny-adapter.proto:1665-1668; `grep -rn "exited_cleanly\|clean-exit" spec/ schemas/ docs/` returns only that one proto line.

FACT: `spec/` carries no adapter `ErrorCode` catalog table and no client SDK enumerates REST error codes, so the two new codes and the new `SlotReclaimOutcome` enum need no parallel spec or SDK row. `SETUP_COMMAND_FAILED` appears in no OpenAPI document either. EVIDENCE: schemas/lenny-adapter.proto:554-588 is the only enum; `grep -rn "SETUP_COMMAND_FAILED" sdks/ pkg/gateway/externalapi/openapi/openapi.json` returns nothing.

FACT: the runtime SDKs (sdks/runtime/{go,python,typescript}) implement the JSONL and CH-RUNTIMEOPS side only; they carry no adapter-gRPC types. The staging changes no JSONL or `runtime-ops-events` frame, so no SDK language mirror is owed. EVIDENCE: sdks/runtime/go/runtime/types.go, sdks/runtime/typescript/src/types.ts (lifecycle/JSONL types only); schemas/runtime-ops-events.schema.json:174-185 (`terminate` unchanged).

FACT: the CH-RUNTIMEOPS `terminate` frame carries exactly `type`, `deadlineMs`, `reason` and no session identifier, which is what makes the staged §4.7 `Shutdown` row's "the signal is pod-global and names no session" true. EVIDENCE: schemas/runtime-ops-events.schema.json:174-185.

FACT: §28 needs no edit. §28.4's claim-register rows live in `tests/claim-map.json`, not in spec/28, so the staged §15.4 commentary's "§28.4 claim-register row with status `ABSENT`" is discharged by SCHEMA-1 and does not contradict the "§28's registers" entry in `Spec sections deliberately untouched`. The §28.5.1 gateway-to-pod cards are CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER, CH-PODHEALTH — none covers the session-assignment RPCs, so new fields on them add no card content. EVIDENCE: spec/28_communication-channels.md:161-166, :205-320.

FACT: the tier-11 gate asserts both carriers contain the literal `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` — the staged §4.7 `ReportSessionScrub` replacement preserves it word for word, so the gate holds. EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,:64-75; spec-changes.md:744.

USEFUL [the review log's §15.4.2 entry]: the standing-context entry "the §4.7 `Shutdown` row's citation of [Section 15.4.2] graceful-shutdown signal is sound ... I spent a round on this; do not file it as a false citation" (review-log.md:1825) stopped me filing exactly that, after I had already verified §15.4.2's `DRAINING` row and the §29.4 step-13 §15.4.3/§28.5.3 citations. It saved a full finding cycle.

WATCHOUT: two loose glosses in the staged §4.7 `Shutdown` row and §15.4 block read like defects and are NOT ones under the bar, so do not spend a finding on either. (1) The row's "the response reports which entry the request was addressed to and what became of it" — the wire outcome is a three-valued enum that names no entry, but the next sentence delegates the meanings to §4.7.1 rule 15, so this is wording. (2) The §15.4 block's "§4.7.1 states the gRPC status, the `ErrorCode` and the `Shutdown` outcome each rule answers on, except in two places" — rules 1, 3, 8 and 10 state a status and no `ErrorCode`, so the "two places" is not exhaustive, but the conformance sentence immediately below scopes the obligation with "it states", which makes the contract complete. EVIDENCE: spec-changes.md:299, :1050-1052.

### [spec.7.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the docs-alignment lens in the spec lane — BECAUSE every
docs-side correction this lens can derive on the current staging is already captured as a DEFERRED entry
in the review log (execution-modes.md + security-principles.md "and the adapter reports its outcome to
the gateway"; troubleshooting.md `setup_command_failed`; gateway-replica-failure.md's benign-crash
narrative), and each of those has its remedy in a docs file this loop may not edit —
ALTERNATIVES: filing the two deferred residues (self-recreated entry, gateway-crash-stranded entry) as
"accepted failure mode absent from landing spec text" — rejected, the review log records that an earlier
round already filed them and the applied remedy was ONE SENTENCE EACH in spec-changes.md's own Edge-cases
section, not staged spec text.

FACT: the spec-changes.md closing docs-mirror paragraph (spec-changes.md:1258-1278) checks out verbatim
against the tree on every page it names — EVIDENCE: docs/reference/state-machines.md:237 (`slot_cleanup`
→ `released` row), :251 (`slot_cleanup -> leaked` clause), :138 (pod-state-machine projection prose with
the two claim-deletion clauses and the input enumeration); docs/reference/adapter-contract.md:64
(`DemoteSDK` row), :75 (`Shutdown` row), :81 (`ReportSessionScrub` row);
docs/reference/error-catalog.md:129 (`SETUP_COMMAND_FAILED` row with a remedy cell). No page named there
is misdescribed.

FACT: the proposal's claim about `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(spec-changes.md:747-751) is accurate. The gate reads the spec row via `lineContaining` on
"| `ReportSessionScrub` |" and asserts BOTH carriers contain the literal
"The request is session-scoped: it is addressed by the identifier of the released session and names no
slot." — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,
:46-49, :69-77. So SPEC-3's row replacement is safe only while that sentence stays byte-identical on one
physical line in both spec/04 §4.7 and docs/reference/adapter-contract.md.

FACT: `docs/operator-guide/multi-tenancy.md:72` states the per-slot cleanup "runs at each session release
in session mode, on a pod of any concurrency and any recycle setting" with NO reporting clause, so
SPEC-3's withdrawal of the reporting universal does not falsify it. Only execution-modes.md:68 and
security-principles.md:33 carry the "and the adapter reports its outcome to the gateway" tail. The
existing DEFERRED entry naming exactly those two pages is correct and complete; do not widen it to
multi-tenancy.md — EVIDENCE: docs/operator-guide/multi-tenancy.md:72,
docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33.

FACT: the §12.6 `sessions_served` re-key does not break the tier-11 gate that reads it. That gate pins
the READ trigger ("on a concurrent non-`vm-restart` pool evaluated on each session release"), which
SPEC-3 leaves untouched by design — EVIDENCE:
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-176; spec-changes.md:785-789.
A future round should not file "SPEC-3 falsifies §5.2's Session count limit bullet" either: a release
that files no report simply never drives the count to the limit, so the bullet's condition is unmet
rather than false — EVIDENCE: concurrent_slot_lifecycle_doc_reconciliation_test.go:154-159.

FACT: the adapter-metric documentation gate is two-sided and both sides are staged. A metric registered
in `pkg/adapter/metrics.go` must appear in docs/reference/metrics.md AND in the §16.1 catalog, or carry a
`specCatalogPending` entry naming the staging proposal; `specCatalogPending` is currently EMPTY, so
`lenny_slot_shutdown_untokened_entry_total` needs SPEC-6's §16.1 row and CODE-9's metrics.md row to land
together with the registration — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:48,
:93-113. The staged §16.1 rows also match the shipped two-column row format at
spec/16_observability.md:14-15.

UNVERIFIED: whether `docs/reference/metrics.md` has a section the two new counters land in without a
heading addition. I confirmed only that CODE-9 is named as the deliverable; the non-spec loop should
check the landing section.

### [spec.7.review-edit-sites.1]

DECISION: returned an EMPTY findings list for the edit-site lens on spec round 7 — BECAUSE every identifier the staging adds or changes was grepped across `spec/`, `docs/`, `schemas/`, `charts/` and each surface that goes stale is either in an edit list or already carried as an OPEN/DEFERRED entry in the review log — ALTERNATIVES: filing the §15.4 "the `ErrorCode` ... each rule answers on" over-reach (rejected: already recorded as `### Open`, "Does the staged §15.4 conformance criterion over-reach?", FILED, and it is the contract lens's, not this one's); filing `docs/reference/execution-modes.md:68` / `docs/operator-guide/security-principles.md:33` (rejected: already an OPEN and a DEFERRED entry, and the remedy is a docs deliverable this loop may not edit).

FACT: the new identifiers collide with nothing in the tree. `grep -rn "bind_attempt\|unconditional_teardown\|mid_session\|SLOT_BIND_\|slot_reclaim\|SlotReclaimOutcome\|lenny_slot_compensation_superseded_total\|lenny_slot_shutdown_untokened_entry_total" spec/ docs/ schemas/ charts/` returns exactly two hits, both the shipped `mid_session` on `FinalizeWorkspaceRequest` — EVIDENCE: schemas/lenny-adapter.proto:713,:724

FACT: the §4.7 Gateway→Adapter and Adapter→Gateway RPC tables physically live inside `#### 4.7.1 Role and Gateway RPC Contract` (heading at spec/04:659, tables at :665-:693, next heading `#### 4.7.2` at :695), not directly under `### 4.7` (:657). The proposal's habit of calling them "the §4.7 `Shutdown` row" and citing `[Section 4.7](#47-runtime-adapter)` follows the shipped spec's own convention (spec/05:453 cites §4.7 for `ReportSessionScrub`), so it is not a mis-attribution — EVIDENCE: spec/04_system-components.md:657,659,686,692,695

FACT: every "reads, verbatim" anchor in the spec staging checks out word for word. Verified: §4.1 third sentence (spec/04:157), §4.7 `Shutdown` row opening (:686), `DemoteSDK` row (:673 area), `ReportSessionScrub` row (:692), §4.7.9 step 5 (:854), §4.6.1's two claim-deletion bullets and the deleted closing sentence (:418-419), §5.2 action list / scrub-model opening / reporting sentence / leaked-outcome sentence (spec/05:453,:545), §12.6 prose and DDL (spec/12:481,:494), §6.2 projection prose clauses (spec/06:80), the `Occupancy projection` fence `claimed ──→ draining` entry (:94-96), the per-slot fence `slot_cleanup ──→ released` (:154) and `slot_cleanup ──→ leaked` (:148), the `**Client visibility:**` clause (:290), the `resuming → cancelled` clause (:234), §7.1 atomicity paragraph end (spec/07:23), §7.2 preamble / step 2 / step 3 (:210,:213,:214), §7.3 list item 4 (:414), §15.1 `SETUP_COMMAND_FAILED`'s four sentences (spec/15:1136), §29.4 step 13's closing citation (spec/29:711)

FACT: the `claimed ──→ draining` entry the SPEC-4 fence edit targets is unique inside the `Occupancy projection` group. spec/06's §6.2 fence has three further `claimed ──→ draining` entries, two in the `Concurrent occupancy` group and one in `Recycle edges`, so an implementor who searches the whole fence rather than the named group will hit the wrong line — EVIDENCE: spec/06_warm-pod-model.md:94, :115, :134, :138

FACT: `spec/16` §16.1 is one flat two-column table with no per-emitter sub-tables, so SPEC-6's "beside the session-slot failure count" placement of an adapter-side row is well-formed; adapter-side rows already sit in the same table — EVIDENCE: spec/16_observability.md:5, :14-15, :185-189

FACT: the `§10.1 hold-timeout termination` the §5.2 table and hold paragraph cite is real: §10.1.4's "Hold state timeout" bullet states the adapter "initiates graceful session termination" after `coordinatorHoldTimeoutSeconds` and, on a multi-session pod, "terminates every session the adapter has started on that pod" — EVIDENCE: spec/10_gateway-internals.md:53, :57

FACT: the staged §4.7 row's "[Section 15.4.2] graceful-shutdown signal" is not a coined citation. The shipped adapter carries the same one: `drainViaLifecycle` is documented as sending "the §15.4.2 DRAINING-state graceful-shutdown" signal and `drainReason` carries `// spec: §15.4.2` — EVIDENCE: pkg/adapter/session.go:294, :313. §15.4.2 itself names no "graceful-shutdown signal"; its `DRAINING` row says "signals the agent to stop" (spec/15:1702). Do not file this as a false citation; the tree established the convention.

FACT: the tier-11 gate the §4.7 `ReportSessionScrub` commentary invokes asserts only that both carriers contain `"The request is session-scoped: it is "` + the shared addressing rule on one physical line, which the staged row keeps verbatim. It does NOT compare the rows' opening clauses, so the staged spec opening and the DOCS-2 opening may differ — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-75

FACT: `spec/05:488`'s session-count-limit sentence ("the pod transitions to `draining` on the session release that drives the served-session count to `maxSessionsPerPod`") and §12.6's read-trigger clauses survive SPEC-3's report re-key untouched: they are READ triggers on `sessions_served`, and SPEC-3 moves only the WRITE trigger, which the staging says in as many words. Do not file them as stale — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488; spec-changes.md §12.6 block, "The read triggers are unchanged and stand as written"

FACT: `spec/15:625` and `:720` ("a deterministic non-zero setup command surfaces as the non-retryable `SETUP_COMMAND_FAILED`") are sufficient-condition sentences, not exclusivity claims, so SPEC-5's second cause for that row does not falsify them and they owe no edit — EVIDENCE: spec/15_external-api-surface.md:625,:720

FACT: `docs/reference/adapter-contract.md` carries no `ErrorCode` table at all (`grep -n "ErrorCode\|SLOT_\|CATEGORY_"` returns nothing), so the two new adapter codes add no row there, and DOCS-2's four named row edits are the whole surface. `spec/28` mentions `Shutdown` nowhere, confirming the staging's "§28's registers" untouched claim — EVIDENCE: docs/reference/adapter-contract.md (no match); spec/28_communication-channels.md (no match)

WATCHOUT: the SPEC-4 `Occupancy projection` fence edit gives only the REPLACEMENT block, with no "reads, verbatim" original beside it, unlike every other anchor in the staging. The original is spec/06:94-96 and differs in one clause ("claim deleted on a pod with recycle.enabled: false" → "claim deletion — see §4.6.1"). An implementor applying the staging mechanically has nothing to match on — EVIDENCE: spec/06_warm-pod-model.md:94-96. I did not file it: the target is uniquely identified by group plus edge name, so the edit is applicable, and over-specifying is its own defect.

### [spec.7.review-fresh.1]

FACT: Every "reads, verbatim" anchor in spec-changes.md was checked with `grep -rnF` against spec/ this round and ALL match byte-for-byte: §4.1:157, §4.7 `Shutdown` row 04:686, `DemoteSDK` 04:674, `ReportSessionScrub` 04:692, §4.7.9 step 5 04:854, §5.2 453/545/561, §12.6 481/494, §6.2 80/95/148/152/155, §4.6.1 409/415, §7.1 23, §7.2 210/213/214, §7.3 414, §6.2 234/290, §15.1 1136, §29.4 step 13 ends at 29:711. A future round does not need to re-run this sweep unless the staging changes an anchor. — EVIDENCE: spec/04_system-components.md:157,674,686,692,854; spec/05_runtime-registry-and-pool-model.md:453,545,561; spec/06_warm-pod-model.md:80,95,148,152,155,234,290; spec/07_session-lifecycle.md:23,210,213,214,414; spec/12_storage-architecture.md:481,494; spec/15_external-api-surface.md:1136; spec/29_communication-scenarios.md:711

FACT: The §4.6.1 `draining, then terminated` bullet's closing sentence is quoted in the proposal as "The one-session-only invariant of §6.2 is ..." but the tree spells §6.2 as the full markdown link `[Section 6.2](06_warm-pod-model.md#62-pod-state-machine)`. That quote is descriptive (not inside a "verbatim" fence) and the whole bullet is replaced, so it is NOT a finding. — EVIDENCE: spec/04_system-components.md:420

FACT: The §6.2 `claimed ──→ draining` edit gives only ONE code block, the REPLACEMENT, with no "reads, verbatim" block for the current text. The anchor is still unambiguous (the Occupancy-projection fence has exactly one such entry; the Recycle-edges and Concurrent-occupancy groups each have their own). Not a finding, but it is the one edit in the file that departs from the before/after form. — EVIDENCE: spec/06_warm-pod-model.md:95-98

MISTAKE nearly filed (do not re-derive): "§7.2's pre-attached visibility paragraph restates the SETUP_COMMAND_FAILED mapping and SPEC-5's 'single home' claim is false." Refuted by reading the text: spec/07:208 states only the cause→code direction ("a deterministic non-zero setup-command exit ... surfaces as ... SETUP_COMMAND_FAILED") plus a clause scoped to transient transport failures. It carries NO "any other setup-window failure stays retryable" universal, which is the half SPEC-5 falsifies, so it is not made wrong. Same for spec/15:625, 15:647, 15:720, which also carry only the cause→code half. §6.2:290 is the only site carrying the falsified universal and the proposal edits it. — EVIDENCE: spec/07_session-lifecycle.md:208; spec/15_external-api-surface.md:625,647,720; spec/06_warm-pod-model.md:290

MISTAKE nearly filed: "the §5.2 disposition table's last row cites §6.2 for the retirement, but SPEC-4 moves the claim-deletion rule to §4.6.1." Refuted: after SPEC-4 the §6.2 fence entry still lists "claim deletion" as a trigger of `claimed ──→ draining` (with a `see §4.6.1` pointer), so §6.2 does state the edge the cell relies on. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:625 (last table row), :53-60 (the fence replacement)

FACT: `RecordSessionScrub` increments `sessions_served` AND evaluates it against `maxSessionsPerPod` in the same call (`IncrementSessionsServed` then `RetireOnSessionCount`), and the retirer gates on the exact post-increment value, so the write trigger and the read trigger are ONE event in code. This is what makes §12.6's "read on each session release" clause move with the write re-key. — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-482

UNVERIFIED: §15.4's bind-attempt-token block asserts §4.7.1 states "the gRPC status, the `ErrorCode` and the `Shutdown` outcome each rule answers on", but only rules 5 and 6 state an `ErrorCode`; rules 1, 3 and 8 state a bare gRPC status. I read this as distributive ("whichever of those the rule answers on") and did not file it, and the standing context records §15.4 completeness findings as refuted six times. A later round that wants to tighten it should tighten §15.4's wording, not add ErrorCodes to rules. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1050 (§15.4 block), :994,:996,:1000 (rules 2, 3, 8)

USEFUL [Standing context, "The metric-gate topology" and "Do NOT re-derive the capacity family"]: saved re-deriving the §16.1 adapter-row question and the hold-availability family. §16.1 already carries adapter-side rows at spec/16_observability.md:185-189, so SPEC-6's adapter row has precedent in the section.

### [spec.7.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on spec round 7 — BECAUSE the staged spec edits touch no CRD status write, no finalizer, no admission webhook, no field-manager ownership and no reconcile loop except SPEC-4's occupancy-projection re-key, and that re-key matches both the controller and the §4.6.3 ownership table — ALTERNATIVES: filing the read-back-own-status concern (see UNVERIFIED below), rejected as a pre-existing shipped property the proposal cites rather than creates.

FACT: the whole staged spec file contains exactly ONE occurrence of any of {finalizer, etcd, reconcil, webhook, CRD, status subresource, field manager, ForceOwnership, leader} — `grep -n` over 0081_...spec-changes.md returns only line 666 ("the CRD validation rule all stand as written"). A future Kubernetes-idiom pass can start from that grep and will be done in minutes. — EVIDENCE: proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.spec-changes.md:666

FACT: SPEC-4's new §4.6.1 projection input, "the phase the pod currently projects (the controller's own last level, which it may read back because it is the sole writer of that field)", is true on both halves. `ProjectOccupancyPhase` switches on `o.Current` (the Sandbox's live `status.phase`) for the no-claim case, and every `Sandbox` status writer in the tree applies under `FieldOwner(ownership.WarmPoolController)`. — EVIDENCE: pkg/controller/warmpool/occupancy.go:33-35 (`Current is the Sandbox's live status.phase`), :126-140 (the `Reserved`/`Claimed` switch), pkg/controller/warmpool/occupancy.go:268, controller.go:752,:852, pod_reconciler.go:691, gc.go:466, sandbox/controller.go:820 (all `FieldOwner(WarmPoolController)`), spec/04_system-components.md:618 (`Sandbox | status.* | WarmPoolController | Sole writer of phase and conditions`).

FACT: the §5.2 disposition table's last-row retirement clause is sound on timing, not only on the projection table. `Binder.Prepare` runs at `/finalize`, and its own comment states "The pod projects the coarse `claimed` phase set at claim time", so the claim is `bound` and the Sandbox projects `claimed` before any bind-sequence RPC can fail. The claim DELETE that `failPhase`/`drain` issues therefore lands on a `claimed` pod and projects `Draining`. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:901-908, pkg/controller/warmpool/occupancy.go:134-140

UNVERIFIED: whether the claim-deletion retirement can be lost to watch coalescing. If a claim's CREATE, its `bound` status patch and its DELETE were all coalesced into one WarmPoolController reconcile, `ProjectOccupancyPhase` would see no claim with `o.Current` still a warm-inventory phase and return `("", false)`, leaving the pod `idle` and unscrubbed with the failed bind's residue on it. I judged this unreachable in practice (a whole client HTTP round trip separates `/create` from `/finalize`) and pre-existing to this proposal in any case, so I did not file it. Whoever owns a future §4.6.1 fidelity review should settle it. — EVIDENCE: pkg/controller/warmpool/occupancy.go:126-131 (the warm-fill `("", false)` fallthrough)

USEFUL [Standing context, "The occupancy projection reads the pod's CURRENT PHASE, never the recycle setting"]: it named the exact function and the two phase arms, which turned the whole SPEC-4 verification into two targeted reads instead of a spec-and-code crawl.

### [spec.7.review-mechanism.1]
DECISION: returned an EMPTY findings list for the end-to-end-mechanism lens on the staged spec edits — BECAUSE every flow I traced (bind attempt token minting → rule 1/4 stamp → rules 5/6 refusal → rule 8 confirmation → rules 10-15 teardown → §5.2 hold → §5.2 disposition table → §6.2 fence → §7.1 reclaim) closes without a race, an unreachable trigger, a bypassable gate, or a predicate stated with different conjuncts at two sites — ALTERNATIVES: filing the rule-8 / §6.2 "`running` boundary" wording tension (rejected: "the pod's shared runtime process has been given" is bound vocabulary for `runtimeLive` cohort membership per the standing context, so rule 8 firing before the recording is consistent with §6.2; and the standing context records SEVEN rounds lost on rewriting that boundary); filing the reclaim hold reaching `DemoteSDK` (rejected: `DemoteSDK` names no session so it is not addressed to a held identifier, and at `maxConcurrentSessions: 1` a new session brings a new identifier).

FACT: every fenced "reads, verbatim" block in spec-changes.md was machine-checked against `spec/*.md` this round and all 24 source quotes are exact substrings of the current tree; only the replacement blocks are absent, as expected. Reproduce with a python pass that extracts fenced blocks by line number and substring-matches each against `glob('spec/*.md')` — it takes seconds and covers every SPEC-1..SPEC-6 anchor at once — EVIDENCE: proposals/0081_.../0081_...spec-changes.md:251,291,322,436,459,471,500,519,543,566,588,675,692,713,737,763,775,901,913,1085,1097,1109,1121,1151

FACT: every intra-spec anchor the staged blocks use resolves against a real heading (`#471-role-and-gateway-rpc-contract`, `#1542-rpc-lifecycle-state-machine`, `#479-startup-sequence-for-type-agent-runtimes`, `#461-warm-pool-controller-pod-lifecycle`, `#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#74-upload-safety`, `#101-horizontal-scaling`, `#49-credential-leasing-service`). §12.6's heading is "Interface Design"; the `agent_pod_state` schema is a paragraph inside it, so the SPEC-3 heading label is a locator rather than a heading quote — EVIDENCE: spec/12_storage-architecture.md:369, spec/12_storage-architecture.md:481

FACT: the "Spec files touched" list at spec-changes.md:1209-1257 is complete against the staged edit headings — all eight files and every anchor I could enumerate appear. No missing-edit-site finding survived: the only §5.2 `ReportSessionScrub` trigger statements outside spec/04 are at spec/05:453 and spec/05:545, both staged, and spec/12:481, staged; the only `leaked` trigger statements are spec/06:148 (staged) and spec/06:160 (`**\`leaked\` slot semantics.**`, which states no trigger and is correctly left alone) — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:453,545; spec/12_storage-architecture.md:481; spec/06_warm-pod-model.md:148,160

FACT: `Shutdown` and the two new fields appear nowhere in `spec/28_communication-channels.md`, so the "§28's registers" untouched claim at spec-changes.md:1192 holds; and `Shutdown` appears in spec/15 only at an unrelated line about runtime drain, so §15.4 owes no field-set edit — EVIDENCE: spec/15_external-api-surface.md:1797

FACT: `PrepareWorkspace` is the ONLY streaming RPC among the seven entry-creating RPCs, which is what makes rule 9 (first-frame) sufficient rather than needing a per-RPC carve-out. `Attach` and `Checkpoint` stream but create no entry — EVIDENCE: schemas/lenny-adapter.proto:41,48,55,62,72,89,151

FACT: the §5.2 disposition table partitions cleanly because the three non-`Shutdown` performers all deregister BEFORE their remaining acts except the SDK demotion, which closes first — that asymmetry is exactly why row 7 is keyed "an act fails after the deregistration" and row 8 exists separately. A fix that re-keys row 7 without that clause breaks the partition — EVIDENCE: 0081_...spec-changes.md:621-623

USEFUL [standing context, "**'The pod's shared runtime process has been given' is bound vocabulary.**"]: it is what stopped me filing rule 8's "the slot never reached `running`" against the §6.2 `running` boundary definition. Without it the two sentences read as a contradiction.
USEFUL [standing context, "**MISTAKE, SEVEN rounds on one boundary**"]: same site, second guard.

### [spec.7.review-performance.1]

DECISION: returned an EMPTY findings list for the performance/scalability/failure-mode lens on spec round 7 — BECAUSE every write-rate, serialization, cardinality and store-durability question the lens owns either resolves in the proposal's favour on the tree, or is on the standing context's barred list — ALTERNATIVES: filing the gateway-crash unstartable-session degradation (already an accepted failure mode with a one-sentence home in the spec file's Edge-cases section, and the review log bars rebuilding it into a remediation request); filing the `leaked`-rate increase against the `ceil(maxConcurrentSessions / 2)` replacement threshold (the capacity family, explicitly barred).

FACT: the proposal adds NO net control-plane or data-plane write. It adds no CRD status/condition write, no watch, no store-backed value, and two in-process counters. Its only durable-store effect is a REDUCTION: SPEC-3 conditions `ReportSessionScrub` on a `Shutdown` reclaiming a runtime-given slot, so every pre-`running` release stops writing `sessions_served` on `agent_pod_state`. The extra RPC the §7.1 obligation creates is one `Shutdown` per FAILED bind attempt, so its rate scales with bind failures rather than with bind volume. — EVIDENCE: spec-changes.md:568 (the conditioned scrub-model opening), :753-778 (§12.6 re-key)

FACT: the new metric's label set has an exact shipped precedent, so no cardinality finding is available. `lenny_slot_compensation_superseded_total` is labeled `pool`, `k8s_pod_name`; `lenny_slot_pod_replacement_total` already carries exactly that pair and `lenny_slot_failure_total` carries it plus `error_type`. The adapter-side untokened counter is labeled `k8s_pod_name` only, which is one series per adapter process. — EVIDENCE: spec/16_observability.md:14-15, spec-changes.md:1175-1176

FACT: the §4.7.1 registry critical-section paragraph introduces NO new serialization point. Every member it names is already inside `s.mu` in the tree: `ensureSlotStateLocked` runs `resolveSlotPaths` and `slotlayout.EnsureTree` (four mkdirs) under the lock today, and `deregisterSlotLocked` already cancels every armed §4.9 expiry timer and deletes the entry under the same lock. The paragraph's one net addition is rule 8's second resolve at the start, which is one map lookup. A future lens should not re-derive this. — EVIDENCE: pkg/adapter/slot.go:105-126 (create arm under s.mu), pkg/adapter/slot.go:140-143 (`ensureSlotPaths` takes s.mu), pkg/adapter/slotsession.go:174-189 (`deregisterSlotLocked`)

FACT: the staged §5.2 slot-cleanup action list's two added acts are both shipped, so neither is a new cost on the cleanup path. `deregisterSlotLocked` cancels the direct-mode lease-expiry timers, and its doc comment states the reason verbatim ("an armed timer left behind fires AUTH_EXPIRED against a session that has already ended"). — EVIDENCE: pkg/adapter/slotsession.go:158-181

FACT: the two capacity arithmetic claims in the Edge-cases section check out against the spec. §5.2's trigger is `ceil(maxConcurrentSessions / 2)` slots `failed` in a rolling 5-minute window or `leaked` persistently, so one leak reaches it at `maxConcurrentSessions: 2` and does not at 3. `maxSlotRetries = 1` is the shipped constant, so a refused retry is the request's last attempt under the default. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:561, pkg/gateway/sessionserver/start.go:2720

USEFUL [standing context, "The hold is keyed on the slot identifier, which IS the session identifier"]: this is the single fact that closes the whole availability family against the reclaim hold. N concurrent cleanups on one pod hold N distinct identifiers and refuse only their own session's requests, so the hold is not a hot key, not a single-leader serialization point, and creates no cross-session head-of-line blocking at any tier. Combined with "during the hold the entry is deregistered, so a resolving request finds nothing either way", it also kills the reading under which §5.2's wide "creates or resolves" predicate would refuse a mandatory credential control.

UNVERIFIED: nobody in this window has re-quantified what the conditioned `ReportSessionScrub` does to the `mode_factor` the PoolScalingController derives from the `lenny_pod_session_reuse_count` histogram (spec/16_observability.md:128), which is observed per pod at session end. If the histogram observation is driven off the same report, withholding it on pre-`running` releases changes a scaling input rather than only a counter. A scaling or observability lens should check whether the histogram observation and the `sessions_served` increment share a call site; it is out of this loop's scope either way, since the remedy would be code.

### [spec.7.review-reliability.1]

DECISION: returned an EMPTY findings list under the reliability/fault-tolerance lens — BECAUSE every
recovery mechanism the staged spec edits add or change traces correctly through crash, restart, cancelled
context and slow-cleanup, and every candidate I developed either resolved to stated-and-accepted text, to
a code-lane remedy this loop may not close, or to a mistake of my own that the tree refuted — ALTERNATIVES:
the seven candidates below, each killed for the stated reason.

FACT (killed candidate 1, the one that cost the most and is worth writing down). The staged §5.2 reclaim-hold
predicate — "the adapter admits no request that would create or resolve a registry entry under it ... A request
that resolves an entry without creating one is refused on the same terms" — looks like it makes a conforming
adapter refuse `CoordinatorFence`, the §11.4 revoke, `RotateCredentials` and `ExtendCredentialLease` during a
cleanup, which would stall the §10.1 handoff (3 attempts × 1s, then the new coordinator relinquishes the lease)
and fail the revoke open. It does NOT, and the reason is the hold's WINDOW rather than its predicate: the hold
opens AT the deregistration (staged §4.7.1 `**The registry critical section.**`, "On every release ... the
deregistration of the entry and the opening of the ... reclaim hold"), so throughout the hold there is no entry
for those requests to resolve and `boundSlotState` already fails them. The hold's only effect on an
entry-resolving request is to convert a not-found error into the transient `ABORTED` §15.4 publishes, which is
the improvement the §7.4 mid-session-upload sentence names. Do not re-derive this; the spec-vs-code width
difference (Settled #105 wide predicate vs Settled #75 read-only resolves) is real and benign for the same
reason. — EVIDENCE: spec-changes.md:628 (hold paragraph), :987 (critical section); pkg/adapter/coordination.go:107-117
(`CoordinatorFence` resolves via `boundSlotState`); spec/10_gateway-internals.md:38-39 (hard precondition, 3 retries).

FACT: the staged §5.2 hold sentence "bounded by the graceful window the reclaiming `Shutdown` carries when it
carries one, by that request's own deadline when it carries none" is EXACTLY what the shipped handler does.
`contextWithGraceDeadline(ctx, deadlineMs)` returns the parent context unchanged when `deadlineMs <= 0` and
`context.WithTimeout(parent, grace)` otherwise. `resolveShutdownGrace` (which prefers the ctx deadline) sits
INSIDE `MCPRuntime.Close` and picks the SIGTERM→SIGKILL pivot off the already-grace-bounded close context, so it
is not a counterexample. — EVIDENCE: pkg/adapter/session.go:262-265, :327-332; pkg/adapter/mcpruntime.go:295, :312-324.

FACT: the §10.1 hold-timeout "graceful window of ten seconds" in the staged §5.2 hold paragraph survives CODE-6.
CODE-6 re-scopes `onHoldTimeout`'s single ten-second context to the pass's guard-acquisition deadline alone AND
gives each member its own ten-second close context inside `terminateHeldSession`, so the per-member runtime close
is still bounded by ten seconds. A finding that CODE-6 falsifies the §5.2 figure is wrong. — EVIDENCE:
non-spec-changes.md:3722-3726.

FACT (killed candidate 2). SPEC-4's re-key of §4.6.1's hold-expiry bullet drops the old "on a recycling pod under
its limits" qualifier for "while the pod projects `reserved`". This is not a widening: §5.2's session-count limit
retires the pod to `draining` at the recycle disposition, before the claim can reach `reserved`, so a pod
projecting `reserved` is under its limits by construction. The projection also stays TOTAL after the re-key,
because §4.6.1's first bullet ("`idle` for a pod in a warm-inventory phase with no claim") is untouched and
`reserved`/`claimed` are excluded from warm inventory. — EVIDENCE: spec/04_system-components.md:411, :414, :415-416;
spec/05_runtime-registry-and-pool-model.md:488; pkg/controller/warmpool/occupancy.go:57-80, :113-116.

FACT (killed candidate 3). A slow-but-successful pre-`running` cleanup whose `Shutdown` outruns the gateway's
`slotCleanupBudget` books a permanent `leaked` in the gateway ledger while the pod is in fact clean. This is
STATED, not missed: the paragraph after the §5.2 disposition table says "a [§7.1] reclaim the adapter does not
answer enters [`leaked`] as a reclaim whose act fails does, and its hold ends on the terms of the row the cleanup
on the pod met", and the non-spec "Faster pod churn" bullet accepts the churn. — EVIDENCE: spec-changes.md:626;
non-spec-changes.md:3583-3589.

FACT (killed candidate 4). A release outside a `Shutdown` whose act fails holds the identifier for the life of
the pod with NO report and `leaked` "Not entered, because nothing carries the outcome to the gateway", so the
gateway never learns. That cell is an explicit recorded choice, not a gap. — EVIDENCE: spec-changes.md:622, :637-640.

FACT (killed candidate 5). The §5.2 scrub-model biconditional withholds the cleanup-outcome report from the
§10.1 hold-timeout termination even for a slot the runtime WAS given, so one served session goes uncounted
against `recycle.maxSessionsPerPod`. Pre-existing and unreachable: `reportSessionScrub` has one caller, the
`Shutdown` handler, and the §10.1 coordinator hold cannot arm in production. — EVIDENCE:
spec-changes.md:595; summary.md:797-807.

WATCHOUT: `maxConcurrentSessions: 1` leaves `slot_cleanup` with no terminal when a cleanup fails (the
`slot_cleanup ──→ leaked` edge sits in the concurrent-occupancy block only, and `slot_cleanup ──→ released` has
no trigger after SPEC-4). Do NOT file it: the same hole existed before this proposal via `running ──→
slot_cleanup`, which is already in the either-concurrency block. — EVIDENCE: spec/06_warm-pod-model.md:146-155.

USEFUL [Settled #75 "The reclaim hold cannot refuse a mandatory credential control"]: this entry is what made
candidate 1 look like a live fail-open. It is right about the code and silent about the reason the width
difference is harmless. Extend it with the window argument in FACT (killed candidate 1) above so the next lens
stops at the hold's window rather than re-tracing `boundSlotState`.

USEFUL [Traps, "A residue-disposition cell is changed in the §5.2 table and NOWHERE ELSE"]: killed candidates 3,
4 and the single-session `slot_cleanup` terminal in one read each.

UNVERIFIED: whether the gateway double-books a leak for a runtime-given slot whose runtime close fails — row 2 of
the §5.2 table gives BOTH a `leaked` report and an unclean `Shutdown` response, both ledgers are one
`slothealth.Tracker` (Settled #95), and `RecordSessionScrub` has no per-session dedup (Settled #122). If
`MarkLeaked` is not per-slot idempotent, one slot advances the unhealthy threshold by two and drains a pod at
`maxConcurrentSessions: 3-4` a leak early. The remedy is in CODE, so it is out of this loop's scope; the code lane
should check `pkg/slothealth`.

### [spec.7.review-security.1]

DECISION: returning an EMPTY findings list for the security lens on spec round 7 — BECAUSE every
security-relevant claim in the staged spec edits verified against the tree, and the two remaining
families (pod self-report as a leak/reuse bound; the wide hold predicate reaching a mandatory
credential control) are already refuted in the standing context and re-confirmed below —
ALTERNATIVES: filing the `k8s_pod_name` §16.1.1 "used by" cell as an unstaged edit site (rejected:
not a security defect, and the cell's "slot failure and replacement metrics" phrase is a summary,
not an enumeration); filing the §4.6.3 Notes cell ("the occupancy phase is a level-triggered
projection of `SandboxClaim` state") against SPEC-4 adding the pod's own projected phase as a
projection input (rejected: the cell cites §4.6.1 for the detail and is out of this lens).

FACT: the two shipped-behaviour claims under the §5.2 `**Slot cleanup:**` action-list edit both
check out verbatim. `slotlayout.RemoveTree` removes `p.CredentialsDir` as its fourth tree —
EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69 (`for _, dir := range []string{p.slotRoot(),
p.Sessions, p.Artifacts, p.CredentialsDir}`). `deregisterSlotLocked` cancels every armed
direct-mode expiry timer before `delete(s.slots, …)` — EVIDENCE: pkg/adapter/slotsession.go:174-181
(`for provider := range st.timers { s.cancelSlotExpiryTimerLocked(st, provider) }`).

FACT: the staged §4.7.1 sentence "the gateway issues one only for a session the issuing replica
holds a live binding for" (mid-session upload) is TRUE in the tree, so the whole safety argument
for a request that asserts no attempt identity stands and needs no spec change. The handler looks
the binding up in the replica-local pod registry and returns 409 `TARGET_NOT_READY` when it is
absent, before any `PrepareWorkspace`/`FinalizeWorkspace(midSession=true)` goes out — EVIDENCE:
pkg/gateway/sessionserver/upload_to_session.go:111-122. The spec-changes commentary at
spec-changes.md:1036-1039 still asks the implementation to "confirm the shipped mid-session
admission guard reads what it appears to read"; that confirmation is now done and the answer is yes.

FACT: the rule-10 `INVALID_ARGUMENT` arm does NOT open a hole in the whole-pod credential purge,
which is the §5.2 scrub step 0 mandatory control ("every one MUST be purged before any
deployer-defined code runs", spec/05_runtime-registry-and-pool-model.md:461). The staged §4.1
sentence gates the scrub on passing the pairing rule (spec-changes.md:258), but the gateway arms
the missing-`ReportPodScrub` timeout BEFORE it sends the recycle-carrying `Shutdown` and retires
the pod when no report arrives — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:459 ("after
it has patched the claim to `recycling` and armed the missing-report timeout, keyed on `podId`").
A refused recycle `Shutdown` therefore fails closed to pod retirement rather than to a reused pod
carrying a previous session's `credentials.json`. Do not file this; it costs two verifiers.

FACT: the staged token block's "the generation … is validated on the RPCs that carry it … Where
both appear on one message, as on `Shutdown`, each is checked on its own terms" AGREES with the
current spec, so it is not a contradiction finding even though the adapter validates
`coordination_generation` only on `CoordinatorFence`/`CheckpointBarrier` — EVIDENCE:
spec/10_gateway-internals.md:30 ("Pods validate the generation on every gateway→pod RPC — if the
generation is stale, the pod rejects the request"). The code-side gap is the pre-existing
"`Shutdown` is unfenced" fact in the review log's standing context, not a defect this staging
introduces.

USEFUL [MISTAKE nearly filed, the security family]: the four refuted dresses listed there
(`superseded`/`absent` as self-reports exempting a slot from leak accounting; a compromised adapter
refusing every teardown; a late `StartSession` orphan holding credentials; the withheld report
relaxing `maxSessionsPerPod`) are exactly the four the new §5.2 disposition table and the §12.6
`sessions_served` re-key invite a fresh reader to re-derive. The entry saved a full pass.

USEFUL [Do NOT file the §15.4 "`Shutdown` is not held" carve-out against the wide hold predicate]:
the vacuous-resolve argument ("during the hold the entry is deregistered, so a `Shutdown` resolves
nothing") is also what kills the security dress that the §5.2 hold's clause "A request that
resolves an entry without creating one is refused on the same terms" (spec-changes.md:628) blocks
a §11.4 emergency credential revoke. Re-confirmed: revoke/rotate/extend resolve through the
read-only accessors, never `ensureSlotStateLocked`.

WATCHOUT: the §5.2 acknowledged concurrent-pod posture makes a surviving `credentials.json`
readable by every co-tenant slot via the shared `lenny-cred-readers` group — EVIDENCE:
spec/05_runtime-registry-and-pool-model.md:517. That makes every "released outside a `Shutdown`,
an act fails" row of the disposition table look like a credential-exposure finding. It is not one
for this loop: `releaseSessionSlot` discards `removeSlotTree`'s error today
(pkg/adapter/slotsession.go:214-219), so the table records shipped behaviour, and the standing
trap "Do NOT unify the two pre-`running` cleanup PERFORMERS" bars the remedy.

### [spec.7.review-single-source.1]

DECISION: returned an EMPTY findings list for the single-source lens on the staged spec edits — BECAUSE every rule I inventoried resolves to exactly one stating site, and each of the remaining multi-site rules falls inside the lens's own cite/clause/rationale carve-out — ALTERNATIVES: I considered and rejected four candidates, listed below, each for a stated reason; a later round should not re-open them without new evidence.

FACT: the round-6 fixes all landed clean and reduce rather than add. Verified by diff against scratchpad/cp-snap/0081-opt2/spec-r5-prefix: rule 7 lost its stamp-immutability clause (spec-changes.md:999 now reads only "Any other request is admitted."); the §15.4 hold block's `Shutdown` sentence became a citation of §5.2 and §4.7.1 (spec-changes.md:1056); §7.1 lost the minting restatement (spec-changes.md:403); the §5.2 post-table paragraph lost the false whole-pod-scrub sentence (spec-changes.md:626); two Edge-cases bullets became pointers (spec-changes.md:134, :153). Do not re-file any of these.

FACT: the reclaim-hold status has exactly one home and three citing sites, and they are correctly partitioned. §5.2's hold paragraph (spec-changes.md:628) states the window and the refused request set but NO status; §15.4's hold block (spec-changes.md:1056) states `ABORTED`; rule 2 (spec-changes.md:994) cites §15.4 for the status and §5.2 for the scope. Rules 5 and 8 also answer `ABORTED` but for their own refusals, which are different rules, not copies.

WATCHOUT: "at most one cleanup-outcome report per session release" appears at two staged spec sites, §5.2 (spec-changes.md:612) and rule 8 (spec-changes.md:1000). It is NOT a copy: rule 8's use is a `because` clause that names §5.2 as the authority. A lens that greps for repeated phrases will surface this; it is the rationale carve-out working as intended.

WATCHOUT: the `superseded` outcome definition is close to verbatim in two places — rule 15 (spec-changes.md:1012) and the §16.1 metric row for `lenny_slot_compensation_superseded_total` (spec-changes.md:1175, "the adapter holds an entry for the session that the compensation is not addressed to, so the reclaim released nothing"). I did not file it: a metric catalog row has to say what the counter counts, and the row carries "See [Section 4.7.1]". If a future round wants a reduction, the row's gloss is the site to trim, never rule 15.

OPEN (considered, not filed): the `running` boundary is stated twice in spec/06 after the edits — the untouched fence entry `receiving_uploads ──→ running (workspace ready, session dispatched to runtime with its session identifier)` at spec/06_warm-pod-model.md:152-153, and the staged pre-`running` paragraph's "A slot reaches `running` when the pod's shared runtime process has been given the session with its session identifier" (spec-changes.md:949). They agree today. I judged the fence entry to be a state machine's ordinary edge annotation rather than a second stating site, and note the proposal reduced the two `slot_cleanup` fence triggers to pointers only because no short trigger was true of every traversal (spec-changes.md:925-932), which does not apply here. A later round that wants full consistency would make the `receiving_uploads ──→ running` annotation a `(see the paragraph below)` pointer; it would not delete the paragraph's boundary sentence, which §4.7, §5.2 and §6.2's new edge all key on.

FACT: spec/06_warm-pod-model.md:160's `**`leaked` slot semantics.**` paragraph states only what a leaked slot holds and counts toward, and states no entry trigger, so the staged §5.2 disposition table's `leaked` column duplicates nothing already in §6.2. The only pre-existing trigger statements were the fence entry at spec/06:148 and the §5.2 whole-pod-replacement parenthetical at spec/05_runtime-registry-and-pool-model.md:561, and the proposal reduces both to pointers.

USEFUL [Standing context, "DECISION: §15.4's fifteen-row per-rule wire table is DELETED"]: this and the sibling entry on the §5.2 disposition table being the one home let me skip re-deriving the site topology and go straight to checking the carve-outs. The standing-context section is worth reading in full before any single-source pass; it names the home of every rule this proposal states.

### [spec.8.fix-G1.1]

DECISION: completed the existing SPEC-3 §12.6 tier-11-gate paragraph in place rather than opening a new home for gate bookkeeping — BECAUSE that paragraph is already the single site stating which shipped gates the §12.6 replacements break, and the defect was a false count plus a missing entry — ALTERNATIVES: staging the test-file re-key as a spec deliverable (rejected: code lane), weakening the write-clause replacement so the pinned substring survives (rejected: the write trigger is exactly what SPEC-3 corrects), a collecting "gates this proposal turns red" subsection (rejected: would drift from the per-deliverable statements).

FACT: two shipped tier-11 assertions read the §12.6 `sessions_served` sentences and no others in the tree do. `tests/tier11_docs/spec_28_register_writers_test.go` defines `podStateGatewayWrittenSentence` (constant block near the other byte-exact spec sentences) and asserts it in `assertPodStateWriterSetMatchesStorage` against `specSection(spec/12, "### 12.6 ")`, reached from subtest "pod state writer set" of `TestSection28RegisterWritersMatchTheSpec_spec_28_3`; the other is `TestPerReleaseSessionCountDrainAgrees_F5231` in `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`. — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99, :744, :360; spec/12_storage-architecture.md:481.

FACT: other tier-11 tests do read `### 12.6` (`cloudevents_alias_reconciliation_test.go`, `redis_key_prefix_registry_test.go`), but none of them pins a `sessions_served` sentence, so widening the clearance from file scope to tree scope is safe. — EVIDENCE: tests/tier11_docs/redis_key_prefix_registry_test.go:83; tests/tier11_docs/cloudevents_alias_reconciliation_test.go:78.

WATCHOUT: `podStateGatewayWrittenSentence` ends at "...(`ReportPodScrub`) respectively" and therefore pins the WRITE clause alone. The read-clause and DDL-comment replacements do not reach it. Saying otherwise in the staged rationale is a citation finding. — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99-101.

WATCHOUT: the §28.3 `REG-PODSTATE` writer-set cell in spec/28 is a separate thing from this gate. The row itself states no write trigger and SPEC-3 does not touch spec/28; the gate fails because it reads spec/12's sentence. An earlier clearance conflated the two. — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:681-684.

MISTAKE: the archived clearance "`spec_28_register_writers_test.go:101` pins a §12.6 sentence this proposal does not touch" was true only before SPEC-3 acquired the §12.6 prose replacements, and later rounds carried it forward unre-checked. It cost this round a finding and a fix.

CORRECTS [spec round 7 applicability-lens entries in the review log, "no existing gate hard-fails from the spec-only edits" and "FACT: the spec-only edits break no shipped gate. Checked and clear:"]: both are incomplete. `TestSection28RegisterWritersMatchTheSpec_spec_28_3`, subtest "pod state writer set", does hard-fail the moment SPEC-3's §12.6 write-clause replacement is applied. The ledger entries stand as the record of what that round concluded; the correction is here.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.non-spec-changes.md]: the code lane carries no deliverable re-keying `podStateGatewayWrittenSentence` in `tests/tier11_docs/spec_28_register_writers_test.go`. What is true instead: that constant must be re-keyed from "incremented at each session release (`ReportSessionScrub`)" onto "incremented on each cleanup-outcome report (`ReportSessionScrub`)" in the same step that applies SPEC-3, alongside the already-recorded `TestPerReleaseSessionCountDrainAgrees_F5231` re-key.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: step S4 (SPEC-3) names no accompanying test-file re-key at all, and the §12.6 replacements it applies break two shipped tier-11 gates. What is true instead: the step that applies SPEC-3 also re-keys the substring pair in `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go` (`TestPerReleaseSessionCountDrainAgrees_F5231`) and the `podStateGatewayWrittenSentence` constant in `tests/tier11_docs/spec_28_register_writers_test.go`, as the SPEC-3 §12.6 tier-11-gate paragraph now directs.

### [spec.8.fix-design-G1.1]

DECISION: fix the SPEC-3 §12.6 tier-11-gate paragraph in place — it says "a shipped tier-11 gate" and must say two, naming `TestSection28RegisterWritersMatchTheSpec_spec_28_3` ("pod state writer set") and the re-key of its `podStateGatewayWrittenSentence` constant — and replace its file-scoped clearance sentence with a tree-scoped one — BECAUSE that paragraph is the single home for "which shipped gates these replacements break", and the fix is a count plus one more gate in the same form the paragraph already uses for F5231 — ALTERNATIVES: staging the test-file edit itself in spec-changes.md (rejected: code lane, the spec loop may not write non-spec-changes.md); softening the §12.6 write-clause replacement so the constant's substring survives (rejected: the re-key is the deliverable's point, and the gate is supposed to move with the prose); rewriting the round-7 review-log clearance entries (rejected: ledger entries are the audit record of what a round concluded, corrected by a CORRECTS entry, never edited).

FACT: exactly two assertions in the whole tree read the §12.6 `sessions_served` sentences. `tests/tier11_docs/spec_28_register_writers_test.go:99-101` (constant) + :744-746 (`assertPodStateWriterSetMatchesStorage`, reached from the subtest at :360), and `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173-174`. No file under `docs/`, `schemas/` or `charts/` mirrors the write-trigger sentence; `grep -rn "gateway-written recycle counters"` outside `proposals/` returns only spec/12:481 and that test. EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99-101,:744-746; spec/12_storage-architecture.md:481.

FACT: the register-writers constant pins ONLY the write clause (it ends at "...(`ReportPodScrub`) respectively"). SPEC-3's read-clause and DDL-comment replacements do not reach it; only the write-clause replacement does. A fix that attributes the breakage to "these two replacements" without that distinction overstates it. EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99-101 vs proposals/0081_.../....spec-changes.md:773-780.

MISTAKE: spec round 7's applicability lens concluded "no existing gate hard-fails from the spec-only edits" and its "Checked and clear" list enumerated four gates, omitting `spec_28_register_writers_test.go`. The archive's older clearance ("`spec_28_register_writers_test.go:101` pins a §12.6 sentence this proposal does not touch") was true before SPEC-3 acquired the §12.6 prose replacement and went stale when it did. Cost: one round. Whenever SPEC-3's §12.6 text changes, re-grep the pinned substrings rather than trusting an earlier clearance. EVIDENCE: review-log.md:2010 and :1992; review-log-archive.md:6775.

CORRECTS [spec.7.review-applicability.1]: its "FACT: the spec-only edits break no shipped gate" is false. `TestSection28RegisterWritersMatchTheSpec_spec_28_3` / "pod state writer set" hard-fails the moment SPEC-3's §12.6 write-clause replacement lands.

DEFERRED [non-spec-changes.md]: no deliverable edits `tests/tier11_docs/spec_28_register_writers_test.go`. After SPEC-3, `podStateGatewayWrittenSentence` (:99-101) is false: it requires "incremented at each session release (`ReportSessionScrub`)". What is true instead: the constant must read "incremented on each cleanup-outcome report (`ReportSessionScrub`)", with the rest of the sentence unchanged. This is the same shape as the already-recorded DEFERRED for `concurrent_slot_lifecycle_doc_reconciliation_test.go`; one code-lane deliverable should carry both re-keys.

### [spec.8.review-fresh.1]

FACT: SPEC-3's §12.6 PROSE WRITE-CLAUSE replacement breaks a SECOND shipped tier-11 gate that the proposal never names. `tests/tier11_docs/spec_28_register_writers_test.go:99-101` holds the constant `podStateGatewayWrittenSentence`, a byte-exact copy of the §12.6 sentence INCLUDING "incremented at each session release (`ReportSessionScrub`) and on each failed whole-pod scrub (`ReportPodScrub`) respectively"; `assertPodStateWriterSetMatchesStorage` (:739-746) feeds it to `requireAllContain` against `specSection(... "### 12.6 ")`, and the subtest is `TestSection28RegisterWritersMatchTheSpec_spec_28_3` / "pod state writer set" (:360). SPEC-3 replaces that exact substring, so the constant stops matching — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99-101,:360,:739-746; spec/12_storage-architecture.md:481; spec-changes.md:773-780.

CORRECTS [review-log.md:1174 "spec/28:140's `REG-PODSTATE` register row names `sessions_served` but states NO write trigger ... so SPEC-3's §12.6 re-key does not reach it and spec/28 needs no edit"]: the entry is right about the §28.3 ROW and it misled at least one round about spec/28's tier-11 GATE. The gate for §28.3 does not read the row's trigger; it pins the §12.6 sentence verbatim as a Go constant. "spec/28 needs no spec edit" and "no spec/28 gate goes red" are different claims and only the first is true.

CORRECTS [review-log-archive.md:6775 and :49985, "`spec_28_register_writers_test.go:101` pins a §12.6 sentence this proposal does not touch"]: true when written, false since SPEC-3 acquired the §12.6 prose write-clause replacement. Do not reuse that clearance.

WATCHOUT: the spec-changes.md paragraph at :810-821 enumerates the gates SPEC-3's §12.6 edits turn red and names ONE (`TestPerReleaseSessionCountDrainAgrees_F5231`). Its closing sentence "No other assertion in that file reads spec/12" is true but only quantifies over `concurrent_slot_lifecycle_doc_reconciliation_test.go`; a reader takes it for a whole-tree clearance. The whole-tree sweep is `grep -rn "sessions_served" tests/` — EVIDENCE: spec-changes.md:810-821; tests/tier11_docs/spec_28_register_writers_test.go:99.

FACT: the whole-tree sweep for §12.6 `sessions_served` gates returns exactly two files, `concurrent_slot_lifecycle_doc_reconciliation_test.go` and `spec_28_register_writers_test.go`; `docs/` carries no `sessions_served` mirror at all, so SPEC-3 owes no DOCS site — EVIDENCE: `grep -rn sessions_served tests/ docs/`.

FACT (re-verified, no finding): §5.2's `Session count limit` bullet (spec/05:488), spec/16 §16.1's `session_count_limit` row, docs/reference/state-machines.md:248 and §28.3's REG-PODSTATE row (spec/28:140) all survive the read-trigger re-key, each being a restrictive conditional over releases that advance the count rather than a universal over releases. Do not re-file them.

FACT: the code claim added at spec-changes.md:800-806 checks out verbatim. `ScrubReporter.RecordSessionScrub` calls `IncrementSessionsServed` then `RetireOnSessionCount(ctx, podID, count)` with the atomic post-increment value, and the emit is gated on exact equality (`perReleaseOwnsSessionCount`, `count == maxSessionsPerPod` crossing) — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-482,:645-660.

### [spec.9.review-fresh.1]
DECISION: no findings this round — BECAUSE the round-8→9 diff is a single hunk in spec-changes.md SPEC-3 (§12.6), replacing "one shipped tier-11 gate" with "two", and every repository claim it makes verifies. ALTERNATIVES: filing the §5.2 `**Session count limit:**` bullet as a missed edit site, rejected below.
FACT: the §12.6 sentences the hunk quotes are byte-exact in the tree, both on one physical line — EVIDENCE: spec/12_storage-architecture.md:481 (prose write clause and read clause), :494 (DDL comment, which writes `non-vm-restart` without backticks, as the hunk says).
FACT: `podStateGatewayWrittenSentence` ends exactly at "...(`ReportPodScrub`) respectively", so the read-clause and DDL replacements genuinely do not reach it — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99-101, asserted at :745 via `requireAllContain` on `specSection(spec/12, "### 12.6 ")`.
FACT: the hunk's exclusivity claim holds. The only tests/ readers of spec/12 §12.6 besides those two are cloudevents_alias_reconciliation_test.go:78,118 (Event struct), redis_key_prefix_registry_test.go:83,291 (`cb:events`), direct_usage_reportusage_consistency_test.go:119 (usage re-report sentence); none touches the `sessions_served` sentences. No docs/, schemas/, or charts/ page mirrors them (`sessionsServed` in schemas/lenny-adapter.proto:314,453 states the §5.2/§4.7 report rule, already staged under non-spec SCHEMA-1, not the §12.6 counter sentences) — EVIDENCE: schemas/lenny-adapter.proto:314; non-spec-changes.md:2217.
FACT: §28.3's `REG-PODSTATE` row states writers and readers but no write trigger, so the hunk's "writer-set cell is unchanged" is right — EVIDENCE: spec/28_communication-channels.md:140.
WATCHOUT: §5.2's `**Session count limit:**` bullet still says the pod drains "on the session release that drives the served-session count to `maxSessionsPerPod`" while SPEC-3 re-keys §12.6 onto the cleanup-outcome report. This LOOKS like the same defect the §12.6 re-key fixes and is not: the §12.6 clause was a universal ("at each session release") that the conditioned report falsifies, whereas the §5.2 clause is a definite description of the one release that does drive the count, which stays true when a non-reporting release drives nothing. Do not file it — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488; spec-changes.md:772-786.
FACT: `ScrubReporter.RecordSessionScrub` does increment and evaluate in one call, gating the retirement emit on the atomic post-increment count, exactly as the SPEC-3 rationale states — EVIDENCE: pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481.
UNVERIFIED: the hunk says `podStateGatewayWrittenSentence` "is re-keyed onto 'incremented on each cleanup-outcome report (`ReportSessionScrub`)'" without saying whether the constant keeps its trailing `ReportPodScrub` clause. The remedy is in a test file, so it is the code lane's to settle; the implementor should keep the full sentence rather than truncating the constant.

### [spec.10.fix-followup.1]

Corrections to the round-10 conformance-criterion pass, appended to that pass rather than opened
as a new one. The round's own fix left no ledger entry, so this entry carries the pass and its
follow-up together.

DECISION: the §15.4 `**Bind attempt token contract:**` domain sentence names the two teardowns
inside the rules' domain and names the per-RPC work outside it by example, rather than routing
all of §4.7's per-RPC work outside — BECAUSE rules 11 through 14 each fix whether the request
performs the teardowns, and the block's closing paragraph routes their preconditions to the §4.7
`Shutdown` row, so the earlier wording declared outside the domain what four rules state, and the
conformance criterion read against that domain stopped covering a `Shutdown` that tears down a
runtime rule 13 says it must not touch — ALTERNATIVES: leaving the criterion unscoped (the
over-reach the round was opened to fix); listing every excluded act exhaustively (goes stale on
any RPC added to §4.7); dropping the domain sentence and scoping the criterion to the registry
alone (drops the teardowns from conformance entirely, which is the same defect in the other
direction) — EVIDENCE: spec-changes.md, SPEC-5 §15.4 block, and the SPEC-5 §4.7.1 rules 11
through 14 and their closing teardown pointer.

DECISION: `summary.md`'s design-summary bullet on where the rules are stated is re-keyed onto the
same wording as its deliverable-index parallel, "each rule stating whatever answer it fixes", and
its two-exception clause is deleted — BECAUSE rules 1, 3, 4, 7, 8 and 10 name no `ErrorCode` and
rules 4 and 7 fix no answer, so the universal is false of the rule set as staged, and the staged
§15.4 text no longer carries the lead-in the clause described — ALTERNATIVES: qualifying the
universal in place (a second statement of a scope §4.7.1 owns); restoring the §15.4 lead-in so
the clause becomes true again (re-lands the over-reach).

DECISION: the DOCS-2 adapter-contract paragraph's clause about §4.7.1 is re-keyed to "states for
each rule whatever answer it fixes" — BECAUSE it published the same false universal, and unlike
the other two sites it lands in the tree, so the proposal would ship a claim about §4.7.1 that
§4.7.1 does not satisfy. The repair is confined to that clause; the sentence's §15.4 conformance
pointer is unchanged.

DECISION: SPEC-5's §6.2 commentary is re-keyed onto the narrowed replacement it introduces —
BECAUSE the §15.1 row is total only over failures of the setup-command request, so the commentary
could not say the row states "the setup-window mapping for `POST /v1/sessions/{id}/start`", and
the replacement drops the clause's `STARTING_FAILED` half outright rather than turning the whole
restatement into a pointer. The commentary now states which half becomes a pointer, which half is
dropped, and where the dropped half already lives — EVIDENCE: the bullet's own preceding clause,
spec/06_warm-pod-model.md:290, "`POST /v1/sessions/{id}/start` (§15.1) surfaces a runtime-launch
failure as `STARTING_FAILED`", and the §15.1 `STARTING_FAILED` row.

FACT: the four corrections touch no rule text. Rules 1 through 15 in the SPEC-5 §4.7.1 block are
unchanged by this follow-up, and no deliverable is added, removed, merged, split or resequenced,
so `summary.md`'s deliverable index needed no edit beyond the design-summary bullet.

WATCHOUT: the SPEC-5 §15.4 commentary still reads "It states no rule of its own" while the
`**Slot-identifier reclaim hold:**` block publishes the `ABORTED` status rule 2 takes. Entry
`[spec.5.fix-G2.1]` treats that status as one of the block's declared exceptions. The two
statements are reconcilable as written and were left alone; a lens that files the commentary
should read that entry first.

### [spec.10.fix-G1.1]

DECISION: Narrowed SPEC-5's §6.2 replacement clause from "the setup-window failures at `/start` take the envelopes the `SETUP_COMMAND_FAILED` row of [§15.1] states" to "a failure of the setup-command request at `/start` takes the envelope the `SETUP_COMMAND_FAILED` row of [§15.1] states" — BECAUSE SPEC-5's own exclusion replacement re-keys the §15.1 row onto the setup-command request and explicitly declines to assign an envelope to a `FailedPrecondition` at any other bind-sequence request, so the row is total only over that one request. Every other `/start` setup-window failure falls to the bullet's own preceding runtime-launch clause and to §15.1's `STARTING_FAILED` row. — ALTERNATIVES: re-widening the §15.1 exclusion back to the whole window (undoes the re-key SPEC-5 exists to make); adding a `STARTING_FAILED` sentence back into §6.2 (a second full statement of a mapping whose home is §15.1's own `STARTING_FAILED` row).

WATCHOUT: the phrase "the single home of the setup-window mapping" overstates what SPEC-5's §15.1 row carries after its own re-key. The row is the single home of the mapping for the setup-command request only. Both summary sites now say the narrow phrase; a future edit that reverts to the wide phrase re-opens this finding. — EVIDENCE: proposals/0081_.../0081_....summary.md, the SPEC-5 bullet in the design summary and the SPEC-5 entry in the deliverable index.

FACT: the generic `/start` fallback has its own home in the shipped catalog, the `STARTING_FAILED` row of §15.1, so nothing in this proposal needs to restate it. — EVIDENCE: spec/15_external-api-surface.md, `STARTING_FAILED` row of the REST error catalog.

USEFUL [review-log DEFERRED on S1]: the implementation-checklist DEFERRED entry recording S1's staleness already flags the §15.1-re-key-versus-§6.2-pointer split, which located this block immediately. That entry itself repeats the wide "single home of the setup-window mapping" phrase; it is an audit record and stays as written.

### [spec.10.fix-G2.1]

DECISION: closed "the staged §15.4 conformance criterion is unsatisfiable" by DELETION plus one scope sentence, not by quantifying the criterion per rule — BECAUSE both halves of the defect came from §15.4 half-restating §4.7.1's answer contract; "answers as it states" is exactly as strong as the status/`ErrorCode`/outcome triple on every rule that fixes an answer and is vacuous on rules 4 and 7, and one sentence fixing the rules' DOMAIN (refusal or admission, the slot registry, the record of which sessions the pod's shared runtime process holds, the answer; the work a request performs for its own sake is §4.7's, per RPC) makes "performs the acts it states and no others" true without hedging it to "registry acts" — ALTERNATIVES: the reviewer's literal wording (rejected: longer, keeps the triple and the two-exception clause); deleting the criterion (already settled against, §15.4 is the only published definition of conformance and §4.7.1's closing prose leans on it); giving rules 1, 3, 8, 10 an `ErrorCode` (rejected: invents wire surface and forces rule 1's bare `codes.InvalidArgument` through `slotResolveError`).

WATCHOUT: the two-exception clause deleted from the §15.4 lead-in (rule 2's status from the reclaim-hold block, rules 11-14's from rule 15) is NOT lost content and must not be reinstated. Rule 2 already cites §15.4 for its status in its own text, and rule 15 sits inside §4.7.1, so neither was ever an exception to "§4.7.1 states the answers". — EVIDENCE: spec-changes.md, SPEC-5 §4.7.1 block, rules 2 and 15; SPEC-5 §15.4 `**Bind attempt token contract:**` block.

FACT: the same false universal was written at three sites, and a grep for "the gRPC status, the `ErrorCode`" finds all of them: the staged §15.4 lead-in, the SPEC-5 deliverable-index bullet in summary.md, and CONF-1's `**The rule set under test.**` sentence in non-spec-changes.md. All three now say "whatever answer the rule fixes" / "answers as it states". No tree file carries any form of the criterion; SPEC-5 is unlanded. — EVIDENCE: spec-changes.md SPEC-5 §15.4 block; summary.md SPEC-5 index entry; non-spec-changes.md CONF-1 `**The rule set under test.**`.

FACT: `sed -n` with the glob `*spec-changes.md` in this proposal directory silently reads BOTH `...non-spec-changes.md` and `...spec-changes.md` and numbers their lines continuously, so every offset is wrong by the length of the first file. Use the full filename. — EVIDENCE: the two files share the `spec-changes.md` suffix.

DEFERRED [/home/ec2-user/lenny/proposals/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry/0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.implementation-checklist.md]: S1 says §15.4 is re-cut to "state the three non-conformances that are not any single rule's condition: a wrong evaluation order, a separable step, and a gRPC error for an outcome the reclaim-outcome rule reports". The staged §15.4 states no such three non-conformances and deliberately does not: all three are reached through the criterion's citation of §4.7.1 (its cascade order, its registry critical-section paragraph, and rule 15). What is true instead: "state the conformance criterion, which reaches a wrong evaluation order, a separable step and a gRPC error for a reclaim outcome through its citation of §4.7.1".

CORRECTS [review-log.md:354, the OPEN "Does the staged §15.4 conformance criterion over-reach?"]: closed. Both halves are fixed, and the fix is in §15.4 alone, as review-log.md:2169 predicted it should be. The related UNVERIFIED entries (review-log.md:1144, :1261, :1343, :2169) and the WATCHOUT at :2069 concern the deleted lead-in clause and no longer describe the staged text.

USEFUL [review-log.md:31, "§15.4's conformance criterion quantifies over REQUESTS and states no rule-selection predicate"]: it ruled out the whole family of per-rule rewordings before I started, which is why the fix is a deletion.

### [spec.10.fix-G3.1]

DECISION: SPEC-3's §12.6 read clause and DDL read half now CITE §5.2's `**Session count limit:**`
bullet instead of being re-keyed onto "cleanup-outcome report" — BECAUSE the concurrent-pool
`maxSessionsPerPod` evaluation point is one rule written out at three spec sites (spec/05:488,
spec/06:139-142, spec/12:481 and :494) and re-keying only the §12.6 pair would have left two
vocabularies for one rule with the untouched copies carrying the trigger the proposal's own
rationale calls wrong — ALTERNATIVES: re-key §5.2's bullet and §6.2's fence edge to match
(drags in spec/06, docs/reference/state-machines.md and three of the four checks in
`TestPerReleaseSessionCountDrainAgrees_F5231`, and nothing forces it); delete the read clause
with no pointer (a reader of the column loses where the counter is evaluated); widen the gate's
accepted substrings onto the citation (asserts that a citation is a statement of the rule).

DECISION: the `TestPerReleaseSessionCountDrainAgrees_F5231` remedy in SPEC-3's gate paragraph is
now DELETION of the gate's spec/12 substring block rather than a re-key of its two accepted
substrings — BECAUSE after the reduction §12.6 contains no evaluation-point sentence for that
gate to compare. The gate's other three checks and the three sites they read are untouched by
this proposal, verified: spec/05:488 is unedited; spec/06's per-release edge at :139 sits in the
Concurrent-occupancy fence, which SPEC-4 does not open (SPEC-4 edits the Occupancy-projection
fence entry near spec/06:95-114); docs/reference/state-machines.md:248 is unedited (DOCS-1 edits
:138, :237 and :251).

FACT: §5.2's `**Session count limit:**` bullet stays true under the settled mechanism without an
edit. It is phrased on "the session release that drives the served-session count to
`maxSessionsPerPod`", and a release that files no report never drives the count —
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488.

WATCHOUT: `schemas/lenny-adapter.proto` states the withdrawn universal report trigger on the
`ReportSessionScrub` surface TWICE, on the RPC comment and on the request-message comment, and a
fix that corrects one leaves the other. SCHEMA-1 now carries both replacements —
EVIDENCE: schemas/lenny-adapter.proto:308-310 and :451-452.

WATCHOUT: two neighbouring `ReportSessionScrub` comments in the same proto look like the same
site and are not. `SessionScrubOutcome`'s own opening comment describes the CLEANUP, which still
runs on every session release, and `ReportSessionScrubResponse` states the `sessionsServed`
increment as a gateway-side effect with no trigger. Both stay true and are named as unedited in
SCHEMA-1 so a later round does not re-open them —
EVIDENCE: schemas/lenny-adapter.proto:436-437 and :468-473.

FACT: the pre-edit claim in SPEC-3's gate paragraph that "no page under `docs/`, `schemas/`, or
`charts/` mirrors" the §12.6 `sessions_served` sentences was false for `schemas/`.
`grep -rn "sessions_served\|sessionsServed" docs/ schemas/ charts/` returns three hits, all in
`schemas/lenny-adapter.proto` (:314, :453, :471), and nothing under `docs/` or `charts/`.

MISTAKE: an earlier round wrote the §12.6 rationale as "the read trigger moves with the write
trigger because the gateway performs both in one step". The one-step grounding is real for the
INCREMENT and says nothing about where the evaluation point is stated, so it licensed a second
statement of a rule §5.2 already owns. The rationale is now narrowed to the write trigger.

FACT: no implementation-checklist step is falsified by this group's edits, checked directly. Step
S4 (SPEC-3) names §5.2 only and never mentions §12.6 or either tier-11 gate, and step S9
(SCHEMA-1) enumerates the proto fields, enums and codes and never mentions the
`ReportSessionScrub` comment replacements at all, so absorbing two more of them changes nothing
there. No DEFERRED is owed for that file from this group.

### [spec.10.fix-design-G1.1]
DECISION: Narrow SPEC-5's §6.2 client-visibility replacement clause to "a failure of the setup-command request at `/start` takes the envelope the `SETUP_COMMAND_FAILED` row of [§15.1](...) states", and re-word the two summary sentences from "the single home of the setup-window mapping" to "the single home of the mapping for the setup-command request" — BECAUSE SPEC-5's own exclusion replacement re-keys the §15.1 row onto the setup-command request, so the row is total only over that request; the pointer and the summary must name the scope the row now carries. — ALTERNATIVES: (a) re-widen the §15.1 exclusion back to the whole setup window — rejected, it reinstates the sweep the re-key exists to prevent (spec-changes.md:1174-1176); (b) add a second §6.2 sentence restating the `STARTING_FAILED` fallback for other setup-window failures — rejected as a second statement of a mapping whose home is §15.1's own `STARTING_FAILED` row (spec/15_external-api-surface.md:1132), and the shipped §6.2 bullet's preceding clause already routes a runtime-launch failure there.
FACT: the staged rationale paragraph already uses the correct narrow phrase ("the row is the single home of the mapping for the setup-command request") — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:1186-1187. The summary's two "setup-window mapping" sentences are the stale ones, not the rationale.
WATCHOUT: review-log.md:984's DEFERRED entry repeats the same overbroad phrase. It is an audit record of what an earlier round wrote and is not corrected here — EVIDENCE: proposals/0081_.../0081_....review-log.md:984
DEFERRED [proposals/0081_.../0081_....implementation-checklist.md]: S1 (line 17) still describes §6.2's clause as "re-keyed the same way". False twice over: SPEC-5 replaces it with a pointer, and after this fix the pointer is scoped to the setup-command request. Already carried by the review-log DEFERRED at :984; this round adds the narrowed scope to what the checklist must say.


### [spec.10.fix-design-G2.1]

DECISION: the §15.4 conformance criterion is fixed by DELETING the answer enumeration from both of its sentences and adding ONE scope sentence to the lead-in, rather than by the reviewer's quantifier ("for each rule that fixes one ..."). The lead-in loses "together with the gRPC status, the `ErrorCode` and the `Shutdown` outcome each rule answers on, except in two places: ..." and gains a sentence saying what §4.7.1's rules govern (refusal or admission, the slot registry, the record of which sessions the pod's shared runtime process holds, and what the adapter answers) and what is outside them (the work a request performs for its own sake, which §4.7 states per RPC). The criterion's third clause becomes "answers as it states". BECAUSE "answers as it states" is vacuous on rules 4 and 7, which fix no answer, and exact on rules 1, 3, 5, 6, 8, 10 and 15, so no quantifier is needed; and the scope sentence bounds "the acts it states and no others" to the registry, which is the only reason that clause was false. ALTERNATIVES: the reviewer's literal wording (keeps the status/`ErrorCode`/outcome triple, which is a miniature restatement of §4.7.1's answer contract, and keeps a two-exception clause that grows per rule); deleting the criterion (already rejected in standing context, and CONF-1 derives from it); giving rules 1, 3, 8, 10 an `ErrorCode` so the universal becomes true (invents wire surface, and rule 1's answer is a bare `codes.InvalidArgument` from `validateBindFields` that must not route through `slotResolveError`).

FACT: the two "exceptions" the deleted clause named were never exceptions. Rule 2 already cites §15.4 for its status in its own text (spec-changes.md:1035), and rule 15 is inside §4.7.1, so "rules 11 through 14 take their status from rule 15" is not an exception to "§4.7.1 states the statuses" at all. EVIDENCE: proposals/0081_.../spec-changes.md:1035, :1057

FACT: the same false universal is written at three sites, not one. spec-changes.md:1091 (the staged spec text), summary.md:952 ("each rule stating the gRPC status, the `ErrorCode` and the `Shutdown` outcome it answers on"), and non-spec-changes.md:2028 (CONF-1 "states, per numbered rule, the gRPC status, the `ErrorCode` and the `Shutdown` outcome an adapter answers on"). A fix that edits only the spec text leaves two copies of the defect. EVIDENCE: proposals/0081_.../summary.md:952, non-spec-changes.md:2028

FACT: §4.7 is where a request's own work is stated (the RPC table rows: `PrepareWorkspace` "Accept streamed files into staging area", `RunSetup` "Execute bounded setup commands", `AssignCredentials` "Push a per-provider credential map"). §15.4's own preamble states none of it, so the scope sentence must cite §4.7 rather than "elsewhere in this section". EVIDENCE: spec/04_system-components.md:669,671,681; spec/15_external-api-surface.md:1458-1469

WATCHOUT: implementation-checklist.md:17 says §15.4 is re-cut to "state the three non-conformances that are not any single rule's condition: a wrong evaluation order, a separable step, and a gRPC error for an outcome the reclaim-outcome rule reports". The staged §15.4 states no such three, and under this design it deliberately never will: all three are reached through the criterion's citation of §4.7.1 (the ordered cascade, the registry critical-section paragraph, rule 15). Correct the checklist clause rather than adding the three to §15.4. EVIDENCE: proposals/0081_.../implementation-checklist.md:17

WATCHOUT: do not touch the slot-identifier reclaim-hold block (spec-changes.md:1097). It is the single home of rule 2's `ABORTED` status and rule 2 points at it; editing it to absorb the deleted exception clause re-opens the drift this design closes. EVIDENCE: proposals/0081_.../spec-changes.md:1035,1097

UNVERIFIED: whether "the record of which sessions the pod's shared runtime process holds" is the right scope vocabulary for the criterion, or whether §4.7.1 rule 8's start confirmation should be folded under "the slot registry" wholesale. Rule 8's acts are inside the registry critical-section paragraph, so a single "slot registry" scope may be adequate and shorter. A later spec lens should check the standing-context entry on `runtimeLive` being a pod-level cohort before collapsing the two.

### [spec.10.fix-design-G3.1]

DECISION: §12.6's `sessions_served` READ clause (prose and DDL comment) is reduced to a pointer at §5.2's `**Session count limit:**` bullet; the WRITE trigger stays re-keyed onto the cleanup-outcome report, because §12.6 owns the column's write semantics. — BECAUSE the concurrent-pool evaluation point is one rule with three statements (spec/05:488, spec/06:139-142, spec/12:481+:494); re-keying only the §12.6 pair leaves two vocabularies for one rule. — ALTERNATIVES: (a) move the evaluation point in §5.2's bullet and pointer-ize §6.2 and §12.6 — rejected, it drags in spec/06's fence, docs/reference/state-machines.md and three of the four checks in `TestPerReleaseSessionCountDrainAgrees_F5231`, and §5.2's "the session release that drives the served-session count to `maxSessionsPerPod`" stays true when the count only moves on a report, so nothing forces it; (b) leave the staged re-key — rejected, that is the drift; (c) delete the read clause with no pointer — rejected, the column's reader loses where it is evaluated.

DECISION: the tier-11 gate plan in the same paragraph changes from "re-key the spec/12 substring pair" to "DELETE the spec/12 substring block from `TestPerReleaseSessionCountDrainAgrees_F5231`". — BECAUSE after the reduction §12.6 states no evaluation point at all, so there is nothing for that gate to compare; its §5.2, §6.2 and state-machines.md checks are untouched. — ALTERNATIVES: widening the gate's accepted substrings to match a pointer sentence — rejected, it would assert that a citation is a statement of the rule.

FACT: the §12.6 read clause is the ONLY site matching either of the gate's two accepted substrings; the DDL comment writes `non-vm-restart` without backticks and matches neither. — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-177, spec/12_storage-architecture.md:481,:494

FACT: the second gate, `TestSection28RegisterWritersMatchTheSpec_spec_28_3`, reads the WRITE clause byte-exactly and ends at it, so neither the read-clause reduction nor the DDL-comment change reaches it; its staged re-key stands unmodified. — EVIDENCE: proposals/0081_*/...spec-changes.md:820-827

FACT: the gate handling for both tier-11 gates is stated ONLY in the SPEC-3 §12.6 commentary paragraph; neither gate file appears in non-spec-changes.md's deliverables or its "Files touched on application (non-spec)" list. A fix to the gate plan therefore lands in spec-changes.md and nowhere else. — EVIDENCE: proposals/0081_*/...spec-changes.md:811,:821; ...non-spec-changes.md:3803-3805

DECISION: SPEC-3's closing mirror claim is corrected rather than deleted, and it CITES SCHEMA-1 for the proto edit instead of describing it. — BECAUSE deleting it loses the implementer's instruction that a mirror exists, and describing the edit there would give the proto work a second home.

DECISION: SCHEMA-1's `**The scrub-outcome comments.**` block absorbs two further replacements, the report-trigger sentences at schemas/lenny-adapter.proto:308-310 and :451-452, in DOCS-2's voice ("for the cleanups §5.2 states the adapter reports, and for no other release"). — BECAUSE both assert the withdrawn universal report trigger and both sit on the same RPC surface; correcting one and leaving its sibling is the drift. — ALTERNATIVES: a new SCHEMA-2 deliverable — rejected, same file, same surface, same commit.

WATCHOUT: `schemas/lenny-adapter.proto:437` ("the per-slot cleanup the adapter runs on every session release") describes the CLEANUP, not the report, and stays true; do not edit it. The :308-310 sentence is the same construction but its trailing "across the `maxConcurrentSessions > 1` and recycling cases alike" makes it a claim about the report's scope, which is why it moves. — EVIDENCE: schemas/lenny-adapter.proto:308-310,:437,:451-452

FACT: `sessions_served`/`sessionsServed` appears nowhere under docs/ or charts/; all three hits are schemas/lenny-adapter.proto:314,:453,:471, and :471 states the increment as a gateway-side effect with no trigger, so it stays true.

WATCHOUT: non-spec-changes.md:3598 counts "the two scrub-outcome comment replacements" and summary.md:954 names them with the RELEASED-implication qualifier. Both go stale when the two trigger replacements land; the count is dropped rather than raised.

### [spec.10.review-applicability.1]

FACT: every "reads, verbatim" anchor in spec-changes.md was checked byte-for-byte against the
tree this round and ALL of them match. Verified: §4.1:157, §4.7 `Shutdown` row:686 (exact
prefix), §4.7 `DemoteSDK` row:674, §4.7 `ReportSessionScrub` row:692, §4.7.1 insertion point
(Adapter→Gateway table closes :693, `#### 4.7.2` at :695), §4.6.1:409/415/416, §4.7.9 step
5:854, §5.2:453/545(three sentences)/561, §6.2:80/95-97/148/152-155/234/290, §7.1:23 (inside
the 5-54 code fence, as the shipped atomicity paragraph already is), §7.2:210/213/214,
§7.3:414, §12.6:481(two clauses)/494, §15.1:1136 (four sentences), §15.4 insertion point
(:1469 SDK-warm paragraph, :1471 `#### 15.4.1`), §16.1 two-column table :14-15, §29.4 step
13 :704-711. Do not re-derive these; re-check only an anchor whose staged text a later round
edits — EVIDENCE: spec/04_system-components.md:686, spec/05_runtime-registry-and-pool-model.md:545

FACT: every cross-reference anchor the staged blocks introduce resolves to a live heading:
`#471-role-and-gateway-rpc-contract`, `#49-credential-leasing-service`, `#101-horizontal-scaling`,
`#154-runtime-adapter-specification`, `#1542-rpc-lifecycle-state-machine`, `#74-upload-safety`,
`#479-startup-sequence-for-type-agent-runtimes`, `#461-warm-pool-controller-pod-lifecycle`,
`#52-pool-configuration-and-execution-modes`, `#62-pod-state-machine`, `#151-rest-api`,
`#71-normal-flow`, `#73-retry-and-resume` — EVIDENCE: spec/04_system-components.md:659, spec/10_gateway-internals.md:3

FACT: the §5.2 append cannot redirect a shipped `lineContaining`/`requireLine` gate. The four
§5.2-reading tier-11 gates anchor on "Whole-pod replacement trigger", "Session count limit",
"**Slot (session mode).**", "A service-mode slot is a different thing",
"**Fresh-guest reprovision:**" and "the pod is held for its tenant through the claim's
`reserved` state"; none of those strings occurs in the staged append, and `grep -rn '"Slot
cleanup' tests/ --include=*.go` and `'"Scrub model'` both return nothing, so the append's own
"**Slot cleanup:**" mention shadows no gate — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:92

FACT: the two §16.1 rows SPEC-6 adds break no gate on their own.
`tests/tier11_docs/adapter_metric_catalog_test.go` is driven from the names
`pkg/adapter/metrics.go` registers, and `spec161Metrics` in
`pkg/observability/metrics/catalog_test.go` is a hand-held list reconciled against
`MetricCatalog()`, not against the §16.1 text. So S6 (tiers 0, 11) is green with no code
landed — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:80

FACT: the proposal's account of the two §12.6 gates is exact.
`podStateGatewayWrittenSentence` ends at "...(`ReportPodScrub`) respectively" and is checked
with `requireAllContain` against the whole §12 page, and
`TestPerReleaseSessionCountDrainAgrees_F5231` accepts either of the two §12 substrings the
proposal quotes. Both go red on SPEC-3 and both remedies are test edits — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99, tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173

MISTAKE nearly filed, twice, and both are refuted: (1) the SPEC-4 §6.2 fence edit at
spec-changes.md:853 gives only ONE block after "replace the trigger list of the `claimed ──→
draining` entry:" with no "reads, verbatim" pair. It is NOT an unresolvable anchor: the
`Occupancy projection` fence holds exactly one `claimed ──→ draining` entry (the other four in
spec/06 are in the Recycle-edges and Concurrent-occupancy fences), and SPEC-4's §4.6.1 bullet
replacements at spec-changes.md:909 and :916 use the identical "anchor named inline, one block
= the replacement" form. (2) §5.2's "Session count limit" bullet
(spec/05_runtime-registry-and-pool-model.md:488) and §6.2's concurrent fence entry
(spec/06_warm-pod-model.md:139) both key the retirement on "a session release" and are NOT
falsified by SPEC-3's report biconditional, because each names "the session release THAT
DRIVES the served-session count", which self-qualifies; §12.6's clause needed the re-key only
because it said "on EACH session release" — EVIDENCE: spec/06_warm-pod-model.md:95, spec/05_runtime-registry-and-pool-model.md:488

OPEN: does the §5.2 disposition table owe a row for a RUNTIME-GIVEN slot no cleanup reclaims?
Row 9's key is "Pre-`running` slot no cleanup reclaims", and the Edge-cases bullet on an
abandoned attempt's late `StartSession` records a pod "left holding an entry and a runtime
session no gateway attempt owns" — a started slot that no per-slot cleanup reaches until an
unconditional teardown, a demotion, the §10.1 hold-timeout, or pod retirement runs. If the pod
retires first, no row's key column covers it. I did not file it: the table is scoped to the
disposition of each per-slot CLEANUP, and it is arguable that a case with no cleanup owes no
row beyond the one row 9 already carries for the pre-`running` analogue. The single-source or
completeness lens should settle it, not the applicability lens — EVIDENCE: 0081_..._.spec-changes.md:624, 0081_..._.spec-changes.md:176

USEFUL [Standing context, "**The §5.2 append can silently redirect four tier-11 gates...**"]:
it named the exact hazard class and let me discharge it with two greps instead of reading the
gate corpus.
USEFUL [Standing context, "**MISTAKE: checking proto field-number FREENESS and stopping
there.**"]: pointed me at `requireLine`/`lineContaining` first-match semantics, which is what
makes the §5.2 append hazard checkable at all.

### [spec.10.review-citations.1]
DECISION: returned an EMPTY findings list for the citation lens on spec round 10 — BECAUSE every verbatim "reads, verbatim" block in spec-changes.md matched the tree byte-for-byte, and every attributed behaviour (code, test gate, spec section) checked out — ALTERNATIVES: filing two loose-summary wordings (see WATCHOUTs below), rejected as below the bar and explicitly covered by "if unsure, do not report".
FACT: the twenty-odd verbatim anchor quotes in spec-changes.md were re-verified this round and ALL matched. Do not re-derive them; the sites are spec/04:157 (§4.1 third sentence), spec/04:686 (`Shutdown` row), spec/04:674 (`DemoteSDK` row), spec/04:692 (`ReportSessionScrub` row), spec/04:854 (§4.7.9 step 5), spec/04:409/415/416 (§4.6.1 projection sentence + two bullets), spec/05:453 (`**Scrub model.**`), spec/05:545 (`**Slot cleanup:**` action list, reporting sentence, leaked-outcome sentence), spec/05:561 (`**Whole-pod replacement trigger:**` parenthetical), spec/06:80/95/148/152-155/234/290, spec/07:23/210/213/214/414, spec/12:481/494, spec/15:1136 (all four `SETUP_COMMAND_FAILED` sentences), spec/29:704-711 (step 13 tail). — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec/15_external-api-surface.md:1136
FACT: every anchor link the staging writes resolves to a real heading. Verified: §4.1/40, §4.6.1/338, §4.6.3/606, §4.7/657, §4.7.1/659, §4.7.9/848, §4.9/1099 (spec/04); §5.2/365; §6.1/3, §6.2/78; §7.1/3, §7.2/115, §7.3/378, §7.4/438; §10.1/3; §12.6/369; §15.1/614, §15.4/1458, §15.4.2/1686, §15.4.6/2038; §16.1/3; §29.4/575. — EVIDENCE: spec/15_external-api-surface.md:1686
FACT: the three tier-11 gates SPEC-3 names read exactly what the staging says they read. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` pulls the §4.7 row with `lineContaining` and requires spec and adapter-contract.md to share `"The request is session-scoped: it is "`+rule+`"."`; `TestPerReleaseSessionCountDrainAgrees_F5231` requires ONE of two §12 substrings (the DDL comment matches neither, because it writes `non-vm-restart` unbackticked); `podStateGatewayWrittenSentence` ends at the write clause and is byte-exact. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:67; tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:173; tests/tier11_docs/spec_28_register_writers_test.go:99
FACT: `grep -rln sessions_served tests/ docs/ schemas/ charts/` returns six files, and only the two gates SPEC-3 names read the §12.6 sentences (spec_28_index_rows_test.go uses `28.5 sessions_served` as a synthetic slugify fixture). SPEC-3's "these two are the only assertions" claim is TRUE. — EVIDENCE: tests/tier11_docs/spec_28_index_rows_test.go:778
FACT: spec/05:455 (the recycle-lifecycle paragraph) independently confirms BOTH halves of SPEC-3's credential-action rationale: the scrub-step-0 purge of `/run/lenny/slots/{sessionId}/credentials.json`, and that `cleanupCommands` run "after every ended session's per-slot tree and credential lease have been removed". Do not re-derive. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:455
WATCHOUT: SPEC-4's rationale says `ProjectOccupancyPhase` "switches on the pod's current phase alone". Read literally that is false — the function branches on `o.HasClaim` and `o.Binding` first and reaches the current-phase switch only on the no-claim arm. The sentence's own colon-clause ("`state.Reserved` with no claim projects `Idle` and `state.Claimed` with no claim projects `Draining`") scopes it to the claim-deletion half, which is the half SPEC-4 edits, so it is a loose summary rather than a defect. Judged below the bar twice now; do not file it a third time without showing a rule that depends on the wide reading. — EVIDENCE: pkg/controller/warmpool/occupancy.go:84-143
WATCHOUT: the SPEC-5 §15.4 rationale says the `ABSENT` claim-register row follows "the rows the `coordination_generation` fence already carries". Every coordination_generation row in `tests/claim-map.json` is `UNWIRED`, not `ABSENT`. The sentence does not actually assert their status, and §28.4's own definitions ("ABSENT means it is specified and not implemented") make `ABSENT` the right status for an unbuilt third-party harness, so this is below the bar. Do not file unless the sentence is reworded to claim an `ABSENT` precedent. — EVIDENCE: spec/28_communication-channels.md:163
USEFUL [Standing context, "The occupancy projection reads the pod's CURRENT PHASE, never the recycle setting"]: saved a full re-derivation of SPEC-4; the tree confirms it at pkg/controller/warmpool/occupancy.go:128-143 and the function's own doc comment carries the exact sentence SPEC-4 quotes ("the §6.2 state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at the claim DELETE", occupancy.go:57-59).
USEFUL [Traps, "The glob `*spec-changes.md` ... matches BOTH"]: used full filenames throughout; the spec-changes.md line numbers in this shard are from the `.spec-changes.md` file alone.

### [spec.10.review-client-surface.1]
FACT: `pkg/gateway/openapi/openapi.json` does NOT exist; the hand-authored OpenAPI document is at `pkg/gateway/externalapi/openapi/openapi.json`, and it contains NO occurrence of `SETUP_COMMAND_FAILED`, `setup_command_failed` or `setup-output`. SPEC-5's §15.1 row rewrite therefore owes no OpenAPI mirror — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json (grep for the three strings returns nothing)
FACT: no `sdks/**` file mentions `SETUP_COMMAND_FAILED`, `receiving_uploads` or `slot_cleanup`, so no language SDK mirrors the §15.1 row or the §6.2 per-slot fence. The only reader-facing mirrors of the edited surfaces are docs/reference/{error-catalog,state-machines,adapter-contract,metrics}.md and schemas/lenny-adapter.proto, all four already named in spec-changes.md's closing "Each reader-facing reference page ..." paragraph — EVIDENCE: proposals/0081_*/0081_*.spec-changes.md:1300-1320
FACT: `ShutdownResponse` today is exactly `{bool exited_cleanly = 1; int32 exit_code = 2;}` with no comment, and `exited_cleanly` appears NOWHERE in `spec/`. The §5.2 disposition table's "Clean-exit flag on the `Shutdown` response" column and rule 15's clean-exit clause are the corpus's first definition of it, so they contradict no existing text. Do not file a redefinition finding — EVIDENCE: schemas/lenny-adapter.proto:1665-1668; `grep -rn exited_cleanly spec/` is empty
FACT: `Error.ErrorCode` ends at `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27` and `Error.Category` carries `CATEGORY_TRANSIENT`/`CATEGORY_PERMANENT`, so rules 5 and 6 name values that exist and 28/29 are free. The §15.1-precedent claim for `PROTOCOL_VERSION_INCOMPATIBLE` checks out: it is published in §15.4.2's `INIT` row and has no §15.1 catalog row — EVIDENCE: schemas/lenny-adapter.proto:554-588; spec/15_external-api-surface.md:1699
FACT: every cross-file anchor in the staged spec blocks resolves. Verified headings: spec/04 `#### 4.6.1`, `### 4.7`, `#### 4.7.1`, `#### 4.7.9`, `### 4.9`; spec/05 `### 5.2`; spec/06 `### 6.1`, `### 6.2`; spec/07 `### 7.1`, `### 7.3`, `### 7.4`; spec/10 `### 10.1`; spec/15 `### 15.1`, `### 15.4`, `#### 15.4.2`. The §7.2 "Mid-resume terminal transitions" block the SPEC-2 heading names really does sit inside `### 7.2 Interactive Session Model` — EVIDENCE: spec/07_session-lifecycle.md:115,210
FACT: `Shutdown`, `ReportSessionScrub` and `DemoteSDK` appear nowhere in spec/28 and no §29 step enumerates the `ShutdownRequest` field set (§29.4 step 12 names "the pod identity and the whole-pod scrub parameters" in prose only), so the "§28's registers" and "step 12 untouched" claims hold under the client-surface lens — EVIDENCE: spec/29_communication-scenarios.md:696
WATCHOUT: `docs/api/internal.md` still documents this RPC as `StopSession`/`StopSessionRequest`/`StopSessionResponse` with a `clean_exit` field. That page is pre-existing drift from `schemas/lenny-adapter.proto`, not damage this proposal does; do not file it as a missed mirror for SCHEMA-1 — EVIDENCE: docs/api/internal.md:145-158
UNVERIFIED: whether §16.1.1's attribute table (the "Used by" column for `k8s_pod_name` and `pool`) needs the two SPEC-6 series added. The column is prose-descriptive ("slot failure and replacement metrics"), and I found no tier-11 gate reading it. Whoever owns the metrics lane should check for one — EVIDENCE: spec/16_observability.md:297

### [spec.10.review-docs-alignment.1]

FACT: the §12.6 `sessions_served` write trigger HAS a mirror outside spec/, and it is in `schemas/`, not in `docs/`. `schemas/lenny-adapter.proto:451-453` reads "ReportSessionScrubRequest carries the §5.2 per-slot cleanup outcome the adapter reports on every session release. pod_id is the agent_pod_state row key the gateway increments sessionsServed on". That is §12.6's proposition (agent_pod_state row, sessionsServed, keyed on the release) in the proto. spec-changes.md's SPEC-3 §12.6 commentary asserts the opposite. — EVIDENCE: schemas/lenny-adapter.proto:451-453; spec-changes.md line 828-830.

FACT: `grep -rn "sessions_served\|sessionsServed" docs/ schemas/ charts/` returns THREE hits, all in `schemas/lenny-adapter.proto` (:314, :453, :471), and nothing under docs/ or charts/. The docs half of the spec file's claim is true; the schemas half is not. — EVIDENCE: schemas/lenny-adapter.proto:314,453,471.

FACT: SCHEMA-1's two staged `ReportSessionScrub` comment replacements cover only the two "RELEASED implies the acts were clean" sentences (the RPC comment's outcome sentence and the `SESSION_SCRUB_OUTCOME_RELEASED` comment). Neither touches `:451-452` "the adapter reports on every session release", so that sentence is an unstaged site under SPEC-3's biconditional regardless of how the spec-file claim is worded. — EVIDENCE: non-spec-changes.md §SCHEMA-1 "**The scrub-outcome comments.**" block; schemas/lenny-adapter.proto:451-452.

FACT (refuted, do not re-file): the "on a session release" wording at spec/05:488, spec/06:139, spec/16:12 and docs/reference/state-machines.md:249 is NOT falsified by SPEC-3. Each is keyed on the served-session count *reaching* `maxSessionsPerPod` on a release; a release that files no report never drives the count, so the sentence stays true. Only §12.6's unconditional "incremented at each session release" is a universal, and SPEC-3 already replaces it. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488; spec/06_warm-pod-model.md:139; spec/16_observability.md:12; docs/reference/state-machines.md:249.

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` reconciles adapter metrics by NAME presence only, against `docs/reference/metrics.md` and `spec/16_observability.md`. It compares no row wording, so a §16.1 row whose deferral clause differs from metrics.md's phrasing breaks nothing. — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:80-115.

MISTAKE (nearly filed, twice): SPEC-6's untokened-entry §16.1 row says "Adapter-side; not scraped until the adapter metrics endpoint is wired", while non-spec-changes.md's CODE-9 says SPEC-6 "carries the deferral wording verbatim" and the shipped sibling rows say "emitted by the adapter process inside the agent pod and therefore outside the [§16.9] default scrape target set until a deployer wires an adapter scrape target". The two do not match. Judged BELOW the bar: the staged row is not wrong, only shorter, and the gate reads names alone. A later lens that wants this should file it as the CODE-9 claim rather than as the spec row. — EVIDENCE: spec-changes.md line 1217; spec/16_observability.md:186-189.

MISTAKE (nearly filed): spec-changes.md's Edge-case bullet on the bind-sequence refusal says DOCS-3 "makes one replacement §15.1 does not need, because the page states the retryable-fallback exclusion keyed on the cause class where §15.1 keys it on the gRPC code." The extra replacement is actually the REMEDY CELL (§15.1's catalog table has no remedy column, verified at spec/15_external-api-surface.md:1136), and DOCS-3's own reason for it is that the clause must land inside the cell's terminating period. The stated reason describes the fourth mirrored replacement instead. Judged below the bar as a rationale slip that falsifies no applied text. — EVIDENCE: spec-changes.md line 239-241; non-spec-changes.md DOCS-3 remedy-cell paragraph; spec/15_external-api-surface.md:1136.

DEFERRED [docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33]: both still end a sentence "and the adapter reports its outcome to the gateway", the universal SPEC-3's `**Scrub model.**` biconditional withdraws. No DOCS deliverable opens either page. Re-verified this round at those exact lines; this is the same DEFERRED the standing context already carries, unchanged.

DEFERRED [docs/reference/state-machines.md:249]: the `claimed → draining` row reads "Served-session count reaches `recycle.maxSessionsPerPod` on a session release. The gateway stamps the drain request per release". The second clause is a universal over releases and SPEC-3 makes the stamp follow the cleanup-outcome report, so "per release" is now loose. DOCS-1's staged edit list (per spec-changes.md line 1303-1310) covers the per-slot sub-state row, the two `slot_cleanup` triggers and the pod-state-machine claim-deletion paragraph, and not this row. Lower confidence than the two pages above, because the first clause stays true.

USEFUL [Standing context, "**DECISION: the §4.7 `ReportSessionScrub` row's trigger clause is re-keyed into a citation of §5.2 ...**"]: it named the §4.7 docs mirror and the one-physical-line gate, which is what let me check `docs/reference/adapter-contract.md:64,75,81` against DOCS-2 in one pass and clear all three rows.

### [spec.10.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens on the spec lane — BECAUSE every
identifier the staging adds, changes or removes was grepped across spec/, docs/, schemas/ and charts/
and every spec-side surface it touches is either in an edit list or survives the edit unchanged —
ALTERNATIVES: rejected filing §16.1's `lenny_gateway_pod_retirement_total` "per-release
`maxSessionsPerPod` drain" clause (spec/16_observability.md:12) as falsified by the §12.6 re-key; it
is shorthand for §5.2's own trigger sentence, which survives (see FACT below). Also rejected filing
spec/07_session-lifecycle.md:72's `scrubPolicy` parenthetical ("workspace removal, process-group kill,
and scratch directory cleanup") as a second copy of the §5.2 `**Slot cleanup:**` action list the
staging extends: it is a summary that already cites §5.2, and it was incomplete against the tree
before this proposal.

FACT: the §12.6 re-key from "session release" to "cleanup-outcome report" does NOT falsify the two
other spec sentences keyed on the release, and a later round should not re-open them.
spec/05_runtime-registry-and-pool-model.md:488 says the concurrent drain fires "on the session release
that drives the served-session count to `maxSessionsPerPod`" — a restrictive relative clause, so it
picks out exactly the release whose cleanup reported, and stays true. spec/16_observability.md:12's
"per-release … drain" is shorthand for that same trigger. What §12.6 carried, and what made it false,
was the universal "on each session release". — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:488,
spec/16_observability.md:12, spec/12_storage-architecture.md:481

FACT: §28.4's claim register is a file (`tests/claim-map.json`), not spec prose, so the `ABSENT` row the
staged §15.4 block promises for the missing third-party-adapter harness needs no spec/28 edit and its
absence from "Spec files touched" is correct. — EVIDENCE: spec/28_communication-channels.md:161-165

FACT: no channel card on the `gateway-to-pod` boundary carries the bind-sequence RPCs. §28.3's channel
register lists only CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER and CH-PODHEALTH there, so
`Shutdown`, `PrepareWorkspace`, `DemoteSDK` and the rest have no §28.5.1 card whose Exclusivity or
Degradation field the registry critical section or the reclaim hold could falsify. The staging's
"§28's registers untouched" entry holds for that reason, not only the per-field one it gives.
— EVIDENCE: spec/28_communication-channels.md:117-131, spec/28_communication-channels.md:205-219

FACT: every verbatim anchor the spec staging quotes resolves in the current tree, checked one by one:
§4.1 at spec/04_system-components.md:157; §4.7 `Shutdown` row at :688 and `DemoteSDK` row at :673;
§4.7 `ReportSessionScrub` row at :692; §4.7.9 step 5 at :854; §4.6.1 bullets at :415-416 and the input
sentence at :409; §5.2 scrub model at spec/05:453, `**Slot cleanup:**` at :545, replacement trigger at
:561; §12.6 prose at spec/12:481 and DDL at :494; §6.2 fence at spec/06:95-97, :148, :150-155,
projection prose at :80, mid-resume cancel bullet at :234, client-visibility clause at :290; §7.1
atomicity at spec/07:23, §7.2 preamble at :210 and steps at :213-214, §7.3 list item at :414; §15.1
`SETUP_COMMAND_FAILED` row at spec/15:1136; §15.4 demotion contract at :1469; §29.4 step 13 at
spec/29:704-711. A later round need not re-verify these unless the tree moves.

FACT: the two tier-11 gates the §12.6 deliverable names are the only test assertions on those
sentences; grepping tests/ and scripts/ for the other replaced strings ("reported by the adapter via",
"reports each slot cleanup outcome", "If cleanup fails, the slot is leaked", "cleanup timeout
exceeded", "slot workspace removed, processes killed", "claim is deleted on a recycling pod",
"no runtime was started on it") returns nothing, so no further gate re-key is owed by the spec lane.
— EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go,
tests/tier11_docs/spec_28_register_writers_test.go

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` gates a registered adapter metric against BOTH
docs/reference/metrics.md and the §16.1 catalog, with `specCatalogPending` currently empty, so SPEC-6
must land before the adapter counter is registered in pkg/adapter/metrics.go. The §16.1 table also
carries bold section-header rows ("**Coordination & Reconciliation**") under which the other
adapter-emitted counters sit; the staging places both new rows beside the slot-failure counter
instead, which is placement rather than a defect. — EVIDENCE:
tests/tier11_docs/adapter_metric_catalog_test.go:43-48, spec/16_observability.md:178-189

FACT: §16.1.1's attribute table calls itself the single source of truth for label names and its
"Used on" column enumerates where each label appears, but nothing under tests/ or scripts/ reads that
column, so the two new rows' `pool` and `k8s_pod_name` labels need no edit there.
— EVIDENCE: spec/16_observability.md:294-297

USEFUL [the metric-gate topology, settled and re-derived by six shards]: saved re-deriving the
SPEC-6-before-CODE-9 ordering and the adapter-versus-gateway gate split.

OPEN: the §6.2 projection sentence at spec/06_warm-pod-model.md:80 still enumerates the projection's
inputs as claim existence, binding state and disposition, and `sessionPolicy`, while SPEC-4 adds "the
phase the pod currently projects" to §4.6.1's enumeration of the same inputs. The staging defends
leaving it ("no surviving §6.2 clause reads the phase the pod currently projects"), and the colon does
make it a lead-in to the clauses §6.2 keeps, so this round did not file it. If a later round wants it
closed, the remedy is a reduction: end that clause at the §4.6.1 citation rather than adding the
fourth input in a second place.

### [spec.10.review-fresh.1]

DECISION: returned an EMPTY findings list for the fresh-holistic lens on spec round 10 — BECAUSE every claim I independently spot-checked against the tree held, the rule topology is genuinely reduced to the three named homes, and the two candidates I developed far enough to write up were each already adjudicated below the bar by earlier rounds (see below) — ALTERNATIVES: filing the SPEC-4 fence-anchor form and the "started slot no cleanup reclaims" table gap; both rejected, with reasons.

FACT: the seven RPCs §4.7.1's Admission paragraph enumerates as "every request on this contract that can create a slot registry entry" are exactly the seven that reach `ensureSlotStateLocked` in the tree, verified caller by caller: `PrepareWorkspace`/`FinalizeWorkspace`/`RunSetup` through `ensureSlotPaths` (pkg/adapter/staging.go:134,181,337 → pkg/adapter/slot.go:143), `AssignCredentials` through `assignCredentialsSlot` (pkg/adapter/slotcreds.go:26), and `StartSession`/`Resume`/`ConfigureWorkspace` through `claimSessionSlot` (pkg/adapter/session.go:111, resume.go:50, sdkwarm.go:217 → slotsession.go:75). No other non-test caller exists. A later round need not re-derive this list.

FACT: the disposition table's "released outside a `Shutdown`" partition ("an act fails **after** the deregistration") holds for all three named performers, because each deregisters first: `releaseSessionSlot` calls `deregisterSlot` then `removeSlotTree` (pkg/adapter/slotsession.go:214-219); `onHoldTimeout` runs pass 1 `deregisterStartedSessions` before pass 2's `terminateHeldSession` (pkg/adapter/holdstate.go:190-205). Only the SDK demotion closes first, which is why it has its own row.

FACT: the §5.2 action-list additions are shipped behaviour exactly as the rationale claims. `slotlayout.RemoveTree` removes `p.CredentialsDir` alongside the slot root, sessions and artifacts (pkg/adapter/slotlayout/tree.go:58-68), and `deregisterSlotLocked` cancels every armed per-provider expiry timer before deleting the entry (pkg/adapter/slotsession.go:174-188).

FACT: the §4.7 `Shutdown` row's new graceful-signal condition and its ordering are a faithful record of `Server.Shutdown`: `emitFinalUsage` → `drainViaLifecycle` gated on `!boundRemains` → `Runtime.Close` → `removeSlotTree` → `reportSessionScrub`, with `ExitedCleanly: closeErr == nil` (pkg/adapter/session.go:238-290). The row's "flushes the final usage report and then closes the runtime" and "goes out only when the deregistration leaves the adapter holding no bound entry" both match, and the shipped comment at :251-258 carries nearly the row's own words.

FACT: §28 needs no edit from this proposal for a second, independent reason beyond the per-field one the staging gives: `§28.4`'s claim register is the file `tests/claim-map.json` rather than spec prose (spec/28_communication-channels.md:161-165), so the `ABSENT` row the staged §15.4 rationale promises is a non-spec deliverable (SCHEMA-1) and its absence from "Spec files touched" is correct.

FACT: `ShutdownRequest` already carries `coordination_generation` (field 6) and the `recycle` comment the SPEC-1 rationale quotes, verbatim, at schemas/lenny-adapter.proto:1621-1622; `ShutdownResponse.exited_cleanly` is field 1 at :1665. `PrepareWorkspaceRequest` is a flat message with `session_id` on every frame and no `oneof` (:682-696), so §4.1's stream-envelope paragraph (spec/04:153) does not bite on rule 9, and `FinalizeWorkspaceRequest` already carries `mid_session` — the carriage table's "carried" for Finalize is a record, not an addition.

FACT: the resume path sends no `AssignCredentials` at all (pkg/gateway/podlifecycle/podsession/binder.go:1589-1620 and pkg/gateway/sessionserver/start.go:4005-4040 issue `Resume` alone), so rule 6 cannot refuse a post-`Resume` credential assignment. I raised this as a candidate ordering defect and refuted it; §4.9's own fallback ordering (spec/04_system-components.md:1124, "replacement pod, `AssignCredentials`, and `Resume`") agrees.

MISTAKE (nearly filed, refuted by the log): the SPEC-4 §6.2 `claimed ──→ draining` edit gives one block and no "reads, verbatim" original, unlike the other 20 staged edits, and the block IS the new text (it carries "claim deletion — see §4.6.1" where spec/06_warm-pod-model.md:95-97 carries "claim deleted on a pod with recycle.enabled: false"). Three earlier shards examined this, judged it determinable, and recorded "do not file it" (review-log.md:1926, :2017, :2041); the round-10 single-source shard re-raised it as a watchout and asked an anchor lens to confirm. Both the citation and edit-site lenses returned empty this round, so it stands unfiled deliberately, not by oversight. Do not spend a fourth round on it unless someone shows a misapplication it actually causes.

MISTAKE (nearly filed): "the §5.2 disposition table has no row for a slot the runtime WAS given that no cleanup reclaims" — the abandoned attempt's late untokened `StartSession` (Edge-cases bullet, spec-changes.md:172-190). The table's residue scope is explicitly the pre-`running` case, §6.2's staged paragraph and §7.1 cite it with that scope, and the running orphan is disposed of by rule 13 plus that bullet. Adding a row would break the partition the caller directive protects. The round-10 mechanism shard refuted the same candidate independently.

USEFUL [spec.10.review-single-source.1, spec.10.review-citations.1, spec.10.review-edit-sites.1]: reading the three sibling shards before finishing turned three of my candidates into delta checks and stopped one duplicate filing. On a round this late, read the co-round shards as well as the standing context.

USEFUL [Standing context, "the §5.2 disposition table as the one home of every residue cell"]: with the three homes named, the holistic read reduces to checking that each citing site cites, which is an hour rather than a day.

### [spec.10.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on spec round 10 — BECAUSE the staged spec edits touch exactly one Kubernetes surface (SPEC-4's occupancy-projection re-key) and every idiom the lens tests holds there — ALTERNATIVES: filing the level-collapse race and the `sessionPolicy`-input leftover, both rejected below as below bar.

FACT: SPEC-4's new §4.6.1 parenthetical, "the controller's own last level, which it may read back because it is the sole writer of that field", checks out on three independent grounds. §4.6.3's ownership row reads `Sandbox` `status.*` → WarmPoolController, "Sole writer of phase and conditions" — EVIDENCE: spec/04_system-components.md:618. The gateway is explicitly denied the grant — "The gateway holds no `sandboxes/status` grant and no `patch` or `watch` verb on the `Sandbox` main resource" — EVIDENCE: spec/04_system-components.md:627. And both WarmPoolController reconcilers that write the phase use ONE field manager with no `Force` — EVIDENCE: pkg/controller/warmpool/occupancy.go:268 and pkg/controller/warmpool/pod_reconciler.go:691, both `client.FieldOwner(string(ownership.WarmPoolController))`. A future lens should not re-derive this; the two-managers-racing-one-field dress is dead.

FACT: `ProjectOccupancyPhase` really does take the pod's current phase as an input (`occupancy.Current`), and the no-claim arm switches on it alone: `state.Reserved` → `Idle`, `state.Claimed` → `Draining`, everything else `("", false)` — EVIDENCE: pkg/controller/warmpool/occupancy.go:33-35, :127-141. SPEC-4's claim that the shipped function "never returns `idle` from `claimed` on any pool" is true, so the §4.6.1 closing sentence it deletes is genuinely false text.

UNVERIFIED: a level-collapse race I could not close and did not file. The §5.2 disposition table's last row asserts "A pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection". That holds only if the WarmPoolController observed the `claimed` level before the claim DELETE; the projection reads back its OWN last write, so a claim CREATE → `bound` patch → DELETE that collapses inside one reconcile window leaves `o.Current == Idle`, `o.HasClaim == false`, which falls to the `("", false)` default and leaves the pod idle and unscrubbed holding the failed bind's residue. Whether the window is reachable depends on when the exclusive-pool claim is created relative to the failing bind (/create versus /finalize), which I did not trace. Whoever owns the code lane, or a later K8s pass, should settle it; the remedy if real is probably not a spec edit, which is why it is not a finding here.

MISTAKE nearly filed, twice, and both are below bar — do not spend a round on either.
1. "SPEC-4 keeps `sessionPolicy` in §4.6.1's projection-input enumeration while its own commentary says the projection reads neither the recycle setting nor the retirement limits (spec-changes.md:843), and the `occupancy` struct carries no pool policy at all (pkg/controller/warmpool/occupancy.go:33-45)." Refuted: the surviving `claimed` and `sdk_connecting` bullets still key on preConnect-ness and on `scrubProfile` (`standard`/`in-place`/`vm-restart`), which live under `sessionPolicy`, so the enumeration stays honest for the bullets the edit leaves standing.
2. "SPEC-3 re-keys `sessions_served` onto the cleanup-outcome report but §6.2's concurrent-occupancy `claimed ──→ draining` entry still says the count reaches `maxSessionsPerPod` 'on a session release' (spec/06_warm-pod-model.md:139-140), and no deliverable lists it." Refuted: the entry states an edge trigger rather than an evaluation schedule, and the count can only reach the threshold at a reporting release, so the sentence is over-broad rather than false. §12.6's "evaluated on each session release" was different — it asserted an evaluation per release — and SPEC-3 already re-keys it.

USEFUL [Standing context, "The occupancy projection reads the pod's CURRENT PHASE, never the recycle setting"]: it named the exact function and the two no-claim arms, which let me verify SPEC-4's whole re-key against the tree in one read instead of reconstructing the projection table.
### [spec.10.review-kubernetes.1]

DECISION: returned an EMPTY findings list for the Kubernetes-idiom lens on spec round 10 — BECAUSE the staging keeps the whole new mechanism (bind attempt token, stamp-once, registry critical section, reclaim hold, the two teardowns) in adapter process memory and on the gateway-adapter gRPC channel, and touches no CRD spec, no status subresource, no finalizer, no admission webhook, and no controller reconcile on a synchronous request path — ALTERNATIVES: I considered and rejected filing (i) the read-back of `Sandbox.status.phase` as a projection input, (ii) the drain-request annotation as a command channel, (iii) the §5.2 "held for the life of the pod" hold answered on a retryable status. Each is either shipped behaviour the proposal correctly records, pre-existing, or already an `### Open` item.

FACT: §4.6.3 backs SPEC-4's new parenthetical literally. The `Sandbox` `status.*` row reads "WarmPoolController | Sole writer of phase and conditions", so "the controller's own last level, which it may read back because it is the sole writer of that field" is true as written, even though two reconcilers inside that one controller write the field (the occupancy projection and the Sandbox-to-Pod warm-fill writer) — they share the field manager `lenny-warm-pool-controller`, so no SSA conflict and no ForceOwnership is implied. EVIDENCE: spec/04_system-components.md:618 (ownership row); spec/04_system-components.md:622 ("Never force-conflict"); pkg/controller/warmpool/occupancy.go:48-56 (doc comment naming the ok=false hand-back to the warm-fill writer).

FACT: SPEC-4's three claim-deletion re-keys match `ProjectOccupancyPhase` exactly. `HasClaim` false and `Current == state.Reserved` returns `(Idle, true)`; `Current == state.Claimed` returns `(Draining, true)`; every other phase returns `("", false)`. The recycle setting is read nowhere in the function. EVIDENCE: pkg/controller/warmpool/occupancy.go:126-140. The §4.6.1 verbatim anchors SPEC-4 quotes are byte-accurate at spec/04_system-components.md:409 (input enumeration), :419 (the `idle` hold-expiry bullet) and :420 (the `draining, then terminated` bullet including the false closing sentence SPEC-4 deletes).

FACT: the `DemoteSDK` row's ground is real. §6.1 admits `preConnect: true` only at `maxConcurrentSessions: 1` and the pool controller rejects the combination at validation time, so "the entry it removes is the registry's single entry" holds. EVIDENCE: spec/06_warm-pod-model.md:69 and :75.

WATCHOUT: the SPEC-4 §6.2 fence edit says only "replace the trigger list of the `claimed ──→ draining` entry" and gives ONE code block (the replacement, recognisable by its "see §4.6.1"), with no "reads, verbatim" block. There are FOUR `claimed ──→ draining` entries in that fence; only the scoping phrase "In the fenced `Occupancy projection` block" disambiguates. The intended target is the one whose current trigger says "claim deleted on a pod with recycle.enabled: false". EVIDENCE: spec/06_warm-pod-model.md:95-97 (target), :114, :135, :137, :139 (the three siblings). I judged this below the finding bar because the scoping phrase resolves it; an implementor should still anchor on the `recycle.enabled: false` substring rather than on the edge name.

UNVERIFIED: two further sites state the `maxSessionsPerPod` retirement evaluation as happening "on a session release", which SPEC-3 re-keys onto the cleanup-outcome report at §12.6 and nowhere else: spec/05_runtime-registry-and-pool-model.md:488 ("the pod transitions to `draining` on the session release that drives the served-session count to `maxSessionsPerPod`") and spec/06_warm-pod-model.md:139 ("served-session count reaches recycle.maxSessionsPerPod on a session release"). I did NOT file either, because both are existential over the releases that drive the count rather than universal over releases, so SPEC-3's biconditional shrinks the set without falsifying the sentence — unlike §12.6's "on each session release", which is universal and is the one SPEC-3 re-keys. A later lens that wants to file these owes an argument that the existential reading is unavailable.

### [spec.10.review-mechanism.1]

FACT: there are THREE distinct instants on the start path and the staging uses a different one at each site: (a) admission of the starting RPC (`st.started`, set before `Runtime.Start`) — the §4.7 row's runtime-teardown precondition; (b) the physical handover, `Runtime.Start` returning; (c) the recording, `noteRuntimeStartedLocked` setting `runtimeLive` — the §5.2 report biconditional, the disposition table's row key, and rule 8's `running` boundary. Any sentence about `running` must name (c). The §4.7 row itself names (b) explicitly ("rather than at the moment the session reaches the runtime"), so (b) is live vocabulary in the staging and a clause that uses it as the boundary is a defect, not a synonym — EVIDENCE: spec-changes.md:299, :990, :1041; pkg/adapter/session.go:262-267

FACT: "graceful-shutdown signal" anchored at §15.4.2 is CORRECT and in-tree, not a mis-citation. `drainViaLifecycle`'s own doc comment calls it "the §15.4.2 DRAINING-state graceful-shutdown signal on the CH-RUNTIMEOPS", even though §29.4 step 13 cites §15.4.3/§28.5.3 for the same `terminate` frame. Do not file the §4.7 row's §15.4.2 pointer — EVIDENCE: pkg/adapter/session.go:294-295; spec/15_external-api-surface.md:1702

FACT: every "reads, verbatim" anchor in spec-changes.md was checked against the tree this round and all of them match byte-for-byte: §4.1:157, §4.7 rows :674/:686/:692, §4.6.1:409 and its two claim-deletion bullets, §5.2:453/:545/:561, §6.2:87/:95/:148/:152/:155/:234/:290, §7.1:23, §7.2:210/:213/:214, §7.3:414, §4.7.9:854, §12.6:481/:494, §15.1:1136, §15.4:1469/:1471, §29.4 step 13 (spec/29:704-711). Do not re-derive the anchor sweep; re-check only an anchor a later fix touches.

MISTAKE (nearly filed, refuted in-pass): "spec/05:488, spec/06:139 and spec/16:12 still key the `maxSessionsPerPod` drain on 'a session release' and SPEC-3 leaves them unedited." All three are selectors ("the session release that drives the count to the bound"), not universals, so they stay true when some releases file no report. §12.6 needed re-keying only because it quantified universally ("at each session release", "on each session release"). Do not re-file this family.

MISTAKE (nearly filed, refuted in-pass): "the §5.2 disposition table has no row for a `running` slot no cleanup reclaims" (the abandoned attempt's late untokened `StartSession` that starts a session). The table's declared residue scope is exactly "a pre-`running` slot no cleanup reclaims" — §6.2's staged paragraph and §7.1 both cite it with that scope — and the running orphan is recorded by rule 13 and by the Edge-cases bullet. Adding a row would contradict the explicit scoping.

MISTAKE (nearly filed, refuted in-pass): "§7.2 step 3 asserts flatly 'the connection that attempt still holds' where §7.1 conditions it on the connection being open." Step 1 of §7.2 cancels the in-flight RPCs but not the gRPC ClientConn, and the staged commentary makes the clause load-bearing ("Step 1 needs none, because step 3 names the connection"). Low impact; a fix here would reopen step 1's carve-out question.

WATCHOUT: `exited_cleanly` is `ShutdownResponse` field 1 in the proto and the string appears NOWHERE in `spec/`. The §5.2 disposition table's column header "Clean-exit flag on the `Shutdown` response" is the corpus's only introduction of it. If a later round wants the flag named, that is the single site — EVIDENCE: schemas/lenny-adapter.proto:1665-1668

UNVERIFIED: a `PrepareWorkspace` frame arriving after the hold opened, on a call rule 9 already admitted, is not re-admitted by rule 2, so an in-flight upload stream can write into a tree `removeSlotTree` is deleting and defeat §5.2's fresh-workspace guarantee (spec/05:556). Reachability turns on whether the adapter's handler honours the cancelled server context; nobody has traced it. The standing context lists "handler writing after `removeSlotTree`" as cut for budget, so this is the same question with a named consequence.

USEFUL [Adapter predicate nesting / "The pod's shared runtime process has been given" is bound vocabulary]: these two standing-context entries are what let me separate the three start-path instants above and decide which §6.2 clause was the defective one rather than rewriting the whole boundary block (which the seven-round trap forbids).

### [spec.10.review-performance.1]

DECISION: returned an EMPTY findings list for the performance / scalability /
failure-mode lens against the restructured spec staging (disposition table,
critical-section paragraph, choices-only Design, pruned edge cases) — BECAUSE the
staging introduces no new durable write, no new watch, no new lock scope and no new
serialization point, and every failure-mode candidate I derived resolved either to
shipped behaviour the staging now describes, to a row of the §5.2 disposition table, or
to an entry the standing context already bars. ALTERNATIVES: candidates worked and
dropped are recorded below.

FACT: the staging adds ZERO new durable writes. Scanning every staged block: SPEC-1,
SPEC-2, SPEC-4 and SPEC-5 touch no store; SPEC-6 adds two Prometheus counters (in-memory);
SPEC-3's §12.6 edits are trigger re-keys on an existing column. The only rate change is a
REDUCTION in `agent_pod_state` UPDATEs, from the §5.2 biconditional withholding
`ReportSessionScrub` for every cleanup outside a runtime-given `Shutdown`. No etcd write
is added on any per-bind, per-request or per-session path. A future capacity lens can
start from "the staging is write-negative" rather than re-deriving it.

CORRECTS [spec.2.review-performance.2]: that entry's FACT "`st.sessionID` is set ONLY in
`claimSessionSlotUnderLock`, i.e. at `StartSession`" is WRONG, and it understates the
staging's benefit. `st.sessionID` has TWO setters: `pkg/adapter/slotsession.go:87`
(`claimSessionSlotUnderLock`) and `pkg/adapter/slotcreds.go:34` (`assignCredentialsSlot`).
The standing-context entry "Adapter predicate nesting" has it right. Consequence: the
SHIPPED `Shutdown` handler takes `bound == true` for a slot that `AssignCredentials` bound
but that never started (`bound := removed && st.sessionID != ""`,
pkg/adapter/session.go:239), so today it sends the graceful-shutdown terminate frame when
no bound co-tenant remains (:259-261), closes the shared runtime (:262-266), and files
`reportSessionScrub` (:279) — advancing `sessions_served` for a session the runtime never
served. The staged design re-gates the teardown on `st.started` and the report on
`runtimeLive`, so this whole arm disappears. The staging therefore removes a real spurious
Postgres write AND a spurious shared-runtime close, rather than merely transcribing the
code. — EVIDENCE: pkg/adapter/slotcreds.go:23-38, pkg/adapter/session.go:237-280

FACT: the §5.2 disposition table's new `leaked` cells for a PRE-`running` slot
("Pre-`running` slot, reclaimed by a `Shutdown` / An act fails -> Entered") are new
`leaked` occupancy that the shipped `sessionID == ""` arm never produced, and at
`maxConcurrentSessions: 2` one such event reaches `ceil(maxConcurrentSessions / 2) == 1`
and retires the pod (spec/05_runtime-registry-and-pool-model.md:561). I did NOT file it:
the acts on that arm are `slotlayout.RemoveTree`'s four `os.RemoveAll` calls plus the
deregistration, so the rate is disk-error-rare; the staging's own first accepted-failure
bullet already states the concurrency-2 threshold consequence; and the capacity family is
barred in the standing context. Recorded so the next capacity lens does not rebuild it.

FACT: the reclaim hold creates no serialization bottleneck at any tier. It is keyed on the
slot identifier, which is the session identifier, so N concurrent cleanups refuse N
distinct keys; and the hold check rides the registry lock the adapter already takes on
every slot RPC, so it adds a map lookup and no new lock. The critical-section paragraph
likewise widens no lock: entry creation already runs `resolveSlotPaths` and
`slotlayout.EnsureTree` under `s.mu` in the shipped `ensureSlotStateLocked`, and the
rule-8 step (resolve, confirm, record) does no I/O.

WATCHOUT: the §5.2 `**Slot cleanup:**` bullet keeps its
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` cleanup budget while the staged
reclaim-hold paragraph states a DIFFERENT bound for the same cleanup's close (the
`Shutdown`'s graceful window, else the request's deadline, else ten seconds for the §10.1
termination). Two bounds on one cleanup looks like a duplication or contradiction finding
and is NOT one: the bullet's formula has no implementation at all (standing context:
"The adapter enforces NO per-slot cleanup timeout of its own"), the staging says in place
that the formula "stands as written", and the hold paragraph bounds only the close while
`removeSlotTree` takes no context on either path. Pre-existing and deliberate.
— EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545;
proposals/0081_*/0081_*.spec-changes.md:628, :665-666

UNVERIFIED (carried from [spec.4.review-performance.1], still unclosed): after a §10.1.4
hold-timeout termination whose shared ten-second budget expires mid-batch, every remaining
member's identifier is held for the life of a pod that does NOT terminate. The only route
back onto that same pod is a create-time-reserved row keeping its §4.6 pod binding, and I
still could not show a reachable interleaving where such a row's start retries onto a pod
whose coordinator was lost. Whoever can settle it should; the standing context bars the
"§10.1.4 N-identifier hold" family, so it is a note rather than a finding.

### [spec.10.review-reliability.1]

DECISION: returned an EMPTY findings list for the reliability lens on spec round 10 — BECAUSE every
recovery mechanism the staged spec adds (the per-attempt token, the stamp-once rule, rules 10-15,
the §5.2 disposition table, the slot-identifier reclaim hold, the §7.1 reclaim obligation) traces
cleanly through crash, restart, lost-response and racing-successor orderings, and each residue a
trace reaches is already a disposition-table row or a bullet in the spec file's
`## Edge cases and accepted failure modes` — ALTERNATIVES: filing the four near-misses below, each
rejected on the evidence named with it.

MISTAKE (mine, avoided, and it is the second time this lens family has walked into it): I built a
finding on staged §7.2 step 3 stating the connection preference flatly ("which the gateway sends on
the connection that attempt still holds") where staged §7.1 conditions it ("when that connection is
still open"), and was about to file it as restated-rule drift. It was already considered and
withheld in this log's own Ledger as "a summary clause, not a copy", and the archive carries a
WATCHOUT against the sibling cross-replica dress plus the tree fact that makes step 3 implementable
(`Binder.Resume`'s adapter client is per-attempt and still open at the only failure point that
follows a landed reservation). Cost: about a third of the pass.
EVIDENCE: review-log.md:1899-1900; review-log-archive.md:1844, :2623, :8431;
spec-changes.md:403 (§7.1 clause), :478 (§7.2 step 3).

MISTAKE (mine, avoided): I derived that staged §7.1's unconditional "the gateway sends the reclaim
even when the failing RPC's own context is already cancelled" contradicts §10.1.5 rule 1 ("Stop RPCs
immediately: Cancel all in-flight RPCs for that session. Do not retry") for a bind attempt abandoned
on a generation-stale rejection. The log's Retired list already carries "coordinator handoff fencing
the reclaim" as cut for budget (implementor-level or pre-existing), so filing it re-opens a closed
item. Reachability is also thin: `adapterclient` sets `coordination_generation` only on
`CheckpointBarrier`, so no bind-sequence RPC can receive a pod-side stale rejection; only the
failed-Postgres-CAS arm of §10.1.5 is in play.
EVIDENCE: spec/10_gateway-internals.md:66-68; review-log.md:446 (Retired, budget-cut list);
spec-changes.md:403.

FACT: §10.1.4's hold-timeout termination shares ONE 10s context across every member of pass 2
(`closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)` with the loop's own
comment "only the last member's close consumes the grace"), so the staged §5.2 hold paragraph's
per-cleanup "graceful window of ten seconds" is true only after CODE-6 re-scopes it to a per-member
close context. Do not file this against the spec text: the staging is deliberately written to the
post-CODE-6 state and the Settled entry says so.
EVIDENCE: pkg/adapter/holdstate.go:201-205; review-log.md:59.

UNVERIFIED: whether a gateway replica's §10.1.7 preStop drain discharges the §7.1 reclaim obligation
for a bind attempt in flight when the drain starts. The spec file's accepted-failure-modes bullet
covers "a compensation lost to a gateway crash" and says the reclaim is "sent once, from the process
that abandoned the attempt", but a graceful drain is distinguishable from a crash and nothing states
whether the drain waits for, or abandons, an in-flight bind's compensation. A Kubernetes-lens or
mechanism shard with §10.1.7 in view should settle it; the remedy, if any, is one clause and may not
clear the bar.
EVIDENCE: spec-changes.md:201-212 (gateway-crash bullet); spec/10_gateway-internals.md:109
(`#### 10.1.7 preStop Hook Drain`).

USEFUL [Settled: "`RecordSessionScrub` has no per-session dedup"] and USEFUL [Trap: "'Completed',
'the cleanup does not complete' and 'the `leaked` predicate' are THREE different things"]: together
these killed three candidate findings about double-counting `sessions_served` and about a failed
directory removal being reported as `leaked`, in minutes rather than in a re-derivation.

### [spec.10.review-security.1]
DECISION: returned an EMPTY findings list for the security lens on spec round 10 — BECAUSE every
security-shaped reading I could construct against the staged spec edits resolves to (a) shipped
pre-existing behaviour the staging records rather than creates, (b) an entry the standing context
already bars, or (c) an OPEN the log routes to a human — ALTERNATIVES considered and rejected below.
FACT: the two code claims SPEC-3 makes on the §5.2 action-list append are both true, and they are the
only new credential-handling assertions in the staging. `slotlayout.RemoveTree` does remove
`p.CredentialsDir` (it iterates `slotRoot, Sessions, Artifacts, CredentialsDir` and `os.RemoveAll`s each,
returning only the FIRST error), and `deregisterSlotLocked` does cancel every armed expiry timer
(`for provider := range st.timers { s.cancelSlotExpiryTimerLocked(st, provider) }`) before
`delete(s.slots, …)` — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69, pkg/adapter/slotsession.go:174-189
FACT: §12.6 is the ONLY spec site outside §5.2/§4.7 that keys `sessions_served` on "each session
release"; `grep -rn ReportSessionScrub spec/` outside spec/04 and spec/05 returns spec/12:481 alone, and
spec/13 and spec/10 carry no "session release", "leaked" or report sentence at all. So SPEC-3's re-key
leaves no unstaged spec surface. `schemas/lenny-adapter.proto:314,453,471` and `migrations/0167_*` do
mirror it, but both are already DEFERRED entries in the review log — EVIDENCE: spec/12_storage-architecture.md:481
FACT: `ProjectOccupancyPhase` switches on the pod's CURRENT PHASE with no recycle input, exactly as
SPEC-4's re-keyed §4.6.1 bullets state: `Reserved`+no claim → `Idle`, `Claimed`+no claim → `Draining`,
and the function's own doc comment already states the rule the edits adopt. SPEC-4 introduces no new
unscrubbed return-to-idle edge; the reserved-phase failed-bind path (claim DELETE while the claim is
still `reserved`) exists identically under the shipped text and the replacement — EVIDENCE:
pkg/controller/warmpool/occupancy.go:83-140
WATCHOUT: the most tempting security finding on this staging is that SPEC-3's §5.2 disposition table
narrows the shipped universal "If cleanup fails, the slot is leaked" so that a runtime-given slot whose
close succeeds and whose `RemoveTree` fails now reports `released`, is `Not entered` into `leaked`, and
frees occupancy — leaving `credentials.json` on a pod that keeps serving with its §4.9 expiry timers
already cancelled by `deregisterSlotLocked`. Do not file it. The standing context bars it twice, once as
"`leaked` is never a free adjective … Do NOT make a surviving `credentials.json` `leaked`: that pins
occupancy above zero and blocks the whole-pod scrub that would collect it", and once as the
`errors.Join(closeErr, treeErr)` re-key recorded as this window's most expensive mistake — EVIDENCE:
review-log.md:193-200, spec-changes.md:618
WATCHOUT: the second-most tempting is that the §5.2 hold sentence "the adapter admits no request that
would create or resolve a registry entry under it" reads, at spec level, as refusing a §11.4 credential
revoke during a hold, which would be a published contract that fails OPEN on a mandatory credential
control. It is vacuous: the hold opens at the deregistration, so during the hold there is no entry left
to resolve, and the shipped revoke/rotate/extend/`CoordinatorFence` handlers resolve through read-only
`slotStateLocked`/`boundSlotState` rather than `ensureSlotStateLocked`. The log bars the whole
"hold reaches a non-bind RPC" family on exactly that reading — EVIDENCE: review-log.md:248-251, :75
FACT: SPEC-1's §4.1 replacement gates the whole-pod scrub on "a request that passed the teardown-pairing
rule", so a rule-10 `INVALID_ARGUMENT` suppresses the scrub. That is fail-CLOSED rather than fail-open: a
pod whose whole-pod scrub is not reported never reaches the `reserved → idle` edge, so it cannot be
reused dirty, and the production caller set is one builder that always sets `unconditional_teardown`.
Not a finding — EVIDENCE: spec-changes.md:258, review-log.md:62
USEFUL [standing context, "MISTAKE nearly filed, the security family"]: it named four refuted dresses
(`superseded`/`absent` as self-reports exempting leak accounting, a compromised adapter refusing every
teardown, a late `StartSession` orphan holding credentials, the withheld report relaxing
`maxSessionsPerPod`) and saved me from re-deriving all four. The bar it states — "the lie must make the
gateway do something new; `leaked = err != nil || !cleanly` is shipped" — is the right filter for this
lens and should survive compaction.
USEFUL [standing context, "MISTAKE nearly filed, repeatedly: 'SPEC-4 removes the one-session-only
control'"]: the one-session-only-reuse control is the first thing this lens reaches for in SPEC-4, and
the entry plus `ProjectOccupancyPhase`'s own comment closed it in one read.
### [spec.10.review-security.1]

DECISION: returned an empty findings list for the security lens on spec round 10 — BECAUSE every security-relevant claim the staged spec text makes checks out against the tree, and each of the four dresses I derived independently lands on a refutation the standing context already records — ALTERNATIVES considered and rejected: (a) the §5.2 reclaim hold's wide "create or resolve a registry entry" predicate refusing a §11.4 credential revoke for the life of the pod (rejected: during the hold the entry is already deregistered, so a revoke resolves nothing and the hold is vacuous on it — the standing-context trap "Do NOT file the §15.4 `Shutdown` carve-out against the wide hold predicate" covers exactly this reading); (b) SPEC-3's biconditional withholding `ReportSessionScrub` relaxing the `recycle.maxSessionsPerPod` reuse bound (rejected: listed verbatim in the refuted security family); (c) rule 13's `superseded` / rule 11's `absent` as a pod self-report exempting a slot from leak accounting (rejected: same refuted list; `leaked = err != nil || !cleanly` is shipped and §7.1's predicate quantifies over every outcome, so it fails closed against a non-conforming adapter); (d) §4.1's "whatever outcome that request answers" letting a `superseded` reclaim run the whole-pod scrub over a live successor (rejected: the recycle disposition rides only the separate occupancy-zero `Shutdown` the gateway sends when occupancy is already zero, and the compensation carries none).

FACT: both new actions SPEC-3 adds to the §5.2 `**Slot cleanup:**` action list are genuinely shipped, so the "shipped behaviour rather than new obligations" claim holds. `slotlayout.RemoveTree` iterates `slotRoot, Sessions, Artifacts, CredentialsDir` and `os.RemoveAll`s each — EVIDENCE: pkg/adapter/slotlayout/tree.go:58-69. `deregisterSlotLocked` cancels every armed provider timer before `delete(s.slots, …)` — EVIDENCE: pkg/adapter/slotsession.go:174-181.

FACT: the fail-closed risk of rule 10 (a `Shutdown` carrying neither field is `INVALID_ARGUMENT`, so a missed caller silently stops tearing down a revoked session) is bounded by there being exactly ONE `ShutdownRequest` construction site outside tests in the whole tree — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:814 is the only non-generated, non-test `&adapterv1.ShutdownRequest{` in pkg/ and cmd/. This is the one claim in the standing context ("five sites behind ONE builder") whose failure would be a real security regression, and it re-verifies clean.

FACT: SPEC-4's new projection input ("the phase the pod currently projects … it may read back because it is the sole writer of that field") agrees with §4.6.1, which states "the gateway does not write `Sandbox.status`" — EVIDENCE: spec/04_system-components.md:409. No §4.6.3 ownership or RBAC boundary is crossed by any staged edit.

FACT: the credential residue the disposition table's failed-cleanup rows leave is reachable by the whole-pod scrub, because the scrub enumerates the on-disk children of `/run/lenny/slots` rather than the registry — EVIDENCE: pkg/adapter/podscrub.go:132-136. A finding that a surviving `credentials.json` is unbounded therefore has no ground.

WATCHOUT: the one place a future security lens could find real ground is a change that moves a mandatory credential control (`RevokeCredentials`, `RotateCredentials`, extend, `CoordinatorFence`) onto `ensureSlotStateLocked`. The staged §5.2 hold predicate is deliberately wide and §15.4 publishes "An adapter that admits a request that hold refuses does not conform", so the moment one of those handlers creates rather than resolves, the hold becomes a fail-open on credential revocation for the life of the pod. Today all four resolve read-only — EVIDENCE: standing context entry "The reclaim hold cannot refuse a mandatory credential control"; re-check on any handler move.

### [spec.10.review-single-source.1]

DECISION: returned an EMPTY findings list for the single-source lens on the staged spec edits — BECAUSE the round-7 shard already inventoried the rule/site topology and returned empty, the only delta since (r7-prefix → now) is the SPEC-3 §12.6 commentary on triggers and tier-11 gates, which states no rule and appears at one site, and every fresh candidate I raised this round resolves into the lens's own cite/clause/rationale carve-out — ALTERNATIVES: four candidates considered and rejected, below.

FACT: the delta this lens had to examine is tiny and entirely commentary. `diff -u spec-r7-prefix spec-r8-prefix` plus `diff -ru spec-r8-prefix <current>` over spec-changes.md touches only the SPEC-3 §12.6 sub-section (read-clause replacement added, one-step increment-and-evaluate rationale, the two tier-11 gate paragraphs) and the "Spec files touched" spec/12 bullet. `grep -n "TestPerReleaseSessionCountDrainAgrees_F5231\|podStateGatewayWrittenSentence\|TestSection28RegisterWritersMatchTheSpec"` over the whole proposal directory returns three lines, all in that one paragraph, so the gate bookkeeping has one home. — EVIDENCE: spec-changes.md:811,:820,:822

FACT: §12.6's `sessions_served` sentence already cites §4.7 as the counter's authority, so the §4.7 `ReportSessionScrub` row's `sessionsServed` clause and §12.6's write clause are home-plus-citation rather than two homes. The prose sentence and the DDL comment on the same column each state the write and read triggers in full; that doubling is pre-existing §12.6 house style for the table schema and SPEC-3 keeps both in step. Not filed. — EVIDENCE: spec/12_storage-architecture.md:481, :494

FACT (considered, not filed): the per-session-release `maxSessionsPerPod` evaluation is worded at four further spec sites SPEC-3 does not touch — spec/05_runtime-registry-and-pool-model.md:488 ("transitions to `draining` on the session release that drives the served-session count to `maxSessionsPerPod`"), spec/06_warm-pod-model.md:139 (fence `claimed ──→ draining ... on a session release`), spec/16_observability.md:12 ("at the per-release `maxSessionsPerPod` drain"). Each is existential or a name for the mechanism rather than the universal "read on EACH session release" that made §12.6's clause false, so none is falsified by SPEC-3 and none is a second home for §12.6's read trigger. A later round should not re-open this without showing one of the three asserts a universal.

WATCHOUT: §7.2 step 3's staged replacement carries "which the gateway sends on the connection that attempt still holds", and §7.1's staged paragraph states the same connection rule with a qualification step 3 drops ("when that connection is still open"). I did not file it: it is a relative clause inside a step that cites §7.1 for the obligation, which is the "summarises in a clause" carve-out the §29.4 step-13 refutation already turned on. A round that wants the reduction should delete the clause from step 3, never re-word §7.1. — EVIDENCE: spec-changes.md:402 (the §7.1 fenced block), :469 (step 3 replacement)

USEFUL [spec.7.review-single-source.1]: its four rejected candidates (the reclaim-hold status partition, "at most one report" at §5.2 and rule 8, the `superseded` gloss at rule 15 and the §16.1 row, the `running` boundary at the fence and the pre-`running` paragraph) are exactly the four a repeated-phrase grep resurfaces. Reading that shard first turned a full re-derivation into a delta check. Do not re-file any of them.

USEFUL [Standing context, "DECISION: the disposition of every per-slot cleanup is ONE TABLE"]: with the table, the §4.7.1 critical-section paragraph and the choices-only Design section named as the three homes, the only work left for this lens is checking the citing sites, which all cite.
### [spec.10.review-single-source.1]

DECISION: filed two findings only — the new §6.2 fence entry's trigger (stated three times) and the §12.6 `sessions_served` read-trigger re-key (one rule, three sites, two vocabularies after the edits) — BECAUSE the rest of the staging is genuinely reduced: §5.2 owns the disposition table and the reclaim hold, §4.7.1 owns rules 1-15 and the registry critical section, §15.4 owns the hold's wire status and the conformance criterion, §4.7/§29.4/§6.2/§7.2/§7.3/§4.7.9 are pointers. ALTERNATIVES: rejected filing the §16.1 `lenny_slot_compensation_superseded_total` row as a copy of rule 15's `superseded` definition, because §16.1's house style already carries long descriptive rows with a trailing "See §X" (spec/16_observability.md:59, :188, :189) and the row does cite §4.7.1.

FACT: the graceful-shutdown-signal condition the staged §4.7 `Shutdown` row adds ("only when the deregistration leaves the adapter holding no bound entry") exists nowhere in spec/ today; the only site that states it is the doc mirror docs/reference/adapter-contract.md:75, which DOCS-2 moves. So the staged row is the first spec statement rather than a copy. Same for "flushes the session's final usage report": zero hits for "final usage report" under spec/.

FACT: the concurrent-pool `maxSessionsPerPod` evaluation point is stated at four sites today and a tier-11 gate exists purely to keep them in sync — spec/05:488 (`**Session count limit:**`, the home), spec/06:139-142 (concurrent-occupancy fence), spec/12:481 + :494 (prose clause and DDL comment), and docs/reference/state-machines.md. The gate is `TestPerReleaseSessionCountDrainAgrees_F5231` in tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:148-195. SPEC-3 re-keys only the spec/12 pair and re-keys the gate to match. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:165-176

WATCHOUT: the SPEC-4 §6.2 fence edit for the `claimed ──→ draining` entry gives a replacement block with no quoted "reads, verbatim" original and no "Replace it with:" line — spec-changes.md:853-860. The current text is spec/06_warm-pod-model.md:95-97 ("claim deleted on a pod with recycle.enabled: false"). It is resolvable, but it is the one staged edit in the file that does not follow the quote-then-replace form. Not my lens; a citation or edit-anchor lens should confirm nobody has filed it.

FACT: the ten-second graceful window the staged §5.2 reclaim-hold paragraph assigns to the §10.1 hold-timeout termination appears nowhere in §10.1 (spec/10_gateway-internals.md:58 states `coordinatorHoldTimeoutSeconds`, default 120s, and no close window) and pkg/adapter/holdstate.go:239 only comments "Best-effort graceful runtime termination". So it is a new constant rather than a copy of §10.1. A defaults/overridability lens should check it against code-best-practices.md's operator-tunable rule.

### [f1.open-decisions-apply.id21]

DECISION: item id:21 (does a bind abandoned before `running` count toward
`recycle.maxSessionsPerPod`) is resolved as staged, answer "no", and its entry left
`## Open decisions for human to make`. No change file was edited, because the answer is the
do-nothing answer and the staging already carries it: the §5.2 disposition table files no
cleanup-outcome report on any pre-`running` row (spec-changes.md:620-622) and the staged
biconditional scopes the report to a slot the shared runtime process was given
(spec-changes.md:601). Site: summary.md, entry 21 deleted, and the section preamble updated so
that 21 is listed with the entries the staging answered and with the entries that became defects
rows.

DECISION: the entry's residual-state half was not dropped with it. It is now a row under
`## Defects in the shipped tree that this proposal does not stage`, one bullet naming §5.2's
retirement predicate, the report-keyed write trigger, and the §4.7.9 `RunSetup` step. The
entry's other clause, "raise it as its own finding", is recorded in that row as what correcting
it would take rather than staged as an action.

FACT: every citation in the new defects row re-verified against the tree.
`spec/05_runtime-registry-and-pool-model.md:488` carries both the served-session-count trigger
and the deployer-choice sentence; `:473` carries the residual-state vectors the scrub cannot
address; `spec/04_system-components.md:854` is step 5 of the §4.7.9 startup sequence and names
`RunSetup`; `pkg/agentpodstate/agentpodstate.go:124-133` is the `IncrementSessionsServed` doc
comment and declaration; and
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:457` is the one
production call of it, inside `RecordSessionScrub`.

FACT: no staged change file carries an `## Open decisions for review` section, confirmed again
this firing, so there was nothing to delete outside the summary.


### [f1.open-decisions-cleanup.1]

FACT: `summary.md` already carried exactly the eight required headings, in order, with nothing
else at heading level: `# Summary: ...`, `## Summary` (a container holding `**Problem
statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**` in that order and no
prose of its own), `## Goals`, `## Non-goals`, `## Open decisions for human to make`,
`## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other
proposals`, `## Deliverable index` last. No part needed renaming, no block was relocated, and the
file is unchanged by this pass.

FACT: `## Open decisions for human to make` carries entries 20 and 27 and nothing else. Both keep
their stamped identifiers. No `### Retired` or equivalent block stands inside it, and the apply
pass's deletion of entry 21 left none behind.

DECISION: the section preamble was left exactly as the apply pass wrote it. It is true of the two
entries the section now carries: 27 carries a recommendation with its ground, its alternatives and
a confidence, and 20 carries the question and its ground alone. Entry 21 appears in two of its
lists because both dispositions hold of it, which `[f1.open-decisions-apply.id21]` records as
deliberate, so there is nothing here for a format pass to correct. ALTERNATIVES: striking 21 from
the "answered by the staging" list (rejected: no change file was edited for it, so that clause is
true, and removing it would re-adjudicate the apply pass's recorded choice).

FACT: every out-of-scope marker this firing reported as `no-edit-needed` resolves to an entry
already standing under `## Defects in the shipped tree that this proposal does not stage`,
including the one added this firing for id:21's residual-state half. None was promoted, reworded
or reordered by this pass, and the section holds no decisions.

DECISION: `**Accepted failure modes.**` stays where it stands, inside `## Non-goals`, on the
ground the earlier cleanup recorded: it is a labelled part rather than a heading, the section list
constrains labelled parts only under `## Summary`, `## Non-goals` is a listed section whose subject
covers a residue this proposal declines to close, and four sites in the file cite it from there.
ALTERNATIVES: moving it under the defects section (rejected: that section is scoped to confirmed
defects in the shipped tree, while these are properties of the design staged here).

FACT: `## Deliverable index` was preserved line for line in last position, its closing paragraph on
tests and CONF-1 included. Nothing in this pass touched a staged change file, the problem statement
or the implementation checklist.
