# Review log — 0076_fix_scope-the-coordination-generation-to-the-session

## Standing context

**Compaction pass 17, 2026-09-02.** Aged out non-spec rounds 5 and 6, whose sixteen ledger entries are retired; spec rounds 1, 2, and 3
keep their ledger text. Lifted into `### Settled`: the closed blast radius of the barrier-gate move with the decision that CODE-1 owns it
whole, the deregistration-order rule for a mid-flight case, the `-race`-at-tier-1 fact with the tier-placement discriminator it produced,
the op lock as the true serialiser of co-tenant checkpoints, the landed cases that already pin what §8 might be thought to owe, migration
0181's environment, deploy-time ordering, the pre-existing prestop acked-but-uncaptured gap, the fence half's non-clean `InvalidArgument`,
the colliding barrier ids, and the absence of any 0-sentinel or stated initial value. Lifted into `### Traps`: §8's scoped preamble, the
`StaleRPCRejected` fixture hazard, the accepted-barrier-ack mistake, the D5 co-tenant-freeze mistake, the thrice-refuted tier-list
bookkeeping class, the incomplete disjointness enumeration, and the two-directory migration-test hazard. Applied the two `CORRECTS` that
pass 16 recorded as applied and were not: the §28.8 bijection gate is `tests/tier0_static/matrix_completeness_test.go`, a tier-0 test, so
the derived-inventories line no longer attributes it to `tests/tier11_docs/` and now states what the tier-11 tests do read. Deleted two
superseded items rather than keeping them: the feasibility WATCHOUT about the S3/S5 split of `barrierGate`, whose trap the same round's fix
dissolved, and the claim that no tier-4 test references `CheckpointBarrier`, which a later round corrected. The sixteen unclosed `OPEN` and
`UNVERIFIED` items of both rounds moved whole into a new ledger residue entry, `[non-spec.5-6.*]`; `### Open` still names them by their
originating ids and says where the detail now lives. `### Deferred` gained the three unclosed `DEFERRED` items spec round 3 filed, kept
whole, and the standing OPEN one of them supersedes was dropped. `### Open` gained the two items spec rounds 2 and 3 opened.
**The target of 540 lines was not reached, and the section grew: 665 lines against pass 16's 507, at the same line width.** `### Settled`
is 338 lines, `### Traps` 187, `### Open` 78, `### Deferred` 35. The growth is the two rounds' residue, which is the largest body of
derived work this proposal has aged out at once, and the three `DEFERRED` items rule 6 requires whole. Reaching 540 means dropping trap
bodies, summarising a `DEFERRED` a later pass has to apply, or cutting the derived inventories, and all three are barred while their
subjects stand. The reducible items and their conditions are unchanged from pass 16: the derived inventories once the code and test lanes
land, the carrier enumeration once SCHEMA-1's target list is settled, the §28/§29 edit-site line once SPEC-2 is applied, the tier-0-gates
line once SCHEMA-1 and migration 0181 have landed with their stubs and their registry row, the S3/S4 split line once both steps are
committed, the three `DEFERRED` items once the non-spec loop applies them, and about fifty `### Open` lines that close as a batch when the
human-review pass runs. A pass that lands the human review should expect to reach the target without dropping anything.

### Settled

- **Counter baseline.** The session row's counter is baselined at 1: the row is carried unchanged and §10.1.2 step 1 is untouched, so the
  first takeover mints 2. CODE-4 carries migration 0181, both session-store `Create` floors, and the deletion of `coordfence`'s zero floor.
  The counter has three writers (step 1's compare-and-swap, `Sweeper.RecordHandoff`, `bumpCoordinationGenerationOnSnapshotClose`), so any
  floor repeats on each, and `Create` inserts the value explicitly, so a column default baselines nothing. The §7.2 snapshot-close bump fires
  only under a terminal write, after which no takeover follows.
- **The `>= 1` CHECK has one write path to guard.** Both stores' `Update` re-read under lock and clamp `CoordinationGeneration` to the
  previous value (`pgstore.go:460`, `:475-477`; `memstore.go:129`, `:144-146`); `pgstore.go:170` is the only `INSERT INTO sessions` in the
  tree and every raw-SQL fixture omits the column; `RecordHandoff`'s 0-return sentinel survives because 0 stays impossible as a row value.
  This is what makes CODE-4's "both `Create` floors plus the migration in one commit" the complete edit set. USEFUL: a round-5 lens credits
  it with saving the writer enumeration.
- **The barrier's cache fallback puts a literal 0 on the wire and must not be floored.** `httpsurface.go` seeds the target's generation at 0
  and overwrites it only on a successful session-row read, so under a Postgres fault the barrier carries 0 and is refused with
  `InvalidArgument` whatever the baseline is; the staged "unreachable by construction" claim is not exact, though the outcome is fail-closed.
  The fence path is not symmetric, its reader returning an error rather than 0, so deleting `coordfence`'s floor is safe.
- **D7: the barrier is accepted for a bound session the pod holds no fenced generation for.** CODE-2's gate becomes
  `initialized && gen != fenced` read from the slot entry, and `checkSessionBound` runs before it, so acceptance-when-unset never reaches an
  unbound session. The barrier carries 1 rather than 0, clears the adapter's non-positive `InvalidArgument` guard, and is accepted on the
  unset arm. Anything in the ledger reading the unset case as a refusal predates D7. Round 6 declined to file the §10.1.8 step-1 imprecision
  (the shipped "either outcome is safe" now covers the acceptance arm, where a superseded draining replica quiesces a session it no longer
  coordinates for up to the 90s ack deadline), so it stands for the human-review pass.
- **D6: the pod holds `last_fenced_generation` per bound session,** unset until that session's first accepted fence there, both predicates
  applying only against a recorded value. The exemption is a separate `initialized` bool on `coordinationState`, read by the stale rejection
  and by the gap test. The adapter performs no cancel-and-reset, so staged text may re-scope that requirement and may not assert the adapter
  meets it. `coordinationState` also carries `quiesced`, which has no production reader; the object carrying the quiescence is `barrierGate`,
  which CODE-1 moves onto the slot entry with the state, because §10.1.8 step 3 already fixes the barrier's unit at the session and a pod-wide
  single-slot gate cross-links two co-tenant barriers and blocks the loser to the 90s ack deadline.
- **The gap reset and the record-and-reject rule have four spec mirrors and seven proto carriers.** `spec/28` and `spec/29` restate clauses
  (a) through (d) citing §10.1, and SPEC-2 stages both. The proto carriers, all SCHEMA-1 sites: the `CoordinatorFence` RPC comment (which
  also spells the exemption per pod lifetime), the `CoordinatorFenceRequest` and `CoordinatorFenceResponse` message comments, the request's
  field comment, the `CheckpointBarrier` RPC comment, `CheckpointBarrierRequest`, and its field comment. There is no eighth carrier of this
  rule: `CoordinatorFenceResponse.last_fenced_generation` (`proto:1465`) carries no leading comment, and the `Checkpoint` RPC comment and
  `CheckpointStart.checkpoint_id` are session-neutral and stay true under the per-entry gate. SCHEMA-1's target list is wider than these
  seven: it is these seven plus the twelve operational-RPC `coordination_generation` field comments the next bullet names, which SPEC-2's
  closing paragraph brings in, and within the seven the `CoordinatorFenceRequest` field comment keeps its wording. A grep for
  `fenced|older generation|handoff_stale|lifetime` over `schemas/lenny-adapter.proto` returns hits only at `:161-162`, `:167`, `:1444-1446`, `:1457-1460`, `:1465`,
  `:1472-1473`, and `:1479`, all inside those seven.
- **The other twelve `coordination_generation` field comments are session-scoped but not neutral.** Eleven close with "so a replica that has
  lost coordination cannot drive the pod (§10.1)" and `ShutdownRequest`'s with "cannot tear the session down", which SPEC-1's staged unset
  clause falsifies for exactly the session class D6 makes ordinary, so SPEC-2's ground for excluding them is false.
- **D5: the coordinator-loss hold has no per-session arm and cannot be given one here.** Its sole arm is the close of the pod's single
  CH-ADAPTEREVENTS stream, which names no session, and §10.1.4 fixes the same posture. D5 keeps both whole-pod sentences, drops the
  generation from the pod-level arming line, and has each terminated session's `coordinator_lost` line and post-mortem read its own entry,
  reporting `0` where no coordinator fenced it there. Zero is impossible as a fenced value, so the sentinel costs no wire, JSON, or state
  change, and those records already carry `last_generation` and `started_sessions`. The code's hold allowlist is wider than §10.1.4's "only
  `CoordinatorFence`", which is pre-existing drift rather than this proposal's defect.
- **A barrier's generation comes from shared state, never from the sending replica.** The healthy path reads the `coordination_lease` mirror
  row and the cache fallback reads the live session row, and the value is fixed once, when the barrier-target set is assembled, so a
  superseded replica's barrier can carry the value the pod holds, which the pod accepts, and can carry a value the pod does not hold, which
  it refuses. Both outcomes are reachable and neither is asserted. Do not state a closed enumeration of the values it can carry: the mirror
  can lag below the pod's value, the live-row read sits one above it between a successor's compare-and-swap and its fence, and the fallback's
  zero seed puts a value on the wire that no fence ever installed. The mirror is written from the sweep's pre-`RecordHandoff` snapshot, so
  after a takeover it carries G while the pod is fenced at G+1 for a whole sweep interval rather than a race, which is a code defect and
  falsifies the staged rationale "there is no second value to keep in agreement with the row". The existence of the accepting case is what
  makes §28.8's `CH-BARRIER` cell an edit site and the parallel `CH-CHECKPOINT` cell one too.
- **§4's "either order" claim is false and routes to human review.** §4's second design bullet claims equality "catches a superseded sender
  whenever the assembly and the successor's fence straddle each other in either order", which fails in the fence-then-assembly direction: the
  assembly reads G+1, the pod holds G+1, and the barrier is accepted. It contradicts SPEC-2's own §28.8 `CH-BARRIER` rationale, lands nowhere
  in `spec/`, and is the premise §7's first open decision hands the human reviewer.
- **The `spec/28` and `spec/29` edit sites are settled by one membership criterion.** A sentence is an edit site when it states what the pod
  does with an RPC's generation and fixes the value that outcome is measured against, and is not one when it states the exclusivity
  constraint and its guard, a duty on the sending replica, the hold, or the pod's validation without fixing the compared value; a sentence
  doing both falls under the site arm, which is why the `CH-FENCE` Exclusivity bullet is staged while §28.5.1's `CH-CHECKPOINT` and
  `CH-BARRIER` Exclusivity bullets stay non-sites. §29.8 step 9 is the `spec/29` mirror of the window clause by the specification's own
  cross-reference, so it moves into the value form alongside its three `spec/28` twins; round 4's rejection of §29.8's Preconditions
  paragraph as unit-neutral does not reach step 9. §28.6's second opener spans four channels whose gates differ, so it takes one clause per
  gate. That sentence, §28.5.1's `CH-FENCE` Exclusivity bullet, and the §28.8 `CH-FENCE` window cell are the other three sites carrying the
  owner-phrased window clause. The neighbouring "One holder per session" sentence keeps "older than" where the second opener says "other
  than"; that asymmetry is shipped text rather than a defect.
- **§10.1.2 step 3 fixes equality as the operational-RPC gate, and step 2's two halves are load-bearing.** "The pod accepts only RPCs whose
  generation matches the fenced value" is the only acceptance-gate sentence in `spec/` or `docs/`, so loosening the barrier to "at least the
  last fenced generation" is barred by spec text; SPEC-1 changes only the unit of the compared value and the unset case, and §7's first open
  decision owns the operator. Step 2's bar, that the new coordinator send no operational RPC until `CoordinatorFence` is acknowledged, is an
  obligation on the acquiring coordinator, and the acceptance window that follows covers the generation the pod already holds. USEFUL: five
  rounds credit this with keeping a fix off the operator and making the stamp collision visible.
- **`CoordinatorFence` has two senders and nothing fences a normally-started session.** The resume path and the sweeper's crash-takeover
  re-adopt drive the same `coordfence.Fencer`, and `.Fence(` outside tests returns those two alone; `coordfixture.FenceReadopter` calls the
  `adapterclient` RPC directly. `coordfence`'s own tests do construct a `Fencer` over fake generation readers (`coordfence_test.go:164`,
  `:178`), which is why a tier-2 construction of the resume-then-takeover flow is not impossible. The resume path fences without bumping the
  row, so a resumed session's first crash takeover is delayed one sweep cycle rather than blocked, at the cost of a spurious stale-handoff
  and fence-relinquished metric pair. §10.1.2's sequence triggers on lease acquisition, which for a fresh session happens before any pod
  exists, so "no fenced generation" is the ordinary case in the specification too.
- **`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches no gateway decision.** `adapterclient` copies it into
  `CoordinatorFenceResult` (`coordinatorfence.go:29`, `:60`) and nothing outside tests reads it: `fence()` branches on `res.Accepted` alone
  and on a rejection re-reads the authoritative Postgres value (`coordfence.go:159-179`). USEFUL: a round-5 security lens credits this with
  saving the whole trust-boundary re-derivation, so it stands until the code lane lands.
- **Derived inventories. Do not re-derive any of these.** Every anchor SPEC-1 and SPEC-2 quote resolves verbatim and uniquely, re-checked in
  rounds 4, 5, and 6, as does every code citation in CODE-1 through CODE-4, §8, and §9, re-resolved five times across the non-spec rounds.
  §10.1's no-window claim occurs once in `spec/`, `docs/`, and `schemas/`. The surface outside `spec/10`, `spec/28`, and `spec/29` is five
  `spec/04` sentences plus unit-neutral lines in `spec/07`, `spec/12`, `spec/16`, and `spec/18`, and `spec/18` puts the fence and the
  compare-and-swap in Phase 4 and `CheckpointBarrier` in Phase 8, so there is no phase inversion. The §28.8 matrix enforces one row per
  §28.3 identifier in both directions, so editing a cell is safe and deleting or splitting a row is not. That gate is
  `tests/tier0_static/matrix_completeness_test.go:16-33`, a tier-0 test, and `tests/tier11_docs/` has no §28.8 row gate, so the staged
  §28.8 `CH-FENCE` bullet's "a tier-0 gate reads that correspondence in both directions" is right where two earlier passes of this log
  were wrong; nothing is at risk either way, because SPEC-2 edits cells and adds or removes no row. The tier-11 tests that read `spec/28`
  and `spec/29` read surfaces SPEC-2 does not touch: `spec/README.md` anchor rows, the §28.2 and §28.3 tables with byte-exact §12.6,
  §4.6.3, and §7.2 sentences, proposal prose, the §29.3 off-holder matrix, and §5.2 with §6.2. Nothing reads a §28.8 cell body, §29.10's
  lists, §10.1.2, or §10.1.4's Observability bullet, so checklist S1's tier 11 is precautionary rather than load-bearing. §29.10's
  successor-pointer gate is satisfied by the opening paragraph's `CH-` link, and the removed §29.10 "does not state" bullet has no inbound
  reference outside the `spec/README` contents and asks two questions the staged edits answer. Every proto message declaring
  `coordination_generation` is session-addressed, `ShutdownRequest` included, so step 3's "the session the RPC names" resolves on every
  carrier. Two things look like data-loss leads and are not: `session_checkpoint_meta` is a different table from the `checkpoint_manifest`
  partial-manifest rows §10.1 describes, and the four other `coordination_generation` columns are always written explicitly from the session
  row, so leaving their defaults at 0 is cosmetic. The `docs/` surface is eight sites and states no unit, baseline, or gate:
  `adapter-contract.md:68`, `:69`, `:96`; `metrics.md:307`, `:309-312`; `concepts.md:101`; `architecture.md:173`;
  `operator-guide/upgrades.md:47-54`. It has been re-derived eight times, from the docs, security, client, and operational sides, and nothing
  in it, in `charts/`, in `sdks/`, or in §16 is made wrong by a per-session generation, by D5, D6, D7, or the row baseline; no alert,
  runbook, or tier-11 test is reached. `sdks/`, `schemas/README.md`, and `schemas/examples/` mention `coordination_generation` nowhere at
  all. Its two misattributions of who sends the barrier and who drives the capture, and `adapter-contract.md:79`'s per-session op lock
  against the spec's pod-level one, are pre-existing drift for a docs loop. Landed migrations are not edit sites either: `0164`'s column
  comment states the barrier's match rule D7 narrows, and `0050`'s justifies the zero default CODE-4 falsifies. Known sub-line citation
  drifts that must not be filed: `slotsession.go` cited `:283-285` and `:282-285` for one struct declared at `:282`;
  `coordination_test.go` cited `:184-197` and `:185-197` for one test whose doc comment opens at `:185`; `coordination.go:408` cited for a
  backoff call at `:415` in the same block.
- **No claim-map row moves, and the reason is the generator's source document.** `scripts/seed-claim-register.py` parses exactly one
  document, root `gateway-runtime-comms.md` §7.1, and `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs the generator and diffs
  bytes, so SPEC-2's `spec/28` and `spec/29` cell edits cannot change a register row; `claim_register_test.go` is separately a schema gate.
  Two rows carry three code-line citations into `pkg/adapter/coordination.go` that CODE-1 and CODE-2 will shift (`:81-82` on the R16
  `ABSENT` cancellation row, `:85` and `:212` on the `CoordinatorFence` row, at `tests/claim-map.json:174-178` and `:461-465`), and
  nothing resolves them: `claim_register_test.go` rejects only a surface that is nothing but a bare line number, and
  `claim_register_proto_agreement_test.go` joins the register to the proto rather than to a line — EVIDENCE:
  scripts/seed-claim-register.py:11-13, :38-39; tests/tier0_static/claim_register_generator_test.go:20-31, :45;
  tests/tier0_static/claim_register_test.go:29-46, :145-157.
- **No tier-0 or tier-11 gate reads the sentences SPEC-1 and SPEC-2 rewrite.** `spec_28_index_rows_test.go` and `successor_pointer_test.go`
  read headings and anchors; `per_slot_substate_scope_doc_reconciliation_test.go` cites §29.10 only in its `// spec:` annotation and asserts
  over `spec/06` and `docs/reference/state-machines.md`; `tests/spec-map.json` and `tests/spec-anchor-moves.json` key on headings, which this
  change does not move. `docs/reference/adapter-contract.md` is hand-authored rather than generated from the proto, and its two fence and
  barrier rows (`:68`, `:69`) are unit-neutral.
- **Two tier-0 gates the deliverable must satisfy.** `TestProtoStubsMatchGeneratedOutput` reproduces `make generate-proto` and diffs the
  whole `pkg/proto` tree, so any SCHEMA-1 comment edit needs `lenny-adapter.pb.go` (field and message comments) and
  `lenny-adapter_grpc.pb.go` (the two RPC comments, twice each, at `:180` and `:632`) regenerated in the same commit; the gate self-reports
  "unverified" when `buf` or the plugins are absent, so a green local tier 0 is not evidence the regeneration is unnecessary.
  `scripts/lint-migrations.sh` pass 3 greps `tests/tier2_component/migrations/` alone for a migration's number, and `prodMigrationSchema` in
  that same file drives the per-step rollback walk, so migration 0181 owes both a behaviour file in that directory and a table row even
  though it adds no column; 0180 and 0112 are the precedents for a column-less migration keeping a row. Lint passes 4 and 5 key on
  `add column` and `drop column` and do not reach 0181, which drops a constraint.
- **A `prodMigrationSchema` row with no `columns` and `create:false` is inert.** `TestProdMigrationsApplyExpectedSchema` iterates `m.columns`
  (none) and `TestProdMigrationsRollBackPerStep` `continue`s on `create` then iterates `m.columns` (none), so the 0181 row exists only so the
  rollback walk steps its `.down.sql` and `lint-migrations.sh` pass 3 finds the number. Naming `sessions` rather than `coordination_lease`
  costs nothing — EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:588-603, :610-634; scripts/lint-migrations.sh:73-88.
- **The accessor blast radius is exactly what §9 lists.** `Server.LastFencedGeneration()` has three callers (`pkg/adapter/holdstate.go:119`,
  `pkg/adapter/coordination_test.go:73`, `tests/testinfra/coordfixture/coordfixture.go:115`); `Pod.LastFenced()` is read only in
  `tests/tier4_integration/coordination_fence_split_brain_test.go`, `tests/tier8_chaos/coordination_crash_takeover_test.go`, and
  `tests/tier7a_load_local/coordination_colocation_race_test.go`; `BarrierWaiting()` adds only
  `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163`; `isQuiescedForBarrier()` adds nothing. `pkg/adapter/export_test.go`
  exports none of them.
- **The whole carrier surface for the pod-level hold log line is two lines,** `spec/10_gateway-internals.md:60` and
  `spec/29_communication-scenarios.md:1274`, and SPEC-1 and SPEC-2 stage both. The post-mortem record's `lastGeneration` JSON key appears in
  no schema, doc, or chart, so dropping the pod-level `last_generation` slog key reaches no other carrier — EVIDENCE:
  pkg/adapter/holdstate.go:129-132 (the emit), :283-296 (the record).
- **A refused barrier costs a duplicate capture rather than a lost checkpoint.** `dispatchOne` starts the gateway-driven `Checkpoint` stream
  before `dispatch.Send` and joins it after, so the checkpoint still runs and the stream finalises the manifest row. What is lost is the
  quiescence and the acked-barrier record, after which prestop checkpoints every session whose `barrierAcked` entry is false. The cost is
  the same for `InvalidArgument` and `FailedPrecondition`, since only the latter maps to `ErrGenerationStale` and prestop branches on the
  acknowledgement alone. `quiesced_ms` is never persisted, so naming it among the records a rejected barrier loses is wrong, and
  `lenny_coordinator_handoff_stale_total` increments only on the fence path. Quiescence cannot wedge, being cleared in a deferred close and
  bounded by the 90s ack deadline.
- **The adapter hold's edit set is closed, and it reads a per-session generation off a pointer that outlives the registry entry.**
  `holdState.gen` is written once at `pkg/adapter/holdstate.go:128` from the accessor read at `:119`, read once at `:187`, passed to
  `terminateHeldSession` (`:206`, declared `:225`) and on to `writeHoldPostMortem` (`:283`), and logged as `last_generation` at `:132` and
  `:228`. CODE-3's enumeration is now complete, naming `:43`, `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, and `:283`; do not re-file
  the omission. `heldSession{sessionID, state *slotState}` carries the entry pointer and `deregisterSlotLocked` deletes the map key and
  returns the pointer with no field zeroed, so the post-mortem reads each terminated session's own value after pass 1 removed it. The
  post-mortem's `LastGeneration` JSON key carries no `omitempty`, so the D5/D6 unset sentinel serialises as an explicit `0` and a test
  asserts it with no schema change. `onHoldTimeout` releases `hold.mu` before both passes, so the only nested pair stays `coord.mu` then
  `hold.mu` and CODE-3 closes no cycle; the comment at `pkg/adapter/coordination.go:126-128` ("the hold timeout never reaches back into
  coord.mu") becomes false in letter and is rewritten in the same pass. `pkg/adapter/holdstate_test.go:713` is the file's only generation
  assertion, inside `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` (declared `:674`), and its slog-capture pattern is
  process-global, so the case cannot call `t.Parallel()`.
- **Guard ordering is what the adapter deliverables rely on.** `checkpointRootsForSession` (`pkg/adapter/slot.go:153-166`) fails the
  `Checkpoint` stream with `FailedPrecondition` at `pkg/adapter/checkpoint.go:94` when the session has no bound slot entry, and it runs
  before the barrier link at `:122`; `CoordinatorFence` and `CheckpointBarrier` both run `checkSessionBound` at `:89` and `:216` before
  reading any generation. A guard that exists says nothing about whether it precedes the site that needs it, so verify the order.
  `checkpointRootsForSession` therefore returns the resolved `*slotState` alongside the roots, and its only other caller is
  `pkg/adapter/resume.go:178`.
- **The S3/S4 split turns on a closed two-file class.** Exactly two files take both a CODE-1 accessor edit and a CODE-4 baseline shift:
  `tests/tier8_chaos/coordination_crash_takeover_test.go` and `tests/tier7a_load_local/coordination_colocation_race_test.go`. The third
  `pod.LastFenced` caller, `tests/tier4_integration/coordination_fence_split_brain_test.go`, seeds `CoordinationGeneration: 1` at `:72`, so
  CODE-4 does not reach it. The split is safe in both members because the accessor reads and the shifting assertions sit in different
  subtests: the tier-8 reads at `:150`, `:195`, and `:223` are in subtests seeded 1 at `:118` and `:179`, while the 1, 1, 2 assertions at
  `:267`, `:283`, and `:296` belong to the third subtest, whose seed leaves the field unset (`:239-241`) and which makes no `LastFenced`
  call. CODE-4 reaches tier 7a through the colocation race test's explicit `CoordinationGeneration: 0` seed via `memstore.Create` (`:144`)
  and its assertion at `:287-288`, and S4, §8's CODE-4 tier line, and the per-deliverable line all carry 7a. §9 is a flat list with no
  per-deliverable attribution, so a split needs no §9 change.
- **§8's class-1 inventory is complete,** and every other assertion site in the tree seeds the field explicitly at or above 1:
  `pkg/gateway/sessionserver/coordination_fence_test.go` (4, 9, 14, 10), `pkg/gateway/sessionserver/failure_test.go:360,:394,:426` (7, 1,
  11), `tests/tier4_integration/checkpoint_intent_generation_test.go` (10, 5, 3, 7),
  `tests/tier4_integration/eviction_fallback_outage_test.go:159,:193` (2), and every `barrier.Target{...CoordinationGeneration: n}` literal
  under `pkg/gateway/coordination/barrier/`. The `INSERT INTO sessions` raw-SQL fixtures all omit the column.
- **Two landed memstore `Update` tests sit inside §8's class 1 and are named nowhere.** `TestUpdateAdvancesGenerationCounters` creates unset
  and asserts 2, becoming 3 (`pkg/gateway/session/sessionstore/memstore/memstore_test.go:416`, `:430`), and
  `TestUpdateConcurrentGenerationBumpsPreserveMonotonicity` creates unset, runs 50 concurrent increments, and asserts 50, becoming 51
  (`:471`, `:490`). Two lenses declined to file them, because the class sentence covers them, its "each shifts by one" rule gives the fix,
  and `memstore_test.go` is already in §9; the closed enumeration beside the class names only `TestCreateDefaultsSessionRecordFields`. Hand
  them to the implementor as an addendum to that enumeration rather than filing them.
- **The baseline-shift and spec-phrase sweeps are complete against the tree. Do not re-derive any of them.** Class 2's exhaustiveness claim
  holds over every `CoordinationGeneration:` literal in a `_test.go` file: the two tier-4 checkpoint-intent fixtures set the row by `Update`
  (`checkpoint_intent_generation_test.go:45-47`, `:117-119`), and the barrier fixtures build `barrier.Target` literals against a fake
  dispatcher with no session store (`barrier/concurrent_drain_test.go:79`, `tier7a_load_local/prestop_no_double_checkpoint_test.go:121`).
  Everything class 1 omits is seeded at or above 1: the tier-4 split-brain file at `:72`, tier-8 subtests 1 and 2 at `:118` and `:179`,
  `failure_test.go` and `coordination_fence_test.go` at 5, 7, 9, and 14, and the eviction pairs at 3 and 2. On the spec side, `prior
  coordinator` returns the four window-clause sites SPEC-2 stages plus two non-sites (`spec/10:38`, already in the value form, and
  `spec/29:1328`, a sender-side duty), `coordinator_connection_lost` occurs in `spec/` only at `spec/10:60` and `spec/29:1274`, both staged,
  and no tier-0 or tier-11 gate pins any sentence SPEC-2 rewrites. The `>= 0` to `>= 1` swap has no doc or fixture mirror: there is no
  golden schema dump, no constraint-name assertion, and no `sessions` DDL in `spec/12`, which names the column once at `:160`.
- **The staged tier-3 case is buildable where the landed tier-3 suites are not.** `adapter_checkpointbarrier` and `adapter_generation_fence`
  are descriptor-and-wire-form only and assert no adapter outcome, so the staged acceptance case is new behavioural work rather than an
  amendment, and `adapter.New(` appears in six `tests/tier3_contract` files, so the tier does host behavioural adapter cases.
- **`coordfixture` is in the untagged tree; its consumers are not.** `tests/testinfra/coordfixture/coordfixture.go` carries no `//go:build`
  line, so tier 0's `go vet ./...` compiles it and catches a break inside it. What tier 0 misses is the three tagged consumer trees
  (`tests/tier4_integration`, `tests/tier7a_load_local`, `tests/tier8_chaos`), because it vets the untagged tree plus
  `-tags=contract ./tests/tier3_contract/...` and nothing else, so §8 says to run the tagged vet after CODE-1. A signature change on
  `Server.LastFencedGeneration` breaks nothing visible until those tiers run.
- **Every confirmed finding since round 1 sits in the D7, counter-baseline, and barrier-provenance cluster.** The per-session move (D1
  through D6), which is what the problem statement evidences, has drawn none since round 1, and rounds 2 through 5 each produced one
  finding, every one created by the previous round's own fix. The non-spec lane has since run five times and landed CODE-4, the CODE-1/CODE-3
  step merge, the CODE-1/CODE-2 re-split over the registry-entry move, the tier-4 fence attribution, the proto stubs, the migration test
  registry, the S3/S4 tier-8 split, and the mid-flight deregistration case; what it left is in the residue entry.
- **§7's remaining decisions are deliberately open for the human reviewer**: whether the barrier gate stays equality, whether a fence for an
  unheld session is a rejection or a retryable race, and whether `coord.mu` becomes per-entry. D7 removed the fourth. Cite them by text,
  because D5 and D7 renumbered them. Round 5's falsification lens added a third option to the first decision that the menu does not carry:
  delete the barrier's generation gate outright. Its argument is that the gate as shipped refuses the two legitimate cases (a
  never-taken-over session's 0, and a just-taken-over session's lagging mirror) and admits the superseded replica it is documented to catch,
  and that deleting it dissolves D7, the provenance reconciliation, and the counter baseline together. It is not forced, because repairing
  `upsertMirror` and baselining the counter leaves a working gate, and it contradicts six shipped statements. Route it to the human, along
  with §4's "either order" claim and the three known-imprecise rationale sentences the residue entry carries. CODE-1's move of
  `coordinationState` carries its embedded `mu` onto the entry, settling the third decision by construction while §4 still frames it as open.
- **A converged proposal retires its draft headers.** Only 0075 and 0076, both unconverged, carry `**IMPLEMENTOR TO FILL THE BLANKS.**`; the
  landed 0073 uses `IMPLEMENTOR'S CHOICE:` with a named constraint and carries no fill-the-blanks header over its staged sections. Round 6
  filed the header over `## 5. Proposed changes` on that ground, because SPEC-1 through SPEC-3 beneath it are verbatim staging and a header
  calling them indicative targets tells a maintainer applying tomorrow not to apply them as written.
- **Proposal-format facts. Do not re-derive them and do not invent against them.** The change-proposal format requires a
  `## Deliverable index` in the summary as "the ONLY place a deliverable id resolves", and no proposal in `proposals/` has one, because the
  requirement postdates the migrated layout; do not invent one for 0076. The same format requires that every staged deliverable appear in
  exactly one step and that no step name one that does not exist. `implement-proposal` treats a step declaring two lanes as `bad-lane` and
  stops the run, so 0073's `**S15 · migration, code**` and `**S11 · schema, code, test**` are historical rather than precedent; `code`,
  `schema`, `migration`, `test`, and `docs` all dispatch to the same build handler, so splitting a deliverable across lanes buys no handler
  difference. Each step is one commit, is gated on the tier set it names, and is never ticked on a gate that did not pass. Every test-lane
  step in the neighbouring proposals declares tier 0 explicitly, so tier 0 is not implied by naming a higher tier.
- **Refuted classes. Do not re-file one without new evidence; each cost a round.** Read a refutation's own scope first, and keep its body
  rather than its title: the one killing the "no second value" finding turned on that clause being rationale that lands nowhere in `spec/`,
  and it does not reach staged text. (a) Sender-side against pod-side: `spec/28`'s `CH-FENCE` Preconditions, `spec/04`'s `CoordinatorFence`
  row, and `docs/reference/adapter-contract.md` state a gateway obligation inherited from step 2 while the staged clause states a pod-side
  gate, and the two coexist. This does not extend to §28.6's second-opener sentence or the §28.8 `CH-CHECKPOINT` cell, whose first clauses
  are pod-side rejection rules. (b) The exemption-unit argument, that D6 turns one exempt fence per pod into N: a fence issues only after a
  successful compare-and-swap, an out-of-order pair is self-healing by step 2's own clause, and `checkSessionBound` rejects an unbound
  session's fence first. The full sentence is what refutes; the one-line summary does not. (c) The "unqualified sentence inside a
  session-scoped narrative" class, killed on §29.8 step 7 and on the §7.2 parenthetical in `spec/07`. It does not cover `spec/04` §4.1's
  declared `pod` class for `CoordinatorFenceRequest`, which stands as an OPEN. (d) Session-less pod-scoped RPCs having no session to key on:
  none of the four carries a `coordination_generation` field. (e) "Step 3's unset clause permits what step 2 forbids": step 2's bar is a
  sender obligation, so the window stays closed for compliant coordinators. (f) A fencing hole in D7 admitting a superseded replica's barrier
  because the pod holds no value: for a never-fenced session the only replica accepted is the current coordinator or the immediately prior
  one inside the step-2 window, which step 2 sanctions. (g) "The §10.1.4 hold-exit predicate is unstaged", and its variant that a successful
  fence for any one session exits the hold for the pod: `exitHoldState` is reached only on the accepted arm of `CoordinatorFence`, so the
  staged bullet matches the tree, and SPEC-2 stages the consequence in §29.10's "Shared by the whole pod" bullet. (h) The collapse of the
  first-handoff and later-handoff states in the staged §10.1.8 and §29.7 predicates: both now carry the unset arm and the pre-fence window
  explicitly. (i) The §10.1.2 step 2 edit instruction as an underspecified target: its two candidate insertion points differ in meaning, but
  the SPEC-1 state-model paragraph and the staged §28.5.1 Messages mirror both spell the intended sentence out, so a competent applier cannot
  land the pod-wide reading. (j) The staged §10.1 invariant "every generation a pod validates is positive" as falsified by the cache
  fallback's literal 0: that is the already-refuted "unreachable by construction" class, whose refutation covers the invariant and the
  retained adapter backstop. (k) A missed-edit-site finding over a path or a comment under `tests/` or `pkg/`: criterion (d) enumerates
  `spec/`, `docs/`, `schemas/`, and `charts/` and reaches neither tree, and a Go doc comment that becomes less precise while its assertions
  stay valid is not a site. The precedent is the refutation of `pkg/gateway/coordination/barrier/barrier.go:62-65`, and it settles the whole
  class, `adapterclient/coordinatorfence.go:37`, `coordinatorfence_test.go:15-16`, the stale `// spec:` parenthetical in
  `generation_fence_wire_test.go:134-135`, and `RecordHandoff`'s stale rationale comment included.

- **The barrier-gate move is one deliverable and its blast radius is closed.** CODE-1 owns the whole registry-entry move and states the
  hold-the-pointer rule once over `CoordinatorFence`, `CheckpointBarrier`, and `Checkpoint`; CODE-2 shrinks to the generation gate at
  `pkg/adapter/coordination.go:236`. `s.barrier` has exactly five production readers (`coordination.go:64-66`, `:264`, `:269`,
  `checkpoint.go:122`, `:124`) plus four in `coordination_test.go` (`:224-226`, `:282`, `:285`, `:356-357`), and `s.coord` is confined to
  `coordination.go`, so the move breaks exactly one file outside it. The rejected alternatives were: replacing only CODE-2's last
  sentence, a sibling helper returning the entry beside `checkpointRootsForSession`, a re-lookup at the link site, a pod-wide gate, and a
  compensating close-the-gate action on deregistration.
- **Both deregistration paths remove the slot tree after taking the entry out of the map** (`pkg/adapter/session.go:271`,
  `pkg/adapter/holdstate.go:249`), so a mid-flight case cannot assert a successful `CheckpointSummary` without flaking. The assertions
  that hold whatever the upload does are the link, the ack's `checkpoint_ref`, and the RPC's return, because `barrierGate.link` records
  the id at `checkpoint.go:122` before any chunk work and `defer s.barrier.complete()` fires on any stream return. An accepted barrier's
  ack is assertable with the landed goroutine, wait-for-gate, drive-stream pattern.
- **Tier 1 already runs `-race` over the whole repo,** `cmd/lenny-test/cmd_run.go:880` setting it inside `runUnitTier` (`:869`), so an
  adapter-internal concurrency case does not need tier 7a for the detector. The placement rule §8 now runs on: a case that arranges
  concurrent calls only as the way to reach process-local state stays at tier 1, and a case whose subject is contention itself is pinned
  at tier 7a.
- **The pod-level op lock, rather than the barrier gate, is what serialises co-tenant checkpoints.** `Server.Checkpoint` calls
  `s.ops.Begin(ctx, opCheckpoint, sessionID)` at `pkg/adapter/checkpoint.go:111`, a distinct session id is admitted and waits while only
  the same id coalesces, and §4.7.2 and §5.2 state that serialisation normatively. A per-session `barrierGate` therefore removes the gate
  cross-link without making two co-tenant acks independent: barrier B's ack cannot return before stream A's whole archive finishes, so
  §8's tier-7a phrase "neither waits on the other" is literally false. The operative contrast it draws is against the pod-wide gate's
  block to the shared 90s ack deadline, and the assertions carrying the fix (each ack echoes its own stream's id, both complete promptly)
  hold. `coord.mu` is not held across the blocking wait, being taken and released in three short critical sections
  (`coordination.go:232-235`, `:246-248`, `:253-257`) before the `select` at `:265-268`, so §7's third open decision cannot falsify that
  case. `checkpointBarrierAckTimeoutSeconds` defaults to 90s and the CRD webhook validates it against one slot's
  `max_tiered_checkpoint_cap` rather than `maxConcurrentSessions × cap`, which is shipped and pre-existing and is why "both co-tenant
  barriers ack" cannot be assumed at high `maxConcurrentSessions`.
- **Landed cases already pin what §8 might otherwise be thought to owe.** `TestCoordinatorFenceGapDetected`
  (`pkg/adapter/coordination_test.go:135-172`, fence 3 then 7) pins a genuine within-session gap and survives the per-session rescope;
  `TestCoordinatorFenceStaleGenerationRejected` pins the stale arm; `TestCheckpointBarrierRejectsGenerationMismatch` (`:199-216`, fence 4
  then barrier 3) pins the surviving `gen != fenced` refusal, so only `TestCheckpointBarrierRejectsWithoutFence` sits on the arm D7
  retires. `tests/tier2_component/slotrelease/revoke_double_teardown_test.go:306-334`, `:363-406` drives the coordinator-loss hold to
  termination against a real `adapter.Server` over envtest, so CODE-1 and CODE-3 genuinely reach tier 2 as a regression gate; that file
  carries no generation, `coordinator_lost`, or `last_generation` assertion, so CODE-3 owes it no disposition. The whole of
  `tests/tier9_security` carries no generation, fence, or accessor reference, so tier 9 is correctly absent from every tier list.
- **Migration 0181's environment is settled.** `coordination_lease` is created and touched by migration 0164 alone and its column carries
  no CHECK, only `NOT NULL DEFAULT 0`; the mirror upsert always binds the value explicitly, so `DEFAULT 1` is cosmetic and `upsertMirror`
  runs for every eligible held row at the end of the sweep loop rather than only on the takeover branch, which means post-migration mirror
  rows carrying 0 self-heal within one sweep and no backfill is owed. 0050 declares the check inline on the column, so Postgres names it
  `sessions_coordination_generation_check` exactly as CODE-4 claims; drop-and-re-add of a CHECK with a backfill is precedented by 0103,
  0063, and 0167, and only 0156 uses `NOT VALID` or `CONCURRENTLY`, so no convention is violated.
  `TestProdMigrationsRollBackPerStep` walks `prodMigrationSchema` highest-first calling `MigrateTo(n-1)` and never re-applies, so 0181's
  down-then-up round trip is never exercised. `lint-migrations.sh` pass 3 greps the bare number in any `_test.go` under
  `tests/tier2_component/migrations/`, and `prod_columns_test.go` lives there, so the registry row alone satisfies pass 3 and §8's
  justification that a case in `tests/tier2_component/stores/` alone leaves tier 0 red is not exact; the directory choice it supports is
  still right. `tests/tier2_component/coordination/sweep_test.go` runs `memstore` against a real Redis rather than `pgstore`, so its
  `:275-594` assertions shift through CODE-4's memstore floor rather than through the migration.
- **Deploy-time ordering is a separate axis from commit grouping.** Migrations run as a Helm `pre-install,pre-upgrade` hook at weight -5,
  while 100% of the old gateway fleet is still serving, and the gateway Deployment is applied after all pre-hooks, so the schema is ahead
  of the binaries for the whole rolling window. `pgstore.Create` is the only production `INSERT INTO sessions` and names
  `coordination_generation` in its column list bound from the struct with no floor, so every old-binary insert writes a literal 0. §10.5
  carries the expand-contract rule for exactly this case and the tree already has phase tracking (`schema_migration_phase`,
  `phase1_applied`, `phase3_applied`) — EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16, :38-39;
  spec/10_gateway-internals.md:420, :425, :429-433; pkg/schemamigrate/phasestore_pg_test.go:48, :101.
- **The prestop acked-but-uncaptured gap is pre-existing and is not this proposal's.** `dispatchOne` sets `out.CheckpointErr` and then
  `out.Acked = true` whenever `dispatch.Send` succeeded, `fireBarrier` keys `acked[SessionID]` on `o.Acked` alone, and the post-barrier
  loop counts that session as checkpointed. D7 removes the prestop fallback capture for the whole never-handed-off population, which was
  also the only retry for a failed barrier-window checkpoint, but the skip is what shipped §10.1.8 mandates and the ack-timeout arm
  degrades correctly, a deadline on `Send` not being `ErrGenerationStale` — EVIDENCE:
  pkg/gateway/coordination/barrier/barrier.go:216-232, :243-244; pkg/gateway/podlifecycle/prestop/prestop.go:388-396, :509-514.
- **An `InvalidArgument` from `CoordinatorFence` is not a clean refusal,** so "Both refusals are loud and fail closed" is imprecise for the
  fence half: it falls into `coordfence.fence`'s `default:` transient arm, burns the whole attempt budget, then relinquishes the
  coordination lease and aborts the resume. The barrier half does return immediately — EVIDENCE:
  pkg/gateway/coordination/coordfence/coordfence.go:159-188.
- **Barrier ids collide across co-tenant sessions on one pod and it is not an ack-routing defect.** `nextBarrierID` is per-session
  monotonic (`pkg/gateway/coordination/barrier/barrier.go:265-272`) and the `CheckpointBarrierAck` control event carries `barrier_id`
  with no session id (`pkg/adapter/coordination.go:292-298`), but no gateway code consumes the control-stream mirror and `dispatchOne`
  uses the synchronous `dispatch.Send` return. CODE-1 makes concurrent co-tenant acks more common and introduces no ambiguity.
- **No production code compares `CoordinationGeneration` against a literal 0 as a sentinel,** and no `spec/`, `docs/`, `schemas/`, or
  `charts/` sentence states the counter's initial value, so the baseline reaches no surface outside the ones §9 already lists.
  `schemas/embed.go:13-26` embeds `lenny-adapter.proto` itself rather than a derived artifact, so it follows the source with no separate
  SCHEMA-1 edit.

### Traps

- **MISTAKE: a wire value of 1 on the barrier.** Pass 8 put a wire value of 1 on the barrier, the one positive value a first takeover also
  produces (a row at 0 compare-and-swaps to 1), so predecessor and successor carried the same number and the first fence separated nobody. A
  value invented to satisfy a guard is checked against every other producer in the same domain, and the compare-and-swap is a producer.
- **MISTAKE: the baseline's test-lane consequence is a class, and the residue is outside it.** Pass 9 called
  `tests/tier2_component/coordination/sweep_test.go` the whole test-lane consequence and enumerated even that file incompletely. The
  consequence is every assertion reading a session row's `CoordinationGeneration` after a create that left the field unset, across the
  takeover, mirror, memstore, tier-7a, and tier-8 suites. Two landed tests break outside that class:
  `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` seeds another row's constant at 1 calibrated against a session row created
  unset, the only site of its kind in the tree, and `TestFenceZeroGenerationFencesAtBaseline` pins the very floor CODE-4 deletes. Look at the
  class boundary for residue rather than inside it.
- **MISTAKE: do not floor a value the sender does not hold.** CODE-4 floored the value in `dispatchOne` before the
  `sessioncheckpointmeta.Record` write, so a generation the gateway could not read would have been persisted as a fabricated 1 into the
  column feeding the `MAX(coordination_generation)` resume selector. A floor on a value the sender does not hold converts a loud refusal into
  a silent falsification.
- **WATCHOUT: `initialized` moves with `lastFenced` or the defect comes back.** Left on `Server` while `lastFenced` moves to the slot entry,
  it flips on the first fence anywhere on the pod, so every later co-tenant's first fence tests initialized-true against its own zero and
  reports a gap.
- **MISTAKE: a message's doc comment sits above the `message` line.** An evidence range starting at `message X {` misses it, which is how
  three rounds missed the `CoordinatorFenceRequest` comment at a cost of two findings. Read the comments rather than the proposal's
  description of them: a list built from the common closing phrase alone returns eleven field comments and misses `ShutdownRequest`. That
  instruction produced a round-2 lens's only finding, so keep it until SCHEMA-1's list is settled.
- **MISTAKE: holding a value is not holding a different one.** Pass 7 filed the §28.8 `CH-BARRIER` and `CH-CHECKPOINT` cells as non-sites
  because "a superseded replica is one whose successor's fence the pod has recorded, so the pod holds a value", where rejection is
  conditioned on a mismatch. Cost a round, and the non-site record hid the `CH-CHECKPOINT` cell from every later sweep.
- **MISTAKE: a sentence spanning channels whose gates differ takes one clause per gate.** The earlier fix generalised a rationale true only
  of `CH-FENCE` across all four channels §28.6's second opener spans. Do not give the whole sentence the barrier's equality predicate, since
  a legitimate handoff fence carries a generation above the pod's, and do not keep the row-value relation: between a successor's
  compare-and-swap and its fence acknowledgement the row reads one above the value the pod holds, so that relation rejects exactly where step
  2 states acceptance, on a reachable ordinary path.
- **MISTAKE: when one paragraph carries two statements of a rule, adjudicate both in the pass that touches either.** Pass 10 read §28.6's
  fence-acknowledgement sentence as agreeing with the staged first sentence and left it standing, so the same correction had to be made
  twice, a round apart.
- **MISTAKE: paraphrasing §10.1.2 step 2 as "the pod accepts a coordinator's RPCs until that coordinator's fence is acknowledged"** states
  the opposite of the sender-side bar the step carries, and cost a finding.
- **MISTAKE: step 3's domain claim is attached to the wrong sentence.** SPEC-1 attaches the "whole set of gateway-to-pod RPCs" domain claim
  to step 3's opening sentence ("all subsequent gateway-to-pod RPCs include the local generation stamp") when the sentence carrying that
  domain is the acceptance sentence after it. The opening sentence is scoped by step 3's framing to the acquiring coordinator's own RPCs, and
  the conflation is what makes the barrier's shared-state provenance read as a contradiction of step 3 rather than a fact about one RPC. It
  cost a finding.
- **MISTAKE: a sentence re-stated in an edit is read against every other sentence that edit touches.** Ten passes restated step 3's no-window
  sentence without cross-reading it against the barrier acceptance the same passes were settling, even though refuted class (f) and the §28.8
  `CH-BARRIER` entry together said in so many words that a superseded replica's barrier is accepted on the equality arm.
- **MISTAKE: refuted class (f) holds for the unset arm alone.** It says nothing about the equality arm, where a matching stamp is accepted,
  and reading it as covering both is what let the stamp collision survive a round.
- **MISTAKE: refuted class (k) does not reach §9's tier-3 omission.** A round-2 lens nearly filed §9's omission of any
  `tests/tier3_contract` file on that ground and stopped only at the bar; §8 names the tier-3 case with its assertions, so criterion (f) is
  not met either. Do not spend a verification on it.
- **MISTAKE: a clause naming what an edit bullet leaves alone is relative to that bullet.** Pass 11 split the §28.6 fence-acknowledgement
  sentence into its own bullet and carried the previous bullet's "the paragraph's remaining sentences are unchanged" across unchanged, where
  it asserted the opposite disposition for the sentence the bullet above stages, so an implementor would have left that sentence standing.
  Moving such a clause re-anchors it, and it is re-read against the new bullet's own subject.
- **MISTAKE: a mirror applies the owning section's predicate by reference.** §10.1.8 and §29.7 were staged in terms of whose fence had landed
  while step 3 owns the predicate, and the same round grounded D7 on the generation gate alone when an earlier non-positive guard refused the
  RPC first. A claim about what refuses an RPC today is read against every guard preceding the one being changed.
- **MISTAKE: an UNVERIFIED handed to another lane is re-read by whoever reverses the text it qualifies.** The zero-stamp fact sat as an
  UNVERIFIED routed to another lane while the staged text still refused that barrier; pass 7 then reversed the text to accept it without
  anyone chasing the UNVERIFIED, so D7's repair was unreachable for a round.
- **MISTAKE: state a reconciliation as a fact about provenance rather than as an outcome.** Pass 11 closed the step-3-versus-§10.1.8
  reconciliation by asserting an outcome ("a barrier that reaches the pod after the successor's fence has landed carries the value the pod
  holds and is accepted") where the true statement is about where the value comes from, and the clause had already been copied into three
  live rationale sites, so one wrong sentence cost four edits a pass later. An outcome sentence invites a reviewer to find the interleaving
  that falsifies it.
- **MISTAKE: a withdrawn claim is swept across the whole proposal, the design blanks included.** Pass 12 withdrew that consequence from the
  staged text and left it standing in §4's open-design bullet, which is the premise §7's first open decision hands the human reviewer.
- **MISTAKE: an enumeration that cannot be closed is not repaired by widening it.** Pass 12 replaced pass 11's false universal with a closed
  two-value enumeration of the generation a barrier can carry, which is the same defect one step weaker, and round 5 falsified the narrowing
  as well. The correction is to state the outcome rule and stop, which is what deleting it from staged §10.1.8 step 1 did. A step that
  applies another section's predicate by reference does not owe a list of the values the predicate will see, because reachability is a
  property of the value's producers rather than of that step.
- **MISTAKE: a finding's named sites are a starting set rather than the sweep.** The finding that drove the fixer named two sites while the
  wrong attribution stood in three, the third being §8's own preamble, so correcting only the named anchors would have left §8 contradicting
  itself.
- **MISTAKE: a rationale about the production path is not a description of the fixture that stands in for it.** The tier-4 fence
  misattribution came out of a pass record that justified the tier by the production `coordfence.Fencer`'s relinquish, and the justification
  was then copied into §8 and TEST-1 as a description of the harness.
- **MISTAKE: a deliverable that relocates state owns every site that reads it.** The deliverable split put the sites that read relocated
  state (`checkpoint.go`'s link and complete, the barrier's quiesce-and-hold) in a later deliverable than the move itself, which is what let
  a false exemption be written for one of them and would have left S5 staging an edit S3 could not compile without.
- **MISTAKE (loop-level): a converged spec lane whose consequences are unwritten is not a converged proposal.** The run kept scheduling the
  spec lane, whose corrections were already derived, while the lane holding the undone corrections never ran, so `non-spec-changes.md`,
  `implementation-checklist.md`, and `status.md` stood five rounds behind.
- **WATCHOUT: §4's fill-the-blanks marker is not the §5 header finding.** It names four derivable items each carrying a constraint, which is
  a different case from the header round 6 filed over `## 5. Proposed changes`; it stands as its own OPEN. Do not merge them.
- **Editing hazards in this proposal's own files.** `cat -n *spec-changes.md` globs two files: it matches `.non-spec-changes.md` and
  `.spec-changes.md` and numbers them continuously, so a line taken that way is off by 46. Name the file in full. Every line citation into
  the spec-changes file taken before run 3 has moved; re-derive rather than trusting a range from an earlier round's design, finding, or log
  entry, and anchor each edit on the sentence it quotes rather than on an offset, because a sibling group editing the same file in the same
  round shifts every later line. The spec-changes file wraps at 98 columns and the non-spec-changes file at 99 to 106, and a hand re-split
  cascades, so reflow a whole blank-line-delimited paragraph in one pass at the width of the file being edited. `spec/10:183` carries both
  anchors SPEC-1 edits in §10.1.8 step 1 on one physical line, so an edit that rewrites one must not clobber the other. §28.6's
  second-opener paragraph has two sentences that were both called "its closing sentence" in the live staged text, the fence-acknowledgement
  one and "The constraint excludes a second replica", and their verdicts differ, so name a sentence by content rather than by position. A
  design's stated line range for a staged paragraph can stop mid-sentence, because that paragraph's closing sentence begins inside the same
  physical line as the sentence before it; take the blank-line-delimited extent instead, or the closing sentence is silently dropped when the
  paragraph is replaced.
- **WATCHOUT: a pass record under `## Resolved in adversarial review` keeps the words it was written with.** A later pass withdraws its text
  in a new entry, and a fixer who silently rewrites a citation inside one has edited a record meant to stand. Grep returns those records
  beside the live sites, so check which of the two a hit is before editing it, and a round that lifts a cost clause out of a pass record
  lifts the corrected one; a grep for a step identifier such as `S6` or `S7`, or for `coordfence.Fencer`, returns a dozen frozen records
  beside the one live site. Several fixers now append to that section in one round, so read the last heading rather than assuming the next
  pass number. The section lives only in the spec-changes file, even in a non-spec round, so appending a pass record touches the one file
  such a round is otherwise told to leave alone.
- **WATCHOUT: a later edit that clears fields on deregistration silently zeroes every `coordinator_lost` record.** The post-mortem reads each
  terminated session's own value off the detached `*slotState` pointer, which `deregisterSlotLocked` returns with no field zeroed.
- **WATCHOUT: `pkg/adapter/holdstate_test.go:887-892`'s doc comment justifies a no-fields resolved line** by saying the generation "is
  already on the coordinator_connection_lost line", which CODE-3 removes. The test still passes, so a fixer editing that file for §8's hold
  case should fix the comment while there.
- **MISTAKE: "a per-entry gate never has to handle a missing entry" is false.** The guard-ordering fact used to close with it, and that is
  where CODE-2's false exemption came from. `s.ops.Begin` sits between the guard and the link at `pkg/adapter/checkpoint.go:111` and queues a
  co-tenant checkpoint for the whole of the running session's upload (a distinct session id is admitted and waits; only the same id
  coalesces), and both deregistration paths run inside that window. The guard validates an entry the link site must be handed; the earlier
  success is an ordering fact rather than a persistence fact.
- **Test-lane fixture hazards, each of which makes a case pass or fail for the wrong reason.** `tests/tier7a_load` names no directory: the
  local suite is `tests/tier7a_load_local` and the Kind one is `tests/tier7b_load_kind`. `coordfixture.FenceReadopter.ReadoptAndFence` takes
  a `sessionID` and then calls `r.Pod.Fence(ctx, generation)`, which fences whichever session `StartPod` named; on a single-session fixture
  that is invisible, and the first co-tenant case fences the wrong session, so a per-session accessor set forces the latent defect into the
  open. `coordfixture.StartPod` boots one adapter and starts one session, so a co-tenant case starts its second session over the exported
  `Pod.Client`; reaching for a second `StartPod` gets two adapters and destroys the co-tenancy the case exists to test. The barrier's
  deferred quiescence clear uses the `*slotState` resolved once at the top of `CheckpointBarrier` rather than a second lookup by id, because
  the hold timeout or `Shutdown` can deregister the session mid-barrier and the detached pointer stays valid where a re-lookup returns
  nothing. `newFencedServer` (`pkg/adapter/coordination_test.go:23-30`) claims the session and does not fence it despite its name, so
  `TestCheckpointBarrierRejectsWithoutFence` asserts exactly the refusal D7 retires and the step landing CODE-2 amends it or tier 1 goes red.
  `pkg/adapter/coordination_test.go` and `holdstate_test.go` are `package adapter` while the checkpoint-stream fixtures (`concurrentServer`,
  `slotStartReq`, `adapterClient`, `driveCheckpointConc`) are in the external `adapter_test` package, so a case told to land in
  `coordination_test.go` that drives a stream hits a package wall. Tier 1 already runs `-race` over the whole repo, so an adapter-internal
  concurrency case does not need tier 7a.
- **MISTAKE: `coordfixture` is not behind a build tag.** A design instructed §8 to say the fixture "is compiled only under the integration,
  chaos, and load_local build tags, so tier 0 does not catch the accessor change". The file carries no `//go:build` line and tier 0 does
  compile it. What tier 0 misses is the three tagged consumer trees.
- **MISTAKE: pass 9 of the spec changes tells a later pass to delete two things that were never written.** It says to drop
  `pkg/gateway/coordination/barrier/barrier.go` from the files-touched list and a dispatcher case from §8. Pass 8 wrote neither into the
  deliverable files, which were five rounds behind it, so both instructions are no-ops and a fixer who goes hunting concludes the file is not
  the one pass 9 describes. It cost about ten minutes of doubting the file. Grep for a sentence before hunting for it, and read an
  instruction written against a lane that never ran as a statement of its writer's intent rather than a description of the file.
- **WATCHOUT: batching an S3/S4 file into one step turns a declared tier red in either direction.** Batched into S3 the assertions read 2, 2,
  3 before migration 0181 and the `pgstore` floor exist, and that file runs against the production `pgstore` (`:85`); batched into S4 it
  still calls the zero-argument `LastFenced` after S3 changed the signature, so S3's own tier 8 does not compile. MISTAKE: the finding and
  its design named the tier-8 file alone while the neighbouring sentence covers the tier-7a file, so a file-scoped correction would have left
  the identical defect one sentence away. State a step split as a rule over the class when the class is closed.
- **MISTAKE (avoided, recorded so no one spends the round on it): CODE-2 naming `pkg/adapter/checkpoint.go:122` while CODE-1 removes
  `Server.barrier` is not an S3/S5 compile break.** Checklist step S3's own text says the barrier gate moves onto the slot registry entry, so
  the move and every call site it drags land in S3, and CODE-2's sentence describes the end state of the link rather than staging a separate
  edit. Read a deliverable's sentence against the step text that owns the mechanism before filing a sequencing break.
- **MISTAKE (avoided): §8's "2 (the registry state, the migration, and the Postgres session store's floor)" does not contradict "the
  per-session fenced value ... pinned at tier 1".** "The registry state" reads as the `prodMigrationSchema` migration registry, which is
  CODE-4's tier-2 work alongside the migration behaviour file and the `pgstore` floor, so all three tier-2 subjects have listed cases. A pass
  was spent on the slot-registry reading before ruling it out; do not re-derive it.
- **WATCHOUT: a file the proposal cites is not thereby a file the proposal touches.** `pkg/gateway/coordination/barrier/barrier.go` is read
  by CODE-1 (`:190-201`, `:238-245`) and by §8, and its doc comment at `:62-65` grounds a safety claim on the adapter's barrier gate as an
  unconditional rejection, yet §9 lists no file under `pkg/gateway/coordination/barrier/`. Grep §9 for the path rather than assuming a cited
  file is a listed one. Under refuted class (k) the stale comment itself is not a finding.

- **WATCHOUT: §8's preamble is scoped, and re-tightening it reopens three bullets.** The preamble reads "Each must fail against the
  pre-fix code, except where a bullet states otherwise" because the mid-flight deregistration case does not fail against the pre-fix code:
  against the pod-wide gate the link never sees a missing entry, so it passes, and it fails only against the re-lookup reading CODE-1
  exists to rule out. The two bullets that call themselves amendments of landed cases are the other two the scope regularises. A later
  round that restores the unqualified sentence reopens all three.
- **WATCHOUT: `coordfixture.Pod.StaleRPCRejected` sends a `CheckpointBarrier`,** so it is the one fixture method D7 could turn into a
  hang. It cannot today, because both call sites probe a session the pod has already been fenced for, so the barrier hits the surviving
  `gen != fenced` arm and returns `FailedPrecondition` at once. A co-tenant case that probes an unfenced session blocks to the 90s ack
  deadline instead of returning false. A grep for `CheckpointBarrier` under `tests/tier4_integration/` also returns nothing while
  `coordination_fence_split_brain_test.go:151` drives one through this helper, so the call is invisible to that grep — EVIDENCE:
  tests/testinfra/coordfixture/coordfixture.go:117-127.
- **MISTAKE: nearly filed that the mid-flight deregistration case cannot assert an accepted barrier's ack, because an accepted
  `CheckpointBarrier` blocks.** It can. Landed `TestCheckpointBarrierAcksEchoedCheckpointID`
  (`pkg/adapter/coordination_test.go:243-300`) and `TestCheckpointBarrierMapsAck_spec_10_1`
  (`pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:112-154`) both run the RPC on a goroutine, spin on the gate, and then
  drive the stream, so any §8 bullet saying a barrier "is accepted" is constructible that way.
- **MISTAKE: the D5 co-tenant-freeze residual is shipped spec text, not an unrecorded failure mode.** A docs lens re-derived it as an
  "accepted failure mode stated only in reasoning" before checking: `spec/10_gateway-internals.md:58` already states that on a pod serving
  more than one session the hold timeout terminates every session the adapter has started there, and the premise of two replicas
  coordinating two co-tenant sessions is separately recorded as unreachable in the shipped tree. Cost about ten minutes.
- **MISTAKE: tier-list bookkeeping is a refuted class, three times over.** The S6-omits-tier-0 finding, the S4-declares-tier-3 one, and
  the §8-omits-tier-11 candidate were each worked up and each killed on the same reasoning: the gate the checklist names still runs, no
  new test is owed, and an extra declared tier costs a run rather than a red. A `// diagnosis:` finding over §8 is the same shape: the
  rule in `.claude/rules/test-coverage.md` is always-on and binds the implementor whether or not §8 restates it. A later round wanting any
  of them must argue past all of that first.
- **WATCHOUT: §8's disjointness arguments enumerate only the `pod.LastFenced` reads,** while CODE-1 also rescopes `Pod.Fence` and
  `Pod.StaleRPCRejected`, which appear at tier-8 `:130`, `:165`, `:184`, tier-7a `:169`, and tier-4 `:83`, `:151`. The conclusion survives,
  because every one of those sites sits in a subtest seeded at 1 explicitly, so this is an incomplete enumeration rather than a wrong
  conclusion. Do not file it.
- **WATCHOUT: `ls migrations/` returns `*_test.go` files as well as `*.sql`,** and migration behaviour tests live in both `migrations/`
  and `tests/tier2_component/migrations/`, while `scripts/lint-migrations.sh` pass 3 greps only the latter (`TEST_DIR` at `:45`). A case
  placed in `migrations/` satisfies nothing.

### Open

Detail for each item is in the ledger entry named at the end of its line. The `[non-spec.5.*]` and `[non-spec.6.*]` entries were retired
in compaction pass 17; the items they carried are in the ledger residue entry `[non-spec.5-6.*]`, filed there under the id named here.

- **"Current" generation on the barrier.** Whether §10.1.8 step 1's and §29.7 step 4's "carries the current `coordination_generation`" are
  themselves edit sites, given the mirror lag. `[spec.1-3.*]`
- **"The ordinary false positive".** SPEC-1's live rationale overclaims a sole ordering; drop the two words. `[spec.1-3.*]`
- **SCHEMA-1 qualifier wording.** The exact qualifier each of the seven carriers takes, including the D7 acceptance sentence. `[spec.1-3.*]`
- **Status file.** Its scope bullet drops the hold clause and its closing paragraph still calls the hold-state decision open. `[spec.1-3.*]`
- **D6 test-side cascade.** TEST-1 and §8 owe a co-tenant first fence well above 1; four citations spell the exemption per pod lifetime and
  two of them are in no §9 entry. `[spec.1-3.*]`
- **CH-ADAPTEREVENTS ownership.** Which replica's connection carries a multi-session pod's events, and so whether two co-tenant sessions can
  be coordinated by two replicas at all. `[spec.1-3.*]`
- **Scope of the proposal.** Whether D7, the counter baseline, and the barrier-provenance reconciliation belong here at all. `[spec.1-3.*]`
- **Three imprecise rationale sentences.** The "unreachable by construction" phrase, the "only failure arm" claim, and the twinned "either
  `Create` path" sentence. `[spec.1-3.*]`
- **§4's fill-the-blanks header.** Whether a converged proposal may keep it over four derivable items. `[spec.1-3.*]`
- **Superseded replica's stream against a quiesced pod.** Whether it is acceptable, and whether an accepted false-positive barrier is
  followed by a stream at all. `[spec.1-3.*]`
- **`spec/04` §4.1 fence row.** `CoordinatorFenceRequest` is declared pod-scoped and a tier-3 test pins the classification. `[spec.1-3.*]`,
  and `[spec.1.review-edit-sites.1]` asks whether the staged edits must adjudicate it.
- **§1 severity.** Whether the recalibrated headline harm is restated at the top of §1 rather than only in §1.3. `[spec.1-3.*]`
- **Proposal 0080 overlap.** Its section 1.14 covers the same §29.10 bullet SPEC-2 stages for removal; nobody has recorded the overlap.
  `[spec.1-3.*]`
- **Rebind and the unset state.** Whether a session can unbind and re-bind on the same pod, which would lose the per-entry value.
  `[spec.1-3.*]`
- **§29.2 step 11.** Whether SPEC-1 owes a change to the bullet recording the pre-message announcement as unstated. `[spec.1-3.*]`
- **`coordinator_lost` log line as a spec artifact.** The staged §10.1.4 text names it where no section introduces it. `[spec.1-3.*]`
- **CODE-1's accessor enumeration omits `checkpointbarrier_test.go:163`.** `[spec.1-3.*]`
- **UNVERIFIED: tier-1 home for the sess-b-is-zero assertion.** `[spec.1-3.*]`
- **UNVERIFIED: whether the shipped drain ever quiesces a never-handed-off session.** `[spec.1-3.*]`
- **UNVERIFIED: step 3's "only" against its unset carve-out.** `[spec.1-3.*]`
- **UNVERIFIED: D6's stated ground for the first-fence exemption does not follow,** though the exemption is right. `[spec.1-3.*]`
- **UNVERIFIED: baseline reachability in pure spec terms.** `[spec.1-3.*]`
- **UNVERIFIED: `upsertMirror`'s stale-window barrier.** Whether one dispatched inside the window is refused. `[spec.1-3.*]`
- **UNVERIFIED: wire population.** Two rounds disagree on how many outbound requests set the field; the newer reading (two sites) is the one
  carried here. `[spec.1-3.*]`
- **UNVERIFIED: §28.5.1's `CH-BARRIER` Preconditions bullet,** sender-side or pod-side. `[spec.1-3.*]`
- **UNVERIFIED: `tests/claim-map.json:76-82` files the barrier field `UNWIRED`** where the adapter compares it today. `[spec.1-3.*]`
- **Why CODE-1 reaches tier 4.** `[spec.1-3.*]`
- **UNVERIFIED: the persisted-row half of the tier-7a barrier case.** Tier 7a or proposal 0060's tier-8 harness. `[spec.1-3.*]`
- **UNVERIFIED: the 90s bound for a barrier whose session is deregistered mid-flight.** `[spec.1-3.*]`
- **§9 names no file for the staged tier-3 and tier-2 cases.** `[spec.1-3.*]`
- **UNVERIFIED: whether the tier-7a two-barrier case can be written against real `Checkpoint` streams.** `[spec.1-3.*]`
- **UNVERIFIED: §8's tier sentence omits tier 11 while checklist S1 declares it.** `[spec.1-3.*]`
- **UNVERIFIED: 0181's `.down.sql` names one default where the `.up.sql` changes two.** `[spec.1-3.*]`
- **UNVERIFIED: whether the external test package can stall `s.ops.Begin` deterministically** without a test-only hook.
  `[non-spec.5.fix-G1.1]`
- **UNVERIFIED: whether a tier-7a or tier-4 case breaks under a re-lookup implementation.** `[non-spec.5.fix-design-G1.1]`
- **UNVERIFIED: `coordinatorfence_test.go:15-16` spells the exemption per pod lifetime.** `[non-spec.5.review-applicability.1]` and
  `[non-spec.5.review-citations.1]`
- **UNVERIFIED: §8's tier sentence attributes "the registry state" to tier 2.** `[non-spec.5.review-applicability.1]`
- **UNVERIFIED: SPEC-1 calls the "Generation counters" bullet §10.1's while it lives in §10.1.1.** `[non-spec.5.review-citations.1]`
- **`coordinatorfence.go:37` is a fourth exemption site and the only production one.** In no §9 entry and no deliverable.
  `[non-spec.5.review-client-surface.1]`
- **The docs lens has returned nothing twice on a surface re-derived eight times.** The per-lens re-derivation is what costs rounds.
  `[non-spec.5.review-docs-alignment.1]`
- **CODE-1 declares tier 2 with no tier-2 package touching its accessors.** `[non-spec.5.review-feasibility.1]`
- **§8's tier-4 sentence for D7 names no case, file, or step, and S5 declares no tier 4.** `[non-spec.5.review-feasibility.1]`
- **UNVERIFIED: the tier-2 resume-fence-then-takeover case has no file and no existing tier-2 `coordfixture` pod.**
  `[non-spec.5.review-fresh.1]`
- **Migration `0164`'s column comment states the match rule D7 narrows.** A note rather than an edit site. `[non-spec.5.review-fresh.1]`
- **`coordinationState` embeds its own `mu`,** so CODE-1 settles §7's third decision by construction while §4 frames it as open.
  `[non-spec.5.review-mechanism.1]`
- **UNVERIFIED: the claim-map barrier row against the generation fence.** `[non-spec.5.review-operational.1]`
- **Deploy-time ordering has not been looked at at all.** `[non-spec.5.review-performance.1]`
- **§8's tier-1 disposition for `TestCheckpointBarrierRejectsWithoutFence` states no replacement assertion.**
  `[non-spec.5.review-test-coverage.1]`, carried forward by `[non-spec.6.review-test-coverage.1]`
- **Three weighed-and-not-filed items:** the fence and barrier resolve that misses, §8's tier gloss, and the tier-2 case with no named home.
  `[non-spec.6.review-mechanism.1]`
- **Whether §10.1.8 step 3 fixes the unit of barrier quiescence at the session,** which the design rests CODE-1's per-entry `barrierGate` on
  while SPEC-2 keeps §29.10's clause unanswered. `[spec.1.review-kubernetes.1]`
- **§29.10's quiescence-unit clause admits two remedies** with different consequences, and the round that found it picked neither.
  `[spec.2.review-fresh.1]`
- **UNVERIFIED: staged §10.1.8 step 1's assembly read.** The step's own quoted `SELECT session_id FROM coordination_lease ...` selects no
  generation. `[spec.3.review-mechanism.1]`

### Deferred

- DEFERRED [this review log, from `[spec.1.fix-G1.1]`]: the standing-context carrier bullet says "four spec mirrors and seven proto carriers"
  and enumerates seven. SCHEMA-1's target list is now the seven plus the operational-RPC `coordination_generation` field comments named in
  SPEC-2's closing paragraph. Its "there is no eighth" exclusions (`CoordinatorFenceResponse.last_fenced_generation` at proto:1465 has no
  leading comment; the `Checkpoint` RPC comment and `CheckpointStart.checkpoint_id` are session-neutral) are unaffected and stand. Its
  CORRECTED sub-clause about the twelve non-neutral comments is now applied and can be retired once SCHEMA-1's list is verified.
- DEFERRED [`non-spec-changes.md`, from `[spec.3.review-citations.1]`]: SCHEMA-1 (non-spec-changes.md:11-20) lists
  `CoordinatorFenceRequest.coordination_generation` among the comments that "take the wording SPEC-2 states for it", while SPEC-2 states
  that comment "keeps its wording" (spec-changes.md:497-498). Not filed: it is a no-op either way, and its remedy is in a file this loop
  may not edit.
- DEFERRED [`tests/claim-map.json`, from `[spec.3.review-edit-sites.1]`]: §28.4 states that "Every normative statement this section makes
  about a mechanism carries a row in the claim register at `tests/claim-map.json`", and a non-`WIRED` row "names, through a deferral
  identifier, the step that closes it" (spec/28_communication-channels.md:163-169); `.claude/rules/channel-naming.md` restates it as "the
  claim-register row in §28.4 for any part of the contract that does not yet hold in code". SPEC-2 stages §28.5.1, §28.6, and §28.8
  statements that do not hold in the shipped adapter until CODE-1 and CODE-2 land (per-session recording, the unset arm, barrier
  acceptance when the pod holds no value), while asserting "No §28.4 claim-register row moves ... that file is not opened by this
  proposal" (spec-changes.md, SPEC-2). That assertion answers whether a row moves rather than whether one must be added or restatused.
  What is true instead: the fence and barrier rows need an `ABSENT`-or-deferred status naming S3 and S5 for the interval between S1 and
  S5. It could not be filed there, because the remedy lands in `tests/claim-map.json`, which criterion (d) does not reach and that loop
  may not edit. The non-spec loop owns it. This supersedes the vaguer standing OPEN asking whether §28.4's rule obliges a claim-map row
  for the new §28.5.1 sentence.
- DEFERRED [`non-spec-changes.md`, CODE-4, from `[spec.3.review-performance.3]`]: CODE-4's migration 0181 replaces
  `CHECK (coordination_generation >= 0)` with `>= 1` (non-spec-changes.md:118-125), and migrations in this tree run as a Helm
  `pre-install,pre-upgrade` hook at weight -5, while 100% of the old gateway fleet is still serving. `pgstore.Create` is the tree's only
  production `INSERT INTO sessions` and names `coordination_generation` in its column list bound from the struct with no floor, so every
  old-binary insert writes a literal 0 and every `session.create` fails for the whole rolling window. §10.5 states the expand-contract
  rule for precisely this case: a constraint old-version replicas' writes violate may only be added after every replica runs the new code,
  in a separate migration file and a separate deployment. The summary's "CODE-4's migration and both session-store `Create` floors land in
  one commit" addresses in-commit ordering only and does not reach deploy ordering. What is true instead: 0181 needs the §10.5 phase
  split, or an explicit statement of why it is exempt. The remedy is entirely in the non-spec staging, so the spec loop could not land it.
  `[non-spec.5.review-performance.1]` recorded the three underlying facts but its only decision declined the lock and backfill cost rather
  than this; nobody has decided this one — EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16, :38-39;
  spec/10_gateway-internals.md:429-433; pkg/gateway/session/sessionstore/pgstore/pgstore.go:170, :177; the summary's "Watch out for"
  paragraph.

## Ledger

### [spec.1-3.*] · residue of run 2 and of run 3's rounds 1 and 2 · the obligations they still own

Their facts, watch-outs, decisions, and mistakes are in `## Standing context`. What is left here is what nothing
has closed. The non-spec lane has now run five times and landed most of the deliverable-side corrections this entry
carried; the bullets below are what it did not close, plus the items later rounds added.

- OPEN [from round 4]: whether §10.1.8 step 1's and §29.7 step 4's "the `CheckpointBarrier` message carries the
  current `coordination_generation`" should themselves become edit sites. The mirror lag makes "current" false on
  the healthy path for up to a sweep interval. SPEC-1 declares both non-sites on a positivity argument, which
  does not reach currency, and the wording that landed in step 1 is compatible with either answer. For a later
  round or the human reviewer.
- OPEN [from round 4]: SPEC-1's live rationale opens "in the ordinary false positive, where the barrier carries
  the generation the acquiring replica's compare-and-swap wrote". The sentence is sound, because it is
  conditioned on a target set read after the compare-and-swap; only "the ordinary" overclaims that this is the
  sole ordering. Drop the two words rather than rewriting the sentence.
- OPEN: SCHEMA-1 in the non-spec changes names two field doc comments and widens to the seven carriers the
  standing context enumerates. The record-and-reject carriers take one qualifier: the pod records the new
  generation against the session the fence names and from that point rejects every RPC carrying an older
  generation than the one it holds for that session, with the `FailedPrecondition` and `coordinator_handoff_stale`
  detail string they already name; the `CoordinatorFence` RPC comment additionally states the exemption as the
  first call for that session on this pod and scopes the cancellation and the reset to that session, and the
  `CoordinatorFenceResponse` comment takes the qualifier on its stale-fence and `gap_detected` sentences. The
  acceptance carriers validate against the last fenced generation for the session the request names, and under
  D7 state that a barrier naming a bound session the pod holds no fenced generation for is accepted and records
  no value. The `CoordinatorFenceRequest.coordination_generation` field comment keeps its wording.
- OPEN: the status file's scope bullet drops "and releases its coordinator-loss hold", and its closing paragraph
  replaces "The hold-state decision in §7 is genuinely open and is the substance of this change." with the
  settled position: D5 decides the hold's scope and what moves is the generation the hold reports. Two non-spec
  rounds have passed over it without closing it.
- OPEN: the test-side cascade of D6 lives outside a spec loop's editable set. TEST-1 and §8 need a co-tenant's
  first fence well above 1 producing neither a gap nor a stale rejection. The D6 negative arm is already pinned
  by landed `TestCoordinatorFenceGapDetected`, so §8 owes no new case for it. Four citations spell the exemption
  as per pod lifetime and must rescope in the same pass — EVIDENCE:
  `pkg/adapter/coordination_test.go:58-61`; `tests/testinfra/coordfixture/coordfixture.go:106-108`;
  `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16`;
  `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37`, the only production-side one. CORRECTED, twice, by
  round 5: this entry used to say the last two are "covered only by their files appearing in §9". §9 carries
  `coordination_test.go` and `coordfixture.go` with its rescope spelled out, but `coordinatorfence_test.go` and
  `coordinatorfence.go` appear in §9 nowhere at all, so those two sites are covered by nothing. Whoever next
  writes §9 should add the paths; under refuted class (k) a reviewer should not file them.
- OPEN: which replica's connection carries a multi-session pod's CH-ADAPTEREVENTS events when more than one
  replica holds a connection to the pod, and therefore whether two co-tenant sessions can be coordinated by two
  different replicas at all. `spec/28` records the specification as not stating it. The defect's premise rests on
  the answer and D5's residual cannot be removed until it is settled. Staged as a §6 non-goal. Note that a
  second replica's fence at a lower generation is rejected on the pod-wide gate today, and once it fences higher
  the first replica's RPCs are rejected on the same gate, so multi-coordinator co-tenancy does not arise in the
  shipped tree (`pkg/adapter/coordination.go:97-103`). D7's separate premise, the ordinary session's drain
  barrier, is reachable: the sweeper's `upsertMirror` gives a never-handed-off session a lease mirror row, so it
  is in the barrier-target set.
- OPEN [scope, from round 5]: whether D7, the counter baseline (SPEC-3, CODE-4, migration 0181), and the
  barrier-provenance reconciliation belong in this proposal at all. The per-session move (D1 through D6) drew no
  finding after round 1 and every finding since is downstream of those three additions. A scope decision for the
  human reviewer.
- OPEN [rationale prose, from round 5]: three live rationale sentences are known imprecise and were deliberately
  not filed, because none lands in `spec/`. The §10.1 baseline paragraph's "becomes unreachable by construction
  rather than by a floor" is falsified by the cache fallback's zero seed and may soften to "unreachable for any
  value the gateway reads successfully"; "the ack deadline stays the only failure arm §10.1.8 defines"
  contradicts the staged step-1 rejection arm in its own paragraph; and the twinned `summary.md:78-80` /
  `non-spec-changes.md:115-118` sentence about "either `Create` path" writing an explicit zero is true only of
  `pgstore`, since `memstore` cannot make a Postgres CHECK reject anything. All three belong to a prose or
  human-review pass.
- OPEN [from round 5]: `## 4. Detailed design` is still headed `**IMPLEMENTOR TO FILL THE BLANKS.**`. It names
  four derivable items each carrying a constraint, which is a different case from the §5 header round 6 filed, and
  no round has settled whether a converged proposal may keep it.
- OPEN: whether a superseded replica driving a `Checkpoint` stream against the live coordinator's quiesced pod is
  acceptable, and whether an accepted false-positive barrier is followed by a stream from that same replica or
  leaves the pod quiesced with no stream. It is bounded by the 90s barrier ack deadline and is already what the
  code does, so this proposal records it rather than fixing it. A human reviewer may want it as a §7 decision.
- OPEN: `spec/04` §4.1's Request Message Scope table declares `CoordinatorFenceRequest` pod-scoped
  (`spec/04_system-components.md:175`, `:188`) while `CheckpointBarrierRequest` is session-scoped (`:171`), and a
  tier-3 test pins the classification
  (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:38-43`). SPEC-1's step 3 states the
  same per-session reading and no staged entry names `spec/04`. After SPEC-1 the fence's generation effect is per
  session while its hold-exit effect stays pod-wide under D5, so the row may or may not still be right. The
  column is a declaration rather than a derivation ("because `session_id` appears on messages of both classes")
  and §4.1 already carries a worked precedent for a message whose scope needs explaining, the `ShutdownRequest`
  paragraph. Whether the declared classification survives is a question for the reviewer or a later round.
- OPEN: after the §1.3 recalibration the headline harm is availability churn plus unquiesced capture, and no
  bullet claims data loss. The rationale still stands on the false split-brain signal, the misattributed
  `coordinator_lost` generation, and the stale counter, but a reviewer may want the severity restated once at the
  top of §1 rather than only inside §1.3.
- OPEN [citations]: proposal 0080's section "1.14 Whether the adapter's hold state is partitioned per slot is
  still unstated" covers the same §29.10 bullet SPEC-2 stages for removal. Whichever lands second rebases;
  nobody has recorded the overlap.
- OPEN [mechanism]: SPEC-1 states the initial condition as "unset until that session's first accepted fence on
  that pod" while D6 states the unit as "the session's binding on the pod". The value lives on the slot registry
  entry, whose lifetime is the binding, so the two differ only if a session can unbind and re-bind on the same
  pod. If it can, D6's per-entry `last_fenced_generation` is lost on rebind and the pod re-enters the unset state
  staged step 3 makes permissive, which would be a fencing hole across a rebind. Nobody has settled whether the
  gateway ever re-binds the same session id onto the same pod after `releaseSessionSlot` runs on a resume failure
  path (`pkg/adapter/resume.go:69-141`; `pkg/adapter/slotsession.go`). Owner: a gateway-side reviewer reading
  `pkg/gateway/sessionserver` resume placement.
- OPEN: `spec/29` §29.2 step 11 records as unstated "whether that replica announces a generation on `CH-FENCE`
  before its first gateway-to-pod message for a pod it has just claimed"
  (`spec/29_communication-scenarios.md:204-206`). D6's unset-value model and §7's first open decision both turn
  on the answer being "it does not", and nothing staged reconciles the two. An unstated spec is consistent with
  either answer, so a finding built on pairing this with D6 will be refuted; the question is whether SPEC-1 owes
  that bullet a change.
- OPEN: does the genuinely new §28.5.1 sentence ("A fence for one session does not change the generation the pod holds
  for another") owe a `tests/claim-map.json` row under §28.4's rule that every normative statement about a mechanism
  carries one? SPEC-2 says no row moves, and nothing fails, because the register is generated from one document and
  `claim_register_test.go` is a schema gate. The fix lands in `tests/`, so it belongs to the code and test loop.
- OPEN: the staged §10.1.4 text names "the per-session `coordinator_lost` log line" as a spec artifact. The adapter
  emits it (`pkg/adapter/holdstate.go:225-228`) but no spec section introduces it, and §10.1.4 uses `coordinator_lost`
  only as a `session.terminated` reason. Judged not a finding because the staged sentence is itself the definition
  site; a later lens may disagree.
- OPEN [from non-spec round 2]: `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163` calls
  `srv.BarrierWaiting()`, which CODE-1 makes a per-session read. §9 lists the file and tier 0 compiles it, but
  CODE-1's prose enumerates the accessor call sites and omits this one. A completeness pass over that enumeration
  should name it; no step boundary is crossed, since it lands inside S3.
- UNVERIFIED: whether tier 1 alone is the right home for the sess-b-is-zero assertion, or whether the
  non-spec-changes §8 pinning case should also name it.
- UNVERIFIED: whether the shipped drain therefore never quiesces for an ordinary never-handed-off session, which
  is what the code implies. Nobody has run a tier-4 or tier-5 drain against a session that was never fenced. The
  implementation loop should confirm before treating the §10.1.8 reconciliation as documentation-only.
- UNVERIFIED: SPEC-1's staged step 3 reads "The pod accepts only RPCs whose generation matches the value it
  holds for the session the RPC names" and the next sentence carves out the unset case. Read as written, the
  "only" and the carve-out are in tension. A contradiction lens should decide whether the carve-out reads as a
  scoping refinement or as a self-negation; the citation lens judged it a wording call that resolves
  operationally.
- UNVERIFIED [d6-reset-ground]: D6's stated ground for the first-fence exemption is "a session that has never
  been fenced on this pod has accumulated no state for the gap path's reset to act on". The inference does not
  follow, since a session running for an hour under its original coordinator has never been fenced and has
  accumulated exactly the transient state clause (b) names. The exemption is still right, because the reset has
  no defined starting point with no recorded fence and running it would discard the original coordinator's work,
  so only the stated ground is wrong and no staged spec sentence carries it. A later fixer should restate the
  ground rather than defend it.
- UNVERIFIED [baseline-reachability]: staged §10.1's new sentence rests on the class "a session no replica has
  taken over", while §10.1's own existing bullet says a replica takes over by incrementing the generation and
  §10.1.2's sequence triggers on lease acquisition, which the creating replica also performs. Read that way the
  class is empty in pure spec terms. The standing context settles it the other way (lease acquisition for a fresh
  session happens before any pod exists) and §7's first open decision is framed on the settled reading, so it was
  not filed. A contradiction lens that wants to reopen it must argue against that bullet rather than against the
  tree — EVIDENCE: `spec/10_gateway-internals.md:30`, `:34`.
- UNVERIFIED: `upsertMirror` writes `row.CoordinationGeneration` from the pre-`RecordHandoff` List snapshot, so on
  the sweep that performs a takeover the mirror row records the value the takeover superseded and is corrected
  only on the next sweep. The implementation loop should check whether a barrier dispatched inside that window
  carries the stale value and is refused with `FailedPrecondition`.
- UNVERIFIED [wire-population]: two rounds disagree and neither corrects the other. Run 2's round 2 recorded that
  every operational request other than the fence and the barrier carries `coordination_generation` on the wire
  and its handler ignores it; run 3's feasibility lens recorded that the gateway sets `CoordinationGeneration` on
  exactly two outbound requests, `coordinatorfence.go:53` and `client.go:470`, leaving the proto field zero on
  every other adapter RPC. The newer reading is kept in `## Standing context` by implication and the older is
  retired. It matters because step 3's domain is every gateway-to-pod RPC and `tests/claim-map.json` carries an
  `UNWIRED` row per request that declares the field. A code-lane reviewer should settle which is true of the
  field's declaration as against its population.
- UNVERIFIED: whether the §28.5.1 `CH-BARRIER` Preconditions bullet ("The generation stamp and the fence
  acknowledgement that govern every gateway-to-pod RPC") is a sender-side statement, and so a non-site under the
  refuted precondition class, or a pod-side one that D7 falsifies. Read as sender-side and not reported. A
  mechanism lens should settle it.
- UNVERIFIED: `tests/claim-map.json:76-82` files `CheckpointBarrierRequest.coordination_generation` as `UNWIRED`
  with the note "no production reader compares it until the generation fence lands", while
  `pkg/adapter/coordination.go:223`/`:236` reads and compares it today. The row is wrong before this proposal and
  stays wrong after CODE-2, no gate catches it, and D7 changes the comparison. Owner: a claim-register or
  fidelity loop deciding whether the row moves to `WIRED` and whether it is a 0076 edit site.
- OPEN [from non-spec round 1]: why CODE-1 reaches tier 4. The compose-stack surface it names is not obvious
  from the deliverable, and the persisted-row assertion the design describes rides proposal 0060's harness at
  the tier §8 already names. Whoever next rewrites §8 either justifies tier 4 or drops it.
- UNVERIFIED [from non-spec round 1]: whether the persisted-row half of the tier-7a barrier case (two
  `session_checkpoint_meta` rows with distinct `checkpoint_ref`) belongs at tier 7a or rides proposal 0060's
  two-replica tier-8 harness. The adapter half, that each ack echoes its own stream's id and neither RPC waits
  on the other, is tier 1 plus a tier-7a `-race` case.
- UNVERIFIED [from non-spec round 2]: whether §8's tier-7a barrier case should state the 90s bound explicitly for
  a barrier whose session is deregistered mid-flight, where the per-entry gate blocks to the ack deadline and the
  pod-wide gate could be completed by an unrelated session's `Checkpoint` stream. Judged not a regression, since
  that accidental completion is the cross-link CODE-1 exists to remove. Round 5 added a tier-1 mid-flight
  deregistration bullet, which may or may not discharge it; a reliability lens should decide.
- OPEN [from non-spec round 3]: §8 stages a tier-3 case (the barrier accepted for a bound session with no
  recorded generation) and a tier-2 case (resume fence at 1, then a crash-takeover compare-and-swap to 2),
  and §9 names a file for neither while naming one for every other case, the new migrations file included.
  Round 3 filed the tier-3 half as its only finding; a non-spec fixer with the files open should add both
  §9 entries.
- UNVERIFIED [from non-spec round 3]: whether §8's tier-7a case ("two `CheckpointBarrier` RPCs accepted
  concurrently on one pod each receive their own stream's checkpoint id in the ack and neither waits on the
  other") can be written against real `Checkpoint` streams. The pod-level op lock queues a second co-tenant
  checkpoint rather than refusing it, so two real streams serialize while both complete; the assertion holds
  only if the case links and completes the two gates directly, the way landed
  `TestCheckpointBarrierAcksEchoedCheckpointID` does (`pkg/adapter/coordination_test.go:271-290`). A test
  lens or the implementor should confirm the case is written that way.
- UNVERIFIED [from non-spec round 3]: checklist S1 declares tier 11 while §8's own tier sentence enumerates
  0, 1, 2, 3, 4, 7a, and 8. Tier 11 does read `spec/29`, so S1 is the side that is right; round 6 worked the
  same mismatch up and declined to file it. A prose or bookkeeping pass owns whether §8's sentence gains 11.
- UNVERIFIED [moved from `[non-spec.4.review-edit-sites.1]` in compaction pass 16]: CODE-4's `.up.sql` changes two
  column defaults (`sessions.coordination_generation` and `coordination_lease.coordination_generation`) while the
  `.down.sql` sentence names one: "restores the `DEFAULT 0` and the `>= 0` check". §8's staged migration case asserts
  both defaults forward but only the check on the way back, so nothing would catch a down that reverses `sessions`
  alone. Judged below the bar (the lease column is always written explicitly from the session row, so a stale default
  is inert), but a one-word widening to "restores both `DEFAULT 0`s" would close it. Whoever next writes
  `non-spec-changes.md` should decide — EVIDENCE: non-spec-changes.md:100-104, :226-230.

### [non-spec.5-6.*] · residue of run 3's non-spec rounds 5 and 6 · the obligations they still own

Compaction pass 17 lifted those rounds' facts, decisions, watch-outs, and mistakes into `## Standing context`
and retired the entries themselves. What is left here is what nothing has closed. The `### Open` index still
names the retired entries by their own ids; every item it names is below, under the id it came from.

UNVERIFIED [`[non-spec.5.fix-G1.1]`]: whether the `pkg/adapter` external test package can stall `s.ops.Begin`
deterministically enough for the mid-flight deregistration case without a test-only hook. `driveCheckpointConc`
and `concurrentServer` drive two real streams, so the queue is reachable, but the case still has to hold the
first stream open while it deregisters the second's entry. The implementor of S6 should check before choosing
the file.

UNVERIFIED [`[non-spec.5.fix-design-G1.1]`]: whether any tier-7a or tier-4 case would also break under a
re-lookup implementation. None of the listed §8 cases drives a deregistration concurrently with a barrier or a
stream, but the tagged suites were not swept for an incidental one. Whoever writes the case should grep
`tests/tier7a_load_local` and `tests/tier8_chaos` for a `Shutdown` issued while a barrier is open before
assuming tier 1 is the only home.

OPEN [`[non-spec.5.review-client-surface.1]`, `[non-spec.5.review-applicability.1]`,
`[non-spec.5.review-citations.1]`, `[non-spec.5.review-mechanism.1]`]: the exemption is spelled per pod lifetime
at four sites, and the production one is `pkg/gateway/runtime/adapterclient/coordinatorfence.go:33-41` ("The
first fence on a pod's lifetime is always accepted"), which is in no §9 entry and no deliverable. Of the three
test-side sites, `pkg/adapter/coordination_test.go` and `tests/testinfra/coordfixture/coordfixture.go` are in
§9 and `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16` is absent from it entirely, so the
older claim that the unnamed sites "are covered only by their files appearing in §9" is false for that one.
Each sentence stays literally true after D6 and only becomes incomplete, which is why refuted class (k) keeps
it off the finding list; a pass that rescopes the other three should take all four in one edit, and whoever
next writes §9 should add the path.

OPEN [`[non-spec.5.review-applicability.1]`, restated by `[non-spec.6.review-mechanism.1]`]: §8's tier sentence
attributes "the registry state" to tier 2 while the next sentence pins the per-session fenced value, the hold
records, and the barrier-gate independence at tier 1, and no tier-2 case in the list covers the registry. S3
declares tier 2 on that basis. Harmless, because tier 2 is genuinely reached by the landed
`tests/tier2_component/slotrelease/revoke_double_teardown_test.go`, so the gloss is bookkeeping rather than a
wrong tier — EVIDENCE: non-spec-changes.md:137-142; implementation-checklist.md:10-14

OPEN [`[non-spec.5.review-applicability.1]`]: TEST-1 names `pkg/adapter/holdstate_test.go` among the files its
cases land in while §8 assigns that amendment to the step landing CODE-3. The tension is real —
`holdstate_test.go:713` asserts `LastGeneration != 7` for both `sess-a` and `sess-b`, so S3 turns tier 1 red
unless S3 amends it — and it was not filed because §8 resolves the ownership in one explicit sentence, which is
the "already handled in its own text" ground this loop has refuted four findings on.

UNVERIFIED [`[non-spec.5.review-citations.1]`]: SPEC-1 calls the "Generation counters" bullet "§10.1's" while
the bullet lives in §10.1.1 (`spec/10_gateway-internals.md:30`, under the `#### 10.1.1` heading at `:5`), and
§9's `spec/10` parenthetical lists §10.1, §10.1.2, §10.1.4, and §10.1.8 without §10.1.1. Judged containment
rather than misattribution. A lens that wants the parenthetical to name the subsection an edit lands in should
decide.

OPEN [`[non-spec.5.review-docs-alignment.1]`]: the docs lens has now returned nothing twice on a surface that
has been re-derived eight times. The per-lens re-derivation is what costs rounds rather than the surface.

OPEN [`[non-spec.5.review-feasibility.1]`]: CODE-1 declares tier 2 and checklist S3 gates on it, but no tier-2
package touches the accessors CODE-1 changes: `coordfixture`'s consumers are the integration, load_local, and
chaos trees, and the three tier-2 packages that build an `adapter.Server` (slotrelease, warmlayout,
translators) never call `LastFencedGeneration`, `BarrierWaiting`, or `isQuiescedForBarrier`. Running a tier
with no case is harmless; if a later round tightens the tier lists this is the one to drop.

OPEN [`[non-spec.5.review-feasibility.1]`]: §8 says "Tier 4 covers the same flow across the gateway, the session
store, and the pod" for D7's acceptance and names no case, no file, and no step, while S5 (CODE-2) declares
tiers 0, 1, 3, and 7a. The sentence is either a claim about a case that does not exist or a loose pointer at
the tier-4 co-tenant fence bullet, which is a different flow.

UNVERIFIED [`[non-spec.5.review-fresh.1]`]: nothing in §8 or §9 gives the tier-2 resume-fence-then-takeover case
a file, and no existing tier-2 test constructs a `coordfixture` pod (`coordfixture` appears only under tiers 4,
7a, and 8). It is constructible at tier 2 (memstore plus a Redis container plus the untagged in-process
adapter), but nobody has checked whether the tier-2 coordination suite is the intended home.

OPEN [`[non-spec.5.review-fresh.1]`, `[non-spec.5.review-kubernetes.1]`]:
`migrations/0164_coordination_lease.up.sql:40-45`'s column comment states "the adapter rejects a barrier whose
generation does not match its last fenced value", which D7 narrows. Landed migrations are not edited, so this is
a note for a later reader rather than an edit site, and CODE-4's 0181 correctly changes the column's DEFAULT
without touching 0164's text.

OPEN [`[non-spec.5.review-mechanism.1]`]: `coordinationState` embeds its own `mu` as its first field
(`pkg/adapter/coordination.go:26`), so CODE-1's move of `coordinationState` onto the slot registry entry moves
the mutex too and settles §7's third open decision by construction, while §4's fourth bullet still frames
"whether the pod-wide `coord.mu` becomes per-entry or stays one lock" as open. A fixer touching CODE-1 should
say which of the two it means.

UNVERIFIED [`[non-spec.5.review-operational.1]`]: `tests/claim-map.json:75-82`'s
`CheckpointBarrierRequest.coordination_generation` row is `UNWIRED` with the note "no production reader compares
it until the generation fence lands", while `pkg/adapter/coordination.go:236-239` compares exactly that field
today. The row looks wrong before this proposal and stays wrong after CODE-2, and
`claim_register_proto_agreement_test.go` does not catch it (`fenceReadersExempt` holds `CoordinatorFenceRequest`
alone, and the coverage half only requires that a row exist). Pre-existing drift; a claim-register or fidelity
loop should decide whether the row moves to `WIRED`.

OPEN [`[non-spec.5.review-performance.1]`]: nobody on this proposal has looked at deploy-time ordering at all.
Grepping all proposal files for `expand-contract`, `rolling deploy`, `mixed-version`, `pre-upgrade`, and `10.5`
returns nothing. The concrete consequence is now carried as a `DEFERRED` in `## Standing context`.

OPEN [`[non-spec.5.review-test-coverage.1]`, carried forward by `[non-spec.6.review-test-coverage.1]`]: §8's
tier-1 disposition for `TestCheckpointBarrierRejectsWithoutFence` is the only landed-test disposition in §8 that
names no replacement assertion ("the step landing CODE-2 amends it in the same commit rather than leaving tier 1
red"), where the memstore, holdstate, and uploaddriver dispositions each state what the amended case asserts.
Pass 7 records the intended assertion at `spec-changes.md:841-843`. Two test-coverage lenses declined to file it
because it invites the "already handled in its own text" refutation. It is a fixer's job: whoever next writes §8
should copy the assertion in and close this.

OPEN [`[non-spec.5.review-test-coverage.1]`]: the three D7 cases pass 7 enumerated (`spec-changes.md:848-852`)
are still absent from §8 and nothing withdraws them: the tier-1 co-tenancy case (A fenced at 6, B never fenced,
accept B's barrier and refuse A's at 5), the tier-3 stale-refusal arm, and the tier-8 "crash takeover whose fence
has not yet landed does not lose the draining replica's barrier". They were not filed because the stale arm is
covered by landed tests, the co-tenancy case is caught indirectly by §8's gap bullet if `initialized` is left
pod-wide, and the tier-8 one is doubtful after pass 12 withdrew the provenance universal. A later round that
wants them must argue past those three reasons rather than re-citing pass 7.

OPEN [`[non-spec.6.review-mechanism.1]`, weighed and not filed, strongest first]: (1) CODE-1 prescribes a resolve
of the slot entry AFTER `checkSessionBound` on the fence and barrier paths (`pkg/adapter/coordination.go:89`,
`:216`), which is two lookups with `s.mu` released between, while the same paragraph establishes for the stream
that "That guard does not make a second lookup safe"; the proposal states nothing about what a fence or barrier
resolve that misses does. The natural implementation (`if !ok { return FailedPrecondition }`) is fail-closed and
no stated claim becomes false, and the symmetric fix is to have `checkSessionBound` return the `*slotState`.
(2) §8's tier gloss names a subject its own next paragraph pins at tier 1 and omits the tier-2 resume-then-takeover
case that §8 does stage. (3) That tier-2 case names no file and §9 lists none for it, the IMPLEMENTOR'S CHOICE
paragraph being scoped to tier-1 `pkg/adapter` cases. (4) §8's tier-8 and tier-7a S3/S4 split enumerates only the
`pod.LastFenced` reads while `pod.Fence` and `pod.StaleRPCRejected` also take CODE-1's session key; the
disjointness claim still holds because all of those sites sit in the subtests seeded at 1 explicitly.


### [spec.1.fix-G1.1]

DECISION: Closed the "twelve field comments are neutral non-sites" finding by adding them to SPEC-2's carrier list and deleting their closing consequence clause, replacing the span from "A pod validates" onward with "A pod validates the generation on every gateway-to-pod RPC against the value it holds for the session the RPC names, and rejects a request whose generation does not match it (§10.1)." — BECAUSE only the consequence clause is falsified by SPEC-1's staged §10.1.2 step 3 unset clause, and deleting it leaves the rule stated once (§10.1.2 step 3 plus the fence and barrier carriers) instead of a dozen conditional copies to keep in sync — ALTERNATIVES: re-conditioning each clause ("once another coordinator has fenced the session on it"), rejected as accretion; stating a true ground for exclusion, rejected because none exists; deleting the comments outright, rejected because their first sentence carries the field's meaning for adapter authors.
FACT: the carrier set is exactly the `gateway's view of the active` sites minus `CheckpointBarrierRequest`. `grep -n "gateway's view of the active" schemas/lenny-adapter.proto` returns thirteen sites; `:1477` is `CheckpointBarrierRequest` and is already a named barrier carrier. — EVIDENCE: schemas/lenny-adapter.proto:969, :995, :1046, :1070, :1091, :1114, :1172, :1305, :1393, :1477, :1531, :1576, :1618
WATCHOUT: a list built from the shared phrase `cannot drive the pod` returns only eleven; `ShutdownRequest`'s clause closes `cannot tear the session down (§10.1)`. — EVIDENCE: schemas/lenny-adapter.proto:1622
WATCHOUT: `AttachRequest` (`:995-1001`) and `CheckpointRequest` (`:1172-1178`) carry a trailing sentence about the generation being on every frame of the stream. It is not part of the gate and must survive the edit; do not replace the whole comment block on those two. — EVIDENCE: schemas/lenny-adapter.proto:999-1001, :1176-1178
FACT: `CoordinatorFenceRequest.coordination_generation` (`:1449-1451`) is not in the "gateway's view" family; SPEC-2 says it keeps its wording, while SCHEMA-1 lists it as a carrier. That is consistent because SCHEMA-1 now defers to SPEC-2 for each carrier's wording rather than asserting each one changes. — EVIDENCE: 0076...spec-changes.md SPEC-2 fence-carrier paragraph
DEFERRED [/home/ec2-user/lenny/proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.review-log.md]: the standing-context carrier bullet says "four spec mirrors and seven proto carriers" and enumerates seven. SCHEMA-1's target list is now the seven plus the operational-RPC `coordination_generation` field comments named in SPEC-2's closing paragraph. Its "there is no eighth" exclusions (`CoordinatorFenceResponse.last_fenced_generation` at proto:1465 has no leading comment; the `Checkpoint` RPC comment and `CheckpointStart.checkpoint_id` are session-neutral) are unaffected and stand. Its CORRECTED sub-clause about the twelve non-neutral comments is now applied and can be retired once SCHEMA-1's list is verified.
USEFUL [standing context, carrier bullet]: the CORRECTED clause at review-log.md:70-75 had already derived this finding's whole content, including the `ShutdownRequest` grep trap, which saved re-deriving the carrier set from scratch.
FACT: no implementation-checklist change is owed. Checklist step S2 names SCHEMA-1 generically and the enlarged comment list adds no tier surface; the tier-0 `TestProtoStubsMatchGeneratedOutput` obligation already covers field and message comments landing in `lenny-adapter.pb.go`.


### [spec.1.fix-design-G1.1]

DECISION: SPEC-2's closing wire-mirror paragraph stops declaring the twelve remaining `coordination_generation` field comments non-sites and hands them to SCHEMA-1 as carriers, with one wording for all twelve: keep sentence 1, and replace the "A pod validates ... cannot drive the pod (§10.1)." span with "A pod validates the generation on every gateway-to-pod RPC against the value it holds for the session the RPC names, and rejects a request whose generation does not match it (§10.1)." — BECAUSE the deleted consequence clause is the falsified half (staged §10.1.2 step 3 says a session with no fenced generation is not rejected on generation grounds, which D6 makes the ordinary state), while validation-plus-unit is true on every carrier; deleting the consequence removes twelve copies of a rule that would otherwise have to stay in sync with §10.1 and with the fence/barrier carriers — ALTERNATIVES: (1) keep the consequence and condition it ("once another coordinator has fenced the session on this pod") — twelve conditional restatements of a rule stated elsewhere, i.e. hair; (2) find a true ground for excluding them — none exists, the clause is false for the D6-ordinary class; (3) delete the comments entirely — loses the field's meaning for external adapter authors.

FACT: the twelve are exactly the 13 "gateway's view of the active" sites minus `CheckpointBarrierRequest` (:1477, already a carrier); `CoordinatorFenceRequest` (:1449) has different wording and is separately handled. Message names and comment start lines: SendMessageRequest 969, AttachRequest 995, RotateCredentialsRequest 1046, ExtendCredentialLeaseRequest 1070, RevokeCredentialsRequest 1091, InterruptRequest 1114, CheckpointRequest 1172, SignalDeadlineRequest 1305, ResumeRequest 1393, ExportPathsRequest 1531, ReportUsageRequest 1576, ShutdownRequest 1618 — EVIDENCE: schemas/lenny-adapter.proto:969,995,1046,1070,1091,1114,1172,1305,1393,1531,1576,1618

WATCHOUT: two of the twelve carry a trailing sentence after "(§10.1)." that is unrelated to the generation gate and must survive the edit: `AttachRequest` "It is carried on every frame of the stream rather than on the opening frame alone, for the same reason session_id is." (:995-1001) and `CheckpointRequest` "It sits outside the `msg` oneof because the fence applies to every frame the gateway sends on the stream rather than to the opening frame alone." (:1172-1178) — EVIDENCE: schemas/lenny-adapter.proto:999-1001, :1176-1178

WATCHOUT: `ShutdownRequest`'s clause closes "cannot tear the session down (§10.1)", so a list built by grepping "cannot drive the pod" returns eleven and silently drops it — EVIDENCE: schemas/lenny-adapter.proto:1622

FACT: the carrier count in the review log's standing context ("seven proto carriers ... There is no eighth") is a count of the fence/barrier carriers only; after this fix SCHEMA-1's list is nineteen. The "no eighth" exclusions (`CoordinatorFenceResponse.last_fenced_generation` at :1465 has no leading comment; the `Checkpoint` RPC comment and `CheckpointStart.checkpoint_id` are session-neutral) are unaffected — EVIDENCE: schemas/lenny-adapter.proto:1465

FACT: SCHEMA-1 already states that `make generate-proto` regenerates the committed stubs in the same commit and that a field comment lands in `pkg/proto/adapter/v1/lenny-adapter.pb.go`; twelve more field comments change nothing in that statement, so no checklist or tier line moves — EVIDENCE: proposals/0076_.../0076_....non-spec-changes.md:12-19

USEFUL [standing context, "gap reset and record-and-reject rule have four spec mirrors and seven proto carriers"]: its CORRECTED clause derived this finding two rounds before it was filed and named the ShutdownRequest trap; it saved the whole re-derivation.


# Post-fix verification, spec round 1 (G1)

VERDICT: no findings. The G1 fix landed; no drift; every new citation resolves.

FACT [landed]: SPEC-2's closing wire-mirror paragraph no longer rests on the neutrality ground.
`...spec-changes.md:507-533` now names the twelve operational-RPC `coordination_generation` field
comments as carriers, states why the consequence clause is false against SPEC-1's staged §10.1.2
step 3 unset clause (`:159-161`), and gives one replacement sentence for the span from "A pod
validates" to the end of the consequence clause. Option (a) of the supplied design.

FACT [citations]: all twelve proto ranges in the new text are exact. `grep -n "gateway's view of the
active" schemas/lenny-adapter.proto` returns 969, 995, 1046, 1070, 1091, 1114, 1172, 1305, 1393,
1477, 1531, 1576, 1618; 1477 is the already-named `CheckpointBarrierRequest` carrier. The closing
clause sits at 973, 999, 1050, 1074, 1095, 1118, 1176, 1309, 1397, 1535, 1580 ("cannot drive the
pod") and 1622 ("cannot tear the session down"), so each cited end line (`:995-1001`, `:1172-1178`,
`:1618-1622`, etc.) covers the trailing continuation lines it claims. The Pass 22 record's citations
(`:969-973`, `:1618-1622`) resolve the same way.

FACT [drift, both in-scope sites edited and consistent]:
- `...non-spec-changes.md:11-20` now lists the whole carrier set, in the same order SPEC-2 states it
  (fence RPC comment, the two message comments, the fence request field comment, barrier RPC
  comment, barrier message comment, barrier field comment, then the twelve by message), which is
  what `spec-changes.md:532-533` asserts about it. The stub-regeneration sentence and its
  `Makefile:91-94` / `proto_no_drift_test.go:70` citations are untouched and still true.
- `...summary.md:62-68` no longer describes SCHEMA-1 as omitting the five fence/barrier comments;
  it now states SCHEMA-1 names the whole carrier set, and keeps the status-file corrections.

FACT [drift sweep, unpredicted parallels]: no other live statement of SCHEMA-1's scope exists.
`grep -n "not edit sites\|neutral"` in spec-changes returns only :257 and :408 (unrelated spec-side
non-site paragraphs) and :728, which is inside the frozen "Resolved in adversarial review" pass
record and keeps its words by the historical-record convention. The implementation checklist's S2
line (`...implementation-checklist.md:7-8`) names SCHEMA-1 generically and stays true. The status,
problem-statement, and deviations files carry no carrier enumeration.

WATCHOUT: the review log's standing-context bullet (`...review-log.md:63-75`) still reads "seven
proto carriers", and its ledger OPEN (`:400`) still describes the remedy as widening SCHEMA-1 to
those seven. Both are now short by the twelve. Not reported as a finding: the review log is the
loop's own record and outside the fixer's write scope, and the fixer recorded the deferral. The loop
should refresh that bullet's enumeration when it next compacts.

NOTE [not a finding]: SCHEMA-1's list includes the `CoordinatorFenceRequest.coordination_generation`
field comment, which SPEC-2 says "keeps its wording" (`spec-changes.md:501-503`). SCHEMA-1 defers to
SPEC-2 for each carrier's wording, so an implementor reads "unchanged" there and makes no wrong
edit.


### [spec.1.review-applicability.1]

DECISION: filed exactly one finding, the stale `**IMPLEMENTOR TO FILL THE BLANKS.**` header over
`## 5. Proposed changes` (spec-changes.md:132-135) — BECAUSE it is round 6's finding, verified live and
unfixed in run 4 (run 3 ended before a fix round ran), and it is the one applicability defect left: a
maintainer told the staging is "indicative targets ... written during convergence" will not apply
SPEC-1/2/3 as written — ALTERNATIVES: rejected filing the §4 header (standing context: separate OPEN, its
four items each carry a constraint), the §4 "either order" claim and the three imprecise rationale
sentences (routed to human review), and the SPEC-2 closing paragraph calling the twelve remaining
`coordination_generation` field comments "neutral" (contested: refuted classes (e) and (f) both bear on
whether the unset clause really falsifies them, and the remedy is SCHEMA-1's list in the non-spec file,
which this loop may not edit).

FACT: every anchor SPEC-1/SPEC-2/SPEC-3 quote still resolves verbatim and uniquely at the cited line, in
run 4 as in rounds 4-6. Re-verified this pass: spec/10:30, :37, :38, :40, :41, :58, :60, :183, :184, :198;
spec/28:330-331, :333-334, :1675, :1679-1681, :1683-1685, :1805, :1806, :1807, :1808; spec/29:1150-1152,
:1186, :1274, :1322-1326, :1461-1470, :1519-1543; spec/04:200. The four `prior coordinator's RPCs are still
accepted` sites are spec/28:331, :1685, :1807 and spec/29:1325 and all four are staged — EVIDENCE:
grep over the tree returns no fifth outside `proposals/`.

FACT: the §29.10 bullet-removal is safe against every gate that names §29.10. The three that do are
`tests/spec-map-exceptions.yaml:388` with `tests/tier0_static/spec_map_exception_blocker_retention_test.go:65`
(section-level exception row, blocker R7, indifferent to the section's sentences),
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:84` (asserts only §6.2/§7.2/state-machines.md
sub-state text), and `tests/tier11_docs/successor_pointer_test.go:55`. None string-matches a sentence SPEC-2
rewrites — EVIDENCE: tests/tier0_static/spec_map_exception_blocker_retention_test.go:60-70.

FACT: the §10.1.4 Observability edit costs no docs or metrics site. `coordinator_connection_lost` and
`last_generation` occur nowhere under `docs/`, `charts/`, or `pkg/alerting/`; the fields the staged sentence
describes exist in code as `started_sessions` and `last_generation` — EVIDENCE: pkg/adapter/holdstate.go:128-132,
and `docs/reference/metrics.md:303-312` lists no coordination log field.

WATCHOUT: `## 4. Detailed design`'s marker and `## 5. Proposed changes`'s marker are the same string on
lines 107 and 134 of the spec-changes file. A grep-driven fix that deletes both lands the §4 OPEN as a
side effect, which the standing context says not to merge — EVIDENCE:
0076_...spec-changes.md:107, :134.


### [spec.1.review-citations.1]

USEFUL [standing context, "gap reset ... four spec mirrors and seven proto carriers"]: its CORRECTED clause
("the other twelve `coordination_generation` field comments are session-scoped but not neutral ... so SPEC-2's
ground for excluding them is false") is the one live, unapplied correction I found; the staged text at
spec-changes.md:507-509 still carries the refuted ground. That saved a whole proto sweep.
FACT: the twelve remaining carriers are the request-field comments on `SendMessageRequest` (:973),
`AttachRequest` (:999), `RotateCredentialsRequest` (:1050), `ExtendCredentialLeaseRequest` (:1074),
`RevokeCredentialsRequest` (:1095), `InterruptRequest` (:1118), `CheckpointRequest` (:1176),
`SignalDeadlineRequest` (:1309), `ResumeRequest` (:1397), `ExportPathsRequest` (:1535),
`ReportUsageRequest` (:1580), and `ShutdownRequest` (:1622). Eleven close "so a replica that has lost
coordination cannot drive the pod (§10.1)"; `ShutdownRequest` closes "cannot tear the session down". Derived
with `awk '/^message /{m=$2} /cannot drive the pod|cannot tear the session down/{print NR": "m}'`, which is
faster and complete where a grep on the common phrase alone returns eleven.
EVIDENCE: schemas/lenny-adapter.proto:969-973, :1617-1622

FACT: every other citation in the staged SPEC-1, SPEC-2, and SPEC-3 text resolves. Re-verified this run,
against the tree rather than against an earlier round's inventory: spec/10:30, :37, :38, :41, :57, :58, :60,
:62, :183, :184, :198; spec/28:237-240, :251-253, :291-296, :314-315 (CH-FENCE Messages), :330-331, :349-353,
:361-365, :1675, :1679-1681, :1683-1685, :1805, :1806, :1807, :1808; spec/29:1150-1152, :1186, :1274, :1301
(step 7), :1322-1326, §29.10's four bullets; spec/04:200 (inside §4.2, 192-230); the proto ranges :153-162,
:165-179, :1442-1446, :1449-1451, :1455-1462, :1469-1474, :1475-1483, :1477-1479; and the code sites in D5,
D6, D7 and §7 (holdstate.go:39-44, :90-100, :107-112, :119, :172-176, :192; adapterevents.go:80-96;
coordination.go:29-32, :89, :92, :93-94, :99, :108, :216, :223, :224-226, :228-231, :236-239;
slotsession.go:267; coordfence.go:147-153; coordination/coordination.go:399, :430, :463-468; barrier.go:
229-246; wiring.go:49, :51-53, :104-114; httpsurface.go:592-599; prestop.go:390-397, :395, :510;
start.go:3975, :4067, :4233, :4237; seams.go:155-160, :233; migrations/0050:38-39). Section boundaries used:
spec/10 §10.1.1=5-31, §10.1.2=32-42, §10.1.4=53-63, §10.1.8=177-199; spec/29 §29.7=1142-1243,
§29.8=1244-1343, §29.10=1424-end.

FACT: `sessionGenerationReader.CoordinationGeneration` reads the authoritative session row through
`sessionstore.Store.Get`, not the `coordination_lease` mirror, so SPEC-1's "a session row can no longer carry
a non-positive value" is the right justification for deleting the coordfence floor. The mirror is written
from `row.CoordinationGeneration` and only read on the barrier target path.
EVIDENCE: cmd/lenny-gateway/main.go:375-381, pkg/gateway/coordination/coordination/coordination.go:430,
pkg/gateway/coordination/barrier/wiring.go:104-114

WATCHOUT: the symmetry objection a verifier will raise against the finding above. `spec/10:30` ("if the
generation is stale, the pod rejects the request ... This prevents split-brain") and `spec/28:237-240` carry
consequence clauses of the same kind, and SPEC-1 and SPEC-2 explicitly adjudicate both as standing. The
finding is therefore framed on the falsifiable half — the staged sentence says the comments "describe the
validation neutrally", and they state an outcome — rather than on whether the outcome clause is wrong. Do not
widen it into "spec/10:30 is also an edit site"; that was adjudicated and would re-open a settled sentence.


### [spec.1.review-client-surface.1]

DECISION: Filed exactly one finding, SPEC-2's closing "remaining `coordination_generation` field comments ... describe the validation neutrally" paragraph (spec-changes.md:507-509) — BECAUSE the twelve comments are not neutral: eleven close "so a replica that has lost coordination cannot drive the pod (§10.1)" and `ShutdownRequest`'s "cannot tear the session down", which staged step 3's unset clause (spec-changes.md:159-161) falsifies for the session class D6 makes ordinary — ALTERNATIVES: rejected filing `spec/04` §4.1's `CoordinatorFenceRequest` pod-scoped row (:175, :188) because D5 leaves the fence a genuine pod-wide effect (hold exit), so "stays pod-scoped" is defensible and prior rounds declined; rejected `spec/04:712`'s "Precondition for any subsequent operational RPC" under refuted class (a).

FACT: the client-surface sweep outside the proto returns nothing. `coordination_generation` appears in `docs/` only at `concepts.md:101` (no unit, no baseline) and in the operator/metrics docs without a unit; `sdks/`, `charts/`, `docs/api/*` carry it nowhere; there is no `pkg/gateway/openapi` directory in this tree — EVIDENCE: `docs/getting-started/concepts.md:101`; `docs/reference/metrics.md:307-312`; `docs/reference/adapter-contract.md:68-69`.

FACT: `spec/15` states nothing about the generation, the fence, or the barrier gate, so the runtime-author/external-API surface is reached only through `schemas/lenny-adapter.proto` and `spec/28` §28.7's row for it — EVIDENCE: `spec/28_communication-channels.md:1774` (the proto's §28.7 row, which states no generation semantics).

FACT: §28.4's claim register is the file `tests/claim-map.json` rather than a table inside `spec/28`, so a new-normative-statement register row is never a spec-lane edit site — EVIDENCE: `spec/28_communication-channels.md:161-165`.

USEFUL [standing context, "The gap reset and the record-and-reject rule have four spec mirrors and seven proto carriers"]: its CORRECTED clause about the other twelve field comments is exactly the finding above and saved the whole proto sweep; the instruction "read the comments rather than the proposal's description of them" is what produced it. Keep it until SCHEMA-1's list is settled.

FACT: every proto citation in the staged spec text resolves verbatim — `:153-162`, `:165-179`, `:1442-1446`, `:1449-1451`, `:1455-1462`, `:1469-1474`, `:1475-1483`, `:1477-1479`. Re-derived this run; do not re-derive.


### [spec.1.review-docs-alignment.1]

DECISION: returned an empty findings list for the documentation-alignment lens on the staged spec edits — BECAUSE every behaviour SPEC-1/2/3 changes (per-session fenced generation, the D7 barrier acceptance, the `coordinator_connection_lost` generation drop, the per-session `coordinator_lost` generation, the counter baseline of 1) has no describing surface in `docs/`, and every accepted or deferred failure mode I could name already lands in staged spec text or is an already-adjudicated OPEN — ALTERNATIVES: filing the absence of an "Edge cases and accepted failure modes" section (rejected: `.claude/skills/change-proposal/SKILL.md` names no such section and no proposal in `proposals/` carries one, so its absence is a format complaint rather than a defect); refiling the cache-fallback literal-zero barrier refusal (rejected: refuted class (j)); refiling the D7 superseded-replica quiescence residual (rejected: round 6 declined it and it stands as a §7/human OPEN).

FACT: the whole `docs/` surface touching this change is unit-neutral and states no scope, baseline, or gate, so a per-session generation and a baseline of 1 leave it true. The complete hit set for `coordination_generation|coordinator_*|CheckpointBarrier|CoordinatorFence|generation` under `docs/` is `getting-started/concepts.md:101`, `getting-started/architecture.md:173`, `reference/adapter-contract.md:68,:69,:96`, `reference/metrics.md:40,:197,:307,:309`, `operator-guide/upgrades.md:47-54`, `reference/glossary.md:54`. None names the pod-side unit of `last_fenced_generation`, the barrier's generation gate, or the counter's starting value — EVIDENCE: docs/reference/metrics.md:307 `| \`lenny_coordinator_handoff_stale_total\` | Counter | -- | Generation-stale rejection during handoff. |`; docs/getting-started/concepts.md:101.

FACT: `coordinator_connection_lost` occurs in the whole tree's spec/docs/schemas/charts surface at exactly two sites, both staged by this proposal, so dropping its generation field reaches no unstaged mirror — EVIDENCE: `spec/10_gateway-internals.md:60`, `spec/29_communication-scenarios.md:1274`; `grep -rn coordinator_connection_lost spec/ schemas/ charts/ docs/` returns nothing else.

FACT: `coordination_generation` appears in `schemas/` only in `lenny-adapter.proto` and in `charts/` not at all, so SCHEMA-1 is the complete non-spec carrier set and there is no chart or OpenAPI mirror of the column default — EVIDENCE: `grep -rln coordination_generation schemas/ charts/` returns `schemas/lenny-adapter.proto` alone.

FACT: `spec/README.md` lists `spec/29` only at heading granularity (`:280-290`), so SPEC-2's removal of a bullet from §29.10's "What the specification does not state" list has no README edit site — EVIDENCE: spec/README.md:290.

USEFUL [Standing context, "Derived inventories"]: its eight-site `docs/` enumeration and its statement that no alert, runbook, or tier-11 test is reached matched my own independent sweep exactly and saved a full re-derivation. Its note that `upgrades.md`'s "the controller sends a `CheckpointBarrier`" is pre-existing drift for a docs loop is confirmed at docs/operator-guide/upgrades.md:49 and correctly kept out of this loop's scope.


### [spec.1.review-edit-sites.1]

DECISION: filed exactly one finding, the `spec/04` §4.1 Request Message Scope row for `CoordinatorFenceRequest` — BECAUSE the standing context's refuted-class (c) explicitly says it does not cover that row and records it as an unadjudicated OPEN, and SPEC-3 already opens `spec/04` so the remedy lands in the staged spec edits — ALTERNATIVES: rejected filing `spec/10:157` ("the coordinator's fenced generation at intent-row INSERT time") and `spec/28:390` (CH-PODHEALTH's "does not state how that rule applies to a probe") as sites: both carry the same tension today with a never-fenced session, so the proposal does not make them wrong; rejected `spec/04:712`'s "Precondition for any subsequent operational RPC", which is the sender-side class (a) already refuted.
FACT: my own independent sweep reproduced the standing context's derived inventory rather than extending it. `coordination_generation` outside `spec/10|28|29` resolves to `spec/04:200,:323,:461,:711,:712`, `spec/07:93,:215,:398`, `spec/12:160`, `spec/16:199,:208`, `spec/18:238`, `spec/28:991`, `docs/getting-started/concepts.md:101`, and the adapter proto; none states a unit, a baseline, or a compared value. No sessions-table DDL, no migration inventory, and no default value for the counter exists anywhere under `spec/`, `docs/`, or `charts/`, so SPEC-3's §4.2 sentence is the whole spec surface of the baseline — EVIDENCE: `grep -rn coordination_generation spec/ docs/ schemas/ charts/`; `grep -rn "DEFAULT 0" spec/` returns only `spec/25`.
FACT: `spec/16` needs nothing. The generation appears in the observability surface only as the `coordinator.handoff` span attribute (`spec/16_observability.md:366`) and inside the `lenny_coordinator_handoff_stale_total` and partial-manifest-supersede descriptions (`:183`, `:199`), none of which fixes a unit or a compared value — EVIDENCE: spec/16_observability.md:183, :199, :366.
FACT: the `coordinator_connection_lost` / `coordinator_lost` records have exactly two spec carriers and both are staged: `spec/10_gateway-internals.md:60` and `spec/29_communication-scenarios.md:1274`. The CloudEvents catalog's `session_terminated` row carries `session_id`, `reason`, `terminatedBy` and no generation, so dropping the pod-level generation reaches no event schema — EVIDENCE: docs/reference/cloudevents-catalog.md:71.
USEFUL [Standing context, refuted classes]: class (k) (no missed-site finding over `tests/` or `pkg/`) and the derived `docs/` inventory each saved a sweep; class (a) stopped me from filing `spec/04:712` and `spec/28:237-240`.
UNVERIFIED: whether the §4.1 `pod` class for `CoordinatorFenceRequest` should flip to `session` or be kept with an explanatory paragraph. The tier-3 suite reads the classification as an addressing statement ("every request message §4.1 addresses to one session", `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:36-43`), while D5 leaves the fence one genuinely pod-wide effect, the hold exit. A human reviewer or the fixer should pick; the finding asks only that the staged edits adjudicate it rather than leave the file half-opened.


### [spec.1.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action-feasibility lens — BECAUSE every action the
staged spec text assigns is one the named component can perform with data it can see, and every anchor and
code citation I re-opened resolved verbatim — ALTERNATIVES: I considered and rejected filing (i) SPEC-2's
"the remaining `coordination_generation` field comments ... describe the validation neutrally" claim, which
is false against `schemas/lenny-adapter.proto:969-1623` but is already ledgered as a non-spec-lane
correction and whose remedy is SCHEMA-1's target list, and (ii) the staged §28.6 CH-FENCE arm's silence on a
fence carrying a generation *equal* to the held one, which is below the bar (see WATCHOUT).

FACT: the four §28.8 rows are single physical lines with pipe-separated cells, so `sed`/`grep` on a line
number shows only the first ~1200 columns and the "Holder of the exclusivity constraint changes" cell is
column 4. Read it with `awk -F'|' '{print $5}'` on the row's line number or the quoted clause looks absent —
EVIDENCE: spec/28_communication-channels.md:1805-1808

FACT: every anchor SPEC-1 and SPEC-2 quote still resolves at the line the staged text cites, re-checked in
run 4 round 1 against spec/10 (30, 37, 38, 40, 41, 58, 60, 183, 184, 198), spec/28 (237-240, 251-253,
291-296, 314-317, 330-331, 349-353, 361-365, 1679-1685, 1805-1808), spec/29 (1150-1152, 1186, 1274, 1311,
1322-1326, 1461-1543), spec/04 (200, 711-712) and schemas/lenny-adapter.proto (153-162, 165-179, 1442-1446,
1449-1451, 1455-1462, 1469-1474, 1477-1479). Nothing has drifted; do not re-derive them again.

FACT: `upsertMirror` is handed `row.CoordinationGeneration` straight from the sweep's session-row snapshot,
so after CODE-4 a never-handed-off session's mirror row carries 1 and D7's premise (the ordinary drain
barrier carries a positive value the pod can match) holds on the healthy path. The lag defect the standing
context records is about *when* that snapshot was taken, not about the initial value — EVIDENCE:
pkg/gateway/coordination/coordination/coordination.go:430, :544

FACT: the only two production `.Fence(` call sites are still `pkg/gateway/sessionserver/start.go:4237` and
`cmd/lenny-gateway/coordination_seams.go:233`, with `tests/testinfra/coordfixture/coordfixture.go:231`
calling the adapter RPC directly. D7's "nothing fences a normally-started session" premise is intact.

WATCHOUT: the staged §28.6 second-opener CH-FENCE arm reads "the pod rejects a fence carrying a generation
older than the one it holds for that session and records a higher one", which enumerates older and higher
and says nothing about equal. Equal is reachable: §10.1.2's fence-failure path has the new coordinator retry
"with the same generation value" after a lost acknowledgement, and the adapter refuses that with
`coordinator_handoff_stale` because its guard is `gen <= lastFenced` rather than `gen < lastFenced`. I did
not file it, because §10.1 does not state the stale-fence rule either (only the proto response comment
does), so the incompleteness is pre-existing across the specification rather than introduced by this edit —
EVIDENCE: spec/10_gateway-internals.md:39; pkg/adapter/coordination.go:99;
schemas/lenny-adapter.proto:1455-1458

USEFUL [standing context, "A barrier's generation comes from shared state, never from the sending replica"]:
it is what let me evaluate the §28.6 and §28.8 owner-form rewrites in one read instead of re-deriving the
barrier's provenance from `wiring.go` and `httpsurface.go`.

USEFUL [standing context, "Derived inventories. Do not re-derive any of these."]: the surface enumeration
outside spec/10, spec/28, and spec/29 held on every spot check I made (spec/04:200, :320-323, :461,
:711-712 and spec/18:238, :404 are all unit-neutral or already per-session), which is what let this pass go
wide on feasibility instead of re-sweeping edit sites.


### [spec.1.review-kubernetes.1]

DECISION: returned an empty findings list under the Kubernetes-idiom lens — BECAUSE the staged spec edits touch only the
gateway-to-pod gRPC generation gate, the Postgres `sessions.coordination_generation` counter, and the §28/§29 channel
mirrors; none of them writes a CRD status subresource, adds a finalizer, puts a controller reconcile on a synchronous
path, or changes an admission webhook. ALTERNATIVES: I considered filing the §29.10 "unit of the quiescence a barrier
establishes stays unanswered" retention against the design's own "§10.1.8 step 3 already fixes the gate's unit at the
session", but it is a contradiction-lens question rather than a Kubernetes-idiom one and the two readings (the gate that
holds quiescence vs. the quiescence itself) are not clearly the same predicate; I judged it below the bar rather than
spend two verifiers on it. Left as an OPEN below.
FACT: no CRD, chart, or `pkg/apis` surface carries `coordination_generation` in any spelling — EVIDENCE: `grep -rn
"coordination_generation\|coordinationGeneration" charts/ schemas/*.yaml config/ pkg/apis` returns nothing, so SPEC-3's
counter baseline has no CRD schema, defaulting-webhook, or chart mirror and criterion (d) does not reach those trees.
FACT: `spec/04` §4.2 (heading at `spec/04_system-components.md:192`) states the session record as Postgres-backed
("**Backed by:** Postgres (primary), Redis"), so SPEC-3's baseline edit lands on a database row rather than on
controller-owned desired state, and the §4.6.3 ownership table is not engaged — EVIDENCE:
`spec/04_system-components.md:192`, `:195`, `:200`.
FACT: the only Kubernetes-side statements the staged edits sit next to are §10.1.4's orphan-session reconciler and the
whole-pod replacement trigger, and neither reads the generation the edits move: the reconciler keys on the
`agent_pod_state` mirror and the trigger on slot failure counts — EVIDENCE: `spec/10_gateway-internals.md:59`, `:62`.
The adapter's coordinator-loss path already routes through `AdapterTerminating` on CH-ADAPTEREVENTS precisely because
agent pods have zero RBAC and no apiserver path, and SPEC-1's §10.1.4 edit only changes what the pod-level event and the
per-session post-mortem record, both of which stay pod-local — EVIDENCE: `spec/10_gateway-internals.md:58`, `:60`.
FACT: all §10/§28/§29 anchors I opened resolve verbatim: the generation-counter bullet at `spec/10:30`, step 2 at `:38`,
step 3 at `:41`, §10.1.8 step 1's two anchors on the single physical line `:183`, the §10.1.4 Observability bullet at
`:60`, §29.7's framing rejection sentence at `spec/29:1150-1152`, step 4 at `:1186`, §29.8 step 2 at `:1268-1275`,
step 7 at `:1307-1313`, step 9 at `:1322-1326`, and §29.10's removed "does not state" bullet at `:1523-1529`.
OPEN: does `spec/10` §10.1.8 step 3 ("the gateway's barrier dispatcher opens the `Checkpoint` stream for each quiesced
session ... and then releases quiescence", `spec/10_gateway-internals.md:185`) fix the unit of barrier quiescence at the
session? The proposal's §3 design overview and the summary both rest CODE-1's per-entry `barrierGate` on the claim that
it does, while SPEC-2 stages §29.10 to keep "the unit of the quiescence a barrier establishes" as unanswered. Either the
design's citation overreads step 3 or the retained §29.10 clause is stale. For a later contradiction lens or the human.


### [spec.1.review-mechanism.1]

DECISION: returned an empty findings list for the end-to-end mechanism lens on the staged spec edits — BECAUSE every flow I traced (fence -> per-session lastFenced -> step-3 equality gate; barrier assembly -> mirror/cache -> adapter gate; hold arm -> timeout -> per-session coordinator_lost; row baseline 1 -> CAS mints 2) is internally consistent across SPEC-1, SPEC-2, SPEC-3, and every anchor I re-resolved is verbatim — ALTERNATIVES: I considered filing three things and rejected each, listed below.

FACT: every spec anchor SPEC-1/SPEC-2/SPEC-3 quote re-resolved verbatim in this run, independently of the standing context's derived-inventory bullet: spec/10 :30 (Generation counters), :34, :37 (step 1), :38 (step 2), :40 (gap bullet), :41 (step 3), :58 (hold-timeout post-mortem), :60 (Observability), :183 (§10.1.8 step 1, carrying BOTH the "carries the current coordination_generation" clause and the false-positive rejection sentence on one physical line), :184, :185, :198; spec/28 :237-240, :251-253, :291-296, :330-331, :333-335, :349-353, :361-365, :1679-1681, :1683-1685, :1805-1808; spec/29 :1150-1152, :1186, :1274, :1309-1311, :1322-1326, :1424-1543 (§29.10); spec/04 :175, :188, :200 — EVIDENCE: spec/10_gateway-internals.md:41; spec/28_communication-channels.md:1808; spec/29_communication-scenarios.md:1325

FACT: the four load-bearing adapter code citations are exact as written: checkSessionBound at coordination.go:89 and :216, the non-positive guards at :93-94 and :224-226, the stale/gap predicates at :99 and :108, the barrier gate `!initialized || gen != fenced` at :236-239, the deadlock comment at :126-128; and on the gateway side coordfence.go:147-153 is the zero floor, wiring.go:49 the barrier send and :51-53 the FailedPrecondition -> ErrGenerationStale map — EVIDENCE: pkg/adapter/coordination.go:236-239; pkg/gateway/coordination/barrier/wiring.go:49

FACT: the shipped no-window sentence ("there is no window in which both the old and new coordinator can simultaneously issue accepted RPCs") occurs exactly once in the whole tree outside unrelated prose, at spec/10_gateway-internals.md:41, so SPEC-1's rewrite of it has no docs/ or schemas/ mirror to chase — EVIDENCE: spec/10_gateway-internals.md:41

WATCHOUT: the staged §10.1.2 step-3 acceptance sentence's asserted domain ("the whole set of gateway-to-pod RPCs, including CheckpointBarrier") formally swallows `CoordinatorFence`, which must be accepted carrying a generation ABOVE the value the pod holds — and SPEC-2's own §28.6 second-opener bullet says in so many words that "one predicate cannot span all four channels" and splits CH-FENCE out. I did not file it: the domain claim is proposal rationale, the applied step-3 text is scoped by "Begin coordination" exactly as the shipped sentence is, and the barrier's inclusion is spelled out explicitly in the staged clause rather than resting on the domain claim. A later lens that wants it must argue the applied sentence, not the rationale — EVIDENCE: proposal spec-changes.md, SPEC-1 "the one whose domain is the whole set of gateway-to-pod RPCs"; spec/28_communication-channels.md:1679-1681

WATCHOUT: CODE-1 moves the barrier gate that "holds quiescence open" onto the per-session slot entry, which makes quiescence per session, while SPEC-2's narrowed §29.10 bullet deliberately keeps "the unit of the quiescence a barrier establishes" recorded as unanswered by the specification, and §10.1.8 step 2 stays pod-phrased and unchanged. I judged this a deliberate non-goal rather than a contradiction, because a spec may leave unstated what an implementation chooses; a spec-driven-development lens may disagree — EVIDENCE: spec/10_gateway-internals.md:184; spec/29_communication-scenarios.md:1528-1535

USEFUL [standing context, §28/§29 membership criterion and the refuted-class list]: the closed criterion for what is and is not a §28 edit site let me confirm the CH-FENCE, CH-BARRIER, CH-CHECKPOINT, and CH-ATTACH dispositions in one read of each card instead of re-deriving three ad-hoc lists, and refuted classes (a), (e), (f), and (k) killed four candidates before they cost a verification.

USEFUL [standing context, "spec/10:183 carries both anchors SPEC-1 edits on one physical line"]: confirmed true this run; :183 holds the "carries the current coordination_generation" clause and the false-positive rejection sentence together, so the applier must rewrite one without clobbering the other.


### [spec.1.review-operational.1]

DECISION: Returned an empty findings list for the operational lens over the staged spec edits — BECAUSE every
observability surface the staging touches was re-verified against the tree and agrees: the two `coordinator_connection_lost`
sites are both staged (`spec/10_gateway-internals.md:60`, `spec/29_communication-scenarios.md:1274`, and grep shows no
third in `spec/`, `docs/`, `schemas/`, `charts/`); the `lenny_adapter_coordinator_hold` gauge stays pod-scoped and
label-less in both inventories (`spec/16_observability.md:185`, `docs/reference/metrics.md:309`); the four gap-reset
mirrors the proposal enumerates are exactly the four in the tree (`spec/10:40`, `spec/28_communication-channels.md:333-335`,
`:1807`, `spec/29:1311`); no alert, runbook, or `docs/operator-guide` page keys on a generation, a fence, or the hold
(`grep -rn "fenc\|coordination_generation" docs/runbooks/ docs/operator-guide/` returns one unrelated hit) —
ALTERNATIVES: rejected filing the §16.4 "every log line carries `session_id`" tension against the pod-level arming event
(pre-existing, the event is pod-level before the change), the §29.8 Preconditions "the session's `coordination_generation`
is the generation the pod last fenced" (round 4 already rejected that paragraph, and it is false before the edits too),
and the cache-fallback zero (refuted class (j)).

FACT: `lenny_checkpoint_barrier_ack_total`'s label set already carries an `error` outcome
(`spec/16_observability.md:41`: "`outcome`: `success`, `timeout`, `partial_captured`, `error`"), so D7 turning a refused
barrier into an accepted one moves a count from `error` to `success` and invents no outcome value. The §28.8 `CH-BARRIER`
Observability cell names only `timeout` and `partial_captured` (`spec/28_communication-channels.md:1808`), but as an
open enumeration, so it is not an edit site under D7 — EVIDENCE: `spec/16_observability.md:41`,
`spec/28_communication-channels.md:1808`.

FACT: the adapter's gap log already carries `session_id` (`pkg/adapter/coordination.go:114-117`), so re-scoping
`coordinator_generation_gap` per session leaves no operator-attribution gap on a co-tenant pod, and no staged sentence
owes a session-id clause — EVIDENCE: `pkg/adapter/coordination.go:114-117`.

FACT: `enterHoldState` already logs `started_sessions` beside `last_generation`
(`pkg/adapter/holdstate.go:130-132`), so SPEC-1's staged §10.1.4 Observability sentence ("names the number of started
sessions the pod holds and carries no generation") describes a field the code emits today rather than inventing one —
EVIDENCE: `pkg/adapter/holdstate.go:130-132`.

USEFUL [standing context, "Derived inventories"]: the bullet's claim that the `docs/` surface states no unit, baseline,
or gate and that no alert, runbook, or tier-11 test is reached held on an independent re-derivation from the metrics,
alert, runbook, and operator-guide side. It saved a full sweep; a future operational lens can spot-check the two
`coordinator_connection_lost` sites and the gauge row and stop.


### [spec.1.review-performance.1]

DECISION: returned an empty findings list on the staged spec edits — BECAUSE the staging adds no
per-task, per-request, or per-session write to any store, creates no new watch or informer cache, and
introduces no hot key or single-leader serialization; every failure-mode candidate resolved into
pre-existing behaviour, a recorded non-goal, or a refuted class — ALTERNATIVES: the four candidates
below, each killed on stated evidence.

FACT: the staging is write-neutral by construction. The counter baseline (SPEC-3, SPEC-1's §10.1
sentence) changes a value written on an INSERT that already names `coordination_generation` in its
column list, and §10.1.2 step 1's compare-and-swap and the other two counter writers are explicitly
untouched, so the `sessions` write rate at any tier is identical before and after. The per-session
fenced generation is adapter process memory bounded by `maxConcurrentSessions`, so no etcd status
write, no CRD subresource write, and no net-new watch is created.
— EVIDENCE: spec/10_gateway-internals.md:37 (step 1 CAS, unchanged);
  proposals/0076_.../0076_....spec-changes.md:530-537 (SPEC-3, "existing statements are unchanged")

FACT: the fence RPC count does not change. `coordfence` is already per session and each session's
takeover drives its own fence, so a mass pod takeover after a Redis reset issues the same number of
`CoordinatorFence` RPCs under the per-session gate as under the shipped pod-wide one. The only thing
that moves is which value the pod compares against.
— EVIDENCE: spec/10_gateway-internals.md:38 (step 2 is per `CoordinatorFence(session_id, ...)`)

FACT [candidate killed]: the per-session gap reset is strictly less collateral than the shipped one.
Shipped clause (a) cancels "all in-flight RPCs received after `last_fenced_generation`" pod-wide; the
staged clause scopes it to the fenced session. A gap in session A's lineage carries no information
about session B, so narrowing it removes work rather than adding a stall.
— EVIDENCE: spec/10_gateway-internals.md:40 (shipped clauses a-d)

FACT [candidate killed]: `lenny_adapter_coordinator_hold` and `lenny_coordinator_handoff_stale_total`
are declared unit-neutral in both the spec inventory and the rendered docs table, so D5 keeping the
hold pod-scoped and the per-session generation both leave them true. No alert or runbook is reached.
— EVIDENCE: spec/16_observability.md:183, :185; docs/reference/metrics.md:307, :309

USEFUL [Standing context, "A refused barrier costs a duplicate capture rather than a lost checkpoint"]
and USEFUL [non-spec.5.review-performance.1]: between them these gave me the whole prestop/`ErrGenerationStale`
cost model, the pod-level op-lock serialization of co-tenant checkpoint uploads, and the finding that
`dispatchOne` starts the `Checkpoint` stream before `dispatch.Send`, so D7 changes no drain load. Without
them I would have spent the pass re-deriving the drain-concurrency question and would probably have filed
"D7 makes the drain hold N barriers open", which that shard already refutes.

WATCHOUT: a performance lens has now run and returned empty on both lanes (`spec.2.review-performance.1`
and `non-spec.5.review-performance.1`), and the staged SPEC-1/2/3 text has not moved since round 4 — passes
20 and 21 in the spec-changes file's `## Resolved` section are non-spec-lane (§8, §9, CODE-1/CODE-2)
records appended to that file because the section lives only there. Do not read a late pass number in the
spec-changes file as evidence that the staged spec text changed.
— EVIDENCE: proposals/0076_.../0076_....spec-changes.md:1698-1791 (passes 20, 21, both CODE-lane)


### [spec.1.review-reliability.1]

Round 1, run 4, reliability lens over the staged spec edits (SPEC-1/2/3, spec-changes.md lines 1-563). Returned zero findings.

USEFUL [standing-context/refuted classes (e), (f), (g), (j), (k)]: four of the five recovery-path leads I derived independently
land inside an already-refuted class. (e) killed the "step 3's unset arm reopens the split-brain window during a crash
takeover" line; (f) and its MISTAKE rider killed the never-fenced-session barrier hole; (g) killed the pod-wide hold-exit
consequence; (j) killed the cache-fallback zero seed against the staged "every generation a pod validates is positive"
sentence. Reading the refuted list first saved roughly the whole pass.

USEFUL [standing-context / "A refused barrier costs a duplicate capture rather than a lost checkpoint"]: it settles the
reliability cost of both arms of staged §10.1.8 step 1 (quiescence and the acked-barrier record are what is lost, the
checkpoint still runs, quiescence cannot wedge because the clear is deferred and bounded by the 90s ack deadline). That is
the single fact that keeps "Either outcome is safe and requires no special handling" out of finding range for this lens.

FACT: the resume path always claims a *replacement* pod, never the same one — `spec/07_session-lifecycle.md:196`
("`resuming → running` (re-attach succeeds on replacement pod)") and `:197`. That closes the standing OPEN [mechanism]
"can the gateway re-bind the same session id onto the same pod after `releaseSessionSlot`" in the *specification's* terms:
in spec the answer is no, so D6's per-entry `last_fenced_generation` is not lost across a resume retry and staged step 3's
permissive unset arm is not reopened by a rebind. The code-side question (whether `pkg/gateway/sessionserver` placement can
ever pick the same pod) is still open and still belongs to a gateway-side reviewer; I did not chase it, because the spec-side
answer is what the staged text has to be consistent with.

FACT: `CoordinatorFence` is idempotent under retry at the same generation, so §10.1.2 step 2's "retry the fence RPC with the
same generation value (up to 3 attempts with 1-second backoff)" (`spec/10_gateway-internals.md:39`) stays correct under D6.
Equal is neither "older" (no stale rejection) nor `> last + 1` (no gap), on both the unset arm and the recorded arm. Nobody
had written this down and it is the first thing a reliability lens checks on the fence path.

FACT: the partial-manifest machinery in `spec/10_gateway-internals.md:153`, `:157`, and `:171` reads
`coordination_generation` only comparatively — supersede on `coordination_generation <= $incoming`, resume-select on
`MAX(coordination_generation)` — so the SPEC-3 baseline shift from 0 to 1 is uniform across every writer and reader and
changes no outcome there. `:157`'s phrase "the coordinator's fenced generation at intent-row INSERT time" is already
imprecise in the shipped tree (an ordinary never-taken-over session's coordinator has never fenced today either), so it is
pre-existing drift rather than a site SPEC-1 makes wrong. Do not re-derive this; it is not an edit site.

FACT: `coordination_lease` is described in `spec/` only as carrying `session_id`, `coordinator_replica`, and `released_at`
(`spec/10_gateway-internals.md:183`; `spec/28_communication-channels.md:138` registers it as `REG-COORDMIRROR`, "a
projection rather than an exclusion primitive"). Staged §10.1.8 step 1's new provenance sentence ("read from the session's
coordination state when the barrier-target set is assembled") therefore names no column the spec contradicts, and it agrees
with the shipped "carries the current `coordination_generation`" clause on the same line. I checked it as a possible
unsupported-claim finding and it is not one.

DECISION: did not file the D7 acceptance arm (a superseded draining replica quiesces a session it no longer coordinates and
stalls the live coordinator's dispatches for up to the 90s ack deadline) — BECAUSE the standing context records round 6
considering and declining it with reasons that hold under this lens too, and the quiescence is bounded and self-clearing
rather than a wedge — ALTERNATIVES: filing it as a drain path that stalls running work, rejected because it is already
routed to the human-review pass and re-filing costs two verifiers to reach the same verdict.

DECISION: did not file the §10.1.3 dual-store paragraph (`spec/10_gateway-internals.md:47`, "the pod validates the
generation stamp, which remains valid") as a missed edit site — BECAUSE under the unset arm the pod validates nothing but
the sentence's own assertion, that gateway-to-pod RPCs proceed normally, stays true, and the sentence fixes no compared
value so it fails SPEC-2's own membership criterion.


### [spec.1.review-security.1]

DECISION: Returned an empty findings list for the security lens on the run-4 spec staging — BECAUSE both halves of the lens
resolve to ground the standing context already closes, and every candidate I derived independently landed inside a named
refuted class — ALTERNATIVES: I considered and rejected filing four things. (1) D7's acceptance arm as a relaxed fail-closed
gate: refuted class (f), and round 6's declination is recorded in standing context. (2) Staged §10.1.2 step 3's unset clause
plus staged §28.6's second-opener "the pod rejects none of that session's RPCs on generation grounds" as widening the
accepting window across CH-ATTACH/CH-CHECKPOINT/CH-BARRIER for a never-fenced session: this is the same relaxation refuted
class (f) covers, and REG-COORDLEASE remains the excluding guard (`spec/28` §28.6 names it alongside the stamp). (3) Removal
of the generation from the pod-level `coordinator_connection_lost` event (staged §10.1.4 Observability, mirrored in §29.8
step 2) as an audit-surface regression: the value is not lost, it moves onto each terminated session's `coordinator_lost`
record with the correct per-session unit, which is strictly more audit information, and `spec/10_gateway-internals.md:60`
confirms the shipped bullet carries only "the last known generation" pod-wide. (4) The staged §29.10 "Shared by the whole
pod" bullet restating the hold's allowlist as "every inbound RPC other than `CoordinatorFence`": that matches
`spec/10_gateway-internals.md:58` verbatim, and the code's wider allowlist is pre-existing drift the standing context
already classifies as out of scope.

FACT: The gap reset — the split-brain remediation control — survives the re-scoping intact. Shipped clauses (a) and (b) at
`spec/10_gateway-internals.md:41` ("immediately cancel and discard all in-flight RPCs received after
`last_fenced_generation`", "reset any transient tool-call or lifecycle state accumulated since the last fenced coordinator")
are carried by SPEC-1 with only a session qualifier added, and the four mirrors (§28.5.1 `CH-FENCE` Degradation, §28.8
`CH-FENCE` cell, §29.8 step 7) take the same wording. No arm of the control is deleted or feature-gated.
EVIDENCE: spec/10_gateway-internals.md:41; spec-changes.md:145-149, :311-316, :370-377, :463-468

FACT: D6's rationale sentence "a session that has never been fenced on this pod has accumulated no state for the gap path's
reset to act on" is loosely worded but not falsifiable into a skipped reset. I chased the interleaving: for a session S on
pod P whose first coordinator R1 never fenced, R2's takeover mints exactly `expected+1`, so the gap predicate
`new > last + 1` is false on the value even if the exemption were absent; a genuine multi-step jump requires prior fenced
generations, which by construction means the session's value on that pod is recorded and the exemption does not apply.
There is no case where the pre-change code performed a reset the post-change code skips.
EVIDENCE: spec/10_gateway-internals.md:37 (step 1's `$expected_generation + 1`), :41 (gap predicate); spec-changes.md:33-38

USEFUL [standing context, `CoordinatorFenceResponse.last_fenced_generation` bullet]: the round-5 security lens's finding that
the pod's self-reported `last_fenced_generation` reaches no gateway decision (`adapterclient/coordinatorfence.go:29`, `:60`;
`coordfence.go:159-179` branches on `res.Accepted` alone and re-reads Postgres on rejection) saved me the entire
trust-boundary re-derivation for lens check (2). It is still true against the staged text: nothing SPEC-1 through SPEC-3
stages moves a security bound onto a pod self-report. The authoritative counter stays the Postgres session row, whose three
writers the standing context enumerates.

USEFUL [standing context, refuted-classes bullet]: reading each refutation's body rather than its title is what stopped me
filing candidate (2) above. Class (f)'s title reads as barrier-only; its body is the lease-excludes-the-second-replica
argument, which reaches the CH-ATTACH and CH-CHECKPOINT arms of the staged §28.6 second-opener sentence too.


### [spec.2.review-citations.1]

DECISION: Returned an empty findings list — BECAUSE I re-opened every concrete citation in the live sections of the spec-changes file (lines 1-586: §2 decisions D1-D7, §3, §4, SPEC-1, SPEC-2, SPEC-3, §6, §7, §10) against the tree and every one resolves, with the quoted text verbatim where the proposal quotes — ALTERNATIVES: filing the "§10.1's Generation counters bullet" section attribution (the bullet is in §10.1.1, spec/10_gateway-internals.md:5, :30) was considered and rejected: §10.1.1 is inside §10.1, the bullet is named by its own label, and the standing context already carries it as an UNVERIFIED.

FACT: The complete citation inventory verified this run, all resolving. spec/10: :30 (Generation counters bullet, "When a replica takes over coordination (via either mechanism), it increments the generation"), :37 (step 1 CAS), :38 (step 2, both "The pod records the new generation and from this point rejects any RPC carrying an older generation" and "the pod still accepts RPCs carrying the previous generation"), :40 (gap bullet, parenthetical "(the generation from the last successfully acknowledged fence)", clauses (a)-(d)), :41 (step 3, three sentences), :58 (hold timeout + local-disk post-mortem), :60 (Observability, coordinator_connection_lost "with the last known generation"), :62 (Whole-pod connection loss paragraph), :183 (§10.1.8 step 1, both the "carries the current coordination_generation" clause and the quoted closing sentence), :184, :185, :190, :198. spec/28: :237-240, :251-253, :291-296, :314-317 (CH-FENCE Messages), :330-331, :333-340 (Degradation), :349-353, :361-365, :1669-1677 (One holder per session), :1679-1681, :1683-1685, :1805, :1806, :1807, :1808, :1810. spec/29: :1150-1152, :1186, :1193-1196 (step 5 quiescence), :1274, :1307-1313 (step 7), :1322-1326 (step 9), :1424-1543 (§29.10 lists). spec/04: :200 (§4.2 session-record bullet). schemas/lenny-adapter.proto: :153-162, :161-162, :165-179, :1442-1446, :1449-1451, :1455-1462, :1469-1474, :1475-1483, :1477-1479, and all twelve operational field comments (:969-973, :995-1001, :1046-1050, :1070-1074, :1091-1095, :1114-1118, :1172-1178, :1305-1309, :1393-1397, :1531-1535, :1576-1580, :1618-1622, the last closing "cannot tear the session down"). Code: pkg/adapter/coordination.go :29-32 :89 :92 :93-94 :99 :108-121 :112-113 :216 :223 :224-226 :228-231 :236-239; pkg/adapter/holdstate.go :39-44 :90-100 :107-112 :119 :128-132 :172-176 :187 :192; pkg/adapter/adapterevents.go :80-96; pkg/adapter/slotsession.go :267; coordfence.go :147-153; barrier/wiring.go :49 :51-53 :104-114; barrier/barrier.go :207-246; coordination/coordination.go :399 :430; cmd/lenny-gateway/httpsurface.go :592-599; cmd/lenny-gateway/coordination_seams.go :155-160 :233; sessionserver/start.go :3975 :4067 :4233 :4237; prestop.go :390-397 :505-513; migrations/0050_session_record_fields.up.sql :38-39.

CORRECTS [standing context, `### Settled`, "Derived inventories"]: the bullet says the §28.8 one-row-per-§28.3-identifier bijection gate "lives in `tests/tier11_docs/`, so a tier-0-only run does not catch a §28 row defect". It does not. The bijection gate is `tests/tier0_static/matrix_completeness_test.go`, untagged `package tier0_static`, whose header states "asserts a bijection between the channel identifiers in the §28.3 channel register and the rows of the §28.8 failure and degradation matrix" and "The gate reads the bijection in both directions" — EVIDENCE: tests/tier0_static/matrix_completeness_test.go:16-33. `tests/tier11_docs/spec_28_index_rows_test.go` is a different gate (index rows). The proposal's own staged sentence in SPEC-2's §28.8 `CH-FENCE` bullet, "a tier-0 gate reads that correspondence in both directions", is therefore correct as written and must not be "corrected" toward the standing-context bullet. The standing context's derived reason for checklist S1's tier 11 is wrong even though the tier-11 declaration may still be right on other grounds (`tests/tier11_docs/spec_28_ownership_test.go` exists).

USEFUL [standing context, `### Traps`, "Editing hazards in this proposal's own files"]: naming the spec-changes file in full rather than globbing `*spec-changes.md` saved a whole re-derivation of every line number in this pass.

FACT: run 4 round 1 changed nothing in the proposal. `diff -ru scratchpad/cp-snap/0076-run4/spec-r2 proposals/0076_.../` returns a hunk only in the review log (compaction pass 16). Every sentence in the staged spec edits is therefore text that has survived at least one full round, and a "read the newest text hardest" reading order has no newest text to point at this round.


### [spec.2.review-client-surface.2]

DECISION: returned an empty findings list for the client-facing-surface lens — BECAUSE the only externally-consumed
representation this proposal reaches is `schemas/lenny-adapter.proto`'s doc comments (SCHEMA-1), and every carrier the
staged text names resolves verbatim and the enumeration is complete — ALTERNATIVES: I considered filing SPEC-2's
closing paragraph (spec-changes.md:500-505) for describing the barrier carriers' replacement without the D7 unset
arm, and rejected it: spec-changes.md:269-271 already directs "the acceptance sentence ... together with the
unset-value clause" onto exactly those carriers, so the document as a whole is complete and the abbreviated
restatement is not a contradiction. I also considered the `CoordinatorFenceResponse` comment's `accepted`-false
sentence (proto:1455-1458), which states the stale predicate with no unset arm; SPEC-2 stages that comment
(spec-changes.md:491-494) and the wording it prescribes is a re-scoping instruction rather than a replacement, so it
does not meet the bar.

FACT: the proto's `coordination_generation` carrier set is exactly 14 fields — `grep -n "coordination_generation = "
schemas/lenny-adapter.proto` returns :974, :1002, :1051, :1075, :1096, :1119, :1179, :1310, :1398, :1452, :1480,
:1536, :1581, :1623 — i.e. the twelve operational-RPC comments SPEC-2 enumerates at spec-changes.md:526-531 plus
`CoordinatorFenceRequest` and `CheckpointBarrierRequest`. There is no fifteenth. All twelve line ranges in that
enumeration resolve exactly. — EVIDENCE: schemas/lenny-adapter.proto:969-973, :1618-1622

FACT: the client-facing surfaces this lens owns are untouched by the change and need no edit. `coordination_generation`
and `coordinationGeneration` appear nowhere in `sdks/`, `charts/`, `schemas/*.json`, `pkg/gateway/openapi/`,
`docs/api/`, `docs/client-guide/`, or `docs/runtime-author-guide/`; `docs/api/` contains no case-insensitive match for
`coordinat`, `fenc`, or `barrier` at all. The whole `docs/` mention set for the fence and barrier is five files
(`operator-guide/upgrades.md`, `getting-started/architecture.md`, `reference/glossary.md`, `reference/adapter-contract.md`,
`reference/metrics.md`), and every hit in them is unit-neutral and baseline-neutral. This independently re-derives the
standing-context "docs surface is eight sites and states no unit, baseline, or gate" bullet from the client side.
— EVIDENCE: docs/reference/adapter-contract.md:69; docs/getting-started/architecture.md:173; docs/reference/metrics.md:307,:309

FACT: the three §16 metric inventory rows for this mechanism are unit-neutral, so D5's pod-scoped hold and the
per-session generation reach no metric, alert, or runbook edit site. — EVIDENCE: spec/16_observability.md:183, :185, :192

USEFUL [Settled: "sdks/, schemas/README.md, and schemas/examples/ mention coordination_generation nowhere at all"]:
saved a full SDK-parallel sweep; re-confirmed by `grep -rln "CoordinatorFence|coordination_generation|coordinationGeneration|CheckpointBarrier" sdks/`
returning nothing.

FACT: only the review log changed since the r2 snapshot — `diff -q` over the other six proposal files is clean, so the
spec-changes text this round reviewed is byte-identical to the text round 1 reviewed. A lens that ran on the previous
round's spec-changes gains nothing by re-reading it. — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r2/


### [spec.2.review-fresh.1]

DECISION: filed one finding only — the barrier-quiescence unit contradiction between §3's "§10.1.8 step 3 already fixes the gate's unit at the session" (spec-changes.md:94-96) and SPEC-2's staged §29.10 narrowed bullet keeping "the unit of the quiescence a barrier establishes" unanswered (spec-changes.md:445-449) — BECAUSE it is the one place where a live staged spec sentence contradicts a live design claim the code lane rests on, and SPEC-2's own removal ground for the neighbouring bullet (:428-431, "the list's contract is that it holds questions the specification does not answer") condemns it by the same test — ALTERNATIVES: rejected filing §29.2 step 11 (the pre-message-announcement bullet) as a parallel stale "does not state" site, because the staged text states what happens when no fence was announced and never states whether the creating replica announces one, so the question survives literally; rejected filing the §28.5.1/§28.8/§29.8 gap mirrors omitting D6's first-fence exemption, because a mirror carrying less detail than its owning section is not made wrong by the omission; rejected filing the new §28.6 clause "the pod rejects a fence carrying a generation older than the one it holds ... and records a higher one" over the uncovered equal case, because every shipped register sentence says "older" and §10.1.2's own retry path is the pre-existing defect.

FACT: every spec, proto, and code line citation in the live SPEC-1/SPEC-2/SPEC-3 text resolves verbatim, re-checked this run against the tree at HEAD — EVIDENCE: spec/10:30,:37,:38,:41,:58,:60,:183,:184,:185,:190,:198; spec/28:237-240,:251-253,:291-296,:314-317,:329-340,:349-353,:361-365,:1669-1690,:1805-1808 (col 5 of the §28.8 matrix is the "Holder of the exclusivity constraint changes" cell; use `awk -F'|' '{print $5}'`); spec/29:1150-1152,:1186,:1259-1264,:1274,:1307-1313,:1322-1326,:1461-1470,:1523-1535; spec/04:200; pkg/adapter/coordination.go:89,:92,:93-95,:99,:108,:110-113,:119-121,:216,:223,:224-226,:228-231,:236-239; coordfence.go:147-153.

FACT: SPEC-2's twelve non-fence/non-barrier proto field-comment citations are all exact — each range runs from the comment's first line to the line before its `int64 coordination_generation` declaration, and the twelve messages named match — EVIDENCE: schemas/lenny-adapter.proto:969-973/974, 995-1001/1002, 1046-1050/1051, 1070-1074/1075, 1091-1095/1096, 1114-1118/1119, 1172-1178/1179, 1305-1309/1310, 1393-1397/1398, 1531-1535/1536, 1576-1580/1581, 1618-1622/1623.

FACT: the spec-changes file was byte-identical to the run-3 snapshot; only the review log changed (compaction pass 16). A `diff -rq` against scratchpad/cp-snap/0076-run4/spec-r2 is the cheapest way to learn that, and it means "read the changed sections hardest" gave no reading order this round.

USEFUL [Standing context / "The `spec/28` and `spec/29` edit sites are settled by one membership criterion"]: saved re-deriving the site/non-site split for all of §28; every sentence I independently tested against the criterion landed where the entry says.

USEFUL [Standing context / "Derived inventories. Do not re-derive any of these."]: the docs, charts, sdks, and §16 sweeps held on spot-check (docs/reference/metrics.md:307,:309,:312 and spec/16:183,:185,:192 are unit-neutral; nothing in schemas/ or charts/ names the column outside lenny-adapter.proto).

OPEN: the finding admits two remedies with different consequences and I did not pick one. Either §29.10's narrowed bullet drops the quiescence-unit clause (which concedes §10.1.8 step 3 fixes it, and then someone should check that "for each quiesced session" at spec/10:185 really is a unit statement rather than an enumeration of targets), or the clause stands and CODE-1's per-entry `barrierGate` loses its stated spec ground, in which case SPEC-1 owes §10.1.8 step 2 or 3 a sentence fixing the quiescence at the session. A fixer should route the choice through §7 rather than pick silently.


### [spec.3.review-applicability.1]

DECISION: returned an empty findings list for the applicability/sequencing lens over the staged spec edits — BECAUSE every anchor SPEC-1, SPEC-2, and SPEC-3 quote resolves verbatim and uniquely in the current tree; every created/rewritten sentence has either verbatim replacement text or a deterministic prose instruction with a named insertion point; the one relocation (§29.10's first "does not state" bullet) has both legs staged and every element of the source lands in a named destination; and no tier-0 or tier-11 gate string-matches a rewritten sentence — ALTERNATIVES: I weighed and rejected four candidates, listed below, each as below the bar.

FACT: `spec-changes.md` is byte-identical to the `spec-r2` and `spec-r3-start` snapshots — `diff -rq scratchpad/cp-snap/0076-run4/spec-r2 .../spec-r3` reports only the review log differing. Round 2 of this run produced no staged-text edits, so the "read the newest text hardest" instruction has no new text to point at this round; the whole staging is round-1-or-older. EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r2/, spec-r3/, spec-r3-start/

FACT: anchor re-verification, done fresh this round (do not re-derive). spec/10:30 (Generation counters bullet, in §10.1.1 under §10.1), :37, :38, :40 (gap bullet), :41 (step 3, three sentences), :58, :60, :62, :183 (both the "carries the current coordination_generation" clause and the closing false-positive sentence on one physical line), :184, :198. spec/28:315-317 (CH-FENCE Messages), :330-331 (Exclusivity window clause), :333-340 (Degradation), :237-240, :251-253, :291-296, :349-353, :361-365, :1669-1677 (One holder per session), :1679-1690 (second opener), :1806 (CH-CHECKPOINT cell), :1807 (CH-FENCE cell), :1808 (CH-BARRIER cell). spec/29:1150-1152, :1186, :1269-1276 (§29.8 step 2), :1304-1310 (step 7), :1320-1326 (step 9), :1461-1470 (Partitioned per slot), :1472-1518 (Shared by the whole pod), :1519-1543 (does not state, four bullets). spec/04:200. All verbatim.

CORRECTS [standing context, "Derived inventories" bullet]: it says the §28.8 one-row-per-identifier bijection gate "lives in `tests/tier11_docs/`, so a tier-0-only run does not catch a §28 row defect and checklist S1's tier 11 covers it". The gate is `tests/tier0_static/matrix_completeness_test.go:16-29`, a tier-0 test; `tests/tier11_docs/` has no §28.8 row gate. The proposal's own text is right where the log is wrong: spec-changes.md's §28.8 `CH-FENCE` bullet says "a tier-0 gate reads that correspondence in both directions". Nothing is at risk either way, because SPEC-2 edits cells and adds or removes no row, but do not repeat the log's tier attribution.

FACT: no tier-0 or tier-11 gate reads any sentence the staged edits rewrite. Re-derived this round by grepping the two test trees for the rewritten phrases ("prior coordinator", "superseded replica", "older generation", "current generation") — zero hits — and by reading every tier-11 file whose name touches coordination, hold, generation, fence, barrier, or co-tenancy: `rotation_ceiling_cotenant_reconciliation_test.go`, `eviction_coordinator_route_consistency_test.go`, and `slot_placeholder_literal_sweep_test.go` cite none of spec/10, spec/28, or spec/29. `tests/tier0_static/spec_map_slot_address_registration_test.go:1486` asserts only that the heading `29.10` exists, which the §29.10 bullet removal does not touch.

FACT: the §10.1 no-window claim ("there is no window in which both the old and new coordinator can simultaneously issue accepted RPCs to the pod") that SPEC-1's step-3 replacement retires occurs exactly once across `spec/`, `docs/`, and `schemas/` — spec/10_gateway-internals.md:41 — so replacing it strands no mirror. Similarly `coordinator_connection_lost` occurs in spec/ only at spec/10:60 and spec/29:1274, and SPEC-1 and SPEC-2 stage both.

FACT: SPEC-1's staged §10.1.4 claim that the pod-level `coordinator_connection_lost` event "names the number of started sessions the pod holds" is already true of the shipped emit — `pkg/adapter/holdstate.go:130-132` logs `started_sessions` and `last_generation` — so the spec edit adds no code obligation beyond CODE-3's removal of the generation key.

FACT: four candidates weighed and NOT filed this round, with the reason each fails the bar. Do not spend a verification on any of them without new evidence.
  (1) SPEC-1's sentence "SPEC-2 stages it into `spec/29` §29.10 twice ... and each takes the acceptance sentence above", against SPEC-2's actual §29.10 bullets, which carry classification sentences ("the generation the pod records on a fence ... is the fenced session's") rather than step 3's acceptance sentence, and which SPEC-1 elsewhere says deliberately exclude the unset clause. Reads as a loose gloss of "the acceptance rule" rather than a contradictory instruction; SPEC-2's own bullets are the operative text and are deterministic.
  (2) spec/10:30's unqualified consequence "This prevents split-brain even under lease/lock race conditions", left standing, while SPEC-2 rewrites the twelve proto field comments' structurally identical consequence clause on the ground that it is false for the unset session class. Line 30's rejection is stated conditionally ("if the generation is stale"), which stays true when nothing is stale, whereas the proto clause asserts a replica "cannot drive the pod" outright. Different strength, so the parallel does not force the edit.
  (3) The staged §10.1.8 step-1 provenance sentence "read from the session's coordination state when the barrier-target set is assembled" against step 1's own barrier-target query, which selects `session_id` from `coordination_lease` and no generation. The two coexist: step 1 never states where the carried value is read from.
  (4) "The per-session `coordinator_lost` log line" introduced by SPEC-1's §10.1.4 text where §10.1.4 today names `coordinator_lost` only as a `session.terminated` reason. The edit states the artifact's name, its content, and its location in the Observability bullet, so nothing is left to invent; introducing an observability artifact is what that bullet is for. This is the standing `### Open` item of the same name and it stays an OPEN.

WATCHOUT: the checklist's six steps are all unchecked and each carries exactly one lane, and every code step depends on S1, so no code step consumes unlanded spec text. Checklist drift is out of scope for the spec loop, but if a later loop touches it, that is the state it starts from. EVIDENCE: proposals/0076_.../0076_....implementation-checklist.md:3-21


### [spec.3.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in the live sections of the
spec-changes file (§2 D1-D7, §3, §4, SPEC-1, SPEC-2, SPEC-3, §6, §7, §10) resolves verbatim at the cited
location, in `spec/04`, `spec/10`, `spec/28`, `spec/29`, `schemas/lenny-adapter.proto`, `migrations/0050`,
and the eight `pkg/`+`cmd/` files — ALTERNATIVES: filing the three near-miss imprecisions listed below,
each of which is already indexed as an OPEN or sits inside a refuted class.

FACT: the proposal and the working tree are byte-identical to the `spec-r3` and `spec-r3-start` snapshots
(`diff -rq` returns 0 against both), so round 3 landed no edit to any proposal file. A citation lens in a
later round gains nothing from re-reading "what changed"; there was nothing.
EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0076-run4/spec-r3

FACT: the proto carrier enumeration in SPEC-2's closing paragraph is exact and now verified end to end.
`grep -n "int64 coordination_generation" schemas/lenny-adapter.proto` returns fourteen declarations: the
twelve operational-RPC comments SPEC-2 names by message (`:969-973`, `:995-1001`, `:1046-1050`,
`:1070-1074`, `:1091-1095`, `:1114-1118`, `:1172-1178`, `:1305-1309`, `:1393-1397`, `:1531-1535`,
`:1576-1580`, `:1618-1622`) plus `CoordinatorFenceRequest` (`:1452`) and `CheckpointBarrierRequest`
(`:1480`). Eleven close "cannot drive the pod (§10.1)" and `ShutdownRequest` closes "cannot tear the
session down (§10.1)". There is no thirteenth operational carrier and no missed one.
EVIDENCE: schemas/lenny-adapter.proto:973, :999, :1050, :1074, :1095, :1118, :1176, :1309, :1397, :1535, :1580, :1622

FACT: `GetCoordinationGeneration()` has exactly two non-test call sites in `pkg/adapter`, both in
`coordination.go` (`:92` fence, `:223` barrier), which is what D7's "the barrier is the only gateway-to-pod
RPC the adapter validates on the generation gate" rests on. Verified by grep over the whole package.
EVIDENCE: pkg/adapter/coordination.go:92, :223

FACT: `upsertMirror` runs once per held lease per sweep with `row.CoordinationGeneration` taken from the
List snapshot, for every session the replica coordinates rather than only on a takeover edge, so a
never-handed-off session does have a `coordination_lease` mirror row and its barrier does carry the row's
baseline. This is what makes D7's "the ordinary never-handed-off session's barrier then carries the 1 its
own row holds" reachable, and it is worth not re-deriving.
EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:370, :430

WATCHOUT: three sentences read as citation defects and are not. (1) SPEC-1's "Each names the row value the
dispatcher copies onto the wire (`wiring.go:49`)" — on the healthy path the dispatcher copies the
`coordination_lease` mirror value (`wiring.go:104-114`), not the session row's; the mirror is seeded from
the row so the sentence survives, and the currency question it raises is already the standing OPEN
'"Current" generation on the barrier'. (2) SPEC-1 calls the "Generation counters" bullet §10.1's while the
bullet lives in §10.1.1, which is a subsection of §10.1, so the attribution is loose rather than false;
already indexed as an OPEN. (3) SPEC-1's "the sentence the adapter's `CheckpointBarrier` gate cites
(`pkg/adapter/coordination.go:228-231`)" — the comment there cites §10.1.2 as a section rather than step 3
as a sentence. None meets the bar.
EVIDENCE: pkg/gateway/coordination/barrier/wiring.go:104-114; spec/10_gateway-internals.md:30; pkg/adapter/coordination.go:228-231

USEFUL [standing context, "Derived inventories. Do not re-derive any of these."]: its claim that every
SPEC-1/SPEC-2 anchor resolves verbatim and uniquely held on a fourth independent re-check. It did not save
the work this round, because the lens is the citation audit and had to re-open each anchor anyway, but it
predicted the outcome exactly. A future citation lens should read it as evidence that the anchor set is
stable and spend its budget on the code-side attributions (which side of the mirror is authoritative, how
many call sites a helper has) instead, because that is where the two loose statements above sit.


### [spec.3.review-client-surface.1]

FACT: the spec-changes file is byte-identical to run 4's r2 snapshot (`diff -ru scratchpad/cp-snap/0076-run4/spec-r2/*.spec-changes.md proposals/.../*.spec-changes.md` is empty); the 979 diff lines between r2 and now are all in the review log and the non-spec file. A client-surface lens in this round therefore re-reads text that has already survived one pass — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r2.

FACT: the client-facing surface this proposal touches is exactly one, the gateway-to-adapter proto. Re-derived independently this round and it matches the standing context: `coordination_generation` appears in no `sdks/` file, no `charts/` file, no `schemas/*.json`, and not in the served OpenAPI document, which lives at `pkg/gateway/externalapi/openapi/openapi.json` (its only "generation" hit is a pool CRD-versus-Postgres summary at :3318) rather than at the `pkg/gateway/openapi/openapi.json` path the lens brief names. `docs/reference/adapter-contract.md:68`, `:69`, `:96` are unit-neutral one-line table cells. `docs/getting-started/concepts.md:101` states only counter independence and stays true under the baseline — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:3318; docs/reference/adapter-contract.md:68-69.

FACT: the proto carries exactly 14 `int64 coordination_generation` fields (12 operational-RPC requests plus `CoordinatorFenceRequest` and `CheckpointBarrierRequest`), and all 12 field-comment ranges SPEC-2 cites at spec-changes.md:526-531 resolve verbatim, `ShutdownRequest`'s "cannot tear the session down" variant included. The carrier enumeration is closed; do not re-derive it — EVIDENCE: schemas/lenny-adapter.proto:974,1002,1051,1075,1096,1119,1179,1310,1398,1452,1480,1536,1581,1623.

MISTAKE: SPEC-2's proto paragraph treats `CoordinatorFenceResponse` as a repeat of the request comment's record-and-reject rule. It is not: its two sentences define the `accepted` and `gap_detected` response fields, and `accepted`'s false-condition ("not greater than the last fenced generation") is the sentence the per-session move falsifies. Filed this round as the one finding — EVIDENCE: spec-changes.md:491-494 against schemas/lenny-adapter.proto:1455-1462 and pkg/adapter/coordination.go:97-106.

WATCHOUT: the same misdescription is frozen in a `## Resolved in adversarial review` pass record at spec-changes.md:664-665 ("the `CoordinatorFenceResponse` comment repeats both"). That record keeps the words it was written with; only the live paragraph at :487-494 is the edit site. A grep for `CoordinatorFenceResponse` returns both — EVIDENCE: spec-changes.md:664-665.

DEFERRED [non-spec-changes.md]: SCHEMA-1 (non-spec-changes.md:11-20) lists `CoordinatorFenceRequest.coordination_generation` among the comments that "take the wording SPEC-2 states for it", while SPEC-2 states that comment "keeps its wording" (spec-changes.md:497-498). Not filed: it is a no-op either way, and its remedy is in a file this loop may not edit.


### [spec.3.review-docs-alignment.1]

DECISION: Returned an empty findings list for the docs-alignment lens on spec round 3 — BECAUSE the staged
spec edits reach no `docs/` surface and every accepted or deferred failure mode I could enumerate already
lands in staged spec text — ALTERNATIVES: filing the missing "Edge cases and accepted failure modes" section
(rejected: proposal-structure hygiene, remedy is not a staged spec edit, and the material skeptic has already
refuted two hygiene findings on this proposal); filing the §10.1.8 step-1 quiescence imprecision (rejected:
round 6 weighed and declined it, and it is indexed in the standing context's `### Open` for the human pass).

FACT: the proposal stages no docs edit and mentions `docs/` nowhere outside its review log — EVIDENCE:
`grep -n "docs/\|runbook\|metrics.md\|alert"` over the proposal's non-review-log files returns nothing.

FACT: the underscored identifier surface outside `spec/` is two files, and neither states a unit, a baseline,
or a gate. `grep -rln "coordination_generation\|coordinationGeneration" schemas/ charts/ docs/ sdks/` returns
`schemas/lenny-adapter.proto` and `docs/getting-started/concepts.md` alone, and the latter's only paragraph
is unit- and baseline-neutral — EVIDENCE: docs/getting-started/concepts.md:101.

FACT: the observability carriers SPEC-1 and SPEC-2 re-scope are closed at three spec lines, all staged.
`coordinator_connection_lost` occurs at spec/10_gateway-internals.md:60 and
spec/29_communication-scenarios.md:1274 and nowhere else in `spec/`, `docs/`, `schemas/`, or `charts/`;
`coordinator_generation_gap` adds spec/28_communication-channels.md:335 and :1807 plus
schemas/lenny-adapter.proto:160 and :1461, all named by SPEC-2 or SCHEMA-1.

FACT: §16's two coordination entries are unit-neutral and survive the change unedited —
EVIDENCE: spec/16_observability.md:183 ("increments when a replica receives a generation-stale rejection"),
:185 (`lenny_adapter_coordinator_hold`, "1 while the adapter is in hold state"). No alert or runbook is
reached, so tier 11 has no alert-to-runbook consequence here.

FACT: the `spec/04` statements outside SPEC-3's §4.2 paragraph carry no baseline and are not edit sites —
EVIDENCE: spec/04_system-components.md:323 ("the coordinator generation counter"), spec/07:215 and :398,
spec/16:208, each of which states monotonicity or a bump rather than an initial value.

USEFUL [standing context, "The `docs/` surface is eight sites and states no unit, baseline, or gate"]: it
named the exact eight sites and its conclusion held on re-derivation from the identifier grep, which is what
let this pass stop at verification rather than re-walk the docs tree a ninth time.

WATCHOUT: in this spec-scoped loop the docs lens has almost no filable surface. A docs page made wrong by the
staged edits is fixed in the docs edit list, which lives in the non-spec staging this loop may not edit, and
guardrail (1) bars reconciling the spec toward a doc. What remains in scope is only an accepted or deferred
failure mode whose outcome lands in no staged spec sentence. Budget the pass accordingly rather than
re-deriving the docs surface.

FACT: the D5 residual named in §6 ("a pod whose CH-ADAPTEREVENTS stream holder crashes freezes co-tenant
sessions whose own coordinators are alive") does land in staged spec text: SPEC-2's new §29.10 "Shared by the
whole pod" bullet states the whole-pod failure, the pod-wide `UNAVAILABLE` rejection, and the hold timeout
terminating every session the adapter has started on the pod — EVIDENCE: spec-changes.md:439-444, against
live spec/29_communication-scenarios.md:1472. Do not re-file it as an undocumented accepted failure mode.


### [spec.3.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens — BECAUSE every identifier the staged
edits add, change, or retire was swept across `spec/`, `docs/`, `schemas/`, and `charts/`, and every
surface a sweep returned is either staged or genuinely unit-neutral — ALTERNATIVES: filing the
`tests/claim-map.json` row question and the `coordinator_lost` log-line question, both rejected because
their remedy is outside criterion (d)'s four trees or is already a standing OPEN that earlier rounds
weighed and declined.

FACT: the proposal is byte-identical to both `scratchpad/cp-snap/0076-run4/spec-r3` and `spec-r3-start`,
so round 3's spec fixer made no edits and there is no fix-stage text newer than pass 22 to scrutinise.
`diff -rq` against either snapshot returns nothing — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:1815 (pass 22 is the last record)

FACT: the proto carrier set is now closed and countable, which is the cheapest way to re-check SPEC-2's
closing paragraph. `grep -c "int64 coordination_generation" schemas/lenny-adapter.proto` returns 14 and
`grep -c "gateway's view of the active"` returns 13; the 13 are the 12 operational field comments SPEC-2
names by message plus `CheckpointBarrierRequest`'s at :1477, and the 14th is `CoordinatorFenceRequest`'s
own comment at :1449-1451. No 15th carrier exists — EVIDENCE: schemas/lenny-adapter.proto:969, :995,
:1046, :1070, :1091, :1114, :1172, :1305, :1393, :1477, :1531, :1576, :1618, :1449

FACT: the pod-singular phrases have exactly the coverage SPEC-1 and SPEC-2 claim, re-derived from scratch
this round rather than trusted. `current generation` outside a card returns only spec/28:1680 and :1806,
both staged; `no window` returns only spec/10:41, staged; `last known generation` returns only spec/10:60
and spec/29:1274, both staged; `coordinator_connection_lost` returns the same two lines. `spec/16` carries
no structured-event catalog entry for `coordinator_connection_lost` or `coordinator_generation_gap`, so
the §10.1.4 Observability edit reaches no observability inventory — EVIDENCE: spec/16_observability.md:183,
:185 (the only two coordination rows, both unit-neutral)

FACT: the four `spec/29` "the pod ... rejects a stale one" sentences that a naive sweep flags are all
non-sites under SPEC-2's own criterion, because none fixes the compared value: spec/29:622 (§29.4-area
Interrupt step), :819 (`CH-CHECKPOINT` step 3), :1013 (resume framing), and :1263-1264 (§29.8
Preconditions). Do not re-derive these; the criterion decides all four the same way it decides §28.5.1's
`CH-ATTACH` Preconditions bullet — EVIDENCE: proposals/.../0076_....spec-changes.md:408-411

FACT: `spec/07:93`'s derive-failure `coordination_generation` CAS fence is a gateway-and-Postgres-side
fence on a session row, not a pod-side gate, so it survives both the per-session move and the baseline
shift untouched. It is the one `current generation` hit outside `spec/28` and it costs a full-line read to
rule out — EVIDENCE: spec/07_session-lifecycle.md:93 ("the acquiring gateway replica must hold the current
generation stamp obtained at derive admission")

WATCHOUT: §29.10's "Partitioned per slot" coordination bullet does not end where an earlier pass record
says it does. Pass 4's correction quotes it as ending at "so each slot's session carries its own lease and
its own generation" (`spec/29:1464-1468`), but the bullet continues for two more lines with the
cross-reference to the "does not state" list. SPEC-2's own edit instruction is right (it says the bullet
keeps its closing cross-reference); the pass record is the misleading one, and reading the pass record
instead of the file makes SPEC-2 look like it deletes that cross-reference — EVIDENCE:
spec/29_communication-scenarios.md:1469-1471

USEFUL [Settled: "Derived inventories. Do not re-derive any of these."]: the `docs/` eight-site list and
the "no alert, runbook, or tier-11 test is reached" claim both held on independent re-derivation this
round, and the entry saved re-reading `pkg/alerting/rules` and `docs/runbooks/`.


### [spec.3.review-feasibility.1]

DECISION: Returned an empty findings list — BECAUSE every actor the staged spec edits name can perform the action assigned to it, and
every actor-side citation I re-opened resolves — ALTERNATIVES: I weighed and rejected four candidates, each recorded below so nobody
spends a verification pair on them again.

FACT: the proposal is byte-identical to the round-3 start snapshot. `diff -rq scratchpad/cp-snap/0076-run4/spec-r3-start
proposals/0076_.../` is empty, as is the diff against `spec-r3`, so round 3 landed no fix and "read the newest text hardest" resolves to
pass 22 (spec-changes.md:1815-1844), the operational-RPC field-comment rewrite.

FACT: the three adapter-side actions SPEC-1 newly assigns are all backed by an existing accessor, so none of them is a feasibility
finding. The pod-level `coordinator_connection_lost` line already emits `started_sessions` beside `last_generation`, so dropping the
generation and keeping the count is a deletion rather than a new capability — EVIDENCE: pkg/adapter/holdstate.go:119-132 (`gen :=
s.LastFencedGeneration(); started := s.startedSessionCount()`, then `slog.Warn("coordinator_connection_lost", "started_sessions",
started, "last_generation", gen)`). D5's sole-arming-signal ground holds: `AdapterEvents` refuses a second stream per pod with
`FailedPrecondition` (pkg/adapter/adapterevents.go:90-96) and `onCoordinatorChannelClosed` arms off that close with no session id
(holdstate.go:90-100).

FACT: the four gateway-side actor citations that carry D7 and §7's first open decision all resolve to the component the proposal names.
`fenceResumedPod` is declared at pkg/gateway/sessionserver/start.go:4233 and calls `s.fencer.Fence` at :4237, with the two resume call
sites at :3975 and :4067; the sweeper's re-adopt is coordination.go:399 into cmd/lenny-gateway/coordination_seams.go:155 and the
`fencer.Fence` at :233; the barrier's generation is copied onto the wire at pkg/gateway/coordination/barrier/wiring.go:49 and
`FailedPrecondition` maps to `ErrGenerationStale` at :51-53; the mirror read that supplies it is
`MirrorTargetLister.Targets`/`ListHeldByReplica` at wiring.go:97-116, and the cache fallback's zero seed is
cmd/lenny-gateway/httpsurface.go:592-599. `upsertMirror` is coordination.go:430.

FACT: spec/18 puts the session store and `CoordinatorFence` in Phase 4 (spec/18_build-sequence.md:218 heading, :224, :238) and
`CheckpointBarrier` in Phase 8 (:398 heading, :404), so SPEC-3's counter baseline on the §4.2 session record sits in the same phase as
the fence and strictly before the barrier. There is no phase inversion in either direction; do not re-derive this.

WATCHOUT: the staged §10.1.8 step-1 sentence "the generation a barrier carries is read from the session's coordination state when the
barrier-target set is assembled" sits one sentence away from §10.1.8's own unchanged query literal `SELECT session_id FROM
coordination_lease WHERE coordinator_replica = $this_replica_id AND released_at IS NULL`, which selects no generation — EVIDENCE:
spec/10_gateway-internals.md:183. I did not file it: §29.7 step 4's trace states the same read as "the `coordination_lease` rows"
(spec/29_communication-scenarios.md:1178-1179) and the code reads the generation off that row (wiring.go:110-113), so the staged sentence
has a true referent and the narrow `SELECT` list is pre-existing spec imprecision rather than something this change falsifies. A later
lens that wants it should file it as the shipped §10.1.8 query list, not as a defect in the staged sentence.

WATCHOUT: the pass-22 replacement clause for the twelve operational-RPC field comments keeps the words "A pod validates the generation on
every gateway-to-pod RPC", which the proposal's own D7 says is false of the tree ("`coordination_generation` is read on the fence path and
the barrier path alone", pkg/adapter/coordination.go:92 and :223, which I confirmed: no other handler reads the field). It is not a
finding, because the same universal is the shipped §10.1.1 sentence (spec/10_gateway-internals.md:30) and the shipped §28.6 guard
sentence (spec/28_communication-channels.md:1672-1673), so the clause is carrying a spec-versus-code drift that predates this proposal
and that SCHEMA-1 is not staged to fix. Do not file it as an attribution error against pass 22.

USEFUL [Standing context, "Derived inventories"]: the closed statement that every proto message declaring `coordination_generation` is
session-addressed is what let me clear staged step 3's "the session the RPC names" for the whole operational-RPC domain in one step
instead of walking twelve messages.

USEFUL [Standing context, "Every confirmed finding since round 1 sits in the D7, counter-baseline, and barrier-provenance cluster"]: it
correctly predicted where the remaining risk is. The per-session move itself (D1-D6) is feasible on every actor I checked, and my time
was well spent on D7 and the baseline rather than on the move.


### [spec.3.review-fresh.1]

DECISION: returned an empty findings list — BECAUSE every anchor SPEC-1/SPEC-2/SPEC-3 quote resolves verbatim (re-checked independently this round: spec/10:30, :37, :38, :41, :58, :60, :183, :184, :198; spec/28:237-240, :251-253, :291-296, :330-331, :349-353, :361-365, :1679-1685, :1805, :1806, :1807, :1808; spec/29:1150-1152, :1186, :1274, :1322-1326; spec/04:200; proto :153-162, :165-179, :1442-1446, :1449-1451, :1455-1462, :1469-1483, :1477-1479), the edit-site criterion is applied consistently across the §28/§29 sweep, and the three predicates the edits install (step 3 acceptance, §10.1.8 step 1, §28.8 CH-BARRIER) agree word-for-word — ALTERNATIVES: I weighed two candidates and dropped both, recorded below.

FACT: the "no window in which both the old and new coordinator can simultaneously issue accepted RPCs" claim that staged step 3 deletes has exactly one occurrence in the tree, spec/10:41 — the three other `no window in which` hits are unrelated credential/token comments. No mirror is orphaned by the deletion — EVIDENCE: grep -rn "no window in which|simultaneously issue" over spec/ docs/ schemas/ pkg/ tests/ returns spec/10_gateway-internals.md:41, pkg/gateway/credentials/denylist/denylist.go:6, pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go:242, tests/tier2_component/issuedtokencascade/cascade_test.go:172.

FACT: "matches the fenced value" and "last fenced generation" occur nowhere in docs/ or charts/; outside spec/10:41 the only carriers are the five proto sites SCHEMA-1 already lists (schemas/lenny-adapter.proto:167, :1457, :1465, :1472, :1479). The pod-singular gate has no docs mirror to miss — EVIDENCE: grep -rn "last_fenced_generation|last fenced|fenced value" spec/ docs/ schemas/*.proto.

FACT: a derived session is created through the normal create path and inherits no counter, so SPEC-3's "a newly created session row carries coordination_generation = 1" has no derive-path exception — EVIDENCE: spec/07_session-lifecycle.md:95 ("Derive creates a fully independent session"); spec/07:215 is a bump on an existing row, not a create.

WATCHOUT: SPEC-2's §28.5.1 `CH-FENCE` Exclusivity bullet closes its rationale with "and SPEC-1 leaves step 2 unchanged" (spec-changes.md:331), which is false on its face: SPEC-1's own edit list stages step 2's record-and-reject sentence (spec-changes.md:151-153) and its own step-3 rationale calls that "the fence-announcement edit" (:174). I did NOT file it, because criterion (c) requires the contradiction to make the applied spec wrong and this one does not: the sentence the rationale actually relies on, step 2's window clause at spec/10:38, is genuinely untouched, and the edit list is unambiguous. It is rationale that lands nowhere in spec/, which is the exact ground the "no second value" refutation stands on. A future round should treat this as weighed-and-declined rather than re-derive it; the cheap repair is to narrow the clause to "SPEC-1 leaves step 2's window sentence unchanged".

DEFERRED [tests/claim-map.json]: §28.4 states that "Every normative statement this section makes about a mechanism carries a row in the claim register at `tests/claim-map.json`", and a non-`WIRED` row "names, through a deferral identifier, the step that closes it" (spec/28_communication-channels.md:163-169); `.claude/rules/channel-naming.md` restates it as "the claim-register row in §28.4 for any part of the contract that does not yet hold in code". SPEC-2 stages §28.5.1/§28.6/§28.8 statements that do not hold in the shipped adapter until CODE-1 and CODE-2 land (per-session recording, the unset arm, barrier acceptance when the pod holds no value), while asserting "No §28.4 claim-register row moves ... that file is not opened by this proposal" (spec-changes.md, SPEC-2). That assertion answers whether a row *moves*, not whether one must be *added or restatused*. What is true instead: the fence/barrier rows need an `ABSENT`-or-deferred status naming S3/S5 for the interval between S1 and S5. I could not file it here: the remedy lands in `tests/claim-map.json`, which criterion (d) does not reach and this loop may not edit. The non-spec loop owns it. This supersedes the vaguer standing OPEN "Claim-map row for the new §28.5.1 sentence. Whether §28.4's rule obliges one."

USEFUL [Settled: "The `spec/28` and `spec/29` edit sites are settled by one membership criterion"]: I re-tested the criterion against three bullets it does not name — §28.5.1's `CH-BARRIER` Preconditions ("The generation stamp and the fence acknowledgement that govern every gateway-to-pod RPC", spec/28:354-357), §28.5.1's `CH-PODHEALTH` Preconditions (:389-392), and §28.5.2's Preconditions (:447-449). All three defer to §10.1 without fixing a compared value, so all three are non-sites and the sweep is complete. Saved a full re-derivation of the §28 surface.


### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens on run 4 round 3 — BECAUSE none of the
staged edits touches a CRD, a status subresource, a finalizer, an admission webhook, a work queue, or a
controller reconcile. The whole subject matter (the coordination generation) lives on the Postgres `sessions`
row (`spec/04_system-components.md:200`, the §4.2 Session Manager bullet SPEC-3 edits), in Redis/Postgres
`REG-COORDLEASE`, and in adapter process memory. ALTERNATIVES: I considered filing the D5 residual (a pod
whose CH-ADAPTEREVENTS holder crashes freezes co-tenant sessions) as a controller-on-hot-path or
level-triggering defect and rejected it: §10.1.4's "Whole-pod connection loss" paragraph
(`spec/10_gateway-internals.md:62`) already fixes total connection loss as a whole-pod failure that puts
every slot into `resume_pending` and fires the whole-pod replacement trigger, so the posture is shipped spec
that D5 explicitly does not change, and §6 records it as a non-goal.

FACT: run 4 round 3 opened with `diff -ru scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returning
nothing at all. The round-2 fixer landed no edits, so every lens in this round is reading exactly the text
round 2 reviewed. Do not spend a pass looking for "what changed since last round" — EVIDENCE: diff exit 0.

FACT: nothing in `spec/06_warm-pod-model.md` or `docs/reference/state-machines.md` mentions hold state,
`coordinator_hold`, or the coordinator hold at all, so SPEC-2's §29.10 reclassification of the hold as
"Shared by the whole pod" has no per-slot-substate mirror to falsify. This closes the one plausible
unstaged-site lead a Kubernetes/state-machine reading generates — EVIDENCE: grep over both files returns
zero hits; `spec/29_communication-scenarios.md:1461-1491` is the §29.10 classification the edit lands in.

FACT: the zero-RBAC / no-apiserver-path posture is already correctly handled in the staged §10.1.4 text.
Pass 5's record shows an earlier draft called the local-disk post-mortem "the terminal record" and was
corrected, because §10.1.4 assigns the terminal transition to the gateway's orphan session reconciler and
states that agent pods cannot write `Sandbox.status.phase`
(`spec/10_gateway-internals.md:58-59`). The live staged sentence identifies the artifact by what §10.1.4
says about it, and adds no pod-side apiserver duty — EVIDENCE:
0076_...spec-changes.md:782-790 (the pass-5 record), :281-288 (the live staged §10.1.4 text).


### [spec.3.review-mechanism.1]

DECISION: returned no findings — BECAUSE every mechanism-level tension I could construct in the staged
SPEC-1/2/3 text is already in the standing context's `### Open` index or its refuted-class list, and
re-filing one costs two verifiers and closes nothing. ALTERNATIVES: filing the §28.6 second-opener's
silence on an *equal* fence generation (staged "rejects a fence carrying a generation older than the one
it holds ... and records a higher one" leaves `gen == fenced` unstated while the proto says "not greater
than ... rejected") — rejected because the shipped sentence it replaces is equally silent, the
older-than/other-than asymmetry is already recorded as shipped text, and no applied sentence contradicts
another.

FACT: the whole proposal directory is byte-identical to both `spec-r3` and `spec-r3-start` snapshots
(`diff -rq` empty in both directions), so rounds 2 and 3 of run 4's spec lane produced no text change at
all. The "read the changed sections first" instruction has no changed sections to point at this round; do
not spend time looking for a diff. EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3 vs
proposals/0076_fix_scope-the-coordination-generation-to-the-session

FACT: every anchor SPEC-1, SPEC-2, and SPEC-3 quote resolves verbatim, re-checked independently this
round against the tree rather than against the standing context's inventory: spec/10:30 (Generation
counters), :37 (step 1 CAS), :38 (step 2 window), :41 (step 3 "matches the fenced value"), :58 (hold
timeout / post-mortem), :60 (Observability), :183 (both §10.1.8 step-1 anchors on one physical line),
:184, :185, :198; spec/28:315 (CH-FENCE Messages), :330-331, :335 (Degradation gap), :1675 (One holder),
:1679-1681, :1683-1685, :1806, :1807, :1808; spec/29:1150-1152, :1186, :1274, :1322-1326, :1523 (the
removed "does not state" bullet); spec/04:200. EVIDENCE: the greps in this shard's session.

FACT: the proto carrier arithmetic in SPEC-2's closing paragraphs checks out exactly. `int64
coordination_generation` occurs 14 times in `schemas/lenny-adapter.proto` (:974, :1002, :1051, :1075,
:1096, :1119, :1179, :1310, :1398, :1452, :1480, :1536, :1581, :1623); minus the fence and barrier fields
that is the twelve operational comments SPEC-2 enumerates, and each listed comment range sits immediately
above its field. `:1465` genuinely carries no leading comment. Do not re-derive this.

FACT: `coordinator_generation_gap` and `last_fenced_generation` together occur in `spec/` at exactly four
sites — spec/10:40, spec/28:335, spec/28:1807, spec/29:1309-1311 — which is precisely the four mirrors
SPEC-1 and SPEC-2 stage, and nowhere in `docs/` or `charts/`. `coordinator_connection_lost` occurs in
`spec/` only at spec/10:60 and spec/29:1274, both staged. EVIDENCE: grep over spec/ docs/ schemas/ charts/.

FACT: the §29.10 bullet SPEC-2 deletes (spec/29:1523, "Whether the adapter's hold state is partitioned per
slot") has no inbound reference anywhere in `spec/`, `docs/`, or `tests/`; the "Partitioned per slot"
coordination bullet's closing cross-reference points at the *two-replicas* bullet, which SPEC-2 keeps.
EVIDENCE: spec/29_communication-scenarios.md:1441-1447, :1523-1527, :1541-1543.

WATCHOUT: the staged "Partitioned per slot" addition ("a fence for one slot's session neither fences nor
unfences another") and the staged "Shared by the whole pod" hold bullet ("a successful fence for any one of
those sessions exits the hold for the pod") describe two cross-slot effects of the same RPC in two lists
whose preambles say opposite things about independence. This is deliberate — the first is scoped to the
recorded generation, the second to the hold — and refuted class (g) covers it. A later reader will
rediscover it; it is not a finding. EVIDENCE: spec/29_communication-scenarios.md:1461-1462, :1487-1488.

UNVERIFIED: staged §10.1.8 step 1 gains "The generation a barrier carries is read from the session's
coordination state when the barrier-target set is assembled", while the same step's own stated query is
`SELECT session_id FROM coordination_lease ...`, which selects no generation. The step hedges with "of the
form", so I did not file it, but nobody has checked whether the applied step reads coherently to someone
who takes the quoted SQL as the assembly's whole read. EVIDENCE: spec/10_gateway-internals.md:183.


### [spec.3.review-operational.1]

DECISION: returned an empty findings list — BECAUSE the whole observability surface this change could reach
is unchanged by it, and I re-derived that independently rather than trusting the standing context.
ALTERNATIVES: (i) filing the §10.1.4 `coordinator_lost` log line as a spec artifact no section introduces —
rejected, the adapter does emit it (`pkg/adapter/holdstate.go:225` via `reasonCoordinatorLost`) and
§10.1.4 already names `coordinator_lost` as the termination reason at spec/10_gateway-internals.md:58, so
the staged sentence describes an existing emission rather than inventing one; (ii) filing D6's ground
("a session that has never been fenced on this pod has accumulated no state for the gap path's reset to act
on", spec-changes.md:37-39) as falsified by staged step 3's own unset clause, which has the pod accept that
session's operational RPCs from any replica — rejected as rationale that lands nowhere in `spec/`, the exact
ground on which the "no second value" finding was killed; it is already carried as an UNVERIFIED and belongs
to the human-review pass; (iii) filing §10.1:30's "Pods validate the generation on every gateway→pod RPC …
This prevents split-brain" as the same unconditional-consequence defect SPEC-2 stages onto twelve proto
comments — rejected, split-brain is only reachable after a takeover and after a takeover the pod holds a
recorded value, so :30 stays true where the proto clause ("a replica that has lost coordination cannot drive
the pod") does not.

FACT: the alert surface this proposal can reach is one alert and it is untouched. `CoordinatorHandoffSlow`
is the only coordination alert in the tree, it evaluates
`histogram_quantile(0.95, … lenny_coordinator_handoff_duration_seconds_bucket) > 5`, and its runbook
mentions no generation, fence, or hold — EVIDENCE: pkg/alerting/rules/rules.go:1583-1587;
spec/16_observability.md:552; docs/runbooks/coordinator-handoff-slow.md:28-41 (a grep for
`generation|fence|hold state|coordinator_connection_lost` over that file and
`gateway-replica-failure.md` returns nothing).

FACT: the coordination metric inventory is four rows and every one stays true after the change.
`lenny_coordinator_handoff_stale_total` ("increments when a replica receives a generation-stale rejection"),
`lenny_adapter_coordinator_hold` (gauge, 1 in hold state), `lenny_coordinator_handoff_duration_seconds`,
`lenny_coordinator_fence_relinquished_total` — EVIDENCE: spec/16_observability.md:183, :185, :190, :192,
mirrored at docs/reference/metrics.md:307, :309, :312. None states a unit for the fenced value, so
per-session scoping falsifies none of them, and the change only removes false increments.

FACT: the pod-level hold log line already carries the field the staged §10.1.4 text says it names.
`enterHoldState` emits `coordinator_connection_lost` with `started_sessions` and `last_generation`, so
SPEC-1's "names the number of started sessions the pod holds and carries no generation" asks for a deletion
of one existing key rather than the addition of a new one — EVIDENCE: pkg/adapter/holdstate.go:129-132.

FACT: §28.8's fifth column is "Operator observable" and none of its cells is made wrong by this change.
`CH-FENCE` names the `coordinator_generation_gap` event and the `coordinator_lost` termination; `CH-BARRIER`
names `manifest_reason="timeout"`, `lenny_checkpoint_barrier_ack_total`,
`lenny_checkpoint_barrier_ack_duration_seconds`, and `lenny_prestop_barrier_target_source_total`;
`CH-ATTACH` names the `coordinator_hold` detail. SPEC-2 stages only the fourth column of three rows, so no
observable cell needs an edit — EVIDENCE: spec/28_communication-channels.md:1803 (header), :1805-:1808.
Do not re-derive this column; it is the one part of §28.8 an operational lens would open first.

FACT: `lenny_checkpoint_barrier_ack_total`'s outcome set is `success`, `timeout`, `partial_captured`,
`error`. D7 moves a rejected barrier's accounting from `error` to `success`, which is a value shift inside
an existing label set rather than a new label, so §16's row and the §28.8 `CH-BARRIER` observable cell need
no edit — EVIDENCE: spec/16_observability.md:41.

FACT: the counter baseline (0 to 1) touches no observability statement. The two §16 rows that mention
`coordination_generation` state relative comparisons only — the supersede condition ("at or below the
incoming `coordination_generation`") and the derive-audit `fenced` outcome ("a replacement coordinator had
already advanced `coordination_generation`") — as do §10.1's supersede-on-write and reassembly predicates —
EVIDENCE: spec/16_observability.md:199, :208; spec/10_gateway-internals.md:153, :157, :171.

WATCHOUT: `coordinator_connection_lost` has exactly two carriers in `spec/` and both are staged, but the
§29 one sits in §29.8 step 2 rather than in §29.10, and it cites §16.1 as well as §10.1. An operational lens
that greps for the event name and then looks for a §16 edit site finds the citation and not a statement:
§16 never names the event — EVIDENCE: spec/29_communication-scenarios.md:1273-1276;
spec/10_gateway-internals.md:60.

USEFUL [standing context, "The `docs/` surface is eight sites and states no unit, baseline, or gate"]: I
re-derived the docs and runbook surface anyway (grep over `docs/` for
`coordination_generation|last_fenced|coordinator_hold|handoff`) and it agrees exactly. The entry also warns
that "the per-lens re-derivation is what costs rounds", which is right: the whole re-derivation above is
about twenty minutes and has now been paid at least nine times. It should be retired from every lens's
scope once the code lane lands.


### [spec.3.review-performance.3]

DECISION: returned an empty findings list on the staged spec edits (SPEC-1/2/3) — BECAUSE the staging is
write-neutral (no new per-task, per-request, or per-session store write, no new watch or informer cache, no
hot key, no single-leader serialization), and every failure-mode candidate I derived independently landed in
a refuted class, a recorded OPEN, or the non-spec lane — ALTERNATIVES: the three candidates below.

FACT: `diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` is empty this round: the staged
spec text has not moved at all since the round-3 snapshot, and pass 22 (the newest entry, spec-changes.md
:1815-1844) is the last text change, an edit to SPEC-2's wire-mirror carrier paragraph over the twelve
operational-RPC `coordination_generation` proto comments. Nothing pass 22 touches has a load or failure
consequence: proto doc comments carry no runtime behaviour.
— EVIDENCE: proposals/0076_.../0076_....spec-changes.md:1815-1844

USEFUL [spec.1.review-performance.1] and USEFUL [non-spec.5.review-performance.1]: between them they
already carry the whole cost model I would otherwise have re-derived — `dispatchOne` starts the
gateway-driven `Checkpoint` stream before `dispatch.Send`, so D7 adds no drain work; the pod-level op lock
serializes co-tenant checkpoint uploads whatever the barrier gate does; `lenny_adapter_coordinator_hold`
and `lenny_coordinator_handoff_stale_total` are unit-neutral so D5 reaches no alert or runbook; and the
per-session gap reset is strictly less collateral than the shipped pod-wide clause (a). Three performance
passes have now returned empty on this staging.

FACT [candidate killed]: §10.1.8's own failure surface is unchanged by D7. The BarrierAck-timeout
partial-capture path (rules 1-5) reads only Postgres intent-row state the gateway has already committed,
completes in milliseconds, and does not extend the drain budget, and the closing sentence's bound of one
in-flight tool call per session survives an accepted barrier because step 2's quiescence is what enforces
that bound. The Tier-3 400-concurrent-upload burst in step 3 is sized against the barrier-target set, whose
size D7 does not change: D7 changes whether a target's barrier is accepted, never how many targets there are.
— EVIDENCE: spec/10_gateway-internals.md:185 (step 3, 400-session burst), :190-197 (timeout rules),
  :198 (the one-tool-call bound)

DECISION: did not file the acceptance-arm quiescence stall (a false-positive barrier from a superseded
draining replica is now accepted, so a healthy session coordinated by a live successor is quiesced for up to
the 90s `checkpointBarrierAckTimeoutSeconds` while its own pod is not draining) — BECAUSE the standing
context records round 6 as having weighed and declined exactly this, and the `### Open` index carries it as
"Superseded replica's stream against a quiesced pod", routed to the human-review pass; re-filing a recorded
open disposition spends two verifiers and closes nothing — ALTERNATIVES: filing it as "a failure mode less
reliable than the shipped design", which is the strongest reading my lens supports and which the human pass
should adjudicate rather than a fixer.

DEFERRED [proposals/0076_.../0076_....non-spec-changes.md, CODE-4]: CODE-4's migration 0181 replaces
`CHECK (coordination_generation >= 0)` with `>= 1` (non-spec-changes.md:118-125), and migrations in this tree
run as a Helm `pre-install,pre-upgrade` hook at weight -5, i.e. while 100% of the OLD gateway fleet is still
serving. `pgstore.Create` is the tree's only production `INSERT INTO sessions` and names
`coordination_generation` in its column list bound from the struct with no floor, so every old-binary insert
writes a literal 0 and every `session.create` fails for the whole rolling window. spec §10.5 states the
expand-contract rule for precisely this case: a constraint old-version replicas' writes violate may only be
added after every replica runs the new code, in a separate migration file AND a separate deployment. The
summary's "CODE-4's migration and both session-store `Create` floors land in one commit" addresses in-commit
ordering only and does not reach deploy ordering. What is true instead: 0181 needs the §10.5 phase split, or
an explicit statement of why it is exempt. The remedy is entirely in the non-spec staging, so this loop may
not land it. `[non-spec.5.review-performance.1]` recorded the three underlying FACTs but its only DECISION
declined the *lock and backfill cost*, not this; nobody has decided this one.
— EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16, :38-39; spec/10_gateway-internals.md:429-433;
  pkg/gateway/session/sessionstore/pgstore/pgstore.go:170, :177;
  proposals/0076_.../0076_....summary.md (the "Watch out for" paragraph)


### [spec.3.review-reliability.1]

DECISION: Returned an empty findings list for the reliability lens on spec round 3 — BECAUSE every crash,
restart, and store-failover path I traced through the staged edits either lands in the standing `### Open`
index already, sits in a refuted class, or is unchanged shipped behaviour — ALTERNATIVES: I weighed and
rejected filing four candidates, each listed below with why.

FACT: `diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` is empty. The round-3 fixer made
no edits to any proposal file, so the "read the changed sections first and hardest" instruction had no
target this round and the whole document is round-2 text. Do not spend time hunting a diff.

FACT: the four reliability candidates I traced and did NOT file, so the next reliability lens can skip them:
(1) Superseded replica's accepted false-positive barrier consumes drain budget and drives a `Checkpoint`
stream for a session it no longer coordinates. Bounded: §10.1.8 step 3's deadline is one wall-clock 90s
across all pods, not per pod (spec/10_gateway-internals.md:186), so a false positive extends no budget, and
the manifest write is guarded by supersede-on-write plus `partial_manifest_active_uniq`
(spec/12_storage-architecture.md:340). It is the same acceptance-arm mechanism round 6 weighed and left for
human review, so a variant filing would be a close variant.
(2) Postgres failover losing the step-1 CAS commit after the replica has already stamped `RETURNING`
G+1 and fenced the pod at G+1: the row reverts to G, the next takeover mints G+1 again, and the pod refuses
that fence as `gen <= lastFenced`, producing exactly the §1.3 stall from a store fault rather than from
co-tenancy. Real, but pre-existing in shipped spec text, untouched by any staged edit (§10.1.2 step 1 is
explicitly unchanged, spec-changes.md:556), and spec/12_storage-architecture.md:160 already files
uncommitted writes as "At-risk". Filing it is scope creep onto the already-open "Scope of the proposal"
question.
(3) Hold exiting on any one session's fence while co-tenant sessions whose coordinators are dead stay
unterminated: the reclaimer exists (lease expiry then sweeper crash-takeover), and the fix makes that
re-adoption fence succeed where the pod-wide counter refused it, so the proposal adds the reclaimer rather
than removing one. Shipped `exitHoldState` behaviour is unchanged by D5.
(4) Deleting the gateway fence path's floor of a zero row (CODE-4) removing a defensive fail-safe: the
staged §10.1 text keeps the adapter's non-positive refusal as the fail-closed backstop
(spec-changes.md:252-255), so the posture is fail-closed, and the remedy would be code-lane anyway.

FACT: §10.1.3 item 1's dual-store claim ("the pod validates the generation stamp, which remains valid
because no new coordinator can increment it while Postgres is down", spec/10_gateway-internals.md:66) is
NOT falsified by the staged step-3 unset clause. For a never-fenced session the pod rejects nothing, so the
stamp still "remains valid"; the two sentences never meet. I checked this because it is the one §10.1
subsection the proposal's edit lists never name — EVIDENCE: spec/10_gateway-internals.md:63-68.

FACT: `coordinator_connection_lost` and `coordinator_lost` occur across spec/, docs/, pkg/alerting/, and
charts/ at exactly six sites, and the two the pod-level generation removal touches are the two SPEC-1 and
SPEC-2 already stage. No alert rule, runbook, or chart reads either — EVIDENCE: spec/10:58, spec/10:60,
spec/28:338, spec/28:1807, spec/29:1255, spec/29:1274, spec/04:747; `grep -rn` over pkg/alerting/ and
charts/ returns nothing. This confirms the standing-context claim from the reliability side; do not
re-derive it.

USEFUL [Standing context, "Refuted classes" and "### Open"]: the Open index and the refuted-class bodies
together disposed of every candidate my lens generated except the four above, at a cost of one read. Read
both before tracing anything.


### [spec.3.review-security.1]

DECISION: returned an empty findings list for the security lens over the staged spec edits — BECAUSE every
security-relevant consequence of SPEC-1/2/3 traces to a class the standing context already adjudicates (D6's
exemption-unit under refuted (b), the step-3 unset clause under refuted (e) and (f), the barrier's pod
self-report `last_fenced_generation` reaching no gateway decision, the "unreachable by construction" invariant
under refuted (j)), and the two residual weakenings I derived independently are not regressions of a designed
control — ALTERNATIVES: I considered filing (i) the staged §28.6 second-opener clause "the pod rejects none of
that session's RPCs on generation grounds" as removing one of §28.6's two stated guards (lease + stamp) for the
never-fenced session class, and (ii) SPEC-1's replacement of step 3's shipped no-window invariant
(`spec/10_gateway-internals.md:41`, "there is no window in which both the old and new coordinator can
simultaneously issue accepted RPCs to the pod") with a weaker sentence. Both were dropped: see the two FACTs
below.

FACT: the never-fenced-session class contains no stale coordinator, which is why D6/D7 open no new split-brain
window. A session holds no pod-side fenced generation only when it was neither resumed nor taken over
(`CoordinatorFence` has exactly two senders), so exactly one replica has ever coordinated it. The one
interleaving that produces a stale sender against an unfenced session — successor CAS lands, successor's fence
exhausts its 3 retries and relinquishes, predecessor keeps sending — is a window §10.1.2 step 2 already
sanctions in shipped text ("Until the pod acknowledges the fence, the pod still accepts RPCs carrying the
previous generation") — EVIDENCE: spec/10_gateway-internals.md:38, :36 (step 2 retry/relinquish bullet).

FACT: the only behaviour the per-session move actually loses is an ACCIDENTAL rejection, never a designed one.
On a multi-session pod today the pod-wide `initialized`/`lastFenced` pair rejects a co-tenant session's RPC by
comparing it against an unrelated session's value; that is the defect the proposal fixes, so its disappearance
is not a control regression. No operational RPC is gated in code at all — the adapter reads
`coordination_generation` on the fence path and the barrier path only — so step 3's gate is spec-only on
`CH-ATTACH`/`CH-CHECKPOINT` either way — EVIDENCE: pkg/adapter/coordination.go:92, :223.

FACT: the §10.1.4 Observability change (dropping the generation from the pod-level `coordinator_connection_lost`
line) reaches no alert, runbook, or chart. `lenny_coordinator_handoff_stale_total` and
`lenny_adapter_coordinator_hold` carry no alert rule; the only `SplitBrain` alert in the tree is
`OpsLockSplitBrainDetected` over the remediation ops lock, which is unrelated — EVIDENCE:
pkg/alerting/rules/rules.go:664-665; charts/lenny/files/alerting-rules.yaml:325-326; spec/16_observability.md:183.

FACT: §10.1.4's hold allowlist drift is real but pre-existing and larger than the standing context's one-line
note suggests. Spec says `CoordinatorFence` is the only RPC served in hold state; the code allowlist carries
five methods (`CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, `Health/Check`, `Health/Watch`). SPEC-2's
new §29.10 "Shared by the whole pod" hold bullet restates the spec's narrow claim, so it mirrors §10.1.4 rather
than introducing the drift — EVIDENCE: spec/10_gateway-internals.md:56; pkg/adapter/holdstate.go:53-59.

USEFUL [standing context, "`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches
no gateway decision"]: this is the whole of the lens's trust-boundary check for this proposal and it saved the
re-derivation a second time. It still holds: `fence()` branches on `res.Accepted` alone and re-reads the
authoritative Postgres value on rejection. Keep it until the code lane lands.

USEFUL [standing context, refuted classes (b), (e), (f), (j)]: these four are exactly the security lens's
attack surface on this proposal. A future security pass that reads them first has essentially no work left on
the staged spec text unless SPEC-1's step-3 wording changes.

FACT: the spec-changes staging is byte-identical between the round-2 and round-3 snapshots
(`diff -rq scratchpad/cp-snap/0076-run4/spec-r2 .../spec-r3` reports only the review log differing), so a
"read the changed sections first" instruction has no changed sections to read this round.


### [non-spec.1.fix-G1.1]

DECISION: Migration 0181 keeps `DEFAULT 1` on both columns and the backfill and no longer touches the session row's CHECK — BECAUSE the migrate Job is a Helm `pre-install,pre-upgrade` hook at weight -5 that completes before the gateway Deployment rolls (charts/lenny/templates/migrate-job.yaml:10-16, :37-39), so a `>= 1` check would be live while the old fleet still inserts an explicit zero through pgstore.Create (pkg/gateway/session/sessionstore/pgstore/pgstore.go:177, :260), and spec/10 §10.5 puts such a constraint in a Phase 3 migration in a subsequent deployment — ALTERNATIVES: a §10.5 phase split (rejected: the Phase 3 file must not be applied in this release, so it would sit staged with no checklist step and drag in a second prodMigrationSchema row, §9 entry, and tier-2 file); an exemption clause keeping the tightening (rejected: contradicts §10.5 and the chart template's own header); deleting 0181 (rejected: the backfill is load-bearing for rows already at 0).
FACT: nothing in the proposal or the tree reads the `>= 1` check. The invariant that 0 is impossible as a row value rests on the two `Create` floors and both stores' `Update` clamps, so removing the check costs nothing — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:460, :475-477; memstore.go:129, :144-146
WATCHOUT: scripts/lint-migrations.sh pass 4 keys on `add column` only, so it does not catch a constraint tightening; the deploy-axis check is manual — EVIDENCE: scripts/lint-migrations.sh:93-100
WATCHOUT: `TestFenceZeroGenerationFencesAtBaseline` seeds through `genReader{gens: []int64{0}}` rather than through a `CoordinationGeneration:` constant, so §8's class-2 sweep over seeded constants structurally could not find it. Any future sweep for baseline fallout must grep the fake readers too — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence_test.go:173-183
MISTAKE: an earlier pass rejected splitting the migration from the floors on in-commit grounds alone (spec-changes.md:1501, a dated Pass 15 record), which never reached the deploy axis; the check tightening survived three rounds as a result.
DEFERRED [spec-changes.md]: the Pass 15 residue paragraph at ~:1501 and the Pass 4 line at ~:1110 describe 0181 as carrying the check tightening and the tier-2 migration case as asserting it. Both sit inside dated `### Pass` records under `## Resolved in adversarial review`, so the design ruled them not-a-site and this loop may not edit that file; what is true now is that 0181 carries the `DEFAULT 1` on both columns and the backfill only, and its tier-2 case asserts the backfill, both defaults, the retained `>= 0` check, and a `.down.sql` that restores `DEFAULT 0`.
FACT: non-spec-changes.md carried no `## Resolved in adversarial review` section before this round; pass numbering was continued from spec-changes.md, whose last entry is Pass 22 — EVIDENCE: spec-changes.md:1815


### [non-spec.1.fix-G2.1]

DECISION: Rewrote §8's tier-4 co-tenant bullet to state seeds (`sess-a` 7, `sess-b` 2), an explicit at-bind fence of `sess-a` to 7, a live `sess-a` lease so the sweeper adopts `sess-b` alone on `ErrHeld`, and a minted 3 — BECAUSE without a prior fence strictly above `sess-b`'s post-handoff value the pre-fix code accepts the fence and the case passes before and after the fix — ALTERNATIVES: moving it to tier 1 (its subject crosses the sweeper, lease store, and pod), leaving the generations to the IMPLEMENTOR'S CHOICE note (they are what makes the case discriminate), fencing `sess-b` first or equal generations (still accepted pre-fix), hedging the pre-fix outcome (§8's preamble requires each case to fail pre-fix).

DECISION: Replaced the tier-7a barrier bullet's "neither waits on the other" with per-ack content assertions plus an explicit statement that the pod-level op lock serialises the two `Checkpoint` streams, and rewrote §8's tier-split framing sentence to "each of two co-tenant sessions' concurrent RPCs records and returns its own state" — BECAUSE an accepted barrier returns only on its stream's deferred `complete()`, and each stream first passes `s.ops.Begin`, which admits one checkpoint at a time — ALTERNATIVES: deleting the bullet (drops the only case pinning the cross-link on the real RPC path), a bounded-skew timing assertion (flaky and still false in premise), changing the op lock (a deliverable outside this proposal).

FACT: Both the pre-fix stale arm and the gap test are guarded on `s.coord.initialized`, so the FIRST fence on a pod is always accepted whatever its generation — EVIDENCE: pkg/adapter/coordination.go:99, :108; tests/testinfra/coordfixture/coordfixture.go:73-75.

FACT: `Sweeper.Sweep` skips a session whose lease another live replica holds, on `leasestore.ErrHeld` — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:341.

WATCHOUT: The pod-level op lock means no tier-7a case on this path may assert anything about two co-tenant checkpoints' relative completion times. It coalesces on the same session id and queues a distinct one — EVIDENCE: pkg/adapter/checkpoint.go:111, :122-125; pkg/adapter/oplock.go:117-128, :133-140.

MISTAKE: The review log's Settled section already derived that "neither waits on the other" is literally false (review-log.md around :410), and three prior rounds left the staged bullet uncorrected. A derived correction that is not applied to the staged text costs a later round the whole derivation again.

DEFERRED [spec-changes.md, the Pass 21 record around :1812]: it restates the tier-7a classification as "two co-tenant sessions' RPCs do not interfere" and calls both tier-7a bullets that kind. That classification is now wrong for the barrier bullet, whose acks do serialise. The live statement is non-spec-changes.md's tier-split sentence, which this pass corrected; the Pass record is a frozen historical entry and was deliberately not edited.


### [non-spec.1.fix-design-G1.1]

DECISION: close the migration/deploy-ordering finding by DELETING the CHECK tightening from migration 0181 rather than phase-splitting it. 0181 keeps `DEFAULT 1` on both columns and the `UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0` backfill, and leaves 0050's `CHECK (coordination_generation >= 0)` in place; the two `Create` floors become the whole enforcement — BECAUSE the check was only ever the "store-side backstop" (non-spec-changes.md:143-145) and it is the single clause that engages §10.5: a constraint old-version replicas' writes violate may only be added in Phase 3, and the migrate Job is a `pre-install,pre-upgrade` hook at weight -5 that applies the schema while 100% of the old fleet still serves (charts/lenny/templates/migrate-job.yaml:10-16, :37-39). With `>= 0` retained, an old-binary insert of 0 during the rolling window is accepted and degrades to exactly the behaviour CODE-4's own "when the baseline does not fire" paragraph already describes (fence carries 0, adapter refuses with `InvalidArgument`, loud and fail-closed), so the design needs no new sentence to cover the residual — ALTERNATIVES: (a) §10.5 phase split staging a second Phase-3 migration here — rejected: §10.5 requires Phase 3 to be a separate deployment in a subsequent release, so a proposal that stages both files stages one that must not be applied, and it drags in a preflight `DO` block, a second `prodMigrationSchema` row, a second tier-2 file, a §9 entry, and a checklist step for a backstop nothing reads; (b) a stated exemption ("pre-deployment, no fleet in the wild") — rejected as hair: it is an exception clause answering a finding, it contradicts §10.5's normative text and the chart template's own header, and it leaves the tightening in place for the first real upgrade.

FACT: migration 0181's substantive content is the backfill alone. `pgstore.Create` names `coordination_generation` in its insert column list, so the column DEFAULT baselines nothing, and the `coordination_lease` DEFAULT is cosmetic because `upsertMirror` always binds explicitly — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:177, :260; review log Standing context "Migration 0181's environment is settled".

WATCHOUT: under this fix 0181 still owes its `prodMigrationSchema` row and its behaviour file under `tests/tier2_component/migrations/` — `scripts/lint-migrations.sh` pass 3 greps that directory for the bare number and does not care what the migration does. Do not "simplify" the migration out of the registry — EVIDENCE: scripts/lint-migrations.sh:73-88; tests/tier2_component/migrations/prod_columns_test.go:588-603.

FACT: spec-changes.md:1110 and :1501 sit inside dated `### Pass 4` and `### Pass 15` records under `## Resolved in adversarial review`. They are historical pass narrative, not live staged claims, so a non-spec fix that falsifies their wording is not an edit site there. This is the same precedent the review log already records for review-log.md pass prose — EVIDENCE: spec-changes.md:587, :1088, :1438, :1488.

FACT: `TestFenceZeroGenerationFencesAtBaseline` (pkg/gateway/coordination/coordfence/coordfence_test.go:173-183) drives `f.fence` over `genReader{gens: []int64{0}}` and asserts the wire value is 1, which is exactly the `if gen <= 0 { gen = 1 }` block CODE-4 deletes (coordfence.go:147-153). It is in neither of §8's two baseline-shift classes, because it seeds no session row at all. The staged "new" tier-1 coordfence case (non-spec-changes.md:278-279) is that landed case amended, not a new one.

DEFERRED [review-log.md `### Deferred`, from `[spec.3.review-performance.3]`]: that entry says 0181 "needs the §10.5 phase split, or an explicit statement of why it is exempt". A third remedy exists and is the one chosen: delete the tightening, because the check is a backstop the `Create` floors already provide and nothing in the proposal reads it. The entry can be closed against this design rather than against a phase split.

OPEN: whether a later release should add the `>= 1` check as a genuine Phase-3 migration once every replica runs the floored binary. It is defensible defence-in-depth and costs a separate proposal; nothing in 0076 depends on it.


### [non-spec.1.fix-design-G2.1]

DECISION: The §8 tier-4 co-tenant bullet gets an explicit generation arrangement — `sess-a`'s row seeded at 7 with replica 1's at-bind fence driven explicitly to 7, `sess-b`'s row at 2, takeover mints 3 — BECAUSE nothing fences a normally-started session, so without a stated prior fence the pod's pre-fix `initialized` is false when `sess-b`'s fence arrives and the pre-fix code ACCEPTS it, making the case pass before and after the fix. ALTERNATIVES: (a) moving the case to tier 1 — rejected, the case's subject is the sweeper/lease/pod crossing, which tier 1 cannot observe; (b) leaving the ordering to the implementor — rejected, that is exactly how the current bullet became unreproducible; (c) fencing `sess-b` first and then `sess-a` — rejected, that inverts the defect (a fence at 3 after a pod-wide 7 is what the pre-fix code refuses).
FACT: `coordfixture.StartPod` returns a pod that is NOT yet fenced, by its own doc comment, and the landed tier-4 case therefore drives replica-1's at-bind fence by hand before any takeover. EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:73-75; tests/tier4_integration/coordination_fence_split_brain_test.go:83
FACT: the pre-fix stale arm AND the gap test are both guarded on `s.coord.initialized`, so the first fence on a pod is exempt whatever its value. EVIDENCE: pkg/adapter/coordination.go:99, :108
DECISION: The tier-7a two-barrier bullet drops "neither waits on the other" and states positively that the pod-level op lock serialises the two `Checkpoint` streams, so the case asserts only ack contents (own stream's checkpoint id, no empty and no cross-linked `checkpoint_ref`) and completion inside the ack window — BECAUSE barrier B's ack cannot return before stream A's whole archive finishes, so an independence assertion fails or flakes against the FIXED code. ALTERNATIVES: (a) deleting the bullet, since the tier-1 gate-independence case already pins the gate move — rejected, the pod-wide gate's cross-link is a race and only a `-race` contention case discriminates it; (b) asserting a bounded skew between the two acks — rejected, it is a timing assertion on a serialised path and would flake.
DECISION: the §8 framing sentence at non-spec-changes.md:178 ("two co-tenant sessions' RPCs do not interfere") is IN SCOPE for the tier-7a fix and is rewritten to "each of two co-tenant sessions' concurrent RPCs records and returns its own state" — BECAUSE the corrected bullet asserts the barriers DO interfere in completion time. The Pass-21 restatement at spec-changes.md:1812 is NOT a site: it sits inside a frozen `## Resolved in adversarial review` pass record, which keeps the words it was written with.
WATCHOUT: do not "fix" the tier-4 bullet by weakening the pre-fix outcome to "the pod may refuse the fence". §8's preamble requires each case to fail against the pre-fix code; a hedge there makes the headline case non-discriminating instead of unreproducible. EVIDENCE: non-spec-changes.md:189-190
WATCHOUT: the survivor's `Sweeper` only adopts `sess-b` alone if replica 1's lease on `sess-a` is still live (it skips on `ErrHeld`); the bullet must say so or the flow does not hold. EVIDENCE: tests/tier4_integration/coordination_fence_split_brain_test.go:100-106
FACT: CODE-1 already gives `coordfixture.Pod`'s `Fence`, `LastFenced`, and `StaleRPCRejected` the session key, so the tier-4 bullet may write a per-session at-bind fence without adding a new fixture deliverable. EVIDENCE: non-spec-changes.md:84-86
USEFUL [review-log Settled, "op lock as the true serialiser"] and [Traps, "a per-entry gate never has to handle a missing entry is false"]: both had already derived the op-lock serialisation, which saved re-deriving finding 3's ground truth from scratch.


# Post-fix review, round 1 (non-spec lane), 0076

Verdict: no findings. The four confirmed findings landed, the three in-scope
cascade sites were all edited, and every citation in newly written text resolves.

LANDED
- Migration 0181: the `>= 1` tightening is gone from every live site
  (non-spec-changes.md:119-125, :135-143, :150-153, :305-310, :375; summary.md:42-48).
  No live text in the proposal still asserts a tightened check; the only remaining
  `>= 1` mentions are the dated Pass 9/16/17 records in spec-changes.md
  (:1055-1110, :1494-1503, :1582) and the new Pass-23 bullet that describes the
  correction. Verified 0050:38-39 carries the inline `CHECK (coordination_generation >= 0)`
  and 0164:44 the mirror default; charts/lenny/templates/migrate-job.yaml:10-16 and :37-39
  carry the pre-install/pre-upgrade hook at weight -5 and the "before the gateway
  Deployment" statement; spec/10:420, :429-434 back the Phase-3 placement claim.
- Coordfence: §8 now amends the landed TestFenceZeroGenerationFencesAtBaseline
  (non-spec-changes.md:298-304) rather than staging a new case, the two-class
  sentence names it as the third site (:328-329), and summary.md:54-56 carries it.
  coordfence_test.go:173-183 and coordfence.go:147-153 read as cited.
- Tier-4 co-tenant bullet (non-spec-changes.md:240-256): the missing precondition
  is stated (sess-a fenced at 7, sess-b at 2, takeover mints 3, ErrHeld skip).
  Verified coordination.go:341 (ErrHeld continue), coordfixture.go:73-75, :76,
  :98-102, :220-241, split_brain_test.go:83, adapter/coordination.go:99, :108.
- Tier-7a barrier bullet (:258-267): "neither waits on the other" is gone; the op
  lock serialisation is stated with checkpoint.go:111, :122-125 and oplock.go:117-128
  verified.

DRIFT
- All three handed sites edited: non-spec-changes.md:279 -> :305-310 (tier-2 migration
  case now asserts the retained `>= 0` check and DEFAULT-0 down); summary.md:47 -> :54-56
  (third amended test added); non-spec-changes.md:178 -> :186 ("each of two co-tenant
  sessions' concurrent RPCs records and returns its own state").
- Open sweep found no stale parallel: §8's tier-1 bullets still use 7/2 consistently with
  the rewritten tier-4 bullet; §9's file list, the checklist S1-S6, the deliverable index,
  problem-statement, status, and deviations carry no claim the edits falsify;
  non-spec-changes.md:175 "independence of the barrier gates" is object-level and survives;
  the design paragraph at :40-52 (gateway dispatches concurrently) does not contradict the
  pod-side op-lock serialisation the new bullet states.

CITATIONS
- Every citation in newly written text opened and confirmed: migrate-job.yaml:10-16/:37-39,
  pgstore.go:177/:260, coordfence.go:147-153 and :143, coordfence_test.go:173-183/:177,
  adapter/coordination.go:93-94/:99/:108/:158-165, checkpoint.go:111/:122-125,
  oplock.go:117-128/:133-140, coordination/coordination.go:341/:408/:512-517,
  coordfixture.go:73-75/:76/:98-102/:220-241, split_brain_test.go:83,
  migrations/0050:38-39, migrations/0164:44, spec/10 §10.5 (:420, :429-434).


### [non-spec.1.review-applicability.1]

DECISION: filed two findings — (1) migration 0181's `>= 1` CHECK against the §10.5 rolling-deploy window, (2) the tier-2 resume-fence-then-takeover case with no file, package, or harness and no §9 entry — BECAUSE both are unmarked gaps a competent implementor would have to invent past, and (1) is the standing `DEFERRED [non-spec-changes.md, CODE-4, from [spec.3.review-performance.3]]` that the spec loop could not land and this loop owns — ALTERNATIVES: rejected §8's tier-7a "neither waits on the other" phrase (standing context already adjudicates it as literally false but with the carrying assertions intact, so a verifier would kill it as imprecision), the `.down.sql` naming one default where the `.up.sql` changes two (no gate reads it, `prodMigrationSchema`'s 0181 row is inert, and a down migration reverting what its up did is ordinary judgment), the `IMPLEMENTOR TO FILL THE BLANKS` header at non-spec-changes.md:6-7 (same shape as the refuted spec-changes header), and the tier-3 wire case's missing §9 file (explicitly pre-refuted in `### Traps`).
FACT: `pgstore.Create` binds `sess.CoordinationGeneration` with no floor (pgstore.go:260) and NO production caller sets the field — grep over `pkg/gateway/session/` and `pkg/gateway/sessionserver/` returns only `Update` clamps, the meta store, and the snapshot-close bump. So an old-binary insert during the rolling window writes a literal 0 and 0181's `>= 1` CHECK rejects it. EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:260; charts/lenny/templates/migrate-job.yaml:37-39.
FACT: `scripts/lint-migrations.sh` pass 4 is the tree's mechanized §10.5 expand-contract gate, and its own comment names the exact hazard class ("old-version replicas ... issue INSERT statements that omit it"). It keys on `add column` alone, so it does NOT catch 0181's constraint tightening. EVIDENCE: scripts/lint-migrations.sh:93-100.
FACT: `TestClaimRegisterAgreesWithTheAdapterProto` reads proto FIELD DECLARATIONS only (`protoFields`, `fenceReadersExempt`), never comment text, so SCHEMA-1's comment-only edits cannot turn it red and the §28.4 claim-map DEFERRED is not a gate failure. EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:92-102.
FACT: checklist bookkeeping is clean this round — every staged deliverable appears in exactly one step (S1 SPEC-1/2/3, S2 SCHEMA-1, S3 CODE-1+CODE-3, S4 CODE-4, S5 CODE-2, S6 TEST-1), every step carries one lane, spec leads, no Depends-on names a later or absent step, and no box is checked. The new `## Deliverable index` in the summary matches the checklist exactly. Do not re-derive.
FACT: every code citation in CODE-1 through CODE-4 and §8 that this pass opened resolves verbatim: coordination.go `:44`, `:52`, `:63`, `:89`, `:94`, `:99`, `:108-116`, `:148`, `:158-166`, `:180-188`, `:216`, `:225`, `:236`, `:247`, `:264`, `:269`; slot.go:21 `slotState`, `:153` `checkpointRootsForSession`; server.go coord/hold/barrier fields; checkpoint.go:94, :111, :122; migrations/0050:38-39, 0164:44; 0180 is the last taken number.
FACT: the newly added SCHEMA-1 carve-out is accurate — `CoordinatorFenceRequest.coordination_generation`'s comment already reads "Strictly monotonic on the pod side per session." EVIDENCE: schemas/lenny-adapter.proto:1448-1450. The DEFERRED `[spec.3.review-citations.1]` is now applied and can be retired.
WATCHOUT: `diff -ru` of this proposal against the run-4 snapshot is 1913 lines and 1870 of them are the review log's compaction pass 17. Only two substantive edits landed: the SCHEMA-1 carve-out sentence and the summary's new `## Deliverable index`. Do not spend the round re-reading the log diff. EVIDENCE: /tmp/d.txt hunk headers at :1, :22, :1893.
OPEN: the standing context says the change-proposal format's `## Deliverable index` requirement postdates the migrated layout and "do not invent one for 0076"; a fixer added one anyway this round. It is internally consistent, so nothing is broken, but the standing-context bullet is now stale and should be corrected or the index justified.


### [non-spec.1.review-citations.1]

DECISION: Returned an empty findings list — BECAUSE every concrete citation in the non-spec staging
(SCHEMA-1, CODE-1 through CODE-4, TEST-1, §8, §9) and in the spec-changes design sections §2 and §4
resolves to text that says what the proposal claims, re-verified from scratch this round.
ALTERNATIVES: filing §8's tier-7a "neither waits on the other" phrase (rejected: the standing-context
entry on the pod-level op lock already adjudicated it and kept the bullet's operative assertions), and
filing 0181's `.down.sql` naming one default where the `.up.sql` changes two (rejected: outside a
citation lens, already carried as an UNVERIFIED, and the `coordination_lease` default is cosmetic).

FACT: The §2/§4 design-prose code citations are NOT covered by the standing "derived inventories"
bullet, which names only CODE-1..CODE-4, §8, and §9. I re-resolved them this round and they hold:
adapterevents.go:80-96 (the already-open refusal), holdstate.go:90-100/:107-112/:172-176/:192,
start.go:4237 (`s.fencer.Fence`), coordination_seams.go:233 (`fencer.Fence`),
coordination/coordination.go:430 (`upsertMirror`), httpsurface.go:592-599 (the zero-seeded fallback),
barrier/wiring.go:49/:51-53/:104-114, prestop.go:390-397/:395/:510, barrier.go:229-246,
proto:153-162/:161-162/:165-179/:1475-1483, coordination.go:92/:223/:224-226/:236-239.
EVIDENCE: pkg/adapter/adapterevents.go:90-96; cmd/lenny-gateway/coordination_seams.go:233;
pkg/gateway/coordination/coordination/coordination.go:430.

FACT: §10.1.8 step 3 is one physical line (spec/10_gateway-internals.md:185) and it does carry, verbatim,
both halves CODE-1 attributes to it: "opens the `Checkpoint` stream for each quiesced session
**concurrently with** the in-flight `CheckpointBarrier` RPC to that session" and the ack "echoing the
gateway-minted `checkpoint_id` it received in `Start`". Step 1's stale-rejection sentence that SPEC-1
re-qualifies is on spec/10_gateway-internals.md:183, a different physical line.
EVIDENCE: spec/10_gateway-internals.md:183, :185.

FACT: The twelve operational-RPC carriers SCHEMA-1 lists are exactly the messages declaring
`coordination_generation` minus `CoordinatorFenceRequest` and `CheckpointBarrierRequest`; a grep for
`^message |coordination_generation = ` over the proto returns fourteen messages, so the enumeration is
complete and has no extras. EVIDENCE: schemas/lenny-adapter.proto:957, :982, :1028, :1056, :1080, :1110,
:1166, :1297, :1326, :1447, :1475, :1526, :1564, :1597.

USEFUL [standing context, "Known sub-line citation drifts that must not be filed"]: saved a verification
on three sites I independently re-derived as off by one to seven lines with the meaning intact —
`coordination_test.go:184-197` (test declared :189, doc comment opens :185), `slotsession.go:283-285`
versus `:282-285` for one struct declared at :282, and `coordination.go:408` cited for the
`recordAdoptionBackoff` call at :415 inside the same comment block.

FACT: `memstore_test.go`'s `TestCreateDefaultsSessionRecordFields` is declared at :311 with its doc
comment opening at :308; the proposal cites `:309-325`. Same immaterial-drift class as the three above.
EVIDENCE: pkg/gateway/session/sessionstore/memstore/memstore_test.go:308, :311, :323-325.

FACT: `sweep_test.go`'s generation assertions do run to :594 — a `head -30` on the grep truncates at
:578 and makes the proposal's ":275 to :594" look wrong. The last pair is at :593-594.
EVIDENCE: tests/tier2_component/coordination/sweep_test.go:593-594.

WATCHOUT: `releaseSessionSlot` is a third path that calls `deregisterSlotLocked`
(pkg/adapter/slotsession.go:214-215, reached from resume.go, session.go, and sdkwarm.go), so CODE-1's
"both deregistration paths" is not the full caller set of the deregister helper. It is still right for
the mid-flight case, because every `releaseSessionSlot` call site is a failed-start or failed-resume
compensation for a session that was never started, so no barrier or `Checkpoint` stream can be in
flight for it. Do not file this; check the call sites before re-deriving it.
EVIDENCE: pkg/adapter/slotsession.go:214-215; pkg/adapter/session.go:133, :147, :157;
pkg/adapter/sdkwarm.go:236, :241, :251, :298.


### [non-spec.1.review-client-surface.1]

Returned zero findings. The client-facing surface this proposal touches is one file
(`schemas/lenny-adapter.proto` doc comments) plus its two generated Go stubs, and after
re-deriving it end to end I could not make a defect stick.

FACT: SCHEMA-1's carrier enumeration is exhaustive against the proto, verified mechanically.
`grep -n "int64 coordination_generation" schemas/lenny-adapter.proto` returns exactly 14 fields
(:974, :1002, :1051, :1075, :1096, :1119, :1179, :1310, :1398, :1452, :1480, :1536, :1581, :1623).
Minus `CoordinatorFenceRequest` (:1452) and `CheckpointBarrierRequest` (:1480) that is the twelve
operational-RPC field comments SPEC-2 lists at spec-changes.md:526-531, and every line range SPEC-2
gives for them resolves verbatim — EVIDENCE: schemas/lenny-adapter.proto:969-973 (SendMessageRequest),
:995-1001 (AttachRequest, whose trailing every-frame sentence SPEC-2 correctly exempts), :1172-1178
(CheckpointRequest, same), :1618-1622 (ShutdownRequest's "cannot tear the session down" variant).
Do not re-derive this.

FACT: the round-4 SCHEMA-1 edit is correct. The new sentence at non-spec-changes.md:19-21 says the
`CoordinatorFenceRequest.coordination_generation` field comment "already states per-session
monotonicity" and takes no edit. It does: `// protocol. Strictly monotonic on the pod side per
session.` — EVIDENCE: schemas/lenny-adapter.proto:1451; matched by SPEC-2 at spec-changes.md:496-498.
This closes the `DEFERRED [non-spec-changes.md, from [spec.3.review-citations.1]]` item in the review
log's `### Deferred` section; that item can be retired.

FACT: SCHEMA-1's three stub citations all resolve. `schemas/lenny-adapter.proto:1451` reappears
verbatim at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`; the `CoordinatorFence` RPC comment
reappears at `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:180` (client interface) and `:632`
(server interface).

FACT: no gate anywhere pins the comment text SCHEMA-1 rewrites. The tier-0 comment gates
(`checkpoint_scoping_key_comment_test.go`, `slot_absence_claim_comment_test.go`,
`checkpoint_dropped_slot_column_comment_test.go`, `adapter_proto_message_scope_test.go`,
`adapter_proto_intrapod_pointer_test.go`) contain no `coordination_generation`, `CoordinatorFence`,
or `CheckpointBarrier` reference at all, and a tree-wide grep for the distinctive phrases
("first call on a pod", "last fenced generation", "cannot drive the pod", "cannot tear the session
down") outside `pkg/proto/` returns only `pkg/gateway/runtime/adapterclient/coordinatorfence.go:24`,
`pkg/adapter/coordination.go:230`, and `pkg/adapter/coordination_test.go:74` — all under refuted
class (k).

FACT: the two landed tier-3 adapter suites cannot break under D7/CODE-2, confirming the standing
context. `tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` and
`tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` assert descriptor
presence, wire-form byte behaviour, and round-tripping only; neither constructs an `adapter.Server`
or asserts an accept/reject outcome — EVIDENCE: the only test funcs are
`TestBoundSessionRequestsCarryGenerationFence` (:104), `TestUnsetGenerationFenceAddsNoBytes` (:141),
`TestGenerationFenceRoundTrips` (:193), `TestCheckpointRequestFenceCoexistsWithEveryOneofArm` (:228),
and `TestCheckpointBarrierResponseWireContract` (:48).

FACT: no parallel client representation carries any contract this proposal changes. Re-derived this
run rather than trusted: `coordination_generation` / `coordinationGeneration` appears nowhere in
`pkg/gateway/openapi/openapi.json` or `docs/api/`; `sdks/` matches none of
`coordination_generation|CoordinatorFence`; `schemas/buf.gen.yaml` emits Go only, so there is no
second-language stub to regenerate; `docs/api/internal.md` (the runtime-adapter-author gRPC page)
contains no "generation", "Fence", or "Barrier" hit; the hold records reach no schema, doc, or chart
(`coordinator_lost|coordinator_connection_lost|last_generation|lastGeneration|
coordinator_generation_gap` over `schemas/ docs/ charts/ sdks/` returns only
`docs/reference/metrics.md:309`'s pod-scoped hold gauge, which D5 leaves pod-scoped, and two proto
lines); and `pkg/alerting/`, `docs/runbooks/`, `tests/tier11_docs/` carry no coordinator-handoff
alert or runbook at all.

WATCHOUT: `docs/reference/adapter-contract.md:69` reads `CoordinatorFence` is a "precondition for any
subsequent operational RPC", which staged §10.1.2 step 3's unset clause makes loose. It is not a
finding and I did not file it: the sentence is already not literally true against the shipped pod-wide
`initialized` guard (before any fence on a pod, operational RPCs pass), so it is pre-existing drift
for a docs loop, and the standing context has ruled the eight-site `docs/` surface clean eight times.
A later client or docs lens will land on this line; stop here rather than spending the verification.

DECISION: filed nothing on `tests/claim-map.json` — BECAUSE the review log's third `### Deferred`
item assigns the fence and barrier rows' `ABSENT` restatus to "the non-spec loop", i.e. this one, and
`non-spec-changes.md` §9 lists no `tests/claim-map.json` edit while SPEC-2 asserts the file "is not
opened by this proposal" (spec-changes.md:420-422). ALTERNATIVES: filing it as an unstaged site, which
I rejected because criterion (d) enumerates `spec/`, `docs/`, `schemas/`, `charts/` and reaches no path
under `tests/`, and the surface is a test registry rather than a client contract, so it is outside this
lens. It belongs to an edit-sites or test-coverage lens; it is still open and nobody has staged it.

USEFUL [standing context, "The gap reset and the record-and-reject rule have four spec mirrors and
seven proto carriers"]: its "there is no eighth carrier" exclusion list saved re-deriving whether
`CoordinatorFenceResponse.last_fenced_generation`, the `Checkpoint` RPC comment, and
`CheckpointStart.checkpoint_id` are carriers. I did independently confirm `CheckpointBarrierResponse`
(`schemas/lenny-adapter.proto:1493-1502`) is session-neutral and states no gate, so it is a ninth
non-carrier the bullet does not name and does not need to.


### [non-spec.1.review-docs-alignment.4]

DECISION: returned an empty findings list for the docs-alignment lens over the non-spec staging (SCHEMA-1, CODE-1 through CODE-4, TEST-1, §8, §9) — BECAUSE no `docs/` page states the pod-side unit of the fenced generation, the barrier's generation gate, the counter's initial value, the hold post-mortem's fields, or the prestop capture population, so nothing the non-spec lane changes has a describing docs surface to mirror; and every accepted or deferred failure mode I could name either lands in staged spec text or has no docs page that enumerates its failure's causes — ALTERNATIVES: filing the missing "Edge cases and accepted failure modes" section (rejected: twice adjudicated as format hygiene on this proposal); filing D7's removal of the prestop fallback capture for the never-handed-off population as a new cause of a data-loss path (rejected: no operator doc enumerates causes of a session missing its drain capture — `docs/runbooks/minio-failure.md:106` is the only such narrative and it is MinIO-specific — and the standing context settles the acked-but-uncaptured gap as pre-existing); filing CODE-4's "Both refusals are loud and fail closed" (rejected: not this lens, and the standing context already records the fence half's non-clean `InvalidArgument` as a settled fact).

FACT: the identifier sweep for this non-spec lane is two files. `grep -rn "last_fenced_generation|coordinator_lost|lastGeneration|last_generation|gap_detected|coordinator_generation_gap|coordination_generation|coordinationGeneration" docs/ charts/ sdks/ schemas/ -l` returns `docs/getting-started/concepts.md` and `schemas/lenny-adapter.proto` alone; `concepts.md:101` states only that the counter tracks replica handoffs and is independent of `recovery_generation`, with no unit, baseline, or gate. Re-derived this run for the ninth time; do not re-derive.

FACT: the coordinator-loss hold's docs surface is `docs/reference/adapter-contract.md:97` (`AdapterTerminating`, "e.g., coordinator-loss hold timeout") and `docs/reference/metrics.md:309` (`lenny_adapter_coordinator_hold`, "1 while adapter is in hold state"). Neither names a generation or the post-mortem record, so CODE-3's per-session `coordinator_lost` value and the dropped pod-level `last_generation` key reach no docs page. `grep -rni "post-mortem|coordinator_connection_lost"` over `docs/` returns only an unrelated backup line.

FACT: `docs/runbooks/coordinator-handoff-slow.md` is the only coordinator runbook and it triggers on `lenny_coordinator_handoff_duration_seconds` p95 with phases `claim|materialize|warmup|attach`. It never mentions `lenny_coordinator_handoff_stale_total`, so the proposal's removal of that counter's false increments owes it no edit — EVIDENCE: docs/runbooks/coordinator-handoff-slow.md:32, :41-44.

FACT: no docs page enumerates the causes of a session missing its drain checkpoint. `docs/operator-guide/upgrades.md:47-54` describes the barrier fan-out and names only the two ack metrics; `docs/runbooks/minio-failure.md:106` is the one narrative of a lost drain capture and its cause is a MinIO outage. So D7's prestop-skip consequence has no "enumerates that failure's causes" page to gain a cause.

FACT: `docs/operator-guide/upgrades.md:238-266` carries the 3-phase expand-contract migration narrative and `lenny-ctl migrate status`, but it lists no migration by number and states no per-migration constraint, so migration 0181 owes it no edit whatever the loop decides about the standing `DEFERRED` on 0181's deploy-time phase split.

USEFUL [standing context, "The `docs/` surface is eight sites and states no unit, baseline, or gate"]: held exactly on independent re-derivation from the identifier grep and from a second sweep over `fence|fenced|generation` across `docs/`, and it is what let this pass verify rather than re-walk the tree.

WATCHOUT: in the non-spec loop this lens has almost no filable surface either. Everything the lane changes is adapter-internal state, a proto doc comment, a migration, and test fixtures, and `docs/` describes none of them. Budget the pass at a verification of the two-file identifier sweep plus the two category-2 questions (a new operator-facing failure mode, a new cause of an existing one) and stop; the four prior docs passes on this proposal all returned empty on the same ground.


### [non-spec.1.review-edit-sites.1]

DECISION: Filed exactly one finding, the §10.5 deploy-ordering defect in CODE-4's migration 0181 — BECAUSE it is the one unclosed `DEFERRED` whose remedy is entirely inside the non-spec staging this loop owns, and it is verifiable end to end in the tree — ALTERNATIVES: rejected the `tests/claim-map.json` DEFERRED (below), the `.down.sql` asymmetry (cosmetic), and every tier-list/inventory shape the Traps section has already killed.

FACT: the §10.5 deploy-ordering finding verifies completely and independently of the review log. `pgstore.Create` is the tree's only `INSERT INTO sessions` (`grep -rn "INSERT INTO sessions" --include=*.go --include=*.sql` returns one non-test hit), it names `coordination_generation` in the column list and binds `sess.CoordinationGeneration` with no floor, and no production caller sets that field (`grep -rn "CoordinationGeneration:" pkg/gateway/sessionserver/ cmd/` returns only the barrier cache fallback) — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:170, :177, :260; cmd/lenny-gateway/httpsurface.go:599

DECISION: did NOT file the `tests/claim-map.json` DEFERRED, even though it is addressed to "the non-spec loop" — BECAUSE the remedy it proposes is itself wrong on two counts. (1) `ABSENT` is defined as "specified and not implemented" (spec/28_communication-channels.md:163-165) and both `CoordinatorFence` and the barrier rows are genuinely `WIRED` — the mechanism is reachable from production code before and after this change; what the S1→S5 window creates is a partially-rescoped mechanism, which §28.4's three-value status set does not model. (2) The register is generated: `scripts/seed-claim-register.py` parses root `gateway-runtime-comms.md` §7.1 and `TestClaimRegisterIsReproducibleFromItsGenerator` diffs bytes, so a row cannot be restatused by editing `tests/claim-map.json` at all; the edit would have to land in the root planning document. Anyone reopening this must argue past both — EVIDENCE: spec/28_communication-channels.md:161-169; tests/claim-map.json:4-11 (an ABSENT row's shape), :460-464 (the `CoordinatorFence` row, WIRED)

FACT: SCHEMA-1's carrier list now matches SPEC-2's enumeration item for item, including the exclusion of `CoordinatorFenceRequest.coordination_generation`. The spec-round DEFERRED that flagged the mismatch is discharged; do not re-derive the list — EVIDENCE: non-spec-changes.md:11-21 against spec-changes.md:487-535

FACT: the docs/charts/schemas sweep for the counter baseline is genuinely empty and cheap to re-run: `grep -rn "coordination_generation\|coordination generation" docs/ charts/ schemas/` returns four docs sites (concepts.md:101, architecture.md:173, adapter-contract.md:69, metrics.md:307), all unit- and baseline-neutral, and no chart or JSON-schema site at all. No doc, chart, or spec file enumerates migration numbers (`grep -rn "0180\|migrations/01" docs/ charts/ spec/` is empty), so migration 0181 creates no doc edit site.


### [non-spec.1.review-feasibility.1]

DECISION: filed exactly one finding, the migration-0181 deploy-window constraint violation — BECAUSE it is the one place the staged work assigns an actor (the pre-upgrade migration Job) a change the co-running actor (the old gateway fleet's `pgstore.Create`) cannot satisfy, and §10.5 states the rule it breaks in so many words — ALTERNATIVES: rejected the §28.4 claim-register DEFERRED (rows for `CoordinatorFence` and `CheckpointBarrier` already exist and are `WIRED`, so §28.4's "carries a row" obligation is already met; the DEFERRED's real subject is row *status*, and the only remedy edits the root `gateway-runtime-comms.md` §7.1 that `scripts/seed-claim-register.py` parses, since `TestClaimRegisterIsReproducibleFromItsGenerator` byte-diffs a regeneration — a two-file remedy in a document the proposal does not open). Also rejected the 0181 `.down.sql` default asymmetry (the `coordination_lease` default is cosmetic; the mirror upsert always binds the value).

FACT: no production code sets `sessionstore.Session.CoordinationGeneration` before `Create` anywhere under `pkg/gateway/sessionserver/`; the only writers are `bumpCoordinationGenerationOnSnapshotClose` and the sweep. So every session created today inserts a literal 0 through `pgstore.Create`'s explicit column bind — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:177; grep of `CoordinationGeneration` under pkg/gateway/sessionserver/ returns only bump/derive sites.

FACT: `s.slots` is `map[string]*slotState` (pkg/adapter/server.go:379), so CODE-1 moving `coordinationState` (which embeds `sync.Mutex`) onto the entry raises no `go vet` copylocks problem. Checked so nobody re-checks it.

FACT: every coordfixture and adapter line citation in CODE-1, CODE-3, and §8 re-resolved this round: coordfixture.go:76/109/115/122/220-241; coordination.go:44/52/63/89/93-94/99/148/158-166/180-188/216/224-226/232-236/245-269; checkpoint.go:94/111/122/124; slot.go:21/153; slotsession.go:267/282-285/347-361; session.go:237-239/271; holdstate.go:43/119/128/187/206/225/283; oplock.go:119-129; cmd_run.go:498-508/635-641/880; lint-migrations.sh:45/74-88; uploaddriver_test.go:992/993-995/1007/1015; checkpointer_test.go:89-96; uploaddriver.go:422. The `server.go` field offsets are one low (coord is :301 not :302, barrier :313 not :314) — that is the known sub-line drift class, do not file it.

WATCHOUT: the whole-proposal grep for `expand|rolling|10.5|pre-upgrade|phase` returns nothing in the staged files outside two unrelated `spec/10` quotations. The deploy-time axis is genuinely unaddressed in the staging, not addressed somewhere I missed.

UNVERIFIED: whether the tree already has a worked precedent for a two-release constraint tightening. `pkg/schemamigrate` carries `schema_migration_phase`/`phase1_applied`/`phase3_applied`, which is the machinery §10.5 Phase 3 needs, but I did not check whether any landed migration uses it. Whoever writes the fix should look before inventing a convention.


### [non-spec.1.review-kubernetes.1]

DECISION: filed exactly one finding, CODE-4's migration 0181 tightening `CHECK (coordination_generation >= 1)` inside a Helm `pre-install,pre-upgrade` hook while old gateway replicas still insert an explicit literal 0 — BECAUSE it is a genuine mixed-version rolling-deploy break barred by spec/10 §10.5, its remedy lands entirely in the non-spec staging, and the review log's own `### Deferred` entry says nobody has decided it and that the non-spec loop owns it — ALTERNATIVES: rejected filing the `.down.sql` one-default-vs-two-defaults asymmetry (cosmetic: the mirror upsert always binds explicitly), the backfill/ACCESS EXCLUSIVE lock cost (a round already declined it), and the newly-added `## Deliverable index` in the summary (the standing context says the format requirement postdates the layout, and the table's contents agree with §9).

FACT: the Kubernetes-idiom lens has almost no surface on this proposal. Nothing here writes a CRD status, runs a finalizer, touches an admission webhook, or puts a controller on a synchronous path; the whole change is adapter in-process state plus a Postgres migration. The only idiom that bites is rolling-deploy / mixed-version coexistence around the Helm migrate hook — EVIDENCE: charts/lenny/templates/migrate-job.yaml:37-39; spec/10_gateway-internals.md:420, :432

FACT: no production caller sets `Session.CoordinationGeneration` before `Create`; `pkg/gateway/sessionserver/` references it only in `_test.go` files, so every real insert binds a literal 0 — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:177, :260

USEFUL [standing context, "Deploy-time ordering is a separate axis from commit grouping"]: it carried the three facts (hook weight -5, gateway Deployment applied after pre-hooks, `pgstore.Create` binds with no floor) that made this finding a fifteen-minute verification instead of a re-derivation.

FACT: the round-4 diff to this proposal is tiny. Only `non-spec-changes.md` SCHEMA-1's carrier list (moving `CoordinatorFenceRequest.coordination_generation` out of the edited set and stating it keeps its wording) and the summary's new `## Deliverable index` changed; `spec-changes.md` and the checklist are byte-identical to the snapshot. The SCHEMA-1 correction is right: SPEC-2 does say that comment "keeps its wording" — EVIDENCE: 0076...spec-changes.md:497-498; schemas/lenny-adapter.proto:1449-1451

OPEN: whether the §10.5 phase split for 0181 forces a second release (Phase 1 ships the `Create` floors and the `DEFAULT 1`; a later release adds the `>= 1` CHECK). If it does, the checklist's single S4 step is not enough and the proposal needs to say which half ships now.


### [non-spec.1.review-mechanism.1]

DECISION: filed three findings — the 0181 deploy-ordering DEFERRED, the tier-4 co-tenant case that cannot
reproduce the defect pre-fix, and the landed `TestFenceZeroGenerationFencesAtBaseline` that CODE-4's
floor deletion turns red with no disposition — BECAUSE each is a mechanism that does not work as staged and
each remedy lands in `non-spec-changes.md`, which this loop owns — ALTERNATIVES: two weighed and not filed,
below.

FACT: nothing fences a normally-started session, and the harness says so in its own words:
`coordfixture.StartPod` returns "The pod is not yet fenced; the first CoordinatorFence a coordinator drives
records the pod's initial generation", and the landed tier-4 file models replica-1's at-bind fence with an
explicit `pod.Fence(ctx, 1)` call. So any co-tenant case that wants the pre-fix `coordinator_handoff_stale`
must fence `sess-a` explicitly, at a value at or above `sess-b`'s post-handoff generation; the pod-wide
`initialized` flag is false otherwise and `pkg/adapter/coordination.go:96-99` accepts the first fence at any
positive value — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:72-75;
tests/tier4_integration/coordination_fence_split_brain_test.go:82-84; pkg/adapter/coordination.go:96-99.

FACT: `pgstore.Create` binds `sess.CoordinationGeneration` verbatim (`pgstore.go:260`) and no production
caller of `store.Create` sets that field (`start.go:865`, `create.go:389`, `derive.go:401`, `:641`,
`replay.go:180`), so every old-binary insert writes a literal 0 while migration 0181's `CHECK >= 1` is
already applied by the Helm pre-upgrade hook at weight -5 (`charts/lenny/templates/migrate-job.yaml:38-39`).
§10.5 is explicit that the previous code version stays compatible with the current schema
(`spec/10_gateway-internals.md:420`, `:448`).

WATCHOUT: `TestFenceZeroGenerationFencesAtBaseline` (`pkg/gateway/coordination/coordfence/coordfence_test.go:175`,
assertion `:181`) is in no §8 disposition and in no summary "Watch out for" list, while §9 does list
`coordfence_test.go`. Listing the file is not a disposition: §8 states "the step that lands CODE-4 corrects
both" classes and this break is in neither class.

Weighed and not filed, so nobody spends the round re-deriving them:
- §8's tier-7a barrier bullet "neither waits on the other" is literally false against the pod-level op lock
  (`pkg/adapter/oplock.go:117-128` queues a distinct session id rather than admitting it concurrently), but
  the standing context has already adjudicated the operative contrast as holding and routed the writing style
  of the case to the implementor. Left alone deliberately.
- 0181's `.down.sql` sentence names one default where the `.up.sql` changes two. Inert (the lease column is
  always bound explicitly) and already carried as an UNVERIFIED.

UNVERIFIED: whether the tier-4 co-tenant case, once it fences `sess-a` explicitly, can still be driven
through `coordfixture.FenceReadopter` without `Pod.Fence`'s single-session id — CODE-1 gives those methods a
session key, so it should, but nobody has written the sequence out.


### [non-spec.1.review-operational.1]

DECISION: filed exactly one finding, the migration-0181 rolling-upgrade break, and returned nothing
else — BECAUSE the observability surface this proposal touches is genuinely clean and I re-derived
enough of it to say so rather than to trust the standing context — ALTERNATIVES: filing
`docs/reference/adapter-contract.md:69`'s "precondition for any subsequent operational RPC" against
staged §10.1.2 step 3's unset arm (rejected: it is the §10.1.2 step-2 sender-side duty, refuted class
(a)); filing the staged §29.10 "Shared by the whole pod" bullet's "every inbound RPC ... other than
`CoordinatorFence`" against the wider code allowlist (rejected: it is verbatim shipped §10.1.4 text at
spec/10_gateway-internals.md:57, so it is pre-existing spec-vs-code drift the proposal mirrors rather
than creates).

FACT: the alert surface over this change is empty and it is cheap to confirm. `pkg/alerting/rules/rules.go`
carries exactly one coordinator alert, `CoordinatorHandoffSlow` over
`lenny_coordinator_handoff_duration_seconds` (rules.go:1583), and `grep -n barrier pkg/alerting/rules/rules.go`
and `docs/alerting/rules.yaml` return nothing. So no alert reads
`lenny_coordinator_handoff_stale_total`, `lenny_coordinator_fence_relinquished_total`,
`lenny_adapter_coordinator_hold`, or `lenny_checkpoint_barrier_ack_total`, and no runbook resolution
changes — EVIDENCE: pkg/alerting/rules/rules.go:1583-1587; docs/runbooks/index.md:95.

FACT: no adapter metric carries a generation, so nothing needs a session label after the rescope.
`grep -n 'Name: "lenny' pkg/adapter/metrics.go` returns twelve names and the only coordination one is
the `lenny_adapter_coordinator_hold` gauge at :108, which stays pod-scoped under D5 — EVIDENCE:
pkg/adapter/metrics.go:108.

FACT: the summary's "pair of split-brain metric increments" is exact, not loose. `coordfence.fence`'s
FailedPrecondition arm calls `incStale()` and then, when the re-read shows no advance, `relinquish()`,
which calls `incRelinquished()` before releasing the lease. So one rejected fence with no row advance
increments both counters — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:170,
:178-179, :192-196.

FACT: the gateway fence floor CODE-4 deletes is `if gen <= 0 { gen = 1 }` immediately after the
`CoordinationGeneration` read, and its comment already names 1 as "the first
`coordination_generation` a session row carries", so the baseline SPEC-3 states is what that comment
already assumes — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:144-153.

USEFUL [Standing context, "Derived inventories"]: the eight-site `docs/` list is right and complete. I
re-derived it with `grep -rln coordination_generation schemas/ sdks/ charts/ docs/`, which returns only
`schemas/lenny-adapter.proto` and `docs/getting-started/concepts.md`, plus the two prose sites
`docs/reference/adapter-contract.md:69` and `docs/getting-started/architecture.md:173`. None states a
unit, a baseline, or a gate. The docs lens has now returned nothing three times on this surface; a
future round should spend its budget elsewhere.

WATCHOUT: the `coordinator_lost` post-mortem's per-session values are asserted by a live test that a
reader skimming §8 will mis-locate. `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1`
fences `sess-a` at 7 and then loops over `{sess-a, sess-b}` reading
`coordinator_lost-<id>.json` and asserting `LastGeneration != 7` as an error, so CODE-3 turns one
constant into a two-row table. Its own subject is the `AdapterTerminating` drop delta, not the
generation — EVIDENCE: pkg/adapter/holdstate_test.go:674, :679-684, :700-721.


### [non-spec.1.review-performance.1]

DECISION: filed exactly one finding, the standing `DEFERRED` on migration 0181's deploy-time ordering, and nothing else — BECAUSE it is the one item in my lens that (a) is still absent from the staged CODE-4 text, (b) has a remedy that lands wholly in `non-spec-changes.md`, which this loop may edit, and (c) nobody has decided: the `### Deferred` entry from `[spec.3.review-performance.3]` says in so many words "nobody has decided this one" — ALTERNATIVES: the ACCESS EXCLUSIVE lock and full-table backfill cost of 0181 at tier 3/4 (declined by `[non-spec.5.review-performance.1]`, per `### Settled`); §8's tier-7a "neither waits on the other" phrase (`### Settled` records it as literally false and the round that derived it consciously left it, judging the operative contrast to hold); "Both refusals are loud and fail closed" for the fence half (`### Settled` calls it imprecise, and it is imprecise rather than false — an `InvalidArgument` still ends in relinquish-and-abort, which is fail-closed, just not cheaply).

FACT: the capacity numbers that quantify the 0181 outage are `spec/16_observability.md:592-593` — max concurrent sessions 100 / 1,000 / 10,000 / 100,000 and sustained session-creation rate 5/s, 30/s, 200/s, 2,000/s across tiers 1-4. Every failed create during a rolling window multiplies against that row — EVIDENCE: spec/16_observability.md:592-593
FACT: the migrate Job runs `args: [up]` only, so a Helm rollback never applies 0181's `.down.sql`. The outage therefore does not end by rolling the release back; it ends only when every replica runs the new `Create` floor, or when an operator applies the down migration by hand — EVIDENCE: charts/lenny/templates/migrate-job.yaml:38-39, :62-63
FACT: no production `Create` caller sets `CoordinationGeneration`, so the struct field is zero on every insert and the old binary writes a literal 0 — a grep for `CoordinationGeneration` over `pkg/` and `cmd/` excluding tests returns no assignment on a create path — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:177, :260
FACT: §10.5's operational rule is stated for `NOT NULL` specifically but its ground covers any constraint an old replica's write violates, and the same paragraph states "Mixed-version replicas must coexist during rollout" — EVIDENCE: spec/10_gateway-internals.md:420, :432

WATCHOUT: the chart's own header comment already claims the discipline this migration breaks: "the expand-contract discipline (§10.5) keeps a Phase-1 migration backward-compatible so old replicas coexist with the new schema during the rolling deploy". A reader who takes that comment as a property of the chart rather than as an obligation on each migration file will read 0181 as safe — EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16

USEFUL [the `### Deferred` entry from `[spec.3.review-performance.3]`]: it carried the three underlying facts (hook weight, sole `INSERT INTO sessions`, §10.5) with citations, which is most of this finding's evidence. Re-verified every one rather than trusting it; all four resolved.
USEFUL [`### Settled` "Migration 0181's environment is settled"]: saved re-deriving the constraint name, the 0164 mirror column, and the `TestProdMigrationsRollBackPerStep` walk.

OPEN: whether the remedy is a §10.5 phase split (0181 expand-only now, the `>= 1` check in a later release), dropping the check tightening from 0181 and letting the two `Create` floors be the enforcement, or an explicit exemption argued in CODE-4. The finding names all three; the proposal has to pick one, and the choice changes what the tier-2 migration case asserts (it currently asserts "the tightened `>= 1` check rejects an insert at 0", non-spec-changes.md:280-282).


### [non-spec.1.review-reliability.1]

DECISION: filed exactly one finding, the migration-0181 deploy-window defect the `### Deferred` entry
from `[spec.3.review-performance.3]` derived and could not land — BECAUSE this loop owns
`non-spec-changes.md`, the defect is verified end to end (hook weight, the explicit column bind, the
absent pre-rollback hook), and §10.5 states the rule it breaks — ALTERNATIVES: rejected re-filing the
prestop/D7 fallback loss and the `upsertMirror` stale-window barrier, both of which the standing context
already adjudicated.

FACT: a Helm rollback does not undo 0181. `charts/lenny/templates/migrate-job.yaml:38` annotates the
migrate Job `pre-install,pre-upgrade` only, and `grep -rn "pre-rollback" charts/` returns nothing, so
rolling the chart back to the previous release leaves `CHECK (coordination_generation >= 1)` in place
under binaries whose `pgstore.Create` writes a literal 0. This is the half of the deploy-ordering defect
the `### Deferred` entry did not carry: it is not only a rolling window, it is a one-way door —
EVIDENCE: charts/lenny/templates/migrate-job.yaml:38; pkg/gateway/session/sessionstore/pgstore/pgstore.go:170-177.

FACT: `upsertMirror` (`pkg/gateway/coordination/coordination/coordination.go:430`, declared `:544`) is
the only writer of the barrier-target set and `coordlease` `ListHeldByReplica`
(`pkg/gateway/coordination/coordlease/pgstore/pgstore.go:76-81`) filters on `coordinator_replica`, so at
any instant one replica dispatches barriers for a session. That closes the "two concurrent barriers for
the SAME session cross-link the per-entry gate" lead: `barrier.Coordinator.Dispatch` has exactly one
production caller, `pkg/gateway/podlifecycle/prestop/prestop.go:505`. Do not spend a round on it.

FACT: the OPEN "UNVERIFIED: `upsertMirror`'s stale-window barrier. Whether one dispatched inside the
window is refused" resolves as: refused, and it fails safe. The mirror carries G while the pod holds G+1,
so CODE-2's `initialized && gen != fenced` gate refuses with `FailedPrecondition`, `adapterclient` maps it
to `ErrGenerationStale`, `dispatchOne` sets `Stale` and leaves `Acked` false
(`pkg/gateway/coordination/barrier/barrier.go:229-233`), and prestop's fallback capture then runs
(`prestop.go:388-396`). No capture is lost in that window; the cost is an unquiesced capture, which is
today's behaviour for that session. Not a finding.

FACT: the S3/S4/S5 ordering is coherent on the failure paths, checked step by step. After S4 alone
(baseline, no D7), a never-handed-off session's barrier carries 1 instead of 0, so it clears the
adapter's `gen <= 0` guard (`pkg/adapter/coordination.go:224-226`) and is refused one guard later by the
unset arm (`:236`) as `FailedPrecondition`, which prestop still reads as unacked. Nothing loses a capture
between S4 and S5, and 0 stays impossible as a fenced value at every intermediate step, so CODE-3's
zero-means-unset sentinel is sound before CODE-4 lands.

UNVERIFIED: whether the phase split the finding asks for changes migration 0181's number, its
`prodMigrationSchema` row, or `scripts/lint-migrations.sh` pass 3's reference requirement, which would
touch §8, §9, the summary's "Watch out for" paragraph, and checklist step S4. Whoever applies the fix
owns that cascade.


### [non-spec.1.review-security.1]

DECISION: filed exactly one finding, a criterion-(f) test gap: D7 retires the barrier's `!initialized` refusal, leaving `checkSessionBound` as the sole fail-closed guard on the barrier path, and §8 names no case pinning it — BECAUSE the guard is what refuted class (f) leans on to close the fencing hole, so the design argument makes it load-bearing while nothing in the tree or in §8 asserts it — ALTERNATIVES: rejected re-filing the D7 fencing hole (refuted class (f)); rejected the rebind/unset reset (see FACT below); rejected the deploy-window migration item (already a DEFERRED, and not a security defect); rejected the "Both refusals are loud and fail closed" imprecision at non-spec-changes.md:144 (already a settled recorded imprecision, outcome is still fail-closed).

FACT: no test anywhere in the tree asserts `CheckpointBarrier` for a session with no slot binding is refused. `TestCheckpointBarrierRequiresSession` covers only an empty session id (InvalidArgument), and the full grep for `CheckpointBarrier` across `pkg/**/*_test.go` and `tests/` returns no unbound-session case. Nor is there a barrier-side counterpart to `TestCoordinatorFenceRejectsZeroGeneration` — EVIDENCE: pkg/adapter/coordination_test.go:175-183, :47.

FACT: the rebind-reset worry is not exploitable and should not be filed. `ensureSlotStateLocked` does recreate a `slotState` for a slotID after `deregisterSlotLocked` removed it (pkg/adapter/slot.go:82-102), so a per-entry `lastFenced` genuinely resets where the pod-wide one did not. But a fence only issues after a successful §10.1.2 step-1 compare-and-swap, so the exemption a reset grants admits only a CAS winner — the same reasoning that kills refuted class (b). It cost me a detour; do not spend another.

FACT: the security surface outside `pkg/adapter` is clean and I verified it independently of the standing context's derived inventory. `grep -rn "last_generation|coordinator_connection_lost|coordinator_generation_gap|coordinator_lost" docs/ charts/ pkg/alerting/ spec/16*.md` returns nothing, and the three coordination metrics in `docs/reference/metrics.md:307,:309,:312` and `spec/16_observability.md:183,:185,:192` are unit-neutral with no alert rule. Dropping `last_generation` from the pod-level arming line reaches no runbook or alert.

FACT: the fence path's generation reader fails closed on a store fault rather than returning 0, so CODE-4's deletion of `coordfence.go:147-153` is safe on the outage axis. `sessionGenerationReader.CoordinationGeneration` returns `(0, err)` and `fence()` aborts on `err != nil` — EVIDENCE: cmd/lenny-gateway/main.go:374-380; pkg/gateway/coordination/coordfence/coordfence.go:143-146.

FACT: every code citation I re-opened resolves. `pkg/adapter/coordination.go:89`, `:94`, `:216`, `:225`, `:236`; `pkg/adapter/holdstate.go:43`, `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:283`; `pkg/gateway/coordination/coordfence/coordfence.go:143`, `:147-153`. The `quiesced` field has no production reader (its only readers are `isQuiescedForBarrier`, test-only, and its own write sites), so moving it per-entry changes no shipped behavior — EVIDENCE: pkg/adapter/coordination.go:33-38, :52-56.

UNVERIFIED: whether CODE-3's post-mortem read of the terminated session's `lastFenced` off the detached `*slotState` takes the entry's `coordinationState.mu`. A `CoordinatorFence` that passed `checkSessionBound` before pass 1 can still be writing that field on the detached pointer. `coordinationState` embeds its own `mu` and moves whole onto the entry, so the natural accessor is locked and I judged the race too speculative to file. A code-lane implementor should use a locked read, not a bare field read. Nobody has checked this.


### [non-spec.1.review-test-coverage.1]

FACT: `TestFenceZeroGenerationFencesAtBaseline` is a third landed-test break from CODE-4, outside both classes §8 enumerates, and the completed sweep could not have found it because it seeds through `genReader{gens: []int64{0}}` rather than a `CoordinationGeneration:` literal. It is the only `genReader` site carrying 0 in the tree — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence_test.go:173-183; grep for `gens: []int64` returns coordfence_test.go:80,:100,:123,:143,:177 only.
FACT: the standing context's own Traps entry already named this test as a break outside the class ("`TestFenceZeroGenerationFencesAtBaseline` pins the very floor CODE-4 deletes"), and the fix that answered that MISTAKE landed only the class-2 paragraph for `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1`. Half of a two-part MISTAKE was applied — EVIDENCE: review-log.md:378-382 against non-spec-changes.md:324-338.
FACT: the pod-level op lock genuinely makes §8's tier-7a phrase "neither waits on the other" false, and I re-derived it rather than trusting the log: `opLock.Begin` puts a distinct session id in `l.checkpoints` and calls `l.wait`, which blocks on `promote` until the running op releases — EVIDENCE: pkg/adapter/oplock.go:117-128, :133-140; pkg/adapter/checkpoint.go:111, :122-125.
DECISION: filed two findings and nothing else — BECAUSE every other tier and behavior in §8 resolves to a named case with concrete assertions, and the remaining candidates are all in the thrice-refuted tier-list-bookkeeping class or already carried as OPEN — ALTERNATIVES: rejected filing §8's tier-4 sentence for D7 (names no case while S5 declares no tier 4; it is bookkeeping and is already OPEN under `[non-spec.5.review-feasibility.1]`), the tier-3 case's missing §9 file (Traps says explicitly not to spend a verification on it), the `.down.sql` asserting one restored check but not the two restored defaults (nice-to-have beside a mechanism gap already OPEN), and the two memstore `Update` tests (the class sentence covers them and two lenses already declined).
USEFUL [review-log Standing context, "Landed cases already pin what §8 might otherwise be thought to owe"]: saved re-deriving the disposition of every landed adapter coordination test; `revoke_double_teardown_test.go` as CODE-1/CODE-3's tier-2 regression gate closed the "CODE-1 declares tier 2 with no case" question before I spent a pass on it.
USEFUL [review-log Traps, "Editing hazards in this proposal's own files"]: `grep -n ... *spec-changes.md` really does glob both files; every hit I took that way was labelled with its own filename, which is what let me see that no file names `TestFenceZeroGenerationFencesAtBaseline`.
WATCHOUT: §8's two-class framing invites the reader to check inside the classes. Both residues found so far were outside them. A landed test breaks under CODE-4 if it asserts anything downstream of the deleted `coordfence` floor or of either `Create` floor, whether or not a `CoordinationGeneration:` literal appears in it.

## Retired

Retired in compaction pass 17:

- `[nonspec.5.postfix-fix.1]`, the postfix correction to pass 21: its two FACTs are lifted, the internal-versus-external test package wall
  into the fixture-hazards trap and the `-race`-at-tier-1 finding into a `### Settled` line carrying the tier-placement discriminator it
  produced. Its DECISION that the corrections were appended to pass 21 rather than opened as a new pass records one round's bookkeeping.
- `[non-spec.5.fix-G1.1]` and `[non-spec.5.fix-design-G1.1]`, the CODE-1/CODE-2 re-split and the design behind it: the decision, the
  closed reader set for `s.barrier`, the two-caller `checkpointRootsForSession` signature change, the deregistration-order fact, and the
  rejected alternatives are one `### Settled` line; the MISTAKE that a deliverable relocating state owns every site that reads it was
  already standing; the §8-preamble scope is a new trap. Their `CORRECTS` of the guard-ordering bullet's "a per-entry gate never has to
  handle a missing entry" was applied in pass 15 and stands as a trap. Their two `UNVERIFIED` items moved to `[non-spec.5-6.*]`.
- The round-5 post-fix review header: it records one round's diff scope and a LANDED list, which is history.
- The twelve `[non-spec.5.review-*.1]` lenses and the two `[non-spec.6.review-*.1]` lenses: their derived inventories (the docs surface at
  its eighth derivation, the proto carriers, the migration gates, the claim-map surfaces, the baseline-shift classes, the citation
  re-resolutions) are already the derived-inventories and baseline-shift lines, and what they added is lifted as the deploy-ordering, op
  lock, landed-cases, migration-environment, prestop-gap, fence-refusal, barrier-id, and no-sentinel lines in `### Settled` plus five new
  traps. Their empty-findings and one-finding DECISION paragraphs record a verdict rather than a durable fact, and their snapshot-diff
  FACTs and WATCHOUTs describe one round's inputs. Every `OPEN` and `UNVERIFIED` they carried moved to `[non-spec.5-6.*]`.
- The round-5 verification of the CODE-2 exemption finding: it confirms a finding the same round's fix landed, so its re-derived citations
  are spent, and its two recorded overstatements in the finding's own wording are history.
- WATCHOUT [`[non-spec.5.review-feasibility.1]`] that CODE-1 and CODE-2 split `barrierGate`'s move across S3 and S5, leaving S3
  non-compiling: deleted rather than kept. The same round's fix gave CODE-1 the whole move, so the trap it warns about no longer exists.
- FACT [`[non-spec.5.review-test-coverage.1]`] that no test under `tests/tier4_integration/` references `CheckpointBarrier`: deleted
  rather than kept. `[non-spec.6.review-test-coverage.1]` corrected it, the call sitting behind `coordfixture.Pod.StaleRPCRejected`, and
  the correction is the fixture trap in `### Traps`.
- The `USEFUL` markers of both rounds: every item they credit (the derived inventories, the editing hazards, the refuted classes, the
  refused-barrier cost model, the self-report trace, the `>= 1` writer enumeration, the §28/§29 membership criterion, the §10.1.8 step 3
  quote) is in `## Standing context`, where it is protected.

Retired in compaction pass 16:

- `[non-spec.4.review-edit-sites.1]`, the round-4 edit-site lens: its empty-findings DECISION records a verdict rather than a durable fact,
  and its six FACTs are lifted into `## Standing context`. The claim-map generator mechanism, the inert `prodMigrationSchema` row, the
  class-1 inventory's completeness, the two-line hold-log-line surface, the seven-carrier proto grep, and the absence of any tier-0 or
  tier-11 gate over the rewritten sentences are each one Settled line; the accessor blast radius is a Settled line of its own. Its `CORRECTS`
  of the §28.8 row gate's tier was already applied in pass 15. Its `CORRECTS` warning a future round off the slot-registry reading of §8's
  "the registry state" gloss is a trap, and its `USEFUL` marker crediting the `docs/`/`charts/`/`sdks/`/§16 sweep is carried by the
  derived-inventories line the marker names. Its one unclosed `UNVERIFIED`, the 0181 `.down.sql` reversal gap, moved into the residue entry.

Retired in compaction pass 15:

- `[non-spec.3.fix-G1.1]` and `[non-spec.3.fix-design-G1.1]`, the S3/S4 split fix and the design behind it: the rule they landed, the closed
  two-file class, the two batching directions that turn a tier red, and the lesson about correcting a class rather than a file are one
  bullet in `## Standing context`. Their two `CORRECTS` of the design (the tier-7a assertion is `:287-288` rather than `:286-288`, and the
  per-deliverable "CODE-4 reaches tiers ..." line had to gain 7a beside S4) were applied in the same round. The design's OPEN asking whether
  S4 runs tier 7a is closed: the fix added 7a to S4, to §8's CODE-4 tier line, and to the per-deliverable line.
- The round-3 post-fix verification header: it records a diff scope for one round rather than a durable fact.
- `[non-spec.3.review-edit-sites.1]`, `[non-spec.3.review-feasibility.1]`, and `[non-spec.3.review-fresh.1]`: their memstore, class-2,
  spec-phrase, proto, migration, tier-gate, and citation inventories are the derived-inventories and baseline-shift bullets, and the hold's
  per-session read, the lock order with the stale `coordination.go:126-128` comment, the guard ordering, the op lock's queueing behaviour,
  the single `INSERT INTO sessions`, and the `coordfence` reader's error return were already standing. Their empty-findings and
  one-finding DECISION paragraphs record a verdict rather than a durable fact, their snapshot-diff FACTs describe one round's inputs, and
  their UNVERIFIED `tests/claim-map.json` row duplicates the residue entry's. Three items nothing closed moved into the residue entry.
- `[non-spec.3.verify-edit-sites.1]`: it confirms the finding the same round's fix landed, so its re-derived citations are spent. Its
  WATCHOUT that a round-2 reviewer had seen the same defect and declined to file it is history rather than a live trap.
- USEFUL [non-spec.3.fix-G1.1] crediting the reflow-a-whole-paragraph instruction: not dropped, carried by the editing-hazards bullet the
  marker names, which already states the rule and the file widths.

Retired in compaction pass 14:

- Non-spec round 2's three fix shards (`[non-spec.2.fix-G1.1]`, `-G2.1`, `-G3.1`) and three design shards
  (`[non-spec.2.fix-design-G1.1]`, `-G2.1`, `-G3.1`): the proto codegen-drift gate, the migration-lint directory rule, the
  `uploaddriver_test.go` supersede site and the two-class baseline-shift rule, the tier-4 fence attribution, and the S3/S6 merge all landed
  in the deliverable files and are one clause each in the tier-0-gates, counter-baseline, and staging-lesson bullets. Their `heldSession`,
  `holdState.gen`, `coordfixture`, and accessor-caller enumerations are the adapter-hold and fixture-hazard bullets, and their citation
  re-derivations are superseded by rounds 3, 4, and 5.
- The round-2 post-fix header and the six `[non-spec.2.review-*.1]` lenses: their `docs/`, proto, migration, claim-map, and
  baseline-shift inventories are one statement each in the derived-inventories bullet, their `>= 1`-CHECK and self-report traces are
  promoted to bullets of their own on the two round-5 `USEFUL` markers that asked for it, and their empty-findings DECISION paragraphs
  record a verdict rather than a durable fact.
- `[nonspec.2.postfix-fix.1]`, the round-2 postfix correction to pass 17: its correction stands and is the standing context's sentence that
  0181 owes both a behaviour file and a `prodMigrationSchema` row, with 0180 and 0112 as the precedents. The two round-2 FACTs it reversed,
  that a column-less migration takes no row and that 0180 is the precedent for having none, were not lifted.
- FACT [non-spec.2.fix-G1.1] and FACT [non-spec.2.fix-design-G1.1] that migration 0180 is the precedent for a column-less migration with no
  `prodMigrationSchema` row, and the DECISION rejecting a row for 0181: deleted rather than kept. `[nonspec.2.postfix-fix.1]` reversed all
  three in the same round, and `TestProdMigrationsRollBackPerStep` iterates the table alone.
- USEFUL [non-spec.2.review-fresh.1] crediting the recorded DECISION that a generated `.pb.go` is not a SCHEMA-1 site: not carried. That
  DECISION was itself false on both premises and was rewritten in pass 11, so the marker credits a trap rather than a saving.
- WATCHOUT [non-spec.2.fix-design-G1.1] that §8's closing sentence exempts a constant seeded "above the baseline" while the new class's site
  seeds at 1: deleted rather than kept. The same round widened the class and reconciled the exemption, so the trap no longer exists.
- OPEN [non-spec.2.review-edit-sites.1] and OPEN [non-spec.2.review-fresh.1] that §8 stages a tier-3 wire case, a tier-2 migration case, and
  a tier-2 resume-then-takeover case for which §9 names no file: carried live by `[non-spec.3.review-feasibility.1]`,
  `[non-spec.3.review-fresh.1]`, and `[non-spec.5.review-fresh.1]`, which own the same question with newer evidence.
- OPEN [non-spec.2.review-fresh.1] that §8's aggregate tier sentence omits tier 11 while checklist S1 claims it, and that CODE-1 claims tier
  2 with no tier-2 case: the tier-11 half is carried by `[non-spec.3.review-fresh.1]`'s UNVERIFIED and the tier-2 half is closed by
  `[non-spec.5.review-test-coverage.1]`, which found `tests/tier2_component/slotrelease/revoke_double_teardown_test.go` driving the hold to
  termination against a real `adapter.Server`.
- OPEN [non-spec.2.review-fresh.1] that §8 puts the tier-8 file's accessor edit and its baseline edit in one pass: closed. The finding was
  confirmed by `[non-spec.3.verify-edit-sites.1]` and fixed by `[non-spec.3.fix-G1.1]`, which states the S3/S4 split as a rule over the
  closed two-file class and added tier 7a to S4.
- UNVERIFIED [non-spec.2.review-feasibility.1] whether `pkg/gateway/checkpoint/checkpointer/` holds a second fixture calibrated against the
  zero baseline: closed by `[non-spec.3.review-feasibility.1]` and `[non-spec.5.review-feasibility.1]`, both of which re-ran the sweep over
  every `CoordinationGeneration:` literal and found the class has one site.
- The round-2 lenses' repeated "only the review log changed since the snapshot" FACTs: housekeeping about a comparison that rounds 3, 4, and
  5 record again in their own entries.

Retired in compaction pass 13:

- Non-spec round 1's three fix shards (`[non-spec.1.fix-G1.1]`, `-G2.1`, `-G3.1`) and three design shards
  (`[non-spec.1.fix-design-G1.1]`, `-G2.1`, `-G3.1`): their facts, watch-outs, decisions, and mistakes are the new
  hold, guard-ordering, fixture-hazard, build-tag, pass-9-phantom-edit, and proposal-format bullets in
  `## Standing context`, and their surviving OPEN and UNVERIFIED items are in the residue entry. The CODE-1/CODE-2
  rewrite, the §8 rewrite, CODE-4's bundling, and S1's bundling are recorded in the deliverable files and in the
  pass records the spec-changes file keeps.
- The six `[non-spec.1.review-*.1]` lenses, the round-1 post-fix header, and `# Verification — barrier-gate-scope`
  (CONFIRMED): the confirmed finding was fixed by CODE-1's move of `barrierGate` onto the slot registry entry, their
  `docs/`, proto, migration, and accessor inventories are one statement each in the derived-inventories and tier-0
  bullets, their citation re-derivations are superseded by rounds 2 through 4, and their finding-count DECISION
  paragraphs record a verdict rather than a durable fact. The pod-wide gate's cross-link mechanism, which four of
  them re-derived, is the premise of the CODE-1 decision rather than a live trap.
- The orphaned verification bodies that sat below round 4 (the LANDED, DRIFT, CITATIONS, Verdict, Caveats,
  FINDINGS, and "Checked and clean" sections of rounds 1 through 3): every finding they carried was fixed in the
  round that raised it, and each fact that outlives the fix is a standing-context bullet.
- `[nonspec.1.postfix-fix.1]`, the round-1 post-fix correction shard: its correction of pass 16's
  `coordination_mirror_test.go:116` entry (the row is seeded at 2, so it does not shift), its addition of the
  tier-8 third subtest to the enumeration, its move of tier 8 from CODE-3 onto CODE-1 and CODE-4, and its finding
  that `pgstore.Create` has no tier-1 lane all landed in §8 and §9. Its watch-out that those corrections were
  appended to the pass-16 subsection rather than opened as pass 17 is discharged by this line.
- FAILURE, from a round-2 verification body, that migration 0180 is the precedent for a column-less migration with
  no `prodMigrationSchema` row: corrected in place by `[nonspec.2.postfix-fix.1]`, which stands. 0180 and 0112 both
  keep a row, and 0181 owes one.
- OPEN [non-spec.1.fix-G2.1], its three G3 assignments (§9's `tests/tier7a_load/` and its omissions, the S3 and
  TEST-1 tier lists, and pass 9's narrow target sequence): closed by `[non-spec.1.fix-G3.1]`, by the round-2 and
  round-3 fixes, and by round 4's drift check, which reads the per-deliverable tier lines and the checklist as
  agreeing.
- OPEN [non-spec.1.fix-design-G1.1] that §9 omits `pkg/adapter/checkpoint.go` and names `slotsession.go` where the
  struct is `slot.go`: closed by the same fixes, and §9's contents were re-verified in rounds 3 and 4.
- WATCHOUT [non-spec.1.fix-G1.1] that CODE-3 still reads "`pkg/adapter/holdstate.go`, per §7", and WATCHOUT
  [non-spec.1.fix-G1.1] that §8, CODE-1, and S3 state three different tier sets: deleted rather than kept. CODE-3
  was rewritten to the D5 position in the same round and the tier lists were reconciled, so neither trap exists.
- OPEN [non-spec.1.review-reliability.1] that §8 should add tier 4 to CODE-4: closed. CODE-4 reaches tiers 0, 1, 2,
  3, 4, 7a, and 8 in both §8 and checklist S4.
- UNVERIFIED [non-spec.1.fix-design-G2.1] whether `coordfixture.Pod` can start a co-tenant session over its single
  dialed client: closed by `[non-spec.2.fix-G2.1]`. `Pod.Client` is exported and the second `StartSession` runs
  over it.
- UNVERIFIED [non-spec.1.review-docs-alignment.1] whether pre-0181 `coordination_lease` rows can put a 0 on the
  barrier wire: closed by `[non-spec.2.review-security.1]`. `coordlease`'s upsert names the column in both its
  INSERT and its ON CONFLICT SET lists so no row takes the default, a 0 on the wire is refused `InvalidArgument`,
  and the behaviour is identical to pre-migration.
- UNVERIFIED [non-spec.1.review-feasibility.1] whether `sweep_test.go` carries eleven zero-baseline assertions:
  closed by `[non-spec.2.review-feasibility.1]`, which counted them between `:275` and `:594` and established that
  the file's seeds never name the field, so the whole range shifts.
- UNVERIFIED [non-spec.1.review-fresh.1] whether `status.md` is inside the loop's writable set: superseded. The
  residue entry carries the status-file corrections as an OPEN with no owner, which is the live form of the
  question.
- UNVERIFIED [non-spec.1.review-security.1] whether a per-session `barrierGate` is the right resolution or the
  gateway should serialise co-tenant barriers per pod: closed by `[non-spec.1.fix-design-G1.1]` and CODE-1.
  §10.1.8 step 3 fixes the unit at the session, and serialising starves the second session under one 90s deadline.

Retired in compaction pass 12:

- `[spec.6.review-fresh.1]`, round 6's fresh lens over the converged spec text: its one finding (the stale `**IMPLEMENTOR TO FILL THE
  BLANKS.**` header over `## 5. Proposed changes`) and the 0073/0075 evidence behind it are the draft-header bullet, as is its watch-out that
  §4's own marker is a different case; its two declined defects are in the barrier-provenance and D7 bullets; its §7.2 terminal-write fact is
  in the counter-baseline bullet and its §29.10 two-questions fact in the derived inventories; and its citation re-verification and its
  `pgstore.Create` fact restate bullets that already stand. It filed no OPEN in the spec lane.
- OPEN [spec.1-3.*] whether the new migration should tighten the `sessions` CHECK from `>= 0` to `>= 1`: closed. CODE-4 lands the tightening
  and drops the auto-named constraint first, three lenses confirmed that no raw-SQL `INSERT INTO sessions` in the tree names the column so no
  seed path breaks, and both stores' `Update` clamp to the previous value so no writer can drive a row to 0.
- OPEN [spec.1-3.*] that CODE-1 must state `initialized` moves with `lastFenced`: closed by `[non-spec.1.fix-G1.1]`, whose CODE-1 rewrite says
  so. The test-side half of the same cascade is still open and is rewritten in place to the three citations that remain.
- UNVERIFIED [citations] whether `coord.quiesced` moving onto the registry entry needs its own §10.1.8 edit: closed by
  `[non-spec.1.fix-design-G1.1]`. §10.1.8 step 3 already fixes the barrier's unit at the session, so the pod-wide gate was a code defect
  against shipped text and no spec edit is owed; `quiesced` has no production reader in any case.
- UNVERIFIED [floor-deletion] whether CODE-4's floor deletion must also handle the barrier cache-fallback branch: closed by
  `[non-spec.2.review-feasibility.1]` and `[non-spec.3.review-feasibility.1]`. It must not. The fence path's only production reader returns an
  error rather than 0, so deleting `coordfence`'s floor is safe, and flooring the cache fallback is the falsification the cache-fallback
  bullet records as a MISTAKE.

Retired in compaction pass 11:

- `[spec.5.fix-G1.1]` and `[spec.5.fix-design-G1.1]`, round 5's fix and design pair deleting the closed value enumeration from staged
  §10.1.8 step 1 rather than widening it: the deletion decision and its rejected alternatives are the barrier-provenance bullet, their
  MISTAKE that widening an unclosable enumeration reproduces the defect one step weaker is the sixth staging lesson, their cache-fallback
  and compare-and-swap-window FACTs are the cache-fallback and barrier-provenance bullets, their watch-out that a design's stated line
  range can stop mid-sentence is in the editing-hazards bullet, and their shared OPEN on "unreachable by construction" is an OPEN in the
  residue entry.
- `[spec.5.postfix.1]`, verifying that fix: its LANDED and CITATIONS facts are history, and its watch-out that the "no second value"
  clause was already falsified before the fixer ran is carried by the same OPEN.
- `[spec.5.review-fresh.1]`: its FACT that the two-way enumeration omits the value the compare-and-swap-to-fence window produces is the
  barrier-provenance bullet, its anchor and surface inventories are the derived-inventories bullet, and its two declined candidates are
  refuted classes (i) and (j).
- `[spec.5.introspect.1]`, `[spec.5.introspect-falsify-cascade.1]`, and `[spec.5.panel-architecture.1]`, the round-5 introspection
  verdict and the two lenses that falsified it: the finding-rate and lane-order facts are the loop-level MISTAKE bullet, the "nine live
  sites in nine different forms" indictment was falsified by both lenses and is not carried, and the scope question and the §4
  fill-the-blanks marker are OPENs in the residue entry.
- The round-5 SMALLER-MECHANISM falsification pass and its orphaned body sections, from "What I checked myself" to "What I would add to
  the human question": its inverted-gate enumeration is the barrier-provenance and cache-fallback bullets, and its third option for the
  human reviewer, deleting the barrier's generation gate outright, is the closing clause of the §7 bullet.
- OPEN [spec.1-3.*] that CODE-1 moves `coordinationState` per session while `barrierGate` stays pod-wide: closed by the non-spec lane,
  which moved the gate onto the slot registry entry with the state and gave `Checkpoint` a per-session link site. §10.1.8 step 3 already
  fixed the barrier's unit at the session, so the pod-wide gate was a code defect against shipped text and no spec edit was owed.
- UNVERIFIED [security] whether the gateway ever acts on the pod's self-reported `last_fenced_generation`: closed by
  `[non-spec.2.review-security.1]`. `adapterclient` copies it into `CoordinatorFenceResult` and nothing outside tests reads it; `fence()`
  branches on `res.Accepted` alone and re-reads the authoritative Postgres value on a rejection.
- UNVERIFIED whether any tier-3 or tier-8 test asserts a refused barrier for a never-fenced session outside `pkg/adapter`: resolves to
  NO, per `[non-spec.2.review-feasibility.1]`. Every tier-3 barrier suite is a wire field-number or descriptor pin, no tier-8 test
  asserts a barrier refusal, and `tests/tier7a_load_local/prestop_no_double_checkpoint_test.go` drives a fake dispatcher.
- The "Checked and clean" bullet in the round-1 non-spec post-fix shard reading "prod_columns_test.go's ledger is not exhaustive over
  migration files and 0181 adds no column, so it needs no entry": retired in place by `[nonspec.2.postfix-fix.1]`. The table is what
  drives the per-step rollback walk and a column-less migration is exactly the case it carries, so 0181 owes a row.
- DECISION [non-spec.1.review-edit-sites.1] that a generated `.pb.go` is not a SCHEMA-1 site: rewritten in place to what is true, under
  the CORRECTS in `[non-spec.2.fix-design-G1.1]`. Both of its grounds were false and the wrong decision cost the site a full round.

Retired in compaction pass 10:

- `[spec.4.fix-G1.1]`, round 4's fix entry replacing pass 11's consequence clause in staged §10.1.8 step 1 with a read-time
  provenance clause: its decision, its FACT that both barrier-target producers fix the generation at target-set assembly, and its
  FACT that the mirror lag is a whole sweep interval are the barrier-provenance bullet; its MISTAKE on stating an outcome where the
  true statement is about provenance is the fourth staging lesson; its watch-out on pass records is in the editing-hazards bullet;
  its USEFUL credited the reflow and one-physical-line hazards, which stand; its OPEN on the word "current" is in the residue entry.
- `[spec.4.fix-design-G1.1]`, the design half of the same fix: its alternatives, its FACT that the provenance half is missing from
  shipped step 1, and its FACT that one over-general clause was live at three rationale sites are carried by the same bullet and
  lesson. Its watch-out on the three further copies inside `## Resolved in adversarial review` is in the editing-hazards bullet, and
  its watch-out that only "the ordinary" overclaims is an OPEN in the residue entry.
- `[spec.4.postfix.1]`: its LANDED and CITATIONS facts are history, its MISTAKE that a withdrawal must be swept across §4's design
  blanks is the fifth staging lesson, its watch-out that the §28.8 `CH-BARRIER` and `CH-CHECKPOINT` verdicts survive the weakening is
  in the barrier-provenance bullet, and its CORRECTS against that bullet was applied there.
- `[spec.4.review-fresh.1]`: its two provenance FACTs and its MISTAKE restate the same subject; its FACT that
  `coordinationState.quiesced` has no production reader is in the `initialized` bullet, its watch-out that a refutation's scope is
  read before a mirror-staleness argument is treated as closed opens the refuted-classes bullet, and its §29.10 inbound-reference and
  anchor inventories are in the derived-inventories bullet.
- `[spec.4.postfix-fix-G1.1]`, the late-appended entry carrying the two CORRECTS the pass-12 withdrawal left standing: both were
  applied, and the barrier-provenance bullet carries the corrected text. Its decision to reflow §4's gate bullet onto the same weaker
  form is history, and its watch-out that a compaction pass should read it as the round's own shard is discharged by this line.
- OPEN [spec.1-3.*] on the files-touched list and step S1, and OPEN on CODE-3's dangling "per §7" pointer: closed by the non-spec
  round, whose LANDED record has §9 naming `spec/28`, `spec/29`, and `spec/04`, S1 bundling SPEC-1/2/3, and CODE-3 rewritten to the
  D5 position.
- OPEN [spec.1-3.*] on TEST-1's §8 pinning case asserting what D5 forbids: closed by the same round, which replaced the clause with
  an amendment of `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1`.
- OPEN [applicability] whether the pod-level `coordinator_connection_lost` event's started-session count owes a field name: closed by
  `[non-spec.1.review-docs-alignment.1]`, which found the field already named `started_sessions` in the tree, so SPEC-1's staged
  sentence is a one-attribute deletion.
- UNVERIFIED [reliability] whether the fence-retry path is idempotent: closed by `[non-spec.1.review-reliability.1]` as not a finding
  for this proposal. The rejection of a retry after a lost ack is pre-existing on both sides, no staged edit changes the predicate,
  and D6's exemption does not reach it because the first attempt sets `initialized`. It needs its own proposal.

Retired in compaction pass 9:

- `[spec.3.fix-G1.1]`, round 3's fix entry re-phrasing staged §10.1.2 step 3 on the value an RPC carries: its decision, its FACT
  that every owner-named statement of when the pod stops accepting an RPC is false on `CH-BARRIER`, its FACT that step 2 already
  states the window on the carried value while its three `spec/28` mirrors re-owner-ised it, its decision to stage the §28.5.1
  `CH-FENCE` Exclusivity bullet and the §28.8 `CH-FENCE` cell, its FACT that the §28.5.1 `CH-CHECKPOINT` and `CH-BARRIER`
  Exclusivity bullets stay non-sites, and its MISTAKE that pass 10 left the §28.6 fence-acknowledgement sentence standing are in
  the barrier-provenance, §28.6, and membership-criterion bullets. Its two watch-outs are in the editing-hazards and
  derived-inventories bullets, and its OPEN recorded that the finding needed no §7 decision.
- `[spec.3.fix-design-G1.1]`, the design half of the same fix: its four rejected alternatives, its FACT that `spec/10:41` is the
  only site of the no-window claim, and its MISTAKE that SPEC-1 attaches the whole-RPC-set domain claim to step 3's opening
  sentence rather than to the acceptance sentence are in the step-3 and derived-inventories bullets. Its FACT on both
  target-producer paths carried a consequence clause `[spec.4.postfix-fix-G1.1]` retired, and the surviving provenance half is the
  barrier-provenance bullet.
- `[spec.3.review-feasibility.1]`, the actor-action lens: its five capability checks are the residue entry's CODE-3 obligation,
  its verified anchor list and its `ShutdownRequest` and `spec/18` phase facts are in the derived-inventories bullet, its FACT
  that the four staged mirrors read alike is restated by rounds 4 and 5, and its watch-out on §29.8's Preconditions paragraph is
  in the §29.8 step 9 bullet.
- `[spec.3.review-fresh.1]`: its anchor inventory and its `last_fenced_generation` and `coordinator_connection_lost` counts are in
  the derived-inventories bullet, its MISTAKE that ten passes never cross-read the step-3 no-window sentence against the barrier
  acceptance is in the step-3 bullet, and its three declined candidates are carried as UNVERIFIED items in the residue entry. Its
  two USEFUL markers name bullets that still stand.
- The round-3 post-fix review: its header, which opened the ledger, and its LANDED, CITATIONS, and DRIFT body, which sat orphaned
  below round 6's entry. DRIFT F1 is the §29.8 step 9 bullet, DRIFT F2 is the first staging lesson, its citation verdict is
  re-derived in rounds 4, 5, and 6, and its non-findings paragraph records that every surviving "no-window sentence" phrase in the
  proposal sits in a historical pass record.
- WATCHOUT [spec.3.fix-design-G1.1] that SPEC-2 files §28.6's fence-acknowledgement sentence as "unchanged / agrees" and that a
  later round will refile the finding against it: deleted rather than kept. SPEC-2 stages that sentence now, so the trap no longer
  exists.

Retired in compaction pass 8:

- `[spec.2.postfix-fix-G1.1]`, the post-fix entry staging §29.8 step 9's window clause into the value form: its decision, its
  two FACTs, and its watch-out against reading step 9 as adjudicated are the §29.8 step 9 bullet in `## Standing context`.
- `[spec.2.postfix-fix-G2.1]`, the post-fix entry rescoping SPEC-2's §28.6 unchanged-sentences clause: its decision and its
  MISTAKE on a scope clause re-anchoring when it moves to a new bullet are the first lesson in the staging-lessons bullet.
- The round-2 post-fix review body verifying pass 10 (its LANDED, DRIFT, and CITATIONS sections): its DRIFT failure on
  SPEC-2's "costs the drain checkpoint and double-captures" clause was closed in pass 6, its citation list is re-derived in
  later rounds' entries, and its note that a pass record keeps the words it was written with is in the editing-hazards bullet.

Retired in compaction pass 7:

- `[spec.1.postfix-fix-G2.1]`, run 3 round 1's post-fix entry splitting §28.6's second opener by channel: its decision, its
  FACT on the compare-and-swap-to-fence-acknowledgement window, and its MISTAKE on generalising a `CH-FENCE` rationale across
  four channels are the §28.6 bullet in `## Standing context`. Its CORRECTS named a watch-out retired in pass 6.
- `[spec.1.postfix-fix-G3.1]`, run 3 round 1's post-fix entry rewriting SPEC-2's `CH-BARRIER` cost clause: its decision and
  its FACT are the refused-barrier bullet in `## Standing context`, and its CORRECTS is the new closing clause of the
  editing-hazards bullet, that a round lifting a cost clause out of a pass record lifts the corrected one.

Retired in compaction pass 6:

- The `[spec.1-4.*]` residue entry of run 2: replaced by the residue entry that now opens the ledger, which carries its
  OPEN and UNVERIFIED items unchanged alongside the ones round 1 and round 2 added.
- Run 3's round 1: `[spec.1.fix-G1.1]`, `[spec.1.fix-design-G1.1]`, `[spec.1.fix-design-G2.1]`,
  `[spec.1.postfix-review.round1]`, `[spec.1.review-docs-alignment.1]`, `[spec.1.review-edit-sites.1]`,
  `[spec.1.review-feasibility.1]`, `[spec.1.review-fresh.1]`, `[spec.1.review-reliability.1]`,
  `[spec.1.review-security.1]`, `[spec.1.verify-fresh.29.7-predicate]`, `[spec.1.verify-generation-stamp-baseline]`,
  `[spec.1.fix-G1.2]`, and `[spec.1.fix-G2.2]`. Their facts, watch-outs, decisions, and mistakes are in
  `## Standing context`, and their surviving OPEN and UNVERIFIED items are in the residue entry.
- Run 3's round 2: `[spec.2.fix-G1.1]`, `[spec.2.fix-design-G1.1]`, the round-2 post-fix review header, and the six
  `[spec.2.review-*.1]` lens entries. Same disposition.
- OPEN [spec.1.fix-G1.1] and OPEN [spec.1.fix-G1.2] on the §28.5.1 `CH-BARRIER` Messages non-site record: closed by
  `[spec.1.fix-G2.2]`, which enumerates the bullet in SPEC-2's `spec/28` non-site paragraph.
- OPEN [spec.1.review-feasibility.1] asking a human to decide the baseline-at-1 option before the stamp sentence lands:
  closed by `[spec.1.fix-G1.2]`, which adopts the baseline and withdraws the stamp sentence.
- OPEN [spec.2.fix-G1.1] and OPEN [spec.2.fix-design-G1.1] on SPEC-2's "costs the drain checkpoint and double-captures"
  cost clause: closed by `[spec.1.postfix-fix-G3.1]`, which rewrote the live clause.
- UNVERIFIED [spec.1.review-feasibility.1] [stamp-vs-§07]: already closed and retired in pass 5; this second copy goes
  with its entry.
- WATCHOUT [spec.1.review-edit-sites.1] not to read `coordfence`'s floor as precedent for the stamp rule: deleted rather
  than kept. The row baseline withdrew the stamp rule, so the trap it warns about no longer exists.
- WATCHOUT [spec.1.review-fresh.1] that staged §10.1.8 and §29.7 carry different guards: deleted. Pass 11 aligned them
  and round 3's feasibility lens records all four mirrors reading alike.
- WATCHOUT [spec.1.fix-design-G2.1] to keep §28.6's second-opener relation and add only the guard: deleted. It is
  corrected in place by `[spec.1.postfix-fix-G2.1]`, and the standing context carries the split-by-channel rule.
- CORRECTS [spec.2.review-edit-sites.1] against refuted class (c): applied in pass 5 and still applied. Class (c) does
  not claim `spec/04` §4.1's declared `pod` class was killed, and the §4.1 question stands as an OPEN.
- CORRECTS [spec.1.review-reliability.1], both of them, against round-4 entries retired in pass 5: their corrected
  content is the refuted-class MISTAKE on the equality arm.
- The two verify shards (`[spec.1.verify-*]`, both CONFIRMED): the findings they confirmed were fixed in the same round,
  and their premises are standing-context bullets.
- The round-1 and round-2 lenses' re-derivations of the `docs/`, proto, `spec/04`, `spec/28`, and `spec/29` inventories,
  their anchor-resolution lists, and their empty-findings DECISION paragraphs: one statement of each is in
  `## Standing context`.
- The "only the review log changed since the snapshot" FACT and the snapshot-comparison WATCHOUT, carried again by
  round-2 entries: housekeeping about a comparison that has nothing left to say.

Retired in compaction pass 5:

- Run 2's round 4 (`[spec.4.*]`): the fix, fix-design, post-fix, and eight lens entries. Their facts, watch-outs,
  decisions, and mistakes are in `## Standing context`, and their two surviving OPEN items are in the residue entry.
- OPEN [spec.4.fix-G1.1] and OPEN [scope] [spec.4.fix-design-G1.1], both asking whether the session row should be
  baselined at 1: closed by `[spec.1.fix-G1.2]`, which adopts the baseline.
- OPEN [spec.4.fix-G1.1] carrying CODE-4 and its checklist step for a loop that may write the deliverable files:
  superseded, because run 3 deleted CODE-4 and Pass 9 of the spec changes carries the replacement.
- OPEN [spec.4.review-reliability.1] whether §28.8's `CH-CHECKPOINT` cell survives step 3's unset clause: closed by
  `[spec.1.postfix-review.round1]`, which records the cell as a staged edit site.
- UNVERIFIED [spec.4.review-security.1] whether a barrier accepted under D7 can be issued by a replica that never
  coordinated the session: closed by `[spec.2.review-security.1]`, since both target producers require this replica
  to have been the coordinator.
- UNVERIFIED [stamp-vs-§07] in the residue entry: closed by `[spec.2.review-edit-sites.1]`. Under a row baseline of 1
  the §7.2 bump runs 1 to 2 while the stale coordinator carries 1, so the bump fences; the defect belonged to pass 8's
  withdrawn wire value.
- The round-4 copies of the [d6-reset-ground] UNVERIFIED and of the §28.5.1 `CH-BARRIER` Preconditions UNVERIFIED:
  duplicates of the residue entry's own, which stand.
- WATCHOUT [spec.4.fix-G1.1] "D7 and the stamp rule are not substitutes": deleted rather than kept. The row baseline
  withdrew the stamp rule, so the trap it warns about no longer exists.
- MISTAKE [spec.4.review-reliability.1] that §28.6's second-opener paragraph is not an unstaged mirror D7 falsifies:
  reversed by `[spec.1.fix-G2.2]` and `[spec.1.postfix-fix-G2.1]`, which stage that sentence and split it by channel.
  Deleted so it does not send a later round away from a live edit site.
- CORRECTS [spec.2.review-edit-sites.1] against refuted class (c): applied. Class (c) no longer claims `spec/04` §4.1's
  declared `pod` class was killed, and the §4.1 question stands as an OPEN in the residue entry.
- The round-4 lenses' re-derivations of the `docs/`, proto, `spec/28`, `spec/29`, and zero-gate inventories, and their
  empty-findings DECISION paragraphs: one statement of each is in `## Standing context`.

Retired in compaction pass 4:

- Run 2's `[spec.1.*]` round-1 residue prose, the merged `[spec.2.fix-G1]`, `[spec.2.fix-G2]`, `[spec.2.fix-G3]`,
  and `[spec.2.review-*.1]` entries, and the twelve `[spec.3.*]` fix, design, post-fix, and lens entries: their
  facts, watch-outs, decisions, and mistakes are in `## Standing context`, and their live OPEN and UNVERIFIED
  items are the `[spec.1-3.*]` ledger entry.
- The duplicate copy of `[spec.1.fix-G2.2]`: the log carried the same entry twice, once undated and once dated
  2026-08-31. The dated copy stands.
- WATCHOUT [spec.3.fix-design-G1.1] "Leave §28.5.1's CH-BARRIER card and the §28.8 CH-BARRIER row alone: they say
  'superseded replica', which means a later generation was fenced, and that stays exactly true": deleted rather
  than kept. `[spec.1.fix-G2.2]` corrects it. The §28.5.1 Exclusivity bullet does stay true, the §28.8 row does
  not, and the same watch-out's advice to file §28.6's second-opener paragraph whole under hold sentences is
  wrong for the same reason.
- OPEN [spec.4.review-docs-alignment.1] on flooring the barrier stamp at 1: marked retired in place. Run 3
  baselined the session row instead, and the OPEN's second half is backwards, as `[spec.1.review-reliability.1]`
  records: the gate is `gen != fenced`, so a floored 1 against a recorded 1 is accepted.
- DECISION [spec.4.fix-G1.1], its second, closing the reachability finding on the sender with a stamped wire
  value of 1: marked withdrawn in place by `[spec.1.fix-G1.2]`, which baselines the row at 1 and deletes CODE-4,
  `coordfence`'s floor, and the two restatements the wire value forced.
- MISTAKE [spec.4.review-reliability.1] on the D7 fencing hole: qualified in place. It is sound for the unset arm
  and says nothing about the equality arm.
- FACT [spec.2.fix-G1] that every operational request other than the fence and the barrier carries
  `coordination_generation` on the wire: superseded by run 3's finding that the gateway populates the field on
  exactly two outbound requests. The two disagree and neither corrects the other, so the disagreement is carried
  as an UNVERIFIED line in the residue entry.
- UNVERIFIED [spec.3.review-docs-alignment.1] whether the never-fenced session's barrier is sent as 0 and refused
  with `InvalidArgument`: closed by `[spec.4.review-docs-alignment.1]`, which confirmed it and made it the
  load-bearing fact D7 was written without.
- OPEN [spec.3.review-mechanism.1] that §10.1.8 becomes an edit site if the reviewer keeps equality on the
  barrier gate: closed. §10.1.8 step 1 is a staged SPEC-1 edit site.
- OPEN [spec.3.fix-G1.1] carrying CODE-2, §8, and the files-touched deltas for a loop that may write the
  deliverable files: superseded by the same obligation in `[spec.4.fix-G1.1]` and `[spec.1.fix-G1.2]`, which
  state the current form. The residue entry carries the parts that did not change.
- OPEN [spec.3.fix-G1.1] that the implementation checklist needs no structural change for D7: superseded. Run 3
  replaced CODE-4 and reordered its step ahead of CODE-2's, and Pass 9 of the spec changes carries the target
  sequence.
- FINDINGS [spec.3.postfix-review], its three: all acted on in run 2's round 4 and in run 3, and the surviving
  half of each is a standing-context bullet.
- The "only the review log changed since the snapshot" FACT, repeated by nine entries across rounds 2, 3, and 4:
  housekeeping about a snapshot comparison that has nothing left to say.
- The empty-findings DECISION paragraphs of the round-3 applicability, citations, client-surface, and
  operational lenses: they record a verdict rather than a durable fact, and the refutations worth keeping are in
  the refuted-class bullet.
- The round-3 lenses' repeated re-derivations of the `docs/`, proto, `spec/28`, `spec/29`, fence-sender, and
  zero-gate inventories: one statement of each is in `## Standing context`.

Retired in compaction pass 3:

- Round 1's `[spec.1.followup-fix.1]` MISTAKE and FACT lines, the D5, D6, and SPEC-2 decision bodies with their
  ALTERNATIVES, and `[spec.1.review-*.1]`: their durable residue is in `## Standing context` and their OPEN and
  UNVERIFIED items are in the round-1 residue entry.
- OPEN [spec.1.followup-fix.2] whether the reviewer settling §7 item 1 keeps the barrier's refusal of a
  never-fenced session: closed by D7, which settles it as acceptance and leaves the reviewer the operator alone.
- OPEN [spec.2.fix-G1.3] for a test pinning step 3's refusal of a never-fenced session's barrier: the refusal
  is retired by D7, and round 3's `[spec.3.fix-G1.1]` OPEN carries the replacement test obligation.
- WATCHOUT [spec.2.fix-design-G1.1] "do not add the unset clause to the `spec/28` or `spec/29` mirrors":
  deleted. D7's staging puts the qualifier into §29.7's framing paragraph, so the warning now points away from
  an edit the proposal makes.
- WATCHOUT [spec.2.fix-design-G1.1] that SPEC-1's "two sentences" count must become three: deleted. The fixer
  removed the count instead, which the post-fix review confirmed as the better call.
- OPEN [spec.2.fix-design-G1.1] whether §7 item 1 should gain the never-resumed-session fact: closed by round
  3, which rewrote item 1 to drop the refusal assertion and keep the operator open.
- OPEN [spec.2.fix-design-G1.1] that CODE-2 should be told to cite §10.1.2 step 3: closed by
  `[spec.2.fix-G1.1]`'s FACT that the comment at `pkg/adapter/coordination.go:228-231` already cites §10.1.2.
- OPEN [spec.2.review-feasibility.1] asking a human to settle which way the spec states the unset case: closed
  by D7.
- UNVERIFIED [spec.2.review-fresh.1] whether a genuinely new §28.5.1 sentence owes an `ABSENT` claim-register
  row: closed by round 3's finding that `claim_register_test.go` is a schema gate; promoted as a fact.
- UNVERIFIED [spec.1.fix-G3.1] on the `CheckpointBarrierRequest.coordination_generation` claim-map row:
  confirmed twice as pre-existing test-lane drift; promoted as a fact.
- OPEN [spec.2.fix-G3.3] whether §1.3's `lenny_coordinator_handoff_stale_total` bullet still holds: closed by
  the feasibility lens and kept as a FACT in the merged `[spec.2.fix-G3]` entry.
- FINDING [spec.2.postfix-review.2] that the `spec/29` mirrors do not carry step 3's refusal half, and FINDING
  [spec.2.postfix-review.3] that the refusal would reach `Interrupt`, `Checkpoint`, `ExportPaths`, and
  `Shutdown`: both were acted on in pass 4 and are superseded by D7.
- The "only the review log changed since the snapshot" FACT, carried by six round-2 entries: housekeeping about
  a snapshot comparison that no longer has anything to say.
- The twelve lens entries' re-derivations of the `docs/`, proto, `spec/28`, and `spec/29` inventories and of
  the two-fence-sender set: one statement of each is in `## Standing context`.
- The empty-findings DECISION paragraphs of the Kubernetes, performance, client-surface, and security lenses:
  they record a verdict rather than a durable fact, and the refutation notes worth keeping are in the merged
  lens entry.
- The two CORRECTS [spec.2.fix-G1.1] raised against standing-context bullets: applied in pass 2, and the
  bullets carry the corrected text.
- The duplicate CORRECTS on D5's "terminal record" half, raised by both `[spec.2.fix-G2.1]` and
  `[spec.2.fix-design-G2.1]`: kept once, in the merged `[spec.2.fix-G2]` entry.
- The four CORRECTS in `[spec.2.followup-fix.1]` against pass-7 text: the corrections landed in the
  spec-changes file, and the durable half is the step-2 wording FACT in the merged `[spec.2.fix-G1]` entry.

Retired in earlier passes:

- The twelve lens entries' repeated inventories of the same mirror sites (`spec/04`, `spec/28`, `spec/29`):
  eleven near-identical statements of one inventory, kept once in `## Standing context` and in the SPEC-2
  entry.
- The `coordinationState` quiesce warning as carried separately by review-citations, review-client-surface,
  review-feasibility, review-mechanism, and review-security: one subject, kept as the OPEN in the fix-G1 entry.
- FACT [review-operational] "spec/28:1807 is the §28.6 escalation register row for CH-FENCE": wrong. Line 1807
  is the `CH-FENCE` row of the §28.8 failure and degradation matrix (section headings at spec/28:1660, :1752,
  and :1785). The §28.8 reading in the fix-G3 entry is the one kept.
- FACT [review-docs-alignment] "§28.6 'One holder per session' is not an edit site": corrected by
  [spec.1.fix-design-G3.1]. The paragraph's unit sentence is about the exclusivity constraint and its
  rejection sentence at spec/28:1673-1675 is about the pod's gate, which is an edit site SPEC-2 stages.
- WATCHOUT "`holdState` carries one session and one generation, at spec-changes §7 and at problem-statement
  §1.3": deleted rather than kept. Both sites were rewritten when D5 landed, so the trap no longer exists.
- WATCHOUT "summary.md still claims the hold release as fixed": deleted. The summary now records the pod-level
  release as correct behavior under D5.
- UNVERIFIED [review-client-surface] whether SCHEMA-1 covers the message-level `CoordinatorFenceRequest` and
  `CoordinatorFenceResponse` comments: half of it was verified in [spec.1.followup-fix.1], which names the
  `CoordinatorFence` RPC comment and `CoordinatorFenceResponse`. Corrected by [spec.2.review-applicability.1]:
  the message-level `CoordinatorFenceRequest` comment at `schemas/lenny-adapter.proto:1442-1446` is a third
  carrier, and the settled seven-carrier enumeration is in `## Standing context`.
- UNVERIFIED [review-docs-alignment] whether `tests/claim-map.json` carries a row for the §10.1.2 cancellation
  needing a re-scope: verified. No row states the fence rule's scope; the nearest names the mechanism only.
- UNVERIFIED [review-edit-sites] and [review-kubernetes] on sites that would move if §7 settled on a
  per-session hold: closed by D5. The gauge, `spec/16_observability.md:185`, `docs/reference/metrics.md:309`,
  and the §28.8 hold cells need no edit.
- OPEN [review-security] whether the human should be asked to retract `spec/10` §10.1.4's two whole-pod
  sentences rather than pick a §7 option: closed by D5, which keeps both sentences and settles the item.
- OPEN [review-mechanism] whether two co-tenant sessions on one pod can have two different coordinating
  replicas: merged into the CH-ADAPTEREVENTS ownership OPEN in the fix-G1 entry, which is the same question.
- OPEN [spec.1.fix-design-G2.1] whether §4 bullet 1 and §7's unheld-session decision interact with D6: closed
  by the `checkSessionBound` FACT in the fix-G2 entry.
- OPEN [spec.1.fix-design-G3.1] that SPEC-1 did not stage §10.1.2's reset clauses under SPEC-2's re-scoped
  mirrors: closed by the fix-G2 decision that widened SPEC-1's clauses (a), (b), and (c).
- OPEN [spec.1.fix-design-G3.1] that `spec/29` was claimed by three findings under a one-bullet-per-file list:
  closed by SPEC-2's single `spec/29` sub-block. The files-touched list itself remains OPEN in
  [spec.1.followup-fix.1].
- The verify shard for the hold-observability finding (run 2, verdict CONFIRMED): its five FACTs are the
  premises of D5 and of SPEC-1's §10.1.4 half, both landed and recorded above.
- The DRIFT list of [spec.1.review-postfix.1]: its five items are the OPEN items [spec.1.followup-fix.1]
  carries verbatim.
- FACT [spec.1.followup-fix.1] "Neither request-field comment carries any of them": false at the message
  level. `[spec.2.review-citations.1]` and `[spec.2.fix-G1.2]` both correct it; the settled enumeration of all
  seven proto carriers is the standing-context bullet on the mirrored gap reset.
- CORRECTS [spec.1.followup-fix.1] on the problem statement's §1.2 citations: applied. `Server.coord` is at
  `pkg/adapter/server.go:302` and the `CoordinatorFenceRequest.coordination_generation` comment at
  `schemas/lenny-adapter.proto:1449-1451`.
- FACT [spec.1.fix-G1.1] that `pkg/adapter/holdstate_test.go:699-715` asserts `LastGeneration: 7` on both
  sessions: the same assertion is carried by `[spec.1.followup-fix.1]`'s TEST-1 OPEN and by
  `[spec.2.fix-design-G2.1]`.
- FACT [spec.1.fix-G1.1] that "last known generation" has exactly two sites: restated with evidence by
  `[spec.2.review-docs-alignment.1]` and `[spec.2.review-performance.1]`, both of which confirm both are staged.
- DECISION [spec.1.fix-G2.1] to name the §10.1.2 sentences by quoted text rather than by line number under
  channel-naming N8: the convention landed, and `[spec.2.fix-G3.3]` records that `proposals/` sits outside the
  N8 scope in any case.
- FACT [spec.1.fix-G2.1] that only `CoordinatorFence` and `CheckpointBarrier` read the generation: restated
  with the same evidence by `[spec.1.followup-fix.2]` and `[spec.2.fix-design-G1.1]`.
- FACT [spec.1.fix-G3.1] on the §28.8 tier-0 bijection gate: restated by `[spec.2.review-applicability.1]` and
  `[spec.2.review-mechanism.1]`, which also verify the gate's line numbers independently.
- FACT [spec.1.fix-G3.1] that nothing in tests/ pins §29.10's classification lists: restated by
  `[spec.2.review-citations.1]` and `[spec.2.review-applicability.1]`.
- FACT [spec.1.fix-G3.1] on §29.8's two mirror sites: covered by `[spec.2.review-applicability.1]`'s verified
  anchor list, which names §29.8 step 2 and step 7 among the anchors no round needs to re-verify.
- USEFUL [spec.1.fix-G2.1] crediting G1 for leaving the gap sentence verbatim as an anchor: its subject,
  sequencing the two §10.1.2 edits inside one round, is done.
- The USEFUL markers in the round-1 fix entries, crediting review-mechanism, review-security,
  review-applicability, and two standing-context bullets: every credited item is promoted into
  `## Standing context`, where it is protected.
- FACT [spec.1.fix-G1.1] restating `heldSession`'s fields: merged into the same fact in
  `[spec.1.followup-fix.1]`, which is the entry that owns the CODE-3 obligation reading from it.
- FACT [spec.1.fix-G2.1] that the exemption already exists in two carriers and was missing only from `spec/`:
  both carriers are named in `## Standing context`, the adapter's `initialized` guard in the first bullet and
  the proto's per-pod-lifetime spelling in the mirrored-gap-reset bullet.
- [spec.1.review-postfix.1], the whole entry: its LANDED list is history, its DRIFT list was already retired
  as duplicating `[spec.1.followup-fix.1]`'s OPEN items, and its citation verdict is superseded by the wider
  verification in `[spec.2.postfix-review.1]` and `[spec.2.review-citations.1]`.
- UNVERIFIED [operational], [performance], [reliability], and [security] from the lens residue: all four
  closed in round 2 and promoted to `## Standing context` as facts with their evidence.

## Index and checklist reconciliation (2026-09-02, automated)

Rebuilt `## Deliverable index` in the summary from the converged staging: SPEC-1, SPEC-2, SPEC-3, SCHEMA-1,
CODE-1, CODE-2, CODE-3, CODE-4, and TEST-1, each once, with the file it lands in. No deliverable id was
added, removed, or renumbered by this pass. The checklist's leading spec block already names the current
SPEC ids and needed no rewrite: S1 lands SPEC-1, SPEC-2, and SPEC-3 together, which the summary's
"Watch out for" paragraph states the ground for, and every non-spec step's `Depends on` already resolves to
S1 and to the code steps its work reads from.

CORRECTS [`### Deferred`, DEFERRED [this review log, from `[spec.1.fix-G1.1]`]]: the standing-context bullet
"The gap reset and the record-and-reject rule have four spec mirrors and seven proto carriers" read as
bounding SCHEMA-1's target list at the seven. SCHEMA-1's list is verified against SPEC-2's closing
paragraph and is those seven plus the twelve operational-RPC `coordination_generation` field comments the
next bullet names. The bullet now states that the "no eighth" bound is on the carriers of this rule rather
than on SCHEMA-1's list, names the wider list, and records that the `CoordinatorFenceRequest` field comment
keeps its wording. The exclusions the entry says stand are untouched, and its CORRECTED sub-clause about the
twelve is retired.

CORRECTS [`### Deferred`, DEFERRED [`non-spec-changes.md`, from `[spec.3.review-citations.1]`]]: SCHEMA-1
listed `CoordinatorFenceRequest.coordination_generation` among the comments that take the wording SPEC-2
states for each, while SPEC-2 states that comment keeps its wording (`spec-changes.md:497-498`). Removed it
from the takes-the-wording list and recorded it as the one carrier that takes no edit, with SPEC-2's reason.

OPEN [`### Deferred`, DEFERRED [`tests/claim-map.json`, from `[spec.3.review-edit-sites.1]`]]: SPEC-2 stages
§28.5.1, §28.6, and §28.8 statements that do not hold in the shipped adapter until CODE-1 and CODE-2 land,
so §28.4's rule obliges the fence and barrier claim rows an `ABSENT`-or-deferred status naming S3 and S5 for
the interval between S1 and S5. The remedy lands in `tests/claim-map.json`, which this pass may not edit and
which no deliverable stages. It needs a staged registry deliverable, and the next loop's first round owns
writing one.

OPEN [`### Deferred`, DEFERRED [`non-spec-changes.md`, CODE-4, from `[spec.3.review-performance.3]`]]:
CODE-4's migration 0181 tightens the session row's check to `>= 1` while the old gateway fleet is still
serving and `pgstore.Create` still writes a literal 0, which §10.5's expand-contract rule forbids in one
migration and one deployment. The remedy is either a §10.5 phase split of 0181 into two migration files and
two deployments or a stated exemption, and both are staged code and migration content that does not exist
yet in `non-spec-changes.md`. It lands in CODE-4 in `non-spec-changes.md` and in the migration files CODE-4
names, and the next loop's first round owns authoring it.
