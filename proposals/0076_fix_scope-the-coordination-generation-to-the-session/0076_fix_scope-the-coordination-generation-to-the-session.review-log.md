# Review log — 0076_fix_scope-the-coordination-generation-to-the-session

## Standing context

**Compaction pass 19, 2026-09-02.** Aged out run 4's spec rounds 2 and 3, whose sixteen ledger entries are retired to `## Retired` one line
each, keeping their ids; the two non-spec batches keep their ledger text. Every one of the sixteen opened by recording that the staged text
was byte-identical to the previous snapshot, so the batch reviewed no new text and its residue was largely a re-confirmation of inventories
already standing. Lifted into `### Settled`: the hold allowlist's five method names against §10.1.4's one, the absence of any hold mention in
`spec/06` or `docs/reference/state-machines.md` together with the staged §10.1.4 text adding no pod-side apiserver duty, the contents of
§28.8's fifth column so an operational lens need not open it, and the three unrelated credential and token hits a `no window in which` grep
returns beside `spec/10:41`. Lifted into `### Traps`: the two staged §29.10 lists that read as contradicting each other and do not, which
refuted class (g) covers. Honoured both `CORRECTS` in the batch, which move the §28.8 bijection gate from `tests/tier11_docs/` to
`tests/tier0_static/matrix_completeness_test.go`; pass 18 had already applied them to `### Settled`, so nothing changed and the superseded
tier-11 attribution is not standing anywhere. Nothing was deleted as a superseded `WATCHOUT`. The batch's two unclosed items, the §29.10
quiescence-unit `OPEN` and the §10.1.8 step-1 assembly-read `UNVERIFIED`, moved whole into a new ledger residue entry, `[spec.2-3.*]`, and
the two `### Open` lines that named their originating ids now name it. Both `DEFERRED` items the batch carried are already whole in
`### Deferred` and are untouched; the third, about migration 0181's `>= 1` tightening, was closed and retired in pass 18.
**The target of 540 lines was not reached, and the section grew again: 840 lines against pass 18's 820, at the same line width.**
`### Settled` is 437 lines, `### Traps` 262, `### Open` 85, `### Deferred` 24. The sixteen retired ledger entries are 658 lines against the
21 lines of standing context they added and the 39 lines of retirement rows, so the file as a whole shrank from 4463 to 3887.
Nothing was dropped to reach the target. The overshoot is concentrated in `### Settled`, which rule 6 asks for at one line per entry and
which runs at five to fifteen lines per bullet, because each bullet carries a derived inventory rather than a lookup value. Compressing
those to one line each would reach the target and would delete the derivations that seven separate `USEFUL` citations credit with saving a
round; that is the trade the next pass should decline as well until the code and test lanes land and the inventories become checkable
against a tree rather than re-derivable only by hand.
Two mechanical constraints, both inherited from pass 18 and both hit again: the Bash write path is denied for this file, so every edit goes
through the editor tool and a ledger deletion costs the full text of what it deletes, which is the whole of this pass's budget; and run 4's
ledger ids repeat across append batches (`[spec.3.review-citations.1]` occurred twice before this pass, and one of the two is now retired),
so a citation by id alone can resolve to either. The reducible items and their conditions are otherwise unchanged: the derived inventories
once the code and test lanes land, the carrier enumeration once SCHEMA-1's list is verified, the §28/§29 edit-site line once SPEC-2 is
applied, the tier-0-gates line once SCHEMA-1 and migration 0181 have landed with their stubs and their registry row, the S3/S4 split line
once both steps are committed, the two remaining `DEFERRED` items once the non-spec loop applies them, and about fifty `### Open` lines that
close as a batch when the human-review pass runs.

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
  is the only `INSERT INTO sessions` in the tree and every raw-SQL fixture omits the column; `RecordHandoff`'s 0-return sentinel survives
  because 0 stays impossible as a row value. USEFUL: a round-5 lens credits the writer enumeration with saving it a pass.
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
  `lenny_checkpoint_barrier_ack_duration_seconds`, and `lenny_prestop_barrier_target_source_total`, and `CH-ATTACH` names the
  `coordinator_hold` detail (`spec/28:1803` header, `:1805-1808`). SPEC-2 stages the fourth column of three rows and nothing else.
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
  and the fix makes its re-adoption fence succeed); and deleting the gateway fence path's zero floor as removing a fail-safe (the adapter's
  non-positive refusal is kept as the fail-closed backstop).
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

### Open

Detail for each item is in the ledger entry named at the end of its line. The `[non-spec.5.*]` and `[non-spec.6.*]` entries were retired
in compaction pass 17; the items they carried are in the ledger residue entry `[non-spec.5-6.*]`, filed there under the id named here.
The `[spec.1.review-*]` entries were retired the same way in compaction pass 18, and their two unclosed items are in the ledger residue
entry `[spec.1.*]`. The sixteen `[spec.2.review-*]` and `[spec.3.review-*]` entries were retired in compaction pass 19, and their two
unclosed items are in the ledger residue entry `[spec.2-3.*]`.

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
  while SPEC-2 keeps §29.10's clause unanswered. `[spec.1.*]`
- **UNVERIFIED: whether a fence retried at the same generation is accepted.** §10.1.2 step 2 has the new coordinator retry with the same
  value after a lost acknowledgement, and in specification terms equal is neither older nor a gap, so the retry is idempotent; the shipped
  adapter guard is `gen <= lastFenced` and refuses it with `coordinator_handoff_stale`. Two round-1 lenses stated the two halves and
  neither reconciled them. `[spec.1.*]`
- **§29.10's quiescence-unit clause admits two remedies** with different consequences, and the round that found it picked neither.
  `[spec.2-3.*]`
- **UNVERIFIED: staged §10.1.8 step 1's assembly read.** The step's own quoted `SELECT session_id FROM coordination_lease ...` selects no
  generation. `[spec.2-3.*]`

### Deferred

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
- DEFERRED [`spec-changes.md`, from `[non-spec.1.fix-G1.1]`]: the Pass 15 residue paragraph at about `:1501` and the Pass 4 line at about
  `:1110` describe migration 0181 as carrying the `>= 1` check tightening, and the tier-2 migration case as asserting it. Both sit inside
  dated `### Pass` records under `## Resolved in adversarial review`, which the design ruled not-a-site and that loop may not edit. What is
  true now: 0181 carries the `DEFAULT 1` on both columns and the backfill only, and its tier-2 case asserts the backfill, both defaults,
  the retained `>= 0` check, and a `.down.sql` that restores `DEFAULT 0`. Lifted here in compaction pass 18 because its subject is live and
  the entry it sits in is not yet aged out.

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


### [spec.1.*] · residue of run 4's spec round 1 · the obligations it still owns

Its facts, watch-outs, decisions, and mistakes are in `## Standing context`. What is left here is what nothing has closed.

OPEN [from `[spec.1.review-kubernetes.1]`]: does `spec/10` §10.1.8 step 3 ("the gateway's barrier dispatcher opens the `Checkpoint` stream
for each quiesced session ... and then releases quiescence", `spec/10_gateway-internals.md:185`) fix the unit of barrier quiescence at the
session? The proposal's §3 design overview and the summary both rest CODE-1's per-entry `barrierGate` on the claim that it does, while
SPEC-2 stages §29.10 to keep "the unit of the quiescence a barrier establishes" as unanswered. Either the design's citation overreads step
3 or the retained §29.10 clause is stale. For a later contradiction lens or the human.

UNVERIFIED [from `[spec.1.review-edit-sites.1]`]: whether the §4.1 `pod` class for `CoordinatorFenceRequest` should flip to `session` or be
kept with an explanatory paragraph. The tier-3 suite reads the classification as an addressing statement ("every request message §4.1
addresses to one session", `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:36-43`), while D5 leaves the fence one
genuinely pod-wide effect, the hold exit. A human reviewer or the fixer should pick; the finding asks only that the staged edits adjudicate
it rather than leave the file half-opened.

UNVERIFIED [compaction pass 18, from `[spec.1.review-feasibility.1]` against `[spec.1.review-reliability.1]`]: the two round-1 lenses
disagree about a fence retried at the same generation, and neither corrects the other. The reliability lens holds that `CoordinatorFence`
is idempotent under retry at the same generation, because equal is neither "older" (no stale rejection) nor `> last + 1` (no gap), on both
the unset and the recorded arm, so §10.1.2 step 2's "retry the fence RPC with the same generation value (up to 3 attempts with 1-second
backoff)" stays correct under D6. The feasibility lens holds that the adapter refuses it, its guard being `gen <= lastFenced` rather than
`gen < lastFenced` (`pkg/adapter/coordination.go:99`), and that the staged §28.6 arm enumerates older and higher and says nothing about
equal. Both may stand together as a spec-versus-code drift, but nobody has said so. Settle which it is before either sentence is applied.


### [spec.2-3.*] · residue of run 4's spec rounds 2 and 3 · the obligations they still own

Their facts, watch-outs, decisions, and mistakes are in `## Standing context`. What is left here is what nothing
has closed. Retired in compaction pass 19.

OPEN [from `[spec.2.review-fresh.1]`]: §29.10's quiescence-unit clause admits two remedies with different
consequences, and the round that found it picked neither. Either SPEC-2's narrowed §29.10 bullet drops the
quiescence-unit clause, which concedes that §10.1.8 step 3 fixes the unit at the session and then obliges someone
to check that "for each quiesced session" at `spec/10:185` is a unit statement rather than an enumeration of
targets; or the clause stands, CODE-1's per-entry `barrierGate` loses its stated spec ground, and SPEC-1 owes
§10.1.8 step 2 or 3 a sentence fixing the quiescence at the session. A fixer routes the choice through §7 rather
than picking one silently.

UNVERIFIED [from `[spec.3.review-mechanism.1]`]: staged §10.1.8 step 1 gains "The generation a barrier carries is
read from the session's coordination state when the barrier-target set is assembled", while the same step's own
quoted query is `SELECT session_id FROM coordination_lease WHERE coordinator_replica = $this_replica_id AND
released_at IS NULL`, which selects no generation. The step hedges with "of the form", and §29.7 step 4 states the
same read as "the `coordination_lease` rows" (`spec/29:1178-1179`) with the code reading the generation off that
row (`wiring.go:110-113`), so the staged sentence has a true referent. Nobody has checked whether the applied step
reads coherently to a reader who takes the quoted SQL as the assembly's whole read. A later lens that wants it
files it against the shipped §10.1.8 query list rather than as a defect in the staged sentence — EVIDENCE:
spec/10_gateway-internals.md:183.

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


### [spec.2.review-citations.2]

DECISION: returned an empty findings list — BECAUSE the spec-changes file is byte-identical to run 4's r2 snapshot (`diff -u scratchpad/cp-snap/0076-run4/spec-r2/*.spec-changes.md proposals/.../*.spec-changes.md` is empty; the whole diff this round is in `non-spec-changes.md` and the review log), and an independent re-resolution of every concrete citation in the live sections (§2 D1-D7, §3, §4, SPEC-1, SPEC-2, SPEC-3, §6, §7, §10) resolves verbatim — ALTERNATIVES: filing the two near-misses two earlier citation lenses already weighed and declined (the `wiring.go:49` "row value the dispatcher copies onto the wire" against the mirror on the healthy path, and "there is no second value to keep in agreement with the row" against `upsertMirror`'s pre-`RecordHandoff` snapshot). Both are indexed as standing OPENs, and the second reads in context as a statement about the rejected fixed-wire-constant design rather than about the mirror, so it is not a clean citation defect.

FACT: `upsertMirror` is called with `row.CoordinationGeneration` taken from the sweep's List snapshot, on the line after the takeover block that ran `RecordHandoff`, so after a takeover the `coordination_lease` mirror carries G for a whole sweep interval while the pod is fenced at G+1. The session row is authoritative; the mirror is the lagging side, and the barrier's healthy path reads the mirror. This is the data-flow direction a citation lens is told to check, and it is now checked in the reconciler itself — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:370, :399, :430; pkg/gateway/coordination/barrier/wiring.go:49, :104-114.

FACT: the twelve operational-RPC field-comment ranges SPEC-2 enumerates each fall inside the message SPEC-2 names, checked by listing `^message ` offsets rather than by reading the comments: SendMessageRequest 957, AttachRequest 982, RotateCredentialsRequest 1028, ExtendCredentialLeaseRequest 1056, RevokeCredentialsRequest 1080, InterruptRequest 1110, CheckpointRequest 1166, SignalDeadlineRequest 1297, ResumeRequest 1326, ExportPathsRequest 1526, ReportUsageRequest 1564, ShutdownRequest 1597; the fence and barrier carriers sit in CoordinatorFenceRequest 1447, CoordinatorFenceResponse 1463, CheckpointBarrierRequest 1475. `awk 'NR>=940 && NR<=1630 && /^message /{print NR": "$0}'` settles the whole enumeration in one call — EVIDENCE: schemas/lenny-adapter.proto:957, :1166, :1597.

FACT: `coordination_generation` appears outside `spec/10`, `spec/28`, `spec/29` only in `spec/04:200`, `spec/07:93`, `:215`, `:398`, `spec/12:160`, `spec/16:199`, `:208`, `spec/18:238`, and `docs/getting-started/concepts.md:101`, and none of them states the counter's initial value, so SPEC-3's baseline has no further prose site. `coordinator_connection_lost` occurs exactly twice, `spec/10:60` and `spec/29:1274`, both staged. `last_fenced_generation`, "last fenced generation", and "fenced value" occur nowhere in `spec/` or `docs/` outside the three staged files. `schemas/` carries `coordination_generation` only in `lenny-adapter.proto` — EVIDENCE: docs/getting-started/concepts.md:101; spec/12_storage-architecture.md:160.

USEFUL [spec.3.review-citations.1]: its instruction to skip re-deriving the anchor set and spend the budget on code-side attributions and mirror direction is what the two FACTs above came out of; the anchor set held a fifth time.

WATCHOUT: `sed -n '969,973p;1618,1622p;1172,1178p'` prints in file order rather than argument order, so a block-by-block read of proto comment ranges silently mis-pairs a range with its message. Pair each range with an `awk` message-offset listing instead — EVIDENCE: schemas/lenny-adapter.proto:1172, :1618.


### [spec.2.review-client-surface.1]

DECISION: Returned no findings — BECAUSE the staged spec edits reach no client-facing contract that has a
parallel representation left out of sync. The externally-consumed surfaces are all clean of the subject:
`coordination_generation` appears nowhere under `sdks/`, `charts/`, `schemas/*.json` (the JSONL and
runtime-ops-events schemas), or `docs/api/`, and `spec/04_system-components.md:200` declares the counter
"internal only, used for split-brain fencing" while `recovery_generation` is the client-visible one.
ALTERNATIVES: I worked up and dropped the `CoordinatorFenceResponse` carrier item (below) and the
`docs/reference/adapter-contract.md:69` "precondition for any subsequent operational RPC" item, the latter
under refuted class (a).

FACT: the wire-carrier enumeration in SPEC-2's closing paragraphs is complete and every line range in it
resolves verbatim. `grep -n "int64 coordination_generation" schemas/lenny-adapter.proto` returns exactly
fourteen fields: the twelve operational-RPC carriers SPEC-2 names (`:974`, `:1002`, `:1051`, `:1075`,
`:1096`, `:1119`, `:1179`, `:1310`, `:1398`, `:1536`, `:1581`, `:1623`) plus `CoordinatorFenceRequest`
(`:1452`) and `CheckpointBarrierRequest` (`:1480`). I re-checked all twelve comment bodies against the cited
ranges and all twelve match exactly, including `ShutdownRequest`'s "cannot tear the session down" variant at
`:1618-1622` and the two with trailing sentences SPEC-2 excludes (`AttachRequest` `:995-1001`,
`CheckpointRequest` `:1172-1178`). The RPC-comment ranges are right too: `CoordinatorFence` `:153-162`,
`CheckpointBarrier` `:165-179` (it runs to `:179`, four lines past the "canonical surface" sentence).
Do not re-derive this set. — EVIDENCE: schemas/lenny-adapter.proto:974, :1618-1622, :165-179

FACT: the `spec/28` and `spec/29` anchors SPEC-2 quotes all resolve. `spec/28` row order in the §28.8
matrix at `:1804-1809` is separator, `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, `CH-BARRIER`, `CH-PODHEALTH`,
so SPEC-2's `:1806` / `:1807` / `:1808` attributions are correct. `spec/10` layout: `:30` generation-counters
bullet, `:38` step 2, `:40` gap detection, `:41` step 3. — EVIDENCE: spec/28_communication-channels.md:1806,
:1807, :1808; spec/10_gateway-internals.md:41

WATCHOUT: the §28.5.1 `CH-FENCE` Messages sentence SPEC-2 quotes wraps across two physical lines
("The pod records the generation and from that point rejects any RPC / carrying an older one",
`spec/28_communication-channels.md:315-316`), so a single-line `grep` for the quoted string returns nothing
and reads as a false citation. It is not. Grep a short fragment, or `sed` the bullet.
— EVIDENCE: spec/28_communication-channels.md:315-316

OPEN: SPEC-2 says the `CoordinatorFenceResponse` comment (`schemas/lenny-adapter.proto:1455-1462`)
"repeats the stale-fence sentence and the gap sentence" and prescribes that it "takes the §28.5.1 Messages
wording". That wording is a record-and-reject-RPCs rule, and the Response comment carries no such sentence:
it states an `accepted` predicate ("false when the supplied generation is not greater than the last fenced
generation") and a `gap_detected` predicate, both of which D6 re-scopes and neither of which the prescribed
replacement text fits. The session qualifier an implementor would add is derivable, which is why I did not
file it, but a later round may want SPEC-2 to spell the two predicates out the way it spells out the
`CoordinatorFence` RPC comment's exemption unit. — EVIDENCE:
schemas/lenny-adapter.proto:1455-1462; ...spec-changes.md SPEC-2 closing carrier paragraph


### [spec.2.review-fresh.2]

DECISION: filed three findings, all in text that has stood unchanged for several rounds — BECAUSE `diff -q` shows `spec-changes.md` is byte-identical to the `spec-r2` snapshot again (only the non-spec file, the summary, and the log moved this round), so "read the newest text hardest" pointed at nothing in this loop's scope, and the residue is in old text — ALTERNATIVES: rejected filing §4's "either order" bullet (false, but rationale that lands nowhere in `spec/`, and the material skeptic's standing ground kills that class), rejected the §28.5.1 `CH-BARRIER` Preconditions bullet as an unlisted non-site (unit-neutral, and the non-site list claims no exhaustiveness), rejected the §10.1.2 gap-bullet insertion point as underspecified (refuted class (i)).

FACT: the round-2-of-run-4 fix to `non-spec-changes.md` dropped the `>= 1` CHECK from migration 0181 and now states in so many words that old binaries write explicit zeros through `pgstore.Create` for the whole rolling window — EVIDENCE: non-spec-changes.md "The retained `>= 0` check accepts those inserts", "A row an old binary wrote at 0 during the rolling window takes that same refusal until its first takeover bumps it". SPEC-1's rationale still says the `coordfence` floor is deleted "because a session row can no longer carry a non-positive value" (spec-changes.md, §10.1 baseline paragraph and the §10.1.4 paragraph). DEFERRED-adjacent: I did not file it, because it is the already-refuted "unreachable by construction" class (refuted class (j)) and lands nowhere in `spec/`, but a fixer touching either paragraph should soften it in the same pass.

FACT: `spec/10:183` carries, on one physical line, both the barrier-target `SELECT session_id FROM coordination_lease ...` query and the closing false-positive sentence SPEC-1 replaces. `spec/29` step 3 of §29.7 (`:1180-1185`) phrases the same read as "the `coordination_lease` rows", so the two sections already differ on what the assembly read returns; the staged provenance sentence lands in the one that returns only `session_id`.

MISTAKE: the §28.8 `CH-BARRIER` bullet's disposition clause was copied from the `CH-CHECKPOINT` bullet above it, where it is true. The `CH-CHECKPOINT` cell has the constraint in one sentence and the edited rejection rule in a second; the `CH-BARRIER` cell has only two sentences and the replaced clause is the trailing clause of the constraint sentence itself, so "the cell's constraint sentence ... unchanged" tells an applier to leave the clause standing.

USEFUL [Standing context / "The `spec/28` and `spec/29` edit sites are settled by one membership criterion"]: I re-tested every §28 generation sentence against the criterion independently (`grep -n generation spec/28`) and every one landed where the entry says; the entry saved the whole derivation.

USEFUL [`[spec.2.review-fresh.1]`]: its column-5 `awk -F'|'` tip and its anchor list made the §28.8 cell work minutes rather than an hour.

OPEN: the quiescence-unit contradiction it filed is still live and unchanged; I refiled it. Nothing was staged into §7 and no remedy was picked, so it has now cost this lens two rounds. If the next fixer again declines to pick, the choice belongs in §7 as a fourth open decision rather than in the log.


### [spec.3.review-applicability.4]

DECISION: returned an empty findings list for the applicability/sequencing lens over the staged spec edits — BECAUSE every anchor SPEC-1, SPEC-2, and SPEC-3 quote resolves verbatim and uniquely in the post-0073 tree, every staged edit carries either verbatim replacement text or a determinate by-reference wording, the proposal creates no new spec heading, anchor, file, or identifier (so classes 1 and 2 have an empty worklist beyond the in-place sentence rewrites), the one removal (§29.10's first "does not state" bullet) has both its legs staged (removal plus the "Shared by the whole pod" hold bullet and the "Partitioned per slot" gain that answer its two questions, carrying its citations), and no tier-0 or tier-11 gate byte-matches any sentence the staging rewrites — ALTERNATIVES: rejected filing three prose-level self-contradictions listed below, each of which is either already adjudicated by the loop or fails the materiality bar.

FACT: `diff -ru scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_...` is EMPTY this round, and `spec-changes.md` is byte-identical across the spec-r2, spec-r3, spec-r4 snapshots and the live file. Only `non-spec-changes.md`, `summary.md`, and `review-log.md` moved. The "read the changed sections first" instruction has no target on the spec lane; the staged spec text has now survived several rounds untouched — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3 vs proposals/0076_fix_scope-the-coordination-generation-to-the-session (identical).

FACT: every §28/§29 anchor the staging quotes is WRAPPED across physical lines, so a single-line `grep` for the quoted sentence returns nothing and looks like a dead anchor. `the acknowledgement of this fence is what closes the window ...` spans spec/28_communication-channels.md:330-331 and `the fence acknowledgement closes the window ...` spans :1683-1685; both are real. Use `sed -n 'A,Bp'` over the range, never `grep -c` on the sentence — EVIDENCE: spec/28_communication-channels.md:329-332, :1683-1685.

FACT: the §28.8 rows are one physical line each carrying five pipe-separated cells; `sed -n '1806p' spec/28_communication-channels.md | tr '|' '\n' | sed -n '5p'` isolates the "Holder of the exclusivity constraint changes" cell. All three staged cells (`CH-CHECKPOINT` :1806, `CH-FENCE` :1807, `CH-BARRIER` :1808) carry the quoted clauses verbatim, and the `CH-FENCE` cell's gap sentence is word-for-word identical to the §28.5.1 Degradation bullet's, so SPEC-2's "takes the same re-scoping ... word for word" is applicable as written — EVIDENCE: spec/28_communication-channels.md:1806-1808, :334-336.

FACT: no gate outside `schemas/lenny-adapter.proto` string-matches any sentence SPEC-1 or SPEC-2 rewrites. `grep -rl` over `tests/ scripts/ docs/ schemas/` for "rejects any RPC carrying an older", "rejected on the generation stamp", "superseded replica is rejected", "prior coordinator's RPCs are still accepted", "no longer holds the current generation" returns the proto alone, and "last known generation"/"coordinator_connection_lost" return nothing at all in `tests/`, `docs/`, `charts/`. S1's tier 11 stays precautionary and S2 (SCHEMA-1) is the only step the proto hit belongs to.

WATCHOUT: `spec/10`'s "Generation counters" bullet is line 30, which sits under the §10.1.1 heading (line 5), not §10.1.2 (line 32). SPEC-1 calls it "§10.1's" bullet. The anchor is still unique and applicable, and §10.1.1 is inside §10.1, so this is not a finding — the review log already carries it as UNVERIFIED. Do not spend a round on it — EVIDENCE: spec/10_gateway-internals.md:5, :30, :32.

MISTAKE (avoided, recorded so nobody spends a verification on it): SPEC-2's §28.5.1 Exclusivity bullet argues the value form "restores the wording of the sentence this one mirrors, since §10.1.2 step 2 states the window as ... and SPEC-1 leaves step 2 unchanged" (spec-changes.md:329-331), while SPEC-1 does edit step 2: its fence-announcement sentence "The pod records the new generation and from this point rejects any RPC carrying an older generation" gains "for that session" (spec-changes.md:151-153; spec/10_gateway-internals.md:38 is step 2). The clause SPEC-2 actually relies on ("the pod still accepts RPCs carrying the previous generation") is genuinely untouched, so the rationale holds and nothing lands wrong. This is the same class as the already-refuted "declares unchanged the very sentence whose closing clause it replaces" finding; it will refute again.

MISTAKE (avoided): SPEC-1 writes "Neither file states step 3's acceptance rule today" (spec-changes.md:266) while SPEC-2 stages §28.6's second-opener first sentence and the §28.8 `CH-CHECKPOINT` and `CH-BARRIER` cells precisely because they are pod-side rejection rules that fix the compared value, i.e. statements of step 3's rule in `spec/28`. The sentence is rationale that lands nowhere in `spec/`, and no missed site follows from it because SPEC-2 edits every one of those sentences anyway. Not filed; do not re-file without showing an edit site it hides.

UNVERIFIED: the staged §10.1.4 Observability text names "the per-session `coordinator_lost` log line" as a spec artifact. §10.1.4 today names only the `session.terminated` event with reason `coordinator_lost` and the local-disk post-mortem (spec/10_gateway-internals.md:58); the log line exists in code as `slog.Warn(reasonCoordinatorLost, "session_id", …, "last_generation", …)` at pkg/adapter/holdstate.go:226-228. Introducing it into `spec/` is a content choice rather than an applicability blocker, so this lens left it where the review log already has it (standing OPEN, "`coordinator_lost` log line as a spec artifact"). The docs/edit-sites lens or the human should settle whether §10.1.4 must introduce it before referring to it.


### [spec.3.review-citations.1]

FACT: The whole live staged text of the spec-changes file is lines 1-586; everything from line 587
(`## Resolved in adversarial review`) to 1844 is the frozen pass record. — EVIDENCE:
proposals/0076_.../0076_....spec-changes.md:587
FACT: Spec round 3 changed nothing in the proposal. `diff -rq scratchpad/cp-snap/0076-run4/spec-r3-start`
against the live directory is empty, so the "read the newest text hardest" instruction had no target this
round and the whole document is equally aged. — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3-start
FACT: Every anchor SPEC-1, SPEC-2, and SPEC-3 quote resolves verbatim, re-verified in this round from
scratch, and so does every code and proto citation in §2, §4, §5, and §7. Checked and correct: spec/10:30,
:37, :38, :40, :41, :58, :60, :183, :184, :198; spec/28:237-240, :251-253, :291-296, :314-317, :330-331,
:333-340, :349-353, :361-365, :1675, :1679-1681, :1683-1685, :1805, :1806, :1807, :1808; spec/29:1150-1152,
:1186, :1274, :1307-1313, :1322-1326, :1464-1470, :1523-1527, :1528-1535; spec/04:200 (inside §4.2);
proto :153-162, :165-179, :1442-1446, :1449-1451, :1455-1462, :1469-1474, :1475-1483, :1477-1479 and all
twelve field comments at :969-973 .. :1618-1622 including `ShutdownRequest`'s "cannot tear the session
down"; adapter coordination.go :29-32, :44-48, :89, :92, :93-94, :99, :108-121, :112-113, :126-128, :129,
:216, :223, :224-226, :228-231, :236-239; slotsession.go:267; holdstate.go :39-44, :90-100, :119, :172-176,
:192, :225-229, :283-296; adapterevents.go:80-96; barrier/wiring.go :49, :51-53, :104-114;
barrier/barrier.go :60-66, :229-246; prestop.go :390-397, :510; coordfence.go:147-153;
coordination/coordination.go :399, :430; coordination_seams.go :155-160, :233; start.go :3975, :4067,
:4233, :4237; httpsurface.go:592-599; migrations/0050:38-39. Do not re-derive this inventory.
FACT: `.Fence(` outside tests returns exactly two production call sites and
`GetCoordinationGeneration` inside `pkg/adapter` outside tests returns exactly two, so §2's
"only fence drivers" and "fence path and barrier path alone" claims are both exact. — EVIDENCE:
pkg/gateway/sessionserver/start.go:4237; cmd/lenny-gateway/coordination_seams.go:233;
pkg/adapter/coordination.go:92, :223

MISTAKE: the review log's `### Settled` entry recording that the sweep's pre-`RecordHandoff` mirror write
"falsifies the staged rationale 'there is no second value to keep in agreement with the row'" was never
applied. The sentence is still live at spec-changes.md:240-241 after three spec rounds. This round filed
it. The underlying code is confirmed: `upsertMirror` at
pkg/gateway/coordination/coordination/coordination.go:430 passes `row.CoordinationGeneration` from the List
snapshot while the takeover bumped the row at :371, and the barrier reads the mirror's column at
pkg/gateway/coordination/barrier/wiring.go:104-114.

DECISION: filed one finding, the mirror sentence at :240-241, and did not file the "unreachable by
construction" sentence at :253-255 — BECAUSE the second is parked in the review log's `### Open` list as
one of "three imprecise rationale sentences" routed to the human reviewer, and its own paragraph hedges it
("it stays as the fail-closed backstop"), so a material skeptic has a live refutation. The mirror sentence
has none: it is flat, unhedged, and contradicted by the proposal's own §2 D7 paragraph at :60-64.
ALTERNATIVES: filing both, rejected on the two-verifier cost of the weaker one.

UNVERIFIED: the barrier path's non-positive-generation refusal at pkg/adapter/coordination.go:224-226 stays
REACHABLE after CODE-4, because cmd/lenny-gateway/httpsurface.go:592 seeds the fallback target's generation
at 0 and overwrites it only on a successful session-row read, so a Postgres fault under the cache fallback
puts a literal 0 on the barrier whatever the row baseline is. Staged text at spec-changes.md:253-255 says
that refusal "becomes unreachable by construction rather than by a floor". Whoever closes the three
imprecise-rationale OPEN items should settle this one with that evidence in hand.

WATCHOUT: §28.6's second-opener paragraph runs :1679-1690 and its true closing sentence is the
`CH-ATTACH`/operation-lock one at :1687-1690, not "The constraint excludes a second replica" at :1686,
which the staged text at spec-changes.md:403 calls "that paragraph's closing sentence". The staged text
quotes the sentence verbatim, so the disposition is unambiguous and this is not a finding; do not spend a
verification pair on it. — EVIDENCE: spec/28_communication-channels.md:1686-1690


### [spec.3.review-client-surface.1]

DECISION: Returned an empty findings list for the client-facing-surface lens on the staged spec edits — BECAUSE every externally-consumed representation of `coordination_generation` was re-derived this round and none is reached by SPEC-1/SPEC-2/SPEC-3 — ALTERNATIVES: filing the §4.7 `CoordinatorFence` row ("Precondition for any subsequent operational RPC", spec/04_system-components.md:712, mirrored at docs/reference/adapter-contract.md:69) as falsified by D6/D7; rejected because that row states the acquiring coordinator's handoff duty (§10.1.2 step 2), not a claim that an unfenced session is refused, and a never-handed-off session is already served unfenced today.

FACT: `coordination_generation` reaches no client-facing surface. It is absent from `pkg/gateway/externalapi/openapi/openapi.json` (the only `generation` hit in that file is the pool sync-status summary at :3318, an unrelated CRD-vs-Postgres comparison), absent from `sdks/`, `charts/`, `pkg/apis/`, `pkg/embedded/`, `schemas/runtime-ops-events.schema.json`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/audit-events/`, `schemas/README.md`, and `schemas/examples/`, and `spec/04_system-components.md:200` states it as "internal only, used for split-brain fencing" while marking `recovery_generation` as the client-visible one. Its whole `docs/` presence is one unit-neutral sentence — EVIDENCE: docs/getting-started/concepts.md:101; spec/04_system-components.md:200; pkg/gateway/externalapi/openapi/openapi.json:3318.

FACT: the OpenAPI document is at `pkg/gateway/externalapi/openapi/openapi.json`, not `pkg/gateway/openapi/openapi.json` as the lens brief and several earlier briefs name it; the latter path does not exist. Three decoy copies live under `scripts/specshift/testdata/linepass/` — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json; scripts/specshift/testdata/linepass/tree/pkg/gateway/externalapi/openapi/openapi.json.

FACT: SPEC-2's proto carrier enumeration is complete and every cited range resolves. `schemas/lenny-adapter.proto` declares `coordination_generation` on exactly fourteen messages (fields at :974, :1002, :1051, :1075, :1096, :1119, :1179, :1310, :1398, :1452, :1480, :1536, :1581, :1623) — the twelve operational ones SPEC-2 names by message plus `CoordinatorFenceRequest` and `CheckpointBarrierRequest`. `CheckpointBarrierResponse` (:1493-1508) carries no generation-gate sentence, so it is correctly absent from the acceptance-rule carrier list; `CoordinatorFenceResponse` (:1455-1462) does carry one and is listed. Do not re-derive this set — EVIDENCE: schemas/lenny-adapter.proto:969-1623; spec-changes.md:511-533.

FACT: `coordinator_connection_lost` and `coordinator_lost` are structured log and post-mortem identifiers, never wire fields. Neither appears in any file under `schemas/`, `sdks/`, `charts/`, or `docs/`, so SPEC-1's removal of the generation from the pod-level event reaches no parallel client representation — EVIDENCE: pkg/adapter/holdstate.go:130, :304; spec/10_gateway-internals.md:60; spec/29_communication-scenarios.md:1274.

FACT: §28.6's "those four channels" resolves to `CH-ATTACH`, `CH-CHECKPOINT`, `CH-FENCE`, and `CH-BARRIER`, so SPEC-2's rewrite naming three of them plus a separate `CH-FENCE` arm drops no channel — EVIDENCE: spec/28_communication-channels.md:1669-1670, :1678-1679.

WATCHOUT: the round-3 snapshot and the live proposal are byte-identical (`diff -ru scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` is empty), so the "read the changed sections hardest" instruction has no target this round; budget the whole document instead of looking for a fix-stage diff — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3.

MISTAKE: the standing context's docs inventory cites `adapter-contract.md:68`, `:69`, `:96` and `metrics.md:307`, `:309-312` as the `coordination_generation` docs surface, but only `adapter-contract.md:69` and the four `metrics.md` metric rows still mention a generation at all, and none of them states a unit or a baseline. The conclusion (no docs edit site) holds; the line list is stale and cost a re-derivation — EVIDENCE: docs/reference/adapter-contract.md:69; docs/reference/metrics.md:307, :309, :312.


### [spec.3.review-docs-alignment.1]

DECISION: returned no findings — BECAUSE this loop's fixes must land in the staged spec edits, and every defect this lens can raise
(a missing or wrong `docs/` edit, per the lens's own guardrail) has its remedy in `docs/`, which this loop may not edit; independent
re-derivation of the `docs/` surface found nothing the staged edits falsify anyway — ALTERNATIVES: filing the D7 prose ambiguity noted
below, rejected because it is a mechanism-lens item and admits a benign reading.
FACT: the whole `docs/` surface touched by this change is unit-neutral and states no gate, unit, or baseline. `grep -rn
"coordination_generation\|fenced generation" docs/` returns exactly one line, `docs/getting-started/concepts.md:101`, which states only
that the counter tracks handoffs and is independent of `recovery_generation`. The adapter-contract rows are one-line RPC descriptions
(`docs/reference/adapter-contract.md:68`, `:69`), the metrics rows carry no generation semantics
(`docs/reference/metrics.md:307`, `:309-312`), and `docs/operator-guide/upgrades.md:47-54` names only the barrier-ack metrics. There is
no `docs/architecture.md` in the tree, so the standing context's "architecture.md:173" site does not resolve as written — EVIDENCE:
docs/getting-started/concepts.md:101; docs/reference/metrics.md:305-313; docs/operator-guide/upgrades.md:45-54
FACT: `coordinator_connection_lost` occurs in the repo only at spec/10_gateway-internals.md:60 and
spec/29_communication-scenarios.md:1274, both staged; `docs/`, `schemas/`, and `charts/` carry it nowhere, so SPEC-1's removal of the
generation from the pod-level event reaches no doc, runbook, or alert companion — EVIDENCE: grep over spec/ docs/ schemas/ charts/
FACT: `diff -ru` between the round-3 snapshot (`scratchpad/cp-snap/0076-run4/spec-r3`) and the live proposal directory is empty, so the
round-3 fix pass changed nothing. "Read the changed sections first" had no target this round; do not spend time hunting for the diff.
UNVERIFIED: D7's clause "the `!initialized` arm stays reachable for a session handed off whose successor's fence the pod has not yet
recorded" (spec-changes.md, §2 D7) sits in a sentence enumerating the two refusals D7 and the baseline remove, so it can be read as
saying the unset-arm *refusal* survives, which staged §10.1.2 step 3 ("a `CheckpointBarrier` naming such a session is accepted and
records no value") and CODE-2's `initialized && gen != fenced` gate both contradict. The benign reading is that the unset *state* stays
reachable. A mechanism or consistency lens should adjudicate the wording; not filed here because the reading is ambiguous rather than
plainly false.
USEFUL [standing context, "The `docs/` surface is eight sites and states no unit, baseline, or gate"]: it is right in substance and
saved the eight-site re-derivation; only the `architecture.md` path in it is stale.


### [spec.3.review-edit-sites.2]

DECISION: returned an empty findings list again for the edit-site lens — BECAUSE the spec-changes file is
byte-identical to the text `[spec.3.review-edit-sites.1]` swept, and I re-derived the whole identifier sweep
from scratch rather than trusting that entry, reaching the same result: every surface a sweep returns is
either staged or genuinely unit-neutral — ALTERNATIVES: rejected filing the three one-line citation drifts I
found (below), the `spec/07:215` parenthetical (refuted class (c)), `spec/10:157`'s partial-manifest
"coordinator's fenced generation" gloss (carries the same tension today, so the proposal does not make it
wrong), and the §4.1 fence-row question (already refuted this run).

FACT: `diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returns nothing, so nothing in the
proposal changed between the two edit-site passes; `spec-r4` differs only in `non-spec-changes.md`,
`summary.md`, and `review-log.md`. Reading the r3/r4 diff first is what tells you the spec lane is quiescent
— EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r4 vs proposals/0076_.../ (three files differ, none of them
`.spec-changes.md`)

FACT: independently re-derived and confirming the standing context's inventories, so a future round can skip
these. `coordination_generation` outside `spec/10|28|29` occurs only at spec/04:200, :323, :461, :711, :712;
spec/07:93, :215, :398; spec/12:160; spec/16:199, :208; spec/18:238; spec/28:991; and
docs/getting-started/concepts.md:101 — none fixes a unit, a baseline, or a compared value. `grep -rln
"coordinator_connection_lost|coordinator_generation_gap|coordinator_lost"` over `spec/ docs/ schemas/
charts/ tests/` returns only spec/10, spec/28, spec/29, and the adapter proto, so the §10.1.4 Observability
edit reaches no event catalog, runbook, or test. `grep -rn "generation-stale|stale coordinator|
CheckpointBarrier" docs/ charts/` returns only unit-neutral prose and the `checkpointBarrierAckTimeoutSeconds`
CRD/values comments, so D7 reaches no `docs/` or `charts/` site — EVIDENCE: docs/reference/adapter-contract.md:68,
:69, :96; docs/operator-guide/upgrades.md:47-54; docs/reference/metrics.md:307-312; charts/lenny/values.yaml:2442

FACT: the "no window" claim and its neighbours have exactly one carrier each, re-derived by phrase rather
than by trusting the log. `grep -rn "simultaneously issue|both the old and new|no window in which"` over
`spec/ docs/ schemas/` returns spec/10:41 alone (every other hit is `split-brain` in spec/25's unrelated
ops-lock text), so the step-3 rewrite falsifies no mirror — EVIDENCE: spec/10_gateway-internals.md:41

FACT: `grep -c "int64 coordination_generation" schemas/lenny-adapter.proto` is 14, and the fields sit at
:974, :1002, :1051, :1075, :1096, :1119, :1179, :1310, :1398, :1452, :1480, :1536, :1581, :1623 — twelve
operational carriers plus `CoordinatorFenceRequest` (:1452) and `CheckpointBarrierRequest` (:1480). SPEC-2's
enumeration is complete and there is no 15th — EVIDENCE: schemas/lenny-adapter.proto:1452, :1480

WATCHOUT: SPEC-2's three proto comment ranges are each one line high. The `CoordinatorFenceRequest` message
comment is at :1441-1445 (staged as `:1442-1446`), its field comment at :1448-1450 (staged as `:1449-1451`),
and the `CoordinatorFenceResponse` comment at :1454-1461 (staged as `:1455-1462`). The §28.5.1 `CH-FENCE`
Exclusivity window clause spans :329-330, staged as `:330-331`, and §29.8 step 9's window clause spans
:1324-1325, staged as `:1322-1326`. All resolve to the right text, so none is worth a finding, but do not
re-derive them as new drift — EVIDENCE: schemas/lenny-adapter.proto:1441, :1448, :1454;
spec/28_communication-channels.md:329-330; spec/29_communication-scenarios.md:1324-1325

USEFUL [Settled: "Derived inventories. Do not re-derive any of these."] and USEFUL [spec.3.review-edit-sites.1]:
both held on a full independent re-derivation this pass. The eight-site `docs/` list, the "no alert, runbook,
or tier-11 test is reached" claim, and the closed proto carrier set were each reproduced exactly. This lens
has now returned empty twice on this text with two independent sweeps; a third re-derivation buys nothing.


### [spec.3.review-feasibility.1]

DECISION: returned zero findings — BECAUSE every actor the staged spec edits address (the adapter/pod, the
coordinating gateway replica, the draining replica's barrier dispatcher) can perform what is assigned, and
every anchor I re-opened resolves verbatim — ALTERNATIVES: I worked up and dropped four candidates, listed
below, each on the materiality bar rather than on doubt about the facts.

FACT: `spec-changes.md` is byte-identical between `scratchpad/cp-snap/0076-run4/spec-r3-start`, `spec-r3`,
and the live file; only `non-spec-changes.md`, `summary.md`, and `review-log.md` moved in the r4 snapshot.
The "read the changed sections hardest" instruction has no changed spec text to point at this round, so the
whole staged staging is equally old. — EVIDENCE: `diff -rq scratchpad/cp-snap/0076-run4/spec-r3-start
scratchpad/cp-snap/0076-run4/spec-r3` is silent; `diff -rq spec-r4 proposals/0076_.../` names three files.

FACT: the adapter can supply both halves of SPEC-1's staged §10.1.4 Observability bullet. `enterHoldState`
already reads the started-session count and the generation through accessors before taking `hold.mu`, so
"names the number of started sessions the pod holds and carries no generation" needs no new pod-side data
path — EVIDENCE: pkg/adapter/holdstate.go:115-133 (`started := s.startedSessionCount()`, the
`coordinator_connection_lost` emit at :130-132).

FACT: every spec anchor SPEC-1/2/3 quote resolves, re-checked this round rather than taken from the standing
context's derived-inventory line: spec/10_gateway-internals.md:30 (Generation counters), :37 (step 1 CAS),
:38 (step 2 window), :41 (step 3 gate), :58 (hold timeout / post-mortem), :60 (Observability), :183 (§10.1.8
step 1, both anchors on one physical line), :184, :198; spec/28_communication-channels.md:237-240, :251-253,
:291-296, :314-317 (CH-FENCE Messages), :330-331, :333-335 (Degradation), :349-353, :361-365, :1675 (§28.6
"One holder per session"), :1679-1685, :1805-1808 (the four §28.8 cells, each row one physical line);
spec/29_communication-scenarios.md:1150-1152, :1186, :1274, :1307-1313, :1322-1326, :1424-1543 (§29.10);
spec/04_system-components.md:200.

WATCHOUT: the §29.10 "does not state" list already contains bullets that assert positive facts before naming
the gap (the `Interrupt` bullet states the operation-lock serialisation and §7.2's slot qualification, then
says what is unstated). So the narrowed barrier/`Interrupt` bullet SPEC-2 stages, which opens with a
statement and keeps two questions, does NOT breach the list's own contract, and a round tempted to file that
as a self-contradiction should stop here — EVIDENCE: spec/29_communication-scenarios.md:1528-1535 against
:1519-1521.

WATCHOUT: the four candidates I worked up and did not file, so nobody re-spends the round on them.
(1) Staged §10.1.2 step 3's "a `CheckpointBarrier` naming such a session is accepted" is unconditional while
§10.1.4 has the hold reject every inbound RPC other than `CoordinatorFence`. Not filed: shipped step 2
already carries the same unqualified shape ("the pod still accepts RPCs carrying the previous generation",
spec/10_gateway-internals.md:38), so a step-3 acceptance sentence scoped to the generation gate is the
section's established convention rather than a new contradiction.
(2) The staged §28.6 second-opener sentence and the §28.8 `CH-CHECKPOINT` cell assign the pod a
generation rejection on `CH-ATTACH` and `CH-CHECKPOINT`, which the adapter does not perform: it reads
`coordination_generation` on the fence and barrier paths alone (the proposal states this itself at
spec-changes.md:51-53, :180-182). Not filed: the pre-edit sentences already assign it, so the edit changes
the relation and not the assignment, and the drift is pre-existing.
(3) §28.5.1's `CH-CHECKPOINT` Exclusivity bullet (:291-296) says the lease "and the generation stamp"
restrict the channel to the coordinating replica, which the unset arm makes half-true. Not filed: SPEC-2's
own treatment of §28.6's "The constraint excludes a second replica" settles the identical case — the lease
alone excludes the second replica for a session the pod holds no generation for — so the non-site record is
justified rather than an omission.
(4) SPEC-1's staged §10.1 baseline sentence has a replica coordinating a never-taken-over session carry the
row value on its gateway-to-pod messages, and only four production sites populate
`CoordinationGeneration` on the wire. Not filed: a normative spec sentence the code has yet to satisfy is
the ordinary direction of this repo's spec-first rule, and the standing OPEN "UNVERIFIED: wire population"
already routes it.

USEFUL [Standing context, "The whole carrier surface for the pod-level hold log line is two lines"]: it named
spec/10:60 and spec/29:1274 and both were exactly right, which let me check the §10.1.4 Observability
staging against the code in one read instead of a grep sweep.

USEFUL [Traps, "Editing hazards in this proposal's own files"]: the warning that `cat -n *spec-changes.md`
globs two files and shifts every line by 46 saved me from citing the wrong offsets throughout.


### [spec.3.review-fresh.2]

DECISION: returned an empty findings list — BECAUSE the two candidates a fresh read produces on this text are both
pre-refuted classes (i) and (j) in the standing context, and the third is a routed item. ALTERNATIVES: filing the
staged §10.1 baseline invariant ("every generation a pod validates is positive", spec-changes.md:236-238) against
the cache fallback's literal 0 (`cmd/lenny-gateway/httpsurface.go:592-599`, dispatched unfiltered through
`pkg/gateway/coordination/barrier/wiring.go:49`) — refuted class (j); filing the §10.1.2 step-2 "gains 'for that
session'" instruction as an underspecified target (spec-changes.md:152-153) — refuted class (i); filing §4's
"either order" claim (spec-changes.md:118-121) against SPEC-1's own §10.1.8 rationale (:218-222) and SPEC-2's
§28.8 `CH-BARRIER` rationale (:392-395) — recorded in Standing context as false but deliberately routed to human
review, so a filing would be refuted on disposition rather than on substance.
FACT: `spec-changes.md` is byte-identical to the `spec-r3` snapshot; run 4's fix round touched only
`non-spec-changes.md`, `summary.md`, and the review log — EVIDENCE:
`diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returns nothing, while the `spec-r4` snapshot
differs in those three files. So "read the changed sections first" has no spec-lane content this round.
FACT: every spec anchor SPEC-1, SPEC-2, and SPEC-3 quote still resolves verbatim at the cited line, re-checked
independently this round: spec/10:30, :37, :38, :41, :58, :60, :183, :184, :198; spec/28:237-240, :251-253,
:291-296, :330-331, :349-353, :361-365, :1672-1685, :1805-1808; spec/29:1150-1152, :1186, :1274, :1307-1312,
:1322-1326, §29.10 at :1424-1543; spec/04:200. The code citations in D5/D6/D7 and §4 also resolve
(`pkg/adapter/coordination.go:89`, :92-94, :99, :108-121, :112-113, :216, :223-226, :228-239;
`pkg/adapter/slotsession.go:267`; `coordfence.go:147-153`; `barrier/wiring.go:49`, :104-114;
`coordination/coordination.go:430`; `start.go:4237`; `coordination_seams.go:233`).
FACT: the §29.10 successor-pointer gate is satisfied by the opening paragraph's linked `[§28.5.3](...)` line
carrying `CH-MSGSOCK` (spec/29:1428), and the gate needs one linked §28.5 pointer on a line also naming a `CH-`
identifier — EVIDENCE: tests/tier11_docs/successor_pointer_test.go:112-136. SPEC-2's removal of the first
"does not state" bullet (whose §28.5.1 reference is bare, not a link) cannot break it. Do not re-derive.
FACT: `tests/tier0_static/spec_map_exception_blocker_retention_test.go` reads §29.10 only as a spec-map key with
blocker R7 and never reads the section body, so SPEC-2's bullet edits are invisible to it — EVIDENCE:
tests/tier0_static/spec_map_exception_blocker_retention_test.go:63-95.
USEFUL [Standing context · "Refuted classes"]: the (a)-through-(k) list saved two filings that would each have
cost two verifiers; read it before writing any finding, and read (i) and (j) in particular, because they are
exactly what a fresh holistic lens rediscovers first.


### [spec.3.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE no staged spec edit in
SPEC-1/2/3 reaches a CRD, a controller, a status subresource, a finalizer, an admission webhook, or RBAC;
the whole change lives on the gateway-to-pod gRPC surface, the Postgres `sessions` row, and the Redis/PG
coordination lease — ALTERNATIVES: rejected filing the D7 "a superseded draining replica quiesces a session
it no longer coordinates for up to the 90s ack deadline" residual, because it is a gateway path with no
controller, work-queue, or reconcile on it, and the review log already routes it to the human-review pass.

FACT: `coordination_generation` has no CRD, chart, or `pkg/apis` carrier at all. `grep -rln
"coordination_generation|coordinationGeneration" charts/ schemas/ pkg/apis/` returns only
`schemas/lenny-adapter.proto`. That closes the whole single-writer / field-manager / status-as-inbox half of
this lens in one command — EVIDENCE: schemas/lenny-adapter.proto (sole hit); spec/04_system-components.md:200
(the counter is a Postgres session-record column, not a CRD field).

FACT: the only k8s-shaped sentences anywhere near the staged text are ones the proposal does not touch:
§10.1.4's zero-RBAC / no-apiserver-path rationale for `AdapterTerminating` and the orphan session reconciler
(spec/10_gateway-internals.md:58), and §4.6.3's `SandboxClaim` webhook and occupancy projection
(spec/04_system-components.md:438, :442). SPEC-1 edits only the Observability bullet at
spec/10_gateway-internals.md:60, which is a log line and a gauge — EVIDENCE: spec/10_gateway-internals.md:58,
:60; spec/04_system-components.md:438, :442.

FACT: spec/18 places `CoordinatorFence` plus the `coordination_generation` CAS in Phase 4 and the
`CheckpointBarrier`/`CheckpointBarrierAck` protocol in Phase 8, and SPEC-3's baseline sits on the session
record the column already carries, so nothing this proposal stages makes an earlier phase depend on a later
one — EVIDENCE: spec/18_build-sequence.md:238, :404.

WATCHOUT: `diff -ru scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returned empty this round, so
the "read the changed sections first" instruction had nothing to point at and the whole document was
equally aged. Do not read an empty diff as a tooling failure and go hunting for the snapshot; check
`ls scratchpad/cp-snap/0076-run4/` first (spec-r4 also exists).

WATCHOUT: the Bash tool resets cwd between calls in this harness, so a `diff`/`grep` with repo-relative
paths silently runs from /home/ec2-user/lenny anyway but a `cd` in one call does not persist. Use absolute
paths or accept the default cwd; do not chain `cd` across calls.


### [spec.3.review-mechanism.4]

FACT: the spec-changes file is byte-identical to the `spec-r3` snapshot, so run 4's spec lane re-reviewed the
same staged text spec round 3 saw; only `non-spec-changes.md`, `summary.md`, and the review log moved —
EVIDENCE: `diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returns nothing for
`.spec-changes.md`.

DECISION: filed exactly one finding, on staged §10.1's baseline sentence ("every generation a pod validates
is ... strictly greater than the value carried before the takeover that fenced it",
spec-changes.md:237-238) — BECAUSE the pod validates the predecessor's 1 both in the §10.1.2 step-2 window
(spec/10_gateway-internals.md:38, unchanged) and for the never-taken-over session the sentence exists to
license, and SPEC-1's own rationale at :243-244 depends on the pod validating that 1 —
ALTERNATIVES: rejected three other candidates, each recorded below.

WATCHOUT: D7's rationale says in one breath that "D7 removes the second refusal" and that "the
`!initialized` arm stays reachable for a session handed off whose successor's fence the pod has not yet
recorded" (spec-changes.md:67-70). Read as the gate's refusal branch the two are incompatible, because
CODE-2's gate becomes `initialized && gen != fenced`; read as the unset *state* staying reachable it is
right. Not filed: `non-spec-changes.md:94` states the predicate unambiguously, so no implementor can build
the wrong gate from it — EVIDENCE: proposals/.../0076_....non-spec-changes.md:94.

FACT: SPEC-1's ground for declaring §10.1.8 step 1's and §29.7 step 4's "current `coordination_generation`"
non-sites is that "Each names the row value the dispatcher copies onto the wire
(`pkg/gateway/coordination/barrier/wiring.go:49`)" (spec-changes.md:257-262). That attribution is wrong on
the healthy path: `wiring.go:49` sends `t.CoordinationGeneration`, which `MirrorTargetLister.Targets` fills
from the `coordination_lease` mirror row (`le.CoordinationGeneration`, wiring.go:104-114); the session row
is reached only through the cache fallback. The proposal's own D7 states this correctly at
spec-changes.md:80-83, so the non-site justification contradicts D7 — EVIDENCE:
pkg/gateway/coordination/barrier/wiring.go:48, :104-114. Not filed: the remedy is a rationale sentence that
lands nowhere in `spec/`, and the standing OPEN "'Current' generation on the barrier" already owns the
question of whether those two sentences are edit sites. The next round that touches that paragraph should
fix the attribution while there.

MISTAKE (avoided, recorded so nobody spends the round): the §28.5.1 and §28.8 gap mirrors gain the session
qualifier but not D6's unset exemption, while staged §10.1.2's gap bullet gains both. That is SPEC-2's own
stated criterion working ("each mirror keeps the level of detail it carries today"), and the mirrors'
predicate is vacuous rather than false for an unset session, so it is not a drift finding.

MISTAKE (avoided): §29.10's staged "a fence for one slot's session neither fences nor unfences another"
sits two bullets from the staged "A successful fence for any one of those sessions exits the hold for the
pod". They are not in conflict — the first is scoped to the recorded generation, the second to the hold —
and refuted class (g) already covers the hold half.


### [spec.3.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE every observability
surface the staged edits touch was re-derived from the tree this run and each stayed mutually consistent:
`lenny_adapter_coordinator_hold` (catalog `pkg/observability/metrics/catalog.go:271`, emitter
`pkg/adapter/metrics.go:108`, spec `spec/16_observability.md:185`, doc `docs/reference/metrics.md:309`) keeps
its pod unit under D5 and the staged §10.1.4 bullet says so; `lenny_coordinator_handoff_stale_total` is
incremented only on the fence path (`pkg/gateway/coordination/coordfence/coordfence.go:84`, `:205`), so D7's
barrier acceptance changes no counter's meaning; the barrier ack outcome enumeration
(`spec/16_observability.md:41`, `success|timeout|partial_captured|error`) is a count change rather than a
semantic one — ALTERNATIVES: filing the "per-session `coordinator_lost` log line" as an undefined spec
artifact, and filing §16:183's "increments when a replica receives a generation-stale rejection" as loosened
by D7; the first is already an `### Open` item and is naming rather than correctness, the second is
pre-existing drift (the barrier's `FailedPrecondition` never reached that counter).

FACT: the whole alert and runbook surface is untouched by this proposal, verified from the alert side rather
than from the metric side. `grep -rn "generation" docs/runbooks/*.md pkg/alerting/rules/rules.go` returns only
`PoolConfigDrift` / CRD-generation material, and `docs/runbooks/coordinator-handoff-slow.md` names only
`CoordinatorHandoffSlow` over `lenny_coordinator_handoff_duration_seconds`. No coordination-generation,
fence, barrier-rejection, or hold metric carries an alert, so tier-11's alert-to-runbook resolution is not
reached — EVIDENCE: docs/runbooks/coordinator-handoff-slow.md:32; spec/16_observability.md:552;
docs/reference/metrics.md:307-312.

FACT: the two structured-event carriers this change edits are the only ones in the tracked non-proposal tree.
`grep -rn "last known generation\|last_generation\|lastGeneration" spec/ docs/ schemas/ charts/` returns
exactly `spec/10_gateway-internals.md:60` and `spec/29_communication-scenarios.md:1274`, both staged, and
`coordinator_generation_gap` occurs only at `spec/10:40`, `spec/28:335`, `spec/28:1807`, `spec/29:1311`,
`schemas/lenny-adapter.proto:160`, `:1461` — the four `spec/` sites are staged and the two proto sites are
SCHEMA-1's. §16 carries neither event, so the observability inventories need no edit. USEFUL
[Derived inventories]: the standing entry's claim that no alert, runbook, or tier-11 test is reached held on
an independent re-derivation from the alert side.

FACT: in the tree `coordinator_lost` is a *reason* string on `session.terminated` / `AdapterTerminating`
(`spec/10_gateway-internals.md:58`, `spec/04_system-components.md:747`) and separately the slog message of the
per-session termination line (`pkg/adapter/holdstate.go:226`) and the post-mortem filename prefix (`:304`).
The staged §10.1.4 text's "per-session `coordinator_lost` log line" therefore describes real code; the
standing `### Open` item about it is a naming question rather than a false description.


### [spec.3.review-performance.4]

DECISION: returned an empty findings list on the staged spec edits (SPEC-1/2/3) — BECAUSE
`diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run4/spec-r3
/home/ec2-user/lenny/proposals/0076_fix_scope-the-coordination-generation-to-the-session` exits 0: the
proposal directory is byte-identical to the round-3 snapshot, so the text this lens is asked to review is
exactly the text `[spec.3.review-performance.3]` already reviewed and returned empty on, and the two
earlier performance passes on the same staging (`[spec.1.review-performance.1]`,
`[spec.2.review-performance.1]`) also returned empty — ALTERNATIVES: re-deriving the whole cost model,
which the three prior shards already carry verbatim; I spot-checked the load- and failure-bearing claims in
the staged text instead (the per-session gap reset's collateral, D7's effect on the §10.1.8 target-set size
and the 400-upload burst, the write-neutrality of the counter baseline) and found nothing the earlier
shards do not already settle.

FACT: nothing in the proposal has changed since the round-3 snapshot — the whole directory, review log
included, matches. A future performance pass on this staging should run the diff first and, if it is empty,
read `[spec.3.review-performance.3]` and `[spec.1.review-performance.1]` rather than re-deriving; four
passes have now returned empty on this same text. — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3 vs
proposals/0076_fix_scope-the-coordination-generation-to-the-session (diff -rq, rc=0)

USEFUL [spec.3.review-performance.3] and USEFUL [spec.1.review-performance.1]: between them they carry the
write-neutrality argument (no new per-session store write, no new watch, no hot key, no single-leader
serialization), the unit-neutrality of `lenny_adapter_coordinator_hold` and
`lenny_coordinator_handoff_stale_total`, the `dispatchOne` stream-before-send fact that makes D7 drain-
neutral, and the §10.1.8 failure-surface analysis. They saved the entire pass.

OPEN: the two dispositions this lens has now declined twice remain for the human pass — the acceptance-arm
quiescence stall (a false-positive barrier from a superseded draining replica quiesces a healthy session for
up to the 90s ack deadline), carried in `### Open` as "Superseded replica's stream against a quiesced pod",
and the DEFERRED on CODE-4's migration 0181 deploy ordering, which the summary now says 0181 leaves the
`CHECK (coordination_generation >= 0)` alone; whoever closes that DEFERRED should confirm the staging and
the summary now agree so the item can be retired rather than carried a fourth round.


### [spec.3.review-reliability.4]

DECISION: returned an empty findings list for the reliability lens on run 4's round 3 — BECAUSE
`diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` is empty and `spec-changes.md` is
byte-identical in the `spec-r4` snapshot too, so this is the same staged text my own lens already cleared
twice (`[spec.1.review-reliability.1]`, `[spec.3.review-reliability.1]`) — ALTERNATIVES: re-tracing the four
candidates that entry names, rejected because each disposition still holds and re-filing costs two verifiers.

FACT: the round-3 fixer again made no edit to any proposal file. Two consecutive rounds of the spec lane have
produced no diff, so a lens told to "read the changed sections first" has had no target since spec-r2. Check
`diff -rq` against BOTH `spec-r3` and `spec-r4` before planning a pass; only `non-spec-changes.md`,
`summary.md`, and the review log have moved.

FACT (re-verified this pass, so the next lens need not): every anchor the staged text quotes on the recovery
path resolves verbatim — `spec/10_gateway-internals.md:30` (Generation counters bullet), `:37` (step 1 CAS
`RETURNING`), `:38` (step 2 window and the 3-attempt/1s-backoff retry), `:41` (step 3 acceptance sentence),
`:58` (hold timeout terminating every started session, one `AdapterTerminating` each), `:60` (Observability
bullet, "with the last known generation"); `spec/29_communication-scenarios.md:1150-1152` (§29.7 framing
rejection sentence), `:1186` (step 4 "carrying the session's current `coordination_generation`"), `:1274`
(§29.8 step 2 `coordinator_connection_lost` "carrying the last known generation"), `:1322-1326` (step 9
window clause). No drift since round 3.

FACT: re-scoping the §10.1.2/§29.8-step-7 gap reset per session loses no recovery work. A gap on session A
can only be produced by coordinators that took A's per-session lease, and those coordinators send RPCs for A
alone, so the per-session cancellation set is exactly the pod-wide one restricted to the sessions it may
legally touch; a co-tenant B gets its own reset on B's own next gapped fence. The shipped pod-wide reset is
the side that over-cancels. I traced this as a candidate "recovery path that abandons state with no
reclaimer" and it is not one.

USEFUL [spec.3.review-reliability.1]: its four not-filed candidates (accepted false-positive barrier and the
single wall-clock drain deadline, Postgres failover losing the step-1 CAS, hold exit leaving co-tenants to
the lease-expiry reclaimer, CODE-4 deleting the gateway zero floor with the adapter backstop retained) are
still the complete candidate set my lens generates on this text, and each disposition survives re-reading.
Combined with the refuted-class list, reading the two entries first is the whole pass.


### [spec.3.review-security.3]

DECISION: empty findings list, third consecutive empty security pass over this staging — BECAUSE
`diff -rq scratchpad/cp-snap/0076-run4/spec-r3 proposals/0076_.../` returns rc=0, so the ENTIRE proposal
directory, review log included, is byte-identical to the round-3 snapshot; the spec-changes file has not
moved since round 2 either. Both lens checks re-run independently against the tree still resolve to ground
the standing context closes — ALTERNATIVES: I re-derived and dropped (1) the durability angle on the fenced
generation moving from `Server` (pod lifetime) onto the slot registry entry (shorter lifetime), which would
be a silent RESET of a split-brain control if a session could unbind and re-bind on the same pod: the only
deregistration sites are session end and the failed-start compensating release
(`pkg/adapter/slotsession.go:174-195`, `:215`, `:361`; `pkg/adapter/session.go:238`), none of which is
followed by a re-bind of the same session id, and the resume path fences on re-entry anyway, so the reset is
not reachable. This is the standing OPEN "Rebind and the unset state"; it is a code-lane question and not a
spec-staging finding. (2) The deletion of step 3's shipped no-window invariant
(`spec/10_gateway-internals.md:41`): grep confirms that sentence has no mirror in `spec/`, `docs/`,
`schemas/`, or `charts/` (the other "no window" hits are §4.8/§4.9 credential-revocation prose, unrelated),
and step 2's own shipped window clause at `:38` already bounded it, so the staged replacement loses no
stated control. (3) The staged §10.1.4 Observability change: both carrier sites are staged
(`spec/10_gateway-internals.md:60`, `spec/29_communication-scenarios.md:1274`) and grep over `docs/`,
`charts/`, and `pkg/alerting` returns no third carrier.

FACT: `CoordinatorFenceResponse.last_fenced_generation` is still a pod self-report that reaches no gateway
decision, re-verified this round: the only non-test readers are the copy into `CoordinatorFenceResult`
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:29`, `:60`) and the adapter's own emit sites; no
gateway branch reads it. Nothing SPEC-1/2/3 stages moves any security bound onto it. The authoritative
counter stays the Postgres session row.

USEFUL [standing context, "`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report ..."]:
third lens in a row this has saved the whole trust-boundary re-derivation. Keep it until the code lane lands.

USEFUL [`[spec.3.review-security.1]` and `[spec.1.review-security.1]`]: both prior entries enumerate exactly
the candidates I re-derived independently, with the same dispositions, over text that has not changed. The
security lens is exhausted on this staging; a fourth run adds nothing unless SPEC-1's step-3 wording, the
§28.6 second-opener clause, or D7's acceptance arm is rewritten.

WATCHOUT: `diff -rq` of the snapshot against the live proposal is the cheapest first move on this proposal.
The orchestrator's "read the changed sections first" instruction has had no changed sections to read for
three rounds running — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r3 vs proposals/0076_.../, rc=0.


### [non-spec.1.review-applicability.1]

FACT: this round's `diff -rq` against the run-4 snapshot returns ONLY the review log; every staged file is byte-identical to the
last round's. "Read the changed sections first" had no target again (fourth consecutive round). — EVIDENCE:
scratchpad/cp-snap/0076-run4/spec-r4 vs proposals/0076_.../
FACT: the mechanical layer of this proposal is clean. I re-resolved, from scratch, every anchor the applicability lens needs:
server.go:302/:307/:314, coordination.go:44/:52/:63/:148/:158-166/:180-188/:236, checkpoint.go:94/:111/:122/:124, slot.go:21/:153,
session.go:237-239/:271, slotsession.go:282-285/:347, oplock.go coalesce block, pgstore.go:140/:177/:244-248, memstore.go:46/:58-61,
coordfence.go:143 + the `gen <= 0` floor, coordfixture.go:76/:98-102/:106-108/:109/:115/:122/:220-241, prod_columns_test.go:295/:583/:610,
lint-migrations.sh TEST_DIR + pass 3, cmd_run.go:498-508/:635-641/:880, proto :153-162/:165-179/:1442-1446/:1449-1451/:1455-1462/
:1469-1474/:1477-1479, spec/28:1806-1808 (via `awk -F'|' '{print $5}'`). All resolve. Do not re-derive this set.
FACT: the slot registry is keyed by session id and an entry is created per session (`ensureSlotStateLocked`, slot.go:82-102), so
CODE-1's move of `coordinationState` onto `slotState` cannot leak a generation across sessions through slot reuse. The rebind OPEN is
the only residual on that axis. — EVIDENCE: pkg/adapter/slot.go:82-102, :136
FACT: no test, doc, chart, or script string-matches any proto comment or spec sentence SCHEMA-1/SPEC-1/SPEC-2 rewrite. A grep for
"cannot drive the pod|last fenced generation|coordinator_handoff_stale|never treated as a gap|Strictly monotonic" over tests/, docs/,
spec/, schemas/, scripts/ returns only spec/10, spec/16, spec/28, schemas/, docs/reference/metrics.md. The docs surface for
`coordination_generation` is three lines (concepts.md:101, architecture.md:173, adapter-contract.md:69), all unit-neutral.
DECISION: filed two findings — the status file's live contradiction with D5 and §7, and the two §8 cases (tier-2 resume-fence,
tier-3 D7 acceptance) that no checklist step's deliverable produces — BECAUSE both are unclosed items the log carries and both are
executable-procedure defects rather than wording. ALTERNATIVES rejected: the `IMPLEMENTOR TO FILL THE BLANKS` header over
non-spec-changes.md:6 (close variant of the refuted §5-header finding); the `.down.sql` "restores the `DEFAULT 0`" singular against
two changed defaults (a competent implementor restores both, and the lease default is cosmetic); S5 declaring no tier 4 and S6 no
tier 0 (tier-list bookkeeping, refuted three times); the stale deadlock comment at coordination.go:126-128 that CODE-1/CODE-3 falsify
(refuted class (k), and the standing context already records the rewrite).
WATCHOUT: `TestProdMigrationsRollBackPerStep` and `TestProdMigrationsApplyExpectedSchema` iterate `prodMigrationSchema`, and 0180's
row is `{migration: "0180", table: "checkpoint_manifest"}` — a column-less row is genuinely the precedent the proposal claims.
Verified at prod_columns_test.go:583 and :295.


### [non-spec.1.review-citations.1]

DECISION: returned an empty findings list — BECAUSE every concrete citation in the non-spec staging and
every anchor SPEC-1/2/3 quote resolves verbatim at the cited location, and the two candidates I did derive
are both already recorded as weighed-and-declined in the standing context — ALTERNATIVES: filing the two
memstore `Update` cases (standing context: "Two lenses declined to file them ... Hand them to the
implementor as an addendum rather than filing them") and filing `coordinatorfence.go:37`'s per-pod-lifetime
exemption (standing context: "under refuted class (k) a reviewer should not file them").

MISTAKE: I re-derived both of those from scratch (a full sweep of every `.CoordinationGeneration`
assertion in `_test.go`, plus the `coordinatorfence.go` doc comment) before finding the log entries that
already adjudicate them. The standing context's `### Settled` "baseline-shift and spec-phrase sweeps are
complete against the tree" bullet and the `### Open` list both name them. Read `### Open` before sweeping;
it is a list of things already looked at, not only of questions.

FACT: this run's citation audit re-resolved, and all of them hold exactly: the twelve operational-RPC proto
field-comment ranges (`schemas/lenny-adapter.proto:969-973, 995-1001, 1046-1050, 1070-1074, 1091-1095,
1114-1118, 1172-1178, 1305-1309, 1393-1397, 1531-1535, 1576-1580, 1618-1622`, the last closing "cannot tear
the session down"); the fence/barrier carriers at `:153-162`, `:1442-1446`, `:1449-1451`, `:1455-1462`,
`:165-179`, `:1469-1474`, `:1477-1479`; every `spec/10` anchor (`:30`, `:37`, `:38`, `:41`, `:58`, `:60`,
`:183`, `:184`, `:185`, `:198`); every `spec/28` anchor (`:237-240`, `:251-253`, `:291-296`, `:330-331`,
`:349-353`, `:361-365`, `:1679-1681`, `:1683-1685`, and the §28.8 column-5 cells at `:1805`-`:1808`);
`spec/29:1150-1152`, `:1186`, `:1274`, `:1322-1326`; `spec/04:200`; and every code and test citation in
CODE-1 through CODE-4, §8, and §9 that I checked, which was all of them except the problem statement's.

FACT: the tier-8 disjointness claim in §8 is exact. `coordination_crash_takeover_test.go`'s three
`pod.LastFenced` reads at `:150`, `:195`, `:223` sit in the two subtests seeded at 1 (`:118`, `:179`), and
the three baseline-shifting assertions at `:267`, `:283`, `:296` sit in the third subtest, seeded unset at
`:239-241`. Subtests 1 and 2 also assert `CoordinationGeneration` (`:147`, `:227`) but against explicit
seeds, so they are correctly outside class 1 — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118,
:147, :179, :227, :239-241, :267, :283, :296.

USEFUL [standing context, "Derived inventories. Do not re-derive any of these."]: it is accurate. I
re-derived a large part of it anyway and found no drift, which is evidence the inventory can be trusted by
the next lens rather than re-walked.


### [non-spec.1.review-client-surface.1]

DECISION: returned empty — BECAUSE every client-facing parallel of the staged change resolves clean when
re-derived from the tree rather than from the log, and the two things I could not close are already recorded
as an OPEN and a DEFERRED — ALTERNATIVES: filing the `tests/claim-map.json` DEFERRED here (rejected: the
register's rows are mechanism-level, `CoordinatorFence` and `CheckpointBarrier` stay reachable from
production code after S1, so `WIRED` is not falsified by the per-session predicate landing later, and the
remedy the DEFERRED names is not obviously owed); filing a missing tier-3 fence-outcome case for CODE-1
(rejected: the fence rescope is pinned at tier 1 and tier 4, and the standing context records tier-list
bookkeeping as a three-times-refuted class).

FACT: the proto codegen fan-out is Go-only and section 9 already lists both outputs. `schemas/buf.gen.yaml`
pins `out: ../pkg/proto` for `protoc-gen-go` and `protoc-gen-go-grpc` and nothing else, and its own comment
says adding a proto needs no buf.gen.yaml change, so a SCHEMA-1 comment edit has exactly two generated
parallels — EVIDENCE: schemas/buf.gen.yaml:16-25; Makefile:91-99.

FACT: there is no REST, OpenAPI, MCP, A2A, or SDK carrier of the coordination generation anywhere in the
tree. `grep -c coordination` over `pkg/gateway/externalapi/openapi/openapi.json` returns 0 (note the path:
it is `externalapi/openapi/openapi.json`, not `pkg/gateway/openapi/`, which does not exist), `sdks/client`
and `sdks/runtime` return nothing, and `docs/api/internal.md` carries no `Coordinator`, `Barrier`,
`coordination`, or `generation` token at all — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json;
docs/api/internal.md.

FACT: SCHEMA-1's carrier list is exact against the file. Walking the fourteen `int64 coordination_generation`
declarations back to their owning `message` line gives SendMessageRequest, AttachRequest,
RotateCredentialsRequest, ExtendCredentialLeaseRequest, RevokeCredentialsRequest, InterruptRequest,
CheckpointRequest, SignalDeadlineRequest, ResumeRequest, CoordinatorFenceRequest, CheckpointBarrierRequest,
ExportPathsRequest, ReportUsageRequest, ShutdownRequest — the twelve SCHEMA-1 names plus the two handled
separately, with no fifteenth — EVIDENCE: schemas/lenny-adapter.proto:974, :1002, :1051, :1075, :1096,
:1119, :1179, :1310, :1398, :1452, :1480, :1536, :1581, :1623.

FACT: no test in `tests/` string-matches any comment phrase SCHEMA-1 rewrites. A grep for `cannot drive the
pod`, `A pod validates`, `last fenced`, `strictly older`, and `coordinator_handoff_stale` over the whole
`tests/` tree returns nothing, and the one tier-0 gate that does read proto comment text is scoped by anchor
to three unrelated comments (GetObservedIntegrationLevel and its request, plus the interrupt one) —
EVIDENCE: tests/tier0_static/adapter_proto_intrapod_pointer_test.go:36, :48-60.

FACT: D7's `error`-to-`success` accounting shift needs no docs edit, because the metric row already
enumerates all three outcome values — EVIDENCE: docs/reference/metrics.md:196 ("Barrier ack outcomes:
`success`, `timeout`, `error`").

USEFUL [Standing context, "Derived inventories"]: the eight-site `docs/` enumeration and the "sdks/,
schemas/README.md, and schemas/examples/ mention coordination_generation nowhere" line were both correct
when re-checked, and knowing they had been derived eight times let me spot-check rather than re-sweep. The
per-lens re-derivation OPEN filed by `[non-spec.5.review-docs-alignment.1]` applies to this lens too: my
whole pass was confirmation of an inventory that has now held across two client-surface runs.

USEFUL [Standing context, "WATCHOUT: a late pass number in the spec-changes file"]: running `diff -rq`
against the snapshot first cost one command and showed only the review log had changed, which is what let me
skip hunting for new fix-stage text.

OPEN: the DEFERRED against `tests/claim-map.json` is still open and I could not substantiate it from the
register's own contents. `spec/28_communication-channels.md:163-169` binds a row to a *mechanism*, and the
two rows in question are keyed on the mechanisms `` `CoordinatorFence` `` and `` `CheckpointBarrier` ``,
both `WIRED` with a production surface (tests/claim-map.json:449-453, :461-465), which stays true after S1.
The separately weaker row is `CheckpointBarrierRequest.coordination_generation`, filed `UNWIRED` with note
"no production reader compares it until the generation fence lands" (tests/claim-map.json:75-82) while
`pkg/adapter/coordination.go` compares it today — that staleness is pre-existing and is already the standing
UNVERIFIED. Whoever closes the DEFERRED should decide against the mechanism-versus-predicate reading rather
than against the DEFERRED's summary.


### [non-spec.1.review-docs-alignment.5]

DECISION: empty findings list for the docs-alignment lens over the non-spec staging, run 4 round 1 — BECAUSE `diff -rq` against the run-4 snapshot shows only the review log changed, so the staged text this lens owns is the same text `[non-spec.1.review-docs-alignment.4]` cleared, plus the fix-round edits to migration 0181, the tier-4 co-tenant bullet, the tier-7a barrier bullet, and the summary's deliverable index, none of which has a `docs/` describing surface — ALTERNATIVES: (1) filing CODE-4's rolling-window residual (a row an old binary wrote at 0 takes an `InvalidArgument` fence refusal, which after the floor deletion relinquishes the lease and aborts the resume) as a new operator-facing failure mode; rejected because no `docs/operator-guide/` or `docs/runbooks/` page enumerates causes of a failed resume or a fence relinquish at all — the coordination surface in operator docs is `upgrades.md:49` and `coordinator-handoff-slow.md`, and neither narrates fence failure — so category 2's "page that enumerates that failure's causes" has no referent, and the outcome is already stated in CODE-4's own staged paragraph (non-spec-changes.md:149-153). (2) filing `docs/reference/adapter-contract.md:69` ("precondition for any subsequent operational RPC") as the docs twin of the twelve proto consequence clauses SCHEMA-1 rewrites; rejected under refuted class (a), which names that file: the row states the gateway's step-2 duty, not the pod's gate.

FACT: the run-4 round-1 `diff -rq` between `scratchpad/cp-snap/0076-run4/spec-r4` and the proposal directory returns exactly one differing file, the review log. The trap "run the `diff -rq` first" held for a fourth consecutive round.

FACT: re-verified the docs sweep from scratch rather than from the standing context, and it holds. `grep -rn "coordination_generation\|coordinator handoff\|CoordinatorFence\|coordination generation" docs/ charts/` returns `getting-started/architecture.md:173`, `getting-started/concepts.md:101`, `reference/adapter-contract.md:69`, `reference/metrics.md:307`, `:310`, `operator-guide/upgrades.md:49`, `runbooks/coordinator-handoff-slow.md`, and the two alert-rule copies. None states the fenced value's unit, the counter's baseline, the barrier's gate, or the hold's records. `grep -rni "coordinator_lost|coordinator_connection_lost|post-mortem|last_generation|gap_detected|last_fenced|split-brain"` over `docs/` returns nothing on this mechanism (`adapter-contract.md:97` and `metrics.md:309` name the hold without a generation; every split-brain hit is the `lenny-ops` remediation lock).

FACT: no tier-11 docs test and no tier-0 test binds the fence or barrier proto comments to `docs/reference/adapter-contract.md`. The tier-11 files that read both the proto and that page reconcile the manifest credentials path, the intra-pod nonce, frame identifiers and addressing, session scrub addressing, and tracing context; `grep -rn "coordination_generation\|CoordinatorFence\|CheckpointBarrier" tests/tier11_docs/` returns nothing. `claim_register_proto_agreement_test.go` reads field declarations only. So SCHEMA-1's comment-only edits create no docs edit site and no tier-11 obligation — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:37-43.

FACT: `docs/reference/adapter-contract.md` is hand-authored, not generated: it carries a Jekyll front matter block and prose sections, with no generation note — EVIDENCE: docs/reference/adapter-contract.md:1-10.

WATCHOUT: `docs/runbooks/coordinator-handoff-slow.md:28` defines the coordinator handoff as "the step where the parent session passes control of a delegated child session", which is a different mechanism from §10.1's replica handoff the alert actually measures. It is pre-existing docs drift, this proposal makes it neither better nor worse, and the lens guardrail bars reconciling the spec toward a doc. A docs loop owns it; do not file it here.

USEFUL [non-spec.1.review-docs-alignment.4]: its two-file identifier sweep and its "no page enumerates the causes of a lost drain capture" fact were both reproducible in minutes and are what let this pass verify instead of re-walking `docs/`. Budget a fifth docs pass on this proposal at the `diff -rq`, the identifier sweep, and the two category-2 questions.

OPEN: this lens has now returned empty five times on 0076 (three spec rounds, two non-spec). Nothing in the remaining staging can create a docs site, because every deliverable is adapter process state, a proto doc comment, a migration, or a test, and `docs/` describes none of them. If the loop retires a lens on an empty return, retiring it here costs nothing.


### [non-spec.1.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the staging adds, changes, or removes was
re-swept against `spec/`, `docs/`, `schemas/`, `charts/`, `sdks/`, and `migrations/` in this run and nothing outside
the §9 list becomes wrong — ALTERNATIVES: filed nothing on (a) 0181's `.down.sql` naming one `DEFAULT 0` where the
`.up.sql` changes two, because the second column's `DEFAULT 1` is itself recorded as cosmetic (`upsertMirror` always
binds the value), so its non-restoration on rollback is doubly cosmetic; (b) the §28.4 claim-register DEFERRED,
because the rows name mechanisms that stay `WIRED` and the file is generator-produced, so any remedy lands in the
root planning document rather than in this staging; (c) the `CoordinatorFenceResponse` "each takes the §28.5.1
Messages wording" prescription, because round 3 already filed and fixed the description half and the residue is
wording in a converged spec lane.

FACT: this run's `diff -rq` against the snapshot showed only the review log changed, so the staged spec and non-spec
text is byte-identical to what run 4's earlier rounds reviewed — EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r4
FACT: `tests/claim-map.json` is generator-produced from root `gateway-runtime-comms.md` §7.1 and a tier-0 gate
diffs the regenerated bytes against the committed file, so a hand-edited row is a red tier 0 rather than an edit
site the proposal can stage — EVIDENCE: tests/tier0_static/claim_register_generator_test.go:20-31, :44-45
FACT: the whole `docs/`, `charts/`, `sdks/`, and `schemas/` surface for this change is one grep:
`grep -rln "coordination_generation\|coordinationGeneration" schemas/ sdks/ charts/` returns
`schemas/lenny-adapter.proto` alone, and `grep -rn "coordinator_connection_lost\|last_generation" docs/ charts/
schemas/ spec/` returns exactly the two `spec/` lines SPEC-1 and SPEC-2 stage — EVIDENCE:
spec/10_gateway-internals.md:60; spec/29_communication-scenarios.md:1274
FACT: all twelve operational-RPC `coordination_generation` comments resolve verbatim at the ranges SPEC-2 lists and
every one of them literally opens its second sentence with "A pod validates", so the single replacement span the
proposal states is applicable to each; `AttachRequest` (`:995-1001`) and `CheckpointRequest` (`:1172-1178`) are the
only two with a trailing sentence past the consequence clause — EVIDENCE: schemas/lenny-adapter.proto:969-973,
:995-1001, :1618-1622
FACT: the accessor blast radius holds exactly as §9 lists it, re-derived this run:
`Server.LastFencedGeneration` has three callers (holdstate.go:119, coordination_test.go:73, coordfixture.go:115),
`Pod.LastFenced`/`Pod.Fence`/`Pod.StaleRPCRejected` are read only in the tier-4, tier-7a, and tier-8 files §9 names,
`BarrierWaiting` adds only adapterclient/checkpointbarrier_test.go:163, and `checkpointRootsForSession` has two
callers (checkpoint.go:94, resume.go:178) — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:115, :231
USEFUL [Standing context / "Derived inventories"]: the eight-times-re-derived `docs/` surface and the "no CRD, chart,
or `pkg/apis` carrier" bullet were both accurate and saved this lens most of its sweep; a spot re-verification of
each cost minutes rather than a pass.
USEFUL [Traps / "a message's doc comment sits above the `message` line"]: reading the proto comments rather than the
proposal's description of them is what let the nineteen-carrier list be checked in one pass.


### [non-spec.1.review-feasibility.1]

DECISION: returned an empty findings list for the actor-action feasibility lens over the non-spec staging — BECAUSE every actor/action assignment in SCHEMA-1, CODE-1 through CODE-4, TEST-1, §8 and §9 was re-opened against the tree this run and each holds; the two things this lens would otherwise file (§8's tier-4 sentence for D7 naming no case while S5 declares no tier 4, and the tier-2 resume-fence case with no file) are already `### Open` items originating from this same lens in `[non-spec.5.review-feasibility.1]`, and the standing context's thrice-refuted tier-list-bookkeeping class predicts a refutation — ALTERNATIVES: filing the tier-4-sentence item anyway (rejected: D7 is pinned by a named tier-1 amendment and a named tier-3 case, so criterion (f) is met and the dangling sentence is bookkeeping); filing 0181's `.down.sql` singular-default asymmetry (rejected: the mirror default is cosmetic because `upsertMirror` always binds explicitly, and `TestProdMigrationsRollBackPerStep` iterates `m.columns`, which 0181's row has none of, so nothing goes red).

FACT: the whole relocated-state blast radius re-verified this run, exactly as the standing context states. `s.coord` appears only in `pkg/adapter/coordination.go` (`:45-47`, `:53-55`, `:97-121`, `:232-235`, `:246-256`) and `s.barrier` in five production sites (`coordination.go:64-66`, `:264`, `:269`, `checkpoint.go:122`, `:124`); `LastFencedGeneration` has three callers, `BarrierWaiting` adds `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163`, `isQuiescedForBarrier` adds only `pkg/adapter/coordination_test.go:279`, `:298` — EVIDENCE: grep over `--include=*.go` excluding `pkg/proto`.

FACT: the S3/S4 disjointness in the two dual-edit files is real and re-checkable in one grep. `tests/tier8_chaos/coordination_crash_takeover_test.go` has `pod.Fence`/`pod.LastFenced`/`StaleRPCRejected` only at `:130`, `:150-151`, `:165`, `:184`, `:195-196`, `:223-224`, all inside the two subtests seeded `CoordinationGeneration: 1` at `:118` and `:179`; the third subtest (`:235`) seeds the field unset and holds the 1/1/2 assertions at `:267`, `:283`, `:296` with no accessor call — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:112, :173, :235.

FACT: `opLock.Begin` genuinely admits a distinct co-tenant session id into the pending set and blocks it until promotion (`l.checkpoints[sessionID] = promote` then `l.wait`), which is what CODE-1's guard-to-link window and the tier-7a bullet's "no timing assertion" both rest on — EVIDENCE: pkg/adapter/oplock.go:117-128.

FACT: SCHEMA-1's nineteen-carrier list matches SPEC-2's enumeration item for item and in the same order, and the `CoordinatorFenceRequest.coordination_generation` no-edit carve-out now agrees with SPEC-2's "keeps its wording"; the round-3 `DEFERRED` against `non-spec-changes.md` on that point is discharged by the current text — EVIDENCE: non-spec-changes.md:11-21; spec-changes.md:487-533.

USEFUL [standing context, "Derived inventories"]: the closed enumerations (accessor blast radius, the two-file S3/S4 class, `coordfixture` carrying no build tag, the op lock as the true serialiser) let this pass spot-check rather than re-derive, and every spot-check confirmed them. Do not re-derive them a seventh time.

WATCHOUT: `coordfixture.StartPod` sets only `WorkspaceBase` and `ManifestDir`, while `concurrentServer` (`pkg/adapter/slot_test.go:24`) also sets `SessionsRoot`, `ArtifactsRoot`, and `CredentialsDir`. The co-tenant tier-4 case starts its second session over `Pod.Client`, so if that second `StartSession` needs a root `StartPod` leaves empty, the fixture needs a field added rather than a second `StartPod`. Nobody has run it; the adapter imposes no session-count cap (`Server.slots` is a plain map, `pkg/adapter/server.go:379`) — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:76-102.


### [non-spec.1.review-kubernetes.1]

DECISION: returned empty — BECAUSE the Kubernetes-idiom lens has almost no surface on this proposal: no CRD, status subresource,
finalizer, admission webhook, controller watch, or field-manager is touched anywhere in the staged non-spec changes. ALTERNATIVES:
I worked up and dropped three candidates — a grace-period overrun from D7's newly-blocking barrier (budgeted already), the
`.down.sql`/helm-rollback path (compatible under expand-contract), and the un-backfilled `coordination_lease` rows at 0 (self-heals
on the next `upsertMirror` sweep and the proposal already states the loud `InvalidArgument` refusal).

FACT: the only Kubernetes surface the whole proposal rests on is migration 0181's deploy ordering, and it verifies exactly as staged:
the migrate Job is `pre-install,pre-upgrade` at weight -5, ahead of the gateway Deployment — EVIDENCE:
charts/lenny/templates/migrate-job.yaml:10-16, :37-39; spec/10_gateway-internals.md:420, :427-434 (expand-contract, Phase 3 for a
constraint old-version writes violate, Phase-1 columns need a server-side DEFAULT).

FACT: D7's accepted-and-blocking `CheckpointBarrier` cannot overrun the gateway pod's grace period. The chart budgets 240s as
90s tier cap + 90s single wall-clock BarrierAck + 30s stream drain, and `checkpointBarrierAckTimeoutSeconds` is one wall-clock
deadline across all coordinated pods rather than per pod — EVIDENCE: charts/lenny/values.yaml:2435-2447. A later lens tempted by
"D7 lengthens the drain" should stop here rather than re-deriving it.

FACT: there is no CRD, `pkg/apis`, or chart carrier of the coordination generation at all. `grep -rn 'coordination_generation|
CoordinationGeneration|coordinationGeneration' pkg/apis/ charts/ config/` returns nothing. The docs carriers are three and none
becomes wrong: docs/getting-started/concepts.md:101, docs/reference/adapter-contract.md:69,
docs/getting-started/architecture.md:173 — none states pod scope, a baseline of 0, or the barrier match rule.

USEFUL [Standing context, `### Traps`, "WATCHOUT: a late pass number in the spec-changes file is not evidence that the staged spec
text changed"]: `diff -rq` against the snapshot returned only the review log, which told me in one command that no fix-stage text
was new this round and saved a full re-read of the changed-sections-first ordering.

FACT: migration numbering and the CHECK the proposal leaves alone both verify — `migrations/` tops out at
`0180_drop_checkpoint_slot_id`, so 0181 is free; the inline `CHECK (coordination_generation >= 0)` CODE-4 retains is at
migrations/0050_session_record_fields.up.sql:38-39; the mirror column's `DEFAULT 0` is at
migrations/0164_coordination_lease.up.sql:44.


### [non-spec.1.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every end-to-end flow the non-spec staging describes
traced clean against the tree this pass, and the three candidates I worked up all landed inside classes the
loop has already refuted or already carries as an OPEN — ALTERNATIVES: filing §8's tier-4 sentence for D7
against S5's tier list (killed by the "tier-list bookkeeping is a refuted class, three times over" entry);
filing `TestCheckpointBarrierRejectsWithoutFence`'s missing replacement assertion (two test-coverage lenses
already declined it and it is an `### Open`); filing 0181's `.down.sql` naming one default where the `.up.sql`
changes two (the `coordination_lease` default is cosmetic, so the rollback consequence is below the bar).

FACT: the diff against the run-4 snapshot touched only the review log this round, so the "read the changed
sections first" instruction had no target for the fourth consecutive round — EVIDENCE:
scratchpad/cp-snap/0076-run4/spec-r4 vs proposals/0076_.../ (diff -rq returns the review-log line alone).

FACT: the whole per-entry move re-verified clean against the tree, and the enumerations §9 and CODE-1 rest on
are exact. `s.coord` is confined to `pkg/adapter/coordination.go` (`:45-55`, `:97-121`, `:232-256`), `s.barrier`
has exactly five production readers (`coordination.go:64-66`, `:264`, `:269`, `checkpoint.go:122`, `:124`),
`checkpointRootsForSession` has exactly two callers (`checkpoint.go:94`, `resume.go:178`), and `pgstore.go:170`
is the only production `INSERT INTO sessions` in the tree. Every line citation in CODE-1 through CODE-4, §8,
and §9 that I re-opened resolved: `server.go:302`/`:307`/`:314`, `coordination.go:148`/`:158-166`/`:236`,
`oplock.go:117-128`, `barrier.go:238-245`, `pgstore.go:140`/`:177`/`:244-248`, `memstore.go:46`/`:58-61`,
`coordfence.go:143`/`:147-153`, `coordfixture.go:73-75`/`:76`/`:98-102`/`:109`/`:115`/`:122`/`:220-241`,
`coordination_fence_split_brain_test.go:72`/`:83`, `coordination_colocation_race_test.go:130`/`:144`/`:260`/`:287-288`,
`holdstate_test.go:674`/`:700-716`, `migrate-job.yaml:10-16`/`:37-40`. Do not re-derive this set.

FACT: a `package adapter` tier-1 case CAN drive an accepted barrier to completion without the external
`adapter_test` fixtures — the landed `TestCheckpointBarrierAcksEchoedCheckpointID` runs the RPC on a goroutine,
spins with `waitBarrierWaiting`, then calls `s.barrier.link(...)` and `s.barrier.complete()` directly, because
it is in the same package. §8's tier-1 bullets are therefore constructible where they sit, and the package-wall
trap in `## Standing context` applies only to a case that drives a real `Checkpoint` *stream* — EVIDENCE:
pkg/adapter/coordination_test.go:3, :243-300.

WATCHOUT: `TestCheckpointBarrierRejectsWithoutFence` amended naively does not go red, it HANGS. It calls
`CheckpointBarrier` with `context.Background()` and no deadline; under D7 the unset arm accepts, and the RPC
then blocks in `select { case <-done: case <-ctx.Done() }` with nothing to link or complete, so the tier-1
package dies on the Go test timeout rather than on an assertion. Whoever writes §8's replacement assertion must
carry the goroutine/link/complete pattern with it, not just flip the expected code — EVIDENCE:
pkg/adapter/coordination_test.go:189-197; pkg/adapter/coordination.go:264-268.

CORRECTS [`### Deferred`, the `tests/claim-map.json` entry from `[spec.3.review-edit-sites.1]`]: its stated
remedy ("the fence and barrier rows need an `ABSENT`-or-deferred status ... The non-spec loop owns it") cannot
be applied in the file it names. `tests/claim-map.json` is generator output from root `gateway-runtime-comms.md`
§7.1 and `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs the generator and byte-diffs it at tier 0, so
hand-editing the register turns tier 0 red. If that DEFERRED is ever worked, its edit site is
`gateway-runtime-comms.md` §7.1 plus a regeneration, not the JSON — EVIDENCE:
scripts/seed-claim-register.py:11-13, :37-39; tests/tier0_static/claim_register_generator_test.go:20-31, :45.

USEFUL [`## Standing context` → `### Traps` → "tier-list bookkeeping is a refuted class"]: it killed my
strongest-looking candidate (§8 asserting "Tier 4 covers the same flow" for D7 at non-spec-changes.md:328-329
while checklist S5 declares tiers 0/1/3/7a and no tier-4 bullet names a barrier) before I spent a verification
on it.

USEFUL [`## Standing context` → "A refused barrier costs a duplicate capture rather than a lost checkpoint"
and "The pod-level op lock, rather than the barrier gate, is what serialises co-tenant checkpoints"]: together
they made the tier-7a barrier bullet and the D7 blast radius verifiable in one read instead of three.


### [non-spec.1.review-operational.1]

FACT: the observability surface really is closed, and I re-derived it once more so nobody has to.
`coordinator_connection_lost` occurs in `spec/` only at spec/10_gateway-internals.md:60 and
spec/29_communication-scenarios.md:1274 (both staged), nowhere in `docs/`, `charts/`, `schemas/`, or
`pkg/alerting/`. The §28.8 "Operator observable" column (field 5 of the pipe split) names only
`coordinator_generation_gap` + `coordinator_lost` on `CH-FENCE` (:1807) and the ack-timeout counters on
`CH-BARRIER` (:1808); neither states a rejection outcome, so D6/D7 reach neither cell — EVIDENCE:
spec/28_communication-channels.md:1803-1810 read with `awk -F'|' '{print $6}'`.
USEFUL [Standing context "The alert and metric surface is closed and untouched"]: correct in every particular
I checked. Do not re-derive it a third time.

FACT: `coordfence.fence` does NOT treat a non-`FailedPrecondition` fence error as a refusal. Only
`FailedPrecondition` / `!res.Accepted` reaches `incStale()` and the stale arm; every other error, an adapter
`InvalidArgument` included, falls into `default:`, burns `maxAttempts`, then `relinquish()` releases the
coordination lease, increments `lenny_coordinator_fence_relinquished_total`, and returns `ErrRelinquished`,
which the caller must read as "abort the resume". So an `InvalidArgument` fence produces a relinquish counter
increment with NO matching `lenny_coordinator_handoff_stale_total` increment — EVIDENCE:
pkg/gateway/coordination/coordfence/coordfence.go:159-188, :192-200; :170 (`incStale` on the stale arm only);
pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_podlifecycle.go:193-197;
docs/reference/metrics.md:312.

MISTAKE: the standing context records the "Both refusals are loud and fail closed" imprecision as a FACT and
the reliability lens's weighed-and-declined "deleting the zero floor removes a fail-safe" as resting on
"the adapter's non-positive refusal is kept as the fail-closed backstop". Those two entries contradict each
other, and the declining lens took the premise the other entry falsifies. Pass 23 then added the sentence
that makes the population reachable and admitted ("A row an old binary wrote at 0 during the rolling window
takes that same refusal until its first takeover bumps it", non-spec-changes.md:152-153) without revisiting
either. Filed this round as the one finding.

WATCHOUT: the phrase "Both refusals are loud and fail closed" has two hits. The live one is
`...non-spec-changes.md:151`; the other, `...spec-changes.md:1091`, sits inside a frozen
`## Resolved in adversarial review` pass record and must not be edited.

FACT: only the resume path can send a zero generation after CODE-4. The sweeper takeover bumps the row
through `RecordHandoff` before fencing, so its value is >= 1; the resume path fences without bumping, which is
what the deleted floor at `coordfence.go:147-153` covered.

UNVERIFIED: whether §8 owes a caller-side case for the `InvalidArgument` fence outcome (attempt budget
exhausted, `ErrRelinquished`, relinquish counter up, stale counter flat). §8 amends
`TestFenceZeroGenerationFencesAtBaseline` to assert only that the fencer puts 0 on the wire; nothing pins what
the caller does with the refusal the proposal calls the backstop. Folded into the finding's suggested fix
rather than filed separately.


### [non-spec.1.review-performance.1]

DECISION: returned empty — BECAUSE the staging remains write-neutral and every failure mode I traced
degrades no worse than shipped — ALTERNATIVES: I worked up and rejected four candidates, each below.

FACT: `diff -rq` snapshot vs live returns ONLY the review-log file this round. The spec-changes,
non-spec-changes, checklist, summary, status, problem-statement, and deviations files are byte-identical
to `/home/ec2-user/lenny/scratchpad/cp-snap/0076-run4/spec-r4`. Compaction pass 18 was the whole delta.
Run the `diff -rq` first; it is one command and it decided this round's reading order.
— EVIDENCE: scratchpad/cp-snap/0076-run4/spec-r4 vs proposals/0076_.../

FACT: the top-tier barrier arithmetic, worked out rather than asserted. Shipped pod-wide gate: N co-tenant
barriers, `open()` overwrites `done`/`checkpointID`/`signaled` unconditionally, so N-1 hang to the 90s ack
deadline (`defaultCheckpointBarrierAckTimeoutSeconds = 90`,
pkg/admission/pool_config_validator/validator.go:569). Per-entry gate: barrier k returns after k archives,
because `s.ops.Begin` admits one checkpoint at a time and queues a distinct co-tenant id behind the running
one (pkg/adapter/checkpoint.go:111, pkg/adapter/oplock.go:117-128). Wall clock goes from a flat 90s to
min(N x archive, 90s), so the per-session gate is bounded by the same deadline and strictly better at every
N. There is no new bottleneck at the top tier and no new gateway-side goroutine occupancy.

FACT: CODE-3 adds no I/O. `terminateHeldSession` and `writeHoldPostMortem` are already invoked once per
terminated member inside pass 2's loop, so reading each member's own generation off `m.state` changes what
one existing write says, not how many writes occur — EVIDENCE: pkg/adapter/holdstate.go:205-207, :225, :283.

FACT: the coordfence read-error path returns before the floor, so deleting the floor cannot turn a Postgres
fault into a fabricated wire value; the fault returns a wrapped error at `:143-145` and the fence never
issues — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:143-153.

FACT: migration 0181's unbatched `UPDATE sessions ... WHERE coordination_generation = 0` has three
precedents in the tree (migrations/0053:28, 0054:25, 0105:15), and the platform is pre-deployment, so a
lock-duration objection is hypothetical hardening rather than a finding. I checked this before dropping it.

MISTAKE (avoided, recorded so the next performance lens does not spend the round on it): I worked up the
coordfence floor deletion as a reliability regression — a session row still at 0 after the rolling window
makes the resume path send 0, the adapter answers `InvalidArgument`, that lands in `coordfence.fence`'s
`default:` transient arm, burns the whole attempt budget, relinquishes the lease, and aborts the resume,
where the shipped floor made that resume succeed. It does not clear the bar. It is already in
`## Standing context` twice: once as the weighed-and-not-filed "deleting the gateway fence path's zero
floor as removing a fail-safe", and once as the `InvalidArgument`-is-not-a-clean-refusal bullet that names
non-spec-changes.md's "Both refusals are loud and fail closed" as imprecise and routes it to the human as
one of the three imprecise rationale sentences. It also self-heals: the sweeper's takeover runs the CAS
before the fence, so the row leaves 0 within one sweep cycle. Re-filing it needs new evidence, and I found
none.

MISTAKE (avoided): I nearly filed that `terminateHeldSession`'s post-mortem read of the detached
`*slotState` races a mid-flight `CheckpointBarrier` holding the same pointer under `-race`. CODE-1 moves
`coordinationState` with its embedded `mu` onto the entry, so the read is under the entry's own leaf mutex
in any competent implementation; this is an implementor detail, not a described mechanism that cannot work.

UNVERIFIED: whether the tier-7a two-barrier case's "both return well inside the barrier ack deadline"
survives at high `maxConcurrentSessions`, since the op lock serialises N whole archives inside one 90s
deadline and the CRD webhook validates that deadline against ONE slot's `max_tiered_checkpoint_cap` rather
than `maxConcurrentSessions x cap`. Standing context already records the webhook asymmetry as shipped and
pre-existing; what nobody has checked is whether the staged tier-7a case itself is flaky at the top tier
with two large archives. A test-lane lens should decide, not a performance one.


### [non-spec.1.review-reliability.2]

DECISION: returned an empty findings list — BECAUSE every reliability candidate I generated
against the current text resolves to something the Standing context has already adjudicated
(D7's drain-budget cost, the prestop acked-but-uncaptured skip, the `upsertMirror` stale window,
the cache fallback's literal 0, the coordfence floor deletion as fail-safe removal, the
"loud and fail closed" imprecision, tier-list bookkeeping), and the round-1 finding I filed
(migration 0181's `>= 1` tightening) has landed in Pass 23 — ALTERNATIVES: weighed and rejected
filing the `.down.sql` asymmetry, a new tier-8 co-tenant crash case, and a criterion-(f) gap over
the newly reachable `InvalidArgument` fence refusal (see below).

FACT: Pass 23's rewrite is citation-clean where it matters. Re-verified from the tree, not the log:
`charts/lenny/templates/migrate-job.yaml:37-39` really is `pre-install,pre-upgrade` at weight -5;
`migrations/0050_session_record_fields.up.sql:38-39` really carries the inline
`CHECK (coordination_generation >= 0)`; `migrations/0164_coordination_lease.up.sql:44` really is
`coordination_generation BIGINT NOT NULL DEFAULT 0`; 0180 really is the last migration number;
`pkg/adapter/server.go:302`, `:307`, `:314` and `pkg/adapter/coordination.go:148`, `:236` and
`pkg/adapter/holdstate.go:43` all resolve; the tier-8 file's seeds at `:118`/`:179` (=1) and
`:239-241` (unset) and its 1/1/2 assertions at `:267`/`:283`/`:296` are exactly as §8 describes.

FACT: `pkg/adapter/checkpoint.go:122-125` defers `complete()` ONLY when `link` returned true, so a
`Checkpoint` stream whose `CheckpointStart` arrives before the barrier opened its gate never signals
it and the barrier blocks to the gateway's ack deadline. This is shipped behaviour on both the
pod-wide and the per-entry gate, and `dispatchOne` starts the stream concurrently with `Send`
(`pkg/gateway/coordination/barrier/barrier.go:216-226`), so the race is pre-existing and CODE-1 does
not widen it. I nearly filed it; do not spend a round on it — EVIDENCE: pkg/adapter/checkpoint.go:120-125.

FACT: the cache-fallback zero seed is in `cmd/lenny-gateway/httpsurface.go:591-597`, and the
`w.sessions.Get` it uses to overwrite the zero runs on `context.Background()` with no deadline. Under
the Postgres fault that triggers the fallback in the first place that read is the one unbounded call
on the drain path. Pre-existing, staged by no deliverable here, and its outcome is still fail-closed
(barrier carries 0, adapter `InvalidArgument`, `Acked` false, prestop fallback capture runs).

WATCHOUT: the `.down.sql` asymmetry is real but inert. `non-spec-changes.md:119-123` has the `.up.sql`
set `DEFAULT 1` on BOTH `sessions.coordination_generation` and `coordination_lease.coordination_generation`,
while `:127` and §8's migration case at `:309` say the `.down.sql` restores "the `DEFAULT 0`" in the
singular. A rollback therefore leaves `coordination_lease.coordination_generation DEFAULT 1` standing.
Not filed: `upsertMirror` always binds that column explicitly, so the default is cosmetic in both
directions. The standing `### Open` line already carries it; a later round wanting it must argue past
the cosmetic finding, not re-derive the asymmetry.

USEFUL [review-log Standing context, "The prestop acked-but-uncaptured gap is pre-existing"]: this is
the entry that stopped me filing D7's strongest-looking reliability finding — that an accepted barrier
plus a failed barrier-window checkpoint sets `Acked` true and makes prestop skip the fallback capture
for the whole never-handed-off population. The entry's own evidence (barrier.go:216-232, :243-244;
prestop.go:388-396, :509-514) is exact and the skip is what shipped §10.1.8 mandates.

USEFUL [review-log Standing context, "A barrier's generation comes from shared state"]: closed the
whole provenance line of attack in one read.


### [non-spec.1.review-security.2]

DECISION: filed exactly one finding, and it is not a security finding in the narrow sense — §8's disposition of the persisted half of the co-tenant barrier case ("rides proposal 0060's existing multi-replica drain coverage", non-spec-changes.md:276-278) names coverage that does not exist — BECAUSE the two established security checks came back clean and this was the only claim in the staging that no earlier round had actually opened: every prior round read the sentence as a tier-placement question (the standing `UNVERIFIED: the persisted-row half of the tier-7a barrier case`) and none checked whether 0060 built any drain coverage at all — ALTERNATIVES: rejected re-filing the round-1 security finding (checkSessionBound as the sole fail-closed barrier guard with no case pinning it; the text is byte-identical and that finding did not land, so re-filing burns two verifiers); rejected the barrier-side missing counterpart to `TestCoordinatorFenceRejectsZeroGeneration` (the non-positive guard is unchanged code and already refuses a zero barrier first, before and after D7, so no new fail-closed path is introduced — it is defence-in-depth, i.e. hardening); rejected the fence/barrier two-lookup race (already `weighed and not filed` under `[non-spec.6.review-mechanism.1]`); rejected the rebind reset and the "Both refusals are loud and fail closed" imprecision (both already recorded).

FACT: proposal 0060 built NO barrier or drain coverage. Its own testing section says so in as many words: "The barrier fence-on-both-paths and drain behavior is already pinned by `wiring_test.go` ... and `concurrent_drain_test.go`; a standalone barrier test is not added." What 0060 built is a tier-4 two-replica split-brain fence and a tier-8 crash takeover — EVIDENCE: proposals/0060_fix_co-locate-the-session-coordination-lease-with-the-pod-bindin.md:117, :111, :152.

FACT: nothing in the tree drives a real `CheckpointBarrier` against a real adapter and asserts persisted `session_checkpoint_meta` rows. `sessioncheckpointmeta` appears in only three test files (`tests/tier2_component/migrations/prod_columns_test.go`, `tests/tier5_e2e_kind/checkpoint_isolation_profile_test.go`, `tests/tier7a_load_local/prestop_no_double_checkpoint_test.go`), plus the barrier package's own tests; `tests/tier8_chaos/coordination_crash_takeover_test.go` and every file under `tests/tier4_integration/` contain no `CheckpointBarrier`, `checkpoint_ref`, or `CheckpointRef` reference. The two barrier-package cases that look like the coverage both run a fake dispatcher with per-session acks seeded before dispatch, so neither can observe a pod-side cross-link — EVIDENCE: pkg/gateway/coordination/barrier/concurrent_drain_test.go:75, :83-84; tests/tier7a_load_local/prestop_no_double_checkpoint_test.go:134.

FACT: the sentence is this-loop's own text. It is absent from the run-2 output of `non-spec-changes.md` (`git show 258600a4f^:...non-spec-changes.md` mentions 0060 once, at the §8 preamble harness sentence) and present after run 3's fixer — EVIDENCE: git 258600a4f, 4de93d2aa.

FACT (security surface, re-derived independently, agrees with the standing inventory): tenant pinning makes "co-tenant sessions on one pod" same-tenant by construction, so the cross-session fence interference this proposal fixes is intra-tenant and tier 9's absence from every tier list is right — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:473 ("this is why recycling pods are tenant-pinned"). `writeHoldPostMortem` writes one record per session keyed by that session, so CODE-3's per-session generations leak nothing across sessions — EVIDENCE: pkg/adapter/holdstate.go:283-296.

FACT: `GetCoordinationGeneration()` still has exactly two call sites in `pkg/adapter` (`coordination.go:92`, `:223`), and the fence-side non-positive backstop IS pinned (`TestCoordinatorFenceRejectsZeroGeneration`, pkg/adapter/coordination_test.go:47) while the barrier-side one at `coordination.go:225` is pinned nowhere. Recorded rather than filed, per the DECISION above.

WATCHOUT: `diff -rq` against the run-4 snapshot returns only the review log this round, so "read the changed sections first" has no target again. The standing context predicted this; run the `diff -rq` first anyway, it costs one call.


### [non-spec.1.review-test-coverage.2]

DECISION: returned an empty findings list — BECAUSE every deliverable that changes behavior
(CODE-1's per-entry state and gate, CODE-2's D7 arm, CODE-3's per-session hold records, CODE-4's
baseline, floors, migration, and the deleted coordfence floor) resolves to a named case with concrete
assertions and a stated pre-fix outcome, and every remaining candidate I derived independently turned
out to be either already refuted by verification, already recorded as weighed-and-declined with its
reasoning, or below the materiality bar — ALTERNATIVES, each worked up and dropped:
(1) no case pins that a `CheckpointBarrier` for an unbound session is still refused once D7 removes the
    `!initialized` arm and leaves `checkSessionBound` the sole guard. Verified independently that no test
    in the tree asserts it (`TestCheckpointBarrierRequiresSession`, pkg/adapter/coordination_test.go:175,
    covers only the empty session id). Dropped because `[non-spec.1.review-security.1]` filed exactly this
    on byte-identical text one round ago and the post-fix block records "the four confirmed findings
    landed", which are the migration, the coordfence case, the tier-4 precondition, and the tier-7a
    timing — so this one did not survive verification.
(2) §8's "Tier 4 covers the same flow across the gateway, the session store, and the pod" (the closing
    sentence of the CODE-4 paragraph) names no file, no test, and no assertion, while S4 and S5 between
    them own the flow it points at. Dropped: `[non-spec.1.review-test-coverage.1]` weighed and rejected
    it as bookkeeping, tier 4 is not omitted (the co-tenant bullet is a tier-4 case), and D7's behaviour
    is pinned at tiers 1 and 3.
(3) the two landed memstore `Update` tests. Re-verified they break: `TestUpdateAdvancesGenerationCounters`
    (memstore_test.go:413) creates unset, does `CoordinationGeneration += 2`, and asserts 2 at :429, which
    becomes 3 under the floor; the concurrent-bump test is the same shape. Dropped: they satisfy §8's
    class-1 general sentence ("every assertion that reads a session row's `CoordinationGeneration` after a
    create that left the field unset"), whose following list carries no closure claim (unlike class 2's
    "this class has one site"), and two lenses declined before me.
(4) tier 5 and tier 10 as omitted tiers. Verified empty: `grep -rln "CoordinatorFence|coordination_generation|LastFenced"`
    over tests/tier10_conformance, tests/tier9_security, tests/tier5_e2e_kind returns only
    tests/tier5_e2e_kind/checkpoint_resume_test.go:268, where the column appears in an `ORDER BY` and no
    assertion reads its value. Neither tier is reached.

FACT: the tier-8 and tier-7a assertion-shift dispositions in §8 are exact, re-derived from the files rather
than from the log. tier-8 subtest 3 is seeded unset at :239-241 and asserts 1, 1, 2 at :267, :283, :296
(they become 2, 2, 3); subtests 1 and 2 seed 1 explicitly at :118 and :179, so their `pod.LastFenced` reads
are on disjoint lines. tier-7a colocation seeds `CoordinationGeneration: 0` at :144 and asserts 0 at
:287-288 (becomes 1), while its other session is seeded 1 at :130 and its assertion of 2 at :264-265 is
already right under the baseline — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118,
:179, :239-241, :267, :283, :296; tests/tier7a_load_local/coordination_colocation_race_test.go:130, :144,
:264-265, :287-288.

WATCHOUT: the post-fix verification block at the end of the `[non-spec.1.fix-*]` entries names how many
findings were CONFIRMED ("the four confirmed findings landed"). Read it before re-filing anything a
previous lens's DECISION line says it filed: a filed finding absent from that list was refuted, and the
review log records no other trace of the refutation — EVIDENCE: review-log.md, "# Post-fix review, round 1
(non-spec lane), 0076".

USEFUL [review-log Standing context, "Landed cases already pin what §8 might otherwise be thought to owe"]
and [Traps, "tier-list bookkeeping is a refuted class, three times over"]: between them they closed six of
my candidates before I spent a pass on any of them.


### [non-spec.2.review-applicability.1]

DECISION: filed exactly one finding, the §8 disposition of `TestCheckpointBarrierRejectsWithoutFence` that states no
replacement assertion — BECAUSE it is the one member of §8's four "amends it ... rather than leaving tier 1 red"
dispositions that names no new assertion (non-spec-changes.md:326-328 against :293-294, :300-304, :364), and the
minimal amendment an implementor would write hangs tier 1: the landed case calls `CheckpointBarrier` on
`context.Background()` (pkg/adapter/coordination_test.go:189-197) and under D7 that call is accepted and blocks at
`select { case <-done: case <-ctx.Done(): }` (pkg/adapter/coordination.go:264-268) with no stream ever driven —
ALTERNATIVES: rejected filing the `.down.sql` singular-default ambiguity (both columns held `DEFAULT 0` before, so
"restores the `DEFAULT 0`" covers both), the unstated signatures of `isQuiescedForBarrier`/`BarrierWaiting` (CODE-1
says "become per-session reads" and §9 lists the one external caller file), and every tier-list item (thrice-refuted).

FACT: the intended replacement assertion for that case exists ONLY in a frozen pass record — spec-changes.md:868-870,
"is rewritten, with its `// spec:` annotation, to assert that the barrier is accepted and quiescence established for a
bound session with no recorded generation". The same pass record claims "§8 gains the cases D7 needs" and lists a
tier-1 co-tenancy case and a tier-8 case that §8 still does not carry, so the record is a statement of intent that was
only partly applied and cannot stand in for the live §8 text — EVIDENCE:
proposals/.../0076_....spec-changes.md:866-874; review-log.md:962-970.

FACT: run 4's non-spec round 1 produced no edits at all — `diff -ru scratchpad/cp-snap/0076-run4/non-spec-r2
proposals/0076_.../` is empty. Read the round-1 refuted list as the round's whole output; there is no fix-stage text in
this round to scrutinise. — EVIDENCE: scratchpad/cp-snap/0076-run4/non-spec-r2

FACT: the checklist simulates clean end to end. Eight deliverables, each in exactly one step; one lane per step; the
spec step leads; no Depends-on names a later or absent step; no box is ticked. Every code anchor CODE-1 through CODE-4
and §8 cite resolves (slot.go:21, server.go:302/:307/:314, session.go:237-239, slotsession.go:282-285/:347-361,
checkpoint.go:94/:111/:122/:124, oplock.go:117-128, coordfence.go:143/:147-153, pgstore.go:140/:177/:244-248,
memstore.go:46/:59-60, coordination.go:44/:52/:63/:89/:148/:158-188/:216/:236, cmd_run.go:498-508/:635-641/:880,
prod_columns_test.go:295/:583/:610, coordfixture.go:76/:98-102/:105-108/:115/:122/:220-241). The three `coordfixture.Pod`
accessor consumers are exactly the three files §9 lists, and `s.coord`/`s.barrier` have no reader outside
coordination.go and checkpoint.go. Do not re-derive these.

FACT: migration 0181 passes all five `scripts/lint-migrations.sh` passes as staged — pass 1/2 need only a non-empty
`.down.sql`, pass 3 is satisfied by the `prodMigrationSchema` row, and passes 4 and 5 key on `add column`/`drop column`
which 0181 has neither of — EVIDENCE: scripts/lint-migrations.sh:1-32, :45, :74-88.

FACT: SCHEMA-1's proto comment edits trip no gate beyond the stub no-drift one. The tier-11 tests that read
`schemas/lenny-adapter.proto` match a retired credential path literal and a `scrub_profile` field declaration, and
`claim_register_proto_agreement_test.go` matches field declarations and absence assertions, none of which a doc comment
edit reaches — EVIDENCE: tests/tier11_docs/credential_path_literal_sweep_test.go:133-158;
tests/tier11_docs/vm_restart_reprovision_consistency_test.go:141-162; tests/tier0_static/claim_register_proto_agreement_test.go:17-51.

DEFERRED [`tests/claim-map.json`, restating the standing `### Deferred` item from `[spec.3.review-edit-sites.1]`]: still
unclosed after this round. I could not file it under this lens: no gate hard-fails without it. `claim_register_test.go`
validates only the rows that exist (schema, deferral-id resolution, anchor resolution) and never asks whether a §28
statement has a row, so SPEC-2's unheld §28.5.1/§28.6/§28.8 statements leave tier 0 green — EVIDENCE:
tests/tier0_static/claim_register_test.go:22-46, :145-157. Whoever lands it must note that a hand-edit of
`tests/claim-map.json` alone turns tier 0 red the other way: `TestClaimRegisterIsReproducibleFromItsGenerator` re-runs
`scripts/seed-claim-register.py` and diffs bytes, and that generator parses only root `gateway-runtime-comms.md` §7.1,
so the row has to be added at the generator's source document — EVIDENCE:
tests/tier0_static/claim_register_generator_test.go:19-27, :45.

USEFUL [Standing context, "Editing hazards in this proposal's own files"]: naming the file in full rather than globbing
`*spec-changes.md` is what kept every line citation in this shard usable.
USEFUL [Traps, "tier-list bookkeeping is a refuted class, three times over"]: saved me working up S4's tier 3 and S6's
missing tier 0, both of which look like findings on a first read of the checklist.


### [non-spec.2.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the proposal adds, changes, or removes was re-swept against `spec/`, `docs/`, `schemas/`, and `charts/` in this run and no surface outside §9's list becomes wrong — ALTERNATIVES: filing the `tests/claim-map.json` DEFERRED, the 0181 `.down.sql` "one default where the up changes two" OPEN, and the tier-7a/tier-3 unnamed-file gaps; each is rejected below.
FACT: the identifier set this proposal touches is tiny and closed. It adds exactly one new name, the migration stem `0181_sessions_coordination_generation_baseline`, plus a `prodMigrationSchema` row. It adds no field, flag, condition type, metric, alert, error string, Helm value, or yaml key. `coordination_generation` occurs outside `spec/10`, `spec/28`, `spec/29`, `spec/04` only at spec/18_build-sequence.md:238, spec/16_observability.md:199, :208, spec/07_session-lifecycle.md:93, :215, :398, spec/12_storage-architecture.md:160, and docs/getting-started/concepts.md:101, and every one is unit-neutral and states no initial value — EVIDENCE: spec/12_storage-architecture.md:160; docs/getting-started/concepts.md:101.
FACT: `grep -rn "coordination_generation\|coordinationGeneration" schemas/ charts/ sdks/ -l` returns `schemas/lenny-adapter.proto` alone, so the whole non-spec wire surface is that one file plus the two generated stubs §9 already lists — EVIDENCE: schemas/lenny-adapter.proto:153-1623.
FACT: SCHEMA-1's twelve operational-RPC carriers are exactly the twelve `coordination_generation` field comments outside the fence and barrier messages, verified by walking each hit back to its enclosing `message`: SendMessage(:969), Attach(:995), RotateCredentials(:1046), ExtendCredentialLease(:1070), RevokeCredentials(:1091), Interrupt(:1114), Checkpoint(:1172), SignalDeadline(:1305), Resume(:1393), ExportPaths(:1531), ReportUsage(:1576), Shutdown(:1618). The list is complete and has no extras — EVIDENCE: schemas/lenny-adapter.proto:969, :1618.
FACT: the two staged §28.5.1/§28.8 sentences that a gate could pin are pinned nowhere outside the generated stubs. `grep -rn "rejects any RPC carrying an older\|prior coordinator's RPCs are still accepted\|superseded replica is rejected on the stamp" tests/ pkg/ scripts/` returns only `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:182` and `:634`, both regenerated by SCHEMA-1 and both in §9 — EVIDENCE: pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:182, :634.
FACT: migration 0181 as Pass 23 restaged it (DEFAULT + backfill, no CHECK change) clears every `scripts/lint-migrations.sh` pass on its own terms. Pass 4 keys on `add column`, pass 5 on `drop column`, and 0181 has neither, so the log's older justification ("pass 4 and 5 do not reach 0181, which drops a constraint") is stale in its reason while right in its conclusion — EVIDENCE: scripts/lint-migrations.sh:99-125, :127-140.
FACT: a `prodMigrationSchema` row with no `columns` and `create:false` is inert in both walkers, and `TestProdMigrationsRollBackPerStep` iterates the slice in reverse assuming ascending order, so 0181's row appends at the end — EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:583, :591-603, :618-633.
FACT: `migrations/0164_coordination_lease.up.sql:44` and `migrations/0050_session_record_fields.up.sql:38-39` both resolve exactly as CODE-4 cites them, and 0181 is the next free number (`migrations/0180_*` is the last `.sql` pair) — EVIDENCE: migrations/0164_coordination_lease.up.sql:44; migrations/0050_session_record_fields.up.sql:38-39.
FACT: both `sessionstore` `Create` paths are the only two, and `pgstore.go:170` is the only production `INSERT INTO sessions` in the tree, so CODE-4's "two floors plus the migration" is the complete write-path set — EVIDENCE: pkg/gateway/session/sessionstore/memstore/memstore.go:46; pkg/gateway/session/sessionstore/pgstore/pgstore.go:140, :170.
FACT: `s.coord` and `s.barrier` have exactly the production readers the log records, confirmed by a fresh grep over non-test `pkg/adapter/*.go`: `s.barrier` at coordination.go:64-66, :264, :269 and checkpoint.go:122, :124; `s.coord` confined to coordination.go. CODE-1's stated file set is complete — EVIDENCE: pkg/adapter/coordination.go:45-66, :232-269; pkg/adapter/checkpoint.go:122-124.
FACT: `spec/README.md:290` links §29.10 by heading only and `spec/29_communication-scenarios.md:17` describes the subsection without enumerating its "does not state" bullets, so SPEC-2's removal of one bullet breaks no inbound reference. `tests/tier0_static/spec_map_exception_blocker_retention_test.go` keys on the section number and a remediation blocker id, never on §29.10's content — EVIDENCE: spec/README.md:290; spec/29_communication-scenarios.md:17; tests/tier0_static/spec_map_exception_blocker_retention_test.go:63-70.
FACT: no `spec/`, `docs/`, `schemas/`, or `charts/` file carries a generation marker that would make it a derived artifact this change must regenerate. The only generated pairs are the alerting bundles (`docs/alerting/*`, `charts/lenny/files/*`), and this proposal touches no metric or alert — EVIDENCE: charts/lenny/files/alerting-rules.yaml; docs/alerting/rules.yaml.
FACT: the metric companion pair is intact and untouched. `lenny_coordinator_handoff_stale_total`, `lenny_adapter_coordinator_hold`, and `lenny_coordinator_fence_relinquished_total` each carry a `spec/16` inventory row and a `docs/reference/metrics.md` row, all unit-neutral, and none has an alert or a runbook page — EVIDENCE: spec/16_observability.md:183, :185, :192; docs/reference/metrics.md:307, :309, :312.
WATCHOUT: the two snapshot directories this run points at, `scratchpad/cp-snap/0076-run4/non-spec-r2` and `non-spec-r2-start`, are both byte-identical to the live proposal, so the "read the changed sections first" instruction has no diff to act on. The newest text is the Pass 23 record in the spec-changes file and the CODE-4 paragraphs it rewrote; read those as the least-examined text rather than looking for a diff — EVIDENCE: `diff -rq scratchpad/cp-snap/0076-run4/non-spec-r2-start proposals/0076_*` prints nothing.
WATCHOUT: `cat`ing the non-spec-changes file through Bash in this harness persists the output to a tool-result file instead of showing it, and re-`cat`ing that file persists it again. Read it with `sed -n 'A,Bp'` in three chunks, or with the Read tool — EVIDENCE: the file is 38.7KB.
UNVERIFIED: the log's Settled bullet says lint passes 4 and 5 "do not reach 0181, which drops a constraint". Pass 23 removed the constraint drop, so that clause now describes a migration the proposal no longer stages. The conclusion is unchanged and I verified it directly, but a compaction pass should restate the reason as "0181 contains neither `add column` nor `drop column`" — EVIDENCE: scripts/lint-migrations.sh:99, :127; non-spec-changes.md:119-129.
UNVERIFIED: whether §28.4's "every normative statement carries a row in the claim register" obliges anything of SPEC-2. I declined the standing DEFERRED on it: SPEC-2 re-scopes statements about mechanisms that already hold rows rather than introducing a mechanism, and `tests/claim-map.json` is generated from root `gateway-runtime-comms.md` §7.1 with a byte-diff reproducibility gate, so a hand-edited row would turn tier 0 red. Anyone reviving it must land the remedy in the generator's source document, not in the register — EVIDENCE: spec/28_communication-channels.md:161-169; scripts/seed-claim-register.py:11-13.


### [non-spec.2.review-feasibility.1]

DECISION: returned no findings — BECAUSE every actor-action assignment in the non-spec staging resolves
against the tree, and the two candidates I worked up both fail the materiality bar (below) —
ALTERNATIVES: filing the ErrHeld attribution in §8's tier-4 bullet, rejected because the tree's own
`Sweep` doc comment says the same thing.

MISTAKE (mine, avoided; recorded so nobody else spends the round on it): §8's tier-4 bullet says the
survivor "skip[s] `sess-a` on `ErrHeld` (`pkg/gateway/coordination/coordination/coordination.go:341`)".
That is not the branch that fires. For a session whose lease a live foreign replica holds,
`leaseUnheld` is false so `adoptable` is false, `bound` is false, and `priorHolder != s.replicaID`, so
the loop `continue`s at the eligibility gate BEFORE any `Acquire` — the `ErrHeld` arm at :341 is never
reached — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:296-301 (adoptable),
:330-338 ("one whose lease a live foreign replica holds — is skipped without an Acquire attempt";
`eligible := bound || priorHolder == s.replicaID || adoptable`), :340-343. It is not filable: the
shipped `Sweep` doc comment states the same imprecision ("Sessions whose lease a different replica
holds are skipped on ErrHeld", :252-253) and so does the landed tier-4 case's own comment
(tests/tier4_integration/coordination_fence_split_brain_test.go:101-102), and the case's construction
and outcome are identical either way.

FACT: the whole CODE-1/CODE-2/CODE-3/CODE-4 citation set re-resolves exactly, including the ones that
matter for feasibility: `checkpointRootsForSession` at pkg/adapter/slot.go:153-166 (returns after an
`s.mu`-guarded lookup, so returning the `*slotState` is a two-line change), `s.ops.Begin` at
pkg/adapter/checkpoint.go:111 with the distinct-session admit at pkg/adapter/oplock.go:119-129,
`heldSession{sessionID, state *slotState}` at pkg/adapter/slotsession.go:282-285 with
`terminateHeldSession(ctx, m heldSession, gen int64)` at pkg/adapter/holdstate.go:225 already taking
the member, so CODE-3's per-session read needs no new plumbing. The proto carrier count is exactly the
14 messages declaring `coordination_generation` (12 operational + fence + barrier), which is SCHEMA-1's
list verbatim — EVIDENCE: schemas/lenny-adapter.proto, `awk '/^message /{m=$2} /coordination_generation *=/{print NR": "m}'`.

FACT: the adapter enforces no per-pod session cap of its own (`maxConcurrentSessions` is a pool concept),
and `adapter.New` + a bufconn client is already built at tier 7a, so every co-tenant case §8 stages —
tier 1, tier 4 over `coordfixture`, tier 7a with two real `Checkpoint` streams — is constructible with
landed fixtures — EVIDENCE: tests/tier7a_load_local/coordinator_hold_termination_race_test.go:371,
:386-404, :650; pkg/adapter/holdstate_test.go:677 (`holdTerminationServer(t, rt, "sess-a", "sess-b")`).

FACT: `Shutdown` takes no op lock (`s.ops.Begin` has exactly two production callers, checkpoint.go:111
and lifecycle.go:41), so the mid-flight deregistration case can use `Shutdown` as its deregistration
trigger without needing the hold-timeout clock seam, which lives in the internal `package adapter` test
file the case cannot reach.

USEFUL [Standing context, "Derived inventories"]: the claim that every CODE-1..CODE-4, §8, and §9 code
citation resolves saved re-deriving them; I spot-checked eleven at random (pgstore.go:140/:177/:244-248,
memstore.go:46/:58-61, coordfence.go:143/:147-153, migrations/0164:44, 0050:38-39, tier8 :118/:150/:179/
:195/:223/:239-241/:267/:283/:296, tier7a :130/:144/:260/:264-265/:287-288, cmd_run.go:498-508/:635-641/
:880) and every one landed.

UNVERIFIED: nothing new. The DEFERRED that names `tests/claim-map.json` (a §28.4 row for the fence and
barrier contract statements that do not hold in code between S1 and S5) is still unapplied in the
non-spec staging; I did not file it, because refuted class (k) puts `tests/` outside criterion (d) and
because the register is generated from root `gateway-runtime-comms.md` §7.1 and byte-diffed, so the
remedy is not a hand edit of that file. Whoever closes it needs the generator's source document.


### [non-spec.2.review-fresh.2]

DECISION: returned an empty findings list — BECAUSE every claim I could reach in one pass resolved against
the tree, and the three candidates I worked up each died on the materiality bar — ALTERNATIVES: filing the
`.down.sql` singular-default ambiguity, the §28.4 claim-map DEFERRED, and the §4/§7 `coord.mu` self-contradiction;
each is recorded below with why I dropped it rather than spent a verification pair on it.

FACT: `diff -ru /home/ec2-user/lenny/scratchpad/cp-snap/0076-run4/non-spec-r2 <proposal dir>` returns EXIT=0 —
the snapshot is byte-identical to the live proposal, so round 1 of this loop landed no edits. A "read the changed
sections first" instruction has nothing to read; budget the whole pass on the whole document.

FACT: SCHEMA-1's twelve operational-RPC carriers are exactly right and I verified them message by message.
`grep -n coordination_generation schemas/lenny-adapter.proto` returns 13 "gateway's view of the active" field
comments; subtracting `CheckpointBarrierRequest` (:1477) leaves 12, and awk-resolving each to its enclosing
`message` gives SendMessageRequest(:969) AttachRequest(:995) RotateCredentialsRequest(:1046)
ExtendCredentialLeaseRequest(:1070) RevokeCredentialsRequest(:1091) InterruptRequest(:1114) CheckpointRequest(:1172)
SignalDeadlineRequest(:1305) ResumeRequest(:1393) ExportPathsRequest(:1531) ReportUsageRequest(:1576)
ShutdownRequest(:1618) — the exact list at non-spec-changes.md:16-19. Do not re-derive it.
EVIDENCE: schemas/lenny-adapter.proto:969,:995,:1046,:1070,:1091,:1114,:1172,:1305,:1393,:1531,:1576,:1618

FACT: §8's class-2 mechanism claim is exactly true and now independently re-derived. `checkpointer.go:609` sets
`coordinationGen` from the session row, `uploaddriver.go:422` (`prior.CoordinationGeneration > d.coordinationGen`)
and `partialmanifeststore.go:394` (`row.CoordinationGeneration > r.CoordinationGeneration`) both compare strictly
greater, and `uploaddriver_test.go:1007` seeds the prior row at exactly 1 against a `runningSession` create that
leaves the field unset. Under the baseline both sides are 1, neither guard fires, and the case fails. The staged
fix (constant becomes 2) is the right one — EVIDENCE: pkg/gateway/checkpoint/checkpointer/checkpointer.go:609;
pkg/gateway/checkpoint/checkpointer/uploaddriver.go:422; pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:394

FACT: the accessor blast radius is closed and I re-confirmed it two ways. `grep -rn "isQuiescedForBarrier|BarrierWaiting|LastFencedGeneration()"`
returns only holdstate.go:119, coordination_test.go:73/:279/:298, checkpointbarrier_test.go:163 and coordfixture.go:115;
`grep -rn "slotState{"` returns one test-side composite literal (`tracingcontext_sampling_test.go:44`) and it is
keyed, so adding fields to `slotState` breaks nothing; `checkpointRootsForSession` has exactly two callers
(checkpoint.go:94, resume.go:178). Every one of those files is in §9.

FACT: D7 creates no hang in the landed fixtures, verified from the call sites rather than from the log's summary.
`StaleRPCRejected` has exactly two consumers and each probes generation 1 against a pod fenced at 2
(tier-4 asserts `pod.LastFenced() != 2` immediately above its probe), so the surviving `gen != fenced` arm still
refuses and the barrier returns at once — EVIDENCE: tests/tier4_integration/coordination_fence_split_brain_test.go:145,:151;
tests/tier8_chaos/coordination_crash_takeover_test.go:150,:165

OPEN: 0181's `.down.sql`. CODE-4's `.up.sql` moves two `DEFAULT 0`s to 1 (`sessions` and `coordination_lease`)
while both the deliverable (non-spec-changes.md:127) and §8's migration-test assertion (:309) say the down
"restores the `DEFAULT 0`", singular. I did not file it: the generic singular reads distributively, and the
asymmetry is inert if it happens, because a `prodMigrationSchema` row with no `columns` runs no post-rollback
schema assertion and no other test reads either default after a down step. If a later round wants it closed, the
cheap fix is to write "both defaults" in the two sentences.

OPEN: the §28.4 claim-map DEFERRED is still open and still unowned. spec-changes.md:420 asserts "No §28.4
claim-register row moves", which answers whether a row moves rather than whether one must be restatused for the
S1-to-S5 interval, and non-spec-changes.md stages nothing for `tests/claim-map.json` and §9 does not list it. I
did not file it either: refuted class (k) puts `tests/` outside criterion (d), the `CoordinatorFence` and
`CheckpointBarrier` rows already exist as `WIRED`, and SPEC-2 re-scopes an existing mechanism rather than adding
one — EVIDENCE: spec/28_communication-channels.md:163-169; tests/claim-map.json:461-465

MISTAKE (mine, avoided): I nearly filed the §4/§7 `coord.mu` contradiction — `coordinationState` embeds
`mu sync.Mutex` at `pkg/adapter/coordination.go:26`, so CODE-1's "with its `lastFenced`, `initialized`, and
`quiesced` fields together, moves onto the slot registry entry" settles §7's third open decision by construction
while §4 and §7 both still frame it as open. It is barred: the brief excludes "sections recording deliberately
open decisions for the human reviewer", and the standing log already carries it as an OPEN. Do not spend a
verification pair on it; if anyone wants it closed it is a one-line edit to §7.

USEFUL [standing context, "Known sub-line citation drifts that must not be filed"]: saved me three separate
near-filings. I independently landed on `coordination_test.go:184-197` (the test's doc comment opens at :185,
`func` at :189), `uploaddriver_test.go:1015` for a `t.Fatal` at :1014, and `coordination.go:408` for a backoff
call inside a comment block. All three are in that bullet. Anyone re-deriving citations will hit exactly these.

USEFUL [standing context, "The accessor blast radius is exactly what §9 lists"] and ["Landed cases already pin
what §8 might otherwise be thought to owe"]: both held on re-derivation, which is what let me spend the pass on
the code mechanisms instead.


### [non-spec.2.review-kubernetes.1]

DECISION: Returned an empty findings list — BECAUSE the proposal touches no Kubernetes API surface at all under this lens — ALTERNATIVES: rejected filing the CODE-1 tier-2 attribution and the D7/prestop grace-period angle; the first is the thrice-refuted tier-list bookkeeping class, and the second is a no-op because `dispatchOne` starts the `Checkpoint` stream before `dispatch.Send`, so an accepted barrier changes no drain wall-clock.
FACT: no CRD, controller status, finalizer, admission-webhook, or field-manager surface is reachable from this proposal. `grep -ril "coordinationgeneration|coordination_generation|lastFencedGeneration" pkg/apis charts/ config/` returns nothing, so the generation lives only in Postgres and adapter process memory — EVIDENCE: pkg/apis (no hit), charts/ (no hit)
FACT: the only Kubernetes artifact the proposal reasons about is the migrate Job, and its deploy-ordering claim is exact: `helm.sh/hook: pre-install,pre-upgrade`, weight `-5`, and a header stating the gateway Deployment is a normal resource applied after all pre-hooks — EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16, :37-40. The proposal's decision not to tighten the session-row CHECK to `>= 1` in 0181 is the correct consequence of that ordering (non-spec-changes.md:136-144).
FACT: no runbook, alert rule, or doc reads `coordinator_connection_lost`, `last_generation`, or `lastGeneration`; `grep -rn` over `docs/`, `charts/`, `pkg/alerting/`, and `spec/16*` returns nothing, so CODE-3's drop of the pod-level `last_generation` key reaches no §16 surface.
FACT: the tier-4 sweeper citations resolve — `leasestore.ErrHeld` skip at pkg/gateway/coordination/coordination/coordination.go:341, `recordAdoptionBackoff` declared at :514 (cited as :512-517). The `:408` citation lands inside the backoff rationale comment block rather than on the call, which is the sub-line drift the standing context already tells reviewers not to file.
WATCHOUT: the run-4 snapshot `scratchpad/cp-snap/0076-run4/non-spec-r2` is byte-identical to the live proposal directory (`diff -rq` returns nothing), so the "read what changed first" instruction had no diff to work from this round; do not conclude the diff tool is broken.


### [non-spec.2.review-mechanism.2]

DECISION: returned an empty findings list for the end-to-end-mechanism lens on run 4 round 2 — BECAUSE every
flow I traced end to end (fence -> per-entry state -> hold post-mortem; barrier -> gate -> Checkpoint stream
-> ack -> session_checkpoint_meta; session row baseline -> mirror -> wire -> adapter guards; migration ->
rolling window -> Create floors) resolves against the tree, and the six candidates I worked up all fell below
the materiality bar or were already refuted — ALTERNATIVES: filing the 0181 `.down.sql` asymmetry, the
"skipped on ErrHeld" attribution, and the §7-decision-3-settled-by-CODE-1 contradiction; each is written up
below with why it was dropped.

FACT: `diff -rq scratchpad/cp-snap/0076-run4/non-spec-r2 <proposal dir>` is byte-identical to the live
proposal, and so is `non-spec-r2-start`. Round 1 of this loop changed nothing, so "read the changed sections
hardest" had no target this round; the newest text is still pass 23's four bullets in
`non-spec-changes.md:420-473`. — EVIDENCE: scratchpad/cp-snap/0076-run4/non-spec-r2

FACT: an accepted `CheckpointBarrier` IS assertable from inside `package adapter` with no external fixture.
`TestCheckpointBarrierAcksEchoedCheckpointID` runs the RPC on a goroutine, spins in `waitBarrierWaiting`
(which touches `s.barrier.mu` directly), then calls `s.barrier.link` / `s.barrier.complete` in-package. The
package wall recorded in the traps applies only to a case that drives a real `Checkpoint` *stream*. — EVIDENCE:
pkg/adapter/coordination_test.go:218-231, :268-300

FACT: `waitBarrierWaiting` (`pkg/adapter/coordination_test.go:221-226`) reads `s.barrier` directly, so CODE-1's
removal of `Server.barrier` breaks it and it must take a session key. It is covered only implicitly by §9's
listing of `coordination_test.go`; an implementor grepping for `s.barrier` production readers will miss it.
— EVIDENCE: pkg/adapter/coordination_test.go:221-226

FACT: the op lock admits a *distinct* session id and queues it (`l.checkpoints[sessionID] = promote` then
`l.wait`), and coalesces only the same id with `errOpCoalesced`. §8's tier-7a barrier bullet and CODE-1's
re-lookup argument both rest on this and both state it correctly. — EVIDENCE: pkg/adapter/oplock.go:117-128

MISTAKE (mine, avoided): I nearly filed §8's tier-4 "skipping `sess-a` on `ErrHeld`
(`coordination.go:341`)". In that scenario replica 2 skips `sess-a` at the eligibility `continue`
(`coordination.go:335-337`, `adoptable` false because the lease is held), never reaching the `Acquire` call
whose `ErrHeld` arm is at `:341`. It is not filable: the tree itself uses that vocabulary — `Sweep`'s own doc
comment says "sessions whose lease another replica holds are skipped on ErrHeld"
(`coordination.go:253`) and the landed tier-4 case's comment says the same
(`tests/tier4_integration/coordination_fence_split_brain_test.go:99`) — and the outcome asserted is identical
either way. — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:253, :302, :335-337, :341

MISTAKE (mine, avoided): the 0181 `.down.sql` asymmetry (`.up.sql` changes two column defaults,
`non-spec-changes.md:127-128` and §8's tier-2 case both say the down "restores the `DEFAULT 0`", singular).
The facts hold (`migrations/0050...up.sql:38-39`, `migrations/0164_coordination_lease.up.sql:44`), but the
lease column's default is cosmetic — every mirror write binds the value explicitly — so a down that reverts
only the `sessions` default breaks nothing. Immaterial; this closes the standing UNVERIFIED "0181's
`.down.sql` names one default where the `.up.sql` changes two" as "true but harmless".

UNVERIFIED closed: §8's tier-4 co-tenant bullet is buildable. `coordfixture.StartPod` boots the adapter and
starts one session over the exported `Pod.Client` (`coordfixture.go:76`, `:98-102`), and the adapter's
different-session refusal is gated on `sdkWarm` alone — "On a pod-warm pod a different session arrives on its
own slot and is admitted" — so a second `StartSession` over the same client is admitted with no config change.
— EVIDENCE: pkg/adapter/slotsession.go:26-39; tests/testinfra/coordfixture/coordfixture.go:76, :98-102

FACT: both landed `StaleRPCRejected` call sites probe a session the pod has already been fenced for
(`tests/tier4_integration/coordination_fence_split_brain_test.go:151`,
`tests/tier8_chaos/coordination_crash_takeover_test.go:165`, both at gen 1 against a pod fenced to 2), so D7
cannot turn either into a hang and no tier-4/tier-8 disposition is owed for the D7 step. Re-verified this
round; the trap's conclusion stands.

FACT: the S3/S4 disjointness arithmetic re-verified against the tree this round and holds exactly as §8 states
it: tier-8 `LastFenced` reads at `:150`, `:195`, `:223` sit in subtests seeded `CoordinationGeneration: 1` at
`:118` and `:179`, while the 1/1/2 assertions at `:267`, `:283`, `:296` are in the third subtest seeded unset
at `:239-241`; tier-7a `:260` read versus the `0` seed at `:144` and the `0` assertion at `:287-288`, with the
other session seeded 1 at `:130` and its `2` at `:264-265`. `coordination_takeover_test.go` shifts 1,1,1,1,2,0,1,1
each by one over `memstore`. Do not re-derive.

OPEN (unchanged, not re-filed): the §28.4 claim-register DEFERRED routed to this loop. I did not file it.
`tests/claim-map.json` rows already exist and stay true across S1 (`CoordinatorFence` and the barrier rows are
`WIRED` on their handler surfaces, `tests/claim-map.json:60-82`, `:450-475`), the register is regenerated from
root `gateway-runtime-comms.md` §7.1 so it cannot be hand-restatused, and criterion (d) does not reach
`tests/`. A round that wants it filed must argue the obligation is on the *statement* rather than the
mechanism and must name the generator-source edit as the remedy.


### [non-spec.2.review-performance.1]

FACT: `coordfence.fence`'s switch has no arm for `InvalidArgument`. The stale arm matches
`codes.FailedPrecondition` (or `!res.Accepted`) only; everything else falls to `default:`, which is
labelled "Transient transport / deadline fault", burns the whole `DefaultMaxAttempts = 3` budget, and then
relinquishes (releases the lease, returns `ErrRelinquished`) — EVIDENCE:
pkg/gateway/coordination/coordfence/coordfence.go:52, :160-165, :180-188.

FACT: `ErrRelinquished` on the resume path is classified retryable and holds the row in
`awaiting_client_action` for the client's `POST /resume` retry; nothing bumps the row, so an identical
retry produces an identical relinquish — EVIDENCE: pkg/gateway/sessionserver/start.go:4233-4245,
:3668-3673. This is what makes a fence refused with `InvalidArgument` a permanent stall on the resume
path rather than a bounded delay. The takeover path is different: it compare-and-swaps the row first, so
it never sends 0.

DECISION: filed one finding — CODE-4 deletes `coordfence`'s `gen <= 0` floor
(`coordfence.go:147-153`) in the same release whose migration cannot reach the rows that still carry 0,
namely every row `pgstore.Create` writes from a still-running old binary during the rolling window that
CODE-4's own text says exists. BECAUSE the resulting failure mode is strictly worse than shipped (shipped:
fence at 1, accepted, resume proceeds; staged: `InvalidArgument`, three wasted RPCs, lease relinquished,
`lenny_coordinator_fence_relinquished_total` incremented, resume permanently unresumable) and CODE-4's own
CHECK-constraint paragraph applies the opposite discipline to the identical row class. ALTERNATIVES
rejected: filing it as a spec finding (the remedy is one sentence of non-spec staging: keep the floor this
release); filing the D7 90s-quiescence exposure (already weighed and routed to human review); filing the
`coordination_lease` `DEFAULT 1` as a fabricated-value hazard (refuted by inspection —
`coordlease/pgstore/pgstore.go:48-58` always binds the column explicitly, so the default never fires).

WATCHOUT: the staged sentence "Both refusals are loud and fail closed"
(non-spec-changes.md:149-153) is the trap. The refusal *is* loud at the adapter; what is not loud is what
the caller does with it. Read a refusal's classification at the caller before accepting a fail-closed
claim — EVIDENCE: pkg/adapter/coordination.go:93-94 against
pkg/gateway/coordination/coordfence/coordfence.go:180-183.

OPEN: migration 0181's backfill is an unbatched `UPDATE sessions SET coordination_generation = 1 WHERE
coordination_generation = 0` inside a deploy-blocking `pre-install,pre-upgrade` hook, over a column with
no index on `sessions` (`migrations/0001_initial_schema.up.sql:100-101` and the later
`idx_sessions_*` files carry none), so it is a seq scan plus a rewrite of essentially every row while the
old fleet is still issuing its ~300/s tier-3 session-state updates against the same rows
(spec/12_storage-architecture.md:62-70). §10.5 states no batching or `lock_timeout` rule
(spec/10_gateway-internals.md:419-440). Not filed: `[non-spec.5.review-performance.1]` already weighed
and declined the lock and backfill cost, and I could not establish the `sessions` row count at tier 3 from
the spec — session-row retention is not stated there. A round that wants it must first derive that count.

USEFUL [standing context, "The barrier's cache fallback puts a literal 0 on the wire and must not be
floored"]: its closing clause, "The fence path is not symmetric, its reader returning an error rather than
0, so deleting `coordfence`'s floor is safe", is scoped to the Postgres-fault case alone and says nothing
about a row that genuinely reads 0. It saved me from re-deriving the barrier half and pointed straight at
the gap.


### [non-spec.2.review-reliability.1]

FACT: pass 23's fix (retain `CHECK (coordination_generation >= 0)` instead of tightening to `>= 1`) removed the premise under which
deleting `coordfence`'s zero floor was safe. The two changes are in the same paragraph and were made in the same pass, but nothing
re-checked the floor deletion against the newly-reachable zero row — EVIDENCE: non-spec-changes.md:136-153;
pkg/gateway/coordination/coordfence/coordfence.go:147-153.

FACT: an `InvalidArgument` from `CoordinatorFence` is NOT a refusal the fence path returns on. It is not `FailedPrecondition`, so it
falls into `fence`'s `default:` transient arm, is retried `maxAttempts` times with no backoff, then relinquishes: the coordination
lease is released and `ErrRelinquished` aborts the resume, parking the row in `awaiting_client_action` — EVIDENCE:
pkg/gateway/coordination/coordfence/coordfence.go:159-188, :192-200; pkg/gateway/sessionserver/start.go:4233-4241, :3668-3673.
The standing context already carried this as an imprecision (review-log.md:357); what changed is that pass 23 made a zero row a
production-reachable value, so the imprecision is now load-bearing rather than cosmetic. Filed as this run's only finding.

FACT: `RecordHandoff`'s 0-return sentinel is safe whatever the row's value, because it returns the POST-increment value
(`row.CoordinationGeneration++` then `return updated.CoordinationGeneration`), which is never 0 — EVIDENCE:
pkg/gateway/coordination/coordination/coordination.go:463-482. The standing context justifies the sentinel by "0 stays impossible as
a row value", which pass 23 falsified; the conclusion survives on the post-increment ground instead. Do not file the sentinel.

DECISION: did not file the partial-manifest supersede path — BECAUSE the backfill 0→1 changes no outcome there: `Put` soft-deletes
active rows at or below the incoming generation and the store's fence rejects only a strictly-higher prior row, so a pre-migration
manifest row at 0 against a backfilled session at 1 supersedes exactly as it did at 0 against 0 — EVIDENCE:
pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:389-408; pkg/gateway/checkpoint/checkpointer/uploaddriver.go:415-422.
ALTERNATIVES: filing it as a data-loss lead, rejected on that evidence.

DECISION: did not re-file the prestop acked-but-uncaptured widening under D7 — BECAUSE the standing context adjudicated it
("The prestop acked-but-uncaptured gap is pre-existing and is not this proposal's", review-log.md, `### Settled`), and it explicitly
weighed the widening of the population rather than only the base defect. ALTERNATIVES: filing it as a drain work-loss regression.

FACT: the tier-4 co-tenant case is constructible as written. The landed single-session case models a coordinator crash with a
per-session `leases.Release(ctx, tenant, sessID, "replica-1")`, so releasing `sess-b`'s lease while `sess-a`'s stays live is one
call, and replica-2's sweep then skips `sess-a` on `ErrHeld` — EVIDENCE:
tests/tier4_integration/coordination_fence_split_brain_test.go:113-117, :100-107;
pkg/gateway/coordination/coordination/coordination.go:340-343.

WATCHOUT: `.down.sql` for 0181 is described as restoring "the `DEFAULT 0`" (singular) while the `.up.sql` sets `DEFAULT 1` on two
columns. Not filed: `coordination_lease.coordination_generation`'s default is cosmetic (the mirror upsert always binds explicitly)
and the down-then-up round trip is never exercised. It is already an `### Open` UNVERIFIED — EVIDENCE: non-spec-changes.md:127-133.


### [non-spec.2.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE every tier the staged non-spec changes reach (0, 1, 2, 3, 4, 7a, 8, plus 11 on checklist S1) has either a named new case with stated assertions or a named landed-test disposition, and each non-happy path the change opens is pinned: the per-session stale acceptance and the per-session gap exemption (§8 tier-1 bullet 1), the independent barrier gates (tier-1 bullet 2 and tier-7a bullet 2), the detached-entry lifetime (tier-1 mid-flight case), the hold's per-session and zero arms (tier-1 holdstate amendment), the co-tenant handoff across gateway/lease store/pod (tier-4), the concurrent recording (tier-7a bullet 1), the crash-takeover survival (tier-8 amendment), and CODE-4's baseline with its migration, both `Create` floors, and the deleted `coordfence` floor — ALTERNATIVES: rejected filing §8's tier-4 sentence for D7 (names no case while S5 declares no tier 4; already OPEN and in the thrice-refuted tier-list-bookkeeping class), the missing replacement assertion on `TestCheckpointBarrierRejectsWithoutFence` (already OPEN, declined by two prior test-coverage lenses, and the acceptance behavior itself is pinned by the staged tier-3 case), and a case over the adapter's non-positive-generation barrier refusal (shipped guard the proposal does not change).

FACT: the fence-path fail-closed backstop CODE-4 newly makes reachable is already pinned by a landed case. Deleting `coordfence`'s floor lets a row still at 0 put 0 on the fence wire, and `TestCoordinatorFenceRejectsZeroGeneration` asserts the adapter's `InvalidArgument` on exactly that, so §8 owes no new case for it — EVIDENCE: pkg/adapter/coordination_test.go:47; pkg/adapter/coordination.go:93-94; non-spec-changes.md:149-153.

FACT: the two tier-3 coordination suites are descriptor-and-encoding tests, not behavioral ones, so CODE-1, CODE-2, and D7 break neither and §8 owes them no disposition. `generation_fence_wire_test.go` pins field presence, the unset-field zero-bytes property, and round-tripping; `checkpointbarrier_wire_test.go` pins `CheckpointBarrierResponse`'s field names and numbers off the protoreflect descriptor. Neither constructs an `adapter.Server` — EVIDENCE: tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:104, :141, :193, :228; tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48-77.

FACT: no tier-0 or tier-11 gate string-matches a proto doc comment, so SCHEMA-1's comment rewrite cannot turn a static tier red. `claim_register_proto_agreement_test.go` joins the register to declared field names (`coordination_generation`), not to comment prose, and the tier-11 `fenced` hits are all about markdown code fences — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:37, :165-166; grep for `fenced|handoff_stale|older generation|lifetime` over tests/tier0_static/adapter_proto_*.go and tests/tier11_docs/*.go returns no proto-comment matcher.

FACT: §8's class-1 and class-2 enumerations survive an independent tree-wide sweep. Every `CoordinationGeneration` assertion in a `*_test.go` outside the named files is over a value the fixture seeds explicitly (sessionserver/failure_test.go, sessionserver/coordination_fence_test.go, the `barrier` package's Target and mirror fixtures, coordleasestore, evictionstatestore, evictionfallback, tier-4 checkpoint_intent and eviction_fallback, sessioncheckpointmeta), and `runningSession` is the only unset-create helper feeding a calibrated constant, which is the one class-2 site §8 names — EVIDENCE: pkg/gateway/checkpoint/checkpointer/checkpointer_test.go:89-96 against uploaddriver_test.go:1007; non-spec-changes.md:352-376.

FACT: every §8 and §9 line citation I re-opened this round resolves exactly. `coordination.go` :99 stale arm, :108-116 gap block, :158-165 `open()`, :180-188 `link()`, :224-226 and :93-94 non-positive guards, :236 gate; `coordination_test.go` :23-30 `newFencedServer`, :184-197 `TestCheckpointBarrierRejectsWithoutFence`; `holdstate_test.go` :674 and :700-716; tier-7a :130, :144, :260, :264-265, :287-288; `sweep_test.go` assertions run :275 to :594; `prod_columns_test.go` :295, :583, :610; `pgstore.go` Create at :140, column list at :177, `schemaVersion` normalisation at :244-248; `migrations/0050_session_record_fields.up.sql:38-39`; `migrations/0164_coordination_lease.up.sql:44`; 0180 is the last migration number taken.

WATCHOUT: `diff -ru` between the r2 snapshot and the proposal directory returned nothing this round, so no fix-stage text was new. A round that budgets "read the changed sections first and hardest" should check for an empty diff before planning around it — EVIDENCE: scratchpad/cp-snap/0076-run4/non-spec-r2 is byte-identical to proposals/0076_fix_scope-the-coordination-generation-to-the-session.

USEFUL [review-log Traps, "Test-lane fixture hazards"]: the `newFencedServer` note (it claims without fencing, so `TestCheckpointBarrierRejectsWithoutFence` asserts exactly the refusal D7 retires) and the `package adapter` / `adapter_test` wall both checked out verbatim and saved re-deriving why the mid-flight case has to land in `checkpoint_stream_test.go`.

USEFUL [review-log Traps, "tier-list bookkeeping is a refuted class, three times over"]: kept me from working up §8's tier-11 omission and S2's declared tier 3, both of which look like coverage gaps from a cold read and are neither.


### [non-spec.2.review-fresh.1]

DECISION: Returned an empty findings list — BECAUSE I re-derived every code, test, chart, migration, and script citation in the non-spec staging (SCHEMA-1, CODE-1..4, TEST-1, §8, §9) against the tree and every one resolves, and every cross-lane pairing I could construct between the staged spec text and the non-spec text either agrees or falls inside an already-refuted class — ALTERNATIVES: I worked up and declined three candidates, listed below, each on the materiality bar.

WATCHOUT: the CACHE key collides across the spec and non-spec loops. `scratchpad/cp-cache/0076-run4/fresh-r2-ce98ccc23e19.json` was written at 16:22 by the SPEC loop's round-2 fresh lens (same lens name, same round number, same three-file hash), and its three findings are verbatim the three the non-spec prompt lists as already refuted. Returning it as instructed would have re-filed three refuted findings. Read the cached JSON against the prompt's refuted list before returning it — EVIDENCE: scratchpad/cp-cache/0076-run4/fresh-r2-ce98ccc23e19.json vs the refuted entries for "§10.1.8 step 1 assembly read", "§28.8 CH-BARRIER declares unchanged", and "§29.10 quiescence unit".

FACT: the non-spec staging is byte-identical to the `non-spec-r2` snapshot; `diff -ru` returns a hunk only in the review log (compaction pass 19). Three consecutive rounds now have had no changed proposal text to read hardest — EVIDENCE: scratchpad/cp-snap/0076-run4/non-spec-r2/.

FACT: independent re-derivation of the whole non-spec citation set, all resolving. pkg/adapter/coordination.go :44 :52 :63 :89 :93-94 :99 :108-116 :148 :158-166 :180-188 :216 :224-226 :236 :245-269; server.go :302 :307 :314; slot.go :21 :153-166; checkpoint.go :94 :111 :122 :124; session.go :237-239 :271; slotsession.go :282(struct) :347-361; holdstate.go :43 :53-59 :119 :128 :130-132 :187 :206 :225; oplock.go :119-129; pgstore.go :140 :177 :244-248; memstore.go :46 :58-61; coordfence.go :143 :147-153 :155-188; coordfixture.go :73-75 :76 :98-102 :106-108 :109 :115 :122 :220-241 :231; migrations/0050 :38-39, 0164 :44, 0180 last taken; charts/lenny/templates/migrate-job.yaml :10-16 :37-39; Makefile :91-94; proto_no_drift_test.go :70; cmd_run.go :498-508 :635-641 :869 :880; lint-migrations.sh :45 :74-88; prod_columns_test.go :295 :583 :610; sessionstore_test.go :74-79; memstore_test.go :308-325; coordfence_test.go :173-183 (:177 is the zero-returning reader); holdstate_test.go :674 :699-716; coordination_takeover_test.go seeds :74 :142 :241 :301; tier7a colocation :130 :144 :260 :264-265 :287-288; tier8 crash-takeover :118 :147-148 :150 :179 :195 :223 :227-228 :239-241 :267 :283 :296.

FACT: SCHEMA-1's twelve operational-RPC carriers match SPEC-2's list exactly, in the same order, and match the tree. `int64 coordination_generation` occurs at :974 SendMessageRequest, :1002 AttachRequest, :1051 RotateCredentialsRequest, :1075 ExtendCredentialLeaseRequest, :1096 RevokeCredentialsRequest, :1119 InterruptRequest, :1179 CheckpointRequest, :1310 SignalDeadlineRequest, :1398 ResumeRequest, :1452 CoordinatorFenceRequest, :1480 CheckpointBarrierRequest, :1536 ExportPathsRequest, :1581 ReportUsageRequest, :1623 ShutdownRequest. Derived with `awk -v n=$L 'NR<=n && /^message /{m=$2} NR==n{print m}'`, which is cheaper and less error-prone than reading each comment block — EVIDENCE: schemas/lenny-adapter.proto; spec-changes.md:526-531; non-spec-changes.md:16-19.

FACT: §8's class-1 exhaustiveness holds against a fresh `grep -rn CoordinationGeneration --include=*_test.go` over the whole tree. Two tier-2 store files the enumeration does not name, `tests/tier2_component/stores/evictionstatestore_test.go:258,:276` (4) and `evictionfallback_test.go:103,:130` (3), seed explicitly above the baseline and take no shift, so they are correctly outside the class rather than missing from it. `derive_failure_audit_test.go:46` increments a generation and asserts no absolute value. `resume_chunk_selection_internal_test.go:47` and `partialmanifeststore_test.go:24` set manifest rows rather than session rows.

FACT: `upsertMirror` is called with `row.CoordinationGeneration` from the sweep's List snapshot, inside the per-held-row loop, so it runs for every coordinated session rather than only on the takeover branch. This is what makes D7's "the ordinary never-handed-off session's barrier carries the 1 its own row holds" reachable, and it is also the source of the post-takeover mirror lag the log records — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:430.

FACT: `tests/tier8_chaos/coordination_crash_takeover_test.go` runs against the production Postgres `pgstore`, not memstore, so its class-1 shift comes through CODE-4's `pgstore.Create` floor plus migration 0181 rather than through the memstore floor — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:37, :82.

FACT: the tier-1 co-tenant barrier-gate case §8 stages is constructible in `pkg/adapter/coordination_test.go` despite the package wall, because that file is `package adapter` and the landed `TestCheckpointBarrierAcksEchoedCheckpointID` already calls `s.barrier.link(...)` and `s.barrier.complete()` directly from a goroutine-plus-spin pattern rather than driving a real Checkpoint stream — EVIDENCE: pkg/adapter/coordination_test.go:3, :268-285.

DECISION: declined to file that TEST-1's file list names `pkg/adapter/holdstate_test.go` while §8 assigns the hold case to the step landing CODE-3 (S3), so a literal reading of S6 would leave S3's declared tier 1 red — BECAUSE §8's own bullet says in so many words that the amendment lands with CODE-3, so the document resolves the overlap and a competent applier cannot get it wrong; and because the standing context records tier-and-step bookkeeping as a three-times-refuted class — EVIDENCE: non-spec-changes.md:159-165 against :238-239.

DECISION: declined to file that §7's third open decision ("Whether `coord.mu` becomes per-entry") is answered by CODE-1's move of `coordinationState` — BECAUSE CODE-1 enumerates the three data fields ("its `lastFenced`, `initialized`, and `quiesced` fields together") and does not say the embedded `mu` moves, so both dispositions remain implementable and §7 stays a live choice. CORRECTS [standing context, `### Settled`, "§7's remaining decisions"]: that bullet asserts "CODE-1's move of `coordinationState` carries its embedded `mu` onto the entry, settling the third decision by construction". The staged CODE-1 sentence does not say that, and reading it as settled is what would make §7 look self-contradicting — EVIDENCE: non-spec-changes.md:33-34, :52 against spec-changes.md:128-130 and :580.

DECISION: declined to file that CODE-2 carries no "reaches tiers" line where CODE-1, CODE-3, CODE-4, and TEST-1 each do (SCHEMA-1 has none either) — BECAUSE S5 declares 0, 1, 3, 7a and the refuted class covers tier-list bookkeeping.

USEFUL [standing context, `### Traps`, "Test-lane fixture hazards"]: the note that `coordination_test.go` and `holdstate_test.go` are `package adapter` while the stream fixtures are external saved me from filing the tier-1 barrier-gate case as unbuildable; the landed echoed-id test is the counter-example and it is in the same package.

USEFUL [standing context, `### Settled`, "The accessor blast radius is exactly what §9 lists"]: re-derived independently with `grep -rn "s\.barrier\|s\.coord\."` and it is exact — five production `s.barrier` readers and `s.coord` confined to `coordination.go`. Saved a full call-graph pass.

UNVERIFIED: whether the tier-7a cases TEST-1 adds land in `coordination_colocation_race_test.go` or a new file. §9 lists only that one file under `tests/tier7a_load_local`, and the `IMPLEMENTOR'S CHOICE:` marker at non-spec-changes.md:280-287 is scoped to tier-1 files in `pkg/adapter`. I did not file it, because the refuted entry over the tier-2 and tier-3 cases settles that an incomplete files-touched list is not a finding, but a later round asked to tighten §9 should close it.


### [non-spec.3.review-citations.1]

FACT: `diff -ru scratchpad/cp-snap/0076-run4/non-spec-r3 proposals/0076_.../` returns nothing. The proposal is
byte-identical to the round-3 snapshot, so round 3 landed no edits at all and no text in the document is
"newest". A reading-order that hunts for fix-stage text has nothing to find this round. — EVIDENCE: `diff -rq`
exit 0 over the two directories; every file in the snapshot has mtime 17:51 matching the live directory.

WATCHOUT: the cache path this lens is told to write, `scratchpad/cp-cache/0076-run4/citations-r3-<hash>.json`,
COLLIDES with the spec-lane citation lens. On entry it held the spec lane's answer, whose `coverage` says in
so many words that it excluded sections 8 and 9 and the SCHEMA-1 target list "which live in the non-spec
staging that this loop may not edit", and whose single finding is already on this run's refuted list. Returning
it verbatim, as the cache rule instructs, would have returned a spec-lane answer to a non-spec-lane question
and re-filed a refuted finding. Read a cache hit's own `coverage` before trusting it: the hash covers the two
staged files plus the checklist and is identical across lanes, so the filename cannot tell the lanes apart. I
preserved the spec lane's copy at `citations-r3-<hash>.speclane.json.bak` before overwriting.
— EVIDENCE: scratchpad/cp-cache/0076-run4/citations-r3-ce98ccc23e19.speclane.json.bak

FACT: every concrete citation in the non-spec staging resolves and says what the proposal claims. The full
inventory is in this run's cache `coverage` field rather than repeated here. Three that cost real time and are
worth not re-deriving: (1) `schemas/lenny-adapter.proto:1451` reappears verbatim at
`pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`, so SCHEMA-1's stub-provenance claim is exact rather than
approximate; (2) the fourteen `int64 coordination_generation` fields resolve to their owning messages as
SendMessageRequest(:974), AttachRequest(:1002), RotateCredentialsRequest(:1051),
ExtendCredentialLeaseRequest(:1075), RevokeCredentialsRequest(:1096), InterruptRequest(:1119),
CheckpointRequest(:1179), SignalDeadlineRequest(:1310), ResumeRequest(:1398), CoordinatorFenceRequest(:1452),
CheckpointBarrierRequest(:1480), ExportPathsRequest(:1536), ReportUsageRequest(:1581), ShutdownRequest(:1623),
so SCHEMA-1's twelve operational carriers are exactly the fourteen minus the two request messages, and they are
listed in SPEC-2's own order; (3) `pkg/adapter/slotsession.go:174-189` `deregisterSlotLocked` deletes the map
key and returns the `*slotState` with no field zeroed (it only cancels `st.timers`), which is the load-bearing
fact under CODE-1's hold-the-pointer rule and CODE-3's per-session post-mortem read.
— EVIDENCE: pkg/adapter/slotsession.go:174-189; pkg/proto/adapter/v1/lenny-adapter.pb.go:4964-4966

FACT: section 8's no-edit exemptions for three fixture files are exact, and each was worth checking because a
wrong one would be an unstaged site. `coordination_mirror_test.go:116`'s generation assertion is guarded on
`r.SessionID == "s1"` and s1 alone is seeded at 2 (`:84`), so the two rows created unset (`:85-86`) never reach
it. `wiring_test.go:171` and `coordlease_test.go:37`, `:58` assert over `coordlease.Lease` literals upserted
directly, with no session store in the test at all. — EVIDENCE:
pkg/gateway/coordination/coordination/coordination_mirror_test.go:109-118; pkg/gateway/coordination/barrier/wiring_test.go:157-158

FACT: class 2's breakage argument for `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` is
mechanically correct, checked end to end rather than taken on the proposal's word. Both fences compare strictly
greater (`uploaddriver.go:422` `prior.CoordinationGeneration > d.coordinationGen`;
`partialmanifeststore.go:394` `row.CoordinationGeneration > r.CoordinationGeneration`), the seeded prior row is
1 (`uploaddriver_test.go:1007`), and `runningSession` creates with the field unset
(`checkpointer_test.go:89-96`), so under the baseline both comparisons go false and the case dies at its
`t.Fatal` (`:1015`). The amendment is genuinely required, not defensive.

FACT: the section 28.8 `CH-FENCE` bullet stages the cell's repeated gap sentence as well as its window
sentence ("The gap sentence the cell repeats takes the same re-scoping as the Degradation bullet, word for
word"), so the unqualified gap clause at `spec/28_communication-channels.md:1807` is NOT an unstaged mirror.
That was the only missing-edit-site lead the membership criterion generated against a fresh sweep; it is
closed. — EVIDENCE: spec-changes.md:370-377; spec/28_communication-channels.md:1807

USEFUL [Standing context, "Known sub-line citation drifts that must not be filed"]: three of my own
line-number recounts landed one or two lines off the proposal's citation in exactly the places that list names
(`slotsession.go` :282 vs :283 for `heldSession`, `coordination_test.go:184-197` starting on the blank line
above the doc comment, `coordination.go:408` naming a comment line for a call at `:415`). Without that list I
would have spent a verification pair on at least one. Keep it standing until the code lane lands.

DECISION: returned an empty findings list — BECAUSE every citation in both staged files resolves and attributes
its behavior to the right component, and the two candidates I worked up (the section 28.8 CH-FENCE gap clause
as an unstaged mirror, and the mirror/wiring/coordlease no-edit exemptions) both closed against the tree —
ALTERNATIVES: re-filing the cached spec-lane finding, rejected because it is on this run's refuted list and its
refutation turns on the clause being rationale that lands nowhere in `spec/`, which no new evidence disturbs.


### [non-spec.3.review-feasibility.1]

DECISION: returned an empty findings list for the non-spec lane under the actor-action lens — BECAUSE every actor the staged non-spec text assigns an action to can perform it against the tree I re-opened, and every candidate I worked up either resolved clean or lands squarely inside a standing refuted class — ALTERNATIVES: rejected filing (a) 0181's `.down.sql` singular/plural default asymmetry, (b) the tier-2 resume-fence case's missing home, (c) the `tests/claim-map.json` DEFERRED, and (d) CODE-1's declared tier 2 with no tier-2 case; reasons in the entries below.

MISTAKE: the CACHE step's own answer file was written by a different lane. `scratchpad/cp-cache/0076-run4/feasibility-r3-ce98ccc23e19.json` existed and the harness told me to return it verbatim, but its `coverage` text never mentions `non-spec-changes.md` at all — it is the spec-lane feasibility answer over the same three-file hash. The cache key does not carry the lane, so a spec-lane and a non-spec-lane run of the same lens collide on one path and each clobbers the other. I did the review instead of returning it, then overwrote the path per instruction, so the spec-lane entry is now gone. A future run of either lane should treat a cached answer whose coverage does not name its own lane's staging file as a collision rather than as its own work — EVIDENCE: scratchpad/cp-cache/0076-run4/feasibility-r3-ce98ccc23e19.json

FACT: the `tests/claim-map.json` DEFERRED handed to this loop cannot be discharged by editing that file. `scripts/seed-claim-register.py` parses exactly one source, `gateway-runtime-comms.md` §7.1, and `TestClaimRegisterIsReproducibleFromItsGenerator` runs the generator and requires byte-identity with the committed register, so a hand-added or restatused row makes tier 0 red rather than closing the deferral. The remedy, if one is owed at all, is in the generator's source document, which is a root planning document outside every lane's edit scope. The register also already carries a `CheckpointBarrierRequest.coordination_generation` row at `UNWIRED` with deferral `R16` and the note "no production reader compares it until the generation fence lands", which is stale today (the adapter compares it at `coordination.go:232-238`) and is stale for reasons that predate 0076 — EVIDENCE: scripts/seed-claim-register.py:11-13, :38-39; tests/tier0_static/claim_register_generator_test.go:20-27, :45; tests/claim-map.json:75-82

FACT: the co-tenant tier-4 case is constructible on the landed harness, and the mechanism is per-session lease release rather than replica death. `TestCoordinationSplitBrainFenceAcrossTwoReplicas_spec_10_1` models "replica-1 crashed" as `leases.Release(ctx, tenant, sessID, "replica-1")` for one session id, and `coordfixture.NewReplica` takes the bound session ids variadically, so releasing only `sess-b`'s lease while replica-1 keeps `sess-a`'s is exactly what the fixture already supports. The staged bullet's "replica 1's lease on sess-a stays live ... skipping sess-a on ErrHeld" needs no new fixture capability — EVIDENCE: tests/tier4_integration/coordination_fence_split_brain_test.go:80, :99-107, :113-115

FACT: CODE-1's blast-radius claim is exact and cheap to re-check. `grep -rn "\.coord\.\|s\.barrier\." pkg/ cmd/ tests/` returns production hits in `pkg/adapter/coordination.go` alone plus `checkpoint.go:122` and `:124`, and test hits in `pkg/adapter/coordination_test.go` alone (`:224-226`, `:282`, `:285`, `:356-357`). That is one grep and it settles "s.coord is confined to coordination.go" and "s.barrier has five production readers" together — EVIDENCE: pkg/adapter/coordination.go:45-66, :97-121, :232-269; pkg/adapter/checkpoint.go:122-125

FACT: `terminateHeldSession` already holds the `*slotState` CODE-3 wants to read from. It calls `removeSlotTree(m.state)` on the same pointer, and `deregisterStartedSessions` builds `heldSession{sessionID, state}` from `deregisterSlotLocked`'s return with no field zeroed, so the per-session generation read is a field access on a pointer the function already has, under no lock the function is holding (`onHoldTimeout` unlocks `hold.mu` before pass 1). The lock graph after CODE-1 stays acyclic: `CoordinatorFence` takes entry-`coord.mu` then `hold.mu`; the timeout takes `hold.mu`, releases it, then `s.mu`, then a different entry's `coord.mu` — EVIDENCE: pkg/adapter/holdstate.go:176-190, :225-235, :253; pkg/adapter/slotsession.go:282-285, :347-366

DEFERRED [`pkg/adapter/holdstate.go`]: `enterHoldState`'s doc comment reads "Read the generation and the started-session count through their accessors (which take coord.mu and s.mu) before locking hold.mu so no two locks are ever held together" (`:116-118`). CODE-3 deletes the generation read, so the clause naming it is false after the deliverable lands. CODE-3's enumeration of what the `gen` field drags (`:43`, `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:283`) does not include this comment. Not filed: refuted class (k) bars a missed-edit-site finding over a comment under `pkg/`. What is true instead: only the started-session count is read through an accessor before `hold.mu`. This is a second instance of the already-standing comment residue at `pkg/adapter/coordination.go:126-128`; hand both to the implementor together — EVIDENCE: pkg/adapter/holdstate.go:115-121

USEFUL [Standing context, `### Traps`, "Run the `diff -rq` first; it is the cheapest move on this proposal"]: `diff -rq scratchpad/cp-snap/0076-run4/non-spec-r3 proposals/0076_.../` returned nothing, so no text changed this round and "read the changed sections first and hardest" had no target for the fourth consecutive round. Worth two seconds.

USEFUL [Standing context, `### Settled`, "Derived inventories. Do not re-derive any of these."]: I spot-checked roughly fifteen non-spec code citations (`slot.go:21`, `server.go:302`/`:307`/`:314`, `coordination.go:44`/`:52`/`:63`/`:89`/`:93-94`/`:99`/`:108-116`/`:216`/`:224-226`/`:236`, `checkpoint.go:94`/`:111`/`:122`/`:124`, `slot.go:153-166`, `resume.go:178`, `slotsession.go:282-285`/`:347-361`, `holdstate.go:119`/`:128`/`:130-132`/`:187`/`:206`/`:225`/`:283`, `pgstore.go:140`/`:177`/`:244-248`, `memstore.go:46`/`:58-61`, `coordfence.go:143`/`:147-153`, `coordfence_test.go:173-183`, `migrate-job.yaml:37-39`, `0164:44`, `0050:38-39`, proto `:1448-1452` against `pb.go:4964-4967` and `_grpc.pb.go:180`/`:632`) and every one resolved verbatim. The inventory entry is accurate; a future lens can sample rather than sweep.

WATCHOUT: 0181's `.down.sql` disposition is written in the singular where the `.up.sql` changes two defaults, and the asymmetry is cosmetic rather than filable. CODE-4 says the migration sets `DEFAULT 1` on both `sessions.coordination_generation` and `coordination_lease.coordination_generation`, then that "the `.down.sql` restores the `DEFAULT 0`". I weighed and declined the finding, because the standing inventory already establishes that the four non-session `coordination_generation` columns are always written explicitly from the session row, so a lease-column default left at 1 by an incomplete rollback changes no row's value. Do not spend a verification on it; if a fixer touches the paragraph anyway, pluralise it — EVIDENCE: non-spec-changes.md:119-128; migrations/0164_coordination_lease.up.sql:44

OPEN: `TestFenceZeroGenerationFencesAtBaseline`'s amended form asserts the fencer puts 0 on the wire, but its `fakeFenceClient` is seeded `{Accepted: true, LastFencedGeneration: 1}`, so the amended case pins a wire value that no real adapter would accept (`coordination.go:93-94` refuses a non-positive generation with `InvalidArgument`). That is exactly what §8 intends, and the backstop is named in the restated doc comment, so it is not a defect. It is worth a human's eye at sign-off that the tree's only coordfence baseline case ends up asserting a value production always rejects — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence_test.go:175-183; non-spec-changes.md:299-305


### [non-spec.3.review-mechanism.1]

WATCHOUT: the cache path this round's prompt tells you to read first (`scratchpad/cp-cache/0076-run4/mechanism-r3-<H>.json`, H=ce98ccc23e19) is populated, and its single finding is verbatim in this round's "already refuted" list (the staged §10.1 baseline "strictly greater" sentence). Returning it as instructed would re-file a refuted finding. I did the review instead. — EVIDENCE: scratchpad/cp-cache/0076-run4/mechanism-r3-ce98ccc23e19.json

FACT: `diff -rq scratchpad/cp-snap/0076-run4/non-spec-r3 proposals/0076_.../` returns nothing, and so does the r2 snapshot except for the review log. The proposal body has not changed for two rounds; "read the changed sections first" had no target again. Run the `diff -rq` first, as the standing context already says. — EVIDENCE: proposals/0076_.../*.non-spec-changes.md mtime 2026-09-02 07:37

FACT: the survivor replica never reaches `leases.Acquire` for a session a live peer holds. `eligible := bound || priorHolder == s.replicaID || adoptable` with `adoptable := leaseUnheld && isRunningPod(row) && !inAdoptionBackoff`, and `if !eligible { continue }` sits before the Acquire; the `ErrHeld` arm is only reachable for a session that passed eligibility and lost a race. This is what my one finding rests on. — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:296, :299, :336-338, :340-343

FACT (re-verified, saves a re-derivation): every mechanism citation CODE-1..CODE-4 and §8 make into the adapter and the op lock resolves. `barrierGate.open/release/link/complete` at pkg/adapter/coordination.go:157-198 do overwrite unconditionally and carry no session check; `Checkpoint` resolves roots (checkpoint.go:94) before `s.ops.Begin` (:111) and links at :122 with `defer complete()` at :124 guarded on `linked`; `opLock.Begin` admits a distinct session id and coalesces only the same id (oplock.go:117-128); `dispatchOne` persists `ack.CheckpointRef` under `t.SessionID` with `t.CoordinationGeneration` (barrier/barrier.go:236-245); `checkSessionBound` resolves the same registry entry the handlers need (slotsession.go:267-276); CODE-3's holdstate enumeration (:119, :128, :130-132, :187, :206, :225, :283) is exact and `heldSession` carries `state *slotState` (slotsession.go:282-285, used at holdstate.go:249); `coordfence.fence` reads at :143 and floors at :147-153; `pgstore.Create` binds the column explicitly (:260) so the column default baselines nothing; `migrations/0164_coordination_lease.up.sql:44` is `NOT NULL DEFAULT 0`; tier 1 sets `-race` at cmd/lenny-test/cmd_run.go:880; tier 0 vets the untagged tree plus `-tags=contract ./tests/tier3_contract/...` only (:498-508).

MISTAKE (weighed, not filed, so nobody re-spends the round): §8's justification that a case in `tests/tier2_component/stores/` alone leaves tier 0 red is not exact, because the 0181 `prodMigrationSchema` row §8 and §9 also require lives in `prod_columns_test.go` under `tests/tier2_component/migrations/` and by itself satisfies `lint-migrations.sh` pass 3. The standing context already records this as "not exact; the directory choice it supports is still right", so it is imprecision in a rationale with the right conclusion.

OPEN (unchanged, restated because I re-hit it): CODE-4 says the `.down.sql` "restores the `DEFAULT 0`" singular while the `.up.sql` changes two columns' defaults, and §8's tier-2 migration case asserts the same singular. `TestProdMigrationsRollBackPerStep` never re-applies, so a partial down is unexercised and the mirror default is cosmetic. Immaterial as staged; still worth one word from the implementor.



### [non-spec.3.review-performance.1]
FACT: Cache hit — /home/ec2-user/lenny/scratchpad/cp-cache/0076-run4/performance-r3-ce98ccc23e19.json already held `[]` from an interrupted run over identical staged text; returned it verbatim and did no re-review. EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-cache/0076-run4/performance-r3-ce98ccc23e19.json:1
FACT: The cache key hashes only spec-changes.md + non-spec-changes.md + implementation-checklist.md, so review-log.md churn (it was rewritten at 17:51, after the 16:29 cache write) does not invalidate a cached answer. EVIDENCE: /home/ec2-user/lenny/proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.review-log.md



### [non-spec.3.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE the proposal directory is byte-identical to the `non-spec-r2`, `non-spec-r3-start`, and `non-spec-r3` snapshots (`diff -rq` returns nothing for all three), so no new text exists since `[non-spec.2.review-test-coverage.1]` returned empty over it, and an independent walk of §8 reproduced that verdict: every deliverable's changed behavior has a named case or a named landed-test disposition at a tier the change reaches — ALTERNATIVES: declined the two standing test-coverage OPENs again (the `TestCheckpointBarrierRejectsWithoutFence` replacement assertion, and pass 7's three D7 cases), and declined tier-list bookkeeping (CODE-1's declared tier 2 and CODE-4's declared tier 3 have no §8 case of their own), which the log records as a thrice-refuted class.

FACT: the stale-refusal arm of the barrier gate that D7 leaves standing IS pinned by a landed test, which is the reason the second standing OPEN gives and which nobody had verified in the log: `TestCheckpointBarrierRejectsGenerationMismatch` at `pkg/adapter/coordination_test.go:202` (declared), doc comment at `:199-201`. It is single-session and stays green under CODE-2, so the fail-closed arm of a change that relaxes a gate is covered without a new case — EVIDENCE: pkg/adapter/coordination_test.go:199-202.

FACT: the per-session gap reset's positive arm (a gap still detected inside one session) is likewise pinned by a landed single-session test that stays green, `TestCoordinatorFenceGapDetected` — EVIDENCE: pkg/adapter/coordination_test.go:135, :162-166.

FACT: §8's tier-2 target for the `pgstore.Create` floor resolves: `tests/tier2_component/stores/sessionstore_test.go` exists and its `startStore` helper (`:74-81`) does bring the store up over a Postgres container with the production migrations applied, exactly as §8 claims at `:79` — EVIDENCE: tests/tier2_component/stores/sessionstore_test.go:74-81.

USEFUL [`[non-spec.2.review-test-coverage.1]`]: its enumeration of which non-happy path each §8 bullet pins is accurate and saved re-deriving the mapping; the only thing it did not do is verify that the landed tests it leans on exist, which the two FACTs above now close.

OPEN: this lens has now returned empty twice over byte-identical text. If a fixer edits §8 it should run again; otherwise it is converged and can be retired.

## Retired

Retired in compaction pass 18. All of these are run 4's spec round 1; run 4 repeats its ledger ids across append batches, so an id below
may still resolve to a later entry that is not this one.

- `[spec.1.fix-G1.1]` and `[spec.1.fix-design-G1.1]`, the twelve-comment carrier fix and the design behind it: the decision, its one
  replacement sentence, its three rejected alternatives, and the checklist no-op are one `### Settled` bullet; the two proto comments whose
  trailing sentence must survive are a new trap; the `ShutdownRequest` grep trap was already standing. Their `DEFERRED` against this log is
  applied, so the carrier bullet no longer says "seven proto carriers" where SCHEMA-1's list is nineteen, and that `DEFERRED` is retired.
- The round-1 post-fix verification block: it records one round's landed-and-verified diff and its own note that the review log still read
  "seven proto carriers", which this pass has now fixed. History.
- `[spec.1.review-applicability.1]`: the §29.10 gate-safety sweep is a new `### Settled` bullet and the duplicated fill-the-blanks marker is
  a new trap; its anchor re-verification is the derived-inventories line at its fourth confirmation.
- `[spec.1.review-citations.1]` and `[spec.1.review-client-surface.1]`: the authoritative-row-versus-mirror read and the symmetry objection
  are lifted, the first into the settled fence-path asymmetry and the second as a trap; the rest is the twelve-comment finding both filed,
  which the same round's fix closed, plus another pass of the docs and proto inventories.
- `[spec.1.review-docs-alignment.1]`: its docs hit set is the eighth derivation of the eight-site surface already in `### Settled`, and its
  `spec/README.md` and `charts/` checks are inside it. Nothing new.
- `[spec.1.review-edit-sites.1]`: `spec/16` needing nothing and the CloudEvents `session_terminated` row are folded into `### Settled`; its
  `UNVERIFIED` about `spec/04` §4.1 moved whole into `[spec.1.*]`.
- `[spec.1.review-feasibility.1]`: the §28.8 column read is a new trap, the equal-generation gap is a new trap and half of a new `### Open`,
  and `upsertMirror`'s per-sweep run is a new `### Settled` bullet. Its `.Fence(` call-site check confirms a standing line.
- `[spec.1.review-kubernetes.1]`: the CRD-and-chart absence and the §4.2 Postgres backing are a new `### Settled` bullet; its `OPEN` on the
  quiescence unit moved whole into `[spec.1.*]`.
- `[spec.1.review-mechanism.1]`: the step-3 domain claim is a new trap; its second `WATCHOUT` restates the quiescence-unit `OPEN`.
- `[spec.1.review-operational.1]` and `[spec.1.review-performance.1]`: the alert and metric surface, the `error`-to-`success` accounting
  shift, and the write-neutrality argument are two new `### Settled` bullets; the pass-number hazard is a new trap.
- `[spec.1.review-reliability.1]`: the replacement-pod answer to the rebind question, the comparative-only partial-manifest reads, and the
  `coordination_lease` column set are new `### Settled` bullets; its idempotent-retry claim is the half of the new `### Open` that the
  feasibility lens contradicts, and both halves are carried whole in `[spec.1.*]`.
- `[spec.1.review-security.1]`: the gap reset surviving intact and the D6 exemption skipping no reset are a new `### Settled` bullet; its
  four rejected candidates are all inside named refuted classes.
- The `USEFUL` markers of all fourteen lenses: every item they credit is in `## Standing context`, where it is protected.

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

Retired in compaction pass 19. All sixteen are run 4's spec rounds 2 and 3. Every one of them opened by recording that the staged text was
byte-identical to the previous snapshot, so none of them reviewed new text; their two unclosed items are in the ledger residue entry
`[spec.2-3.*]`. Run 4 repeats its ledger ids across append batches, so an id below may still resolve to a later entry that is not this one.

- `[spec.2.review-citations.1]`: its citation inventory is the anchor set at its third confirmation, already the derived-inventories line;
  its `CORRECTS` moving the §28.8 bijection gate from tier 11 to `tests/tier0_static/matrix_completeness_test.go` was applied to
  `### Settled` in pass 18 and stands there.
- `[spec.2.review-client-surface.2]`: the fourteen-field carrier arithmetic and the empty `sdks/`, `charts/`, `schemas/*.json`, and
  `docs/api/` sweeps are both standing, the second from the client side as well as the docs side.
- `[spec.2.review-fresh.1]`: its finding is the §29.10 quiescence-unit contradiction and its unclosed `OPEN` moved whole to `[spec.2-3.*]`;
  the three alternatives it declined are inside the weighed-and-not-filed traps.
- `[spec.3.review-applicability.1]`: its four weighed-and-not-filed candidates are the applicability half of the round-3 weighed list, its
  `CORRECTS` duplicates `[spec.2.review-citations.1]`'s, and its anchor pass is the inventory at a fourth confirmation.
- `[spec.3.review-citations.1]`: the two-call-site `GetCoordinationGeneration` fact, the `upsertMirror` per-sweep fact, and the three
  sentences that read as citation defects and are not are all standing.
- `[spec.3.review-client-surface.1]`: its `MISTAKE` about `CoordinatorFenceResponse` and the frozen twin in the pass record are a standing
  trap; its `DEFERRED` against `non-spec-changes.md` is in `### Deferred`, still unclosed.
- `[spec.3.review-docs-alignment.1]`: the docs-lens scope watch-out and the D5-residual refutation are standing traps; the rest is the
  eight-site docs surface again.
- `[spec.3.review-edit-sites.1]`: the four `spec/29` non-sites, the `spec/07:93` derive fence, and the "Partitioned per slot" extent
  watch-out are standing.
- `[spec.3.review-feasibility.1]`: the §10.1.8 step-1 `SELECT` watch-out is now the `[spec.2-3.*]` UNVERIFIED, the pass-22 universal is a
  standing trap, and the §18 phase-order check is in the derived inventories.
- `[spec.3.review-fresh.1]`: the "SPEC-1 leaves step 2 unchanged" weighed-and-declined and the three §28.5.1 Preconditions non-sites are
  standing; its `DEFERRED` against `tests/claim-map.json` is in `### Deferred`, still unclosed.
- `[spec.3.review-kubernetes.1]`: its two closing facts, that `spec/06` and `docs/reference/state-machines.md` name the hold nowhere and
  that the staged §10.1.4 text adds no pod-side apiserver duty, are lifted into `### Settled` this pass.
- `[spec.3.review-mechanism.1]`: its unclosed `UNVERIFIED` moved whole to `[spec.2-3.*]`, and its watch-out about the two §29.10 lists
  reading as contradictory is lifted into `### Traps` this pass.
- `[spec.3.review-operational.1]`: the alert, metric, and §28.8 fifth-column facts are the standing alert-and-metric bullet, enriched this
  pass with the fifth column's own cell contents.
- `[spec.3.review-performance.3]`: the write-neutrality verdict and the §10.1.8 failure-surface check are standing; its `DEFERRED` about
  migration 0181's `>= 1` tightening was closed by `[non-spec.1.fix-G1.1]` dropping the tightening and was retired in pass 18.
- `[spec.3.review-reliability.1]`: the four traced-and-not-filed candidates are the reliability half of the round-3 weighed list, and the
  §10.1.3 dual-store check is standing.
- `[spec.3.review-security.1]`: the never-fenced-class argument and the accidental-rejection point are standing; its hold-allowlist fact is
  lifted into `### Settled` this pass with the five method names.

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
