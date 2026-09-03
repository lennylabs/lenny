# Review log — 0076_fix_scope-the-coordination-generation-to-the-session

## Standing context

**Compaction pass 20, 2026-09-03.** Read the whole ledger, which now covers run 4's non-spec rounds 1 to 3 and its fix round, run 4's spec
rounds 2 and 3, and run 5's spec round 1 with its fix and post-fix shards. The ledger itself is left untouched, because the round boundary
archives it whole and with its ids as soon as this pass returns; every durable line in it is lifted here instead.
Lifted into `### Settled`: the four findings run 4's non-spec fix round landed, the §29.8 Preconditions deletion run 5's spec fix staged,
the single-dispatcher fact that closes the `upsertMirror` stale-window question, the two-site wire population that closes the
wire-population disagreement, the checklist's step-to-deliverable map, the S3/S4/S5 failure-path ordering, the copylocks and Helm-rollback
facts, and the `InvalidArgument` fence outcome as a permanent stall on the resume path. Lifted into `### Traps`: the cross-lane cache
collision three agents hit in one round, the `ErrHeld` attribution three agents worked up independently, the `releaseSessionSlot` third
deregistration path, the proto and wrapped-anchor reading hazards, the harness hazards, the run-5 §29.8 mistake and its two recorded
imprecisions, and the two pre-existing observability drifts.
Honoured five `CORRECTS` against the standing context: the `## Deliverable index` bullet, since 0076's summary now carries one; the
SCHEMA-1 `DEFERRED`, discharged and deleted; the claim-map `DEFERRED`, whose remedy lands in the generator's source document rather than in
the register; the cache-fallback bullet's closing clause that deleting `coordfence`'s floor is safe, deleted, with the floor deletion now
carried whole as a `DEFERRED`; and the clause asserting that CODE-1 settles §7's third open decision by construction. Two further claims
were corrected in place from newer entries: the `RecordHandoff` sentinel now rests on the post-increment value rather than on 0 being
impossible as a row value, and the lint-pass reason for 0181 now names the absence of `add column` and `drop column` rather than a
constraint drop the migration no longer stages. Deleted as superseded: two `OPEN`s the migration decision closed and one `OPEN` the
phase-split decision made moot. `### Deferred` gains three entries whole and loses one, leaving five.
**The target of 200 lines was not reached, and the section grew: 1,051 lines against pass 19's 840.** `### Settled` is 518 lines,
`### Traps` 343, `### Open` 107, `### Deferred` 52. Nothing was dropped to reach
it. Two lanes' worth of ledger arrived in one window, most of it re-derivation of inventories that are already standing, but its residue is
a dozen new facts, three unclosed `DEFERRED` items, and twelve new `### Open` lines, none of which a later agent can recover once the
ledger is archived. The overshoot sits where pass 19 left it, in `### Settled`, whose bullets carry derived inventories rather than lookup
values and which nine separate `USEFUL` citations credit with saving a round; compressing them to one line each would reach the target by
deleting exactly what the citations name. Decline that trade until the code and test lanes land and the inventories become checkable
against a tree.
Mechanical constraints, unchanged: the Bash write path is denied for this file, so every edit goes through the editor tool and a deletion
costs the full text of what it deletes; and run 4's ledger ids repeat across append batches, so `[non-spec.1.review-applicability.1]`,
`[non-spec.1.review-citations.1]`, and their siblings each resolve to two different entries and a citation by id alone is ambiguous.

### Settled

- **Counter baseline.** The session row's counter is baselined at 1: the row is carried unchanged and §10.1.2 step 1 is untouched, so the
  first takeover mints 2. CODE-4 carries migration 0181, both session-store `Create` floors, and the deletion of `coordfence`'s zero floor.
  The counter has three writers (step 1's compare-and-swap, `Sweeper.RecordHandoff`, `bumpCoordinationGenerationOnSnapshotClose`), so any
  floor repeats on each, and `Create` inserts the value explicitly, so a column default baselines nothing. The §7.2 snapshot-close bump fires
  only under a terminal write, after which no takeover follows.
- **The `>= 1` CHECK is no longer staged, and the invariant does not rest on it.** CODE-4's migration 0181 keeps `DEFAULT 1` on both
  columns and the backfill and leaves the session row's `CHECK (coordination_generation >= 0)` alone, because the migrate Job is a Helm
  `pre-install,pre-upgrade` hook that completes before the gateway Deployment rolls and a `>= 1` check would be live while the old fleet
  still inserts an explicit zero through `pgstore.Create`; §10.5 puts such a constraint in a Phase 3 migration in a later deployment. The
  rejected alternatives were the §10.5 phase split (the Phase 3 file must not be applied in this release, so it would sit staged with no
  checklist step and drag in a second `prodMigrationSchema` row, §9 entry, and tier-2 file), an exemption clause keeping the tightening,
  and deleting 0181 (the backfill is load-bearing for rows already at 0). Nothing in the proposal or the tree reads the `>= 1` check: the
  invariant that 0 is impossible as a row value rests on the two `Create` floors and both stores' `Update` clamps, which re-read under lock
  and clamp `CoordinationGeneration` to the previous value (`pgstore.go:460`, `:475-477`; `memstore.go:129`, `:144-146`). `pgstore.go:170`
  is the only `INSERT INTO sessions` in the tree and every raw-SQL fixture omits the column. Corrected in pass 20: 0 is not impossible as a
  row value while the roll is in flight, because an old binary's `pgstore.Create` writes an explicit 0 and 0181's one-shot backfill has
  already run past it. `RecordHandoff`'s 0-return sentinel survives anyway, on the different ground that it returns the post-increment
  value, which is never 0 (`coordination.go:463-482`). USEFUL: a round-5 lens credits the writer enumeration with saving it a pass.
- **The barrier's cache fallback puts a literal 0 on the wire and must not be floored.** `httpsurface.go` seeds the target's generation at 0
  and overwrites it only on a successful session-row read, so under a Postgres fault the barrier carries 0 and is refused with
  `InvalidArgument` whatever the baseline is; the staged "unreachable by construction" claim is not exact, though the outcome is fail-closed.
  CORRECTED in pass 20: the old closing clause, that the fence path's reader returns an error rather than 0 and deleting `coordfence`'s
  floor is therefore safe, does not follow. The reader returns an error only when the read fails; when it succeeds on a row an old binary
  wrote at 0 during the roll it returns 0. The floor deletion is now carried whole in `### Deferred`.
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
- **The gap reset and the record-and-reject rule have four spec mirrors and seven proto carriers, and SCHEMA-1's list is nineteen.**
  `spec/28` and `spec/29` restate clauses (a) through (d) citing §10.1, and SPEC-2 stages both. The seven carriers of the rule itself: the
  `CoordinatorFence` RPC comment (which also spells the exemption per pod lifetime), the `CoordinatorFenceRequest` and
  `CoordinatorFenceResponse` message comments, the request's field comment, the `CheckpointBarrier` RPC comment,
  `CheckpointBarrierRequest`, and its field comment. There is no eighth carrier of this rule:
  `CoordinatorFenceResponse.last_fenced_generation` (`proto:1465`) carries no leading comment, and the `Checkpoint` RPC comment and
  `CheckpointStart.checkpoint_id` are session-neutral and stay true under the per-entry gate. SCHEMA-1's target list is those seven plus
  the twelve operational-RPC `coordination_generation` field comments the next bullet names, and within the seven the
  `CoordinatorFenceRequest` field comment keeps its wording. A grep for `fenced|older generation|handoff_stale|lifetime` over
  `schemas/lenny-adapter.proto` returns hits only at `:161-162`, `:167`, `:1444-1446`, `:1457-1460`, `:1465`, `:1472-1473`, and `:1479`,
  all inside those seven.
- **The proto carrier arithmetic is closed: fourteen fields, thirteen "gateway's view" comments, no fifteenth.** `int64
  coordination_generation` occurs fourteen times (`:974`, `:1002`, `:1051`, `:1075`, `:1096`, `:1119`, `:1179`, `:1310`, `:1398`, `:1452`,
  `:1480`, `:1536`, `:1581`, `:1623`); `gateway's view of the active` occurs thirteen times, which is the twelve operational comments plus
  `CheckpointBarrierRequest` at `:1477`, and `CoordinatorFenceRequest`'s comment at `:1449-1451` is worded differently and is handled
  separately. Each listed comment range sits immediately above its field. Four independent lenses derived this; do not derive it again.
- **The other twelve `coordination_generation` field comments are session-scoped but not neutral, and they are now SCHEMA-1 carriers.**
  Eleven close with "so a replica that has lost coordination cannot drive the pod (§10.1)" and `ShutdownRequest`'s with "cannot tear the
  session down", which SPEC-1's staged unset clause falsifies for exactly the session class D6 makes ordinary. The round-1 fix took the
  consequence clause out rather than conditioning it: each of the twelve keeps its first sentence, and the span from "A pod validates" to
  the end of the consequence clause becomes "A pod validates the generation on every gateway-to-pod RPC against the value it holds for the
  session the RPC names, and rejects a request whose generation does not match it (§10.1)." The rejected alternatives were conditioning
  each clause ("once another coordinator has fenced the session on this pod"), stating a true ground for exclusion (none exists), and
  deleting the comments outright (they carry the field's meaning for external adapter authors). No checklist or tier line moves: S2 names
  SCHEMA-1 generically and `TestProtoStubsMatchGeneratedOutput` already covers field comments landing in `lenny-adapter.pb.go`.
- **D5: the coordinator-loss hold has no per-session arm and cannot be given one here.** Its sole arm is the close of the pod's single
  CH-ADAPTEREVENTS stream, which names no session, and §10.1.4 fixes the same posture. D5 keeps both whole-pod sentences, drops the
  generation from the pod-level arming line, and has each terminated session's `coordinator_lost` line and post-mortem read its own entry,
  reporting `0` where no coordinator fenced it there. Zero is impossible as a fenced value, so the sentinel costs no wire, JSON, or state
  change, and those records already carry `last_generation` and `started_sessions`. The code's hold allowlist is wider than §10.1.4's "only
  `CoordinatorFence`": it carries five methods (`CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, `Health/Check`, `Health/Watch`) at
  `pkg/adapter/holdstate.go:53-59` against `spec/10:56`. That is pre-existing drift rather than this proposal's defect, and SPEC-2's new
  §29.10 hold bullet restates the specification's narrow claim, so it mirrors §10.1.4 rather than widening the drift.
- **The hold has no state-machine mirror and the staged §10.1.4 text adds no apiserver duty.** Neither `spec/06_warm-pod-model.md` nor
  `docs/reference/state-machines.md` names hold state, `coordinator_hold`, or the coordinator hold at all, so SPEC-2's §29.10
  reclassification of the hold as shared by the whole pod has no per-slot-substate mirror to falsify; that closes the one unstaged-site lead
  a Kubernetes or state-machine reading generates. §10.1.4 assigns the terminal transition to the gateway's orphan-session reconciler and
  states that agent pods cannot write `Sandbox.status.phase` (`spec/10:58-59`), and the live staged sentence identifies the local-disk
  post-mortem by what §10.1.4 already says about it, so no pod-side apiserver path is introduced. An earlier draft that called the
  post-mortem "the terminal record" was corrected on exactly this ground in the pass-5 record.
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
  paragraph as unit-neutral does not reach step 9, and it does not reach the paragraph's own equality clause either. Run 5 staged that
  clause (`spec/29:1259-1261`, "the session's `coordination_generation` is the generation the pod last fenced") for deletion under SPEC-2,
  on the ground that the identity it asserts between the row and a pod-held value is what D6's unset arm and SPEC-3's baseline dissolve;
  the paragraph's second sentence (`:1263-1264`) stays a non-site. §28.6's second opener spans four channels whose gates differ, so it takes one clause per
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
  §10.1's no-window claim occurs once in `spec/`, `docs/`, and `schemas/`, at `spec/10:41`; a grep for `no window in which` returns three
  further hits and all three are unrelated credential and token comments (`denylist.go:6`, `issuedtokenstore.go:242`,
  `issuedtokencascade/cascade_test.go:172`), so the deletion strands no mirror. The surface outside `spec/10`, `spec/28`, and `spec/29` is five
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
  carrier; that blanket holds at the stream rather than at the frame, because `CheckpointRequest` carries no `session_id` field and its
  session is named only by the `CheckpointStart` frame inside the `msg` oneof, so do not lean on it for a frame-level claim. Two things look like data-loss leads and are not: `session_checkpoint_meta` is a different table from the `checkpoint_manifest`
  partial-manifest rows §10.1 describes, and the four other `coordination_generation` columns are always written explicitly from the session
  row, so leaving their defaults at 0 is cosmetic. The `docs/` surface states no unit, baseline, or gate. Five sites carry the
  counter token itself (`getting-started/concepts.md:101`, `getting-started/architecture.md:173`, `reference/adapter-contract.md:69`,
  `reference/metrics.md:307`, `:309`) and three more are the barrier, op-lock, and rolling-update lines a wider sweep returns beside them
  (`adapter-contract.md:68`, `:96`, `operator-guide/upgrades.md:47-54`), equally unit-neutral. Corrected in pass 20: the path is
  `docs/getting-started/architecture.md`, there being no `docs/architecture.md`. It has been re-derived eleven times, from the docs,
  security, client, kubernetes, and operational sides, and nothing
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
  `add column` and `drop column`, and 0181 as staged contains neither, so they do not reach it. Corrected in pass 20: the older reason,
  that 0181 drops a constraint, describes a migration the proposal no longer stages; the conclusion is unchanged.
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
  with §4's "either order" claim and the three known-imprecise rationale sentences the residue entry carries. CORRECTED in pass 20: the third decision is still live.
  `coordinationState` does embed its own `mu` as its first field (`pkg/adapter/coordination.go:26`), which is why lens after lens derives
  that CODE-1 settles the decision by construction, but CODE-1's staged sentence enumerates the three data fields (`lastFenced`,
  `initialized`, `quiesced`) and does not say the mutex moves, so both dispositions stay implementable and §4 and §7 are not
  self-contradicting. A fixer touching CODE-1 should say which of the two it means.
- **A converged proposal retires its draft headers.** Only 0075 and 0076, both unconverged, carry `**IMPLEMENTOR TO FILL THE BLANKS.**`; the
  landed 0073 uses `IMPLEMENTOR'S CHOICE:` with a named constraint and carries no fill-the-blanks header over its staged sections. Round 6
  filed the header over `## 5. Proposed changes` on that ground, because SPEC-1 through SPEC-3 beneath it are verbatim staging and a header
  calling them indicative targets tells a maintainer applying tomorrow not to apply them as written.
- **Proposal-format facts. Do not re-derive them and do not invent against them.** The change-proposal format requires a
  `## Deliverable index` in the summary as "the ONLY place a deliverable id resolves". CORRECTED in pass 20: the old instruction not to
  invent one for 0076 is spent, because commit 7229ef0f3 added a `## Deliverable index` table to 0076's summary alongside its hand-written
  `## Open decisions`, one row per deliverable, and two lenses have since confirmed it matches the checklist and §9. Do not remove it and
  do not re-derive the question. The same format requires that every staged deliverable appear in
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
  rows carrying 0 self-heal within one sweep and no backfill is owed there. The CHECK material this bullet used to carry is spent: 0181 no
  longer touches the session row's check, so 0050's inline declaration, the constraint name Postgres derives from it, and the drop-and-
  re-add precedents in 0103, 0063, and 0167 no longer bear on any staged edit. `scripts/lint-migrations.sh` pass 4 keys on `add column`
  only, so it would not have caught a constraint tightening in any case; the deploy-axis check is manual.
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
- **The membership criterion has now been tested against every bullet a sweep flags, and it decides them all the same way.** The four
  `spec/29` "the pod ... rejects a stale one" sentences (`:622`, `:819`, `:1013`, `:1263-1264`) and the three §28.5.1 Preconditions bullets
  that defer to §10.1 without fixing a compared value (`CH-BARRIER` at `:354-357`, `CH-PODHEALTH` at `:389-392`, §28.5.2's at `:447-449`)
  are non-sites, as is §28.5.1's `CH-ATTACH` Preconditions bullet. The §28 sweep is complete; do not re-derive it.
- **The §29.10 bullet removal is safe against every gate that names §29.10.** The three are `tests/spec-map-exceptions.yaml:388` with
  `tests/tier0_static/spec_map_exception_blocker_retention_test.go:65` (a section-level exception row, blocker R7, indifferent to the
  section's sentences), `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:84`, and
  `tests/tier11_docs/successor_pointer_test.go:55`. None string-matches a sentence SPEC-2 rewrites, and
  `tests/tier0_static/spec_map_slot_address_registration_test.go:1486` asserts only that the heading exists. The deleted bullet ("Whether
  the adapter's hold state is partitioned per slot", `spec/29:1523`) has no inbound reference in `spec/`, `docs/`, or `tests/`.
- **No CRD, chart, or `pkg/apis` surface carries the counter in any spelling,** so SPEC-3's baseline has no CRD schema, defaulting-webhook,
  or chart mirror and criterion (d) does not reach those trees. `spec/04` §4.2 states the session record as Postgres-backed, so the
  baseline edit lands on a database row rather than on controller-owned desired state and the §4.6.3 ownership table is not engaged. The
  only Kubernetes-side statements the edits sit beside are §10.1.4's orphan-session reconciler, which keys on the `agent_pod_state` mirror,
  and the whole-pod replacement trigger, which keys on slot failure counts; neither reads the generation.
- **The alert and metric surface is closed and untouched.** `CoordinatorHandoffSlow` is the only coordination alert in the tree, it
  evaluates a p95 of `lenny_coordinator_handoff_duration_seconds_bucket`, and its runbook names no generation, fence, or hold. The metric
  inventory is four rows (`lenny_coordinator_handoff_stale_total`, `lenny_adapter_coordinator_hold`,
  `lenny_coordinator_handoff_duration_seconds`, `lenny_coordinator_fence_relinquished_total`), none of which states a unit for the fenced
  value. `lenny_checkpoint_barrier_ack_total`'s outcome set already carries `error`, so D7 moves a count from `error` to `success` inside
  an existing label set and invents no value; §28.8's fifth column, "Operator observable", needs no edit on any of the three staged rows.
  Its cells, so an operational lens need not open them: `CH-FENCE` names the `coordinator_generation_gap` event and the `coordinator_lost`
  termination, `CH-BARRIER` names `manifest_reason="timeout"`, `lenny_checkpoint_barrier_ack_total`,
  `lenny_checkpoint_barrier_ack_duration_seconds`, and `lenny_prestop_barrier_target_source_total`, and `CH-CHECKPOINT` names the
  `partial = true` manifest row and `lenny_checkpoint_storage_failure_total` (`spec/28:1803` header, `:1805-1808`). Corrected in pass 20:
  an earlier version of this line named `CH-ATTACH`, whose cell is the sender-side one; the conclusion that no cell needs an edit is
  unaffected. SPEC-2 stages the fourth column of three rows and nothing else.
  The `coordinator_connection_lost` carrier in `spec/29` sits in §29.8 step 2 rather than §29.10 and cites §16.1, but §16 never names the
  event, so a grep that follows the citation finds no statement to edit.
- **The rebind question has a spec-side answer: the resume path always claims a replacement pod.** `spec/07:196-197` states
  `resuming → running` as re-attach on a replacement pod, so in specification terms a session cannot re-bind onto the pod that held its
  per-entry value and staged step 3's permissive unset arm is not reopened by a rebind. The code-side half, whether
  `pkg/gateway/sessionserver` placement can ever pick the same pod, is still open and belongs to a gateway-side reviewer.
- **The partial-manifest machinery reads the generation comparatively only,** superseding on `coordination_generation <= $incoming` and
  selecting on `MAX(coordination_generation)` (`spec/10:153`, `:157`, `:171`), so the baseline shift is uniform across every writer and
  reader and changes no outcome. `spec/10:157`'s "the coordinator's fenced generation at intent-row INSERT time" is already imprecise in
  the shipped tree, so it is pre-existing drift rather than a site SPEC-1 makes wrong. §10.1.3's two dual-store sentences (`:47`, `:66`)
  are likewise not falsified by the unset clause: for a never-fenced session the pod rejects nothing, so the stamp "remains valid" and the
  two sentences never meet.
- **`GetCoordinationGeneration()` has exactly two non-test call sites,** `pkg/adapter/coordination.go:92` on the fence path and `:223` on
  the barrier path, which is what D7's "the barrier is the only gateway-to-pod RPC the adapter validates on the generation gate" rests on.
  No operational RPC is gated on the generation in code at all, so step 3's gate is spec-only on `CH-ATTACH` and `CH-CHECKPOINT`.
- **`upsertMirror` runs once per held lease per sweep for every session the replica coordinates,** rather than only on the takeover edge,
  so a never-handed-off session does have a `coordination_lease` mirror row and its barrier carries that row's baseline. This is what makes
  D7's "the ordinary never-handed-off session's barrier then carries the 1 its own row holds" reachable
  (`pkg/gateway/coordination/coordination/coordination.go:370`, `:430`).
- **The gap reset survives the re-scoping intact, and D6's exemption skips no reset the shipped code performs.** Clauses (a) and (b) are
  carried by SPEC-1 with only a session qualifier added, and the four mirrors take the same wording, so no arm of the control is deleted or
  feature-gated. On the exemption: step 1 mints exactly `expected + 1`, so for a session whose first coordinator never fenced the gap
  predicate `new > last + 1` is false on the value even if the exemption were absent, and a genuine multi-step jump requires prior fenced
  generations, which means the value is recorded and the exemption does not apply. D6's stated ground is loosely worded; its conclusion is
  sound. The one interleaving that produces a stale sender against an unfenced session (successor CAS lands, successor's fence exhausts its
  three retries and relinquishes, predecessor keeps sending) is a window §10.1.2 step 2 already sanctions in shipped text.
- **The derive path takes no baseline exception and `spec/07:93` is not a pod-side gate.** A derived session is created through the normal
  create path and inherits no counter (`spec/07:95`), so SPEC-3's "a newly created session row carries `coordination_generation = 1`" needs
  no derive carve-out; `spec/07:215` is a bump on an existing row. `spec/07:93`'s derive-failure CAS is a gateway-and-Postgres-side fence
  on a session row rather than a pod-side gate, and it is the one `current generation` hit outside `spec/28` that costs a full-line read to
  rule out.
- **`coordination_lease` is described in `spec/` only as carrying `session_id`, `coordinator_replica`, and `released_at`** (`spec/10:183`;
  registered as `REG-COORDMIRROR` at `spec/28:138`, "a projection rather than an exclusion primitive"), so staged §10.1.8 step 1's
  provenance sentence names no column the specification contradicts. The CloudEvents `session_terminated` row carries `session_id`,
  `reason`, and `terminatedBy` and no generation, so dropping the pod-level generation reaches no event schema.
- **The staging is write-neutral.** It adds no per-task, per-request, or per-session store write, no watch or informer cache, no hot key,
  and no single-leader serialisation. The baseline changes a value written on an INSERT that already names the column; step 1's
  compare-and-swap and the other two writers are untouched, so the `sessions` write rate is identical before and after. The per-session
  fenced value is adapter process memory bounded by `maxConcurrentSessions`. The fence RPC count does not change, because `coordfence` is
  already per session. The per-session gap reset is strictly less collateral than the shipped pod-wide clause (a). §10.1.8's own failure
  surface is unchanged: the ack-timeout partial-capture path reads committed Postgres state, and the tier-3 burst is sized against the
  barrier-target set, whose size D7 does not change. Three performance passes have returned empty on this staging.
- **Run 4's non-spec fix round landed four findings, and what is absent from its post-fix list was refuted.** Migration 0181's `>= 1`
  tightening is gone from every live site; §8 now amends the landed `TestFenceZeroGenerationFencesAtBaseline` rather than staging a new
  case, and names it as the third baseline-shift site; the tier-4 co-tenant bullet states its preconditions (`sess-a` fenced at 7,
  `sess-b`'s row at 2, the takeover minting 3, and the survivor skipping `sess-a` because replica 1's lease stays live); and the tier-7a
  barrier bullet drops "neither waits on the other" for per-ack content assertions plus the pod-level op lock's serialisation. Read the
  post-fix block before re-filing anything a lens's DECISION line says it filed: a filed finding absent from that list was refuted, and the
  log records no other trace of the refutation.
- **Run 5's spec fix staged the §29.8 Preconditions deletion.** The clause is deleted rather than restated, because the paragraph's
  stale-rejection sentence already carries the pod-side rule and defers to §10.1, which is the non-site form the membership criterion
  blesses, and §29.7's Preconditions is the in-file precedent for a paragraph naming the lease and the mirror and no generation. The
  rejected alternatives were the finding's own two-arm replacement and a one-clause pod-side restatement, both of which mirror a §10.1.2
  rule or a §4.2 baseline into a §29 trace, which is the mechanism that produced the defect. Nothing cascaded: no gate reads §29.8, the
  clause is unique in the tree, both citations on the sentence serve the surviving lease clause, and no numbered step depends on it.
- **Staged §10.1.8 step 1's assembly read is closed.** The provenance sentence names no column, `spec/` never states the mirror's column
  set, and `MirrorTargetLister.Targets` does read `CoordinationGeneration` off each `coordination_lease` row at assembly time
  (`wiring.go:97-122`), so the step's illustrative `SELECT session_id ...` describes which sessions are in the set rather than what the
  dispatcher reads. Two run-5 lenses closed it independently; treat it as weighed and declined rather than reopening it.
- **One replica dispatches barriers for a session at any instant, and a barrier inside the mirror's stale window fails safe.**
  `upsertMirror` is the only writer of the barrier-target set, `coordlease` `ListHeldByReplica` filters on `coordinator_replica`, and
  `barrier.Coordinator.Dispatch` has one production caller (`prestop.go:505`), so two concurrent barriers for the same session cannot
  cross-link a per-entry gate. Inside the window the mirror carries G while the pod holds G+1, so the gate refuses with
  `FailedPrecondition`, `adapterclient` maps it to `ErrGenerationStale`, `dispatchOne` leaves `Acked` false, and prestop's fallback capture
  runs: no capture is lost and the cost is an unquiesced capture, which is that session's behaviour today. That closes the stale-window
  question.
- **Wire population is two sites.** `pkg/gateway/runtime/adapterclient/coordinatorfence.go:53` and `client.go:470` are the only outbound
  gateway sites that set `CoordinationGeneration`, so the proto field is zero on every other adapter RPC. The staged §10.1 sentence is
  therefore a spec obligation the shipped gateway does not meet on most RPCs, and it is not a finding, because shipped §10.1.2 step 3
  (`spec/10:41`) already states the same universal. This settles the older reading that every operational request carries the field.
- **The checklist's step map, verified in three separate rounds.** S1 SPEC-1/2/3, S2 SCHEMA-1, S3 CODE-1 and CODE-3, S4 CODE-4, S5 CODE-2,
  S6 TEST-1; every staged deliverable in exactly one step, one lane per step, the spec step leading, no `Depends-on` naming a later or
  absent step, and no box ticked. The summary's `## Deliverable index` matches it row for row. Do not re-derive.
- **The S3/S4/S5 ordering is coherent on the failure paths.** After S4 alone, with the baseline landed and D7 not yet, a never-handed-off
  session's barrier carries 1 rather than 0, clears the adapter's `gen <= 0` guard (`coordination.go:224-226`), and is refused one guard
  later by the unset arm (`:236`) as `FailedPrecondition`, which prestop still reads as unacked. Nothing loses a capture between S4 and S5,
  and 0 stays impossible as a *fenced* value at every intermediate step, so CODE-3's zero-means-unset sentinel is sound before CODE-4 lands.
- **Moving `coordinationState` onto the entry raises no `go vet` copylocks problem**, `s.slots` being `map[string]*slotState`
  (`pkg/adapter/server.go:379`), and `slotState` has one composite literal in the tree, which is keyed
  (`tracingcontext_sampling_test.go:44`), so adding fields to it breaks nothing.
- **A Helm rollback never undoes a migration.** The migrate Job runs `args: [up]` and is annotated `pre-install,pre-upgrade` only, and
  `grep -rn "pre-rollback" charts/` returns nothing, so a schema change is a one-way door within a release: it ends when every replica runs
  the new binary, or when an operator applies the down migration by hand.
- **An `InvalidArgument` fence is a permanent stall on the resume path.** `coordfence.fence` has no `InvalidArgument` arm: only
  `FailedPrecondition` or `!res.Accepted` reaches `incStale()`, and everything else falls to `default:`, burns `DefaultMaxAttempts = 3`,
  relinquishes the lease, increments `lenny_coordinator_fence_relinquished_total` with no matching stale increment, and returns
  `ErrRelinquished`. The resume path classifies that retryable and parks the row in `awaiting_client_action`; nothing bumps the row, so an
  identical client retry produces an identical relinquish. Only the resume path can send a zero generation after CODE-4, because the
  sweeper's takeover compare-and-swaps the row before it fences.
- **§4.1's escape hatch is real and a reclassification costs two sentences.** The table declares `ShutdownRequest` session-scoped while
  `spec/04:190` says its handler runs the whole-pod scrub, and `:151` says the classification "is declared in the table below rather than
  derived from a message's field set". Reclassifying the fence row therefore has to restate `:151` as well as `:188`, because `:188` says
  the other pod-scoped Adapter messages carry no session field at all, which makes the fence row `:151`'s only Adapter-service example.
- **`scripts/specshift/scope/scope.go` excludes the whole `proposals/` prefix from every specshift pass and gate**, so the N8
  line-citation ratchet does not reach a proposal's own `spec/10:183`-style citations; `tests/registers/line-citations.yaml` is `files: []`,
  a flat prohibition over the files it does reach.
- **Two more landed cases pin arms this change relaxes, and §8's tier-2 target exists.**
  `TestCheckpointBarrierRejectsGenerationMismatch` (`pkg/adapter/coordination_test.go:199-202`) keeps the surviving `gen != fenced`
  refusal and `TestCoordinatorFenceGapDetected` keeps the within-session gap; both are single-session and stay green.
  `tests/tier2_component/stores/sessionstore_test.go:74-81` does bring the store up over a Postgres container with the production
  migrations applied, which is what §8 claims for the `pgstore.Create` floor.
- **Tenant pinning makes co-tenancy same-tenant by construction** (`spec/05:473`, `spec/29:1447-1452`), so the cross-session interference
  this proposal fixes is intra-tenant and tier 9's absence from every tier list is right. `writeHoldPostMortem` writes one record per
  session keyed by that session, so CODE-3's per-session generations leak nothing across sessions.

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
- **WATCHOUT: two of the twelve operational-RPC comments carry a trailing sentence the edit must not swallow.** `AttachRequest`
  (`:995-1001`) closes with "It is carried on every frame of the stream rather than on the opening frame alone, for the same reason
  session_id is." and `CheckpointRequest` (`:1172-1178`) with "It sits outside the `msg` oneof because the fence applies to every frame the
  gateway sends on the stream rather than to the opening frame alone." Neither is part of the generation gate. Replace the span from "A
  pod validates" to the end of the consequence clause on those two rather than the whole comment block.
- **WATCHOUT: the same fill-the-blanks marker string sits over `## 4. Detailed design` and over `## 5. Proposed changes`,** at
  spec-changes.md:107 and :134. A grep-driven fix that deletes both lands the §4 OPEN as a side effect, which the standing context says not
  to merge with the §5 header finding.
- **WATCHOUT: the §28.8 rows are single physical lines with pipe-separated cells,** so a `sed` or `grep` on the row's line number shows
  only the first columns and the "Holder of the exclusivity constraint changes" cell, which is column 4 of the row and field 5 of the
  split, looks absent. Read it with `awk -F'|' '{print $5}'` on the row's line number.
- **WATCHOUT: a late pass number in the spec-changes file is not evidence that the staged spec text changed.** The
  `## Resolved in adversarial review` section lives only in that file, so non-spec-lane rounds append their pass records there too; passes
  20 and 21 are CODE-lane records and pass 22 is the operational-RPC field-comment rewrite. Three consecutive rounds opened with a
  `diff -rq` against the snapshot returning nothing at all, and "read the changed sections first" had no target in any of them. Run the
  `diff -rq` first; it is the cheapest move on this proposal.
- **WATCHOUT: the symmetry objection a verifier raises against the twelve-comment finding.** `spec/10:30` ("if the generation is stale, the
  pod rejects the request ... This prevents split-brain") and `spec/28:237-240` carry consequence clauses of the same kind and were
  adjudicated as standing. Three lenses generated this independently. The distinction that holds: `:30`'s rejection is stated
  conditionally, so it stays true when nothing is stale, while the proto clause asserts outright that a replica "cannot drive the pod", and
  split-brain is only reachable after a takeover, after which the pod holds a recorded value. Do not widen the finding into `spec/10:30`.
- **WATCHOUT: the staged §28.6 second-opener `CH-FENCE` arm says nothing about an equal fence generation,** enumerating older and higher
  only, and equal is reachable: §10.1.2's fence-failure path has the new coordinator retry "with the same generation value" after a lost
  acknowledgement. Two lenses weighed it and declined, on the ground that the shipped sentence it replaces is equally silent and §10.1
  never states the stale-fence rule at all (only the proto response comment does), so the incompleteness is pre-existing across the
  specification. The code-side half of this is now an `### Open`.
- **WATCHOUT: staged step 3's rationale domain claim formally swallows `CoordinatorFence`,** which must be accepted carrying a generation
  above the value the pod holds, while SPEC-2's own §28.6 bullet says one predicate cannot span all four channels. It is not a finding: the
  domain claim is proposal rationale, the applied step-3 text is scoped by "Begin coordination" exactly as the shipped sentence is, and the
  barrier's inclusion is spelled out in the staged clause rather than resting on the domain claim. A later lens that wants it must argue
  the applied sentence.
- **MISTAKE: SPEC-2's proto paragraph treats `CoordinatorFenceResponse` as a repeat of the request comment's record-and-reject rule.** It is
  not. Its two sentences define the `accepted` and `gap_detected` response fields, and `accepted`'s false-condition ("not greater than the
  last fenced generation") is the sentence the per-session move falsifies. Filed as round 3's one finding. The same misdescription is frozen
  in a `## Resolved in adversarial review` pass record at spec-changes.md:664-665, which keeps the words it was written with; only the live
  paragraph at `:487-494` is the edit site, and a grep for `CoordinatorFenceResponse` returns both.
- **WATCHOUT: the two staged §29.10 lists read as contradicting each other and do not.** The staged "Partitioned per slot" addition ("a
  fence for one slot's session neither fences nor unfences another") and the staged "Shared by the whole pod" hold bullet ("a successful
  fence for any one of those sessions exits the hold for the pod") describe two cross-slot effects of the same RPC in two lists whose
  preambles say opposite things about independence. The first is scoped to the recorded generation and the second to the hold, refuted class
  (g) covers the pair, and a later reader will rediscover it. It is not a finding.
- **WATCHOUT: §29.10's "Partitioned per slot" coordination bullet does not end where pass 4's record says it does.** The record quotes it
  as ending at "so each slot's session carries its own lease and its own generation", but the bullet continues for two more lines with the
  cross-reference to the "does not state" list, which SPEC-2's edit instruction correctly keeps. Reading the pass record instead of the
  file makes SPEC-2 look like it deletes that cross-reference.
- **WATCHOUT: the pass-22 replacement clause keeps "A pod validates the generation on every gateway-to-pod RPC",** which the proposal's own
  D7 says is false of the tree, the field being read on the fence and barrier paths alone. Do not file it as an attribution error: the same
  universal is the shipped §10.1.1 sentence (`spec/10:30`) and the shipped §28.6 guard sentence (`spec/28:1672-1673`), so the clause carries
  a spec-versus-code drift that predates this proposal and that SCHEMA-1 is not staged to fix.
- **WATCHOUT: three sentences read as citation defects and are not.** (1) SPEC-1's "Each names the row value the dispatcher copies onto the
  wire (`wiring.go:49`)": on the healthy path the dispatcher copies the mirror value (`wiring.go:104-114`), but the mirror is seeded from
  the row, so the sentence survives and the currency question it raises is the standing OPEN about the barrier's "current" generation.
  (2) SPEC-1 calls the "Generation counters" bullet §10.1's while it lives in §10.1.1, a subsection of §10.1, so the attribution is loose
  rather than false. (3) SPEC-1's "the sentence the adapter's `CheckpointBarrier` gate cites (`coordination.go:228-231`)": the comment
  there cites §10.1.2 as a section rather than step 3 as a sentence. None meets the bar.
- **Weighed and not filed, spec round 3, applicability and reliability. Do not spend a verification on one without new evidence.**
  Applicability: SPEC-1's gloss that SPEC-2 "stages it into §29.10 twice ... and each takes the acceptance sentence above" against SPEC-2's
  actual classification bullets; `spec/10:30`'s unqualified consequence left standing beside the twelve rewritten proto clauses; the staged
  §10.1.8 step-1 provenance sentence against step 1's own `SELECT session_id FROM coordination_lease` literal; and the `coordinator_lost`
  log line as an artifact no section introduces. Reliability: a superseded replica's accepted barrier consuming drain budget (bounded, one
  wall-clock 90s across all pods, and the manifest write is guarded by supersede-on-write plus `partial_manifest_active_uniq`); Postgres
  failover losing the step-1 CAS commit after the replica has stamped and fenced at G+1 (real, pre-existing, and `spec/12:160` already
  files uncommitted writes as at-risk); the hold exiting on one session's fence while co-tenants stay unterminated (the reclaimer exists
  and the fix makes its re-adoption fence succeed); and deleting the gateway fence path's zero floor as removing a fail-safe. CORRECTED in pass 20:
  that last disposition rested on the adapter's non-positive refusal being kept as the fail-closed backstop, which answers whether the
  system fails closed rather than whether a previously succeeding operation still succeeds. The deploy-ordering evidence was added after
  the weighing and reopens it; the item is now a `DEFERRED`.
- **WATCHOUT: SPEC-2's §28.5.1 `CH-FENCE` Exclusivity bullet closes "and SPEC-1 leaves step 2 unchanged",** which is false on its face,
  since SPEC-1 stages step 2's record-and-reject sentence. Weighed and declined: the sentence the rationale relies on is step 2's window
  clause at `spec/10:38`, which is genuinely untouched, and the edit list is unambiguous, so the applied spec is not made wrong. The cheap
  repair is to narrow the clause to "SPEC-1 leaves step 2's window sentence unchanged". Treat as weighed-and-declined rather than
  re-deriving it.
- **WATCHOUT: in a spec-scoped loop the docs lens has almost no filable surface.** A docs page made wrong by the staged edits is fixed in
  the non-spec staging the loop may not edit, and the guardrail bars reconciling the spec toward a doc. What remains in scope is only an
  accepted or deferred failure mode whose outcome lands in no staged spec sentence. The D5 residual is not one: SPEC-2's new §29.10 "Shared
  by the whole pod" bullet states the whole-pod failure, the pod-wide `UNAVAILABLE` rejection, and the hold timeout terminating every
  session the adapter started there.
- **WATCHOUT: a cached lens answer can belong to the other lane, and the cache rule says to return it verbatim.** The key hashes the two
  staged files plus the checklist and carries no lane, so a spec-lane and a non-spec-lane run of the same lens at the same round number
  collide on one path and each clobbers the other. Three shards hit this in one round; two of them would have re-filed findings already on
  their own refuted list had they followed the rule. Read a hit's own `coverage` field first and treat an answer whose coverage does not
  name your lane's staging file as a collision rather than as your own work.
- **MISTAKE: §8's tier-4 "skipping `sess-a` on `ErrHeld`" attribution, worked up independently by three agents and filable by none.** For a
  session whose lease a live foreign replica holds, `adoptable` is false and `eligible` is false, so the sweep `continue`s at the
  eligibility gate before any `Acquire` and the `ErrHeld` arm at `coordination.go:341` is never reached. It is not a defect in the bullet:
  `Sweep`'s own doc comment and the landed tier-4 case's comment use the same vocabulary, and the case's construction and outcome are
  identical either way. Read this entry before working it up again.
- **WATCHOUT: `releaseSessionSlot` is a third caller of `deregisterSlotLocked`** (`pkg/adapter/slotsession.go:214-215`, reached from
  `resume.go`, `session.go`, and `sdkwarm.go`), so CODE-1's "both deregistration paths" is not the full caller set of the helper. The
  claim is still right for the mid-flight case, because every `releaseSessionSlot` site is a failed-start or failed-resume compensation for
  a session that was never started, so no barrier or `Checkpoint` stream can be in flight for it. Check the call sites rather than
  re-deriving this.
- **MISTAKE: round 4 disposed of §29.8's Preconditions paragraph as unit-neutral and the equality clause survived four rounds.**
  Unit-neutrality is true of the paragraph and does not reach the defect, which is by the unset arm and the row baseline rather than by the
  unit. The completed non-site sweep named the paragraph's second sentence (`:1263-1264`) and never separately classified the clause at
  `:1261`. A paragraph-level disposition does not cover every sentence in the paragraph.
- **WATCHOUT: the run-5 §29.8 bullet carries two known imprecisions that are not findings.** It calls the stale-rejection sentence "the
  paragraph's next sentence" where the `replica B` sentence sits between them, and it quotes the clause to delete without its leading
  ", and", so a literal deletion would leave a dangling comma. Both are recorded in the fix and post-fix shards, the bullet names the
  sentence by content as well as by position, and its "the lease clause and its two citations are unchanged" fixes the intended result. Do
  not re-derive either as a finding and do not edit the frozen shards to correct the ordinal.
- **MISTAKE: a two-part MISTAKE can be half-applied.** The fix answering "the residue is outside the class" landed the class-2 paragraph
  for `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` and left `TestFenceZeroGenerationFencesAtBaseline` unstaged for a
  further round. That case seeds through `genReader{gens: []int64{0}}` rather than a `CoordinationGeneration:` literal, so a sweep over
  seeded constants structurally cannot find it: any future sweep for baseline fallout must grep the fake readers too.
- **WATCHOUT: `TestCheckpointBarrierRejectsWithoutFence` amended naively does not go red, it hangs.** It calls `CheckpointBarrier` on
  `context.Background()` with no deadline; under D7 the unset arm accepts and the RPC blocks in `select { case <-done: case <-ctx.Done() }`
  with nothing to link or complete, so the tier-1 package dies on the Go test timeout. The replacement assertion must carry the
  goroutine, wait-for-gate, drive-the-gate pattern rather than only flipping the expected code. The intended assertion exists only inside a
  frozen pass record (`spec-changes.md:868-870`), which cannot stand in for §8's live text.
- **WATCHOUT: reading the proto.** `sed -n '969,973p;1618,1622p'` prints in file order rather than argument order and silently mis-pairs a
  range with its message; `awk '/^message /{m=$2} /coordination_generation *=/{print NR": "m}'` binds every field to its declaring message
  in one call. A message's doc comment sits above the `message` line. `ResumeRequest.recovery_generation`'s comment ("Zero when the gateway
  has not recorded a generation for the session") is the recovery counter and is not a fifteenth carrier.
- **WATCHOUT: a wrapped spec sentence does not grep.** Every §28 and §29 anchor the staging quotes spans two or three physical lines, so
  `grep -n "<quoted sentence>"` returns nothing and reads as a dead anchor. Match with `\s+` joined between the words rather than escaping
  the whole pattern, since `re.escape` on Python 3.9 escapes the spaces and the substitution then matches nothing, or `sed` the range. Four
  anchors read as missing to one lens before it noticed.
- **WATCHOUT: harness hazards on this proposal.** Bash resets cwd between calls, so a `cd` does not persist and repo-relative paths run
  from the repo root anyway. `cat`ing the non-spec-changes file through Bash persists the output to a tool-result file instead of showing
  it; read it with `sed -n 'A,Bp'` or the Read tool. `.claude/rules/*` are loaded into every agent's context and the repo `CLAUDE.md` is a
  stub, so do not hunt for build or test commands there. An empty `diff -rq` against the snapshot is the ordinary case here rather than a
  tool failure, and a large diff is usually this log's own compaction pass: one round's 1,913-line diff was 1,870 lines of it.
- **Lens exhaustion, counted.** On this staging the docs-alignment lens has returned empty five times, performance four, security three,
  reliability three, and edit-sites twice, each over byte-identical text and each after an independent re-derivation that confirmed the
  standing inventories. A further run of any of them buys nothing unless SPEC-1's step-3 wording, the §28.6 second-opener clause, D7's
  acceptance arm, or §8 is rewritten.
- **MISTAKE: the §28.8 `CH-BARRIER` disposition clause was copied from the `CH-CHECKPOINT` bullet above it,** where it is true. The
  `CH-CHECKPOINT` cell states the constraint in one sentence and the edited rejection rule in a second; the `CH-BARRIER` cell has only two
  sentences and the replaced clause is the trailing clause of the constraint sentence itself, so "the cell's constraint sentence ...
  unchanged" tells an applier to leave the clause standing.
- **MISTAKE (avoided, twice): "Neither file states step 3's acceptance rule today."** SPEC-2 stages §28.6's second-opener first sentence
  and the two §28.8 cells precisely because they are pod-side rejection rules that fix the compared value, which are statements of step 3's
  rule in `spec/28`. The sentence is rationale landing nowhere in `spec/` and it hides no missed site, since SPEC-2 edits those sentences
  anyway. Do not re-file without naming a site it hides.
- **WATCHOUT: the rebind reset is not exploitable.** `ensureSlotStateLocked` does recreate a `slotState` for a slot id after
  `deregisterSlotLocked` removed it (`pkg/adapter/slot.go:82-102`), so a per-entry `lastFenced` genuinely resets where the pod-wide one did
  not. A fence issues only after a successful §10.1.2 step-1 compare-and-swap, so the exemption a reset grants admits only a CAS winner,
  which is refuted class (b)'s reasoning. It cost one lens a detour; do not spend another.
- **WATCHOUT: §29.10's "does not state" list already carries bullets that assert positive facts before naming the gap** (the `Interrupt`
  bullet states the operation-lock serialisation and §7.2's slot qualification first), so SPEC-2's narrowed barrier and `Interrupt` bullet
  does not breach the list's own preamble. A round tempted to file that as a self-contradiction should stop here.
- **WATCHOUT: D7's rationale reads two ways and only one is right.** "The `!initialized` arm stays reachable for a session handed off whose
  successor's fence the pod has not yet recorded" means the unset *state* stays reachable, not the unset *refusal*: CODE-2's gate becomes
  `initialized && gen != fenced`, and `non-spec-changes.md:94` states the predicate unambiguously, so no implementor can build the wrong
  gate from it. Two lenses adjudicated it; it is not a finding.
- **WATCHOUT: two pre-existing observability drifts sit beside this surface and are not made wrong by it.** `spec/16:552` points operators
  at `lenny_coordinator_fence_retry_total`, which appears in no §16.1 inventory row and no metric catalog, and `docs/reference/metrics.md:196`
  states the barrier-ack outcome set as `success, timeout, error` where `spec/16:41` has `partial_captured` as well, with
  `docs/operator-guide/upgrades.md:52` repeating the docs list. Both predate the staging and belong to a docs loop.
- **WATCHOUT: the docs pages a lens is most tempted to file are all pre-existing drift.** `docs/reference/adapter-contract.md:68` says the
  adapter "flushes a best-effort checkpoint" where the gateway drives the stream, `:69` calls `CoordinatorFence` a "precondition for any
  subsequent operational RPC" which is already loose against the shipped pod-wide `initialized` guard, `:79` claims a per-session operation
  lock where the shipped lock is pod-level, and `docs/runbooks/coordinator-handoff-slow.md:28` defines the coordinator handoff as the
  parent-to-child delegation rather than the §10.1 replica handoff. None is touched by this proposal and the guardrail bars reconciling the
  spec toward a doc.

### Open

Detail for each item is in the ledger entry named at the end of its line. The `[non-spec.5.*]` and `[non-spec.6.*]` entries were retired
in compaction pass 17; the items they carried are in the ledger residue entry `[non-spec.5-6.*]`, filed there under the id named here.
The `[spec.1.review-*]` entries were retired the same way in compaction pass 18, and their two unclosed items are in the ledger residue
entry `[spec.1.*]`. The sixteen `[spec.2.review-*]` and `[spec.3.review-*]` entries were retired in compaction pass 19, and their two
unclosed items are in the ledger residue entry `[spec.2-3.*]`. Compaction pass 20 retired no ledger entry, the round boundary archiving
the whole ledger instead, so the `[non-spec.1.*]`, `[non-spec.2.*]`, `[non-spec.3.*]`, and `[spec.1.review-*]` ids below resolve in that
archive rather than in this file. Two id collisions to expect there: several `[non-spec.1.review-*]` ids name two different entries, and
`[spec.1.*]` is both run 4's spec-round-1 residue entry and the stem of run 5's spec-round-1 lens ids.

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
  and `[spec.1.*]` asks whether the staged edits must adjudicate it.
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
- **§8's tier-1 disposition for `TestCheckpointBarrierRejectsWithoutFence` states no replacement assertion.**
  `[non-spec.5.review-test-coverage.1]`, carried forward by `[non-spec.6.review-test-coverage.1]`
- **Three weighed-and-not-filed items:** the fence and barrier resolve that misses, §8's tier gloss, and the tier-2 case with no named home.
  `[non-spec.6.review-mechanism.1]`
- **Whether §10.1.8 step 3 fixes the unit of barrier quiescence at the session,** which the design rests CODE-1's per-entry `barrierGate` on
  while SPEC-2 keeps §29.10's clause unanswered. `[spec.1.*]`
- **UNVERIFIED: whether a fence retried at the same generation is accepted.** §10.1.2 step 2 has the new coordinator retry with the same
  value after a lost acknowledgement, and in specification terms equal is neither older nor a gap, so the retry is idempotent; the shipped
  adapter guard is `gen <= lastFenced` and refuses it with `coordinator_handoff_stale`. Two round-1 lenses stated the two halves and
  neither reconciled them. `[spec.1.*]`
- **§29.10's quiescence-unit clause admits two remedies** with different consequences, and the round that found it picked neither.
  `[spec.2-3.*]`
- **Barrier for an unbound session.** No test in the tree asserts that `CheckpointBarrier` for a session with no slot binding is refused,
  and D7 leaves `checkSessionBound` the sole fail-closed guard on that path; filed in run 4's non-spec round 1 and not landed.
  `[non-spec.1.review-security.1]`
- **UNVERIFIED: whether CODE-3's post-mortem read of the detached `*slotState` takes the entry's `coordinationState.mu`.** A fence that
  passed `checkSessionBound` before pass 1 can still be writing that field; use a locked read. `[non-spec.1.review-security.1]`
- **UNVERIFIED: the rolling-window zero-row population.** Nobody has written down what happens to rows minted at 0 after the roll
  completes. `[spec.1.review-security.1]`
- **UNVERIFIED: whether the tree has a worked precedent for a two-release constraint tightening.** `pkg/schemamigrate` carries the phase
  machinery; nobody checked whether a landed migration uses it. `[non-spec.1.review-feasibility.1]`
- **Whether a later release adds the `>= 1` check as a genuine Phase-3 migration.** Defensible defence-in-depth, costs a separate
  proposal, and nothing in 0076 depends on it. `[non-spec.1.fix-design-G1.1]`
- **The `CoordinatorFenceResponse` prescription is live and has been declined by four lenses.** SPEC-2 has the comment take the §28.5.1
  Messages wording while its two sentences define `accepted` and `gap_detected`, so applied verbatim it overwrites two field definitions;
  close it inside the SCHEMA-1 qualifier item rather than as a fresh finding. `[spec.1.review-fresh.1]`
- **OD3's unwritten knock-on.** Reclassifying §4.1's fence row also has to restate `spec/04:151`, whose only Adapter-service example it is;
  OD3 names neither `:151` nor `:188`. `[spec.1.review-client-surface.1]`
- **UNVERIFIED: whether the summary's SPEC-2 deliverable row should name the §29.8 Preconditions deletion.** It already omits the §29.8
  step-2 edit, so the row is loose by precedent. `[spec.1.fix-design-G1.1]`
- **UNVERIFIED: whether the tier-4 co-tenant case can be driven through `coordfixture.FenceReadopter`** once `sess-a` is fenced
  explicitly; nobody has written the sequence out. `[non-spec.1.review-mechanism.1]`
- **`TestFenceZeroGenerationFencesAtBaseline`'s amended form asserts a wire value production always rejects.** That is what §8 intends, and
  it is worth a human's eye at sign-off that the tree's only coordfence baseline case ends there. `[non-spec.3.review-feasibility.1]`
- **The coordfence floor deletion against rows minted at 0 during the roll** was filed by two of run 4's non-spec round-2 lenses and no
  post-fix record in this window says it landed; it is also now a `DEFERRED`. `[non-spec.2.review-performance.1]`,
  `[non-spec.2.review-reliability.1]`
- **Whether an exhausted lens is retired.** The docs-alignment and test-coverage lenses have each returned empty over byte-identical text
  more than once and both say retiring them on an empty return costs nothing. `[non-spec.1.review-docs-alignment.5]`,
  `[non-spec.3.review-test-coverage.1]`

### Deferred

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
  for the new §28.5.1 sentence. CORRECTED in pass 20, twice over, and the correction is what a later worker needs: the remedy cannot be
  applied in the file this entry names. `tests/claim-map.json` is generator output from root `gateway-runtime-comms.md` §7.1 and
  `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs the generator and byte-diffs it at tier 0, so a hand-added or restatused row
  turns tier 0 red rather than closing the deferral; the edit site is the generator's source document plus a regeneration. The proposed
  status is wrong as well: `ABSENT` is defined as "specified and not implemented" (`spec/28:163-165`) and both the `CoordinatorFence` and
  `CheckpointBarrier` rows are genuinely `WIRED` before and after this change, since what the S1-to-S5 window creates is a partially
  rescoped mechanism, which §28.4's three-value status set does not model. Anyone reviving this must argue that the obligation is on the
  statement rather than on the mechanism, and must name the generator-source edit as the remedy.
- DEFERRED [`spec-changes.md`, from `[non-spec.1.fix-G1.1]`]: the Pass 15 residue paragraph at about `:1501` and the Pass 4 line at about
  `:1110` describe migration 0181 as carrying the `>= 1` check tightening, and the tier-2 migration case as asserting it. Both sit inside
  dated `### Pass` records under `## Resolved in adversarial review`, which the design ruled not-a-site and that loop may not edit. What is
  true now: 0181 carries the `DEFAULT 1` on both columns and the backfill only, and its tier-2 case asserts the backfill, both defaults,
  the retained `>= 0` check, and a `.down.sql` that restores `DEFAULT 0`. Lifted here in compaction pass 18 because its subject is live and
  the entry it sits in is not yet aged out.
- DEFERRED [`spec-changes.md`, the Pass 21 record at about `:1812`, from `[non-spec.1.fix-G2.1]`]: that record restates the tier-7a
  classification as "two co-tenant sessions' RPCs do not interfere" and calls both tier-7a bullets that kind. The classification is now
  wrong for the barrier bullet, whose acks do serialise behind the pod-level op lock. The live statement is `non-spec-changes.md`'s
  tier-split sentence, which run 4's fix round corrected to "each of two co-tenant sessions' concurrent RPCs records and returns its own
  state"; the Pass record sits inside `## Resolved in adversarial review`, keeps the words it was written with, and was deliberately not
  edited.
- DEFERRED [`pkg/adapter/holdstate.go`, from `[non-spec.3.review-feasibility.1]`]: `enterHoldState`'s doc comment at `:116-118` reads
  "Read the generation and the started-session count through their accessors (which take coord.mu and s.mu) before locking hold.mu so no
  two locks are ever held together". CODE-3 deletes the generation read, so the clause naming it is false once the deliverable lands, and
  CODE-3's enumeration of the sites the `gen` field drags (`:43`, `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:283`) does not
  include this comment. Not filed: refuted class (k) bars a missed-edit-site finding over a comment under `pkg/`. What is true instead:
  only the started-session count is read through an accessor before `hold.mu`. This is the second instance of the comment residue at
  `pkg/adapter/coordination.go:126-128`; hand both to the implementor together.
- DEFERRED [`non-spec-changes.md` / CODE-4, and secondarily `spec-changes.md:250-252` and `:284-286`, from
  `[spec.1.review-performance.1]`]: deleting `coordfence`'s non-positive floor is not safe, and the ground the proposal gives for deleting
  it is false. What is claimed: "CODE-4 deletes that floor, because a session row can no longer carry a non-positive value" and "the row
  value it guards against can no longer exist". What is true instead: the migrate Job is a `pre-install,pre-upgrade` hook that completes
  before the gateway Deployment rolls (`charts/lenny/templates/migrate-job.yaml:10-16`), and no production create path sets the field
  (`pgstore.Create` binds `sess.CoordinationGeneration` straight through at `pgstore.go:177`, `:260`), so every session the still-running
  old fleet inserts during the rolling window carries an explicit 0 that 0181's one-shot backfill has already run past; the proposal's own
  non-spec staging says exactly this. Consequence, which nothing has written down: after the floor is deleted, `fenceResumedPod` reads that
  0 (`cmd/lenny-gateway/main.go:375-381`), sends it, the adapter refuses with `InvalidArgument`, which falls into `coordfence.fence`'s
  `default:` transient arm rather than the stale arm, burns all three attempts, relinquishes the lease, and aborts the resume, where the
  shipped floor fenced at 1 and the resume succeeded. The sweeper path self-heals, `RecordHandoff` bumping 0 to 1 before it fences; the
  client-driven resume path does not, because it fences without bumping. Remedy: keep the floor until the release that also tightens the
  CHECK to `>= 1` under §10.5's Phase 3, which is the same deploy argument the proposal already accepted for the CHECK.

## Ledger

### [spec.2.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE the staged spec text is byte-identical to run 5 round 1's
(`diff -rq` against `scratchpad/cp-snap/0076-run5/spec-r2` reports ONLY the review log as differing), the one
mechanism finding that round raised (§29.8 Preconditions) is landed, and every candidate I generated
independently was already adjudicated in `### Settled`, `### Traps`, or the refuted-class list —
ALTERNATIVES: I worked up and rejected six candidates, each named below with the ground that killed it, so a
later mechanism lens does not re-spend the round on them.

FACT: `diff -rq snapshot proposal` returned exactly one differing file (the review log). On this proposal the
cheapest first move is that diff plus `wc -l` on the two staged files; when the staged text has not moved, the
standing context's "Lens exhaustion, counted" bullet applies verbatim and the honest answer is empty —
EVIDENCE: proposals/.../0076_....spec-changes.md is 599 lines and non-spec-changes.md 419, matching the
post-archive sizes the round boundary predicted.

FACT: the six candidates and their refutations, all re-derived from primary sources this run.
(1) Step 3's "accepts only" against its own unset carve-out: the carve-out is the immediately following
sentence, so the paragraph reads as rule-then-exception; standing `### Open` already carries it as an
UNVERIFIED and it is below the bar. (2) Step 3's third sentence ("the pod holds one generation for that
session at a time") against the second sentence's unset arm: sentence 3 is scoped by "Because fence
confirmation is required before this step is reached", so its population is the post-fence session and the two
do not collide. (3) §28.6's "The constraint excludes a second replica" left standing while the staged
second-opener sentence says the pod rejects none of an unfenced session's RPCs on generation grounds: this is
refuted class (e). The proposal's stated ground (the lease alone excludes) is loose, but the mechanism holds
for a different reason — §10.1.2 step 2 bars the acquiring replica from sending any operational RPC until its
own fence is acknowledged, so the unset window always closes with a fence before a second sender's first RPC,
and §10.1.1's "prevents split-brain even under lease/lock race conditions" survives SPEC-1 unedited.
(4) §28.5.1's `CH-BARRIER` Preconditions bullet ("The generation stamp and the fence acknowledgement that
govern every gateway-to-pod RPC", spec/28:354-357) as falsified by D7's acceptance arm: already recorded as a
MISTAKE-avoided in `[spec.1.review-mechanism.1]`; it is sender-side and pre-existing (refuted class (a)).
This closes the standing `UNVERIFIED: §28.5.1's CH-BARRIER Preconditions bullet, sender-side or pod-side` —
it is sender-side and therefore a non-site under SPEC-2's own criterion. (5) `spec/28:389-392`
(`CH-PODHEALTH` Preconditions) says §10.1 "does not state how that rule applies to a probe against a pod that
is not yet serving a coordinated session"; staged step 3's unset clause does not answer it, because the clause
is keyed on "the session the RPC names" and a health probe names none, so the "does not state" claim survives
and the bullet is not an edit site. (6) `spec/10:157`'s partial-manifest field gloss ("`coordination_generation`
(the coordinator's fenced generation at intent-row INSERT time)") reads as falsified by D6's unset arm, but the
value written is the row's rather than a pod-held one, the gloss is already loose in the shipped text, and the
`MAX(coordination_generation)` resume selector is baseline-neutral.

FACT: proto anchor arithmetic in SPEC-2's closing paragraphs is exact and I re-derived it by counting from
`schemas/lenny-adapter.proto:1440`: `CoordinatorFenceRequest` message comment `:1442-1446`, its field comment
`:1449-1451`, `CoordinatorFenceResponse` comment `:1455-1462`, `CheckpointBarrierRequest` comment `:1469-1474`,
its field comment `:1477-1479`, and D7's `:1475-1483` for "session id, generation, barrier id, no replica
identity". Do not re-derive.

FACT: every code citation in §7's three open decisions resolves. `fenceResumedPod` call sites at
pkg/gateway/sessionserver/start.go:3975 and :4067, the function at :4233, `s.fencer.Fence` at :4237;
`coordinationReadopter.ReadoptAndFence` at cmd/lenny-gateway/coordination_seams.go:155, `fencer.Fence` at :233;
`s.readoptAndFence` at pkg/gateway/coordination/coordination/coordination.go:399 and the pre-bump
`upsertMirror(..., row.CoordinationGeneration)` at :430.

USEFUL [standing context, "Refuted classes ... (e) Step 3's unset clause permits what step 2 forbids"]: this is
the entry that kills candidate (3) above, which is the strongest mechanism objection the staged §28.6 edit
attracts. Keep it; two lenses have now reached it independently.
USEFUL [standing context, "WATCHOUT: a late pass number in the spec-changes file is not evidence that the
staged spec text changed" / the `diff -rq` instruction]: it is what let me scope this run correctly in one call.
USEFUL [`[spec.1.review-mechanism.1]`, its two MISTAKE-avoided entries]: saved me filing candidates (4) and the
§29.10 "does not state" preamble objection.

WATCHOUT: `sed -n '120,400p'` on this review log exceeds the Bash output cap and is persisted to a tool-result
file whose own `sed`/`Read` then reports a shorter length than the source range, which reads as truncation and
is not. Read the review log with the Read tool at explicit offsets against the real file instead.

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

## Index and checklist reconciliation, second pass (2026-09-02, automated)

Re-derived `## Deliverable index` from the converged staging. The staged deliverable set is SPEC-1, SPEC-2,
SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3, CODE-4, and TEST-1, and no other identifier appears in either
staging file. Each already carries one index row naming the file it lands in and one line of scope, and each
row agrees with the section that stages it, including CODE-4's row after pass 23 dropped the `>= 1` check
tightening. No row was added, removed, renumbered, or reworded.

The checklist's leading spec block names the current SPEC ids. S1 lands SPEC-1, SPEC-2, and SPEC-3 in one
step and is the only spec-lane step, so the block is the whole spec prefix and no code step is interleaved
before a remaining spec step. Every step names one lane and one lane only, every staged deliverable appears
in exactly one step, and each step's declared tiers match the tiers its deliverables declare. Each
`Depends on` resolves to S1 and to the code steps its work reads from. The checklist needed no rewrite.

CORRECTS [`## Index and checklist reconciliation (2026-09-02, automated)`, its closing `OPEN` line, and
`### Deferred`'s `DEFERRED [review-log.md ...]` from `[non-spec.1.fix-design-G1.1]`]: that `OPEN` line states
that CODE-4's migration 0181 tightens the session row's check to `>= 1` and that the remedy is either a
§10.5 phase split or a stated exemption. Both halves are false against the current staging. Pass 23 of the
non-spec lane deleted the tightening, so 0181 carries the `DEFAULT 1` on both columns and the backfill alone
and leaves `0050`'s `CHECK (coordination_generation >= 0)` in place, and the two `Create` floors are the
whole enforcement. The third remedy `[non-spec.1.fix-design-G1.1]` chose is the one that landed, and the
deferral it filed against this log asked for exactly that closure. Compaction pass 18 had already retired
the same item from `### Settled` and `### Deferred`; the reconciliation pass that followed it restated the
retired framing. The item is closed. Nothing in `non-spec-changes.md`, the summary, or the checklist states
the tightening, and S4's tier list stands as one step.

CORRECTS [`### Deferred`, `DEFERRED [non-spec-changes.md, from [spec.3.review-citations.1]]`]: verified
closed rather than re-applied. SCHEMA-1 now records
`CoordinatorFenceRequest.coordination_generation` as the one carrier that takes no edit, with SPEC-2's
reason, and it no longer lists that comment among the carriers that take the wording SPEC-2 states. The
entry is retired.

OPEN [`### Deferred`, `DEFERRED [tests/claim-map.json, from [spec.3.review-edit-sites.1]]`]: SPEC-2 stages
§28.5.1, §28.6, and §28.8 statements that do not hold in the shipped adapter until CODE-1 and CODE-2 land,
and §28.4 obliges a claim-register row to carry a status naming the step that closes it. It lands in
`tests/claim-map.json`, which this pass may not edit and which no deliverable stages, so it needs a staged
registry deliverable that does not exist yet. Two facts the next round should not re-derive:
`[non-spec.1.review-edit-sites.1]` and `[non-spec.1.review-feasibility.1]` establish that the
`CoordinatorFence` and `CheckpointBarrier` rows are already present and `WIRED`, so §28.4's obligation to
carry a row is met and the subject is row status, and that the register is generated from the root
`gateway-runtime-comms.md` §7.1 by `scripts/seed-claim-register.py` under a byte-diff gate, so no status
can be changed by editing `tests/claim-map.json` directly. A deliverable closing this lands in the root
planning document and in the regenerated register.

OPEN [`### Deferred`, `DEFERRED [spec-changes.md, from [non-spec.1.fix-G1.1]]`]: the Pass 15 residue
paragraph at about `spec-changes.md:1501` and the Pass 4 line at about `:1110` describe migration 0181 as
carrying the `>= 1` check tightening and its tier-2 case as asserting it, which pass 23 made false. It lands
in `spec-changes.md`, which this pass may not edit, and both sites sit inside dated `### Pass` records under
`## Resolved in adversarial review`, which the design ruled not-a-site. What is true now is that 0181
carries the `DEFAULT 1` on both columns and the backfill alone, and its tier-2 case asserts the backfill,
both defaults, the retained `>= 0` check, and a `.down.sql` that restores `DEFAULT 0`.

OPEN [`[non-spec.1.fix-G2.1]`, `DEFERRED [spec-changes.md, the Pass 21 record around :1812]`]: that record
restates the tier-7a classification as "two co-tenant sessions' RPCs do not interfere" and applies it to
both tier-7a bullets. It is false for the barrier bullet, whose two acks serialise behind the pod-level op
lock. It lands in `spec-changes.md`, which this pass may not edit, and the record is frozen pass narrative.
The live statement is the tier-split sentence in `non-spec-changes.md` §8, which states that each of two
co-tenant sessions' concurrent RPCs records and returns its own state, and it is already correct. This
deferral was never lifted into `### Deferred`; the next loop's first round should read it here.

## Index and checklist reconciliation, third pass (2026-09-02, automated)

Re-derived the deliverable index and the checklist from the two staging files. The staged set is SPEC-1,
SPEC-2, SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3, CODE-4, and TEST-1, each named in exactly one step, and no
step names a deliverable neither staging file stages. Every `Depends on` resolves to an earlier step, S1 is
the whole spec prefix, and applying S1 through S6 in order hits no forward reference. Three corrections
landed.

CORRECTS [the checklist, S5]: S5 declared tiers 0, 1, 3, and 7a while CODE-2's own tier reach includes 4.
§8 states the tier-3 wire case D7 stages and then "Tier 4 covers the same flow across the gateway, the
session store, and the pod", and that flow is the accepted barrier, which needs CODE-2's gate as well as
CODE-4's baseline: at S4 the baseline is landed and the shipped `!initialized` arm still refuses the
barrier, so the tier-4 acceptance case can only be green from the step that lands CODE-2. S5's tier list
takes 4, and its line states the acceptance half of CODE-2 so that its dependency on S4 reads from the step
itself. No other step's tier list or dependency moves.

CORRECTS [the summary, `**What changes.**`]: D6 is a settled decision with a wire-visible consequence, the
exemption's unit moving from the pod's lifetime to the session's binding on the pod, and the paragraph
stated only that gap detection reads the per-session value. It now states D6 and names SCHEMA-1 as the
carrier of the new unit.

CORRECTS [the summary, `## Deliverable index`, SPEC-1 and SPEC-2]: SPEC-1's row attributed D7's acceptance
rule to §10.1.8 step 1, while SPEC-1 stages the predicate once in §10.1.2 step 3 and has §10.1.8 apply it by
reference. SPEC-2's row named the `CH-FENCE` and `CH-BARRIER` mirrors and omitted the §28.8 `CH-CHECKPOINT`
cell and §29.10's co-tenancy classification, both of which SPEC-2 stages. Both rows now state what their
deliverable stages. No row was added, removed, or renumbered.

## Index and checklist reconciliation, fourth pass (2026-09-03, automated)

Reconciled against the staging as it now stands, which the spec loop left unconverged with findings open in
`### Open`. The staged deliverable set is SPEC-1, SPEC-2, SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3, CODE-4,
and TEST-1, and a scan of both staging files for deliverable identifiers returns those and no other. Each
already carries one `## Deliverable index` row naming the file it lands in and one line of scope, and each row
agrees with the section that stages it, including SPEC-2's row after the §29.8 Preconditions deletion the last
staging edit added, which the row's "§29 mirrors" clause already covers. No row was added, removed,
renumbered, or reworded.

The checklist's leading spec block names the current SPEC ids. S1 is the only spec-lane step and lands SPEC-1,
SPEC-2, and SPEC-3 together; the bundle was rechecked against the one-deliverable-per-step preference and
kept, on the ground the summary's "Watch out for" paragraph states, which is that landing SPEC-1 alone leaves
`spec/28` and `spec/29` restating the pod-wide rule while citing a `spec/10` that contradicts them and no
tier-0 or tier-11 gate catches it. Every step names one lane and one lane only, every staged deliverable
appears in exactly one step, no step names a deliverable neither staging file stages, each step's declared
tiers match the tiers its deliverables declare, and every `Depends on` resolves to an earlier step: S2, S3, and
S4 on S1, S5 on S1, S3, and S4, and S6 on S3, S4, and S5. Applying S1 through S6 in order hits no forward
reference. The checklist needed no rewrite.

CORRECTS [`### Deferred`, `DEFERRED [spec-changes.md, from [non-spec.1.fix-G1.1]]`]: verified moot rather than
re-applied. The entry names the Pass 15 residue paragraph at about `spec-changes.md:1501` and the Pass 4 line
at about `:1110`, both of which describe migration 0181 as carrying the `>= 1` check tightening. The staging
edit that followed the third pass deleted `## Resolved in adversarial review` and every `### Pass` record
under it from `spec-changes.md`, which is now 599 lines and contains neither `0181` nor `>= 1` nor a pass
record. The sites the entry names no longer exist, so nothing remains to repair and the entry is retired
rather than carried forward as an errand no lane can run.

CORRECTS [`### Deferred`, `DEFERRED [spec-changes.md, the Pass 21 record around :1812, from
[non-spec.1.fix-G2.1]]`]: verified moot on the same ground. The Pass 21 record and its tier-7a classification
went with the rest of `## Resolved in adversarial review`. The live statement is the tier-split sentence in
`non-spec-changes.md` §8, which states that each of two co-tenant sessions' concurrent RPCs records and
returns its own state, and which is already correct. The entry is retired.

OPEN [`### Deferred`, `DEFERRED [tests/claim-map.json, from [spec.3.review-edit-sites.1]]`]: unchanged and
carried forward for the third time. SPEC-2 stages §28.5.1, §28.6, and §28.8 statements that do not hold in the
shipped adapter until CODE-1 and CODE-2 land, and §28.4 obliges a claim-register row that is not `WIRED` to
name the step that closes it. It lands in `tests/claim-map.json`, which this pass may not edit, and closing it
needs a staged registry deliverable that does not exist in either staging file. Two facts the next round
should not re-derive: the `CoordinatorFence` and `CheckpointBarrier` rows are already present and `WIRED`, so
the subject is row status; and the register is generated from the root `gateway-runtime-comms.md` §7.1 by
`scripts/seed-claim-register.py` under a tier-0 byte-diff gate, so the edit site is that source document plus a
regeneration. The reviewer now sees the same question as OD11 in the summary.

OPEN [`### Deferred`, `DEFERRED [pkg/adapter/holdstate.go, from [non-spec.3.review-feasibility.1]]`]: carried
forward, and recorded here for the first time, since no earlier reconciliation pass named it. `enterHoldState`'s
doc comment at `:116-118` states that the generation and the started-session count are both read through their
accessors before `hold.mu` is taken, and CODE-3 deletes the generation read, so the clause naming it is false
once the deliverable lands. It lands in `pkg/adapter/holdstate.go`, which this pass may not edit, and closing it
in the proposal would mean adding an edit site to CODE-3's enumeration in `non-spec-changes.md`, which is
staged code content no non-spec lens has read. The same residue exists at
`pkg/adapter/coordination.go:126-128`; hand both to the implementor together.

OPEN [`### Deferred`, `DEFERRED [non-spec-changes.md / CODE-4, and secondarily spec-changes.md:250-252 and
:284-286, from [spec.1.review-performance.1]]`]: carried forward, and recorded here for the first time.
Deleting `coordfence`'s non-positive floor aborts the client-driven resume of any row an old binary minted at
0 during the rolling window, and the ground the proposal gives for the deletion is false. Nothing in
`non-spec-changes.md` is itself falsified: CODE-4 already states the rolling window, the explicit zero, and
the adapter's `InvalidArgument` refusal, so this pass has no statement to repair there. What remains is a
design reversal, which is keeping the floor until the release that tightens the check under §10.5's Phase 3,
together with the two SPEC-1 sentences carrying the false ground, and both are content no lens has read. The
decision now reaches the reviewer as OD8 in the summary, carrying the remedy the review derived.

Carried the open decisions into the summary. The review log's `### Open` list held decisions the human never
sees, and `## Open decisions` now carries OD8 through OD13: the `coordfence` floor deletion, the `>= 1` check
as a later Phase-3 migration, whether the two sentences calling a barrier's generation the current one are
edit sites given the mirror lag, the claim-register status for the S1-to-S5 window, a superseded replica's
checkpoint stream against a quiesced pod, and whether TEST-1 owes a case for a barrier naming an unbound
session. Each carries the ground its log entry gives, and each says whether a recommendation was derived; only
the floor deletion has one. OD3 gained one sentence naming the knock-on the log routed, which is that
`spec/04:151` carries `CoordinatorFenceRequest` as its only Adapter-service example and takes a restatement if
the row is reclassified. The entries left in `### Open` and not carried are the ones marked `UNVERIFIED`, which
are verification questions rather than decisions, and the wording, edit-site, and authoring items the next
loop's rounds own.
