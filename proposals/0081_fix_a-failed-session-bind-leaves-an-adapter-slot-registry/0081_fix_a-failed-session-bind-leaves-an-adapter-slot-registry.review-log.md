# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

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
- **Do NOT file the §15.4 "`Shutdown` is not held" carve-out against the wide hold predicate.** During
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

### Open

- **Can the reclaim-hold refusal reach `Binder.Prepare`/`Binder.Launch` and drain the pod?** — OPEN: `errSlotReclaimInProgress` is a bare `codes.Aborted` matching neither CODE-7 sentinel, so CODE-8's guard falls through to `failPhase` and drains.
- **Does a permanently held slot identifier have to be answered as permanent?** — OPEN: a life-of-the-pod hold repeats `ABORTED`, which §15.4 publishes as retryable; the entry reaper is routed to remediation position 2.
- **Does the staged §15.4 conformance criterion over-reach?** — OPEN, FILED: "the acts it states and no others" over every request over-reaches, and rules 1, 3, 8 and 10 name no `ErrorCode`. Narrow the scope.
- **Does §15.4 state a rule of its own after all?** — OPEN: commentary says it states none, yet its hold block owns rule 2's `ABORTED` and restates the `Shutdown` carve-out and rule 11.
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
- DEFERRED [spec-changes.md, commentary wording]: SPEC-3's rationale says the hold paragraph "states a
  terminal" where only the disposition table does, and the untokened-entry bullet calls the §10.1
  hold-timeout termination "a request" where §5.2 says it runs under no request.
- DEFERRED [implementation-checklist.md, S1, S2, S4, S5, S7, S15, S16, S19, S22]: the checklist predates
  the restructure. S1 names "three non-conformances" §15.4 does not state; S4 omits the biconditional,
  the table, and §12.6; S5 and S7 undercount their edits; S16 must key the hold release on completion.

### Retired

Bodies are in the archive file. This list names subjects only.

- Open, answered by the current text or recorded in the summary's open decisions or unstaged-defects list: socket runtime on a recycling pool; mid-start `Shutdown` bricks the pod; §4.1's retired vocabulary; `receiving_uploads` on an upload-free plan; §5.2's bullet at concurrency 1; `terminate` frame reason value and `deadlineMs`; whose view of "running" §7.1 means; does §7.3 owe a release step; the other `resuming` bullets; concurrent `/start` serialization; retry re-incrementing the Redis reservation; `maxSessionsPerPod` counting a bind that reached `RunSetup` (decision 21); `ProjectOccupancyPhase` no-claim arm and claim-observed generation (decision 27); pre-`running` residue's return-to-pool exit; one-session replacement pod terminal; gateway `leaked` versus adapter `leaked`; §5.2 and §7.1 both stating the `leaked` disposition; hold's "create or resolve" predicate width; rule 10's pre-registry timing; does a bind attempt span `/finalize` to `/start`; headline over-states what the token closes; two residues living only in reasoning; one-session-pod residue bullet; §4.7 `ReportSessionScrub` row edit; `adapter-contract.md:81` row; three docs sentences against the withheld report (two pages survive as the DOCS-4 item); §10.1.4 termination owing a report; CODE-4 session-keyed credential release; second `AssignCredentials` double-count; MCP surface a retry inherits; successor inheriting armed timers; is tier 8 reached; guard-acquisition expiry tests; rule 8 and deregister-and-hold CONF-1 cases; §7.4 mid-session rule; `metrics.md` untokened row; hold refusal accounting on the resume path; hold bounded at concurrency 1; §16.1 superseded row qualifier; §15.4 "stated once" claim; §15.1 exclusion narrowing; §6.2 Client-visibility inclusion clause; §6.2 fence versus prose on `leaked` at concurrency 1; spec/05 versus spec/06 pod retirement wording; `StartSession` row states no refusal.
- Open, mechanism or wording no longer staged: `/finalize` Gap-2 no-re-dial window (epochless form); which step routes `Shutdown` through `reclaimSlotLocked` (old S9/S10); CONF-1 lettered clauses (a)-(j) and clause (j) buildability; tier-9 epoch arms; the "spends one of the attempts" clause; who owns `slot_cleanup`'s start instant; SPEC-3's exclusive-pod arm; exclusive-pod abandonment disposition; "Seven residues survive" count; nine-versus-eleven adapter file lists; `spec-changes.md:98-102` blob-store clause; deleted `## Revision history`; summary host for accepted failure modes; round-8 unlanded findings; lens-retirement questions; tier-7a park placement; late-reclaim arm `leaked=false` observer.
- Open, still unclosed and cut for budget (implementor-level or pre-existing; re-derive from the archive when one matters): §29 incomplete-enumeration rule; §29.10 shared list; concurrent resume occupancy and orphan GC reach; §15.1 retry reaching a draining pod and `ClaimSlot` pass 1; resume-path `error_type`; compensation budget pin and `budget/2` margin, `SocketRuntimeProcess.Close` grace, and override; lease hold bound under §4.9; `SLOT_FAILED` undocumented; `configuration.md:99`; `/sessions/` and `/artifacts/` in the action list and §6.4; timer cancellation conditioned on credential removal; handler writing after `removeSlotTree`; hold spanning the scrub report; non-`Shutdown` hold-retention seam; CODE-2 idempotency sentence and `Resume` confirmation reacher; `ensureSlotStateLocked` whole-of-the-rule comment; zero-frame `PrepareWorkspace` senders; `slotResolveError` preserving `ABORTED`; coordinator handoff fencing the reclaim; §28.3 `LNK-POD-GRPC` multiplicity; N8 in proposal rationale; `superseded` producers; `recordSetupCommandFailed` audit; spec/07:208 and §15.1 precondition rows and "fresh pod" sentence; `state-machines.md` released-row actions; DOCS-2 tier-11 pins, annotation widening and audience clause; `metrics.md` gateway row placement; `Client.Shutdown` doc comment; CODE-1 leak-accounting paragraph; `SlotReclaim` hook arity; `slotFailureWorkspaceFinalize`; CODE-4 `Targets:` list and deliverable heading file lists; checklist tier digits; unused `noteCompensationOutcome` at S10; test-file lists (`binder_test.go`, `start_test.go`, `manifest_fields_test.go`, tier-3 file names, `slot` basename credit, `client_test.go` §15.4 credit); §11.4 fan-out test; setup-envelope tier-3 case; `holdstate_test.go` fake runtime; `fakeSDKWarmRuntime` workspace base; `terminal_reclaim_internal_test.go`; 0079 overlap row; summary index third anchor and `summary.md:141` attribution; `Resume` stall leak rate; re-placement onto a held pod.
- Deferred entries dropped in the 2026-09-20 hard compaction (bodies in the archive): proto `ReportSessionScrub`/`SessionScrubOutcome` comments (discharged, SCHEMA-1); spec/06 `slot_cleanup → released` annotation and its docs mirror (discharged, SPEC-4 and DOCS-1); error-catalog "nothing, deliberately" (mechanism gone, no-retry alternative); docs four sites that stay true (negative record); archive WATCHOUT re-point and ledger "fresh token" sites (log-only targets); CONF-1 per-frame epoch case, precedence note, property count, short-four-cases (discharged, CONF-1 is one case per rule with rule 9); every "RETIRED IN PASS 22" checklist and non-spec entry (discharged); DOCS-3 missing, DOCS-3 index line, summary "four replacements" (discharged, SPEC-5 now stages four); DOCS-1 `state-machines.md` projection clause (discharged); DOCS-2 `ReportSessionScrub` row (discharged); registered-but-unbound credential claim (discharged); §5.2 action list four trees (carried in summary defects); CODE-5 `Targets:` `isTransientPodClaimError`, CODE-6 heading file list, `noteCompensationOutcome` inverted clause, CODE-9 `catalog.go` path, SPEC-6 preamble count, summary CODE-9 bullet, summary "both RPCs", summary SPEC-2/SPEC-3 bullets, summary "Watch out for" connection bullet, summary Decisions file list, "Spec files touched" `Shutdown`-row description, §15.4 table dependants (discharged); SCHEMA-1 gate-text quote and tier-1 wire-rule five-RPC wording (mechanism gone or cosmetic); checklist frame-predicate and S10 negatives (negative records); accepted-residue two-item enumeration (discharged by the disposition table); duplicates of the `Client.Shutdown`, `Binder.drain`, CODE-4 Targets, CODE-9 verbatim, tier-7a, and checklist S1/S15 entries (duplicate).
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
