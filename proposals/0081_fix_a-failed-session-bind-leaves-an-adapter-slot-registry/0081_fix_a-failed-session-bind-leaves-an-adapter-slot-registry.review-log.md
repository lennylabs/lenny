# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass 32, 2026-09-21). Read the whole ledger, which carried the smallest window of
any pass: the five round-2 non-spec lenses `[non-spec.2.review-applicability.1]`,
`[non-spec.2.review-citations.1]`, `[non-spec.2.review-docs-alignment.1]`,
`[non-spec.2.review-reliability.1]` and `[non-spec.2.review-test-coverage.1]`, every one of them
returning EMPTY on its own verdict line. Lifted 12 Settled lines, 2 Traps and 3 Open entries. Honoured no
`CORRECTS`, because the window recorded none: its only corrective act was widening a citation range
(`warmlayout_test.go:164-167` to `:162-167`), which is folded into the Settled line that carries the seam
citations rather than kept as an entry. Deleted no trap and retired no Open or Deferred entry, because
five empty verdicts close nothing. Recorded one new DISAGREEMENT as `UNVERIFIED` rather than picking a
winner: `[non-spec.2.review-applicability.1]` judged the three stale bare-`removeSlotTree`
files-touched bullets below the bar and said not to re-file them, while
`[non-spec.2.review-docs-alignment.1]` filed the `holdstate.go` one as a finding in the same round.
Both are in this window and neither corrects the other, so the trap carries both readings. Did NOT reach
the 977-line target, and this pass could not move toward it: the window added residue and closed nothing,
so every line above it was already load-bearing before the pass began. Nothing was dropped to make the
number.

Prior passes (25 through 31) lifted spec rounds 2 through 23, the two earlier recheck lanes, the
`running`-boundary redesign `[redesign.6.fix.1]`, the prune firings, `[index-reconcile.1]` and the `f1`
open-decision firings. Their per-pass accounting is in `### Retired` and in the archive. None of them
reached a line target either, and each said so.

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
per numbered rule plus one rule-5-before-rule-6 ordering case; and CODE-10 and CODE-11 as
PREDICATE-DEFINED comment-reduction deliverables that enumerate no files anywhere. An archive entry that mentions a bind
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
- **The reclaim-hold refusal cannot drain a healthy pod from `Binder.Prepare` or `Binder.Launch` on the exclusive path** (`[prune.5.fix.1]`). `s.reclaiming` is keyed by the session identifier (`pkg/adapter/slotsession.go:175`, `slot.go:105-124`) and is set only inside a cleanup of that identifier on that pod, through `reclaimSlotLocked` (`Shutdown`, `releaseSessionSlot`, `terminateHeldSession`). `Binder.Prepare` sends no compensation, and every `Prepare` or `Launch` failure runs `failPhase`, which drains (`binder.go:1072-1082`), so a bind failure opens no hold and retires its own pod. The cleanups of the same identifier an exclusive pod can run ahead of, or concurrently with, a `Prepare` or `Launch` RPC for it are: the adapter's own failed-start `releaseSessionSlot` inside the `StartSession`, `Resume` or `ConfigureWorkspace` handler (`session.go:133-157`, `resume.go:69-141`, `sdkwarm.go:236-251`), whose error `Launch` drains on; `DemoteSDK`'s synchronous release of the registry's single entry (`sdkwarm.go:296-298`) inside the loser of two concurrent `/finalize` calls, which completes or fails before that loser's next RPC; a `Shutdown` sent by a terminate or a revoke, which retires the pod; and the §10.1.4 termination, which takes started sessions only (`slotsession.go:379-381`) after a coordinator loss, while `/finalize` and `/start` admit `created` and `ready` alone (`pkg/api/v1/session/session.go:288-289`). A hold outlives its cleanup only when that cleanup failed, and a pod holding a failed cleanup's residue for the session is the pod the accepted failure modes already assign to a drain. The refusal therefore reaches `failPhase` as a bare `codes.Aborted` only on a pod that is already draining or that carries that residue, and CODE-8's two-sentinel predicate needs no third arm.
- **DECISION (`[prune.5.fix.1]`): CODE-10 and CODE-11 are PREDICATE-DEFINED and enumerate no carrier files anywhere.** CODE-10's set is closed by one grep at application time over `pkg/ cmd/ tests/ migrations/ schemas/`; CODE-11 names four sites and one constraint. A finding shaped "file X is missing from CODE-10" is filed against a list that no longer exists.
- **CODE-10's grep needs SIX patterns, and two of them exist only because four patterns missed real sites.** `on every release` reaches `scrubreport_server.go:472` and `scrubreport_server_test.go:444,:455`; `evaluated per release` reaches the two §12 `sessions_served` glosses; `each per-slot cleanup outcome` is shortened to `each per-slot cleanup` because the `podspec.go` sidecar comment wraps the phrase across two source lines.
- **DECISION (`[non-spec.1.fix-G1.1]`, converged at `.fix-design-G1.3`): CODE-10's arm 1 is an OUTCOME TEST rather than a syntactic deletion.** After the edit the sentence must state and imply no universal that a report follows every release or every cleanup. Plain trigger deletion holds only where the trigger is a standalone adverbial; where the quantifier is fused into the report's own object or rides an already-true first conjunct, the report's scoping words take SPEC-3's §5.2 predicate verbatim and the still-true clause stays untouched.
- **The report predicate has ONE home and CODE-10 quotes it rather than pointing at it:** the §5.2 `**Scrub model.**` replacement, "when, and only when, that cleanup is one a `Shutdown` performs to reclaim a slot that reached `running`". Adding a §5.2 pointer to a comment that carries none breaks CODE-10's own invariant.
- **DECISION (`[non-spec.1.fix-G5.1]`): the `Server.removeSlotTreeFn` / `removeSlotTreeVia` tree-removal seam is homed in CODE-6 and lands at S14.** `releaseSessionSlot`, `terminateHeldSession` and CODE-1's `Shutdown` all call `removeSlotTreeVia`; homing it in CODE-1 forces an S14↔S17 cycle. The method is always present and returns `removeSlotTree(st)` unless the nil-defaulted FIELD is set, and the field is the seam.
- **`slotlayout.RemoveTree` returns nil for an absent path and accumulates the first error over four `os.RemoveAll` calls, so no unit test can force a non-nil return without the seam.** That is why the two non-`Shutdown` callers were unforceable before CODE-6 took it.
- **DECISION (`[non-spec.1.fix-G2.1]`): DOCS-2's `DemoteSDK` row carries the §4.7 row's own condition, "the session the pod holds if it holds one", and its second sentence opens "Where that cleanup runs and completes,".** `Server.DemoteSDK` guards on `anyRegisteredSession()` and `Binder.Prepare` demotes before any entry exists. The summary bullet and checklist S7 restate the condition in near-identical words and must move with the row.
- **DECISION (`[non-spec.1.fix-G4.1]`): CODE-9's gateway collector and accessor are pinned by extending `TestCredentialAndLLMProxyAndSlotMetricsEmit` and `TestNewMetricsEmittersNilSafe` in place** (`gatewaymetrics_elicitation_test.go:536,:567,:576`), named once in the Testing counters bullet with S10 citing it. No new gatewaymetrics test file; the shipped cases already assert the `lenny_slot_failure_total` exposition line and nil safety.
- **CODE-11 is exactly four sites, each cut to a citation of the §15.1 row.** `errorclassify.go:464-476`, `start.go:218` (`writeSetupCommandError`) and `:3629-3647` (`isTransientPodClaimError`), and the `// diagnosis:` at `resume_setup_demotion_internal_test.go:40-47`. Its constraint: one sentence per site, cite §15.1 by heading, keep §7.3 where the replaced span cited it, name no gRPC code as the cause. Checklist S25, `Depends on: S1`.
- **The six spec `SETUP_COMMAND_FAILED` sites are one-directional implications and stay true under the widened cause.** spec/07:208 and spec/15:625, :646, :647, :650, :720. The discriminator for any future candidate is whether the site carries an "any other …" complement clause; §6.2:290 does, which is why SPEC-5 edits it and edits none of the six.
- **`errorclassify.go:475-476` carries retired-form `// spec: 15:1105` / `15:1106` citations, and CODE-11 neither converts nor adds one.** The residue is the final row of the summary's unstaged-defects section. The bare `NN:LINE` spelling occurs 125 times across `pkg/`, `cmd/` and `tests/` and the ratchet's grammar cannot see it, so widening the matcher is a 125-site migration rather than a two-site fix.
- **The tier-0 line-citation ratchet is a FLAT PROHIBITION over the tracked tree, `files: []`, with `proposals/` read-excluded.** Its matcher keys on the `(spec/)?NN_name.md:LINE` path spelling, so a code-path `file:line` inside a staged Go comment is harmless while a spec-path one fails tier 0 on the tree's first occurrence. The CODE-1 `answerShutdown` parenthetical that carried one is deleted.
- **The whole non-spec citation sweep is DONE: 171 distinct `file:line` targets, every one resolving and saying what the proposal claims.** Recipe: one python regex over the file, resolving a bare basename under `pkg/adapter/` first, because `sdkwarm.go`, `server.go`, `slot.go`, `catalog.go` and `usage_test.go` collide with same-named files elsewhere and produce false out-of-range reports.
- **CODE-10's grep run verbatim at HEAD returns 45 hits over its five roots and is a strict SUPERSET of the fourteen files the deleted enumeration named,** plus the SCHEMA-1, CODE-1 and S4-sweep rows the blank routes to their own deliverables and the non-carriers the criterion admits. The prune lost no carrier; do not re-run this to prove it again.
- **`lenny_adapter_leaked_slots` is registered and emitted by the GATEWAY (`gatewaymetrics_credential.go:223`) and exists nowhere in `pkg/adapter`, while spec/06:160 and spec/05:545 attribute it to the adapter's `/healthz`.** The tree wins for the gate-topology question, because the adapter catalog gate never sees the series. Open decision 45's attribution sentence was corrected in place.
- **Open decisions 42, 43 and 44 are RESOLVED, and the decisions left with the human are 20, 29, 30, 32, 33, 34, 36, 41 and 45.** 42 (the §4.9 timer ordered against the credential-file removal) stands as staged and becomes an unstaged-defects row; 43 (coordinator preemption as a second unsent-reclaim trigger) stands as staged, because the §7.1 paragraph closes cause-neutrally; 44 (operator-guide rows for the SPEC-6 counters) is answered no.
- **The §4.9-timer-before-tree-removal ordering is UNIFORM across every release path and predates this proposal.** `deregisterSlotLocked` cancels every armed direct-mode timer before deleting the entry, and every performer removes the tree afterwards, best-effort: `Shutdown` at session.go:238 then :271, `releaseSessionSlot` at slotsession.go:214-219, the §10.1.4 pass at holdstate.go:190 then :254. The residue matters in direct delivery mode only; proxy mode is enforced server-side.
- **`docs/operator-guide/observability.md` is a curated operator subset rather than a §16.1 mirror.** It names 57 `lenny_` series and no `lenny_slot_*` or `lenny_adapter_*` row at all, and the one tier-11 test that opens it checks a single credential-rotation series. CODE-9 staging `docs/reference/metrics.md` alone is right.
- **`docs/reference/metrics.md`'s `## Adapter metrics` table is FIVE columns (`Metric | Type | Labels | Description | Used by`) against the gateway table's four,** its preamble already carries the "outside the default scrape set" deferral, and every sibling row repeats "Outside the default scrape set." in its description. CODE-9 names no column set, so the implementor reads it off the page and the untokened-entry row owes a `Used by` cell.
- **`tests/tier11_docs/adapter_metric_catalog_test.go` harvests names by regex over `pkg/adapter/metrics.go` and then demands each in BOTH `docs/reference/metrics.md` and §16.1,** so the missing `lenny_adapter_` prefix on `lenny_slot_shutdown_untokened_entry_total` is not a gate. SPEC-6 plus CODE-9 discharge it.
- **`docs/about/style-guide.md:31,:100` REQUIRES absolute GitHub spec URLs carrying the section anchor, and 53 such links already exist under `docs/`.** DOCS-2's two links are sanctioned by the project's own published guide; do not file them against `doc-content.md`.
- **The §7.4 mid-session upload path funnels EVERY adapter error into one envelope, `502 UPSTREAM_ERROR`** (`upload_to_session.go:126-137`, both the Prepare and the Finalize arm), so the rule-2 hold refusal and the rule-3 mid-session-create refusal need no §15.1 row, no error-catalog row and no OpenAPI change.
- **The exclusive-path `SandboxClaim` strand CANNOT last, because the WarmPoolController runs a leader-elected orphan-claim GC** whose `classify` arm covers `claimstate.Bound` and `claimstate.Recycling` and drains the pod once the orphan key outlives `--claim-orphan-timeout`, default five minutes, with no active session (`pkg/controller/warmpool/gc.go:34,:233,:274-277`). CODE-8's refusal arm is not the stranding case anyway: a typed refusal means another attempt or a live session owns that pod, so deleting its claim is what would be wrong.
- **`sessions_served` has exactly ONE production writer, `ScrubReporter.RecordSessionScrub`, and the adapter emits `ReportSessionScrub` from exactly one site, the `Shutdown` handler** (`pkg/adapter/session.go:274`). SPEC-3's biconditional is spec-to-tree reconciliation and never a relaxed reuse bound; the releases it excludes file nothing on the shipped tree either.
- **`lenny_pod_session_reuse_count` has NO production caller at all;** `ObserveSessionReuseCount` is referenced only from its own test. Conditioning the report therefore cannot perturb the PoolScalingController's `mode_factor` p50.
- **The §10.1 hold-timeout termination already files no report and says why in its own code comment** (`holdstate.go:215-218`, "the scrub report is the record of a scrub a Shutdown teardown performed, and this path performs none"), so SPEC-3's biconditional removes no control the tree enforces.
- **`RotateCredentials` and `ExtendCredentialLease` sit outside the hold and still cannot re-create a removed credential file:** both refuse with `FailedPrecondition, "slot %s has no assigned credentials"` when the registry holds no entry (`slotcreds.go:70-73`). Checked specifically as a hold-exemption hole; it is not one.
- **`migrations/` uses golang-migrate and carries no checksum gate (`pkg/schemamigrate/schemamigrate.go:25-27`), and the only assertions naming 0167 are over its columns,** so CODE-10's in-place comment edit is safe. In-tree precedent: `migrations/runtime_definitions_execution_mode_service_test.go:123-177` re-keys 0033 and 0084 source comments in place.
- **The adapter `ErrorCode` enum is NESTED inside `message Error`, so the generated Go constants are `adapterv1.Error_ERROR_CODE_*`,** which is the form CODE-7's type switch uses. `cmd/lenny-compliance/schemaassert.go` builds the conformance catalog by parsing the enum, so two new members only widen the accepted set and no test reconciles the enum against §15.1.
- **`examples/runtimes/echo/` does NOT exist in the tree although spec/15:1468 calls it the executable reference adapter,** so `pkg/adapter` is the only implementation the new §4.7.1 obligations can land in. Do not file a missing-reference-adapter edit site.
- **The three `completed` predicates stay consistent across the two files that state them, and there is no fourth spelling.** `Shutdown` and `terminateHeldSession` use `closeErr == nil && treeErr == nil`; `releaseSessionSlot` uses the tree removal alone, because it closes no runtime.
- **CODE-1's predicates agree with the §5.2 disposition table ROW FOR ROW, checked in both directions by at least four shards.** `exited_cleanly = closeErr == nil && (live || treeErr == nil)`, the `live`-gated report keyed on `closeErr`, and `completed` for the hold column each reproduce rows 1 through 5 exactly. Do not re-derive.
- **`shutdownReclaimOutcome`'s ARM ORDER means an unconditional teardown against an untokened entry does not increment the untokened counter,** because the `unconditional` case precedes the empty-token case. Both CODE-1's doc comment and CODE-9's row scope that counter to the superseded arm, so the narrower coverage is deliberate.
- **Defer order in CODE-1's removing arm is correct under LIFO: `defer unlockSlot()` registers first, so the hold release runs first and the guard unlock last,** and `answerShutdown` runs before both because it is evaluated in the return expression. A lens reading "deferred ahead of" as "runs before" inverts it.
- **The tier-3 reserved-number map at `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:77-96` independently confirms every SCHEMA-1 field number,** because the retired `slot_id` occupied exactly the reserved slots SCHEMA-1 skips. No tier-3 gate pins a field COUNT on any message SCHEMA-1 opens beyond the one `assertFieldSet`.
- **DECISION (`[index-reconcile.2]`): five unclosed log entries that were decisions rather than verification questions became summary open decisions 41 through 45,** stamped with those numbers because the log never numbered them: the life-of-the-pod hold answered with the transient `ABORTED` (41), the §4.9 timer ordering (42), §10.1.5 preemption (43), operator-guide rows (44), and a §16.1 row for `lenny_adapter_leaked_slots` (45).
- **DECISION (`[index-reconcile.2]`): checklist S4 now names all five of SPEC-3's §5.2 anchors, S5 depends on `S1, S4` and S8 on `S4, S5`.** Every other `Depends on:` resolves to an earlier step, the six spec steps lead, and no step names two lanes. `[index-reconcile.3]` re-parsed the whole 25-step list and changed nothing.
- **The deliverable index is COMPLETE and needs no edit: SPEC-1..6, SCHEMA-1, CODE-1..11, CONF-1 and DOCS-1..4 each appear exactly once,** and the index's id set is byte-identical to the id set the two staged-changes files carry. Verified independently by `[index-reconcile.2]` and `[index-reconcile.3]`.
- **THIRTY-SIX consecutive lenses returned EMPTY across `spec-recheck-2.2`, `spec.24` and `spec.25`,** on applicability, citations, client-surface, docs-alignment, edit-sites, fresh, kubernetes, mechanism, performance, reliability, security and single-source. The kubernetes lens is on its fourth consecutive empty verdict and the security lens on its fifth; both declare their candidate spaces exhausted on the spec staging.
- **The spec-lane delta across this whole window is TWO commits and both are reductions:** `a4e2ce503` (running-boundary vocabulary) and `deb2b9177` (the carrier-table prune, the emptied-tree clause and the ten-second provenance paragraph). A spec lens on this staging is a delta check against these Settled lines rather than a fresh derivation.
- **The singular phrasing "a teardown rule that removes no entry reports a clean exit" at the CODE-1 tier-1 bullet is TIED to `superseded`/`absent` and was deliberately left.** It is not the over-broad universal two verifiers confirmed false against rule 10; that one was deleted from CODE-4. Do not re-file it without evidence that a reader takes the clause as a universal.
- **ALL FIVE round-2 non-spec lenses returned EMPTY** (applicability, citations, docs-alignment, reliability, test-coverage), on a round-1→round-2 diff of three files: the checklist S7/S10/S14/S17 lines, the CODE-1→CODE-6 tree-removal seam move, the DOCS-2 `DemoteSDK` row conditionalisation, the CODE-9 gatewaymetrics test additions, and the summary index and open-decision-45 lines. A round-3 lens on this staging is a delta check against these Settled lines.
- **The tree-removal seam move from CODE-1 to CODE-6 is COMPLETE across the proposal, verified in both directions.** Its one home is non-spec-changes.md's `**The tree-removal seam.**` bullet; CODE-1's heading and the summary's CODE-1 index line dropped `pkg/adapter/slot.go` and CODE-6's heading already carried it; a grep for `removeSlotTreeVia`/`removeSlotTreeFn` over the non-log files returns only CODE-6 attributions and file-keyed entries, with no site still saying "which CODE-1 states".
- **The seam's step ordering is sound and creates no forward reference.** S14 (CODE-6) lands `removeSlotTreeVia`; S16 and S17 (CODE-1) consume it. Do not re-derive the S14↔S17 cycle question; homing the seam in CODE-1 is what would have created it.
- **Every repository citation the CODE-6 seam bullet carries re-verifies exactly, checked independently by three shards:** `removeSlotTree` at `pkg/adapter/slot.go:210-212`; the os.RemoveAll-returns-nil sentence at `pkg/adapter/slotlayout/tree.go:54-55`; the chmod-injection rationale at `pkg/adapter/warmlayout_test.go:162-167` (the earlier `:164-167` was narrower and also true; the injecting clause itself is at `:168`); `scrubDone` at `pkg/adapter/server.go:197-201`; `HoldAfterFunc` at `pkg/adapter/holdstate.go:63`; `ExpiryAfterFunc` at `pkg/adapter/credexpiry.go:49`; the three discards at `pkg/adapter/slotsession.go:217` and `pkg/adapter/holdstate.go:249,:254`. `terminateHeldSession` is a `*Server` method (`holdstate.go:229`), so `s.removeSlotTreeVia(m.state)` compiles there.
- **`removeSlotTree` has exactly THREE production call sites and the seam bullet names all three:** `holdstate.go:254`, `session.go:271`, `slotsession.go:217`. The closure "every site whose hold release reads a tree-removal result" is therefore complete; the command is `grep -rn removeSlotTree pkg/ cmd/ tests/ | grep -v _test.go`.
- **`releaseSessionSlot` closes NO runtime on any of its sixteen production call sites, re-verified with the call list.** The start-path rollbacks at `session.go:133,:147,:157` and `resume.go:69..141` all precede `noteRuntimeStarted`, and the `DemoteSDK` handler closes the SDK first and returns on its error before `anyRegisteredSession()`/`releaseSessionSlot` (`sdkwarm.go:280-300`). This is the tree fact the conditional DOCS-2 `DemoteSDK` row matches.
- **The reworded DOCS-2 `DemoteSDK` row is pinned by NO gate, and its conditional is carried at exactly three sites.** `grep -rn DemoteSDK tests/tier11_docs/` returns nothing, and the shipped adapter-contract gate asserts only the `Shutdown`-row substrings. "the session the pod holds if it holds one" appears at non-spec-changes.md:2731, summary.md:1142 and implementation-checklist.md:29, matching the staged §4.7 row in sense; no fourth site states it unconditionally. The `:64` docs anchor is still the `DemoteSDK` row.
- **CODE-10's second arm quotes the staged report predicate accurately.** The §5.2 source reads "when, and only when, that cleanup is one a `Shutdown` performs to reclaim a slot that reached `running`", and the arm's replacement wording is that predicate in noun form. The `released`-on-a-failed-tree-removal assertion relocated into CODE-1's started-session bullet agrees with the disposition table's `Not entered` cell for that row.
- **CODE-10's grep predicate is scoped to `pkg/ cmd/ tests/ migrations/ schemas/` and therefore CANNOT collide with the `docs/` carriers,** which the SPEC-3 carrier table sends to DOCS-4. DOCS-4 deletes the whole reporting clause, so the surviving docs sentence carries no universal and CODE-10's "deletion is not enough" arm never reaches it.
- **The CODE-9 gatewaymetrics test text checks out against the tree at every claim.** `TestCredentialAndLLMProxyAndSlotMetricsEmit` is at `gatewaymetrics_elicitation_test.go:536` with `IncSlotFailure("session_start", "pool-a", "sbx-1")` at `:550` and the exposition assertion at `:567`; `TestNewMetricsEmittersNilSafe` is at `:576` with the same call on a nil `*gatewaymetrics.Metrics` at `:585`. The asserted label order `k8s_pod_name` before `pool` is Prometheus alphabetical exposition order, as the shipped sibling line shows.
- **Extending `TestCredentialAndLLMProxyAndSlotMetricsEmit` in place needs NO new `// spec:` annotation,** because the case already carries `// spec: §16.1 and §5.2` at `gatewaymetrics_elicitation_test.go:533-535`.
- **Open decision 45's corrected attribution is right on BOTH halves, re-verified by four shards.** `lenny_adapter_leaked_slots` is registered and published by the gateway (`gatewaymetrics_credential.go:222-223`, accessor `SetAdapterLeakedSlots` at `gatewaymetrics.go:1213`) and nothing under `pkg/adapter` publishes it, while `spec/06:160` does attribute the value to the adapter's `/healthz` health metadata. The series has no row in `docs/reference/metrics.md` or `spec/16` today.
- **The untokened-entry counter is NOT uncovered; do not re-file it.** Its firing assertion is CODE-1's tier-1 bullet "The untokened-entry arm fires its counter", and its registration is gated by the unedited `tests/tier11_docs/adapter_metric_catalog_test.go`.

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
  the proposal files for this reason. Measured rate: line numbers into non-spec-changes.md drift by
  roughly ten per fix round, which is how the checklist S10 heading "Gateway tests for CODE-4, CODE-5,
  CODE-7 and CODE-8, tier 1" came to be cited eleven lines off. Re-grep the heading text.
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
- **TRIED AND WITHDRAWN, and the most expensive process lesson of this window: a PER-FILE carrier
  enumeration for CODE-10.** Rounds 3, 4 and 5 each appended carriers one at a time after a hand search,
  and each round's addition was the next round's finding. The set was mirrored in five places and every
  addition had to move all five. `[prune.5.fix.1]` deleted the heading list and the bullets and made the
  set a grep predicate; do not re-enumerate it and do not file "file X is missing from CODE-10".
- **MISTAKE: the `[prune.5.fix.1]` prune deleted BOTH of CODE-10's named exceptions along with the site
  enumeration, and the two-arm subject rule it left behind reproduced neither.** One of them, the tier-11
  addressing test's header comment, then took arm 1 and came out stating the very universal SPEC-3
  withdraws. The repair was the outcome-test rewrite of arm 1. When a prune replaces an enumeration with
  a rule, run the rule against every site the enumeration named before declaring the prune complete.
- **WATCHOUT: CODE-10's closure grep is LINE-BASED and the tree holds one carrier whose sentence wraps a
  comment-line boundary.** `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:454-461`
  splits "each session release" across `:456`/`:457`, so no single line matches. Adding
  `-e 'reports its outcome to the gateway'` closes it and returns nothing else new; a joined-consecutive-
  comment-line sweep finds exactly one wrapped carrier tree-wide.
- **WATCHOUT: the SPEC-3 carrier table declares the residual-state tables and "the tier-11 assertion over
  them" a NON-carrier, which is true of the assertion and FALSE of the same test's `// diagnosis:`
  comment.** `TestPerSlotCleanupStatedOnEverySessionModeRow` pins only the substring "Per-slot cleanup",
  while its diagnosis tail carries the DOCS-4 sentence word for word. A lens that reads the exclusion
  clause and stops will miss the comment; it is CODE-10's to close.
- **MISTAKE: CODE-10 put `IncrementSessionsServed` on the delete arm while its own arm rule, its
  reasoning sentence and the summary roster all send a served-count trigger to the re-key arm,** leaving
  the two `agentpodstate.go` comments on opposite arms inside one file. The same class had been found and
  fixed for three sibling comments the round before. When a fix moves sites between arms, re-walk the
  whole file rather than the named site.
- **MISTAKE: an earlier round staged the leaked-disposition assignment and its explanatory comment TWICE
  for ONE landing site, and the two copies drifted.** The standalone copy generalised to "every teardown
  rule that removes no entry answers `exited_cleanly` true", which staged rule 10 falsifies; the
  `materializeSlot` copy cited rule 15 and was right. It cost a finding, a design pass and four edits
  across three files. One landing site takes one staged block.
- **The over-broad "every rule that removes no entry answers a clean exit" is FALSE and must never come
  back, in any carrier.** Rule 10 removes no entry and answers `INVALID_ARGUMENT`; rule 15 is narrow to
  rules 11 and 13. It had four carriers, two code comments plus the lead-in prose and an accepted-failure
  bullet, and the remedy at each was deletion rather than a qualifier.
- **WATCHOUT: adding a deliverable to this proposal is a FIVE-PLACE edit plus the checklist, and two
  consecutive rounds forgot the fifth.** The places are the block in non-spec-changes.md, the `## Testing`
  line, the `## Files touched on application (non-spec)` bullet, the summary's Deliverable Index bullet
  and the SPEC-3 carrier table, plus a new checklist step. Renumbering invalidates every dependency line,
  which is why a new step goes last.
- **WATCHOUT: when deleting a restating sentence, read the sentence AFTER it for a demonstrative pointing
  into the deleted span.** The `**The token.**` deletion left a successor opening "That write rule is what
  makes the mechanism monotone"; shipped verbatim it would have been a broken referent. A dangling
  referent is the next round's finding.
- **WATCHOUT: a round's finding line numbers drift while that same round's earlier fixes land.** One group
  found its targets twenty lines off because an earlier group had inserted CODE-11 into the same file.
  Anchor on the text, never on the cited line, and schedule the group that shortens a file last.
- **WATCHOUT: two proposal anchors name a test by its UNSUFFIXED name while the tree carries
  `_spec_5_2`.** `TestShutdownSlotEmitsReleasedOnCleanClose` and
  `TestReporterSessionScrubDrivesPerReleaseRetirementWithPostIncrementCount` are each the doc comment's
  own first word, which is this repo's comment convention, so both anchors resolve uniquely. At least six
  shards have reached for this; do not file it and do not "fix" it into a real mismatch.
- **WATCHOUT: `Server.reclaiming` and `Server.slotGuards` are never seeded, and `New` initialises no map
  at all.** `reclaimSlotLocked` inserts into the first and both guard hand-out forms create entries in the
  second, so a nil map is a write panic, and under `RestartPolicy: Never` that ends the pod. Whoever lands
  CODE-6 seeds both in `New` AND lazily, because the package's own tests build `Server` by struct literal.
- **WATCHOUT: `pkg/adapter/gatewaylink.go:70-77` carries the withdrawn universal in TWO fragments,
  `per-session-release` and `on every slot release`,** so deleting the second alone leaves it standing.
  Its paired test file repeats the same fragment in a `// diagnosis:`, which a sweep over non-test Go
  misses entirely; `gatewaylink.go` and `gatewaylink_test.go` are the worked example of that whole class.
- **WATCHOUT: `pkg/controller/sandbox/podspec/podspec.go` has FOUR `ReportSessionScrub` comments and
  exactly three are carriers.** `:168`, `:591` and `:906` say "reports each per-slot cleanup outcome";
  `:687`, the embedded-runtime comment, carries no quantifier and is correctly excluded. The `:591` one
  wraps the phrase across two source lines, so a one-line grep returns only two of the three.
- **Do NOT re-file `docs/reference/state-machines.md:248`, `metrics.md:163`, `configuration.md:91` or
  `execution-modes.md:23` as missed carriers of the served-count re-key.** Each mirrors §5.2's
  `**Session count limit:**` bullet, which SPEC-3 leaves untouched and which still keys on "the session
  release that drives the served-session count". Filing one asks a doc to diverge from its own home.
- **WATCHOUT: the checklist is the least-read file in the lane, and S17 is where the `bound`/`started`
  drift survived longest.** Two mutually exclusive report gates and a runtime teardown left behind the
  binding all lived inside one step. When a fix rewords CODE-1, grep `implementation-checklist.md` and
  `summary.md` for the reworded claim before declaring the fix complete.
- **WATCHOUT: `pkg/adapter/usage_test.go:233` contains "has been given it", one of the phrases the
  running-boundary directive bans.** The ban is on the five proposal files rather than on the tree, and no
  deliverable opens that comment. Do not file it and do not widen CODE-2 to sweep it.
- **DISAGREEMENT inside one round: the three files-touched bullets still spelling the bare
  `removeSlotTree` after the seam move.** The sites are non-spec-changes.md:3969
  (`releaseSessionSlot`), :3983 (`terminateHeldSession`, which CODE-6's body now binds as
  `treeErr := s.removeSlotTreeVia(m.state)`) and summary.md:213-215, whose `server.go` clause omits the
  new `removeSlotTreeFn` field. `[non-spec.2.review-applicability.1]` judged all three below the bar and
  said not to re-file them, on the ground that they are per-file summaries of a rule stated once
  elsewhere and that a site left on the bare call fails CODE-6's own tier-1 assertion loudly.
  `[non-spec.2.review-docs-alignment.1]` filed the `holdstate.go` one as a finding in the same round.
  Neither corrects the other. Whoever opens those bullets for any other reason folds the spelling in
  then; the two readings agree that no separate finding is worth a round.
- **WATCHOUT: the seam move redistributed THREE assertions across two deliverables and a lens reading
  only CODE-6 will think two were lost.** CODE-6 kept the hold-retention assertion and made it
  table-driven over `releaseSessionSlot` and `terminateHeldSession`; the pre-`running` non-clean-exit
  assertion lives in CODE-1's "`exited_cleanly` on a reclaim the runtime never held" bullet; the
  `running`-arm hold-plus-`released` pair moved into CODE-1's "The runtime's own answer still decides"
  bullet. Nothing was dropped. Read both deliverables before filing a missing-coverage finding against
  the seam.

### Open

- **Does a permanently held slot identifier have to be answered as permanent?** — OPEN, now summary open decision 41 and with the human: a life-of-the-pod hold repeats `ABORTED`, which §15.4 publishes as retryable; the entry reaper is routed to remediation position 2, and neither the review loop nor the open-decisions phase derived a recommendation.
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
- **Does `lenny_adapter_leaked_slots` now need a §16.1 row?** — OPEN, now summary open decision 45 and with the human, with no recommendation from either the loop or the open-decisions phase: no catalog or docs row exists, the series is gateway-registered despite its name, and whether `/healthz` `leaked_slots` is implemented at all is the separate `UNVERIFIED` below.
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
- **Does the gateway's preStop drain hand a mid-bind session to another replica, or fail the bind on the draining replica?** — UNVERIFIED [spec.20.review-performance.1]: if there is no mid-bind handoff the preemption trigger narrows to lease expiry and partition rather than disappearing.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** — UNVERIFIED [spec.18.review-performance.1], corrected by [spec.20.fix-G1.1]: the clause it quoted ("as a reclaim whose act fails does") is deleted and the site moved, but the question stands. One leaked slot retires the pod there and the spec publishes no bind-failure rate to multiply.
- **Does `s.reclaiming` grow without bound over a long-lived recycling pod?** — UNVERIFIED [spec.18.review-reliability.1]: one entry per cleanup whose act failed, held for the life of the pod, and the adapter process survives pod reuse, so nothing sweeps the map. Code-lane.
- **Should SPEC-5 also correct `spec/06:290`?** — OPEN, for a human [spec.18.fix-G1.1]: the shipped §6.2 Client-visibility bullet attributes a workspace-validation envelope to the §15.1 finalize precondition note, which states none. Pre-existing, and SPEC-5 edits a different clause of the same bullet.
- **Does the widened §15.1 row owe `RunSetup`'s third deterministic `FailedPrecondition` producer?** — UNVERIFIED [spec.18.review-fresh.1]: "adapter is not configured with a workspace root" (`pkg/adapter/staging.go:345-349`) maps to 422 the same way. The row was equally non-total before SPEC-5, and the "row is total" sentence has since been deleted.
- **Does `docs/runtime-author-guide/` owe a mirror of §15.4's two published blocks?** — UNVERIFIED [spec.12.review-client-surface.1], narrowed by [spec.24.review-docs-alignment.1]: `docs/reference/adapter-contract.md` mirrors §15.4's RPC tables and no §15.4 prose block at all, not even the shipped SDK-warm demotion contract, so the reference page owes none; only the runtime-author-guide half is still open, and the remedy is a docs edit.
- **Does `docs/reference/adapter-contract.md` owe a mirror of the §4.7.1 numbered-rule block?** — UNVERIFIED [spec.15.review-client-surface.1]: DOCS-2 stages four row and paragraph edits only; whether the tier-11 adapter-contract gates want the rules is untraced.
- **Where is the persistent `leaked` ledger behind the `ceil(maxConcurrentSessions / 2)` replacement trigger stored, and does it have a §12.4 durable fallback?** — UNVERIFIED [spec.22.review-performance.1]: the staging routes new pre-`running` leaks into that ledger, so a Redis-backed volatile store would forget pods that should retire on a reset. Pre-existing rather than staged.
- **Do the sentence-repeat sweeps disagree, or did their scopes differ?** — UNVERIFIED [compaction passes 29 and 30]: round 19 reported one repeat, round 22 six over four proposal files, and `[spec-recheck.5.review-single-source.1]` two over `spec-changes.md` alone. The scopes differ, so all three may be right; `### Settled` carries the two-over-`spec-changes.md` result and the six-over-four-files result side by side. Fix the scope once and stop re-running it.
- **Does the §28.4 `ABSENT` claim-register row's precedent sentence name the wrong status?** — UNVERIFIED [spec.16.review-citations.1, spec.19.review-citations.1]: every `coordination_generation` fence row in `tests/claim-map.json` is `UNWIRED`, and only "In-flight RPC cancellation on a generation gap" is `ABSENT`. Judged decorative twice, because the sentence states the status it wants explicitly.
- **Is a `credentials.json` left by a failed cleanup on a concurrent pod reachable by a later session through the shared runtime process?** — UNVERIFIED, the surviving companion of resolved decision 38: `pkg/adapter/slot.go:219-225` returns one `Runtime` for every registered session. Answering it the other way would reverse decision 38, so it is recorded inside the new unstaged-defects row as its own finding against the isolation model. Nothing opened has established filesystem reachability of another slot's credential path.
- **Does DOCS-1's own-voice trigger cell for the new `receiving_uploads → slot_cleanup` row still agree with the paragraph?** — UNVERIFIED [spec-recheck.4.fix-design-G1.1]: the reduction to a pointer did not touch the docs row, and nobody re-read that cell against the paragraph's current wording.
- **Would any gate newly fail because of text the staging ADDS, rather than text it replaces?** — UNVERIFIED [spec-recheck-2.1.review-applicability.1]: gate breakage was checked only for replaced lines. Nothing in `tests/tier11_docs` looked like a structural counter over the §4.7.1 block's new rows or bold paragraphs, but nobody enumerated them.
- **Does S23's tier list "0, 11" agree with DOCS-4's own "Tiers: 0"?** — UNVERIFIED [non-spec-recheck.1.review-applicability.1]: judged immaterial, because running the docs tier costs nothing.
- **In which step do SCHEMA-1's three claim-register rows land?** — UNVERIFIED [non-spec-recheck-2.1.review-applicability.1]: SCHEMA-1 says all three at S9, CONF-1 says the `ABSENT` row lands with the tier-10 file at S22, and checklist S9 names the two `WIRED` rows. The gates tolerate either placement; settle it in ONE home.
- **Can a recycle-carrying `Shutdown` answering `absent` start the whole-pod scrub while a DIFFERENT slot's `releaseSessionSlot` or §10.1.4 termination is still inside `removeSlotTree`?** — UNVERIFIED [non-spec-recheck.1.review-security.1]: a real ordering question, outside the arm resolved decision 35 describes.
- **Does §5.2's "still in flight" mean "not yet admitted"?** — UNVERIFIED [non-spec-recheck-2.1.fix-design-G1.1]: §5.2 says the hold refuses a §7.4 mid-session upload still in flight when the cleanup opens the hold, while CODE-6 says an upload already past its resolve "is not being admitted and re-enters no resolve". Pre-existing and independent of the scope narrowing.
- **Does the ten-second provenance paragraph belong in SPEC-3's commentary at all?** — OPEN [spec-recheck.1.review-applicability.1], and now larger: the caller's prune directive bars adding a rationale sentence to any SPEC-n commentary, and resolved decision 40 appended six more sentences to that same paragraph. Whoever runs the next prune decides whether the provenance belongs there or whether the figure standing in the staged §5.2 sentence is the whole statement.
- **Does rule 8's take-back, stated outside the registry critical section, let a lagging attempt close a successor's session?** — OPEN, dropped below the bar [spec-recheck.4.review-reliability.1] and recorded so it is not re-derived: reaching it needs attempt A descheduled across a whole reclaim, cleanup, hold release and fresh bind, and the reclaiming `Shutdown` has already torn A's runtime down on the admission precondition, so the take-back is belt-and-braces on every reachable ordering. The area is what the `**A start that races the reclaim.**` Edge-cases bullet records.
- **Can `terminateHeldSession` be skipped for a member whose hold pass 1 already opened?** — UNVERIFIED [non-spec-recheck-2.1.review-reliability.1]: no early return after pass 1 exists, so the only skip is a panic, which ends the process under `RestartPolicy: Never`. Do not spend a round on it unless a later staging adds a per-member `continue`.
- **Does checklist S22 (CONF-1) owe S18 in its `Depends on:`?** — OPEN, FILED [non-spec.1.review-applicability.1]: CONF-1's battery drives rule 8, the start-confirmation rule, which CODE-2 at S18 implements, and S18 is in neither S22's `Depends on:` nor its transitive closure. Depends-on COMPLETENESS is a different check from "resolves to an earlier step", and only the latter had been run.
- **Does `Binder.shutdownAdapter`'s retire arm set `unconditional_teardown`?** — UNVERIFIED [spec.24.review-security.1, re-verified by spec.25.review-security.1]: `binder.go:2043` is a fourth gateway-side plain-`Shutdown` caller beyond the three the SPEC-1 commentary enumerates, and under rule 10 a plain `Shutdown` is answered `INVALID_ARGUMENT`, which would make a retire-path teardown a no-op. Code-lane.
- **Is tier 5 reached by CODE-5's third accounting caller?** — UNVERIFIED [non-spec.1.review-mechanism.1]: `DrainSandbox` and the `lenny.dev/drain-request` merge patch now fire in configurations that never reached them, and the checklist lists no tier-5 line anywhere. The drain mechanism itself is untouched and tier 2 covers the patch, but no shard records the question being asked. An applicability lens should settle it once.
- **Do `scrubreporter_seams.go:389-391` and `scrubreporter_seams_test.go:726-727` fall inside CODE-10's predicate?** — UNVERIFIED: both are served-count-per-release statements of the listed class and were never dispositioned individually; the application-time grep decides, and the arm rule must produce an answer for each.
- **Is `pkg/adapter/sessionscrub_emit_test.go:16-19` stale against its own terminate case?** — UNVERIFIED, a disagreement this pass did not settle: the header says the recording double exists partly because "the withhold and terminate paths emit none", while `TestTerminateShutdownEmitsTheSessionScrub_spec_5_2` at `:248-260` asserts a terminate does emit one. Pre-existing, outside the carrier set, and it needs someone to say which "terminate" each sentence means.
- **Does the `outcome` argument of CODE-9's `SlotReclaim` hook have a label to land in?** — UNVERIFIED [non-spec.1.review-fresh.1, re-raised independently by non-spec.2.review-applicability.1 and non-spec.2.review-test-coverage.1]: the hook is `func(outcome, pool, podName string)` while the §16.1 row labels only `pool` and `k8s_pod_name`, and the forwarder fires only on `superseded`. The open question is now precise: whether `Metrics.IncSlotCompensationSuperseded` takes and ignores the outcome so it is directly assignable at `cmd/lenny-gateway/metricsbackfill.go`, or whether the wiring is a closure. Three shards have declined to file it because any value works and nothing downstream reads it; a mechanism or wire lens with a view on accessor signatures should settle it once.
- **Is the POSITIVE arm of the reclaim-hold release at `releaseSessionSlot` pinned by any listed case?** — UNVERIFIED [non-spec.2.review-reliability.1]: a successful tree removal ends the hold, so a later bind on the same identifier is admitted. The park-and-release case drives that arm only at the §10.1.4 termination, because it parks inside `Runtime.Close` and `releaseSessionSlot` closes none, and the tree-failure case pins only the retained arm. Not filed: a stale hold blocks one session identifier on one pod and a retry lands on a fresh pod. Whoever wants it should first check whether a shipped `pkg/adapter` case already does release-then-rebind through `ReleaseSlotForTest`; the new `removeSlotTreeFn` seam is a usable park point.
- **Is the `pkg/adapter/slotsession.go` files-touched wording "its discarded `removeSlotTree` error turned into a logged warning" a defect?** — UNVERIFIED [non-spec.2.review-docs-alignment.1]: it reads as a description of the shipped discard rather than of the new seam call, which is why it was not filed alongside its `holdstate.go` sibling. A round deciding the sibling is a defect decides this one the same way.
- **Is the `/healthz` `leaked_slots` half of spec/06:160 implemented at all?** — UNVERIFIED [non-spec.1.review-citations.1]: no handler publishing that field was found; the hits are comments, the `slothealth` ledger and `slotstate/registry.go`. Whoever answers open decision 45 should establish this first.
- **Does an implementor's reflow of the podspec and gatewaylink comments shift two `tests/claim-map.json` line surfaces?** — UNVERIFIED [non-spec-recheck.4.review-mechanism.1]: `podspec.go:607` and `gatewaylink.go:36-79` are code line surfaces in the register, and the tier-0 gate rejects only a BARE line surface, so nothing catches the drift. The implementing step re-checks both rows after the edit.
- **Is `pkg/adapter/manifest_fields_test.go:220` an omission from the Tests list?** — UNVERIFIED, refuted once at the materiality gate: it calls `ensureSlotStateLocked` and so takes CODE-6's widening, while sixteen other adapter test files are enumerated. Someone should decide whether that list is meant to be closed.
- **Should checklist S24 be reduced to CODE-10's predicate form?** — OPEN [prune.5.fix.1]: S24 still states the carrier set by category, which is the last mirror of a set CODE-10 no longer enumerates. The prune's write scope admitted only S25. The replacement text the prune wrote out is "The comment carriers CODE-10's grep returns take the reduction that deliverable states, under its rule and its IMPLEMENTOR'S CHOICE constraint. No assertion and no cited section number moves."
- **Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?** — OPEN, recorded in the summary's unstaged-defects rows: a CREATE, `bound` patch and DELETE inside one reconcile window leaves `("", false)` and an idle unscrubbed pod, because the per-pod `SandboxClaim` carries no production finalizer. This is the one genuine Kubernetes-idiom hazard in the neighbourhood, it is correctly scoped out of this proposal, and a later reviewer should not re-file it.
- **Do open decisions 41 and 45 get a recommendation?** — OPEN, for the human: both left the review loop carrying the question and its ground alone, and the open-decisions phase supplied no recommendation, alternatives, cost or confidence for either.

### Deferred

- DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start`
  failure branches release the slot by session identifier alone, so a lagging one deletes a later
  attempt's entry and tree. Carried in the summary's unstaged-defects section; do not re-file here.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline
  lets the adapter apply its default grace period" is false; `resolveShutdownGrace` prefers the caller
  ctx's remaining time. CODE-7 opens the file, and no deliverable stages the one-sentence repair.
- DEFERRED [pkg/gateway/podlifecycle/podsession/binder.go:1187-1201, `Binder.drain` doc comment]: it
  states the projection keying SPEC-4 retires, "On a `recycle.enabled: false` pool a claim DELETE
  projects `draining` then `terminated` ... on a recycling pool under its limits the projection returns
  the pod to `idle`". What is true instead: after SPEC-4 the projection reads the recycle setting
  nowhere, and the projected phase at the DELETE selects the edge, `reserved → idle` and
  `claimed → draining`, on a pool of either recycle setting. `failPhase`'s own call is exactly the
  falsified case, because it drains a pod projecting `claimed` on a recycling pool. The carrier is
  unstaged: CODE-10's predicate is scoped to the withdrawn reporting universal and CODE-11's to the
  narrowed `SETUP_COMMAND_FAILED` cause, so neither grep returns it, and DOCS-1 covers only
  `docs/reference/state-machines.md:138`. It lands in non-spec-changes.md, as a site added to CODE-4 or
  CODE-8, both of which already open the file.
- DEFERRED [schemas/lenny-adapter.proto, `Shutdown` RPC comment and `ErrorCode` enum header]: the RPC
  comment predates the two-teardown split, and "The catalog below mirrors spec §15.1" is false for
  codes 27, 28, and 29. SCHEMA-1 stages the `ReportSessionScrub` comments only.
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
- DEFERRED [tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go]: checklist S4's
  sweep deletes that file's spec/12 substring assertion at :172-177, after which the file checks
  nothing in spec/12, while two references to spec/12 stay behind. Header-comment item 2 at :20-21
  still names "§12 (the `sessions_served` schema prose and column comment)" as a landing site, and the
  file-level `// spec:` annotation at :47 still credits "12.1 (sessions_served schema)". What is true
  instead: the file's spec/12 coverage ends with S4, so item 2's §12 clause and the 12.1 credit come
  out with the assertion. The SPEC-3 sweep sentence covers the assertion alone. Check
  `tests/spec-map.json` for a 12.1 credit on this file before removing the annotation. It lands in
  non-spec-changes.md, as a line added to S4's tier-11 sweep in `## Testing`.
- DEFERRED [schemas/lenny-adapter.proto, the LEAKED comment's drain-ledger sentence]: "The gateway
  feeds the outcome into the unhealthy-threshold ledger behind the `lenny.dev/drain-request`
  annotation" may itself be a restatement of a rule whose home is §4.6.3/§5.2. SPEC-3 does not falsify
  it, so it stands; a later round can file the reduction to a citation as its own finding.

### Retired

Bodies are in the archive file. This list names subjects only.

- Retired in compaction pass 32: nothing. All five round-2 non-spec lenses returned EMPTY, so no `OPEN`,
  no `UNVERIFIED` and no `DEFERRED` was closed, and the window's one filed finding (the `holdstate.go`
  files-touched bullet still spelling the bare `removeSlotTree`) is unrepaired and stands as a Trap
  carrying both shards' readings. No Settled text was superseded: the window's only corrective act was
  widening the `warmlayout_test.go` citation range, which is folded into the seam-citations Settled line.

- Retired in compaction pass 31, Deferred entries closed by evidence in this window: the
  `docs/operator-guide/troubleshooting.md` `setup_command_failed` row (the `reason` there is a
  `lenny_warmpool_warmup_failure_total` error_type label in the warm-pool-exhaustion table, not the REST
  envelope reason, so SPEC-5 and DOCS-3 owe it nothing); the `schemas/lenny-adapter.proto:255-257`
  `GatewayControl` service comment (settled as a non-carrier, unquantified "on release", declined by at
  least six rounds); the `spec-changes.md` reclaim-hold-scope entry (both texts it named as false are
  gone at HEAD, and §5.2's hold paragraph now reads "admits no request [§4.7.1] enumerates as governed by
  its admission rules, whether that request would create a registry entry under the identifier or resolve
  one", with the §7.4 upload named in the same sentence); and the
  `schemas/lenny-adapter.proto:445-448` `SESSION_SCRUB_OUTCOME_LEAKED` comment (SCHEMA-1 does stage the
  verbatim/becomes pair, keeping the drain-ledger sentence and the `spec:` line, and both sites that
  would have undercounted are already count-free).
- Retired in compaction pass 31, Open entries closed: the two carried DISAGREEMENTS, on
  `troubleshooting.md:41` and on the `SESSION_SCRUB_OUTCOME_LEAKED` staging, both settled in favour of
  the same side by three independent shards; "Are `gatewaylink.go:71` and `podspec.go:168,:591,:906`
  missing carrier-table rows?" (confirmed carriers, `:687` confirmed a non-carrier, and all four are now
  closed by CODE-10's predicate); "Does `docs/reference/metrics.md`'s `## Adapter metrics` table use the
  column set CODE-9 assumes?" (five columns, deferral sentence present); "Does
  `docs/operator-guide/observability.md` owe rows for the two SPEC-6 counters?" (no, resolved as open
  decision 44); "Does wiring the adapter scrape target move CODE-9's staged rows?" (folded into open
  decision 33 by `[index-reconcile.2]` rather than carried as an entry of its own); "Can an entry stamped
  with a dead token on the EXCLUSIVE path strand the pod's `SandboxClaim` forever?" (the WarmPoolController's
  orphan-claim GC reaches it after `--claim-orphan-timeout`); "Is the §4.9 timer cancellation ordered
  wrongly against the credential-file removal?" (resolved as open decision 42, staged sentence stands,
  recorded as an unstaged-defects row); "Does a coordinator handoff or preemption leave the §7.1 reclaim
  unsent?" (resolved as open decision 43, the Edge-cases bullet stands as staged); and the
  "Do the two non-`Shutdown` hold-retention arms need a seam and a test?" half that `[non-spec.1.review-reliability.1]`
  filed, discharged by homing the `removeSlotTreeFn` seam in CODE-6 at S14.
- Retired in compaction pass 31, superseded Settled text: every entry describing CODE-10 as a
  fourteen-file enumeration, and the 0079 impact row's file-level no-collision ground, both superseded by
  `[prune.5.fix.1]`'s predicate form and by the comment-only rule the row is now keyed to.

- Retired in compaction pass 30, Deferred entries discharged by `[index-reconcile.1]` or by the staging: `pkg/sandbox/slotstate/slotstate.go`'s two doc comments (CODE-3 now stages the four constant docs, the three edge-list glosses, `slothealth.go`'s two sentences and the three further carriers in `registry.go`, `sessionserver.go` and `gatewaymetrics_credential.go`, and checklist S8 names the same set); DOCS-3's retryable-fallback age claim; CODE-4's `Targets:` and files-touched halves, both now present; CODE-6's `ConfigureWorkspace` idempotent-repeat double lock, now an `allowStarted` field on the resolve descriptor; CODE-9's "verbatim" adapter-deferral sentence; the resume-path test bullet's assertion; the tier-7a rule-8 start-step case; the logging and count wording; the `**The mid-session conditioning.**` restatement and the CODE-6 rule-4 comment; CONF-1's reclaim-hold attribution, now §5.2; the scrub-outcome comment count in both carriers; implementation-checklist S1, S4, S5, S7, S15, S16, S19, S22, the S2 final sentence at :19 and the second carrier at :17; summary.md:119's false scope phrase; DOCS-2's `:393` precedent claim; the §16.1 carrier list's "row"; and the `pkg/proto/adapter/v1/lenny-adapter.pb.go` regenerated copy, which is a negative record because `make generate-proto` lands it in the same commit.
- Retired in compaction pass 30, Open entries closed: "Is the staged §5.2 ten-second graceful window the right figure, and does it want an operator override?" and "Is the ten-second graceful window an operator-tunable constant?" (both closed by open decision 40, resolved as staged: the window is fixed and the override rule's antecedent fails); "Does the whole-pod scrub racing the per-slot cleanup on the removing arm want an ordering statement?" (closed by open decision 35, whose premise was false); "Does disposition-table row 3 withdraw a residual-state control without recording the withdrawal?" (closed by open decision 38, resolved as staged and recorded as an unstaged-defects row; its credential-reachability companion stands in `### Open`); "Do S16, S18 and S21's `Depends-on` lines owe S15 and S1?" (discharged at HEAD by `[index-reconcile.1]`); "Does the carrier table's completeness claim survive a LOOSER grep than round 12 ran?" (answered yes by `[spec-recheck.4.review-edit-sites.1]`, whose wider pattern found every carrier already in the table); and the round-19-versus-round-22 form of the sentence-repeat question, replaced by a three-scope form that also carries `[spec-recheck.5.review-single-source.1]`'s count.
- Retired in compaction pass 30, superseded Settled text: the round-22 entry recording the whole-file sentence-repeat sweep as six repeats over four files stands, and `[spec-recheck.5.review-single-source.1]`'s two-over-`spec-changes.md` result is recorded beside it rather than replacing it, because the two sweeps had different file scopes and neither corrects the other.

- Retired by `[prune.5.fix.1]`: the Open "Can the reclaim-hold refusal reach `Binder.Prepare`/`Binder.Launch` and drain the pod?" (CLOSED: the refusal is reachable there only on a pod that is already draining or that carries a failed cleanup's residue, so no healthy pod is drained; argument in the `### Settled` entry of the same name and in `[prune.5.fix.1]`).
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

### [non-spec.3.fix-G1.1]

DECISION: closed the "refused Shutdown suppresses the recycle scrub is untested" finding by extending the existing four-row two-field-precondition case in the CODE-1/CODE-2 tier-1 Testing section, giving the two fail-closed rows a `RecycleScrub` disposition and one assertion that no scrub starts and no `ReportPodScrub` is filed — BECAUSE the rule already has one normative home (staged §4.1 scrub sentence conditioned on "a request that passed the teardown-pairing rule", plus §4.7.1 rule 10 "changes nothing"), so what was missing was a case asserting what the home gives, not more prose — ALTERNATIVES: a fifth standalone Testing bullet (re-describes the rule in test vocabulary and grows the list for one assertion); amending the staged §4.1 sentence or rule 10 (spec lane locked, and it would be a restatement, since §4.1 already excludes a request that failed the pairing rule); editing CODE-1's staged doc comment; adding an explicit guard around the scrub in the CODE-1 code block.

WATCHOUT: CODE-1's staged doc comment says the scrub "runs on every outcome, refusals included", and that reads at first pass as covering the two-field precondition. It does not: "refusals" there scopes to the two SUPERSEDED arms inside `answerShutdown`, and the precondition's `INVALID_ARGUMENT` returns before `answerShutdown` is reached. Do not "clarify" that comment — EVIDENCE: non-spec-changes.md:191-205 (early return), :297 ("runs clause three"), :506-510 (the precondition performs nothing, the scrub included)

FACT: the shipped handler runs the scrub as an unconditional trailing statement, `if rc := req.GetRecycle(); rc != nil { s.startPodScrub(rc) }`, reached on every path below the empty-id return, and `startPodScrub` both runs `scrub.Run` and files `ReportPodScrub` — EVIDENCE: pkg/adapter/session.go:288-290, pkg/adapter/podscrub.go:65,:78

FACT: the Testing section's closing sentence for the two recycle-scrub cases used to read "The two cases together pin clause three as running on every outcome"; it is a count and it went stale the moment the precondition rows gained the assertion. Rewrote it to name the set (absent arm, removing arm, precondition rows) — EVIDENCE: non-spec-changes.md:3156-3159

FACT: nothing else in the non-archive proposal files enumerates or counts the CODE-1/CODE-2 tier-1 cases. The "Scope accounting to record in the deliverable" paragraph after the Testing section, the CODE-1 summary bullet, and the checklist steps that name CODE-1/SPEC-1 all make no coverage claim, so this fix cascaded nowhere else.


### [non-spec.3.fix-design-G1.1]
DECISION: close the "refused Shutdown must not scrub" test gap by extending the EXISTING four-row two-field-precondition bullet (non-spec-changes.md, "Adapter tests for CODE-1 and CODE-2, tier 1"), giving its two fail-closed rows (neither field, both fields) a `Recycle` disposition and one assertion that no scrub starts and no `ReportPodScrub` is filed — BECAUSE the rule already has exactly one home (staged §4.1 scrub sentence, conditioned on "a request that passed the teardown-pairing rule", plus rule 10), so the gap is a missing test case, not a missing rule; a test case asserts what the rule's home gives and adds no second statement — ALTERNATIVES: (a) a fifth standalone bullet "the scrub does not run on a refused request" — rejected, it re-describes the rule and grows the inventory for one assertion; (b) editing spec-changes.md §4.1 to spell out the refusal exception — rejected twice over: the spec lane is LOCKED for this run, and the §4.1 conditional already excludes a request that failed the pairing rule, so the edit would be a restatement; (c) moving the scrub statement into CODE-1's comment block — rejected, that block already scopes "every outcome, refusals included" to the two SUPERSEDED arms inside `answerShutdown` and is consistent as written.
FACT: the two-field precondition returns before `answerShutdown` is reached, so the CODE-1 comment's "refusals included" language is NOT about it; the precondition's exclusion of the scrub is stated only at non-spec-changes.md:506-510 — EVIDENCE: non-spec-changes.md:191-205 (early return), :299-311 (comment scope), :506-510.
WATCHOUT: the tempting implementation this test must kill is a `defer` or a top-of-handler placement of the scrub in `answerShutdown`'s single-exit restructure; it compiles and keeps every currently-listed case green while firing a pod-wide process kill, credential purge and workspace scrub on a request the adapter refused — EVIDENCE: pkg/adapter/podscrub.go:40-80, :64-70, :78; pkg/adapter/session.go:288-290 (today's trailing-statement placement).
FACT: the fix is purely additive to one bullet; the "Scope accounting to record in the deliverable" paragraph that follows the Testing section makes no test-count or coverage claim, so nothing downstream is falsified — EVIDENCE: non-spec-changes.md:3160-3166.

### [non-spec.3.review-applicability.1]

VERDICT: one finding. The whole checklist, the lane/order/depends-on graph, the docs anchors and
the relocated tree-removal seam all verified clean.

FACT: the round-2→round-3 delta is small and entirely the tree-removal seam relocation plus two
test bullets. `diff -ru -x '*.review-log*.md'` against
`scratchpad/cp-snap/0081-opt2/non-spec-r1-prefix` is 246 lines. The seam
(`Server.removeSlotTreeFn`, `Server.removeSlotTreeVia`) moved whole from CODE-1 to CODE-6's new
`**The tree-removal seam.**` bullet (non-spec-changes.md:1485-1504); BOTH legs are staged (CODE-1's
paragraph deleted at the old :512, destination text complete), CODE-1's header and the summary's
CODE-1 index line both dropped `pkg/adapter/slot.go`, CODE-6's header and summary line both carry
it, and checklist S14 now names the seam while S17 cites it. No content lost, no forward
reference: S16/S17 depend transitively on S14. — EVIDENCE: non-spec-changes.md:158, :1485-1504,
:3943-3946; implementation-checklist.md:43, :49

FACT: the three `removeSlotTree` call sites in the tree are exactly `pkg/adapter/session.go:271`,
`pkg/adapter/slotsession.go:217`, `pkg/adapter/holdstate.go:254`, and CODE-6's seam bullet routes
all three through `removeSlotTreeVia`. There is no fourth. — EVIDENCE: `grep -rn removeSlotTree
pkg/ tests/ cmd/`

FACT: checklist audit ran clean and need not be repeated unless the checklist changes. Every
deliverable (SPEC-1..6, SCHEMA-1, CODE-1..11, CONF-1, DOCS-1..4) appears in exactly one step or in
a stated split (CODE-4 = S13+S19, CODE-6 = S14+S15+S21, CODE-1 = S16+S17+S21); every step carries
one lane; the six spec steps lead in one block with no spec step after S6; every `Depends on`
names a strictly earlier existing step; every box is unchecked. Code-vs-spec dependency (class 6)
checked step by step: no code step consumes a spec statement whose own step runs later.
— EVIDENCE: implementation-checklist.md:17-66

FACT: all docs anchors resolve at the cited lines. `docs/reference/adapter-contract.md` :10 (page
scope), :53 (gRPC orientation), :64 `DemoteSDK`, :75 `Shutdown`, :81 `ReportSessionScrub`;
`docs/reference/error-catalog.md:129` carries all four DOCS-3 sentences and the remedy cell
verbatim. The DOCS-2 gate substrings (`no other bound session`, `a session whose start the adapter
has admitted`, `either the bind attempt`, `reclaimed`, `absent`, `**Bind attempt token.**`) each
appear in the staged replacement text. — EVIDENCE: non-spec-changes.md:2724, :2731, :2742, :2750,
:3694-3705

FACT: the new CODE-6 seam bullet's six citations all resolve: `pkg/adapter/slot.go:210-212`
(`removeSlotTree`), `pkg/adapter/slotlayout/tree.go:54-55` (os.RemoveAll nil on absent path),
`pkg/adapter/warmlayout_test.go:162-167` (chmod-injection rationale, the widened range is right),
`pkg/adapter/server.go:197-201` (`scrubDone`), `pkg/adapter/holdstate.go:63` (`HoldAfterFunc`),
`pkg/adapter/credexpiry.go:49` (`ExpiryAfterFunc`).

FACT: `TestCredentialAndLLMProxyAndSlotMetricsEmit` (:536) and `TestNewMetricsEmittersNilSafe`
(:576) both exist in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go`, with
their `IncSlotFailure` lines at :550 and :585, so the new CODE-9 test bullet's "extended in place
beside their `IncSlotFailure` lines" is accurate.

FACT: `slotSubjectFileRE` is `(slot|one_session_only|sole_session)[^/]*_test\.go$` and
`slotSurfaceCallRE` is `slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`
(tests/tier0_static/spec_map_slot_address_registration_test.go:1168,:1173). The new
`pkg/adapter/bindattempt_test.go` and `bindattempt_orderings_test.go` match NEITHER (the adapter's
own helpers are `claimSessionSlot(`, `releaseSessionSlot(`, `ReleaseSlotForTest(`, none of which
the regex matches), so they correctly take no `slotAddressCaseFiles` row. Do not re-file that.

FILED: `s.noteShutdownMetUntokenedEntry` is called by CODE-1's staged handler
(non-spec-changes.md:329) and declared by no deliverable. CODE-9 says only "The untokened-entry
series is registered in `pkg/adapter/metrics.go`" (:2068) and the files-touched entry is one clause
(:4032), while the gateway twin `noteCompensationOutcome` is specified completely at :2056-2060.
`pkg/adapter/metrics.go` declares zero `*Server` methods; every helper there is package-level
(`incControlEventSent`, `incUnaddressedFrameRejected`, …, :160-228), so "registered in metrics.go"
does not cover a `*Server` method.

CORRECTS [the 2026-09-20 hard compaction]: the archived WATCHOUTs at
review-log-archive.md:41457 and :50598 recorded exactly this gap ("no deliverable declares it. Do
not assume CODE-9 covers it") and the hard compaction dropped them without the gap being closed.
The current standing context's line "The untokened-entry counter is NOT uncovered; do not re-file
it" is about TEST coverage of the counter's firing and does NOT close the declaration gap; a
future reader should not read it as closing this.

UNVERIFIED: whether the `sessionID` argument in `s.noteShutdownMetUntokenedEntry(sessionID)` is
meant as a label value. SPEC-6's staged §16.1 row labels the series by `k8s_pod_name` alone
(spec-changes.md:1222), so the parameter is either dead or contradicts the row. The standing Open
"Can the adapter populate `k8s_pod_name` on every pod?" already delegates the label VALUE to the
code lane, so I did not file the label half; whoever closes the declaration gap should state the
signature and the label source in the same clause.

USEFUL [the refuted list in the round-3 prompt]: the three stale bare-`removeSlotTree`
files-touched bullets (slotsession.go :3968-3969, holdstate.go :3981-3983) are still in the text
after the seam relocation. They were refuted as immaterial in round 2 and the relocation did not
change that reading. Do not re-file them.

### [non-spec.3.review-citations.1]

VERDICT: EMPTY. No citation finding met the bar.

FACT: the citation surface of this proposal is now large and, as of round 3, clean. I extracted
every `file.ext:NNN` citation from non-spec-changes.md (about 200) and summary.md (about 100) and
verified well over 150 of them line by line against the tree, including every citation the
round-2→3 fix stage added or moved. Not one target was missing, misattributed, or materially
drifted. — EVIDENCE: the fix-stage citations all check out exactly:
pkg/adapter/slotlayout/tree.go:54-55 ("An already-absent tree is not / an error (os.RemoveAll
returns nil for a missing path)"), pkg/adapter/warmlayout_test.go:162-167 (the widened range now
covers the "non-root adapter UID" clause the seam argument rests on), pkg/adapter/server.go:197-201
(scrubDone seam), pkg/adapter/holdstate.go:63 and pkg/adapter/credexpiry.go:49 (the two AfterFunc
seams), pkg/adapter/slot.go:210-212 (removeSlotTree), pkg/adapter/slotsession.go:217
(`_ = removeSlotTree(st)`), pkg/adapter/holdstate.go:249 and :254 (the two discards).

FACT: the round-3 gatewaymetrics addition is accurate in every particular. Both named tests exist
and both carry an `IncSlotFailure` line with exactly the pool/pod values the staged text names.
— EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:536
`func TestCredentialAndLLMProxyAndSlotMetricsEmit`, :550 `m.IncSlotFailure("session_start",
"pool-a", "sbx-1")`, :567 the `lenny_slot_failure_total{...}` exposition assertion, :576
`func TestNewMetricsEmittersNilSafe`, :585 the nil-receiver `IncSlotFailure` call.

FACT: open decision 45's round-3 correction is right. §6.2 does attribute `leaked_slots` to the
adapter's `/healthz` metadata while the gauge is registered and set in the GATEWAY.
— EVIDENCE: spec/06_warm-pod-model.md:160 "The adapter exposes a `leaked_slots` count in the pod's
health metadata (`/healthz` response and `lenny_adapter_leaked_slots` gauge ...)" against
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-223 (the `NewGauge` with
`Name: "lenny_adapter_leaked_slots"`) and gatewaymetrics.go:1213 `SetAdapterLeakedSlots`. No
adapter-side registration exists.

FACT (saves the next lens a whole pass): the SCHEMA-1 field-number table is exactly right against
`schemas/lenny-adapter.proto` as the file stands. I re-derived all nine bases with
`awk "/^message X \{/,/^\}/"`: PrepareWorkspaceRequest 1-3 used + `reserved 4/"slot_id"`;
FinalizeWorkspaceRequest 1-4 used with `mid_session = 4` + reserved 5; RunSetupRequest 1-3 +
reserved 4; AssignCredentialsRequest 1-2 + reserved 3; ResumeRequest 1-5 and 7-14 used, 6 and 15
reserved; ShutdownRequest 1-3, reserved 4, `recycle = 5`, `coordination_generation = 6`;
ShutdownResponse 1-2. Do not re-derive this.

FACT: the three "drift" claims the summary's 0072 impact row makes against that proposal's own
citations are each correct, and I checked both sides. — EVIDENCE: the Entry-paths bullet is at
spec/07_session-lifecycle.md:432 (0072 cites :431); `lenny_checkpoint_storage_failure_total` is at
spec/16_observability.md:203 and docs/reference/metrics.md:193 (0072 cites :201 and :191, and :201
/ :191 are the `orphaned_objects` rows, so the drift is exactly one row); `## Version Negotiation`
is at docs/reference/adapter-contract.md:446 (0072 cites :426, which is a bare `}`).

WATCHOUT: one citation in the summary is off by two lines and is NOT worth filing. The 0078 impact
row says `tests/tier4_integration/concurrent_delegation_proxy_test.go`'s "cleanup at `:168`"; the
`t.Cleanup(func() { _ = rt.Close(...) })` is at :170 and :168 is `rt.AcceptTimeout = 15 *
time.Second`. The named file, the named construct and the named collision are all real, so this is
the "off-by-a-few drift that does not change the meaning" the lens rubric excludes. Filing it burns
two verifiers on nothing. — EVIDENCE:
tests/tier4_integration/concurrent_delegation_proxy_test.go:168,:170

FACT: every test-function and helper name the proposal invokes by name exists under that exact
name. I checked the ones a rename would silently break:
`TestStartSessionRejectsAMalformedSessionAddress_spec_5_2` (pkg/adapter/session_test.go:292, and
the file is package `adapter_test`, which is the ground for restating `malformedSessionAddresses`
at :284 rather than importing it), `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294, whose
`requireAllContain` list is exactly the four substrings DOCS-2 names, at :302-307),
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43),
`TestPerSlotCleanupStatedOnEverySessionModeRow` (:462),
`TestShutdownMessagePostRemovalDescriptor_spec_4_1`
(tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208 with `assertFieldSet`
at :259 and its unexpected-field error at :266),
`TestClaimRegisterIsReproducibleFromItsGenerator` (tests/tier0_static/claim_register_generator_test.go:45),
`seedResumingRow` and `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3`
(pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go:23,:48), `startDrainSession`
and both drain-race cases (tests/tier7a_load_local/shutdown_drain_gate_race_test.go:211,:232,:316),
`recycleAdapterDialer` (tests/tier4_integration/recycle_scrub_path_test.go:206).

FACT: DOCS-2's `ReportSessionScrub` replacement row preserves the gated string byte for byte. The
gate asserts `"The request is session-scoped: it is " + sessionScrubAddressingRule + "."` on BOTH
carriers, and `sessionScrubAddressingRule` is the constant "addressed by the identifier of the
released session and names no slot". The staged row spells that opener and that rule exactly.
— EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33 and
:67-73 against non-spec-changes.md:2742.

USEFUL [non-spec.2.review-citations.1]: the round-2 shard's widening of the warmlayout citation
from `:164-167` to `:162-167` is the range the round-3 text now carries, and it is the correct one:
:162 is where the "non-root adapter UID does not own the kubelet-created mountpoint" clause starts,
which is the half of the comment the seam argument actually cites.

DECISION: I did not file the `slotfailure.go` range variance between the two files
(non-spec-changes.md cites `:91-99` for the policy-rejection mapping, summary.md cites `:93-100`
for the same mapping). BECAUSE both ranges land inside the same `case
codes.PermissionDenied, codes.FailedPrecondition:` arm (pkg/gateway/podlifecycle/podsession/
slotfailure.go:91-99, with the transient default at :100-101) and neither reading changes what the
arm does. ALTERNATIVES: filing it as a citation defect, which fails the "drift changes the meaning"
test and would have been refuted.

### [non-spec.3.review-client-surface.1]

VERDICT: EMPTY. No finding met the bar under the client-facing surface lens.

FACT: The adapter proto reaches NO SDK. `grep -rln "adapterv1|lenny-adapter.proto|proto/adapter" sdks/` returns nothing, so SCHEMA-1's nine fields, two `ErrorCode` values and `SlotReclaimOutcome` owe no language-SDK mirror. — EVIDENCE: sdks/ (empty grep); schemas/lenny-adapter.proto:531-585
FACT: No client-facing surface enumerates adapter `ErrorCode` values. `pkg/gateway/externalapi/openapi/openapi.json` carries no error-code enum (only the `/v1/sessions/{id}/setup-output` path at :936); `docs/api/internal.md` and `docs/reference/adapter-contract.md` mention only `PROTOCOL_VERSION_INCOMPATIBLE`, as prose about version negotiation (internal.md:443, adapter-contract.md:448). Adding codes 28/29 falsifies nothing there. — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:936; docs/api/internal.md:443
FACT: `docs/api/internal.md`'s "Protobuf service definitions" block is ALREADY pervasively stale against `schemas/lenny-adapter.proto` — it declares `StopSession`, `UploadFiles`, a six-field `StartSessionRequest` and error codes `ALREADY_ACTIVE`/`WORKSPACE_NOT_READY` that the proto does not declare. Pre-existing drift; this proposal neither touches nor worsens it. Do not file it against 0081. — EVIDENCE: docs/api/internal.md:74-136 vs schemas/lenny-adapter.proto:531-585
FACT: All nine SCHEMA-1 field numbers re-verified free against the shipped messages, and `ErrorCode` does run to 27. Per-message occupancy: PrepareWorkspaceRequest 1-3 + reserved 4; FinalizeWorkspaceRequest 1-4 + reserved 5; RunSetupRequest 1-3 + reserved 4; AssignCredentialsRequest 1-2 + reserved 3; ResumeRequest 1-5,7-14 + reserved 6,15; ShutdownRequest 1-3,5,6 + reserved 4; ShutdownResponse 1-2. Do not re-derive. — EVIDENCE: schemas/lenny-adapter.proto:585 (`ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27`)
FACT: The only closed-field-set descriptor gate over a message SCHEMA-1 opens is `TestShutdownMessagePostRemovalDescriptor_spec_4_1`; its `assertFieldSet` errors on any undeclared number. The other tier-3 descriptor pin (`checkpoint_stream_wire_test.go`) closes only `CheckpointStart` (exact count 6) and the checkpoint oneofs, which SCHEMA-1 does not open. SCHEMA-1's claim is exact. — EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208,233-237,259-267; tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:77,151
FACT: Every verbatim quote DOCS-2, DOCS-3, DOCS-4 and DOCS-1 stage matches the tree byte for byte, and every cited line number resolves: adapter-contract.md:10,53,64,75,81; error-catalog.md:129 (all four sentences and the remedy cell); state-machines.md:138,235,237,251; spec/04:692; spec/15:1136. — EVIDENCE: docs/reference/error-catalog.md:129; docs/reference/state-machines.md:235,237,251
FACT: The two tier-11 gates over the doc rows both still pass under the staged text. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the literal opener `"The request is session-scoped: it is addressed by the identifier of the released session and names no slot."` on BOTH the spec §4.7 row and the doc row; the staged §4.7 row (spec-changes.md:768) and the staged DOCS-2 row both carry it verbatim. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub", all four present in the staged `Shutdown` row. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,66-73; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:300-306
FACT: The withdrawn reporting universal has exactly three carriers in `schemas/` under CODE-10's own grep, and SCHEMA-1's disposition of them is complete and correct: :309 (RPC comment, edited), :452 (request message comment, edited), :437 (the `SessionScrubOutcome` enum comment, correctly left alone — its subject is the CLEANUP running on every release, which stays true, not the report). The sibling Go copy at `pkg/adapter/gatewaycontrol/scrubreport.go:12-13` is the same non-carrier; the METHOD comment at :71-72 ("reports ... on every session release") IS a carrier and falls to CODE-10's grep. — EVIDENCE: schemas/lenny-adapter.proto:309,437,452; pkg/adapter/gatewaycontrol/scrubreport.go:12,72
FACT: The 0167 migration comment is genuinely returned by CODE-10's grep (`migrations/0167_runtime_definitions_execution_mode_service.up.sql:102`), so the "edited in place" clause names a real hit rather than a remembered one. — EVIDENCE: migrations/0167_runtime_definitions_execution_mode_service.up.sql:102
FACT: `docs/reference/error-catalog.md` is held by no gate: `grep -rn "error-catalog" tests/ scripts/ cmd/ Makefile` returns nothing. DOCS-3's "nothing holds the two to one text" is true as written. — EVIDENCE: (empty grep over tests/, scripts/, cmd/, Makefile)
FACT: Only two production sites construct `adapterv1.Error`: `pkg/adapter/staging.go:209` (adapter-side, sets `Retryable` explicitly) and `pkg/gateway/mcpfabric/.../leasecontrol.go:1033` (gateway-side, cool-off). Neither is an exhaustive switch over `ErrorCode`, so codes 28/29 force no consumer update. CODE-7's "only status-detail reader in the whole gateway is client.go:333-341" also holds. — EVIDENCE: pkg/adapter/staging.go:209-214; pkg/gateway/runtime/adapterclient/client.go:334-341
FACT: `cmd/lenny-compliance` imports no gRPC, so the `ABSENT` claim row's note ("drives a runtime binary over JSONL against a fake adapter and imports no gRPC") is true, and the runtime-author conformance suite (`lenny runtime validate`, docs/runtime-author-guide/testing.md) covers the runtime↔adapter JSONL contract only — the §4.7.1 gRPC rules are genuinely unharnessed for a third-party adapter. — EVIDENCE: cmd/lenny-compliance (no google.golang.org/grpc import); docs/runtime-author-guide/testing.md:10-12
FACT: `R8` is declared at `gateway-runtime-comms-remediation.md:980` exactly, and `scripts/seed-claim-register.py:188-192` already carries a sibling row deferring to `R8`. — EVIDENCE: gateway-runtime-comms-remediation.md:980; scripts/seed-claim-register.py:192
FACT: `sessions_served`/`sessionsServed` appears in NO CRD (`charts/lenny/crds`, `pkg/embedded/crds`, `pkg/apis`) and in no `docs/` page, so SPEC-3's re-key of its write trigger touches no operator-applied schema. Likewise no CRD mentions the per-slot sub-states. — EVIDENCE: (empty grep over charts/lenny/crds, pkg/embedded/crds, pkg/apis, docs/)
FACT: `lenny_adapter_leaked_slots` is published by the GATEWAY (`gatewaymetrics.SetAdapterLeakedSlots`, `gatewaymetrics.go:1208-1213`), confirming this round's fix to open decision 45. — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go:1213
WATCHOUT: the CODE-4 Targets bullet says "`Binder.ReleaseSlot`'s `Shutdown` and `ShutdownRecycle` calls set `unconditional_teardown`" and the files-touched list names "`Binder.ReleaseSlot`'s two teardown calls" and "`Binder.shutdownAdapter`'s two teardown calls" as edited, while the SAME deliverable's body says "no call site above is edited for it" and checklist S13 says the shipped callers "are carried without being edited". I judged this index-shorthand-vs-body wording, the same class the material skeptic already refuted for the `holdstate.go` files-touched entry, and did not file it. A later lens should not file it either without a new consequence. — EVIDENCE: non-spec-changes.md:876-882 vs :948-953; implementation-checklist.md:41

### [non-spec.3.review-docs-alignment.1]

VERDICT: EMPTY. Third consecutive empty verdict for this lens on this proposal (rounds 7, 11,
non-spec.2, now non-spec.3).

FACT: every DOCS-* line citation re-verified this round and all are exact — `adapter-contract.md`
`DemoteSDK` :64, `Shutdown` :75, `ReportSessionScrub` :81, page self-description :10, gRPC
orientation :53; `state-machines.md` pod-projection paragraph :138, `receiving_uploads|running`
:235, `slot_cleanup|released` :237, `slot_cleanup -> leaked` clause :251; `error-catalog.md`
`SETUP_COMMAND_FAILED` :129; `execution-modes.md` :68 and `security-principles.md` :33 both still
carry the withdrawn ", and the adapter reports its outcome to the gateway" tail, and
`multi-tenancy.md`:72 still does not. EVIDENCE: docs/reference/adapter-contract.md:64,75,81;
docs/reference/state-machines.md:138,235,237,251; docs/reference/error-catalog.md:129

FACT: the three shipped tier-11 gates that read the DOCS-1/DOCS-2 pages all stay green under the
staged text, checked substring by substring. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
needs "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" —
all four survive in the staged `Shutdown` row. `TestSessionScrubReportAddressingAgreesBetweenSpec
AndContractDoc` needs the exact string "The request is session-scoped: it is addressed by the
identifier of the released session and names no slot." on BOTH the staged §4.7 row and the staged
docs row; both carry it verbatim. `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` runs
`generalSlotEdges` as a positive loop over the either-concurrency block and a NEGATIVE loop over
the concurrent block, so adding `receiving_uploads ──→ slot_cleanup` there is safe only because
SPEC-4 inserts that edge in the either-concurrency block alone; it does. EVIDENCE:
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307;
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,66-72;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-37,55-74

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` derives its source of truth from
`regexp.MustCompile("Name:\\s*\"(lenny_[a-z0-9_]+)\"")` over `pkg/adapter/metrics.go` — it is NOT
prefix-scoped to `lenny_adapter_`, so CODE-9's `lenny_slot_shutdown_untokened_entry_total` is
swept and genuinely requires both the §16.1 row (SPEC-6) and the `docs/reference/metrics.md` row
(CODE-9), exactly as CODE-9 claims. It also means SPEC-6 must land before CODE-9 registers the
series, which the checklist order (S6 before S10) satisfies. EVIDENCE:
tests/tier11_docs/adapter_metric_catalog_test.go:26,34,49,96-112

WATCHOUT: `docs/operator-guide/troubleshooting.md:41` ("Setup command failures | `reason:
setup_command_failed` | Check runtime setup commands, increase `setupPolicy.timeoutSeconds`")
looks like an unstaged operator-facing carrier of the cause SPEC-5/DOCS-3 widen. It is NOT. That
table is the warm-pool-exhaustion section's `lenny_warmpool_warmup_failure_total` `error_type`
enumeration (pod warmup), a different surface from the §15.1 REST envelope; SPEC-5 does not reach
it. The standing-context line saying SPEC-5 opens no docs mirror beyond DOCS-3 is correct, and I
confirmed the reason by reading the section header above the table rather than the row.
EVIDENCE: docs/operator-guide/troubleshooting.md:25-45; docs/reference/metrics.md:135

FACT: no docs/ page and no runbook enumerates the causes of a `leaked` slot, so DOCS-1's rewrite of
the `slot_cleanup -> leaked` clause is the only reader-facing carrier of the new causes and no
operator-guide/runbook companion is owed. The proposal adds no alert, so tier-11 alert-to-runbook
resolution is untouched. EVIDENCE: `grep -rn leaked docs/operator-guide/ docs/runbooks/` returns
only docs/runbooks/credential-revocation.md:34, an unrelated use.

FACT: `docs/assets/diagrams/recycle-lifecycle.svg` depicts the recycle projection and already reads
"reserved tenant hold, then claimed again on a same-tenant rebind or idle on hold expiry", which is
what SPEC-4's re-keyed `reserved → idle` bullet says. The diagram needs no edit, and its footer
("A failed or crashed session, or a reached recycle limit, drains the pod instead") stays true
under the re-keyed `claimed`-deletion bullet. No other diagram carries the per-slot machine.
EVIDENCE: docs/assets/diagrams/recycle-lifecycle.svg text nodes; docs/reference/execution-modes.md:78

FACT: `docs/api/internal.md` and `docs/assets/diagrams/internal-rpc-architecture.svg` are wholesale
pre-existing drift (`StopSession`, `UploadFiles`, no `Shutdown`), so nothing this proposal stages
newly falsifies them. Re-confirmed rather than taken from the standing context. EVIDENCE:
docs/api/internal.md:19,81-83,139,447

FACT: DOCS-2's added paragraph links to spec headings by GitHub URL. That has precedent on this
very page (docs/reference/adapter-contract.md:393 links §15.4's fidelity matrix the same way) and
elsewhere under docs/reference/ and docs/client-guide/, so it is not a new convention. Both anchors
resolve: spec/04_system-components.md:659 `#### 4.7.1 Role and Gateway RPC Contract` and
spec/15_external-api-surface.md:1458 `### 15.4 Runtime Adapter Specification`.

UNVERIFIED: DOCS-1's rationale asserts the re-keyed pod-projection sentence is "a partition over
the projection input: exactly one clause answers any claim deletion". A claim deleted while the pod
projects `sdk_connecting` (the preConnect re-warm leg) is answered by neither re-keyed clause. The
same gap sits in SPEC-4's §4.6.1 bullets, so the docs page mirrors the spec faithfully and this is
not a docs defect; it is a question about the locked spec staging. I did not file it, because the
remedy is a spec edit and the projection lens owns it. Someone with the spec lane open should decide
whether it is a real gap or whether `sdk_connecting` cannot see a claim DELETE.

DECISION: did not file "a rolled-back start can close a successor's runtime session is a new
session-loss path documented in neither the staged spec nor docs" — BECAUSE the summary's
"Defects in the shipped tree that this proposal does not stage" already adjudicates exactly this,
recording that `SocketRuntimeProcess.Close` reaches the same state on the shipped `Binder.ReleaseSlot`
path under the same precondition, so CODE-2's rollback "adds a trigger for a state the shipped
release path already produces ... rather than a state class of its own"; the remedy would be a spec
edit under a locked lane, and the absence makes neither the applied spec inconsistent nor the
implementation broken. ALTERNATIVES: filing it as a recordable open decision, rejected because the
record already exists in the summary and re-filing it costs two verification agents for text that is
already written. EVIDENCE: summary.md:772-783, 850-866; pkg/adapter/session.go:156-164 (the shipped
call site has no rollback today, so the close is new, which is what made this worth checking).

### [non-spec.3.review-edit-sites.1]

FACT: `schemas/lenny-adapter.proto:556` carries "The catalog below mirrors spec §15.1." immediately inside `enum ErrorCode`, above value 1. SPEC-5 decides §15.1 takes no row for either new code (spec-changes.md:1057, :1240-1241) while SCHEMA-1 adds values 28 and 29, so the mirror sentence is falsified by this proposal. SCHEMA-1 enumerates the four proto comments it replaces (non-spec-changes.md:2374-2465) and this is not one. — EVIDENCE: schemas/lenny-adapter.proto:554-559; non-spec-changes.md:2324-2343

FACT (verified, do not re-derive): the `error_type` → `SlotBindError.Reason()` hazard from CODE-9's new `slotFailureWorkspaceFinalize` constant is already closed in the text. `pkg/gateway/podlifecycle/podsession/slotfailure.go:96` branches on `e.Stage == slotFailureWorkspacePrep` to keep a workspace `FailedPrecondition` transient, and non-spec-changes.md:3309-3313 explicitly requires the `slotBindError` stage argument at the finalize site to stay `slotFailureWorkspacePrep` so only the metric separates. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:85-103; non-spec-changes.md:3308-3315

FACT: the `sessions_served` re-key closure is complete. `grep -rn sessions_served tests/` returns exactly the two tier-11 carriers SPEC-3's table names (`tests/tier11_docs/spec_28_register_writers_test.go:99-101`, the byte-exact §12.6 pin; `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:170-174`, the read-clause pin), plus non-carriers in tier2/tier3/tier4 and a slugify fixture. Checklist S4 runs that grep, so nothing is unstaged here. — EVIDENCE: tests/tier11_docs/spec_28_register_writers_test.go:99; spec-changes.md:611-612

FACT: DOCS-4's two-site set for the ", and the adapter reports its outcome to the gateway" clause is exhaustive. A grep over `docs/` for that clause returns only `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33`; `docs/operator-guide/multi-tenancy.md:72` carries the sibling sentence WITHOUT the clause and correctly takes no edit. — EVIDENCE: docs/operator-guide/multi-tenancy.md:72; non-spec-changes.md:2839-2841

FACT: `docs/api/internal.md` embeds proto message definitions but documents an idealized RPC set (StartSession/StopSession/Attach/Checkpoint/UploadFiles/DemoteSDK) that carries no `ShutdownRequest`, no `PrepareWorkspaceRequest` and no `ErrorCode` enum. It is pre-existing drift from `schemas/lenny-adapter.proto` and this proposal does not touch it. Do not file it. — EVIDENCE: docs/api/internal.md:71-290

WATCHOUT: `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` survives the new `receiving_uploads ──→ slot_cleanup` edge only because `generalSlotEdges` is used for a positive loop over §6.2's either-concurrency block and a negative loop over the concurrent-occupancy block (`:55-74`). Adding the edge to `generalSlotEdges`, as the Testing section directs, also forbids it appearing in the concurrent-occupancy block. If SPEC-4 ever put the edge in the scoped block instead, the test goes red. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-74; non-spec-changes.md:3688-3690

FACT (checked, not filed): the master "Files touched on application" entry for `pkg/adapter/server.go` (non-spec-changes.md:3939-3942) omits the `SessionScrubReporter` field-comment re-key that CODE-1's own files list carries (`:178-180`) and that SPEC-3's carrier table assigns to CODE-1 (spec-changes.md:609). The edit is in a list, just not the master index, so it does not meet the missing-edit-site bar under the prior refutations of index-bookkeeping findings. — EVIDENCE: non-spec-changes.md:178-180, :3939-3942

FACT: `ReleaseSlotReservation`'s `leaked bool` call-site table (non-spec-changes.md:1165-1173) is complete against the tree: `slotbinder.go:172` (ClaimSlot), `:217` (BindReservedSlot), `:493` (definition), `binder.go:1714` (releaseResumeSlot), `start.go:2727` (interface), `:2834` (applySlotRetryPolicy), `:3246` (rollbackClaim), and the two fakes in `slotretry_test.go:52` and `slotretry_load_test.go:33`. Nothing else in the tree calls it. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493; pkg/gateway/sessionserver/start.go:2727

### [non-spec.3.review-fresh.1]

DECISION: returned EMPTY — BECAUSE every claim I spot-checked in the round-2 fix stage verified against the tree, and the two remaining wobbles I found are below the materiality bar — ALTERNATIVES: filing the `noteShutdownMetUntokenedEntry` parameter ambiguity and the CODE-9 heading's missing file names; both are bookkeeping/over-specification and would not make the applied spec or implementation wrong.

FACT: every citation the round-2 tree-removal-seam rewrite introduced is correct as written. Verified: `pkg/adapter/slot.go:210-212` (`removeSlotTree`), `pkg/adapter/server.go:197-201` (`scrubDone` seam), `pkg/adapter/holdstate.go:63` (`HoldAfterFunc`), `pkg/adapter/credexpiry.go:49` (`ExpiryAfterFunc`), `pkg/adapter/slotlayout/tree.go:54-55` ("os.RemoveAll returns nil for a missing path"), `pkg/adapter/warmlayout_test.go:162-167` (chmod-injection rationale), `pkg/adapter/slotsession.go:217` and `pkg/adapter/holdstate.go:249,251,254` (the three discards). Do not re-derive these. — EVIDENCE: pkg/adapter/slot.go:210

FACT: CODE-10's closure grep, run verbatim from the repo root, returns 46 hits across 24 files. The three tier-11 hits are `basic_level_echo_stamp_doc_reconciliation_test.go:18,473`, `concurrent_slot_lifecycle_doc_reconciliation_test.go:170-174` and `session_scrub_report_addressing_doc_reconciliation_test.go:6,21`. Only `concurrent_slot_lifecycle` is also caught by S4's `grep -rn sessions_served tests/`. CODE-10's phrase "the two tier-11 files under checklist S4's sweep" is NOT a claim about its own grep's output: it points at the SPEC-3 carrier table rows at spec-changes.md:608-609, which name `spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`. The two readings coincide only by accident of wording; a future lens will be tempted to file the mismatch and should not. — EVIDENCE: 0081_...spec-changes.md:608-609

USEFUL [the round-2 CODE-10 arm-rule fix]: the new arm-2 text at non-spec-changes.md:2107-2121 names, as its two worked examples, exactly the two grep hits whose universal survives a bare trigger-clause deletion: "reports each per-slot cleanup outcome" (which is verbatim `pkg/controller/sandbox/podspec/podspec.go:168,591,906`) and "a per-slot cleanup runs at every session release, and the adapter reports its outcome" (which is verbatim `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8`). The fix is exact, not generic. That closes the addressing-test header finding for good.

FACT: CODE-1's staged handler is arithmetically consistent with all eight rows of the SPEC-3 §5.2 disposition table (spec-changes.md:620-628). I walked every row against `exited_cleanly = closeErr == nil && (live || treeErr == nil)` (non-spec-changes.md:495), `completed = closeErr == nil && treeErr == nil` (:440) and `if live { reportSessionScrub(..., closeErr) }` (:442). No row disagrees. A lens looking for a table-vs-code contradiction here will find none; spend the budget elsewhere. — EVIDENCE: 0081_...non-spec-changes.md:440

FACT: the two `defer`s in the removing arm are in the right LIFO order for the stated invariant. `defer unlockSlot()` registers at non-spec-changes.md:353 and the hold-release closure at :396, so the release runs first and the guard outlives the hold, which is what the commentary at :290-291 claims. — EVIDENCE: 0081_...non-spec-changes.md:353

FACT: CODE-9's newly cited gateway sites all verified: `gatewaymetrics_credential.go:185-193` (the `lenny_slot_failure_total` collector), `gatewaymetrics.go:1170-1175` (`IncSlotFailure`), `binder.go:137` (`SlotFailure` field), `slotbinder.go:353-361` (`recordSlotFailure`), `metricsbackfill.go:149` (the wiring), `slotbinder.go:287,294` (both `slotFailureWorkspacePrep` sites). The round-2 addition of `gatewaymetrics_elicitation_test.go` also checks out: `TestCredentialAndLLMProxyAndSlotMetricsEmit` is at :536 with `m.IncSlotFailure("session_start", "pool-a", "sbx-1")` at :550, `TestNewMetricsEmittersNilSafe` at :576 with the nil-receiver call at :585, and the shipped exposition assertion `lenny_slot_failure_total{error_type="session_start",k8s_pod_name="sbx-1",pool="pool-a"} 1` at :567 confirms the alphabetical label order the staged `lenny_slot_compensation_superseded_total{k8s_pod_name="sbx-1",pool="pool-a"} 1` assertion assumes. — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:567

UNVERIFIED: `s.noteShutdownMetUntokenedEntry(sessionID)` (non-spec-changes.md:329) is the only occurrence of that symbol in the whole proposal. No deliverable states its signature, its file, or what the `sessionID` argument is for. SPEC-6's staged §16.1 row labels the series by `k8s_pod_name` alone (spec-changes.md:1222), which the adapter holds as `s.podID`, so the argument is not the label. I judged this below the bar (an implementor cannot get the label set wrong, because the spec row fixes it, and `pkg/adapter/metrics.go` is in CODE-9's files-touched at :4032). Someone should confirm nobody later reads the parameter as a label and introduces a session-cardinality series.

UNVERIFIED: CODE-9's section heading (non-spec-changes.md:2037) and the summary's CODE-9 index line both omit `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go` (the `IncSlotCompensationSuperseded` accessor) and `pkg/observability/metrics/catalog_test.go` (the `spec161Metrics` entry), although the body names both (:2047, :2076) and the checklist S10 line and the files-touched index (:4031) carry `gatewaymetrics.go`. Heading bookkeeping, same class as the S23/DOCS-4 tier mismatch the material skeptic refuted. Not filed.

UNVERIFIED: CODE-6's tier-1 tree-failure case is now table-driven over `releaseSessionSlot` and `terminateHeldSession` "with the `Shutdown` row added when CODE-1 routes that handler through `reclaimSlotLocked`" (non-spec-changes.md:2948-2950). No checklist step explicitly owns adding that row; S16 and S17 both carry tier 1 and either could. I judged it an implementor-obvious continuation rather than a missing test. If a later lens wants to file it, the bar it must clear is (f), and the counter-argument is that the behaviour itself is already pinned by CODE-1's own tier-1 cases at :3120-3135.

### [non-spec.3.review-kubernetes.1]

VERDICT: EMPTY. Fifth consecutive empty verdict for the kubernetes lens on this proposal (four on the spec staging per `### Settled` "THIRTY-SIX consecutive lenses", one now on the non-spec staging).

FACT: the non-spec staging's whole Kubernetes surface is FOUR sites and nothing else. `grep -n "controller\|Controller\|finalizer\|status subresource\|Sandbox\b\|SandboxClaim\|admission\|webhook\|reconcil\|etcd\|apiserver\|CRD\|field manager\|OwnerReference\|podclaim" non-spec-changes.md summary.md` returns hits that reduce to: CODE-8's refusal arm leaving the per-pod `SandboxClaim` in place, CODE-5's `accountSlotFailure` keeping the shipped `Unhealthy → DrainSandbox` tail, the tier-2 envtest pair in `pkg/gateway/podlifecycle/podsession/binder_envtest_test.go`, and DOCS-1's pod-state-machine paragraph. Everything else in the deliverable set is in-process adapter state and gRPC. A future kubernetes lens can run this one grep and stop. — EVIDENCE: non-spec-changes.md:1269, :1947-2035, :3466-3477, :2651-2678

FACT: every apiserver-touching claim the non-spec staging makes re-verifies exactly against the tree and against §4.6.3. `failPhase` is `binder.go:1072`, its `b.drain` call is `:1079`, `drain` is `binder.go:1200-1202` and its body is the single `podclaim.DeleteClaim` (`:1201`) — the proposal's `binder.go:1079`, `:1200-1202` citation pair is correct, not off-by-the-doc-comment as a first read of `sed -n '1195,1210p'` suggests. `Binder.DrainSandbox` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:601`) stamps the `lenny.dev/drain-request` annotation on the agent **Pod** via `podclaim.StampDrainRequest` and writes no `Sandbox.status`, which is exactly what its own doc comment and `spec/04_system-components.md:632` state. So the tier-2 case's "the `Sandbox` untouched … the gateway is not a writer of `Sandbox.status`" (non-spec-changes.md:3473-3474) is sound, and the `SandboxClaim` is the only object either `Prepare` arm writes. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:586-591, spec/04_system-components.md:632

FACT: DOCS-1's four pod-state-machine replacements compose against the live page and agree clause for clause with SPEC-4's staged §4.6.1 bullets. The page's current sentence at `docs/reference/state-machines.md:138` carries the quoted anchor "a level-triggered projection of the per-pod `SandboxClaim`: claim existence, the claim's binding state and disposition, and `sessionPolicy`" byte for byte, and `reserved`-keyed / `claimed`-keyed replacements match spec-changes.md:918 and :927. DOCS-1 correctly omits SPEC-4's sole-writer parenthetical ("the controller's own last level, which it may read back because it is the sole writer of that field", spec-changes.md:909), which is spec-internal justification and not reader-facing page content — that omission is not a cross-lane contradiction. — EVIDENCE: docs/reference/state-machines.md:138, spec-changes.md:909/918/927, non-spec-changes.md:2660-2678

USEFUL [Settled "`OccupancyReconciler` WATCHES ITS OWN OBJECT"] and USEFUL [Settled "§4.6.3 gives `Sandbox.status.*` to the WarmPoolController as sole writer"]: these two together answer every controller-idiom dress against the read-back input DOCS-1 adds (two-managers-racing-one-field, ForceOwnership, self-triggering reconcile loop) without a fresh derivation. I re-verified only the RBAC half, at spec/04:632; the watch half I took from the log.

USEFUL [Settled "The exclusive-path `SandboxClaim` strand CANNOT last"]: the stuck-object / orphaned-claim finding is the one real candidate CODE-8 opens under this lens, and that entry retires it with the leader-elected orphan-claim GC in `pkg/controller/warmpool/gc.go`. I did not re-derive it.

WATCHOUT: the tier-2 heading reads "**Tier 2, the adapter against envtest**" (non-spec-changes.md:3466) although both cases drive `Binder.Prepare`, a gateway component, against envtest, and the adapter appears only as a fake answering the RPC. It is a heading label over a body that names the right component, so it is below the bar as a wrong-component attribution; do not file it, and do not "fix" it into a finding on a later pass.

FACT: a new envtest file in `pkg/gateway/podlifecycle/podsession` is feasible — `binder_test.go` in that same package already stands envtest up, so the tier-2 case owes no new harness. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go (matches `grep -rln envtest`)

### [non-spec.3.review-mechanism.1]

VERDICT: EMPTY. Traced the full delta since `non-spec-r1-prefix` plus the CODE-1 / CODE-6 /
CODE-9 mechanism paths end to end and found nothing meeting the bar.

FACT: the round-3 delta is exactly seven edits and all seven re-verify clean against the tree —
the tree-removal seam moved whole from CODE-1 to CODE-6 (`non-spec-changes.md` "The tree-removal
seam." bullet), the CODE-6 tree-failure tier-1 case became table-driven over `releaseSessionSlot`
and `terminateHeldSession`, CODE-1's "The runtime's own answer still decides" bullet absorbed the
hold-plus-`released` pair, CODE-10 arm 1 gained its non-separable-quantifier branch, DOCS-2's
`DemoteSDK` row gained "if it holds one", open decision 45 gained the gateway attribution, and
CODE-9 gained the two `gatewaymetrics_elicitation_test.go` cases.
EVIDENCE: diff against /home/ec2-user/lenny/scratchpad/cp-snap/0081-opt2/non-spec-r1-prefix

FACT: every code citation the relocated seam bullet carries is exact, re-read line by line:
`removeSlotTree` at pkg/adapter/slot.go:210-212; the discard `_ = removeSlotTree(st)` at
pkg/adapter/slotsession.go:217; `_ = s.Runtime.Close(ctx, m.sessionID)` at
pkg/adapter/holdstate.go:249 and `_ = removeSlotTree(m.state)` at :254; `scrubDone` at
pkg/adapter/server.go:197-201; `HoldAfterFunc` at pkg/adapter/holdstate.go:63 and
`ExpiryAfterFunc` at pkg/adapter/credexpiry.go:49; the `os.RemoveAll` sentence at
pkg/adapter/slotlayout/tree.go:54-55; the chmod-injection rationale at
pkg/adapter/warmlayout_test.go:162-167. Do not re-verify this set.

FACT: the CODE-9 test addition is sound in every particular. Both named cases exist
(`TestCredentialAndLLMProxyAndSlotMetricsEmit` at
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:536, `TestNewMetricsEmittersNilSafe`
at :576), both already carry an `IncSlotFailure` line (:550, :585) to sit beside, and the asserted
exposition string's label order matches the shipped sibling
`lenny_slot_failure_total{error_type=...,k8s_pod_name="sbx-1",pool="pool-a"} 1` (:567), which is
the Prometheus sorted-label form. The four hook citations also hold: `SlotFailure` field at
binder.go:137, `recordSlotFailure` at slotbinder.go:353-361, `IncSlotFailure` at
gatewaymetrics.go:1170-1175, the collector at gatewaymetrics_credential.go:185-193, the wiring at
metricsbackfill.go:149.

FACT: adding `gatewaymetrics_elicitation_test.go` to the Tests list owes NO `slotAddressCaseFiles`
row. The derived inventory rules are `slotSurfaceCallRE` =
`slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`, a
basename rule `(slot|one_session_only|sole_session)[^/]*_test\.go$`, and a `TEST-GAPS.md` rule
scoped to `tests/tier11_docs/`. `IncSlotCompensationSuperseded(` matches none of them.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1169-1173, :1213-1229

FACT: the `ensureSlotStateLocked` switch implements staged rules 3 through 7 with no predicate
drift, checked arm by arm against spec-changes.md:1049-1053 — `!ok && !r.allowCreate` is rule 3
(`allowCreate == !midSession` at every table row), `!ok` is rule 4, the two-non-empty compare is
rule 5, `st.started && !r.allowStarted` is rule 6 with `allowStarted` true exactly for a
mid-session RPC and `ConfigureWorkspace`'s `idempotentRepeat`, and `default` is rule 7. The ten-row
call-site table agrees with all four.

FACT: CODE-1's handler defers `unlockSlot` (line ~353) before the hold-release closure (~396), so
LIFO gives release-then-unlock and the guard does outlive the hold, exactly as the block's own
comment claims. The `untokened` counter fires at most once per request because `answerShutdown` is
the single exit and the untokened arm always sets `remove=false`.

FACT: the four gateway `Shutdown`-caller citations inside CODE-1's `answerShutdown` comment are
exact — slotbinder.go:542 (`Adapter.Shutdown`), :574 (`ShutdownRecycle`), binder.go:1994
(`b.shutdownAdapter(ctx, result, true)`), :2037 (`ShutdownRecycle` in `shutdownAdapter`'s recycle
arm). The ABSENT-versus-RECLAIMED split they ground is right.

FACT: the assertions the seam move redistributed are all still present, confirming the standing
context's entry on that redistribution. The pre-`running` non-clean-exit arm is CODE-1's
"`exited_cleanly` on a reclaim the runtime never held" bullet, the `running`-arm hold-plus-
`released` pair is CODE-1's "The runtime's own answer still decides" bullet, and the retained-hold
arm is CODE-6's table-driven case. Nothing was dropped; do not file a missing-coverage finding
against the seam without reading both deliverables.

UNVERIFIED: `s.noteShutdownMetUntokenedEntry(sessionID)` is called in CODE-1's staged handler body
and is defined by no deliverable. CODE-9 says only that the series "is registered in
`pkg/adapter/metrics.go`", and the shipped adapter metric accessors there are package-level
functions (`incRotationCeilingHit`, `incControlEventSent`, …) rather than `Server` methods, so the
receiver form, its file, and what it passes for the row's single `k8s_pod_name` label are all
implicit. NOT FILED, because the label half is already the standing Open "Can the adapter populate
`k8s_pod_name` on every pod?", which routes the counter's label handling to the code lane, and the
remaining residue is one helper an implementor writes at the one call site. A round that wants it
should file the helper's HOME (which file declares it), not the label.
EVIDENCE: non-spec-changes.md:329 (the call), :2068 (CODE-9's registration sentence),
spec-changes.md:1222 (the §16.1 row's single label), pkg/adapter/metrics.go:160-222

UNVERIFIED: CODE-10 arm 1's new branch tells an implementor to write the predicate "the outcome of
a cleanup a `Shutdown` performs to reclaim a slot that reached `running`" into each comment whose
quantifier is not separable, while the same deliverable's invariants bar adding a §5.2 pointer to a
comment that carries none. That is a restatement of the §5.2 report biconditional at an unbounded
number of comment sites, which reads like the duplicated-rule class. NOT FILED: the phrase is a
scoping noun phrase rather than the biconditional (it carries neither the "when and only when" nor
the served-count ground), the no-pointer invariant leaves no reduction available, and the previous
arm-1 text was found DEFECTIVE for the opposite reason (deleting the trigger left podspec.go's
"reports each per-slot cleanup outcome" still universal). The branch is the repair for that.
EVIDENCE: non-spec-changes.md:2107-2121 (arm 1), :2126-2127 (the no-pointer invariant),
spec-changes.md:574 (the `**Scrub model.**` replacement the phrase is drawn from)

USEFUL [the standing context's seam-redistribution entry, and the trap "Do NOT sweep every 'row' in
the non-spec file onto 'arm'"]: both saved a filing. The first pre-empted a missing-coverage
finding against CODE-6's shortened tree-failure case; the second stopped a reading of the
table-driven case's "row" as a `shutdownReclaimOutcome` arm.

USEFUL [the refuted-index entry on the three stale bare-`removeSlotTree` files-touched bullets]:
two of the three are now GONE, because this round's seam move rewrote the `holdstate.go` and
`slotsession.go` bullets to name `s.removeSlotTreeVia`. Only `summary.md`'s `server.go` clause
question survives, and the CODE-1 summary line no longer lists `pkg/adapter/slot.go` at all. A
future compaction can retire the DISAGREEMENT entry's first two limbs as moot.

WATCHOUT: CODE-6's table-driven tree-failure case says the `Shutdown` row is "added when CODE-1
routes that handler through `reclaimSlotLocked`", and no checklist step (S16 or S17) instructs that
addition. It is NOT a coverage gap, because CODE-1's own two tier-1 bullets pin the `Shutdown`
hold retention on both the pre-`running` and the `running` arm. Do not file it as a missing test;
if a later round opens those lines for another reason, fold the row in then.

### [non-spec.3.review-performance.1]

DECISION: returned an EMPTY findings list — BECAUSE the round-1→round-3 delta on this lane is 136
changed lines across three files and not one of them touches a write path, a store, a lock hold, a
watch, a label dimension or a bound. ALTERNATIVES considered and rejected: (a) the tree-removal seam
move from CODE-1 to CODE-6 — `removeSlotTreeVia` returns `removeSlotTree(st)` unless the nil-defaulted
`removeSlotTreeFn` is set and nothing in production sets it, so a deployed adapter takes the identical
call, and both new call sites are already `*Server` methods so no receiver plumbing is added
(`pkg/adapter/slotsession.go:214` `func (s *Server) releaseSessionSlot`, `pkg/adapter/holdstate.go:229`
`func (s *Server) terminateHeldSession`); (b) `releaseSessionSlot` now READING the tree-removal result
instead of discarding it (`_ = removeSlotTree(st)` at `pkg/adapter/slotsession.go:217`) — it adds one
branch and at most one `slog.Warn`, no store write and no extra hold; (c) CODE-9's two extended
gatewaymetrics cases and the `gatewaymetrics_elicitation_test.go` files-touched entry — test-only;
(d) the CODE-10 arm-rule rewrite, the DOCS-2 `DemoteSDK` conditionalisation and the summary index and
open-decision-45 lines — comment and prose only.

FACT (durable, re-verified this round by grep rather than by trusting the ledger): the capacity half of
this lens is structurally vacuous on the non-spec staging. `grep -n "etcd\|Postgres\|Redis\|informer\|
watch"` over non-spec-changes.md still returns only the two sites describing the SHIPPED Redis
slot-counter behaviour a `leaked=true` release already has. No staged deliverable adds a per-session or
per-request write to etcd, Postgres or Redis, a CRD field, an informer, a watch or a label dimension.
EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:782, :1733.

FACT: the aggregate-bound question on the §10.1.4 pass is ALREADY PRICED in the staging and is not a
delta item. `onHoldTimeout`'s single ten-second context becomes the pass's guard-acquisition deadline
alone and each member mints its own ten-second close context; the failure mode where a parked member
spends the whole acquisition deadline and leaves the remaining members removing unguarded is written
out as an accepted mode, with the ground that unguarded removal is the shipped behaviour of that pass
and that each member still closes on a live context so no final usage report is lost and
`budget_return.lua` still runs on complete totals. EVIDENCE: non-spec-changes.md:3827-3834, :3984-3986;
shipped shared context at pkg/adapter/holdstate.go:201.

WATCHOUT for the next performance agent, restating `[non-spec.2.review-performance.1]` because it held
again: this lens has now fired roughly fourteen times on this proposal and most of those were over text
that did not move in a way this lens can reach. Run the snapshot `diff -ru` FIRST, classify every hunk
as write-path / bound / label / none, and if every hunk lands in "none" read `### Open` and stop. A
filing here needs a mechanism that is NEW in the staged text, not a re-derivation of the durable-
occupancy asymmetry, the replica-local `slothealth` ledger, the compensating `Shutdown` pinning a
gateway goroutine, or the exclusive-pool resume budget. All four are standing Open items declined by
five or more rounds.

USEFUL [non-spec.2.review-performance.1]: its "classify the delta, then stop" method and its four
named-and-killed alternatives are exactly what made this round cheap. Keep it through compaction.

USEFUL [Standing context: "The staging adds NO per-session or per-request write to etcd, Postgres or
Redis and net-REDUCES Postgres writes" and "The staged registry critical section puts NO slow act under
the one registry lock"]: these two are the whole capacity answer for this proposal and both re-verify
against the current text; a future performance round can cite them rather than rebuild the math.

### [non-spec.3.review-reliability.1]

VERDICT: EMPTY. Nothing met the bar under the reliability and fault-tolerance lens.

DECISION: returned no findings — BECAUSE the round-2→3 delta is the tree-removal-seam
relocation (CODE-1 → CODE-6) plus four smaller edits, and every reliability property the
relocation touches re-verifies against the tree — ALTERNATIVES: I considered filing (1) the
guard-acquisition fall-through on a cancelled rollback context, (2) the unbounded-in-N pass-2
wall clock created by per-member close contexts, and (3) the permanent reclaim hold a failed
`releaseSessionSlot` tree removal leaves on a same-pod retry's session identifier. All three
are explicitly reasoned in the staging (see the three FACTs below) and none makes the described
implementation broken, so each defaults to refuted under the rubric.

FACT: every citation the NEW seam bullet introduces re-verifies byte for byte.
`_ = removeSlotTree(st)` is `pkg/adapter/slotsession.go:217`; `_ = s.Runtime.Close(ctx, ...)` is
`pkg/adapter/holdstate.go:249` and `_ = removeSlotTree(m.state)` is `:254`; `removeSlotTree` is
`pkg/adapter/slot.go:210-212`; the "os.RemoveAll returns nil for a missing path" sentence is
`pkg/adapter/slotlayout/tree.go:54-55`; `scrubDone` is `pkg/adapter/server.go:197-201`;
`HoldAfterFunc` is `pkg/adapter/holdstate.go:63`; `ExpiryAfterFunc` is
`pkg/adapter/credexpiry.go:49`; the chmod-injection rationale is
`pkg/adapter/warmlayout_test.go:162-167`; the shared-close-context comment the deliverable
rewrites is `pkg/adapter/holdstate.go:197-200`. Do not re-verify this set.
EVIDENCE: pkg/adapter/holdstate.go:249,254; pkg/adapter/slotsession.go:217

FACT: the guard fall-through on an expired acquisition is a STATED design choice, not a gap.
`non-spec-changes.md` "No guard acquisition outlives its caller's context." says a destructive
section whose acquisition expires removes unguarded and logs `slot_guard_not_acquired`, "because
removing unguarded is exactly what the shipped code does today". The reclaim hold is still
opened, because `reclaimSlotLocked` inserts it under `s.mu` independently of the guard, so the
admission window is closed even when the guard is not taken. A start rollback on a cancelled
client context is the common case of this and is covered by that clause.
EVIDENCE: proposals/.../non-spec-changes.md:1747-1759

FACT: pass 2's per-member ten-second close context is minted independently of the pass context,
so it stays live after the pass's guard-acquisition deadline expires, and the staging says so in
two places (the CODE-6 `terminateHeldSession` bullet and the accepted-risk bullet "A member
parked under its own guard can cost the §10.1.4 pass its whole guard-acquisition deadline").
The wall-clock growth in N is bounded in practice by the shipped observation the deliverable
keeps: a non-last close on a shared runtime process returns without touching the child.
EVIDENCE: proposals/.../non-spec-changes.md:1447-1450, :3827-3834

FACT: `deregisterStartedSessions` holds `s.mu` across every member, does no path work, and
sorts by session identifier before deregistering, so CODE-6's "pass 1 opens a hold for every
member while holding `s.mu` and takes no guard" is exact. It is also what makes the new hold
correct: before CODE-6 a bind arriving between pass 1 and pass 2 could create a fresh entry
under the same identifier while pass 2 still held the OLD `*slotState` and removed its tree,
which is the same directory.
EVIDENCE: pkg/adapter/slotsession.go:375-395

FACT: the round-2 bullet's `Shutdown` assertions were not lost in the relocation. The
pre-`running` arm is pinned by CODE-1's "`exited_cleanly` on a reclaim the runtime never held"
and the `running` arm (hold retained, report `released`) by the newly extended "The runtime's
own answer still decides for a started session". A future round should not re-file the CODE-6
bullet's narrowing as lost coverage.
EVIDENCE: proposals/.../non-spec-changes.md:3125-3138

USEFUL [Settled: "The completion predicate differs by site, deliberately, and all three are
stated."]: this is the entry that stops the reliability lens re-deriving why `releaseSessionSlot`
keys the hold on the tree removal alone while `terminateHeldSession` keys it on both errors. It
saved the whole re-derivation.

USEFUL [Settled: "DECISION: the reclaim hold ends only on a cleanup that COMPLETED, and
otherwise holds for the life of the pod."]: without it the "leaked hold with no reclaimer" family
looks like a live finding. It is a recorded choice; the adapter pod's `RestartPolicy: Never` is
the outer reclaimer.

### [non-spec.3.review-security.1]

VERDICT: EMPTY. Sixth consecutive empty security verdict on this proposal (five on the spec
lane, this one on the non-spec lane). I declare the security candidate space exhausted over the
non-spec staging as it now stands.

FACT: The teardown-precondition fail-closed chain re-verifies end to end in the tree, so no
later lens needs to re-derive it. `Client.shutdown` is a real shipped single builder behind both
exported teardown forms (`pkg/gateway/runtime/adapterclient/client.go:813-824`, called from
`Shutdown` at `:808` and from `ShutdownRecycle` at `:861-867`), so CODE-4's
"`unconditional_teardown = true` is set inside the builder, no caller can forget it" is
structurally available today, and the fenced `ShutdownReclaim` is correctly stated as a THIRD
method that does not route through that builder (non-spec-changes.md:935-940, :1936-1939). A
compensation therefore cannot acquire `unconditional_teardown` by accident, which is the one
failure that would let an abandoned attempt destroy a live successor.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824,:860-867

FACT: CODE-8's credential-isolation argument re-verifies line for line at every cited site.
`failPhase` gates `releaseCredentials` on `leaseAssigned` and calls `b.drain` outside the block
(`binder.go:1072-1082`); `Binder.Launch`'s closure passes the literal `true` (`:998`);
`Binder.Prepare`'s passes a flag declared false at `:865` and set true at `:954`, after the
`assignCredentials` call at `:949`; `credassign.Service.ReleaseSession` is the session-keyed
walk over `LeasesBySession` (`credassign.go:400-408`) and `releaseLocked` no-ops on an unknown
lease id (`:414-418`). Skipping `failPhase` on `SLOT_BIND_ALREADY_STARTED` therefore does not
strip a live session's §4.9 leases, and the attempt-scoped `releaseAttemptCredentials(minted)`
on `Prepare`'s credential-assignment arm closes a lease leak that ships today with no reclaimer.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082,:998,:865,:949,:954

FACT: The leak-ledger citations CODE-1's reasoning rests on are exact.
`leaked = err != nil || !cleanly` is at `pkg/gateway/podlifecycle/podsession/slotbinder.go:543`;
the persistent, never-pruned leak count is `slothealth.Tracker.RecordLeak`/`Unhealthy`
(`slothealth.go:121-136`); `drainLedger.RecordLeak` stamps `drain-request` the moment
`Unhealthy` is true (`pkg/gateway/session/recycle/scrubreporter_seams.go:184-197`). So the
argument for keying the cleanup-outcome report on `closeErr` alone (rather than on `closeErr &&
treeErr`) is a genuine fail-safe choice rather than a relaxation: keying it on both would retire
a pod on the first `os.RemoveAll` failure at an ordinary session end.
EVIDENCE: pkg/gateway/session/recycle/scrubreporter_seams.go:184-197

WATCHOUT: the tempting security finding here is "SPEC-3 withholds the cleanup-outcome report for
a pre-`running` reclaim, so `sessions_served` no longer advances for a bound-but-unstarted
release that DOES report on the shipped tree (review-log Settled entry on `st.sessionID`'s two
setters), which relaxes the `RetireOnSessionCount` reuse bound." Do not file it. It is a SPEC-3
consequence, the spec lane is locked, the security lens already passed over SPEC-3 five times
returning empty, and the bound it touches covers sessions that never ran, so it lands squarely
under the lens's own "merely less strict than it could be is NOT a finding" exclusion.
EVIDENCE: review-log.md:190, review-log.md:374

FACT (negative, checked so nobody re-checks): the `Server.removeSlotTreeFn` seam CODE-6 now owns
is not a security surface. It is unexported, nil-defaulted, has no setter, nothing in production
assigns it, and the package already carries three of the same form (`scrubDone`,
`HoldAfterFunc`, `ExpiryAfterFunc`). Moving its statement from CODE-1 to CODE-6 in this round
changed nothing about that.
EVIDENCE: non-spec-changes.md:1485-1504

### [non-spec.3.review-single-source.1]

Verdict: EMPTY. No rule stated in full at two sites that meets bar (g).

FACT: the round-3 diff is purely reductive for this lens. The tree-removal seam moved out of
CODE-1 into a new CODE-6 bullet, and the three other sites now cite it rather than restate it.
Home: non-spec-changes.md:1485-1504 (`**The tree-removal seam.**`). Citing sites: CODE-1
:512-515 ("the tree-removal seam CODE-6 states"), CODE-6 test :2953 ("the seam CODE-6's
tree-removal bullet states"), tier-1 seam note :3027 ("which CODE-6 states and these cases
reuse"), summary CODE-6 index line. CODE-1's header and target list dropped
`pkg/adapter/slot.go` (:158, :178) and the summary index matches (summary.md CODE-1 line).
Nothing left claims `removeSlotTree` is called directly by a site whose hold release reads the
result. EVIDENCE: non-spec-changes.md:1451, :1468-1469, :1485-1504.

FACT: the hold-release predicate has one general home and three per-site instantiations, which
is the correct form and not a copy. Home is the `reclaimSlotLocked` bullet
(non-spec-changes.md:1428-1432, "takes it only on the arm where the cleanup it then ran
completed ... because §5.2 ends the hold on a completed cleanup"). Instantiations:
`terminateHeldSession` `closeErr == nil && treeErr == nil` (:1452, cites Shutdown),
`releaseSessionSlot` "takes the release when the removal returned nil" (:1476-1478), and
`Shutdown`'s `completed = closeErr == nil && treeErr == nil` (:440). Each names only the acts
its own cleanup owes. Do not file these as duplicates.

WATCHOUT: `exited_cleanly` reads like a duplicate and is not one worth filing. The rule's spec
home is the §5.2 disposition table's clean-exit column (spec-changes.md:618-628) with rule 15
pointing at it (spec-changes.md:1066). CODE-1 then carries it three times inside one
deliverable: the code `closeErr == nil && (live || treeErr == nil)` (non-spec-changes.md:495),
the "stays keyed on" sentence (:487-490) and the prose restatement (:517-519). Two of the three
are a code deliverable describing its own implementation plus the rationale for the `live`
gate, so a finding here fails the bar for rationale-and-implementation sites. I considered it
and rejected it.

WATCHOUT: the proto field comments at non-spec-changes.md:2495-2500 restate rule 1's pairing,
the stamp-once rule and rule 10's pairing, which §4.7.1 owns. This is the same licensed-carrier
case as a docs page, with its reason written out on the page ("a third-party implementor reads
the proto before the specification"). It is old text that has survived many rounds. Not a
finding; do not re-open it without new evidence that the proto text and §4.7.1 disagree.

FACT: the new CODE-9 test citations check out against the tree.
`TestCredentialAndLLMProxyAndSlotMetricsEmit` is at
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:536 and
`TestNewMetricsEmittersNilSafe` at :576, each with an `IncSlotFailure("session_start",
"pool-a"|"p", "sbx-1")` line to sit beside (:550, :585).

UNVERIFIED: non-spec-changes.md:3295-3302 says the new `IncSlotCompensationSuperseded` call is
"passed in the parameter order of the `SlotReclaim` hook CODE-9 declares", but that hook is
`func(outcome, pool, podName string)` (:2053) while the accessor takes no outcome (the
forwarder filters on it at :2059-2060) and the series carries only `pool` and `k8s_pod_name`
(spec-changes.md:1221). The phrase resolves only if read as "pool before podName". A
citations or mechanism lens should decide whether that wording needs tightening; it is not a
single-source defect.

### [non-spec.3.review-test-coverage.1]
FACT: The whole-pod recycle scrub is gated on `req.GetRecycle() != nil` and today runs as a trailing statement on every `Shutdown` path except the empty-session-id return — EVIDENCE: pkg/adapter/session.go:288-290, :229-231
FACT: `startPodScrub` runs `scrub.Run` (process kill, credential purge, workspace scrub) in a goroutine and reports through `PodScrubReporter`, so a spurious start is destructive pod-wide and files an unasked-for ReportPodScrub — EVIDENCE: pkg/adapter/podscrub.go:40-80
DECISION: Filed exactly one finding, the unpinned "scrub included" arm of the two-field precondition — BECAUSE the staged §4.1 sentence makes the scrub conditional on passing rule 10 (spec-changes.md:237) and CODE-1 states the `INVALID_ARGUMENT` return performs nothing "the scrub included" (non-spec-changes.md:506-510), yet the four-row precondition case carries no `Recycle` and the two scrub cases both drive admitted arms (non-spec-changes.md:3050-3053, :3138-3153) — ALTERNATIVES: rejected filing on the DOCS-1 pod-state-machine paragraph having no tier-11 substring assertion, on the nil-`SlotReclaim`-hook arm, and on the `slot_tree_removal_failed` / `runtime_close_failed` log lines having no assertions; all are nice-to-have coverage below the bar.
FACT: The CODE-9 collector/accessor gap from the last round is closed and the cited shipped cases are real: `TestCredentialAndLLMProxyAndSlotMetricsEmit` (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:536, `IncSlotFailure` at :550) and `TestNewMetricsEmittersNilSafe` (:576, `IncSlotFailure` at :585), both in package `gatewaymetrics_test`, so the accessor must stay exported.
FACT: Every seam citation the tree-removal bullet added this round checks out — pkg/adapter/slot.go:210-212 (`removeSlotTree`), pkg/adapter/slotsession.go:217 and pkg/adapter/holdstate.go:254 (the two discards), pkg/adapter/server.go:197-201 (`scrubDone`), pkg/adapter/holdstate.go:63 and pkg/adapter/credexpiry.go:49 (the AfterFunc seams), pkg/adapter/slotlayout/tree.go:54-55 (os.RemoveAll nil on absent), pkg/adapter/warmlayout_test.go:162-167 (the chmod-injection rationale). `s *Server` is in scope at both discard sites, so `s.removeSlotTreeVia` compiles at each.
UNVERIFIED: `Metrics.IncSlotCompensationSuperseded`'s arity. The hook is `SlotReclaim func(outcome, pool, podName string)` (non-spec-changes.md:2053) and `metricsbackfill.go` assigns the method value directly, which forces three parameters with `outcome` unused, while the asserted exposition line carries only `pool` and `k8s_pod_name`. Workable but unstated; the mechanism lens should confirm rather than the test lens.

### [non-spec.4.review-test-coverage.1]
VERDICT: EMPTY. Round-4 diff against the r3 snapshot was three hunks, all in CODE-1 (the `answerShutdown` code comment at non-spec-changes.md:299-313, the matching doc-comment bullet at :583-590, and the tier-1 case list at :3054-3062 and :3158-3161). All three close prior-round findings under my lens; nothing new meets the bar.
FACT: the two tier-1 fixtures the new precondition assertion names both exist in package `adapter`, so the case can set the unexported seam directly — `recycleServer` with the `s.scrubDone = func(){ close(done) }` seam at pkg/adapter/podscrub_test.go:149-161 (seam declared at pkg/adapter/server.go:197-201) and `startRuntimeOps` used from package-internal tests (pkg/adapter/drain_test.go:17, package `adapter` at :3). EVIDENCE: pkg/adapter/podscrub_test.go:149-161
FACT: the new negative assertion agrees with the locked spec staging, so it is not a pin against text the spec does not give. §4.1's replacement sentence scopes the scrub to "a request that passed the teardown-pairing rule ... whatever outcome that request answers" (spec-changes.md:237) and rule 10 says an `INVALID_ARGUMENT` request means the adapter "performs neither teardown, removes no entry, and changes nothing" (spec-changes.md:1047-1049 region, rule 10 at the `**`Shutdown` states which teardown it asks for.**` list). EVIDENCE: 0081_...spec-changes.md:237
FACT: CONF-1's rule-10 case (non-spec-changes.md:2259-2261) asserts only status plus entry/tree/credential-file intactness and does NOT assert scrub suppression. That is not a gap worth filing: the tier-1 precondition rows now pin it, and a conformance battery case is not obliged to duplicate it. A future lens should not file it.
WATCHOUT: the shipped `Shutdown` still starts the scrub as a trailing statement (pkg/adapter/session.go:289), which is the behaviour CODE-1 moves into `answerShutdown`; do not read that line as contradicting the new test pin.

### [non-spec.5.fix-G1.1]

DECISION: reduced the two describing sites (implementation-checklist.md S2, summary.md SPEC-1 deliverable index) from "state(s) the two-field precondition and its `INVALID_ARGUMENT` answer" to "name(s) the two fields a request carries, one of `bind_attempt` and `unconditional_teardown`" — BECAUSE the staged §4.7 `Shutdown` row states carriage only and closes with "this row restates neither", and rule 10 is the one home of the `INVALID_ARGUMENT` answer — ALTERNATIVES: adding the answer to the staged row (rejected: spec lane is locked and it would give rule 10 a second normative home inside spec/04, which the SPEC-1 commentary says the row was cut to avoid); deleting the clause outright (rejected: the row does state the carriage, so the index would understate the block).

FACT: the staged §4.7 `Shutdown` replacement row carries no refusal answer anywhere; its closing sentence is "[Section 4.7.1] states the named rules that decide both, and states what each of `reclaimed`, `superseded`, and `absent` means; this row restates neither." — EVIDENCE: spec-changes.md:277, commentary at :283-284, rule 10 at :1061

WATCHOUT: the DOCS-2 describing sites (summary.md:1142, implementation-checklist.md:29) say "the two-field precondition" with no `INVALID_ARGUMENT` clause, and that scoping is deliberate: an earlier round removed the answer clause from them for the same reason. Do not "harmonise" them with the SPEC-1 wording — EVIDENCE: review-log-archive.md:62606, :62664

FACT: the near-verbatim duplication of one sentence across the checklist step and the deliverable index is why neither site caught the other's falsehood. When a deliverable index bullet and its checklist step share a sentence, verify both against the staged block rather than against each other — EVIDENCE: summary.md:1122, implementation-checklist.md:19

### [non-spec.5.fix-G2.1]
DECISION: closed the rule 6 conformance-case finding by widening the citation form only, "asserts the status, the `ErrorCode` and the category that rule states for the first", matching the rule 5 sibling entry — BECAUSE staged §4.7.1 rule 6 is the single home of the refusal's status, `ErrorCode` and `CATEGORY_PERMANENT`, so the case cites rather than restates — ALTERNATIVES: writing `CATEGORY_PERMANENT` literally into the case (a second full statement of the rule, barred by STATE EACH RULE ONCE); adding a category assertion to every CONF-1 case (rules that fix no category would assert an invention); editing the tier-3 and tier-10 lists (they name rules and arms only and carry no assertion set, so there was nothing to correct).
FACT: the tier-3 behavioural-case list carries CONF-1's cases by rule name and arm only; the one apparent exception, rule 5's "with the detail asserted to carry the code", names the detail carrier rather than an assertion set, so a change to a CONF-1 case's assertion wording cascades nowhere — EVIDENCE: 0081_..._.non-spec-changes.md, "The behavioural cases" list under the tier-3 heading, rule 5 and rule 6 entries.
WATCHOUT: the rule 6 case's two exempt arms (the §7.4 mid-session upload, `TestMidSessionUploadIsAdmittedOnAStartedSession`, and the repeat `ConfigureWorkspace`) are load-bearing from an earlier round and sit in the same sentence a category edit touches; edit the assertion clause alone — EVIDENCE: 0081_..._.non-spec-changes.md, CONF-1 rule 6 case entry.

### [non-spec.5.fix-design-G1.1]
DECISION: Fix SPEC-1's two describing sites (summary.md:1122, implementation-checklist.md:19) by replacing "state(s) the two-field precondition and its `INVALID_ARGUMENT` answer" with "names the two fields a request carries, one of `bind_attempt` and `unconditional_teardown`", keeping the existing "points at §4.7.1 for the named rules" clause — BECAUSE the staged row at spec-changes.md:277 states carriage only and explicitly says "this row restates neither"; rule 10 (spec-changes.md:1061) is the one home of the `INVALID_ARGUMENT` answer — ALTERNATIVES: adding the answer to the staged row (rejected: the staging is locked this run, and it would give rule 10 a second normative home inside spec/04, which the SPEC-1 commentary at spec-changes.md:283-284 says the row was cut to avoid); deleting the clause without replacement (rejected: the row does name the two fields, so the describing sites should say what it does).
FACT: DOCS-2's parallel describing sites (summary.md:1142, implementation-checklist.md:29) were already reduced in an earlier round for the same reason and are correct as they stand; do not sweep them. — EVIDENCE: review-log-archive.md:62606, :62664
WATCHOUT: the two false sentences are near-verbatim of each other, so a fixer editing only one leaves the group open. Both must move in the same edit. — EVIDENCE: summary.md:1122, implementation-checklist.md:19

### [non-spec.5.fix-design-G2.1]
DECISION: G2's single finding is TRIVIAL and applied as suggested — one sentence in the CONF-1 rule 6 case gains "the category", matching the sibling rule 5 case word for word — BECAUSE the fix is a citation-form widening ("the status, the `ErrorCode` and the category that rule states"), which keeps the assertion set owned by staged §4.7.1 rule 6 and restates nothing. ALTERNATIVES: spelling out `CATEGORY_PERMANENT` in the case entry (rejected: second full statement of the rule, banned by STATE EACH RULE ONCE and inconsistent with the rule 5 sibling); adding a category assertion to every case entry (rejected: rules that state no category would then cite a clause their rule does not fix).
FACT: the tier-3 and tier-10 lists carry CONF-1's case list by rule NAME only and restate no assertion set, so they need no edit for an assertion-set change. Rule 5's tier-3 line ("with the detail asserted to carry the code") is the only per-rule addendum there and is about the detail message, not the category. EVIDENCE: non-spec-changes.md:3425-3440
WATCHOUT: do not touch the rule 6 mid-session/ConfigureWorkspace arms in the same edit; the exempt-arm sentence and the `TestMidSessionUploadIsAdmittedOnAStartedSession` naming are load-bearing from an earlier round (the §7.4 regression that stood three rounds). EVIDENCE: non-spec-changes.md:2239-2245

### [non-spec.5.review-applicability.1]

VERDICT: EMPTY. Nothing met the bar under the applicability-and-sequencing lens in round 5.

FACT: The checklist is complete and acyclic as of this round. 25 steps, 25 unchecked boxes, 0 checked. Every staged deliverable is named by at least one step (SPEC-1..6, DOCS-1..4, CODE-1..11, SCHEMA-1, CONF-1); every `Depends on:` names an earlier, existing step; every step carries exactly one lane (`spec`, `docs`, `code`, `schema`), all of which `.claude/skills/implement-proposal/SKILL.md:17-18` recognises; the six spec steps lead in one block. — EVIDENCE: implementation-checklist.md:17-66
FACT: Only ONE production site constructs `adapterv1.ShutdownRequest`: the shared unexported builder `Client.shutdown` at pkg/gateway/runtime/adapterclient/client.go:813-819, reached by both `Client.Shutdown` (:807) and `Client.ShutdownRecycle` (:860). CODE-4's statement that `unconditional_teardown` is "set inside `Shutdown` and `ShutdownRecycle`" (non-spec-changes.md:939-944) is therefore load-bearing against the shared builder: setting it in `Client.shutdown` itself would make the new `ShutdownReclaim` (:1940) carry both fields and be refused by rule 10. The proposal states the constraint twice (:941-942, :1935-1937), so it is specified, not a defect — but an implementor who reads only ":the field is set in a single place" could still land it in the builder. — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:813-819; non-spec-changes.md:939-944
FACT: The claim-register validator checks a row's `spec_anchor` against §28 headings, its status against a closed set, a WIRED row's surface against a line-only form, and a non-WIRED row's `deferral_id` against the remediation plan's declared steps. It does NOT check that the `surface` paths exist. So the ABSENT row's surface naming `tests/tier3_contract/adapter_bind_attempt/` and the tier-10 file is not a forward reference that any gate can fail, whichever step lands it. — EVIDENCE: tests/tier0_static/claim_register_test.go:259-305
UNVERIFIED: SCHEMA-1 says "Three rows, added to the `EXPLICIT` list ... and regenerated ... in the same commit" (non-spec-changes.md:2525-2527), while checklist S9 names only "the two `WIRED` rows" (implementation-checklist.md:33) and both S22 (:59) and the Testing section (non-spec-changes.md:3464) put the `ABSENT` row with the tier-10 conformance file. I did not file it: no gate fails either way (see the FACT above), the end state is identical, and three near-identical per-step-attribution findings were already refuted as immaterial in earlier rounds. A later round that wants it closed should close it as a one-word checklist edit, not as a defect.
FACT: CODE-10's grep, run against the tree today, returns 47 lines across pkg/, cmd/, tests/, migrations/ and schemas/, including the two generated `pkg/proto/adapter/v1/*.pb.go` copies (dispositioned to SCHEMA-1 by regeneration), `pkg/adapter/server.go:169` (CODE-1), `migrations/0167_...up.sql:102` (CODE-10 in place), and the two tier-11 files SPEC-3's carrier table assigns to checklist S4 (`spec_28_register_writers_test.go`, `concurrent_slot_lifecycle_doc_reconciliation_test.go`). Every hit I checked is either dispositioned by the carrier table or reachable by one of CODE-10's two arms, so the predicate-defined closure is applicable as written. — EVIDENCE: non-spec-changes.md:2139-2152; spec-changes.md:594-610
USEFUL [the orchestrator's refuted list]: the three refuted checklist-bookkeeping findings (S23's tier list, S22's Depends-on, the S16/S17 gate-widening split) saved me from re-filing the same class; the pattern is that a per-step annotation whose consumer already gets the right answer from the ordered list is not a defect here.

### [non-spec.5.review-citations.1]

VERDICT: EMPTY.

FACT: the citations lens has now swept this document twice with no finding. Between the round-2
baseline (`scratchpad/cp-snap/0081-opt2/non-spec-r2`, which `[non-spec.2.review-citations.1]`
read and returned EMPTY on) and the current tree, `diff -ru -x '*review-log*'` over the whole
proposal directory returns exactly three hunks, all in non-spec-changes.md (the CODE-1 handler
comment at ~:296-315, the CODE-1 "State the whole-pod recycle scrub" bullet at ~:580-588, and
the two Testing bullets at ~:3054-3062). Nothing else in any proposal file changed, including
the summary, the checklist and the locked spec staging. A future citations pass can start from
that diff.

FACT: the three changed hunks introduce no new file:line citation. Every citation they carry is
pre-existing and re-verified here: `pkg/gateway/podlifecycle/podsession/slotbinder.go:542`
(`cleanly, err := result.Adapter.Shutdown(...)`), `:574` (`result.Adapter.ShutdownRecycle(...)`),
`pkg/gateway/podlifecycle/podsession/binder.go:1994` (`b.shutdownAdapter(ctx, result, true)`) and
`:2037` (`result.Adapter.ShutdownRecycle(...)`) — EVIDENCE: pkg/gateway/podlifecycle/podsession/
slotbinder.go:542,574; pkg/gateway/podlifecycle/podsession/binder.go:1994,2037.

FACT: the new Testing text's appeal to "the staged §4.1 scrub sentence and §4.7.1 rule 10" is
sound. The staged §4.1 sentence conditions the scrub on "a request that passed the
teardown-pairing rule ... whatever outcome that request answers"
(spec-changes.md:237) and staged rule 10 says a refused request means the adapter "performs
neither teardown, removes no entry, and changes nothing" (spec-changes.md:1061). Both support the
new assertion that a precondition refusal starts no scrub — EVIDENCE:
…spec-changes.md:237, :1061.

FACT: the new Testing text's fixture references resolve. `startRuntimeOps` is a real
pkg/adapter test helper (pkg/adapter/runtimeops_test.go:83), and
`TestRecyclePathScrubReportedReuses_spec_5_2` is at tests/tier4_integration/
recycle_scrub_path_test.go:404 with its `binder.Release` at :429, exactly as cited.

FACT: a mechanical bounds check over all 174 distinct `file:line` citations in
non-spec-changes.md, plus every citation in the summary and the checklist, produces no
out-of-range and no missing file once bare basenames are resolved against their deliverable's
own package. Five basenames resolve only in `pkg/adapter` and one only in
`pkg/observability/metrics`, and all six are correct there: `checkpoint.go:140`
(`s.probeWorkspaceBytes(roots)`), `manifest.go:280-283` (`writeSessionManifest` + its
`ManifestDir == ""` guard), `server.go:111-114` (the `ManifestDir` field),
`catalog.go:146`/`:271` (pkg/observability/metrics/catalog.go rows for
`lenny_credential_rotation_inflight_ceiling_hit_total` and `lenny_adapter_coordinator_hold`).
A future pass should resolve bare basenames this way rather than by `find`, which picks the
wrong file.

FACT: every verbatim comment quote in CODE-3's comment-site list is byte-exact against the tree.
Checked: pkg/sandbox/slotstate/slotstate.go:34-47 (the four `SubState` const comments),
pkg/sandbox/slotstate/registry.go:93-95 (`MarkLeaked`),
pkg/gateway/runtime/slothealth/slothealth.go:33 and :110,
pkg/gateway/sessionserver/sessionserver.go:417-420 (`slotLeakGauge`) and :1665-1669
(`SlotLeakGauge`), pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:71-73 and
:219-221. This list is the densest quotation surface in the proposal and it is clean.

FACT: DOCS-2's row anchors are current. `| \`DemoteSDK\` |` is docs/reference/
adapter-contract.md:64, `| \`Shutdown\` |` is :75, `| \`ReportSessionScrub\` |` is :81, and both
named tier-11 gates exist and assert what DOCS-2 says they assert:
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` (tests/tier11_docs/
basic_level_echo_stamp_doc_reconciliation_test.go:294) requires exactly the four substrings
"end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub", all of
which survive DOCS-2's replacement row; `TestSessionScrubReportAddressingAgreesBetweenSpecAnd
ContractDoc` (tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43)
requires the opener "The request is session-scoped: it is " + `sessionScrubAddressingRule` + "."
in both carriers, which DOCS-2's replacement keeps word for word.

FACT: DOCS-3's absence claim holds. `grep -rn "error-catalog" tests/ scripts/ cmd/ Makefile`
returns nothing, so no gate binds docs/reference/error-catalog.md:129 to §15.1, exactly as the
deliverable states.

FACT: open decision 45's attribution is correct after its earlier fix. The gateway declares and
registers `lenny_adapter_leaked_slots` — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/
gatewaymetrics_credential.go:222-225. Its claim of "no `docs/reference/metrics.md` row today"
also holds: `grep -c lenny_adapter_leaked_slots docs/reference/metrics.md` is 0.

USEFUL [non-spec.2.review-citations.1]: its EMPTY verdict over a document that has since changed
in three hunks is what let this pass concentrate on the delta instead of re-walking 174
citations blind. The snapshot-diff-first method the standing context prescribes paid for itself
here.

### [non-spec.5.review-client-surface.1]

VERDICT: EMPTY. Round 5, client-facing surface integrity lens, whole proposal (spec-changes + non-spec-changes + checklist + summary read as one document).

USEFUL [standing context, "No OpenAPI document and no language SDK mirrors any edited surface"]: re-verified in this round and it again saved a full sweep. `pkg/gateway/externalapi/openapi/openapi.json` carries no error-code enum at all (`grep -o "SESSION_CREATION_FAILED\|STARTING_FAILED\|setup_command_failed"` returns nothing; only `setup-output` appears as a path), and `grep -rln "ShutdownRequest\|lenny.adapter.v1\|exited_cleanly\|SessionScrubOutcome" sdks/` returns nothing. The runtime SDKs carry JSONL/CH-RUNTIMEOPS types only.

FACT: the orchestrator prompt names the OpenAPI document as `pkg/gateway/openapi/openapi.json`. That path does not exist. The real one is `pkg/gateway/externalapi/openapi/openapi.json` — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json (the only non-testdata match for `find . -name "openapi*.json"`).

FACT: SCHEMA-1's nine field numbers all re-verify free against the tree as it stands. PrepareWorkspaceRequest 1-3 used + reserved 4 (schemas/lenny-adapter.proto:683-695); FinalizeWorkspaceRequest 1-4 + reserved 5 (:707-730); RunSetupRequest 1-3 + reserved 4 (:847-856); AssignCredentialsRequest 1-2 + reserved 3 (:1022-1030); ResumeRequest highest live is coordination_generation=14 with 6 and 15 reserved (:1405-1412); ShutdownRequest 1-3, reserved 4, recycle=5, coordination_generation=6 (:1610-1635); ShutdownResponse 1-2 (:1665-1668). ErrorCode tops out at 27 (:584-585).

FACT: the proposal's claim "this is the only closed field set over the messages SCHEMA-1 opens" holds. `grep -rn "assertFieldSet\|FieldNumber]protoreflect.Name\|Fields().Len()" tests/ pkg/` returns only `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` and `tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go:77,151`, and the latter's only exact-count pin is `CheckpointStart` (six fields), a message SCHEMA-1 does not open. `tests/tier3_contract/adapter_session_address/session_address_wire_test.go` touches all six opened messages but asserts only reserved-number/reserved-name and the presence of `session_id`, never a closed set — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:112-175.

WATCHOUT: `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:43-84` is a GOLDEN-BYTES pin, not a round-trip: `TestShutdownRequestUnsetRecycleWireIdentical_spec_4_7` asserts the marshalled `ShutdownRequest{SessionId, Reason, DeadlineMs}` equals a hand-written 20-byte literal. If the mechanical `UnconditionalTeardown: true` sweep the files-touched list describes were applied to the literal at :44, field 8 would emit `0x40 0x01` and that tier-3 gate turns red. It is NOT a defect today, because the sweep entry is scoped to "every OTHER file" and this file is enumerated by name in the Tests files-touched list, with SCHEMA-1 saying "Nothing else in that file moves". A future fixer who broadens the sweep wording, or an implementor who greps `ShutdownRequest{` without reading SCHEMA-1, breaks it.

FACT: the withdrawn reporting universal has one more proto carrier that SCHEMA-1 deliberately leaves alone and is RIGHT to leave alone. `schemas/lenny-adapter.proto:436-437` — "SessionScrubOutcome is the result of the §5.2 per-slot cleanup the adapter runs on every session release." That sentence quantifies over the CLEANUP, which SPEC-3 does not withdraw, not over the REPORT, which it does. It is the same treatment `docs/operator-guide/multi-tenancy.md:72` gets under DOCS-4. Do not file it.

FACT: no gate anywhere enumerates the adapter `ErrorCode` values, so minting 28 and 29 owes no test or catalog edit beyond the ones SCHEMA-1 names. `grep -rn "ErrorCode" tests/ scripts/ docs/` returns only unrelated REST/OpenAI/checkpoint error-code helpers. Likewise no CRD surface: `grep -rn "slot_cleanup\|receiving_uploads\|SlotState" charts/lenny/crds/ pkg/apis/` returns nothing, so CODE-3's slot sub-state edits reach no operator-applied schema.

FACT: DOCS-3's four quoted `SETUP_COMMAND_FAILED` sentences and its remedy cell match `docs/reference/error-catalog.md:129` verbatim, and DOCS-2's three quoted rows match `docs/reference/adapter-contract.md:64`, `:75` and `:81` verbatim at the cited line numbers.

FACT: DOCS-3's premise that a started-session refusal reaches the client under `SETUP_COMMAND_FAILED` only at the setup-commands request is mechanically true. Any `RunSetup` error is wrapped as `&SetupCommandFailure{Cause: err}` at pkg/gateway/podlifecycle/podsession/binder.go:938 and pkg/gateway/podlifecycle/podsession/slotbinder.go:304, and `writeSetupCommandError` (pkg/gateway/sessionserver/start.go:236-249) branches on `status.Code(setupFail.Cause) == codes.FailedPrecondition` to select the 422. `SLOT_BIND_ALREADY_STARTED` is FailedPrecondition, so it lands there; no other stage constructs a `SetupCommandFailure`.

FACT: the four production adapter-`Shutdown` call sites are exactly the two functions the proposal names. `Binder.shutdownAdapter` (pkg/gateway/podlifecycle/podsession/binder.go:2032-2047, reached from `Binder.Release`'s recycle branch at :1994) and `Binder.ReleaseSlot` (pkg/gateway/podlifecycle/podsession/slotbinder.go:528-600, its two calls at :542 and :574). Every CODE-1 citation of those four lines re-verifies. `grep -rn "ShutdownRequest{" --include=*.go pkg/ cmd/` outside tests returns one construction site, pkg/gateway/runtime/adapterclient/client.go:814.

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` holds registered adapter metrics to `docs/reference/metrics.md` AND either spec/16 or a `specCatalogPending` entry, and it asserts NOTHING in the reverse direction. So CODE-9 giving `lenny_slot_shutdown_untokened_entry_total` a metrics.md row and a SPEC-6 §16.1 row while withholding a `pkg/observability/metrics/catalog.go` row satisfies the gate — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:92-117. The ordering consequence is that SPEC-6 and the metrics.md row must not land after the `pkg/adapter/metrics.go` registration in a separate commit, or tier 11 is red in between.

FACT: `tests/tier0_static/claim_register_proto_agreement_test.go` only cross-checks a claim row against the proto when the row's `claim` STARTS with a `Message.field` spelling (regex `^(\w+)\.(\w+)`, :47) or when its surface/note matches "no `x` on `y`" (:51). None of SCHEMA-1's three staged rows does either, so none of them can trip that gate.

OPEN (not a finding, recorded for a later round if anyone wants it): the new tier-3 descriptor gate pins the nine added fields and both new `ErrorCode` values by number and name, but pins no value of the new `SlotReclaimOutcome` enum. Rule 15's behavioural case exercises the values, and both ends regenerate from the same proto, so a renumber is symmetric and invisible either way — the same argument that would make the `ErrorCode` pin unnecessary. I judged this a nice-to-have below the bar rather than a coverage gap.

### [non-spec.5.review-docs-alignment.1]

VERDICT: EMPTY. Nothing met the bar.

FACT: the round-5 diff against the r3-prefix snapshot touches only CODE-1's handler comment,
CODE-1's split-gates bullet, and two tier-1 test bullets, all on the "scrub is outside the arms
that remove nothing" wording. It contains NO docs edit, and the change it makes is a
convergence TOWARD the already-staged DOCS-2 `Shutdown` row, which has said "a request carrying
neither or both is rejected as invalid and performs nothing" since before this round.
EVIDENCE: non-spec-changes.md:296-315, :580-585, :3054-3062, :3158-3163 versus
non-spec-changes.md:2740 (the staged `Shutdown` row).

FACT: the five docs-mirroring sweeps all come back covered, and re-running them costs about ten
greps. Recording the exact commands so the next docs lens does not re-derive them:
- withdrawn reporting universal: `grep -rn "reports its outcome to the gateway\|ReportSessionScrub" docs/`
  returns exactly `security-principles.md:33`, `execution-modes.md:68` (both DOCS-4),
  `adapter-contract.md:75` and `:81` (both DOCS-2). No sixth carrier.
- occupancy projection claim-deletion clauses: `grep -rn "claim deleted\|under its limits" docs/`
  returns only `state-machines.md:138` (DOCS-1). `docs/runtime-author-guide/lifecycle.md:392`
  and `docs/assets/diagrams/recycle-lifecycle.svg:45` state hold-expiry → idle, which SPEC-4
  KEEPS (`reserved` → `idle`), so they stay true.
- `SETUP_COMMAND_FAILED`: `grep -rn SETUP_COMMAND_FAILED docs/` returns only
  `error-catalog.md:129` (DOCS-3).
- `sessions_served` write trigger: no docs page carries "incremented at each session release".
  `adapter-contract.md:81` says only "increments the pod's served-session count", which DOCS-2
  keeps verbatim and which stays true.
- gRPC `Shutdown` RPC: `grep -rn "Shutdown\b" docs/` outside `adapter-contract.md` returns only
  JSONL runtime-frame shutdown (`concepts.md:134`, `runtime-author-guide/testing.md:28,:220`),
  a different mechanism.

FACT: all five DOCS-3 "reads, verbatim" quotations match `docs/reference/error-catalog.md:129`
word for word (four description sentences plus the remedy cell), and §15.1's table has
Code/Class/HTTP/Description columns with NO remedy column
(`spec/15_external-api-surface.md:1136`). DOCS-3's remedy-cell replacement is therefore
docs-only by construction and owes no spec mirror; do not file it as an unmirrored docs edit.

FACT: every substring the Testing section tells the implementor to add is present in the staged
docs text. Checked by hand: `"no other bound session"`, `"a session whose start the adapter has
admitted"`, `"either the bind attempt"`, `"reclaimed"`, `"absent"`, `"**Bind attempt token.**"`
against non-spec-changes.md:2740,:2758; and `` `receiving_uploads` | `slot_cleanup` `` against
the DOCS-1 row at non-spec-changes.md:2592. The `generalSlotEdges` addition is safe in both
directions: `scopedBlock` is `s62[scopedHeader:generalHeader]`
(tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:64) and SPEC-4 inserts the
new edge into the general block only, so the negative loop at :70-73 stays green.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-74,
spec-changes.md:955-960.

FACT: DOCS-1's replacement `slot_cleanup -> leaked` clause is checkable against SPEC-3's
disposition table and agrees on all four arms: running+close-fails and pre-running+act-fails
both give "Entered" and are covered by "a `Shutdown` response that reports no clean exit"; the
unanswered reclaim is covered by "a reclaim it sent is never answered"; every release outside a
`Shutdown` gives "Not entered" and the clause names none.
EVIDENCE: non-spec-changes.md:2645-2648 versus spec-changes.md:635-643.

WATCHOUT: `docs/reference/configuration.md:91`, `docs/reference/execution-modes.md:23` and
`docs/operator-guide/configuration.md:307` all carry "counts every session served" for
`maxSessionsPerPod`. SPEC-3 narrows the increment (report-only, so a session released by the
§10.1 hold-timeout termination is no longer counted), which makes the phrase look like a
carrier. It is NOT a docs defect: the phrase's origin is a spec YAML comment at
`spec/05_runtime-registry-and-pool-model.md:404` that SPEC-3 does not touch, so the docs still
mirror the spec exactly. A finding here would be asking the docs to diverge from the spec,
which guardrail (1) of this lens forbids. If anyone wants it fixed, it is a spec finding
against spec/05:404 and the lane is locked.

USEFUL [standing context, "The docs-alignment lens's two accepted residues ... filed and refuted
in rounds 12, 15, 18 and 20"]: it stopped me re-filing the gateway-crash-stranded entry and the
self-recreated-entry-after-unconditional-teardown case. I checked the one angle the trap does
not cover — that the remedy might be a docs/runbooks edit rather than a spec edit — and it does
not survive either: `docs/runbooks/gateway-replica-failure.md:30` says "active sessions persist
... and reconnect to healthy replicas", which is about a session already running, while the
stranded entry belongs to a session that never bound. The page states no falsified claim.

CORRECTS [review log `### Deferred`, "DEFERRED [docs/runbooks/gateway-replica-failure.md]: the
page calls a gateway crash benign for sessions"]: the page does not call a crash benign. Its one
relevant sentence is scoped to ACTIVE sessions (`docs/runbooks/gateway-replica-failure.md:30`),
and the crash-stranded entry arises on a bind that never produced an active session, so the
sentence is not a counterexample and the page owes no new cause. The deferred entry should be
retired rather than closed by a docs edit.

CORRECTS [review log `### Deferred`, "DEFERRED [docs/getting-started/architecture.md:237]: ...
this page is left short one input"]: this is not a missed edit site, and the proposal's own rule
says so. SPEC-4 adds "the phase the pod currently projects" to §4.6.1's enumeration and
explicitly declines to add it to §6.2's, on the ground that "no surviving §6.2 clause reads the
phase the pod currently projects" (spec-changes.md:876-880). `architecture.md:237` carries the
enumeration and NO claim-deletion clause at all, so it is in §6.2's position, not
state-machines.md:138's. Adding the input there would over-specify a concept-depth page against
the proposal's own stated policy. (Its enumeration already omits `sessionPolicy`, so it was a
loose paraphrase before this proposal.) EVIDENCE: docs/getting-started/architecture.md:237,
spec-changes.md:876-880, spec/04_system-components.md:409.

OPEN: the review log's `DEFERRED [docs/reference/adapter-contract.md, the §15.4 reclaim-hold
half]` is the only docs item I could not fully settle. DOCS-2 mirrors SPEC-5's §15.4 bind-attempt
block and not its slot-identifier reclaim-hold block, and gives an exclusion rationale for the
§4.7.1 cascade (non-spec-changes.md:2713-2718) but none for the hold. I did not file it: the
page's gRPC section is a one-line orientation table, a runtime author issues none of the
requests the hold refuses, and nothing on the page becomes false. A later docs lens that
disagrees needs an argument that an adapter author reads this page rather than §15.4, since
DOCS-2's own added paragraph tells that reader to read both spec sections.

### [non-spec.5.review-edit-sites.1]

VERDICT: EMPTY. The edit-site lens found nothing meeting the bar in round 5.

FACT: the identifier-by-identifier sweep is done; these are the carriers I verified and the
deliverable that owns each, so a later edit-site lens need not redo them.
- `mid_session` already exists on `FinalizeWorkspaceRequest` field 4 (schemas/lenny-adapter.proto:724);
  SCHEMA-1 adds a second one on `PrepareWorkspaceRequest` field 6. Every field number in SCHEMA-1's
  table checks out against the live proto: ShutdownRequest 1-3 + reserved 4 + recycle 5 +
  coordination_generation 6 (schemas/lenny-adapter.proto, `message ShutdownRequest`), ShutdownResponse
  1-2, RunSetupRequest 1-3 + reserved 4, AssignCredentialsRequest 1-2 + reserved 3, ResumeRequest
  1-5,7-14 with 6 and 15 reserved. EVIDENCE: schemas/lenny-adapter.proto:682-695, :705-731
- The per-slot sub-state edge has exactly three carriers: spec/06_warm-pod-model.md:151-152 (SPEC-4),
  docs/reference/state-machines.md:234-237 (DOCS-1), pkg/sandbox/slotstate/slotstate.go:99-108 (CODE-3).
  No migration, chart, CRD enum or SQL check duplicates it. EVIDENCE: pkg/sandbox/slotstate/slotstate.go:107-108
- `docs/api/internal.md` reproduces an *illustrative* RuntimeAdapter service (StartSession/StopSession/
  DemoteSDK) and names no `ShutdownRequest`, no `ErrorCode` and none of the nine added fields, so
  SCHEMA-1 leaves it true. EVIDENCE: docs/api/internal.md:19-33, :96-99
- No test anywhere pins a closed set over `adapterv1.ErrorCode`, so adding 28 and 29 turns nothing red
  beyond the CONF-1 descriptor gate the proposal writes. EVIDENCE: grep -rn 'ERROR_CODE_' --include=*_test.go
  returns only pkg/adapter/staging_test.go and three unrelated ops files.
- The only docs carriers of the universal SPEC-3 withdraws are docs/reference/execution-modes.md:68 and
  docs/operator-guide/security-principles.md:33 (DOCS-4) plus docs/reference/adapter-contract.md:81
  (DOCS-2). docs/operator-guide/multi-tenancy.md:72 states the cleanup WITHOUT the reporting clause and
  correctly takes no edit. EVIDENCE: docs/operator-guide/multi-tenancy.md:72
- Neither spec/16_observability.md:14 nor docs/reference/metrics.md:166 enumerates
  `lenny_slot_failure_total`'s `error_type` values, so CODE-9's new `slotFailureWorkspaceFinalize`
  opens no documentation edit site. EVIDENCE: docs/reference/metrics.md:166

FACT: `tests/tier11_docs/adapter_metric_catalog_test.go` is fully rule-derived (regex over
`Name: "lenny_..."` in pkg/adapter/metrics.go, then substring checks against docs/reference/metrics.md
and spec/16). CODE-9's "not edited" is right, and the new counter must stay OUT of both
`undocumentedAdapterMetrics` and `specCatalogPending` or it is exempted from the sweep.
EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:26,:34,:49,:100,:110

WATCHOUT: `pkg/adapter/metrics.go` registers no metric carrying a `k8s_pod_name` label today (its
label sets are provider/pool/event/frame_type only), while the staged §16.1 row labels
`lenny_slot_shutdown_untokened_entry_total` by `k8s_pod_name`. This is implementable — the adapter
caches its own pod name from the Downward API as `Server.podID` — but the implementor must thread
`s.podID` into the increment, and the fail-closed empty-POD_NAME case that
`sessionscrubreporter.go` already handles has no stated analogue here.
EVIDENCE: pkg/adapter/metrics.go:34,:48,:74,:104; pkg/adapter/server.go:36,:380;
pkg/adapter/sessionscrubreporter.go:74

FACT: the shared-builder question on the gateway client is already answered and should not be
re-filed. `Client.Shutdown` and `Client.ShutdownRecycle` both funnel through one private
`c.shutdown` builder that is the tree's only production `adapterv1.ShutdownRequest{` literal, and
the proposal mints `ShutdownReclaim` as a separate method precisely so the unconditional flag can be
set in the two unconditional forms. `BindResult.Adapter` is the concrete `*adapterclient.Client`, so
there is no gateway-side interface or mock to widen.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-824,:860-866;
pkg/gateway/podlifecycle/podsession/binder.go:523; non-spec-changes.md:1929-1944

FACT: the call-site counts in S15 and the files-touched list are accurate, so a later lens can skip
recounting them. `releaseSessionSlot` has exactly 7 production sites (session.go:133,147,157 and
sdkwarm.go:236,241,251,298), 7 more in resume.go that move to the under-guard form, and 2 internal
test sites (podmcp_arming_internal_test.go:144,195). `ReleaseSlotForTest` has exactly 5 call sites,
in the five files the proposal names, plus its seam in export_test.go:32.

FACT: `tests/tier0_static/spec_map_slot_address_registration_test.go` derives inventory membership
from `slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` over a walk of
cmd/migrations/pkg/scripts/sdks/tests, and `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile`
fails tier 0 for any derived file the inventory omits. Four new files this proposal creates match
that pattern (tier7a slot_bind_attempt_race, tier7a slot_reclaim_hold_race, tier9
slot_credential_reclaim_fence, tier10 slot_bind_attempt_conformance, plus the tier11
slot_compensation_metric_reference). The proposal covers them with the general rule at
non-spec-changes.md:3938-3944 rather than four named rows, which is sufficient.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1173,:1191,:1129

USEFUL [orchestrator TREE FACTS]: the note that `sessions_served` is evaluated at the per-release
drain, decoupled from the recycle disposition, is what stopped me filing
docs/reference/state-machines.md:248 ("Served-session count reaches `recycle.maxSessionsPerPod` on a
session release") as an unstaged site after SPEC-3 re-keys the §12.6 *write* onto the cleanup-outcome
report. The evaluation point and the increment point are different things and only the increment moves.

OPEN: docs/reference/adapter-contract.md:84 ("**Scrub responsibilities.** ... the adapter runs the
credential purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") is in
no edit list and is the nearest thing to a residue of the withdrawn universal on a reader-facing page.
I judged it below the bar: its reader is a runtime author in recycling mode whose session ran, so the
slot reached `running` and the report is owed. A later docs-alignment lens may disagree; if it does,
the remedy is one clause, not a new rule statement.

### [non-spec.5.review-fresh.1]

DECISION: returned EMPTY — BECAUSE every claim I spot-checked in the round-4 diff and in the
surrounding CODE-1/CODE-4/CODE-6/CODE-9/DOCS-2/DOCS-4 text verified against the tree —
ALTERNATIVES: I considered filing the `session.go:283-291` clause-three citation (actual shipped
span is 282-290) and the "the field is set in a single place [Client.shutdown]" vs
"ShutdownReclaim sets bind_attempt" tension in CODE-4; both are below the bar (the quoted code is
inside the cited range; the builder split is ordinary implementation judgment).

FACT: the round-4 edit to CODE-1 (scrub "outside the arms that remove nothing" + the new
precondition-rows test bullet) is CONSISTENT with the locked spec staging. spec-changes.md:237
gives "runs the whole-pod scrub when the recycle disposition is set on a request that passed the
teardown-pairing rule … whatever outcome that request answers" and spec-changes.md:1061 rule 10
gives "the adapter performs neither teardown, removes no entry, and changes nothing". Together
they are exactly what the new tier-1 assertion pins. — EVIDENCE:
proposals/…spec-changes.md:237,1061; …non-spec-changes.md:299-314,3054-3062

FACT: the precondition expression at non-spec-changes.md:200,
`if (attempt == "") == !unconditional`, is correct on all four rows (neither and both reject,
each alone admits). Worth not re-deriving. — EVIDENCE: …non-spec-changes.md:200-204

FACT: defer ordering in the staged CODE-1 handler is right as written. `defer unlockSlot()` is
registered at :356 and the hold-release closure at :399, so LIFO runs the hold release first and
the guard unlock last, which is what the comment at :289-294 claims ("deferred ahead of … so it
runs last"). — EVIDENCE: …non-spec-changes.md:289-294, :356, :399-403

FACT: every gateway citation in the changed hunk and its neighbourhood resolves exactly:
slotbinder.go:542 is the unconditional `Adapter.Shutdown`, :574 the `ShutdownRecycle`;
binder.go:1994 is `b.shutdownAdapter(ctx, result, true)`, :2037 the `ShutdownRecycle` send and
:2043 the plain `Shutdown`; user_revocation.go:129 the §11.4 fan-out; client.go:807-824 and
:860-867 the two exported Shutdown forms and the shared builder. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:542,574;
pkg/gateway/podlifecycle/podsession/binder.go:1994,2037,2043;
cmd/lenny-gateway/user_revocation.go:129;
pkg/gateway/runtime/adapterclient/client.go:807,813,860

FACT: the ten resolve call sites CODE-6 tabulates are exactly the production ones in the tree.
`grep -rn "ensureSlotStateLocked\|ensureSlotPaths" pkg/adapter --include=*.go` returns production
hits only at slot.go:105,140,143, slotcreds.go:26, slotsession.go:75, staging.go:134 (inside
`resolvePrepareStagingDir`, declared :133), :181 (`FinalizeWorkspace`, :159) and :337 (`RunSetup`,
:317), plus credentials.go:74's call into `assignCredentialsSlot`. `rotateCredentialsSlot` does
NOT resolve, so it is correctly absent from the table. — EVIDENCE: pkg/adapter/staging.go:133,159,317;
pkg/adapter/credentials.go:74

FACT: `slotFailureWorkspaceFinalize` is not dead code. CODE-9 mints the constant
(…non-spec-changes.md:2100, files-touched :4022) and the finalize `recordSlotFailure` call site it
replaces is slotbinder.go:294, inside `materializeSlot` (declared :265), which the files-touched
entry already lists. The tier-1 bullet at …non-spec-changes.md:3317-3324 asserts it. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/slotbinder.go:265,287,294

FACT: the DOCS-2 tier-11 substrings all occur verbatim in the staged row
(…non-spec-changes.md:2728): "no other bound session", "a session whose start the adapter has
admitted", "either the bind attempt", "reclaimed", "absent". DOCS-4's two sentences exist with the
stated ending and `docs/operator-guide/multi-tenancy.md:72` already lacks the clause, as the
deliverable says. — EVIDENCE: docs/operator-guide/security-principles.md:33;
docs/reference/execution-modes.md:68; docs/operator-guide/multi-tenancy.md:72

FACT: the negative-scrub assertion the new precondition rows need has a shipped precedent,
`TestShutdownTerminatePathRunsNoScrub_spec_4_7` (pkg/adapter/podscrub_test.go:438-455), which nils
`s.scrubDone` and reads the recorder synchronously. That same test builds a bare
`adapterv1.ShutdownRequest{SessionId: …}`, so it is a member of CODE-1's `UnconditionalTeardown`
sweep set and goes red if the sweep skips it. — EVIDENCE: pkg/adapter/podscrub_test.go:438-455

USEFUL [the caller DIRECTIVE's "one home for the running boundary" and "hold-refusal is closed"]:
both held under a fresh read; I found no second normative statement of the running boundary or of
the refusal cascade outside §4.7.1 and its licensed docs restatements.

### [non-spec.5.review-kubernetes.1]

VERDICT: EMPTY. Sixth consecutive empty verdict for the kubernetes lens on this proposal (four on the spec staging, one on the non-spec staging in round 3, this one).

USEFUL [non-spec.3.review-kubernetes.1]: the "whole Kubernetes surface is FOUR sites" grep saved the whole round. I re-ran it against the current files and the surface is unchanged: CODE-8's refusal arm leaving the per-pod `SandboxClaim` in place (non-spec-changes.md:1951-2035), CODE-5's `accountSlotFailure` keeping the shipped `Unhealthy → DrainSandbox` tail (:1269-1273), the tier-2 envtest pair (:3476-3489), and DOCS-1's pod-state-machine paragraph (:2651-2685). Everything else is in-process adapter state and gRPC.

FACT: the r3→r5 delta is confined to non-spec-changes.md and touches only the whole-pod recycle scrub's scope (the CODE-1 handler comment at :296-312, the §4.1/§4.7.1 justification bullet at :580-588, and two Testing bullets at :3054-3062 and :3158-3163). It carries no Kubernetes surface: `diff -rq` reports only that one file differing. — EVIDENCE: scratchpad/cp-snap/0081-opt2/non-spec-r3-prefix vs the proposal directory.

FACT: the delta's new rule (a `Shutdown` the two-field precondition refuses performs nothing, the scrub included) is fail-closed against the CRD projection rather than against it. The refusal returns an error to the gateway, no `ReportPodScrub` arrives, and §4.7's `ReportPodScrub` row bounds a missing report with a gateway-side timeout "after which the pod is retired", so the unscrubbed pod retires instead of returning to inventory. No claim patch to `reserved` can happen on that path. — EVIDENCE: spec/04_system-components.md:693; non-spec-changes.md:296-312.

FACT: `Binder.Prepare`'s ONLY apiserver write is `drain`'s `podclaim.DeleteClaim` (pkg/gateway/podlifecycle/podsession/binder.go:1201, called from `failPhase` at :1079). Claim CREATE and the `bound` status patch happen at acquisition, above `Prepare` (:1881, :1906). So the tier-2 case's assertion that a typed refusal "leaves the per-pod `SandboxClaim` present at the apiserver and the `Sandbox` untouched" is exactly the observable difference between the two arms, and it needs no field-manager or SSA reasoning. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082, :1188-1202.

WATCHOUT: the SPEC-4 projection now lists "the phase the pod currently projects" as a projection input (spec-changes.md:909), which reads as a controller consuming its own status write. It is NOT a finding: the WPC is the sole writer of `Sandbox.status.phase` (spec/04_system-components.md:409), the read is of etcd level rather than of remembered edge state, so the projection stays level-triggered across a restart. Do not re-file it as a self-referential-status anti-pattern.

WATCHOUT: the claim-deletion partition gap (a DELETE while the pod projects `sdk_connecting` or `warming` answered by no clause) is already derived and killed twice on tree evidence — `ProjectOccupancyPhase` switches only on `Reserved`/`Claimed` and returns `ok=false` otherwise, so those inputs are not the projection's to answer. — EVIDENCE: review-log-archive.md:42861, :42915; pkg/controller/warmpool/occupancy.go:113-145.

OPEN (unchanged, not re-filed): the one genuine Kubernetes-idiom hazard in this neighbourhood remains the coalesced CREATE + `bound` patch + DELETE inside one reconcile window, which leaves an idle unscrubbed pod because the per-pod `SandboxClaim` carries no finalizer. It is pre-existing, correctly scoped out, and already recorded in the summary's unstaged-defects rows and at review-log.md:1098.

### [non-spec.5.review-mechanism.1]

VERDICT: EMPTY. Traced the whole `Shutdown` flow (gateway senders -> wire -> adapter handler ->
scrub -> report) end to end against the tree and found nothing that meets the bar.

FACT: the round-4 fix to the scrub arms is consistent with the LOCKED spec staging. The staged
§4.1 replacement sentence conditions the scrub on "a request that passed the teardown-pairing
rule ... whatever outcome that request answers" (spec-changes.md:237) and staged rule 10 says a
refused request "performs neither teardown, removes no entry, and changes nothing"
(spec-changes.md:1061). The rewritten `answerShutdown` comment (non-spec-changes.md:299-313) and
the doc-comment bullet (:583-591) now agree with both. — EVIDENCE:
proposals/.../0081...spec-changes.md:237,1061

FACT: every gateway `Shutdown`/`ShutdownRecycle` call site the proposal tabulates is real and at
the cited line: slotbinder.go:542 (plain) then :574 (recycle) in `Binder.ReleaseSlot`;
binder.go:1994 -> shutdownAdapter -> :2037 (recycle) / :2043 (plain);
cmd/lenny-gateway/user_revocation.go:129. There is exactly ONE production construction of
`adapterv1.ShutdownRequest` in the tree, the shared builder `Client.shutdown`
(pkg/gateway/runtime/adapterclient/client.go:814), which is why S13's "set the field in the
builder" claim holds with no per-call-site edit. — EVIDENCE:
pkg/gateway/runtime/adapterclient/client.go:812-823

FACT: the CODE-8 call-site sweep line numbers are all exact — `reclaim()` sits at binder.go:883,
918, 923, 935, 950, 1010, 1021, 1032; the closures at :866-869 and :997-1000; `failPhase` at
:1072-1082; `leaseAssigned` declared :865 and set :954; assignCredentials mints at :1225 and
:1248 and sends at :1256. The document's ":949 call site" and ":950 (assignCredentials)" are the
`if err :=` line and the `reclaim()` line of the same site, not a contradiction. — EVIDENCE:
pkg/gateway/podlifecycle/podsession/binder.go:865-869,883,918-950,997-1000,1072-1082,1225-1256

FACT: the new precondition test rows are implementable as written. `startRuntimeOps` is in
package `adapter` (pkg/adapter/runtimeops_test.go:83) and so is `slotsession_test.go`, and the
scrub-done seam a negative assertion needs is `Server.SetScrubDoneHook` /
`signalScrubDone`, fired from a `defer` inside the scrub goroutine. — EVIDENCE:
pkg/adapter/podscrub.go:42,88-101; pkg/adapter/runtimeops_test.go:83

WATCHOUT: the staged §4.1 sentence is loose about the OTHER bypassing return. A request carrying
`unconditional_teardown` + `recycle` but an EMPTY `session_id` "passes the teardown-pairing rule"
yet runs no scrub, because the empty-session-id rejection sits above everything
(pkg/adapter/session.go:229-232, kept by CODE-1). I did NOT file this: the shipped sentence is
equally loose today ("runs the whole-pod scrub when the recycle disposition is set"), so the
proposal narrows a pre-existing looseness rather than creating one; the case is unreachable from
the gateway (every sender passes a non-empty `result.SessionID`); and the spec lane is locked.
A later agent tempted by it should weigh those three before spending a round. — EVIDENCE:
proposals/.../spec-changes.md:237 vs non-spec-changes.md:488-491

UNVERIFIED: `noteRuntimeStarted`'s replaced-entry refusal does not discriminate two UNTOKENED
entries: if a start's claim reported `attempt == ""` and a reclaim then replaced the entry with
another untokened one, `st.bindAttempt != attempt` is false and the confirmation records. The
proposal names the untokened-entry class as the fail-closed arm and counts it
(`lenny_slot_shutdown_untokened_entry_total`, non-spec-changes.md:2068-2088), and I could not
construct a gateway path that produces it (both bind entry points stamp at `PrepareWorkspace`
before any start RPC), so I did not file it. Whoever re-opens it should start from whether a
`StartSession` or `ConfigureWorkspace` can be the FIRST request of an attempt on a pod.
— EVIDENCE: non-spec-changes.md:646-655 (the predicate), :208-212 (the untokened producers)

USEFUL [the caller directive's "THE MECHANISM IS SETTLED" block]: it stopped me re-deriving the
bind_attempt/unconditional_teardown pairing from scratch, which is where the two leads above
would otherwise have turned into findings.

### [non-spec.5.review-performance.1]

FACT: This proposal adds exactly one net-new never-pruned in-memory structure to the adapter:
`Server.slotGuards map[string]chan struct{}`. The shipped registry `s.slots` IS pruned
(`delete(s.slots, sessionID)` at pkg/adapter/slotsession.go:179), and `Server.reclaiming` is
deleted by `reclaimSlotLocked`'s `release` on a completed cleanup. `slotGuards` is deliberately
never deleted (non-spec-changes.md:1842-1844) and the deliberate choice is correct — deleting
an entry destroys mutual exclusion for a holder. EVIDENCE: pkg/adapter/slotsession.go:174-188;
non-spec-changes.md:1842-1850.

FACT: `recycle.maxSessionsPerPod` exists only when `recycle.enabled: true`
(spec/05_runtime-registry-and-pool-model.md:160 "required when enabled", :488, :511), and the
retirement policy that reads it is headed "Pod retirement policy (recycling pools)"
(spec/05:486). `maxConcurrentSessions > 1` with `recycle.enabled: false` is a spec-valid pool
(spec/05:435, :513, :519), and such a pod serves more than one session over its lifetime
(spec/05:513) while draining only when its claim is deleted (spec/06_warm-pod-model.md:80).
So "recycle.maxSessionsPerPod retires the pod at that count" is not a bound on that pool.
FILED as the one finding of this pass.

FACT (checked, NOT a finding): the control-plane write-rate math is clean. `accountSlotFailure`'s
only apiserver write is the `Unhealthy → DrainSandbox` tail, gated on the threshold, so the two
new callers (the `BindReservedSlot` branch at pkg/gateway/sessionserver/start.go:2596-2605 and
`resumeOnPod`'s Resume-failure branch at :4041) add writes bounded by pod retirement rate rather
than per session. The reserved branch does NOT pass through `bindSlotWithRetry`
(start.go:2596 vs :2608), so there is no double accounting. The compensating `Shutdown` is one
adapter RPC per FAILED bind on the connection the stage already holds, bounded by
`slotCleanupBudget = max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)s`. CODE-8 REDUCES
apiserver writes on the refusal arm (no `failPhase`, no claim delete, no drain). The two new
counters carry `pool`/`k8s_pod_name`, which spec/16_observability.md:297 already admits as an
attribute (the "Used on" cell is descriptive, not an allowlist — do not file on it).
EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2611; non-spec-changes.md:1264-1305, :2085-2092.

FACT (checked, NOT a finding): every failure-mode trace my lens owns is already recorded as an
accepted failure mode, so do not re-file them. Gateway crash between abandonment and reclaim:
spec-changes.md:182-193, with a tier-8 case at non-spec-changes.md:3661-3668. Unanswered reclaim
crossing Redis+Postgres: tier-4 `TestRecyclePathUnansweredReclaimLeaksTheSlot_spec_5_2`,
non-spec-changes.md:3515-3527. §10.1.4 pass guard-acquisition starvation: non-spec-changes.md:3837-3845.
Guard held across a network-bound `Resume` extraction outliving the cleanup budget: :3828-3836.
No value the fence relies on is store-backed — the token is minted in-process per attempt — so
§12.4's durable-fallback question does not bite.

WATCHOUT: the reclaim hold is keyed on the SLOT identifier and `SlotID == SessionID`
(pkg/adapter/slot.go:105-125, spec/05:515 `/workspace/slots/{sessionId}/`). A held identifier
therefore refuses only retries of that same session, never a co-tenant's bind, so it is not a
pod-capacity bottleneck. An earlier reading that a held identifier shrinks the pod's usable slot
count is wrong. EVIDENCE: pkg/adapter/slot.go:105.

OPEN: what retires a continuously occupied `maxConcurrentSessions > 1`, `recycle.enabled: false`
pod? Neither `maxSessionsPerPod` nor `maxPodUptimeSeconds` is configured on a non-recycling pool
(spec/05:486-489 are the recycling-pool policy), and spec/06:80 drains only on claim deletion.
This is pre-existing spec looseness, not this proposal's to close, but it is what makes the
`slotGuards` bound claim false.

### [non-spec.5.review-reliability.1]

VERDICT: EMPTY. Nothing met the bar under the reliability and fault-tolerance lens.

FACT: `SessionUsageMeter.Usage` ignores its context entirely — `func (m *SessionUsageMeter) Usage(_ context.Context, sessionID string)` — and `WireDirectModeUsage` is the only production writer of `Server.Usage`. EVIDENCE: pkg/adapter/usage.go:144, pkg/adapter/usage.go:246-248. This is what saves the edge-case bullet "A member parked under its own guard can cost the §10.1.4 pass its whole guard-acquisition deadline" (non-spec-changes.md:3837-3844). Its stated reason, "each member closes on its own live ten-second context, so no final usage report is lost", does not itself carry the conclusion: CODE-6 replaces only the close at pkg/adapter/holdstate.go:249 and the tree removal at :254, while `s.emitFinalUsage(ctx, m.sessionID)` at :237 keeps the PASS context, which is exactly the context the park exhausted. The report survives anyway because the meter never reads the context. Conclusion true, stated rationale incomplete — below the bar, but do not re-derive it from scratch.

FACT: the shipped §10.1.4 pass shares ONE ten-second close context across every member, and its comment says so and gives the bound as the rationale: "One close context is shared by every member, which keeps the bound the single-session timeout had". EVIDENCE: pkg/adapter/holdstate.go:198-204. CODE-6 mints a per-member close context inside `terminateHeldSession` instead (non-spec-changes.md:1450-1456), so that comment becomes stale. It is a Go comment, not a spec/docs/schema/chart surface, and prior rounds in this loop refuted the same class of comment residue twice (the `holdstate.go` files-touched bullet, CODE-10's rationale comment), so it is not filed.

FACT: the adapter's only other `codes.Aborted` producers are the checkpoint op lock (`errOpBusy`/`errOpCoalesced`), reached from `Server.Checkpoint` alone. EVIDENCE: pkg/adapter/checkpoint.go:115, pkg/adapter/oplock.go:34-40. `Resume` takes no op lock (grep for oplock in pkg/adapter/resume.go returns nothing), so CODE-5's new `codes.Aborted` arm on `isTransientPodClaimError` (pkg/gateway/sessionserver/start.go:3648-3681) cannot pick up a pre-existing permanent condition. This was the main way the `Aborted`-widening could have been wrong; it is not.

FACT: `restoreChunks` returns on an empty chunk set before touching `checkpointRootsForSession`, and its fetch goroutine cannot leak: `ExtractTree` closing the pipe makes the in-flight `io.Copy(pw, rc)` return `ErrClosedPipe` and the goroutine exits. EVIDENCE: pkg/adapter/resume.go:169-172, :188-204. `GetChunk` does build each fetch on the handler context. EVIDENCE: pkg/adapter/checkpointtransport.go:99-101. Both claims CODE-4's `Binder.Resume` paragraph and the guard-wait edge case rest on (non-spec-changes.md:1209-1214, :3822-3836) hold.

WATCHOUT: the reclaim hold's failure arm (a cleanup that did not complete holds the slot identifier for the life of the pod) is reachable from an ordinary expired-context `Shutdown`, because `lockSlotGuard` returning `guarded=false` does not abort the handler: it proceeds, runs `Runtime.Close` on a context derived from the already-dead `ctx`, gets a non-nil `closeErr`, and never takes `release()`. EVIDENCE: the staged handler at non-spec-changes.md:352-356 and :406-418, with `completed = closeErr == nil && treeErr == nil` at :443. This is NOT a finding: §5.2's disposition table (locked spec staging) is what keys the hold on cleanup completion, the residue class is recorded at non-spec-changes.md:3799-3816 and :3822-3836, and a session refused on that pod re-binds elsewhere. A future round that wants to file it must show the applied spec or the staged code is wrong on its own terms, not that a different design would recover faster; "merely slower to recover" is explicitly out of bounds for this lens.

WATCHOUT: the diff since the round-3 snapshot is ONE hunk, the whole-pod-scrub scoping in CODE-1 plus its two new precondition test rows (non-spec-changes.md:299-314, :583-590, :3054-3061, :3158-3163). Before spending effort on it: the staged §4.1 sentence already scopes the scrub to "a request that passed the teardown-pairing rule ... whatever outcome that request answers" (spec-changes.md:237), so "a refused request performs nothing, the scrub included" is consistent with the spec lane, and the refusal is unreachable from a conforming gateway because `Client.Shutdown` and `Client.ShutdownRecycle` set `unconditional_teardown` themselves (non-spec-changes.md:939-947). The fail-closed direction is right; there is no unscrubbed-pod hazard to file.

USEFUL [the orchestrator's TREE FACTS block]: the four facts about `sdkwarm.go`, `st.started` before `Runtime.Start`, the whole-pod scrub working from on-disk slot directories, and `deregisterSlotLocked` stopping the §4.9 timers each closed a line of inquiry before it cost a read. `deregisterSlotLocked`'s timer cancellation is at pkg/adapter/slotsession.go:174-181 and is unconditional, as claimed.

### [non-spec.5.review-security.1]

VERDICT: EMPTY. No security finding met the bar.

FACT: The refused-`Shutdown` path is FAIL-CLOSED on both recycle senders, and this is the fact
that decides whether rule 10's "performs nothing, the scrub included" (spec-changes.md:1061,
non-spec-changes.md:305-307, :3054-3061) is a regression. Both gateway senders arm the §5.2
missing-report timeout BEFORE the recycle `Shutdown`, so a refusal (or any non-answer) retires
the pod rather than handing it on unscrubbed.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1978-1987 ("Armed before the
best-effort Shutdown so a Shutdown that blocks does not delay the timer",
`b.RecycleBoundary.OnRecycling`) then :1994 `b.shutdownAdapter(ctx, result, true)`;
pkg/gateway/podlifecycle/podsession/slotbinder.go:554-558 (`RecycleBoundary: b.RecycleBoundary`
on the `SlotClaimer`) then :563-578 ("Best-effort: a failure here leaves the armed
missing-report timeout to retire the pod").

FACT: A `Shutdown` answering SUPERSEDED while carrying a recycle disposition is UNREACHABLE in
this tree, so `answerShutdown` running the scrub on that arm scrubs no pod holding a live
session. Both recycle senders go through `Client.ShutdownRecycle`, which sets
`unconditional_teardown` and no token (non-spec-changes.md:928-942), and `unconditional` short-
circuits to RECLAIMED/ABSENT in `shutdownReclaimOutcome` (non-spec-changes.md:245-248). The one
token-carrying caller is `ShutdownReclaim`, which carries no recycle.
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:814 is the only non-test in-tree
`adapterv1.ShutdownRequest{` construction (`grep -rn "ShutdownRequest{" --include=*.go pkg/ cmd/
sdks/` returns it plus the generated `pkg/proto/adapter/v1/lenny-adapter.pb.go:5652`).

FACT: The tree-removal error is ALREADY discarded on the running arm today
(`_ = removeSlotTree(st)` at pkg/adapter/session.go:272, response
`ExitedCleanly: closeErr == nil` at :291), so CODE-1's decision to key `exited_cleanly` on
`closeErr == nil && (live || treeErr == nil)` is strictly stricter than shipped on the
pre-`running` arm and identical on the `running` arm. Anyone tempted to file "a running slot
whose tree removal failed reports a clean exit" as a residual-credential-material regression
should stop here: it is the shipped behaviour, the proposal adds a `slog.Warn`, and the residue
is caught by the occupancy-zero whole-pod scrub failing on a non-empty /run/lenny/slots.

FACT: CODE-8's skip of `failPhase` on a typed slot-bind refusal leaks NO §4.9 lease. In
`Binder.Prepare`, `assignCredentials` is the last stage that can be refused, `leaseAssigned` is
still false at the `:949` call site, and `failPhase` gates `releaseCredentials` on
`leaseAssigned`; the deliverable adds `b.releaseAttemptCredentials(minted)` unconditionally on
that arm. In `Binder.Launch` the only reachable refusal is `SLOT_BIND_ALREADY_STARTED`, where
`failPhase`'s release would strip the WINNER's leases, because the walk is session-keyed.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082 (`if leaseAssigned {
b.releaseCredentials(sessionID) }`, `b.drain` outside it);
pkg/gateway/credentials/credassign/credassign.go:400-408 (`ReleaseSession` walks
`LeasesBySession([]string{sessionID})`); pkg/api/v1/session/session.go:289
(`EndpointStart: {baseStates: []State{StateReady}}`).

FACT: Every security-load-bearing citation I sampled in the non-spec staging is accurate:
`slothealth.go:121-125` / `:127-134` (leak count persistent, not pruned),
`scrubreporter_seams.go:184-197` (`RecordLeak` → `Unhealthy` → `StampDrainRequest`),
`scrubreport_server.go:451-481` (`IncrementSessionsServed` before the `leaked` branch, no
per-session dedup), `credassign.go:400-408`, `session.go:289`, `binder.go:1072-1082`.

FACT: No §10.3 / §13.1 / §13.2 surface is touched. Nothing in the staging adds an apiserver
path or an egress peer to the agent pod: the token rides the existing adapter gRPC, and
`grep -rn "bind_attempt\|unconditional" charts/` and `grep -rn "ShutdownRequest" sdks/` return
nothing relevant.

WATCHOUT: the security-lens "bound sourced from a pod self-report" check does NOT fire on
`sessions_served` here. The adapter's `ReportSessionScrub` has always been the source, and this
proposal only NARROWS what it reports (from `bound` to `live`/runtime-held), which the staged
§5.2 text licenses (spec-changes.md:574) and which removes an over-count for sessions the pod
never ran. Reading the narrowing as a relaxation of the `maxSessionsPerPod` bound is the wrong
reading: the sessions it stops counting are ones that never executed tenant code.

UNVERIFIED: whether the new adapter counter `lenny_slot_shutdown_untokened_entry_total` needs an
alert rule in `pkg/alerting/rules` for the degraded state it names. I judged not (no established
control is removed, and N4's metric half is explicitly deferred with an `ABSENT` claim row), but
an observability lens, not the security lens, owns that call.

### [non-spec.5.review-single-source.1]

VERDICT: ONE finding.

FACT: the staged §4.7 `Shutdown` row does NOT state the two-field precondition's
`INVALID_ARGUMENT` answer. It states carriage only ("Every request states which teardown it
asks for, by carrying either a non-empty `bind_attempt` ... or `unconditional_teardown`") and
then explicitly defers: "[Section 4.7.1] states the named rules that decide both ... this row
restates neither." The SPEC-1 commentary says the same in its own words. The only staged
statement of the refusal answer is rule 10. — EVIDENCE: spec-changes.md:277 (the replacement
row), :283-284 ("The row names the two fields and points at the named rules rather than
restating them"), :1061 (rule 10); `grep -n INVALID_ARGUMENT` over the four non-log files
returns only :290 (commentary), :1047 (rule 1), :1061 (rule 10), non-spec :512,
summary :1112/:1122, checklist :19.

MISTAKE: summary.md:1122 and implementation-checklist.md:19 both describe that row as stating
"the two-field precondition and its `INVALID_ARGUMENT` answer". Two sites, one false
description, and they are near-verbatim of each other, which is why neither caught the other.
The remedy is a REDUCTION (drop the `INVALID_ARGUMENT` clause from both descriptions), not a
spec edit: adding the answer to the row would give rule 10 a second normative home, which the
commentary at :283-284 says the row exists to avoid.

WATCHOUT: summary.md:1122 and implementation-checklist.md:19 are near-duplicate prose
descriptions of SPEC-1 (my mechanical repeat sweep caught two whole sentences shared verbatim
between them, at summary:1122/checklist:19 and summary:1142/checklist:29). A fixer correcting
one MUST sweep the other. The same pairing holds for DOCS-2. — EVIDENCE: summary.md:1122 vs
implementation-checklist.md:19.

UNVERIFIED: whether CODE-1's "Doc-comment work on `Shutdown`" bullet list is a second stating
site for rules already written out in the staged handler-body comment. The clearest pair is the
whole-pod recycle scrub: non-spec-changes.md:300-313 (body comment) and :580-586 (bullet) each
state the rule AND the two-gateway-sender rationale in full, and round 4's fix had to edit both
in lockstep. The same overlap exists for the slot-guard placement (:264-283 vs :543-548). I did
NOT file it: both sites are implementation RATIONALE for code, the rule's normative home is the
staged §4.1 sentence plus rule 10, and the lens excludes "a rule and its rationale". A future
round wanting a reduction should note the format already exists at :549 ("in the terms the code
comment above carries"). Whoever files this must argue past the rationale exclusion first.

FACT: the mechanical sentence-repeat sweep over the four non-log files (split on `(?<=[.;])\s+`,
whitespace-normalised, sentences >90 chars) returns NINE repeats and eight are the known benign
classes: verbatim-anchor/replacement pairs inside spec-changes.md, spec-row/docs-mirror pairs
(DOCS-2's licensed restatement), summary-index/checklist-step pairs, and the two grep-pattern
commands that define a sweep set stated once in the deliverable and once in the files-touched
index. Do not re-file any of them. — EVIDENCE: spec-changes.md:271/:278, :762/:768,
:1162/non-spec:2800, :1168/:1174; non-spec:82/summary:132; non-spec:3186/:4109;
summary:1122/checklist:19; summary:1142/checklist:29.

USEFUL [standing context, review-log.md:341]: "The staged handler has TWO returns that bypass
`answerShutdown`" saved me from filing the code comment's one-return sentence at :310-312 as a
false enumeration; it is a true non-exhaustive statement, and the enumeration's home is the
prose at non-spec-changes.md:509-512.

### [non-spec.5.review-test-coverage.1]

DECISION: filed ONE finding only — CONF-1's rule 6 case asserts status + ErrorCode but not the
`CATEGORY_PERMANENT` the staged rule 6 fixes, while the sibling rule 5 case (non-spec-changes.md:2236-2238)
does assert its category — BECAUSE `Error.Category`'s proto3 default is `CATEGORY_UNSPECIFIED = 0`
(schemas/lenny-adapter.proto:544), so an omitted field is silently non-conforming and passes every listed
case. ALTERNATIVES rejected as below the bar: (a) no listed assertion of `Retryable: true` on the
superseded detail — nothing in the tree reads `adapterv1.Error.Retryable` (grep over pkg/ cmd/ tests/), so
its absence breaks nothing; (b) no test for the `metricsbackfill.go` hook wiring — the shipped
`SlotFailure` wiring beside it has none either, and the proposal names the gap itself
(non-spec-changes.md:2065-2070); (c) no assertion on the `slot_tree_removal_failed` and
`runtime_close_failed` warn lines — log-only; (d) the widened `cancelPodMCPIfRuntimeIdle` reach under
`removed` — double-guarded by shipped code (`pkg/adapter/slotsession.go:238-260`) and already reached from
`releaseSessionSlot`.

FACT: every fixture the Testing section names exists and is reachable from package `adapter`:
`slotPod`/`slotTreeProbe`/`probeRuntime` (pkg/adapter/slotsession_test.go:61,:77,:33),
`startRuntimeOps` (pkg/adapter/files_updated_test.go, package `adapter`), `recordingSessionScrubReporter`
(pkg/adapter/sessionscrub_emit_test.go:20), `assignOne` (pkg/adapter/credexpiry_test.go:105),
`fakeExpiryClock` (pkg/adapter/holdstate_test.go:52). `sdkwarm_test.go` is package `adapter_test`, which is
why the SDK-warm case is placed there; every other named file is package `adapter`. Do not re-derive.

FACT: both mechanical sweeps' greps are scoped `pkg/ tests/` and that scope is complete. No
`adapterv1.ShutdownRequest{` and no bind-sequence request literal exists under `cmd/` or `sdks/`; the
`ShutdownRequest{` set is 26 files, matching the review log's count. EVIDENCE: `grep -rln
"ShutdownRequest{" --include=*_test.go .` returns only pkg/ and tests/ paths.

FACT: `superseded` + a `RecycleScrub` disposition is unreachable from the gateway, because both
`Client.ShutdownRecycle` senders set `unconditional_teardown`, which routes to rule 12 or rule 11. So the
Testing section's claim (non-spec-changes.md:3161-3163) that the absent arm, the removing arm and the
precondition rows pin clause three "across the outcomes a `Shutdown` answers" leaves the superseded
outcome's scrub unpinned, and that is correct rather than a gap: a test for it would pin an unproducible
combination. A later round should not file it.

USEFUL [round-4 fix on the precondition rows]: the new `RecycleScrub` assertion on the two
`InvalidArgument` rows (non-spec-changes.md:3057-3062) is supported by both carriers it cites —
spec-changes.md:237 ("runs the whole-pod scrub ... on a request that passed the teardown-pairing rule")
and rule 10 at spec-changes.md:1061 ("performs neither teardown, removes no entry, and changes nothing").
The earlier contradiction with the staged handler comment is closed: the comment now reads "outside the
arms that remove nothing" and "A request the two-field precondition refuses returns above this helper and
performs nothing, the scrub included" (non-spec-changes.md:299-311).

### [non-spec.6.review-single-source.1]

VERDICT: EMPTY. Round-6 delta was three hunks across two files (plus one mirrored line);
nothing under the single-source lens.

FACT: the round-5→6 diff is tiny. (1) implementation-checklist.md:19 (S2/SPEC-1) and
summary.md:1122 (deliverable index SPEC-1) both replace "states the two-field precondition and
its `INVALID_ARGUMENT` answer" with "names the two fields a request carries, one of
`bind_attempt` and `unconditional_teardown`". Verified against the staged §4.7 `Shutdown` row:
it says "Every request states which teardown it asks for, by carrying either a non-empty
`bind_attempt` ... or `unconditional_teardown`" and states no `INVALID_ARGUMENT`, so the new
wording is now true and the row is no longer a second home for rule 10. — EVIDENCE:
spec-changes.md:278; rule 10 at spec-changes.md:1061.

FACT: (2) non-spec-changes.md:2241-2242 (CONF-1 rule 6 case) gained "and the category", now
word-for-word parallel with the rule 5 case at :2236-2238. Both use the citing form "asserts the
status, the `ErrorCode` and the category that rule states", which under this lens is the
non-copying form: rule 6's home (spec-changes.md:1052) is the only site spelling
`FAILED_PRECONDITION`/`CATEGORY_PERMANENT`. Checked every other `CATEGORY_*` site in the
proposal: only spec-changes.md:1051-1052 (the two rule homes) and summary.md:474 (open decision
29, which is rationale for a human decision, not a normative statement). No second home exists.

FACT (negative, so nobody re-chases it): grepped `two-field precondition` and `INVALID_ARGUMENT`
across summary, checklist and non-spec-changes after the rewrite. Every surviving site is either
the CODE-1 deliverable body (the implementation home), a DOCS-2 docs-page restatement (the one
licensed reader-facing restatement), or a clause-level mention. No site now claims the §4.7
`Shutdown` row states the `INVALID_ARGUMENT` answer. — EVIDENCE: non-spec-changes.md:191, :215,
:3054 (CODE-1/test-table homes); checklist:29 and summary.md:1142 (DOCS-2 page).

USEFUL [prune.3.fix.1 / the "STATE EACH RULE ONCE" citing form]: the "asserts X that rule states"
convention across all fifteen CONF-1 cases is what made this round's check cheap — a rule's
assertion set lives only in its numbered rule, and the case list cites. A future fixer tempted to
spell a code or category literally into a CONF-1 case is creating the copy this lens exists to
catch.

### [non-spec.6.review-test-coverage.1]
DECISION: returned EMPTY for the test-coverage lens on round 6 — BECAUSE the round-5→6 diff is three hunks only (CONF-1 rule 6 gains "the category" at non-spec-changes.md:2240-2242; checklist S2 and summary SPEC-1 drop "states the two-field precondition and its `INVALID_ARGUMENT` answer" in favour of "names the two fields a request carries"), and both are verified sound under this lens — ALTERNATIVES: filing the rule-6 category as unobservable (rejected, see FACT below); filing the S2/SPEC-1 wording change as dropping a pinned behavior (rejected: the two-field `INVALID_ARGUMENT` precondition is stated by staged §4.7.1 rule 10 at spec-changes.md:1061 and pinned by CONF-1's rule 10 case at non-spec-changes.md:2264-2266 at both tiers, so no coverage moved when the §4.7 row stopped restating it).
FACT: the rule-5/rule-6 error category IS assertable at tier 3 over the wire, not just in-process: the adapter error envelope carries `Category category = 2` with `CATEGORY_PERMANENT = 2` — EVIDENCE: schemas/lenny-adapter.proto:533,:547-548; staged rule 6 states it at spec-changes.md:1052 ("answered on `FAILED_PRECONDITION` and carried as `CATEGORY_PERMANENT`"), rule 5 at :1051. So the widened citation form in the CONF-1 rule 5 and rule 6 cases is observable at both listed tiers and needs no extra deliverable.
FACT: the CONF-1 tier-3 and tier-10 case lists name rules by name only and carry no assertion set, so an assertion-set change to a case entry reaches exactly one site. Confirmed again this round by grepping "the category" across the proposal: only non-spec-changes.md:2237 and :2242 carry it. No stale sibling was left by the round-5 edit.

### [non-spec.7.fix.1]

DECISION: closed the follow-up finding that the `tests/spec-map.json` files-touched bullet records additions only, by appending one clause naming S4's removal of section `12.1`'s single row for `TestPerReleaseSessionCountDrainAgrees_F5231` and the `tests/spec-map-exceptions.yaml` non-normative entry that covers the now-empty section — BECAUSE this round's checklist edit gave that file a removal and the files-touched bullet is the proposal's per-file statement of what application does — ALTERNATIVES: reverting the checklist sentence (rejected, the removal is required for the harness not to credit §12.1 to a case that reads nothing there); restating the exceptions-file rationale a second time (rejected, the checklist step is its home and the bullet cites it). EVIDENCE: non-spec-changes.md:3930-3936; implementation-checklist.md:23.

FACT: the row the removal targets is real and is section `12.1`'s only row, and the section carries a standing non-normative exception, so an empty test list leaves no gate red — EVIDENCE: tests/spec-map.json, section `12.1`, `tests` holding the single entry `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go::TestPerReleaseSessionCountDrainAgrees_F5231`; tests/spec-map-exceptions.yaml, `section: "12.1"`, `reason: non-normative`.

DECISION: dropped CODE-12's count of wrapped comment carriers, replacing "Two carriers wrap their sentence across a comment break" with "Some carriers" — BECAUSE the staged command's joined form returns more wrapped blocks than two, and the count went stale within this round's own edit when the regex gained `(back )?` — ALTERNATIVES: restating the number (rejected, `doc-style.md` and `doc-content.md` both say to drop a count the adjacent text already carries, and the set is defined by the command rather than by a tally). EVIDENCE: non-spec-changes.md:2214.

WATCHOUT: nothing downstream of CODE-12 reads a carrier count; the deliverable's done-condition is the command's output under the carrier definition and the non-carrier criterion. Do not reintroduce a number when the alternations change.

### [non-spec.7.fix-G1.1]

DECISION: closed the orphaned §12 credits finding with ONE appended passage on checklist S4 (implementation-checklist.md, the SPEC-3 step) — BECAUSE S4 is the step that performs the deletion, so the deletion's full consequence belongs in that one home, and spec-changes.md:609 already delegates to S4 ("Staged by checklist S4's tier-11 sweep, deletion") and is locked this run — ALTERNATIVES: widening the SPEC-3 carrier row (locked, and a `// spec:` credit carries no rule so the row is true as written); adding the file to non-spec-changes.md's `Tests:` enumeration and a removal line to the spec-map bullet (creates a second home for one step's disposition, and contradicts the treatment of the other S4-swept tier-11 file `tests/tier11_docs/spec_28_register_writers_test.go`, which appears nowhere in non-spec-changes.md); re-keying the deleted assertion onto §12.6's citation text (would restate a rule whose one home SPEC-3 makes §5.2's `**Session count limit:**` bullet).

FACT: `TestPerReleaseSessionCountDrainAgrees_F5231` is credited in exactly three `tests/spec-map.json` sections, 5.2, 6.2 and 12.1, and it is section 12.1's ONLY row — EVIDENCE: tests/spec-map.json:2752-2761 (the `12.1` object), :1070, :1325.

FACT: an empty `tests[]` under `12.1` trips no gate, because `tests/spec-map-exceptions.yaml:85-87` already carries section 12.1 as `reason: non-normative` — EVIDENCE: tests/spec-map-exceptions.yaml:85-87.

FACT: the §12 residue in `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go` is four sites, and all four survive the block deletion at :169-177 — EVIDENCE: :20-21 (header item 2's `§12` landing site), :47 (`12.1 (sessions_served schema)` in the file-level `// spec:`), :135 (`// spec: 5.2, 6.2, 12.1`), :136 (`// diagnosis: §5.2, §6.2, §12`).

WATCHOUT: CODE-10's "No `// spec:` annotation loses a section number" invariant looks like it forbids dropping `12.1` here. It does not: CODE-10 explicitly excludes the two tier-11 files under S4's sweep. Do not soften CODE-10 to accommodate this — EVIDENCE: the CODE-10 deliverable in the non-spec changes file, its scope exclusion clause.

### [non-spec.7.fix-G2.1]

DECISION: closed the `Binder.drain` §4.6.1 comment finding with a new PREDICATE-DEFINED deliverable CODE-12, modelled on the CODE-10 form commit deb2b9177 settled, rather than the finding's own suggested fix — BECAUSE the carrier set is not reliably enumerable by hand: the finding named one confirmed site and five candidates, and the joined-comment grep returns four more that nobody named (`pkg/gateway/podlifecycle/podsession/one_session_only_test.go:70`, `binder_test.go:393`, `pkg/podlifecycle/agentsandbox_test.go:185`, `pkg/podregistry/crd_test.go:221` — this last one does not appear in the grep output I ran, see UNVERIFIED below) — ALTERNATIVES: appending the drain comment to CODE-4/CODE-8's closed `binder.go` edit list and to the files-touched bullet (rejected: it reopens the file-by-file enumeration form the prune removed and leaves `claimer.go:310` and the rest stale); extending CODE-3 (rejected: disjoint subject and file set, and it would mix an enumerated and a predicate form in one deliverable); enumerating CODE-12's sites (rejected: two independent passes already mis-enumerated this set); rewriting each carrier to state the new keying (rejected: that gives §4.6.1 a second full statement in a handful of Go comments).

WATCHOUT: line-oriented greps miss two of these carriers outright, because their sentence wraps mid-clause across the comment break — `pkg/podregistry/crd.go:249` (`returns the pod to` / `idle`) and `pkg/adapter/resume.go:19` (passive form). CODE-12's command joins each comment block before matching, the way the §28.1 N3 matcher joins two consecutive comment lines. A future round that re-derives the carrier set with a plain `grep -rn` will under-count it — EVIDENCE: pkg/podregistry/crd.go:249, pkg/adapter/resume.go:19

FACT: five tree comments state the `reserved → idle` hold-expiry edge and are NOT carriers, because SPEC-4 keeps that bullet and re-keys it onto the `reserved` phase — EVIDENCE: pkg/controller/warmpool/gc.go:370, pkg/controller/warmpool/gc.go:422, pkg/controller/warmpool/occupancy.go:130, pkg/gateway/session/recycle/scrubreporter_seams.go:924, tests/tier8_chaos/reserved_hold_holder_crash_test.go:36

FACT: `Binder.drain` is a one-line wrapper over `podclaim.DeleteClaim`, and BOTH carry the same two-clause recycle-setting-keyed restatement under the same `// spec: §4.6.1 (occupancy projection on claim DELETE)` annotation. Fixing one without the other was the trap here — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1187-1201, pkg/gateway/podlifecycle/podclaim/claimer.go:310-324

FACT: `pkg/controller/warmpool/occupancy.go`'s `ProjectOccupancyPhase` already implements the SPEC-4 keying, so every CODE-12 site is comment prose and no branch, value or assertion moves anywhere. That is why the step's tiers are 0 and 11 only — EVIDENCE: pkg/controller/warmpool/occupancy.go:79-152

CORRECTS [the G2 design's carrier rationale]: the design asserted its command returns `pkg/podregistry/crd_test.go:221`. Run as written it does not, because that comment and its tier-2 sibling say "projects the pod BACK to idle" and the alternation had no optional `back`. Both are carriers. I widened the staged command's two return alternations to `(back )?` before landing it, which brings in `pkg/podregistry/crd_test.go:221` and `tests/tier2_component/stores/crdpodregistry_claim_test.go:180` and changes no other hit. The staged command is the deliverable's definition, so use the command in the proposal rather than the design's quotation of it — EVIDENCE: pkg/podregistry/crd_test.go:220-224, tests/tier2_component/stores/crdpodregistry_claim_test.go:179-184

WATCHOUT: this carrier family is phrased several ways ("returns the pod to idle", "returned to idle", "projects the pod back to idle"), and each new phrasing is a silent miss rather than a visible failure. Any future edit to CODE-12's command should be checked by reading the hits it drops as well as the ones it adds.

DEFERRED [pkg/adapter/session.go, pkg/adapter/resume.go, pkg/podregistry/crd.go, pkg/podlifecycle/agentsandbox.go, pkg/gateway/podlifecycle/podclaim/claimer.go, pkg/gateway/podlifecycle/podsession/binder.go]: each carries a comment asserting that a claim DELETE returns the pod to `idle`, keyed on the pool's recycle setting or unqualified. After SPEC-4 lands, the projection never returns a pod from `claimed` to `idle` on any pool, and a claim deleted while the pod projects `claimed` projects `draining` then `terminated` on a pool of either recycle setting. This loop may not edit the tree; CODE-12 stages the correction and checklist step S26 applies it.

### [non-spec.7.fix-G3.1]

DECISION: closed the DOCS-2 carriage finding and its four parallels by replacing every restatement of the token's carriage set with a count-free citation of §4.7.1's carriage table, and by deleting the clause outright where the site did not need it (the claim-register `note`, whose own `claim` field already states the set) — BECAUSE the carriage rule already has a single locked home (review-log Standing context, "the §4.7.1 'Which fields each request carries' table is the single home") and the remedy for one rule stated in five places is a citation at four of them rather than five synchronised restatements — ALTERNATIVES: narrowing each sentence to "the six requests" (rejected: three of the five sentences name the compensating `Shutdown` in a separate clause, so a count double-counts it, and a count goes stale if the table gains a row); fixing only the DOCS-2 site (rejected: leaves the summary, the Design section and the shipped Go doc comment asserting what DOCS-2 withdrew); amending the carriage table to cover `StartSession`/`ConfigureWorkspace` (rejected: spec lane locked, and the spec is right).

FACT: the DOCS-2 paragraph is a reader-facing docs page and carries no `§` citation, so its replacement cites "the linked contract's carriage table" through the [Role and Gateway RPC Contract] link it already carries, while the four in-proposal sites cite "§4.7.1's carriage table" directly. Two idioms for one rule, deliberately, under doc-content.md's bar on spec section numbers in published prose. — EVIDENCE: non-spec-changes.md, DOCS-2's `**Bind attempt token.**` paragraph.

FACT: after this edit `grep -n "create or resolve\|creates or resolves"` over non-spec-changes.md, summary.md, implementation-checklist.md and problem-statement.md returns nothing. The reclaim hold's deliberately wider predicate survives only in spec-changes.md; the non-spec half was already rewritten to "the seven requests §4.7.1's admission rules govern" by an earlier round, so there was no collateral risk of narrowing the hold in the files this group touched. The standing-context warning against unifying the two predicates still applies to spec-changes.md alone.

WATCHOUT: the phrase "can create or resolve the entry" is TRUE of `StartSession` and `ConfigureWorkspace` (both reach `ensureSlotStateLocked` through `claimSessionSlot`) and FALSE as a statement of which requests carry `bind_attempt`. Any future sentence that reaches for it as shorthand for the carriage set reintroduces this finding. Cite the carriage table. — EVIDENCE: spec-changes.md, the `StartSession` and `ConfigureWorkspace` rows of the carriage table, both "not carried", and the rationale sentence above it.

USEFUL [Standing context, "the §4.7.1 'Which fields each request carries' table is the single home of `bind_attempt`/`mid_session` carriage"]: this line is what made the fix a one-line decision rather than a re-derivation of which requests carry the token.

### [non-spec.7.fix-design-G1.1]
DECISION: the orphaned §12.1 credits are removed inside checklist S4's own tier-11 sweep clause (implementation-checklist.md:23) and nowhere else — BECAUSE S4 is the step that deletes the assertion, the spec-changes.md carrier row already cites S4 as the home ("Staged by checklist S4's tier-11 sweep, deletion") and spec-changes.md is locked this run, and the swept sibling `tests/tier11_docs/spec_28_register_writers_test.go` appears nowhere in non-spec-changes.md, so S4-swept tier-11 files are by precedent absent from the non-spec `Tests:` enumeration — ALTERNATIVES: (a) widen the spec-changes.md:609 carrier row as the finding suggested — barred by lockSpecChanges and unnecessary, because the row's subject is the carrier of the rule and a `// spec:` credit is bookkeeping rather than a restatement; (b) add the file to non-spec-changes.md's `Tests:` list and a removal line to the spec-map bullet — that creates a second home for one step's disposition and breaks the sibling precedent; (c) re-key the §12 assertion onto §12.6's new citation-of-§5.2 text instead of deleting it — that reinstates a second site for the evaluation-point rule whose single home S4 makes §5.2.
FACT: `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:172` is the file's ONLY read of `spec/12_storage-architecture.md` (single `readDocPage` of that file); after S4's deletion the file has no §12 read at all, so both the per-case credit at :135 and the file-level credit `12.1 (sessions_served schema)` at :47 go false. — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:169-177, :47, :135
FACT: `TestPerReleaseSessionCountDrainAgrees_F5231` is credited in `tests/spec-map.json` under exactly three sections, 5.2, 6.2 and 12.1; only the 12.1 row is removed. It is the ONLY entry in 12.1's `tests[]`, and 12.1 already carries a `spec-map-exceptions.yaml` row (`reason: non-normative`), so the array emptying trips neither `validateSpecMapCoverage` nor the tier-0 heading walker. — EVIDENCE: tests/spec-map.json:2752-2761, tests/spec-map-exceptions.yaml:85-87
WATCHOUT: CODE-10's grep sweep carries an invariant "No `// spec:` annotation loses a section number", which reads like a contradiction of this fix. It is not: CODE-10 explicitly excludes the two tier-11 files under S4's sweep. Do not "reconcile" the two by softening CODE-10. — EVIDENCE: non-spec-changes.md CODE-10 deliverable (~2120-2150)

### [non-spec.7.fix-design-G2.1]

DECISION: the §4.6.1 claim-deletion projection comment carriers get ONE NEW PREDICATE-DEFINED deliverable, CODE-12, modelled exactly on the pruned CODE-10 (grep-defined carrier set, no file enumerated anywhere, one reduction rule, SPEC-4's re-keyed §4.6.1 bullets cited as the rule's home) — BECAUSE the carrier set is not reliably enumerable by hand: the finding named 5 candidate sites and a joined-comment grep returns 18, of which at least `pkg/gateway/podlifecycle/podsession/one_session_only_test.go:70`, `binder_test.go:393`, `pkg/podlifecycle/agentsandbox_test.go:185` and `pkg/podregistry/crd_test.go:221` are carriers nobody named, and commit deb2b9177 already retired the file-by-file enumeration form for exactly this failure — ALTERNATIVES: (a) the finding's own suggested fix, adding the binder.go comment to CODE-4/CODE-8's closed edit list and to the files-touched bullet — rejected, it re-opens the retired enumeration form and leaves the high-confidence `claimer.go:310` parallel plus four more stale; (b) a second arm on CODE-3 — rejected, CODE-3 is enumerated and its subject is the §6.2 per-slot fence, a different rule; (c) an enumerated CODE-12 — rejected on the same evidence that the set is mis-enumerated twice already; (d) record as an open decision — rejected, nothing here needs a spec edit.

FACT: the verified carrier-finding command (line-oriented greps MISS `pkg/podregistry/crd.go:249` and `pkg/adapter/resume.go:19`, whose sentences wrap mid-clause; comment blocks must be joined first):
`perl -0777 -ne 'while (/((?:^[ \t]*(?:\/\/|--)[^\n]*\n)+)/gm){$b=$1;$p=$`;$ln=1+($p=~tr/\n//);$j=$b;$j=~s/\n[ \t]*(\/\/|--) ?/ /g;$j=~s/\s+/ /g; print "$ARGV:$ln\n" if $j=~/(returns?|returned|projects?) (the pod|a pod|it) to `?idle|(is|are|was|were) returned to `?idle|projects `?idle|claim (DELETE|deleted) on a `?recycle\.enabled/i}' $(git ls-files '*.go' '*.sql' | grep -E '^(pkg|cmd|tests|migrations)/')`
18 hits. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1187, pkg/gateway/podlifecycle/podclaim/claimer.go:310, pkg/podregistry/crd.go:249, pkg/podlifecycle/agentsandbox.go:214, pkg/adapter/session.go:134, pkg/adapter/resume.go:19

FACT: the non-carrier criterion that filters most of those 18 is the `reserved → idle` hold-expiry edge, which SPEC-4 KEEPS (re-keyed onto the `reserved` phase), so `pkg/controller/warmpool/gc.go:370,:422`, `occupancy.go:130`, `pkg/gateway/session/recycle/scrubreporter_seams.go:924` and `tests/tier8_chaos/reserved_hold_holder_crash_test.go:36` stay true and take no edit. EVIDENCE: spec-changes.md SPEC-4 §4.6.1 block, the replacement `idle` bullet ("deleted while the pod projects `reserved`").

WATCHOUT: the cascade a fixer will miss is `summary.md:1114` (the 0079 impact row), which reads "A file CODE-10 or CODE-11 alone opens is opened for comment prose only and does not count toward this row's file-collision ground". CODE-12 must be added to that sentence or the row's collision ground is wrong. EVIDENCE: summary.md:1114

FACT: CODE-12 needs NO spec-changes.md edit, so the spec lock is not engaged. CODE-10 has a carrier-table row only because SPEC-3 carries a carrier table (spec-changes.md:610); SPEC-4 carries none, and DOCS-1 already owns the docs carrier (`docs/reference/state-machines.md`) and CODE-3 the §6.2 fence comment carriers. EVIDENCE: spec-changes.md:588,:610; non-spec-changes.md:2583; summary.md:1131,:1141

FACT: the tree already implements the SPEC-4 keying (`ProjectOccupancyPhase` never returns `idle` from `claimed`), so every carrier is stale against the CODE as well as against the staged spec, and no behaviour, branch or assertion moves anywhere in CODE-12. EVIDENCE: pkg/controller/warmpool/occupancy.go:113-136; review log Settled, "The occupancy projection reads the pod's CURRENT PHASE".

### [non-spec.7.fix-design-G3.1]

DECISION: the five "create or resolve" token-carriage sites (non-spec-changes.md:16, :896, :2567, :2754 and summary.md:29) are all corrected to CITE the §4.7.1 "Which fields each request carries" table instead of restating a predicate, and none of them enumerates the requests — BECAUSE the Standing context already records that table as the single home of `bind_attempt`/`mid_session` carriage, and the proposal already carries the correct idiom at non-spec-changes.md:56 ("§4.7.1's carriage table fixes for each request"). ALTERNATIVES: (a) narrow each site to "the six requests" — rejected, it plants a count in five places and see the WATCHOUT below; (b) fix DOCS-2 alone as the finding names — rejected, the other four then contradict it inside the same document; (c) edit the spec staging — impossible and unnecessary, the lane is locked and the table is already correct.

WATCHOUT: "the six requests" is NOT a safe narrowing of the sentences at non-spec-changes.md:16, :896 and summary.md:29. The authoritative six are `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `Resume` and `Shutdown`; all three of those sentences already name the compensating `Shutdown` in their own second clause, so writing "six" there double-counts it. The count-free citation of the carriage table is the only wording that is correct at every site — EVIDENCE: spec-changes.md carriage table (rows `PrepareWorkspace` … `Shutdown`), non-spec-changes.md:16, :896, summary.md:28-29.

FACT: "create or resolve" is used for TWO different predicates in this proposal and only one of them is wrong. The reclaim hold's admission predicate (rule 2, §5.2/§15.4) is deliberately wider and must not be touched; the token-carriage predicate is the over-broad one. `StartSession` and `ConfigureWorkspace` create and resolve the entry but carry no token, by §4.7.1's own rationale paragraph — EVIDENCE: spec-changes.md:1022, :1033-1034; review-log.md:131.

FACT: the claim-register row at non-spec-changes.md:2563-2568 already states the correct set in its `claim` field ("the six slot-entry requests") while its `note` field restates the false predicate. The reduction is to delete the carriage clause from the `note` entirely and keep only the adapter-side half the row alone knows.

MISTAKE: an earlier archive round DEFERRED exactly this cluster (design prose, `newBindAttempt` comment, claim-register note, summary) rather than landing it, so the same four sites came back as this round's outward pass. Landing them together now closes the loop.

### [non-spec.7.review-applicability.1]

VERDICT: EMPTY. No finding met the bar on the applicability/sequencing lens in round 7.

FACT: the checklist's dependency graph is fully backward-closed and every staged deliverable is
named by at least one step. Coverage map I built and re-verified this round: S1=SPEC-5, S2=SPEC-1,
S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=SPEC-6, S7=DOCS-1/2/3, S8=CODE-3, S9=SCHEMA-1, S10=CODE-9,
S11=CODE-7, S12=CODE-8, S13=CODE-4(mint), S14/S15=CODE-6, S16/S17=CODE-1, S18=CODE-2,
S19=CODE-4(compensation), S20=CODE-5, S21=CODE-6+CODE-1, S22=CONF-1, S23=DOCS-4, S24=CODE-10,
S25=CODE-11. Every box unchecked. Spec steps S1-S6 form the leading block; no spec step follows a
non-spec one. EVIDENCE: implementation-checklist.md:17-66

MISTAKE (mine, caught before filing): I nearly filed that
`tests/tier7a_load_local/slot_bind_attempt_race_test.go` (non-spec-changes.md:3530) is a new
`slot*_test.go` file with no `slotAddressCaseFiles` row, where three sibling new files
(slot_reclaim_hold_race_test.go :3584, slot_credential_reclaim_fence_test.go :3672,
slot_compensation_metric_reference_test.go :3752) each state one explicitly. It is covered by a
BLANKET rule in the files-touched section: "Every new file whose name matches that pattern enters
the inventory in its sorted position, in the step that creates it".
EVIDENCE: non-spec-changes.md:3938-3944. Cost me ~20 minutes. Do not re-file it.

FACT: the two register rules a new test file must satisfy, and where the proposal discharges each.
(1) `tests/spec-map.json` — blanket rule at non-spec-changes.md:2855-2856 plus the per-surface
enumeration at :3877-3937. (2) `slotAddressCaseFiles` in
`tests/tier0_static/spec_map_slot_address_registration_test.go` — derived membership is
`slotSubjectFileRE` = `(slot|one_session_only|sole_session)[^/]*_test\.go$` (:1173), or a body
match on `slotstate.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`
(:1168), over walk roots cmd/migrations/pkg/scripts/sdks/tests (:1191); the enforcing gates are
`TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` (:1108) and
`TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` (:970).
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1108-1230

FACT: the tier-8 case's FILE name, which the Testing section leaves unnamed at
non-spec-changes.md:3661, is stated in the files-touched spec-map list as
`tests/tier8_chaos/compensation_loss_test.go` (:3886). Checking only the Testing section reads as
an unstated property; it is not.
EVIDENCE: non-spec-changes.md:3886

FACT: SCHEMA-1's nine field numbers all check out free against the current proto, message by
message: PrepareWorkspaceRequest 1-3 used + 4 reserved, FinalizeWorkspaceRequest 1-4 used
(`mid_session` is 4) + 5 reserved, RunSetupRequest 1-3 + 4 reserved, AssignCredentialsRequest 1-2
+ 3 reserved, ResumeRequest 1-5 and 7-14 used with 6 and 15 reserved, ShutdownRequest 1-3 + 4
reserved + 5 recycle + 6 coordination_generation, ShutdownResponse 1-2. `ErrorCode` runs to 27, so
28 and 29 are free. EVIDENCE: schemas/lenny-adapter.proto:554-588, :682-696, :706-730

FACT: `assertFieldSet` exists at exactly one site in the tree
(`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:259`), so SCHEMA-1's
claim that the shutdown gate is the only closed field set over the messages it opens holds. The
only non-test constructions of `ShutdownRequest{}` and of the five bind-sequence request literals
are all in `pkg/gateway/runtime/adapterclient/client.go` (:181, :275, :307, :328, :617, :814), and
`cmd/`, `sdks/`, `migrations/` and `scripts/` carry no `_test.go` literal of any of them, so the
two mechanical sweeps' `pkg/ tests/` scope is complete.

FACT: the tier-11 gates the docs edits must not break, checked against the staged text and green.
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` asserts the literal
`"The request is session-scoped: it is addressed by the identifier of the released session and
names no slot."` on BOTH the §4.7 row and the adapter-contract row
(tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33, :67-75); both
staged replacements keep that sentence word for word (spec-changes.md:768,
non-spec-changes.md:2746). `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only
"end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub"
(tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307), all four of which
survive in the staged row, and it reads the docs page ALONE — it does not cross-check §4.7, so S2
cannot leave it red before S7 lands.

FACT: `CH-RUNTIMEOPS`, used in DOCS-2's staged `Shutdown` row (non-spec-changes.md:2728), is a
live register identifier already in the shipped page it edits
(docs/reference/adapter-contract.md:10, :75). Not a naming-law defect.

FACT: `R8`, the deferral id SCHEMA-1's `ABSENT` claim row names, is declared as
`### R8. Reciprocal host-conformance battery` at gateway-runtime-comms-remediation.md:980, exactly
as cited. Rule `S-2` is at :1883-1890.

USEFUL [the refuted list carried in the round-7 prompt]: the three refutations about per-step
`Depends-on` omissions and about S16/S17 both describing the `bound`→`removed` widening saved me
from re-filing two close variants. The S16-carries-tier-9-while-S17-lands-the-widened-gate reading
is the same argument in another costume; it is covered by the S16/S17 refutation.

OPEN: the §4.7.1 rule-set and the `slotAddressCaseFiles`/spec-map registration rules are each
stated once as a blanket rule in the files-touched section and then re-derived in full, with their
own rationale, at three or four per-file sites in the Testing section (non-spec-changes.md:3461-3464,
:3584-3587, :3672, :3749-3754 against the home at :3938-3944). Every copy agrees today. I judged
this below the duplicated-rule bar because each per-file site reads as an application of the rule
rather than a second normative home, but a later reduction pass may want to cut them to citations.

### [non-spec.7.review-citations.1]

VERDICT: EMPTY. No citation finding met the bar.

FACT: the citations lens's real scope this round is the `non-spec-r2` → HEAD diff, not the
`non-spec-r5-prefix` one the prompt names. The lens last returned EMPTY at round 2, so every
line written since is unexamined by it; that diff is 102 lines over three files and is a strict
superset of the r5 diff. — EVIDENCE: `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-r2 proposals/0081_*/`

FACT: the whole r2→HEAD delta is four hunks and every citation in it re-verifies.
(1) checklist S2 / summary SPEC-1 index now say the §4.7 `Shutdown` row "names the two fields a
request carries, one of `bind_attempt` and `unconditional_teardown`", which the staged row does
(spec-changes.md:278, "by carrying either a non-empty `bind_attempt` ... or
`unconditional_teardown`"); the withdrawn "states the two-field precondition and its
`INVALID_ARGUMENT` answer" wording is gone from both sites.
(2) CONF-1's rule-6 case now asserts status, `ErrorCode` AND category; rule 6 does state one
("carried as `CATEGORY_PERMANENT`", spec-changes.md:1057) and the category is observable —
`Error.Category` is field 2 of the nested envelope (schemas/lenny-adapter.proto:533,543-548) and
the one shipped adapter-side construction sets it (pkg/adapter/staging.go:209-214).
(3) CODE-1's `answerShutdown` comment and its non-spec bullet re-key the scrub from "outside both
teardowns and both refusals / every outcome" to "outside the arms that remove nothing / every
outcome `answerShutdown` builds", plus "a request the two-field precondition refuses returns above
this helper and performs nothing, the scrub included". Consistent with the staged precondition,
which returns before `s.mu` (non-spec-changes.md:194-205).
(4) the new tier-1 precondition rows cite "the staged §4.1 scrub sentence and §4.7.1 rule 10";
both say what is claimed — §4.1 replacement at spec-changes.md:237 ("runs the whole-pod scrub when
the recycle disposition is set on a request that passed the teardown-pairing rule ... whatever
outcome that request answers") and rule 10 at :1061 ("removes no entry, and changes nothing").
The fixture claim also holds: `startRuntimeOps` exists (pkg/adapter/files_updated_test.go:20 and
elsewhere) and the two recycle-scrub cases it points at do name it (non-spec-changes.md:3149,:3154).

FACT: both new pairing predicates are logically equivalent to the rules they implement, checked by
truth table. Rule 1 refuses `(bindAttempt == "") != midSession` (non-spec-changes.md:1533 against
spec-changes.md:1052); rule 10 refuses `(attempt == "") == !unconditional`
(non-spec-changes.md:200 against spec-changes.md:1061). The two spellings differ (`!=` vs `== !`)
and both are correct; do not file the asymmetry.

FACT: `TestRecyclePathScrubReportedReuses_spec_5_2` is at tests/tier4_integration/recycle_scrub_path_test.go:404
and its `binder.Release` at :429 exactly, re-verified because a naive `sed` range made it look
off by one. — EVIDENCE: tests/tier4_integration/recycle_scrub_path_test.go:404,:429

USEFUL [the whole non-spec citation sweep is DONE: 171 distinct `file:line` targets]: it is
correct and it saved the whole pass. I re-ran only a cheap existence-and-range net over all four
non-log proposal files (every backticked `pkg|cmd|tests|schemas|docs|migrations|charts|spec`
path with an optional `:LINE`): 32 paths do not exist and every one of them is a file this
proposal CREATES (`pkg/adapter/bindattempt.go`, the five new tier-7a/8/9/10/11 test files,
`pkg/gateway/podlifecycle/podsession/bindattempt.go`, etc.), and no cited line number exceeds its
file's length. That net is ~20 lines of python and is the right residual check once the content
sweep is recorded done.

### [non-spec.7.review-client-surface.1]

FACT: The client-facing parallels of this proposal's wire change are NARROWER than the lens
prompt suggests, and I re-derived the whole set so a later round need not. No SDK under
`sdks/client/*` or `sdks/runtime/*` references `ShutdownRequest`, `ShutdownResponse`,
`bind_attempt`, `slot_reclaim` or any adapter `ErrorCode`; `grep -rln "ShutdownRequest\|ShutdownResponse" sdks/`
returns nothing. `pkg/gateway/externalapi/openapi/openapi.json` (NOT `pkg/gateway/openapi/...`,
which does not exist) contains zero occurrences of `SETUP_COMMAND_FAILED` and enumerates no
error codes at all, so SPEC-5/DOCS-3's catalog rewording needs no OpenAPI, MCP-tool-schema or
SDK mirror. No `schemas/*.json` carries `error_code`. `schemas/README.md` enumerates no message
or enum. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:936 (only `setup-output` hit)

FACT: `SETUP_COMMAND_FAILED` has exactly one reader-facing carrier,
`docs/reference/error-catalog.md:129`, and DOCS-3's four quoted sentences plus the remedy cell
all match that line verbatim. Nothing under `tests/`, `scripts/`, `cmd/` or a Makefile names
`docs/reference/error-catalog.md`, so DOCS-3's "no gate holds the two to one text" claim is
true as stated. EVIDENCE: docs/reference/error-catalog.md:129

FACT: the SCHEMA-1 field-number table is correct against the tree, message by message
(`PrepareWorkspaceRequest` 1-3 used + `reserved 4 "slot_id"`; `FinalizeWorkspaceRequest`
`mid_session` = 4, `reserved 5`; `RunSetupRequest` reserved 4; `AssignCredentialsRequest`
reserved 3; `ResumeRequest` 1-5, 7-14 used, `reserved 6 "task_id"` and `reserved 15 "slot_id"`;
`ShutdownRequest` 1-3, `reserved 4`, 5 `recycle`, 6 `coordination_generation`;
`ShutdownResponse` 1-2). `ErrorCode` runs to 27 and is nested in `message Error`, so CODE-7's
`adapterv1.Error_ERROR_CODE_...` Go spelling is right. Do not re-derive.
EVIDENCE: schemas/lenny-adapter.proto:530-588

FACT: additive fields break no other tier-3 descriptor gate. `tests/tier3_contract/adapter_session_address/session_address_wire_test.go`
walks every message in the adapter file but only asserts absence of `slot_id`, presence of
`session_id`, and the reserved numbers/names, all of which SCHEMA-1 preserves.
`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go` is indeed the only
closed field set over the opened messages. EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:76-96,155-172

FACT: the two tier-11 gates DOCS-2 reasons about behave as it claims.
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only the four substrings
"end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub", all
present in the staged row; `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
requires the literal `"The request is session-scoped: it is addressed by the identifier of the
released session and names no slot."` in BOTH carriers, and the staged DOCS-2 row reproduces it
exactly. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310;
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:32,66-73

WATCHOUT: the CRD `Sandbox.status.phase` doc comment ALSO enumerates the occupancy-projection
inputs ("per-pod SandboxClaim existence, the claim's binding state, and the pool's
sessionPolicy") and is in no edit list, even though SPEC-4 adds "the phase the pod currently
projects" to §4.6.1's enumeration and DOCS-1 adds it to `docs/reference/state-machines.md:138`.
I decided this is NOT a finding, on the proposal's own stated rule: SPEC-4 leaves §6.2's
enumeration unchanged "because no surviving §6.2 clause reads the phase the pod currently
projects", and the CRD comment likewise carries no claim-deletion clause. The authoring source
is `pkg/apis/lenny/v1alpha1/sandbox_types.go:114`; the two CRD yamls are generated copies, so
anyone who later disagrees must edit the Go type, not the yaml.
EVIDENCE: pkg/apis/lenny/v1alpha1/sandbox_types.go:114; charts/lenny/crds/lenny.dev_sandboxes.yaml:404-411;
pkg/embedded/crds/lenny.dev_sandboxes.yaml:407

FACT: the `error_type` label values of `lenny_slot_failure_total` are enumerated NOWHERE outside
Go (`binder.go:290-298`); neither `spec/16_observability.md:14` nor
`docs/reference/metrics.md:166` lists them, and no test pins the set. CODE-9's new
`slotFailureWorkspaceFinalize` stage value therefore owes no doc or spec mirror.
EVIDENCE: docs/reference/metrics.md:166; spec/16_observability.md:14

FACT: the proto `mid_session` field on `FinalizeWorkspaceRequest` is declared but set by NO
production code today (`grep -rn "MidSession" pkg/ --include=*.go | grep -v _test` returns only
the unrelated §5.1 capability `midSessionUpload`). The proposal wires it for the first time in
`pkg/gateway/sessionserver/upload_to_session.go`. Do not read the shipped field as evidence the
mid-session marker is already on the wire.

DECISION: filed exactly one finding, on DOCS-2's added "Bind attempt token." paragraph
overstating the carriage set. BECAUSE it is a positive wire-contract assertion on the page whose
own staged text says "An adapter author reads both", and it contradicts the staged §4.7.1
carriage table, which marks `StartSession` and `ConfigureWorkspace` "not carried" and states the
harm of comparing a token on them. ALTERNATIVES: I considered and rejected filing (a) the CRD
projection-enumeration omission above; (b) CODE-7's "the six bind-sequence requests" count,
which is five bind-sequence requests plus `ShutdownRequest` — a stale count of the doc-style kind
this loop has repeatedly refuted as immaterial; (c) the absence of a descriptor pin on the three
`SlotReclaimOutcome` enum values by number (the gate pins the nine fields and the two new
`ErrorCode` values but not the new enum's values), which is a nice-to-have hardening test rather
than a required tier.

### [non-spec.7.review-docs-alignment.1]

VERDICT: EMPTY. Full docs-alignment sweep of the proposal (spec-changes + non-spec-changes +
checklist + summary read as one document) found nothing meeting the bar.

FACT: The docs-surface map for 0081 is closed and every surface is in an edit list. Verified
one by one against the tree: adapter-contract.md `Shutdown` :75, `DemoteSDK` :64,
`ReportSessionScrub` :81 (DOCS-2); state-machines.md running row :235, released row :237,
leaked clause :251, pod-state-machine projection paragraph :138 (DOCS-1); error-catalog.md
`SETUP_COMMAND_FAILED` :129 (DOCS-3); execution-modes.md :68 and
operator-guide/security-principles.md :33 (DOCS-4); metrics.md rows (CODE-9). Every quoted
"currently reads" block matches the tree verbatim and every line number is right, including
adapter-contract.md:10 and :53.
EVIDENCE: docs/reference/adapter-contract.md:10,53,64,75,81; docs/reference/state-machines.md:138,235,237,251; docs/reference/error-catalog.md:129; docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33

FACT: The withdrawn per-release reporting universal has exactly three docs carriers, and the
carrier table assigns all three. `grep -rn "reports its outcome to the gateway" docs/` returns
security-principles.md:33 and execution-modes.md:68 only; adapter-contract.md:81 is the third.
multi-tenancy.md:72 and client-guide/session-lifecycle.md:416 state the cleanup without the
reporting clause and stay true. runtime-author-guide/lifecycle.md:390 reports the WHOLE-POD
scrub outcome, not the per-slot one, so it is not a carrier.
EVIDENCE: docs/operator-guide/multi-tenancy.md:72; docs/runtime-author-guide/lifecycle.md:390

FACT: No docs page enumerates the adapter `ErrorCode` enum, so the two new codes owe no docs
row. `grep -rn "ErrorCode\|ERROR_CODE" docs/` returns nothing at all.

WATCHOUT: `docs/operator-guide/troubleshooting.md:41` carries `reason: setup_command_failed`
and looks like a second home of the §15.1 row DOCS-3 edits. It is NOT: that table is the
warm-pool-exhaustion diagnosis table and the token is the `error_type` label of
`lenny_warmpool_warmup_failure_total`, a pod warm-up failure, not the REST envelope's
`details.reason`. Do not file it as a missing DOCS-3 site.
EVIDENCE: docs/operator-guide/troubleshooting.md:41; docs/reference/metrics.md:135

FACT: Linking from docs/ into spec/ by GitHub URL, as DOCS-2's bind-attempt paragraph does, is
the established repo convention rather than a `doc-content.md` violation: the style guide
prescribes it, and 22 docs files already do it.
EVIDENCE: docs/about/style-guide.md:31 "Cite the spec section with a link ... Treat the spec like RFC references: specific section, stable URL."

FACT: Every tier-11 gate that reads an edited docs page survives the staged edits, checked
against the assertion text rather than the proposal's summary of it.
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs only "end-of-session teardown",
"recycle disposition", "ReportSessionScrub", "ReportPodScrub" (all present in DOCS-2's row);
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` needs the addressing sentence
byte-identical on both carriers (DOCS-2 and SPEC-3 both keep it);
`TestStateMachinesDocMirrorsThePerSlotSubStateScope` needs "`slot_cleanup -> leaked`" and
"`ceil(maxConcurrentSessions/2)` slots fail" in the concurrent section (DOCS-1's leaked
replacement keeps the first token and does not touch the fail-or-leak row);
`TestPerSlotCleanupStatedOnEverySessionModeRow` reads only the residual-state table rows, which
DOCS-4 does not touch.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310,455-476; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43-76; tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:86-124

FACT: `generalSlotEdges` in per_slot_substate_scope_doc_reconciliation_test.go feeds BOTH a
positive loop over the §6.2 either-concurrency block and a negative loop over the
concurrent-occupancy block, so the proposal's claim that adding
`"receiving_uploads ──→ slot_cleanup"` gates both sides is exact. The new §6.2 prose paragraph
writes the same edge with a single arrow (`receiving_uploads → slot_cleanup`), which does not
collide with either loop.
EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-37,55-73

FACT: `grep -rn "error-catalog" tests/ scripts/ cmd/ Makefile` returns nothing, confirming
DOCS-3's "no shipped gate" claim.

USEFUL [standing context, "docs/api/internal.md is wholesale pre-existing drift"]: saved a
re-derivation. Confirmed independently: internal.md declares `StopSession`, not `Shutdown`
(docs/api/internal.md:81-83,139), so the new `ShutdownRequest` fields falsify nothing new there.

USEFUL [standing context, "The occupancy projection reads the pod's CURRENT PHASE, never the
recycle setting"]: DOCS-1's three projection-clause replacements line up clause-for-clause with
SPEC-4's re-keyed §4.6.1 bullets, including the "warm-inventory phase" qualifier that §4.6.1
already carries and the published page currently omits.
EVIDENCE: spec/04_system-components.md:411 "`idle` for a pod in a warm-inventory phase with no claim."

UNVERIFIED: whether the accepted failure mode "A rolled-back start can close a successor's
runtime session" (non-spec-changes.md, Edge cases) owes a landing statement anywhere. Rule 8
stages the rollback itself ("It takes the session back off the shared runtime process"), but
the consequence for a concurrently-starting successor on the same identifier appears only in
the proposal's reasoning. I judged it below the bar because the consequence follows from
`Runtime.Close`'s slot-identifier keying, which is code the proposal does not change, and the
non-spec section is declared as "the cases that concern code alone". A later reviewer with a
reader-facing or operator-facing consequence for it should re-weigh it.
EVIDENCE: proposals/.../0081...non-spec-changes.md:3791-3803; proposals/.../0081...spec-changes.md rule 8

### [non-spec.7.review-edit-sites.1]

DECISION: filed exactly two findings, both lifted from the live `### Deferred` list of the review log, because both remedies land in `non-spec-changes.md`, which THIS loop may edit, and both were verified unstaged at HEAD — BECAUSE the Deferred list is explicitly "an answer nobody has applied", and these two name their own landing site in the non-spec lane. ALTERNATIVES: rejected filing the other Deferred entries — `docs/getting-started/architecture.md:237` (pre-existing incompleteness, already omits `sessionPolicy`), `sandbox_types.go:113-118` (marked "record only"), `docs/api/internal.md` (its own entry says the proposal does not make it newly wrong), `Client.Shutdown` doc comment (false today, not falsified by this change).

FACT: the `schemas/lenny-adapter.proto` `Shutdown` RPC comment ("asks the adapter to terminate the agent and release the pod ... Returns when the agent process has exited", :203-205) is ALREADY false at HEAD, so SCHEMA-1 does not falsify it and it is NOT a missed edit site. The shipped handler returns without any teardown when the registry holds no entry (`bound` false) and gates the drain frame on `!boundRemains`. This is the same refutation shape the evidence skeptic used on the `ErrorCode` "mirrors spec §15.1" header. Do not re-file either. — EVIDENCE: pkg/adapter/session.go:227-292; schemas/lenny-adapter.proto:203-206

FACT: all nine SCHEMA-1 field numbers check out free against `schemas/lenny-adapter.proto` as it stands (PrepareWorkspaceRequest 5/6, FinalizeWorkspaceRequest 6, RunSetupRequest 5, AssignCredentialsRequest 4, ResumeRequest 16, ShutdownRequest 7/8, ShutdownResponse 3), and every production constructor of the six carrying requests plus `ShutdownRequest` lives in `pkg/gateway/runtime/adapterclient/client.go` alone (the only other non-generated hit is the adapter's own `ShutdownResponse`). The test-file sweeps in the files-touched list therefore close the set. — EVIDENCE: schemas/lenny-adapter.proto messages; pkg/gateway/runtime/adapterclient/client.go:181,275,307,328,617,814

FACT: no chart template, no `pkg/alerting/rules` entry and no operator-guide dashboard names any `lenny_slot_*` series, so the two new counters owe an alert or runbook page nothing. `docs/reference/metrics.md` is hand-authored (no generation header). — EVIDENCE: `grep -rn "lenny_slot_" charts/ docs/operator-guide/ pkg/alerting/` returns nothing; docs/reference/metrics.md:1-11

FACT: `docs/operator-guide/multi-tenancy.md:72` carries the same per-slot-cleanup sentence as the two DOCS-4 pages but WITHOUT the ", and the adapter reports its outcome to the gateway" clause, so DOCS-4's two-page scope is complete. `docs/runtime-author-guide/index.md:186` and `lifecycle.md:69` state the cleanup with no reporting claim. — EVIDENCE: docs/operator-guide/multi-tenancy.md:72

FACT: `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the literal "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." in BOTH the §4.7 row and the adapter-contract row; SPEC-3's replacement row (spec-changes.md:768) and DOCS-2's replacement row both keep it verbatim, so the gate survives. That gate's own header comment (:6-8) and `// spec:` line (:20-21) carry the withdrawn "every session release" universal and ARE returned by CODE-10's grep, so they are covered. — EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:33,67-74

FACT: `TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` asserts the concurrent-occupancy block contains NONE of `generalSlotEdges`; the new `receiving_uploads ──→ slot_cleanup` edge is not in that list and lands in the general block, so SPEC-4 turns it green-to-green. `slotstate_test.go:22` pins `len(ValidTransitions())` and is in the files-touched Tests list. — EVIDENCE: tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:32-75; pkg/sandbox/slotstate/slotstate_test.go:21-23

WATCHOUT: the root-level `gateway-runtime-comms.md` matches greps for `PrepareWorkspaceRequest`, `Shutdown` and the channel identifiers, but its first three lines declare it a point-in-time record of `fcda83e3` superseded by spec §28/§29 and "not maintained". It is never an edit site. — EVIDENCE: gateway-runtime-comms.md:1-3

UNVERIFIED: CODE-9 declares the hook as `SlotReclaim func(outcome, pool, podName string)` while SPEC-6's §16.1 row gives the superseded series the labels `pool` and `k8s_pod_name` only. If the third argument becomes a label the series gains an `outcome` dimension the spec row does not declare; if it does not, the parameter is dead. The review log already carries "SlotReclaim hook arity" under Open/cut-for-budget, so this is a re-sighting rather than a new lead. The mechanism lens or a human should settle it. — EVIDENCE: non-spec-changes.md:2058-2066; spec-changes.md:1221

USEFUL [the review log's `### Deferred` section]: it is the highest-yield section in the whole 2674-line log for an edit-site lens. Every entry there is a verified missing site someone already paid to derive; the only work left is checking whether it is still unstaged and whether this loop's edit set reaches its file. Two of the eleven entries were both.

### [non-spec.7.review-fresh.1]

VERDICT: EMPTY. A fresh holistic read of the whole proposal (summary, checklist, spec-changes,
non-spec-changes read as one document) returned nothing meeting the bar.

FACT: every `path:line` citation in non-spec-changes.md resolves. Mechanical check: extract with
`grep -oE '`?(pkg|cmd|tests|schemas|charts|migrations|docs|scripts)/[A-Za-z0-9_./-]+\.(go|proto|md|sql|json|py)`?:[0-9]+(-[0-9]+)?'`,
96 distinct cites, all files exist and all line ranges are in range; spot-read of all 96 showed the
quoted content at each. A future round should not re-run this sweep without new edits to the file.
EVIDENCE: /home/ec2-user/lenny/proposals/0081_.../0081_....non-spec-changes.md (whole file)

FACT: the bare-filename cites (no directory prefix) that my regex missed all check out too:
`binder_test.go:301` / `:322-324` (fakeAssigner.released comment and ReleaseSession),
`slotretry_test.go:383-407` / `:468-492` / `:519-548` (the three shipped harnesses),
`slotbinder.go:303-304` (the refused-RunSetup wrap, stage constant `slotFailureSetup` = "setup" at
pkg/gateway/podlifecycle/podsession/binder.go:291), `start.go:87-90` (writePodClaimError).
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:291

FACT: every `rule N (the <name>)` reference across all four proposal files matches the §4.7.1 name
for that number. Checked with
`grep -oiE 'rules? [0-9]+ \(\*?\*?[^)]{0,60}\)'` across spec-changes, non-spec-changes, summary and
checklist; 15 distinct pairs, zero mismatches.

FACT: the rule-1-before-rule-2 ordering question at the guarded RPCs is already answered in the
staging and is not a gap. `acquireSlotGuardForResolve` is taken "at the point that RPC runs
`validateBindFields` and before its resolve ... so the order §4.7.1 publishes, rule 1 then rule 2
then rules 3 through 7, is the order the code runs."
EVIDENCE: 0081_....non-spec-changes.md:1741-1749

FACT: CODE-11's "four comment sites" all exist in the tree, and `writeSetupCommandError` is a
`*Server` METHOD (pkg/gateway/sessionserver/start.go:236), not a package function — `grep -rn "func
writeSetupCommandError"` returns nothing and reads as a false citation until you grep for the
method form.
EVIDENCE: pkg/gateway/sessionserver/start.go:236

FACT: the `slotAddressCaseFiles` obligation for the new `slot*_test.go` files is discharged
generically in the files-touched list ("Every new file whose name matches that pattern enters the
inventory in its sorted position, in the step that creates it"), not only per-file, so
`tests/tier7a_load_local/slot_bind_attempt_race_test.go` is covered even though no step line names
it for that register. Do not file it.
EVIDENCE: 0081_....non-spec-changes.md:3938-3944

FACT: the docs sweep for SPEC-3's withdrawn reporting universal is complete. `grep -rni "per-slot
cleanup" docs/` returns seven sites; the two carrying ", and the adapter reports its outcome to the
gateway" are exactly DOCS-4's two (docs/reference/execution-modes.md:68,
docs/operator-guide/security-principles.md:33). docs/operator-guide/multi-tenancy.md:72 carries the
same sentence WITHOUT the clause and is correctly left unedited.
EVIDENCE: docs/operator-guide/multi-tenancy.md:72

FACT: the three shipped tier-11 gates the staging leans on all pass against the staged replacement
text. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` needs "end-of-session teardown",
"recycle disposition", "ReportSessionScrub", "ReportPodScrub" — all four are in DOCS-2's staged
`Shutdown` row. `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` needs the exact
string "The request is session-scoped: it is addressed by the identifier of the released session and
names no slot." on BOTH the §4.7 row and the docs row — both staged rows carry it verbatim,
period included.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-72

WATCHOUT: `slotSubjectFileRE` is only ONE of three derived inventory rules. The other two are
`slotSurfaceCallRE` over file CONTENT (`slotstate.|ClaimSlot(|ReleaseSlot(|ReserveSlotOnPod(|claimAtCreate(|BindReservedSlot(`)
and the TEST-GAPS.md rule for tier11_docs files. A new test file with no "slot" in its name still
enters the inventory if its body calls one of those six. The proposal states the content rule too.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1168-1186

FACT: the "nine fields" count in the Design section reconciles exactly with SCHEMA-1's table and
with the tier-3 descriptor gate's "nine fields": bind_attempt on six messages, mid_session on
PrepareWorkspaceRequest, unconditional_teardown on ShutdownRequest, slot_reclaim on
ShutdownResponse.

USEFUL [the orchestrator's refuted list]: the four refuted entries (CODE-10 grep closure, S23 tier
11, S22 Depends-on, S16/S17 split, holdstate.go files-touched bullet) all describe the residue class
this document still carries — per-step bookkeeping annotations and index-entry shorthand. I
re-derived two of them independently before reading the list and stopped. A later lens that finds
itself writing "the annotation says X and the deliverable says Y" about a checklist tier list, a
`Depends on:` line or a files-touched bullet should stop: that class is adjudicated immaterial.

OPEN: nothing new. My lens found no defect in the staged spec edits, the staged non-spec text, the
checklist or the summary that would make the applied spec wrong, internally inconsistent or
unimplementable.

### [non-spec.7.review-kubernetes.1]

VERDICT: EMPTY. No finding met the bar under the Kubernetes-idiom lens.

FACT: every apiserver-touching claim the staging makes checks out against the tree, so a later
Kubernetes lens can start from these rather than re-deriving them.
- "the gateway is not a writer of `Sandbox.status`" and "the `lenny.dev/drain-request` annotation
  belongs to `Binder.DrainSandbox`" (non-spec-changes.md:3483-3486) — EVIDENCE:
  spec/04_system-components.md:632 ("The gateway holds no `sandboxes/status` grant..."),
  pkg/gateway/podlifecycle/podsession/slotbinder.go:601 (`DrainSandbox`),
  pkg/controller/warmpool/occupancy.go (OccupancyReconciler doc comment, sole WPC field manager).
- `failPhase`'s drain is the only apiserver write on either `Binder.Prepare` arm, and it is a
  `SandboxClaim` DELETE — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1072-1082 and
  :1200-1202 (`drain` → `podclaim.DeleteClaim`); `Binder.Prepare` (:843-965) writes nothing else
  at the apiserver and its own comment at :900-906 states it writes no per-step CRD phase.
- CODE-8's cited line numbers are all live: reclaim closures at binder.go:866-869 and :997-1000,
  call sites :883, :918, :923, :935, :950, :1010, :1021, :1032.
- CODE-5's `accountSlotFailure` needs no interface widening for the drain tail: `slotBinder`
  already declares `DrainSandbox` — EVIDENCE: pkg/gateway/sessionserver/start.go:2725-2729.
- Summary's "`ReleaseSlotReservation` ... touches the SandboxClaim and the Redis slot counter"
  is accurate — EVIDENCE: slotbinder.go:493-504 (`podclaim.SlotClaimer.ReleaseSlot`, whose
  claim-delete-on-last edge is documented at :513-517).

FACT: DOCS-1's four pod-state-machine replacement strings all exist verbatim on the page today
and the replacements mirror the staged §4.6.1 bullets rather than diverging from the controller.
EVIDENCE: docs/reference/state-machines.md:138 carries "A pod with no claim projects `idle`",
"a claim deleted on a recycling pod under its limits projects `idle`" and ", or a claim deleted
on a pod with `recycle.enabled: false`,"; spec/04_system-components.md:411 already reads "`idle`
for a pod in a warm-inventory phase with no claim", which is the wording DOCS-1 adopts, and
pkg/controller/warmpool/occupancy.go `ProjectOccupancyPhase` returns Idle from Reserved and
Draining from Claimed on a claim DELETE, with ok=false for every warm-fill phase.

FACT: nothing in the staging puts a status subresource on a command path, adds a finalizer,
introduces a second field manager on `Sandbox.status`, or blocks a synchronous request on a
reconcile. The compensation is gateway→adapter gRPC (`ShutdownReclaim`), the leak ledger is
gateway-side plus Redis, and the drain reaches the WPC through the pre-existing
`lenny.dev/drain-request` annotation with no wait. The adapter (agent pod) gains only a counter,
so the zero-RBAC posture is untouched.

WATCHOUT: CODE-8 skips `failPhase` (hence the claim DELETE) on a typed refusal, which looks like
a leaked `SandboxClaim` until you notice `SlotID == SessionID`: both refusal codes can only be
raised by an entry for the SAME session, so the claim that survives is the winner's and is
released by the winner's own end-of-session path. Do not file this as a stuck-claim footgun.
EVIDENCE: non-spec-changes.md:1980-1992, :2018-2026.

### [non-spec.7.review-mechanism.1]

VERDICT: EMPTY. Traced the whole mechanism end to end (mint -> carry -> resolve cascade ->
reclaim hold -> per-slot guard -> Shutdown teardown cascade -> confirmation rollback ->
gateway compensation -> leaked disposition -> accounting) and found nothing meeting the bar.

FACT: the round-6 -> round-7 delta is THREE hunks only, and all three are the fixes named in the
"already found and fixed" list — checklist S2 and summary's SPEC-1 index line dropped
"states the two-field precondition and its `INVALID_ARGUMENT` answer" for "names the two fields
a request carries, one of `bind_attempt` and `unconditional_teardown`", and CONF-1's rule 6 case
gained "and the category". Both re-verify: the staged §4.7 `Shutdown` row does say
"Every request states which teardown it asks for, by carrying either a non-empty `bind_attempt`
... or `unconditional_teardown`" and states no `INVALID_ARGUMENT`
(spec-changes.md:278); rule 6 does state `CATEGORY_PERMANENT` (spec-changes.md:1046).
EVIDENCE: spec-changes.md:278, :1046; implementation-checklist.md:19; summary.md:1122.

FACT: the hold set cannot be double-inserted or released by the wrong cleanup, which is the
availability hazard a mechanism lens reaches for. `reclaimSlotLocked` inserts only when
`deregisterSlotLocked` removed an entry, `delete(s.slots,…)` is atomic under `s.mu`
(pkg/adapter/slotsession.go:180), and entry CREATION runs only through `ensureSlotStateLocked`,
whose first act is the hold test. So a held identifier has no entry for a second remover to
find, and an identifier held for the life of the pod cannot be re-held or prematurely released.
EVIDENCE: pkg/adapter/slotsession.go:174-189; non-spec-changes.md:48-59, :192-196.

FACT: every one of the seven admission RPCs meets the hold at exactly one of the two test
points and none bypasses it. `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup` and `Resume`
take `acquireSlotGuardForResolve`; `AssignCredentials`, `StartSession` and `ConfigureWorkspace`
take no guard and meet it inside `ensureSlotStateLocked`. The guard derivation table and the
CONF-1 rule 2 case agree on that split row for row.
EVIDENCE: non-spec-changes.md:415-427 (table), :2245-2251 (CONF-1 rule 2).

FACT: the mandatory-field sweep has no PRODUCTION hole. The only non-adapter, non-test callers
of the five `validateBindFields`-guarded RPCs are `slotbinder.go:291,:299,:403`,
`binder.go:921,:933,:1256,:1327,:1607` and `upload_to_session.go:128,:134`; every one is
either inside an attempt that mints (`materializeSlot`, `Binder.Prepare`, `Binder.Resume`) or
is the §7.4 mid-session pair. `pkg/embedded/localcli/session.go:396`'s `FinalizeWorkspace` is
the REST SDK client (`sdks/client/go/lenny`), not the adapter, so it is a false hit on the
obvious grep. `stageWorkspace` (binder.go:1283) is shared by both bind entry points and is the
one helper that must thread the token; binder.go is in the files-touched list under
"`Binder.Prepare`'s mint, carry", so it is covered rather than missed.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1283,:1327; pkg/embedded/localcli/session.go:396; non-spec-changes.md:4029-4030.

FACT: CODE-9's new `slotFailureWorkspaceFinalize` label value is safe against the tree. The
shipped stage constants live in `binder.go:289-299` (not `slotfailure.go`, where the proposal's
files-touched entry puts the new one — a placement choice, not a false citation), no alert rule,
runbook, docs page or spec row enumerates `error_type` VALUES for `lenny_slot_failure_total`
(spec/16:14 and docs/reference/metrics.md:166 name the label only), and no shipped test asserts
a `workspace_prep` SlotFailure from a Finalize failure — the only SlotFailure assertions are
`slotbinder_test.go:753,:782`, both on the start stage. So changing the finalize site's metric
label while leaving `SlotBindError.Stage` at `slotFailureWorkspacePrep` (which keeps
`Reason()`'s transient carve-out at slotfailure.go:96) breaks nothing.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:286-299; slotfailure.go:96; spec/16_observability.md:14; docs/reference/metrics.md:166; slotbinder_test.go:753,:782.

FACT: `isTransientPodClaimError` has TWO correct but different citations in the proposal and
both check out — CODE-5 cites `start.go:3648-3681` (the function body) and CODE-11 cites
`:3629-3647` (the setup-cause doc-comment span it rewrites). A lens that assumes one of them is
wrong will waste a pass.
EVIDENCE: pkg/gateway/sessionserver/start.go:3622-3681.

WATCHOUT: CODE-1's staged clause-three citation `pkg/adapter/session.go:283-291` is one line
loose at both ends — the shipped `if rc := req.GetRecycle()` block is :288-290 and :291 is the
`return`. The quoted code is verbatim and inside the range, so it is below the bar; do not
re-file it. EVIDENCE: pkg/adapter/session.go:282-291.

WATCHOUT: the tier-7a fifth arm's sentence explains the successor's `AssignCredentials`
succeeding by "the derivation table leaves `AssignCredentials` unguarded" alone. The other
necessary condition is that `releaseSessionSlotUnderGuard`'s own reclaim hold was RELEASED
(its tree removal returned nil). The mechanism works; the rationale is partial, not false.
Do not file it. EVIDENCE: non-spec-changes.md:3643-3660; non-spec-changes.md:100-102.

USEFUL [the Settled ledger's CODE-1 defer-order and predicate lines]: lines on LIFO defer order,
`live ⊆ started ⊆ removed`, the `answerShutdown` single exit and the `removed`/`started`/`live`
row-for-row agreement with the §5.2 table saved re-deriving the whole handler. Every one of them
re-verified against the staged code block on this pass.

### [non-spec.7.review-performance.1]

VERDICT: EMPTY. Performance, scalability and top-tier failure-mode lens found nothing meeting the bar.

FACT: the change adds NO new control-plane write per unit of work. The token is in-memory on both
ends (gateway `newBindAttempt`, adapter `slotState.bindAttempt`), the compensation is one extra
adapter gRPC on the FAILURE path only, and the two new counters are Prometheus-only. The leaked
disposition strictly REDUCES apiserver and Redis writes on the paths it reaches, because
`SlotClaimer.ReleaseSlot(leaked=true)` returns early with no decrement, no claim DELETE and no
recycle patch. — EVIDENCE: non-spec-changes.md:1002-1024 (compensateFailedSlotBind), review-log.md
Standing context "SlotClaimer.ReleaseSlot(leaked=true) returns early".

FACT: `sessions_served` is already incremented only from `RecordSessionScrub`, i.e. from
`ReportSessionScrub`, which has exactly one caller (the adapter `Shutdown` handler's bound arm).
SPEC-3's report biconditional and the §12.6 re-key therefore record shipped behaviour and lower the
Postgres write rate relative to the OLD SPEC universal, not relative to the code. Do not file the
re-key as a change to a security-bounding counter's write path. — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:457
(`IncrementSessionsServed`), pkg/adapter/session.go:279 (`s.reportSessionScrub`, inside `if bound`).

FACT: CODE-1 narrows the report gate from shipped `bound` (`removed && st.sessionID != ""`) to
`live` (`removed && s.runtimeHoldsLocked(sessionID)`, i.e. `runtimeLive` membership set by
`noteRuntimeStarted`). That is a narrowing, so it can only lower the `sessions_served` rate and can
only DELAY `recycle.maxSessionsPerPod` retirement, never accelerate it, and the sessions it drops
are ones the pod never ran. — EVIDENCE: non-spec-changes.md:386 and :443-445 vs
pkg/adapter/session.go:239-240,:279; spec-changes.md:1041 (the record is `running`).

FACT: the `k8s_pod_name` label on `lenny_slot_compensation_superseded_total` is the shipped
precedent's label set, not a new cardinality class: `lenny_slot_failure_total` already carries
`error_type`, `pool`, `k8s_pod_name` and §16.1 sanctions it there. The adapter series is one series
per pod process. — EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:185-193,
spec/16_observability.md:14.

WATCHOUT: §16.1.1's attribute table is declared "single source of truth" and says a label "must not
appear anywhere in the spec unless it is in this table"; the `k8s_pod_name` row's "Used on" column
enumerates "Session-pool scrub and retirement metrics, slot failure and replacement metrics, and the
session reuse histogram", which does not obviously cover a compensation-outcome counter or an
adapter shutdown counter. I judged this BELOW the bar and did not file: the prohibition's literal
subject is the label's presence in the table (it is present), the "Used on" cell is descriptive, and
the spec lane is locked. A later lens that wants to file it should file it as a spec-lane open
decision on the §16.1.1 cell, not as a non-spec defect. — EVIDENCE:
spec/16_observability.md:294,:297.

DECISION: did NOT file the correlated-failure churn argument (a gateway↔pod-network partition makes
bind AND compensation fail together, so every affected pod is marked leaked and, at
`maxConcurrentSessions: 2`, drained, where shipped released cleanly through Redis/apiserver and
drained nothing) — BECAUSE the proposal states the trade explicitly, it is §6.2's leaked semantics
applied consistently, and an unanswered reclaim genuinely cannot be assumed clean.
ALTERNATIVES: filing it as a failure mode less reliable than shipped; rejected as a preference
between workable designs and hypothetical hardening. — EVIDENCE: non-spec-changes.md:3862-3868
("Faster pod churn at `maxConcurrentSessions >= 3`"), :1318-1330 (CODE-5's stated trade).

DECISION: did NOT file the Redis-restart durability gap (a leaked failed-bind slot's withheld
occupancy lives only in the Redis `active_slots` counter; the §12.4 durable fallback,
`GetActiveSlotsByPod` / `ReserveSlotUnderLock`, counts live non-terminal SESSION rows, and a failed
bind has no live row, so a Redis restart rehydrates the occupancy away) — BECAUSE the shipped
`ReleaseSlot` leaked path has exactly the same property, so the design degrades no worse than
shipped; the lens's bar is "less reliable than the shipped behavior". — EVIDENCE:
pkg/gateway/session/sessionstore/sessionstore.go:984-995 and :997-1011.

FACT: the compensation blocks its caller on a DETACHED context for up to
`max(cleanupTimeoutSeconds/maxConcurrentSessions, 5)` seconds even after the client's context
expired, inside `applySlotRetryPolicy`'s retry loop. I checked the magnitude before deciding: the
spec's own pool examples use `cleanupTimeoutSeconds` 30 and 60, so the bound is ~15-30s per attempt,
and the detachment is the stated point of the design. Not filed. — EVIDENCE:
non-spec-changes.md:1010-1013, spec/05_runtime-registry-and-pool-model.md:166,:412,:508,:545.

FACT: the `deadline_ms` arithmetic checks out. The `terminate` frame's schema minimum is 100 and the
budget's 5s floor puts `budget/2` at 2500ms, so no pool configuration can send a sub-minimum window.
— EVIDENCE: schemas/runtime-ops-events.schema.json:180, spec/05_runtime-registry-and-pool-model.md:545
(the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula and its CRD validation rule).

FACT: no net-new informer, watch, or work-queue is introduced, and no new gateway-global or
single-key serialization point. The two adapter locks are per-pod (`s.mu`) and per-slot
(`slotGuards` capacity-one channels); `Resume`'s network-bound `ExtractTree` is held under the
per-slot guard only, so it serializes one slot rather than the pod. — EVIDENCE:
non-spec-changes.md:1742-1800 (guard derivation table and lock-order paragraph).

### [non-spec.7.review-reliability.1]

VERDICT: EMPTY. Second consecutive empty verdict for this lens (the first was
`[non-spec.2.review-reliability.1]`, recorded in the changelog).

FACT: the compensation's budget cannot divide by zero on either path that calls it.
`Binder.Resume`'s `ResumeRequest.MaxConcurrentSessions` is normalized through
`maxConcurrentSessions(bound)` which clamps to 1 — EVIDENCE:
pkg/gateway/sessionserver/start.go:3353-3358 and :4029 — and `materializeSlot`'s
`SlotBindRequest` is built only on the concurrent path (`slotBindRequest`, start.go:2535-2546,
documented "for a concurrent-workspace pool (maxConcurrentSessions > 1)"). A future lens should
not re-file `slotCleanupBudget`'s unstated zero-divisor case as a finding.

FACT: `slotBindRequest` DOES populate `CleanupTimeoutSeconds` — EVIDENCE:
pkg/gateway/sessionserver/start.go:2566-2567. The contrary sentence in `exclusiveBindRequest`'s
comment ("Only the session-mode bind request carries them; the concurrent slotBindRequest omits
them", start.go:2524-2526) is a stale pre-existing comment in the tree, not a statement about the
current code and not something this proposal touches. I nearly filed "the concurrent compensation
always gets the 5s floor" on the strength of that comment; it is false.

FACT: `SocketRuntimeProcess.Close` calls `releaseActiveLocked(sessionID)` before it consults the
context at all — EVIDENCE: pkg/adapter/socketruntime.go:435-446, with `resolveShutdownGrace(ctx,
...)` only reached at :457. So CODE-2's `StartSession`/`Resume` rollback running
`s.Runtime.Close(ctx, sessionID)` on an already-cancelled inbound context (the canonical trigger,
per CODE-4's `compensateFailedSlotBind` comment) still removes the session from the shared
runtime's active set. There is no "session stranded in the active set with no reclaimer" hole
here; do not re-derive it.

FACT: the three exported teardown forms are consistent. `Client.Shutdown` and
`Client.ShutdownRecycle` both delegate to the one unexported builder `Client.shutdown` —
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:807-809, :813-823, :860-867 — so the
staged `unconditional_teardown = true` has exactly one home, and `ShutdownReclaim` legitimately
sits outside that builder because its return arity is three, not two.

WATCHOUT: this lens's whole hazard list is already answered, item by item, in the proposal's two
accepted-failure-mode sections (non-spec-changes.md:3765-3864 and the spec-changes file's
section of the same name, spec-changes.md:107-137). Before filing a reliability finding, grep
both: unanswered reclaim, gateway crash losing a compensation, hold held for the life of the pod,
a guarded `Resume` eating the compensation's budget, a §10.1.4 member parked under its own guard,
a rolled-back start closing a successor's runtime, and a lease double-count on a retried
`AssignCredentials` each already have an entry naming what is accepted and why.

DECISION: returned no findings — BECAUSE every recovery path I traced through crash, restart and
retry either has a stated reclaimer, a bounded and jittered-or-context-bounded wait, or an
explicit accepted-residue entry; the remaining candidates all failed on evidence in the tree
(above) — ALTERNATIVES: filing the `slotCleanupBudget` zero-divisor and the concurrent-path 5s
floor, both refuted by the two FACTs above before they left my desk.

### [non-spec.7.review-security.1]

FACT: `podsession.CredentialAssigner` (pkg/gateway/podlifecycle/podsession/binder.go:319-333)
declares exactly two methods, `AssignProto` and `ReleaseSession`. It has no `Release(leaseID)`.
CODE-4's `releaseAttemptCredentials(minted)` and `Binder.Prepare`'s attempt-scoped arm both
release BY IDENTIFIER, and the Testing section requires a `fakeAssigner.Release(leaseID)` field
(non-spec-changes.md:3288-3290, :3335-3336, :3355). The interface widening is in no edit list.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:319-333
FACT: both production implementors ALREADY have the method — `credassign.Service.Release`
(pkg/gateway/credentials/credassign/credassign.go:380) and `credassign.Client.Release`
(pkg/gateway/credentials/credassign/client.go:299) — so the widening costs no production code.
The cost is in the test fakes: `fakeAssigner` (pkg/gateway/podlifecycle/podsession/binder_test.go:322),
`reclaimRecordingAssigner` (pkg/gateway/sessionserver/terminal_reclaim_internal_test.go:33, wired at :52),
`recordingLeaseAssigner` (pkg/gateway/sessionserver/start_pod_test.go:1160),
`failingAssigner` and `recordingAssigner` (pkg/gateway/sessionserver/delegated_child_materialize_test.go:132, :143).
Only the first is named in the proposal.
EVIDENCE: pkg/gateway/sessionserver/delegated_child_materialize_test.go:451 `binder.Credentials = failingAssigner{}`

FACT: user-source leases are safe to release by identifier. `assignCredentials` mints them
through `UserCredentials.MintProto` but the comment states "The lease shares the credential-
lease store the pool path uses", so one `Credentials.Release(id)` reaches both classes.
`b.Credentials` can still be nil when only user providers were named, so the helper needs a
nil guard. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1243-1247, :1218

FACT (checked, clean, do not re-derive): the adapter logs no bind-attempt token. The only
unary interceptors on the adapter server are `credentialRedactionInterceptor` and
`holdStateUnaryInterceptor` (pkg/adapter/transport.go:46-47); the redaction one records lease
ids, providers and outcome only and never dumps the request (pkg/adapter/credredact.go:103-148).
No request-dumping logger exists in pkg/adapter.

FACT (checked, clean): the reclaim hold cannot refuse a mandatory credential control. Every
revoke/rotate/extend/coordination path resolves through the read-only `slotStateLocked` or
`boundSlotState`; the only `ensureSlotStateLocked` callers are slot.go:143, slotcreds.go:26 and
slotsession.go:75. EVIDENCE: `grep -n "ensureSlotStateLocked\|slotStateLocked\|boundSlotState" pkg/adapter/*.go`
This confirms the Settled entry of the same name; re-deriving it costs about ten minutes.

FACT (checked, clean): DOCS-4's two target sentences exist verbatim and
`docs/operator-guide/multi-tenancy.md:72` genuinely lacks the clause, as the deliverable claims.
EVIDENCE: docs/operator-guide/security-principles.md:33, docs/reference/execution-modes.md:68

FACT (checked, clean): the tier-0 inventory claim is accurate. `slotSubjectFileRE` is
`(slot|one_session_only|sole_session)[^/]*_test\.go$` and `inventoryWalkRoots` includes `tests`,
so every new `slot*_test.go` under tests/ must enter `slotAddressCaseFiles`. The files-touched
entry states the general rule, so no per-file omission exists.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1173, :1191, :1213-1229

FACT (checked, clean): `sbe.Leaked = cerr != nil || !cleanly` is fail-closed against an
unrecognized outcome, and the persistent leak ledger it feeds is gateway-side and never pruned
(pkg/gateway/runtime/slothealth/slothealth.go:121-125, :127-136), with the threshold read from
the pool's `maxConcurrentSessions` rather than from any pod self-report
(pkg/gateway/session/recycle/scrubreporter_seams.go:184-197). No security bound in this staging
is sourced from an in-pod self-report that was not already so in the shipped tree.

### [non-spec.7.review-single-source.1]

VERDICT: EMPTY. No rule stated in full at two sites that meets bar (g).

FACT: the round-7 delta is three hunks and all three are reductive or citing-form. Checklist S2
and the summary's SPEC-1 index line both dropped the false "states the two-field precondition
and its `INVALID_ARGUMENT` answer" clause and now read "name[s] the two fields a request
carries, one of `bind_attempt` and `unconditional_teardown`", which matches the staged §4.7 row
(spec-changes.md:277 carriage sentence; :283-284 "this row restates neither"). CONF-1's rule 6
case gained "the category that rule states" — a citing clause, not a value, so it does not
become a second home of rule 6's `CATEGORY_PERMANENT`. The round-5 finding and its twin-site
trap are both closed. — EVIDENCE: implementation-checklist.md:19, summary.md:1122,
non-spec-changes.md:2241-2242.

FACT: the mechanical sentence-repeat sweep (four non-log files, split on `(?<=[.;])\s+`,
whitespace-normalised, >90 chars) now returns NINE repeats and every one is a known benign
class: spec-changes verbatim-anchor/replacement pairs (:271/:278, :1168/:1174), spec-row /
DOCS-2-mirror pairs (:278 vs non-spec:2728, :762+:768 vs non-spec:2746, :1162 vs non-spec:2800),
the two grep-pattern commands (non-spec:3186/:4109), and the two summary-index/checklist-step
pairs (summary:1122/checklist:19, summary:1142/checklist:29). The count and the membership are
unchanged from round 5. Do not re-file any of them.

DECISION: did NOT file CODE-6's `slotGuards` lock-order and lifetime invariants as a second home
in checklist S15 — BECAUSE the four clauses at implementation-checklist.md:55 ("`s.mu` is never
held while a guard is acquired, no path holds two guards, ... a guard entry lives for the life
of the pod's slot identifier") are a compressed restatement of non-spec-changes.md:1826-1852
(*Lock order.* and *Lifetime.*) with the rationale stripped, which is the per-step summary the
checklist format carries for all 25 steps. ALTERNATIVES: filing it would condemn the format
itself, and three earlier checklist-annotation findings (S23's tier list, S22's `Depends-on`,
S16/S17's gate widening) were all refuted as immaterial on exactly that ground.

WATCHOUT: the proto enum comments at non-spec-changes.md:2338-2348 DO state rules 6 and 5 in
full — a reader could implement either from the comment alone, and unlike a docs page the proto
is not barred from citing a section (it cites §4.7 and §7.4 already), so the "licensed reader
restatement" exclusion is weaker here than round 3's entry implies. I still did not file: the
text has survived every round since the token redesign, the enum-value comment is the natural
home for what a wire value means, and I verified it AGREES with the staged rules
(spec-changes.md:1053 rule 5, :1054 rule 6 — status, category and the `ConfigureWorkspace`
exemption all match). Anyone filing this owes an argument that an uncommented enum value is
better, not just that the text is duplicative.

FACT: I re-verified the DOCS-1 `slot_cleanup -> leaked` replacement against the §5.2 disposition
table rather than trusting the round-3 note. They agree: the page's "gateway reads a `leaked`
cleanup-outcome report or a `Shutdown` response that reports no clean exit, or a reclaim it sent
is never answered" (non-spec-changes.md:2650) is the reader-vocabulary form of the table's
`leaked` column plus the paragraph sentence "A slot enters `leaked` on the gateway's reading of
the report or of the response, so a §7.1 reclaim the adapter does not answer enters it"
(spec-changes.md:626). One licensed restatement, no drift.

FACT: §15.4.2 in the shipped spec states NO co-tenancy condition on the graceful-shutdown signal
— `grep -n "DRAINING" spec/15_external-api-surface.md` returns only the state-machine row at
:1702 and the integration-level table at :1780, neither of which mentions a bound co-tenant. So
the staged §4.7 `Shutdown` row (spec-changes.md:277) is the signal condition's only home and
§29.4 step 13's reduction to a citation leaves no second site. Checked so a later round does not
have to.

USEFUL [standing context, "The staged handler has TWO returns that bypass `answerShutdown`"]:
again saved me from filing non-spec-changes.md:506-511 against the code comment at :296-313.

UNVERIFIED (carried from `[non-spec.5.review-single-source.1]`, still not closed): whether
CODE-1's "Doc-comment work on `Shutdown`" bullet list (non-spec-changes.md:539-598) is a second
stating site for the whole-pod recycle scrub rule its own staged body comment already writes out
(:299-313). Both state the rule AND the two-gateway-sender rationale in full, and round 4's fix
had to edit both in lockstep. I declined a second time on the rationale exclusion. The reduction
format already exists in the same list at :549 ("in the terms the code comment above carries").
A future round that wants it must argue past the rationale exclusion first; I have no new
evidence.

### [non-spec.7.review-test-coverage.1]

DECISION: filed exactly one finding, the undispositioned DOCS-2 `DemoteSDK` row in the Testing
section — BECAUSE the Testing section's DOCS-2 block (non-spec-changes.md:3704-3715) dispositions
three of DOCS-2's four edits (Shutdown row substrings, the `**Bind attempt token.**` page-level
assertion, and an explicit "takes no new assertion" with a reason for the `ReportSessionScrub`
row) and is silent on the fourth — ALTERNATIVES: rejected filing (a) the absent test on
`cmd/lenny-gateway/metricsbackfill.go`'s `SlotReclaim` hook wiring, because no hook wiring in that
file is tested today (`metricsbackfill.go:141-161` wires six hooks, no `*_test.go` names the file)
so requiring one is hardening, and the proposal already names the blind spot in CODE-9's text;
(b) the absent assertion on `Retryable: true` in `errSlotBindAttemptSuperseded`
(non-spec-changes.md:1513-1518), because the spec fixes only the category and CONF-1 rules 5 and 6
already assert the category on that same envelope; (c) the tier-2 line on S19/S20 with no tier-2
case named, because the already-refuted S23 verdict settled that a step's tier list means "run
those gates", not "author a file".

FACT: every shipped test the Testing section names for extension exists. Verified by name:
`TestSlotBindErrorReason_spec_5_2`, `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2`,
`TestClassifiedSlotFailureKeepsSetupCommandEnvelope_spec_7_3`, `seedResumingRow` (all
pkg/gateway/sessionserver/), `TestCredentialAndLLMProxyAndSlotMetricsEmit`,
`TestNewMetricsEmittersNilSafe` (gatewaymetrics_elicitation_test.go),
`TestValidTransitions_spec_6_2`, `TestRecyclePathScrubReportedReuses_spec_5_2`,
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`,
`TestAdapterEventsEmitsControlEvents_spec_4_7`, `TestShutdownMessagePostRemovalDescriptor_spec_4_1`,
`TestMetricCatalogIsCompleteAgainstSpec161`, `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`,
`TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3`. A later lens need not re-check this set.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311

FACT: `docs/reference/adapter-contract.md`'s `DemoteSDK` row is gated by nothing today.
`grep -n "DemoteSDK" tests/tier11_docs/*.go` returns no hit at all, so the row DOCS-2 amends
enters the page ungated unless the Testing section says to gate it.
EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:302-307 (the only
`requireAllContain` over that page's RPC rows, and it reads the `Shutdown` row alone)

FACT: the only proposal delta since the r5 snapshot is CONF-1's rule 6 case gaining "the category"
(non-spec-changes.md:2241-2242), plus the S2/SPEC-1 index rewording in the checklist and summary.
The Testing section itself is byte-identical to the snapshot, so a lens re-reading it is re-reading
text that has already survived several rounds.
EVIDENCE: `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-r5-prefix proposals/0081_*/`

USEFUL [prune.3.fix.1 / Standing context]: the Settled line "the metric-gate topology, settled and
re-derived by six shards" saved a full re-derivation of why the adapter counter takes no
`catalog.go` row; CODE-9's text (non-spec-changes.md:2076-2091) matches it exactly.

### [redesign.7.fix.1]

DECISION: restaged the comment-carrier reduction as ONE mechanism under a new non-spec heading
`### Comment-carrier reduction: shared invariants`, placed directly before CODE-10, with CODE-10
(SPEC-3's reporting universal), CODE-11 (SPEC-5's `SETUP_COMMAND_FAILED` cause) and CODE-12
(SPEC-4's claim-deletion keying) reduced to sub-blocks that each carry only a carrier definition,
a grep command or site list, and an arm rule — BECAUSE the round-7 introspection found the
invariant block triplicated across the three deliverables, the files-touched bullets copied
verbatim, and CODE-12's non-carrier criterion narrower than its own command, so an implementor
would file deviations for adapter-side hits the deliverable never meant to catch — ALTERNATIVES:
prune alone (rejected: it deletes the duplicate sentences but adds no general non-carrier arm to
CODE-12 and leaves the class open, so the next sweep discovers the next member and copies the
form a fourth time); healthy rounds (rejected: two more sweeps to discover what the class already
names); merging the three into one deliverable id (rejected: S24, S25 and S26 have different
`Depends on` lines and different tiers, and the summary's 0079 impact row cites the three ids).
The shared block states, once: the invariants (no `// spec:` annotation loses a section number;
no assertion moves and no case or assertion is added; a touched tier-11 file keeps every substring
and check; `tests/spec-map.json` and `tests/claim-map.json` take no edit; no pointer or rationale
sentence is added to a comment that carries none; a landed migration's comment is edited in place;
the `podspec.go` and `gatewaylink.go` claim-map line surfaces are re-checked after reflow), the
general non-carrier arm (a hit that neither states the retired proposition nor restates it in
other words stays true, is not a carrier, and takes no edit; only a hit that states the retired
proposition and fits no arm is recorded in `deviations.md`), the routing of hits SPEC-3's carrier
table assigns elsewhere, the closure rule, the comment-prose-only file rule, and the sweep record
below. The Testing section's three "no file" lines collapsed into one, and the three files-touched
bullets into one citing the block. Checklist S24–S26 each cite the block and lost the restated
invariants and the duplicated step-id sentence. The summary index entries for the three are
predicate descriptions consistent with the sub-blocks; CODE-11's names its four sites as the set.

FACT: every command in the three sub-blocks runs from the repository root and returns the sites
its sub-block describes. CODE-10's grep returns 46 lines (the Settled entry says 45 and a later round counted 47; the
tree moves and the set is the command's output, so none of the three counts is load-bearing),
including `migrations/0167_...up.sql:102`, `pkg/adapter/server.go:169`, the two `*.pb.go` copies
and the three tier-11 files. CODE-12's perl command returns 20 sites, among them
`pkg/gateway/podlifecycle/podsession/binder.go:1187`, `pkg/podregistry/crd.go:249` and
`pkg/adapter/resume.go:19`; under the general non-carrier arm, `pkg/adapter/session.go:134`,
`pkg/adapter/resume.go:19` and `pkg/adapter/podscrub_test.go:230` are adapter-side statements
about the adapter's own pod state rather than WarmPoolController projections, so they take no
edit and no deviation, and the five `reserved → idle` hold-expiry comments stay non-carriers
under the same arm without a criterion of their own. CODE-11's four sites exist by name:
`errorclassify.go:465-476`, `start.go:218` (`writeSetupCommandError`), `start.go:3622-3648`
(`isTransientPodClaimError`), `resume_setup_demotion_internal_test.go:39-48`.
EVIDENCE: the three commands run on 2026-09-21 at the working tree of `c4bf83811`.

FACT: the one-time sweep across the other retired statements, over `pkg/ cmd/ tests/ migrations/
schemas/ docs/`, found no comment carriers. SPEC-1 (`per-session teardown`, `holds a bound
entry`, `bound entry for the named`, `selected by a field`, `field's presence`, `presence standing
in for`): 12 hits, every one naming the teardown as an operation or a proto field's wire presence
(`tests/tier10_conformance/recycle_scrub_conformance_test.go:249` and
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:288` were read in full and
state the recycle disposition riding beside the teardown, which SPEC-1 keeps as precedent), none
stating that an entry's presence selects the teardown. SPEC-2 (`half-claimed`, `no live
workspace`, `nothing to seal`, `no runtime (was|has been) started`, `not yet reached attached`):
3 hits, all sealer statements about a session the replica holds no binding for
(`pkg/gateway/sessionserver/seal.go:95`, `pkg/gateway/checkpoint/checkpointer/checkpointer.go:383`,
`checkpointer_test.go:237`); a joined-comment pass for `replacement pod` beside any of those
phrases returned nothing. SPEC-6 adds rows only and retires no statement; its two series names
occur 0 times in the tree, which is CODE-9's to add. The record is in the shared block, one line
per statement, so no later pass re-derives it.

WATCHOUT: the shared block is the ONLY home for the invariants and for the non-carrier arm. A
sub-block that restates either is a duplicate to remove, and a new comment-reduction deliverable
for a later retired statement is a fourth sub-block under that heading, in the same three-part
form (carrier definition, command or site list, arm rule), rather than a copy of CODE-10. A
finding shaped "CODE-n lacks the invariant X" is filed against text that lives in the shared block
by design.

CORRECTS Standing context "How to use" (the line "CODE-10 and CODE-11 as PREDICATE-DEFINED
comment-reduction deliverables that enumerate no files anywhere") and the Settled DECISION from
`[prune.5.fix.1]` ("CODE-10 and CODE-11 are PREDICATE-DEFINED and enumerate no carrier files
anywhere"): CODE-11 names four sites by design and always did; CODE-10 and CODE-12 are the
predicate-defined pair. All three are now sub-blocks of the shared block.

CORRECTS Settled "The report predicate has ONE home ... Adding a §5.2 pointer to a comment that
carries none breaks CODE-10's own invariant": the invariant is the shared block's, stated once
for all three sub-blocks.

CORRECTS `[non-spec.7.fix.1]` WATCHOUT "the deliverable's done-condition is the command's output
under the carrier definition and the non-carrier criterion": CODE-12 carries no non-carrier
criterion of its own; the done-condition is the shared block's closure rule under its general
non-carrier arm.

CORRECTS `[non-spec.7.fix-G1.1]` WATCHOUT "CODE-10 explicitly excludes the two tier-11 files under
S4's sweep": the exclusion is the shared block's routing sentence in its non-carrier arm, and it
still holds for CODE-10's hits.

CORRECTS Open "Should checklist S24 be reduced to CODE-10's predicate form?": S24 now cites the
sub-block and the shared block and states no carrier category; the entry is closed.
