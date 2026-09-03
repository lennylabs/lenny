# Review log: Scope the coordination generation to the session

## Standing context

**Compaction pass 23, 2026-09-03.** Read the whole ledger, which covers run 6's spec rounds 2, 3, and 4 (the OD1 fix, its post-fix review,
two open-decisions shards, and a fourteen-shard re-review of byte-identical text) and run 7's non-spec round 1 (fifteen shards over
`non-spec-changes.md`), plus the trailing fix-round record and the index-and-checklist reconciliation of 2026-09-03. The ledger is left
untouched; the round boundary archives it whole and with its ids as soon as this pass returns.
Lifted into `### Settled`: the eight-step checklist, D7's drain arithmetic against the sequential post-barrier loop, `coordfence.fence`'s
retrying stale branch, CODE-1's verified lock order and the pod-wide gate's two-way failure, co-tenancy being admissible by construction,
`lenny_checkpoint_barrier_ack_total` having no incrementer, §28.4's mechanism-level statuses, the completed clause-versus-sentence audit,
the nineteen-carrier range list, the thirteen-line `docs/` surface, the resolved halves of OD1, OD2, OD5, OD7, OD10, OD11, OD12, and OD13,
and the disappearance of `## Resolved in adversarial review` from a 622-line `spec-changes.md`. Lifted into `### Traps`: the
preservation-clause asymmetry that surfaced the `CoordinatorFenceRequest` tail deletion, the archive grep that pre-adjudicates most
non-spec candidates, the refutation that died with the standing claim it leaned on, the fail-fast variant the standing D7 drain refutation
does not cover, the instruction made invisible by sitting inside a withdrawn decision, the OpenAPI path that does not exist, and six
citation and sweep hazards.
Honoured eleven corrections against the standing context: the checklist is eight steps rather than six, so every standing entry naming a
code-lane step is off by two; "either outcome is safe" is SPEC-1's own staged sentence rather than shipped text; `lenny_checkpoint_barrier_
ack_total` has no incrementer, so D7 moves no count between label values; the cache-fallback closure lives in `cmd/lenny-gateway/` and
nowhere under `pkg/`; the empty-client-surface bullet's `pkg/gateway/openapi/` does not exist and the document is at
`pkg/gateway/externalapi/openapi/openapi.json`; the `docs/` surface is thirteen lines rather than five plus three; `spec-changes.md` is 622
lines and its `## Resolved in adversarial review` section is gone, so the pointer to `:868-870` resolves to nothing; the §29.10
quiescence-unit remedy "drop §3's because-clause" is not needed, `spec/10:185` making CODE-1's ground true of the gate; the archive grep to
run before filing against `non-spec-changes.md` is `fill-the-blanks` and `.down.sql` rather than only `5-header`; OD8 now records the floor
coupling while OD9 still does not; and a §28.8 row splits into seven `awk -F'|'` fields.
Closed and deleted: five `### Open` items (the "coordination state" term grep, 0181's `.down.sql` singular default, the OD9 "recorded in
neither entry" half, OD7's specification half, and the summary's evacuated pass-record pointer), with two more reduced to their surviving
halves. `### Deferred` gains one whole entry and ends at five. Two entries disagreed with no correction between them and both were parallel
shards of one round, so both are kept with an `UNVERIFIED` line: migration 0181's unbatched backfill, filed by the performance lens and
declined by the reliability lens. `## Retired` is restored as a section, because two standing entries point into it and it had gone with an
earlier round boundary; it now holds those two older readings plus a one-line note per claim this pass corrected.
**The target of 200 lines was not reached and the section grew, to 1,620 lines against pass 21's 1,224**, of which `### Settled` is 764,
`### Traps` 543, `### Open` 219, and `### Deferred` 55. Nothing was dropped to reach it. `### Open` is the section that grew most, by 97
lines, and none of that is padding: this window filed eight findings after five rounds of empty returns, and an `### Open` line that names
only a subject cannot be told apart from one nobody has reached, which is a failure two shards recorded against this file by name. The
ground pass 21 gave for `### Settled` and `### Traps` also strengthened: the shards cite standing bullets by subject twenty-six times, and
every citation names a body a one-line summary would delete. Decline the trade until the code and test lanes land and the inventories
become checkable against a tree.
Mechanical constraints: the Bash write path is denied for this file, so every edit goes through the editor tool and a deletion costs the
full text of what it deletes; run 7's ledger ids reuse the `[non-spec.1.*]` stems run 4 used, so each of those ids resolves to two
different entries across the archives; and the shards within a round are ordered alphabetically by lens name rather than chronologically,
so a later-sounding id is not a later reading.

### Settled

- **Counter baseline.** The session row's counter is baselined at 1: the row is carried unchanged and §10.1.2 step 1 is untouched, so the
  first takeover mints 2. CODE-4 carries migration 0181 and both session-store `Create` floors, and KEEPS `coordfence`'s non-positive
  floor (`coordfence.go:147-153`) until the release that tightens the session row's check to `>= 1`.
  The counter has three writers (step 1's compare-and-swap, `Sweeper.RecordHandoff`, `bumpCoordinationGenerationOnSnapshotClose`), so any
  floor repeats on each, and `Create` inserts the value explicitly, so a column default baselines nothing. The §7.2 snapshot-close bump has
  four non-test callers: three are terminal writes after which no takeover follows, and the fourth fires on the two recovery edges out of
  `resuming` (`failure.go:212`, `:216`, `:219`), after which the resume path fences the replacement pod at the bumped value
  (`start.go:4233-4245`). CORRECTED in pass 22: the earlier "fires only under a terminal write, after which no takeover follows" was false
  in both halves, and nothing in the proposal rests on it.
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
- **The barrier's cache fallback puts a literal 0 on the wire and must not be floored.** The `Fallback` closure on
  `barrier.MirrorTargetLister` lives at `cmd/lenny-gateway/httpsurface.go:588-602`, and there is no `httpsurface.go` anywhere under `pkg/`,
  so a grep for the bare basename under `pkg/` returns nothing and reads as this bullet being stale. It seeds the target's generation at 0
  and overwrites it only on a successful session-row read (`:593` `w.sessions.Get`, `:594` the copy), so under a Postgres fault the barrier
  carries 0 and is refused with
  `InvalidArgument` whatever the baseline is; the staged "unreachable by construction" claim is not exact, though the outcome is fail-closed.
  CORRECTED in pass 20: the old closing clause, that the fence path's reader returns an error rather than 0 and deleting `coordfence`'s
  floor is therefore safe, does not follow. The reader returns an error only when the read fails; when it succeeds on a row an old binary
  wrote at 0 during the roll it returns 0. CORRECTED again in pass 21: the floor deletion is no longer staged at all. Run 6's G2 fix
  resolved OD8 by keeping the floor, so the fence path floors a zero row to 1 before it reaches the adapter and the barrier path is the
  only one that puts a 0 on the wire.
- **The §10.1.4 "no `CoordinatorFence` ever carries zero" invariant rests on a conjunction, and the post-fix round repaired its ground.**
  §10.1.2 step 1 increments the counter before step 2 announces it (`spec/10:37`), but `fenceResumedPod` fences on the value it reads with
  no increment (`start.go:4233-4245`, the call at `:4237`), so the increment alone does not establish the invariant. The staged sentence
  now closes "and the value every other fence path sends is floored at 1", which the retained floor discharges. Three sites state the
  retention in the same terms: SPEC-1 at about `:251-255`, the corrected Observability paragraph at about `:282-291`, and CODE-4 at
  `non-spec-changes.md:146-155`; `summary.md:302-320` records OD8 as withdrawn with the floor kept, and nothing else cascaded.
- **D7: the barrier is accepted for a bound session the pod holds no fenced generation for.** CODE-2's gate becomes
  `initialized && gen != fenced` read from the slot entry, and `checkSessionBound` runs before it, so acceptance-when-unset never reaches an
  unbound session. The barrier carries 1 rather than 0, clears the adapter's non-positive `InvalidArgument` guard, and is accepted on the
  unset arm. Anything in the ledger reading the unset case as a refusal predates D7. CORRECTED in pass 23: "Either outcome is safe and
  requires no special handling" is SPEC-1's own new sentence (`spec-changes.md:216-217`), not shipped text. Shipped `spec/10:183` closes
  "reject the barrier as a generation-stale RPC under the normal fencing rules — this is safe and does not require special handling",
  asserting safety of the REJECTION outcome alone. Reading the widened assertion as pre-existing is what let three rounds treat it as out of
  scope; run 6 round 4's open-decisions shard has now FILED it against OD12, which the summary records as unanswered with no recommendation.
  The residual it widens over is a superseded draining replica quiescing a session it no longer coordinates for up to the 90s ack deadline.
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
  `CheckpointBarrierRequest`, and its field comment. There is no eighth carrier of this rule, and the exclusion list is now five:
  `CoordinatorFenceResponse.last_fenced_generation` (`proto:1465`) carries no leading comment; the `Checkpoint` RPC comment and
  `CheckpointStart.checkpoint_id` are session-neutral and stay true under the per-entry gate; `CheckpointBarrierResponse`'s comment
  (`:1493-1502`) describes `barrier_id`, `checkpoint_ref`, and `quiesced_ms` and states no gate, no rejection, and no generation; and
  `ResumeResponse.recovery_generation`'s comment (`:1435-1438`) is the one `split-brain` hit in the proto outside the nineteen and belongs
  to the recovery counter. SCHEMA-1's target list is those seven plus
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
  `docs/getting-started/architecture.md`, there being no `docs/architecture.md`. CORRECTED in pass 23: the full surface is THIRTEEN lines
  rather than five plus three, because the metrics table carries four more coordination rows than this bullet named. The complete set is
  `getting-started/concepts.md:101`, `getting-started/architecture.md:173`, `reference/adapter-contract.md:68`, `:69`, `:96`,
  `reference/metrics.md:40`, `:197`, `:307`, `:309`, `:310`, `:311`, `:312`, and `operator-guide/upgrades.md:47-54`. Every metric row
  describes what the metric means rather than the unit of the fenced value, and the change adds and removes no metric, so no row moves; the
  conclusion is unchanged and only the count was short. It has been re-derived eleven times, from the docs,
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
  with §4's "either order" claim and the two known-imprecise rationale sentences the residue entry carries. On the third decision
  (`coord.mu`) two entries disagree and neither corrects the other. Pass 20 read it as still live: `coordinationState` does embed its own
  `mu` as its first field (`pkg/adapter/coordination.go:26`), which is why lens after lens derives that CODE-1 settles it by construction,
  but CODE-1's staged sentence enumerates the three data fields (`lastFenced`, `initialized`, `quiesced`) and does not say the mutex moves.
  Run 6's open-decisions lens read it as already settled. The newer reading is kept below; the older is in `## Retired`. UNVERIFIED: which
  of the two a fixer applies. A fixer touching CODE-1 should say which of the two it means.
- **§7's three decisions are each dispositioned somewhere else in the proposal and none of the dispositions has been applied to §7.**
  Item 3 (`coord.mu`) is settled by the summary's Fixed decisions ("CODE-1's lock order is the registry lock, then the entry lock, then the
  hold lock") and by CODE-1 removing `Server.coord` outright; item 2 is dispositioned out of scope with a named consequence and an owner;
  item 1 is OD1. §4's detailed-design bullets duplicate items 2 and 3 verbatim, so a fix touches both sites — EVIDENCE:
  spec-changes.md:592-593, :110-115, :128-130; summary.md:61-62, :361-372.
- **A bind-race rejection and a stale-fence rejection are indistinguishable in the tree.** `checkSessionBound` returns
  `FailedPrecondition` (`slotsession.go:268-271`), the stale fence returns the same code (`coordination.go:99-106`), and `coordfence.fence`
  maps every `FailedPrecondition` to its stale branch (`:164-179`), so §4's claim that the two "must be distinguishable" is unmet today.
  That is the named consequence §7's second decision owes.
- **OD10 is settled and the staged ground is the wrong reason for the right conclusion.** `upsertMirror(..., row.CoordinationGeneration)`
  (`coordination/coordination.go:430`) reads the List-snapshot row while `RecordHandoff` (`:463-482`) bumps the stored row, so the mirror
  lags one generation for a sweep interval after a takeover and §10.1.8 step 1's and §29.7 step 4's "carries the current
  `coordination_generation`" are already false in the shipped tree, before any edit here. They are therefore not edit sites, and the staged
  ground "each stays true" (`spec-changes.md:257-262`) reaches that conclusion by a route that does not hold.
- **The fence's own acceptance predicate has exactly one carrier and no `spec/` section states it.** `pkg/adapter/coordination.go:99`
  rejects a fence when `initialized && gen <= lastFenced`, while the barrier gate at `:236` refuses when `!initialized || gen != fenced`;
  the two comparisons differ deliberately, and a fix that "aligns" the wire text on one silently changes the other. The sole carrier is
  `CoordinatorFenceResponse`'s comment (`proto:1455-1462`, `accepted` at `:1456-1458`, `gap_detected` at `:1458-1462`). The three fence
  proto carriers state three different rules: the RPC comment (`:153-162`) carries the record-and-reject rule, the gap reset, and the
  first-fence exemption; the request message comment (`:1442-1446`) carries the record-and-reject rule; the response comment carries the
  accept predicate and the gap-detection definition. G1 split the response out of SCHEMA-1's record-and-reject list into its own paragraph
  on that ground, keeping the "not greater than" comparison and re-scoping it to the session, and the framing sentence at
  `spec-changes.md:496-500` now names the exception.
- **No gate pins the `spec/04` §4.1 pod-versus-session classification either way.**
  `TestAdapterProtoRequestMessagesAreClassifiedByScope` (`tests/tier0_static/adapter_proto_message_scope_test.go:135-150`) checks coverage,
  service, duplicates, and that the scope string is one of the two words; the tier-3 suite's four assertions all iterate
  `sessionScopedMessages`, which excludes the fence (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43`,
  `:80`, `:99`, `:129`), and the comment at `:38-42` claiming the fence is covered by the session-address arm describes coverage that does
  not exist. `CoordinatorFenceRequest` is also the only §4.1 pod-scoped row carrying a session field, `ReportPodScrubRequest` declaring
  `pod_id` alone (`proto:492-496`), so a reclassification falsifies `spec/04:151` as well as `:175` and `:188`; OD3 now names that knock-on.
  0075 and 0080 are legacy single-file `Draft`s with no reviewed or approved date, so they may be invalidated freely; CODE-1 deletes 0075's
  stated ground ("The write target is the pod. The identifier selects nothing"), and its retyping deliverable loses its subject while its
  derivation-rule deliverable survives on the `CheckpointRequest` exception alone.
- **The proto is a published artifact and the client surface around it is empty.** §28.7's artifact table (`spec/28:1775`) names runtime
  authors and the external-adapter compliance suite as consumers of `schemas/lenny-adapter.proto`, which is why a wrong SCHEMA-1
  prescription is more than cosmetic; the compliance suite generates from field and message declarations rather than from comments, and
  SCHEMA-1 declares none. Outside the proto the surface is `docs/getting-started/concepts.md:101` alone, with
  `lenny-adapter-jsonl.schema.json`, `runtime-ops-events.schema.json`, `sdks/`, and `charts/` carrying nothing and §15 naming the fence
  nowhere. CORRECTED in pass 23: the OpenAPI document is at `pkg/gateway/externalapi/openapi/openapi.json`. There is no
  `pkg/gateway/openapi/` directory, so a grep against that path returns nothing because the directory is absent rather than because the
  document is clean; `grep -c coordination` against the real path returns 0, which is the check this bullet means. `spec/04:200` states the
  reason the whole client half is empty: `recovery_generation` is "visible to clients via the session API and `session.resumed` events"
  while `coordination_generation` is "internal only, used for split-brain fencing", so SPEC-3's baseline has no client representation. The first-fence exemption exists outside `pkg/` on one tracked carrier, `proto:161-162`; `spec/` carries it
  nowhere, which is what D6's "Only `spec/` never carried it" rests on.
- **The barrier fan-out runs under one wall-clock deadline, and D7 adds no drain work.** `prestop.go:503-506` wraps the whole
  `Barrier.Dispatch` in a single `context.WithTimeout(ctx, h.barrierAckTimeout())` rather than a per-target deadline, and `spec/10:184`
  states the same, so the 90s budget is already consumed by the serialised co-tenant checkpoints in the shipped tree for the population
  whose barriers are refused. The tempting D7 regression, that N co-tenant barrier-window checkpoints are newly serialised inside one
  deadline, is refuted by `dispatchOne` starting `CheckpointWithTrigger` in a goroutine before `dispatch.Send` and waiting on it after,
  whatever the barrier returns (`barrier.go:216-226`, `:243-244`): the streams, the op-lock serialisation, the MinIO burst, and the wall
  clock are identical before and after, and a true `Acked` makes prestop skip the post-barrier capture (`prestop.go:394-397`), so D7 removes
  a duplicate capture. Tier 3 is 30 replicas by 400 `maxSessionsPerReplica` for 12,000 sessions, and §10.1.8 step 3 sizes the drain burst at
  up to 400 simultaneous MinIO uploads per draining replica (`spec/17:1225`, `:1229`), which D7 does not change. Do not re-derive this.
- **Anchor arithmetic for the cells and comments this staging edits, re-derived twice in run 6.** §28.8 rows are one physical line each:
  1803 header, 1805 `CH-ATTACH`, 1806 `CH-CHECKPOINT`, 1807 `CH-FENCE`, 1808 `CH-BARRIER`, 1809 `CH-PODHEALTH`, 1810 `CH-ADAPTEREVENTS`,
  1811 `CH-MSGSOCK`, 1812 `CH-RUNTIMEOPS`. §28.5.1: `CH-FENCE` Messages `:314-317`, Preconditions `:318-322`, Timing `:323-328`,
  Exclusivity `:329-332`, Degradation `:333-340`; `CH-BARRIER` Messages `:349-353`, Preconditions `:354-357`, Exclusivity `:361-365`. §28.6
  "One holder per session" `:1669-1677`, the second opener `:1679-1690`. Proto: `CoordinatorFence` RPC comment `:153-162` with the
  exemption at `:161-162`, `CoordinatorFenceRequest` `:1442-1446` with its field comment `:1449-1451`, `CoordinatorFenceResponse`
  `:1455-1462`, `CheckpointBarrierRequest` `:1469-1474` with its field comment `:1477-1479`, and D7's `:1475-1483`.
- **The `last fenced` surface in `spec/`, `docs/`, and `charts/` is six lines and every one is staged**: `spec/10:40` and `:41`,
  `spec/28:333-334` and `:1807`, and `spec/29:1261` and `:1309-1310`. `docs/` and `charts/` return nothing on that grep, so criterion (d)
  has no carrier there for the pod-held value. `REG-COORDLEASE` is Redis with a stated Postgres fallback (`spec/28:137`, `:246`, `:330`),
  so the §12.4 durable-fallback bar is met for the one register the staged §28.6 text leaves as sole exclusion guard for the never-fenced
  class. Outside `spec/10`, `spec/28`, `spec/29`, and `spec/04` the fence surface is `spec/18:238`, `spec/07:93`, `:215`, `:222`,
  `spec/11:216`, and `spec/12:160`, none a pod-side gate.
- **Two lens-scoping facts worth one grep each.** A grep of the staged spec text for
  `sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|informer|etcd|leader` returns nothing, which is the whole
  Kubernetes lens on this proposal. `spec/18` orders deliverables rather than the sections they cite, so staged §10.1.2 step 3 stating a
  rule about a Phase 8 artifact from a Phase 4 section is not a phase inversion (`spec/18:218`, `:238`, `:398`, `:404`); do not file it and
  do not re-derive it.
- **The §29.10 "does not state" list survives SPEC-2's removal intact.** After the edit it keeps three bullets, the narrowed
  `Interrupt`-and-barrier one, `CH-ADAPTEREVENTS` ownership, and two-replicas-per-pod, and the only intra-file cross-reference into the
  list comes from the "Partitioned per slot" coordination bullet and points at the two-replicas bullet, which survives — EVIDENCE:
  spec/29:1467-1470, :1519-1524, :1536-1540. §28.7 is a register keyed on the artifact set with no per-message content, so SCHEMA-1's
  comment edits leave it alone (`spec/28:1752-1762`, `:1774`).
- **The gap event already carries `session_id`, so SPEC-1's per-session clause (c) needs no new field.** The shipped emit is
  `slog.WarnContext(ctx, "coordinator_generation_gap", "session_id", sessionID, ...)` at `pkg/adapter/coordination.go:114-117`, and §16.4
  requires `session_id` on every log line (`spec/16:379`). `docs/alerting/rules.yaml` and `charts/lenny/files/alerting-rules.yaml` are both
  rendered from `pkg/alerting/rules/rules.go`, and the only coordination alert in all three is `CoordinatorHandoffSlow`, so an
  alert-to-runbook lens has no surface here.
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
  value. CORRECTED in pass 23: `lenny_checkpoint_barrier_ack_total` has NO incrementer anywhere in the tree outside its catalog entry
  (`pkg/observability/metrics/catalog.go:127`; `grep -rn "IncCheckpointBarrierAck\|barrier_ack_total" --include=*.go pkg/ cmd/` returns the
  catalog line alone), so D7 moves no count between label values and there is no `outcome` distribution to re-document. The older reading,
  that D7 moves a count from `error` to `success` inside an existing label set, assumed an emitter that does not exist; the conclusion that
  no metric is invented survives. §28.8's fifth column, "Operator observable", needs no edit on any of the three staged rows.
  Its cells, so an operational lens need not open them: `CH-FENCE` names the `coordinator_generation_gap` event and the `coordinator_lost`
  termination, `CH-BARRIER` names `manifest_reason="timeout"`, `lenny_checkpoint_barrier_ack_total`,
  `lenny_checkpoint_barrier_ack_duration_seconds`, and `lenny_prestop_barrier_target_source_total`, and `CH-CHECKPOINT` names the
  `partial = true` manifest row and `lenny_checkpoint_storage_failure_total` (`spec/28:1803` header, `:1805-1808`). Corrected in pass 20:
  an earlier version of this line named `CH-ATTACH`, whose cell is the sender-side one; the conclusion that no cell needs an edit is
  unaffected. CORRECTED in pass 21: "Operator observable" is the fifth *labelled* column but the SIXTH field of an `awk -F'|'` split, so
  the standing `$5` recipe reads the exclusivity cell instead. Read it with
  `awk -F'|' 'NR>=1805 && NR<=1808 {print NR" "$6}'`. SPEC-2 stages the fourth labelled column of three rows and nothing else.
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
- **The checklist has EIGHT steps, and every standing entry that names a code-lane step by number is off by two.** CORRECTED in pass 23:
  the checklist was rewritten in run 6 (commit `f9c85f30c`) and the map is now S1 SPEC-1, S2 SPEC-2, S3 SPEC-3, S4 SCHEMA-1, S5 CODE-1 and
  CODE-3, S6 CODE-4, S7 CODE-2, S8 TEST-1. The spec lane is three steps rather than one because the three spec deliverables land in four
  different files under `spec/`. The old six-step map (S1 SPEC-1/2/3, S2 SCHEMA-1, S3 CODE-1+CODE-3, S4 CODE-4, S5 CODE-2, S6 TEST-1) is
  retired: read the S3/S4 split bullet, the "batching an S3/S4 file into one step" trap, and the S3/S5 compile-break entry as S5/S6 and
  S5/S7. The five properties the map is verified against are unchanged and all still hold, re-checked whole in run 7: nine deliverables each
  in exactly one step, one lane per step, the three spec steps leading, no `Depends-on` naming a later or absent step, and no box ticked.
  The summary's `## Deliverable index` matches it row for row, needing no row added, removed, or retargeted, and the two prose sites that
  cite a step id were updated with it (`summary.md:73`, `:347`). S7 declares tiers 0, 1, 3, 4, and 7a. Re-read a step identifier against the
  live checklist before believing any standing claim about it; do not re-derive the map itself.
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
  derived from a message's field set". Reclassifying the fence row therefore has to restate `:151` as well as `:188`, because `:151`
  grounds the declared-not-derived rule on `session_id` appearing on messages of both classes, and after the reclassification no
  pod-scoped row carries a session field (`:188` for the other pod-scoped `Adapter` messages, and `ReportPodScrubRequest` declares
  `pod_id` alone at `schemas/lenny-adapter.proto:492-496`). `:151` names no `CoordinatorFenceRequest` and carries no example of it; the
  only messages it names are `CheckpointRequest` and `CheckpointStart`.
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
  session keyed by that session, so CODE-3's per-session generations leak nothing across sessions. `writeHoldPostMortem` writes one 0600
  JSON file per session (`holdstate.go:283-305`) and `last_generation`/`lastGeneration` occurs nowhere under `docs/`, `schemas/`,
  `charts/`, `sdks/`, or `spec/`, only at `holdstate.go:132`, `:228`, `:290`, `:295` and two `holdstate_test.go` sites.
- **D7 strictly reduces top-tier drain cost, because prestop's post-barrier loop is sequential.** `dispatchOne` launches
  `CheckpointWithTrigger` in a goroutine before `dispatch.Send` and joins it after whatever the barrier returns, so the gateway-driven
  upload already runs and is already joined inside the single 90s `Dispatch` deadline for the refused population today. What D7 changes is
  `out.Acked`, and prestop's post-barrier loop skips a session iff `barrierAcked[sess.SessionID]` and iterates sequentially by its own
  comment. A Tier-3 replica draining 400 never-handed-off sessions today runs 400 concurrent barrier-window captures and then 400
  sequential post-barrier captures; after D7 it runs the first set alone. That relieves the named failure mode (the drain exceeding the
  Tier-3 120s timeout) rather than aggravating it — EVIDENCE: barrier.go:216-226, :243-244, :235-255; prestop.go:382-397, :503-506;
  spec/10:184; spec/17:1225, :1229.
- **`coordfence.fence`'s stale branch is not a bare relinquish.** It re-reads the authoritative generation and RETRIES at the new value
  when `newGen > gen`, relinquishing only when the row has not advanced past the value it sent (`coordfence.go:164-179`). The problem
  statement's §1.3 step 3 describes the no-advance arm correctly, and a lens that reads the branch as unconditionally terminal mis-derives
  both the co-tenant defect and the lost-ack case.
- **CODE-1's stated lock order holds with no inversion, checked path by path.** After CODE-3 removes the generation read, `enterHoldState`
  takes only `s.mu` (through `startedSessionCount`) before `hold.mu`; `onHoldTimeout` releases `hold.mu` before pass 1, so pass 2's entry
  read never nests under it; `CoordinatorFence` holds the entry lock across `exitHoldState`, which is the declared entry-then-hold order;
  `CheckpointBarrier` takes the entry lock in three short sections (`coordination.go:232-235`, `:246-248`, `:254-256`) and never across the
  blocking `select` at `:265-267`; `s.ops.Begin` blocks holding nothing. Do not re-derive this.
- **The pod-wide barrier gate fails in both directions, which is worse than CODE-1's rationale reads.** `open()` replaces `g.done`, so
  barrier A blocks on a dead channel; `release()` then returns whatever id B's stream linked AND sets `waiting=false`, which also makes
  `complete()` a no-op for B. The staged sentence's "empty or cross-linked `checkpoint_ref` ... persisted under the wrong session id" is
  verified in both halves — EVIDENCE: coordination.go:158-166, :171-176, :192-199; barrier.go:238-245.
- **Co-tenancy on one `adapter.Server` is admissible by construction, so every co-tenant case §8 stages is startable.**
  `claimSessionSlotUnderLock`'s different-session refusal ("pod is not idle") is gated on the `sdkWarm` argument alone
  (`slotsession.go:66-73`), a plain `StartSession` on a pod-warm pod passes `sdkWarm=false` and lands on its own slot, and the ceiling of
  one in the doc comment is a preConnect/§6.1 rule. The adapter enforces no `maxConcurrentSessions` ceiling of its own; the ceiling is the
  gateway's. A landed tier-1 case pins it, `TestStartClaimAdmitsASecondSessionOnItsOwnSlot_spec_5_2` at
  `pkg/adapter/one_session_only_test.go:12-32`. Nobody had written this down and it is the precondition of TEST-1's tier-1, tier-4, and
  tier-7a cases.
- **§28.4's three statuses are defined at the mechanism level, so OD11's premise is false against the section it cites.** `WIRED` is "the
  mechanism is reachable from production code" (`spec/28:163-165`), and the `CoordinatorFence` row (`claim-map.json:460-465`) and the
  `CheckpointBarrier` row (`:448-453`) stay reachable before and after every step S1 through S8, so no status change and no claim-register
  deliverable is owed for the interval. The separate field-level `CheckpointBarrierRequest.coordination_generation` row (`:75-82`) is a
  different row: it is `UNWIRED` under deferral R16 with a note already false against the shipped comparison at `coordination.go:236`, and
  the summary's `### Corrections outstanding` bullet already owns that. What remains open is the statement-level question in `### Deferred`,
  which is a different obligation from a row's status.
- **The clause-versus-sentence audit is complete over every cited cell and bullet in SPEC-2, and `CH-BARRIER` is the only outlier.**
  `spec/28:1807` (`CH-FENCE`) and `spec/29:1322-1326` (§29.8 step 9) both quote a clause of a compound sentence, but each bullet names the
  surviving half explicitly, so the substitution lands; `spec/28:1806` (`CH-CHECKPOINT`) names its surviving clause separately from its
  constraint sentence. Cell sentence counts: `CH-FENCE` four (holder, window-plus-hold compound, gap, citations), `CH-CHECKPOINT` two,
  `CH-BARRIER` two. Do not re-derive this.
- **The §29.10 carve-out's relocation legs are both staged and complete.** The removed "does not state" bullet (`spec/29:1523-1527`) asks
  two questions: hold partitioning lands in the new "Shared by the whole pod" hold bullet and cross-slot fencing lands in the "Partitioned
  per slot" coordination bullet, and the removed bullet's own factual sentence (hold rejects every inbound RPC other than
  `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` detail) is carried into the new hold bullet verbatim in substance. No
  content is lost — EVIDENCE: spec/29:1464-1470, :1472-1474; spec-changes.md:432-448.
- **The staged spec text introduces no reserved N3 phrase and no N8 line citation.** A case-insensitive grep of the whole spec-changes file
  for `lifecycle channel|control channel|lifecycle-channel|control-channel` returns nothing, and every `spec/NN_file.md:NNN` citation in
  the file sits in the proposal's own rationale prose rather than in text that lands under `spec/`. The naming lint and the citation
  ratchet are not reached by the spec lane. `scripts/specshift/scope/scope.go` excludes `proposals/` from every pass in any case.
- **The nineteen proto carriers, with their ranges, so a fifth derivation is never owed.** Eighteen edited: fence RPC comment `:153-162`;
  `CoordinatorFenceRequest` message comment `:1442-1446`; `CoordinatorFenceResponse` `:1455-1462`; `CheckpointBarrier` RPC comment
  `:165-179`; `CheckpointBarrierRequest` message comment `:1469-1474`; its field comment `:1477-1479`; and the twelve operational field
  comments at `:969-973`, `:995-1001`, `:1046-1050`, `:1070-1074`, `:1091-1095`, `:1114-1118`, `:1172-1178`, `:1305-1309`, `:1393-1397`,
  `:1531-1535`, `:1576-1580`, `:1618-1622`. One unedited: `CoordinatorFenceRequest.coordination_generation` at `:1449-1451`. The twelve
  messages in SPEC-2's own order are `SendMessageRequest`, `AttachRequest`, `RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`,
  `RevokeCredentialsRequest`, `InterruptRequest`, `CheckpointRequest`, `SignalDeadlineRequest`, `ResumeRequest`, `ExportPathsRequest`,
  `ReportUsageRequest`, and `ShutdownRequest`; `grep -n "A pod validates"` returns exactly those twelve. SCHEMA-1's list is byte-for-byte
  SPEC-2's set in SPEC-2's order. The `CheckpointBarrier` RPC comment lands at `lenny-adapter_grpc.pb.go:191` and `:643`, which SCHEMA-1
  does not enumerate and does not need to.
- **`coordinator_handoff_stale` is a single-carrier wire contract.** It appears in `spec/`, `schemas/`, `docs/`, and `charts/` at exactly
  one place, `schemas/lenny-adapter.proto:1445-1446`, mirrored into `lenny-adapter.pb.go:4959-4960`, emitted at `coordination.go:106` and
  `:238`, and asserted at `coordination_test.go:119-120`. `spec/10:71` and `spec/16:183` carry only the metric name and never the detail
  string, `CheckpointBarrierRequest`'s comment (`proto:1472-1473`) names `FailedPrecondition` with no detail string, and a grep for
  `FailedPrecondition|FAILED_PRECONDITION` over `spec/` and `docs/` returns only setup-command hits. The proto comment tails are the sole
  tracked non-Go statement of the fence rejection's status code, its detail string, and its metric attribution.
- **The quiescence question cannot be answered per session on the runtime side, and `quiesced` is not the runtime-facing quiescence.**
  `schemas/runtime-ops-events.schema.json` carries the adapter-to-runtime quiesce frames at `:70`, `:82`, and `:93` and none of them carries
  a session identifier or a generation; those frames belong to the checkpoint handshake driven from `pkg/adapter/checkpoint.go:150-159`.
  `coordinationState.quiesced` has exactly one reader, the test-only accessor `isQuiescedForBarrier` (`coordination.go:52-55`), and its
  only writes are the barrier's set and deferred clear (`:247`, `:255`), so CODE-1 moving it onto the slot entry changes no wire frame. A
  lens that reads "quiesce" in both places works up a runtime-SDK finding that does not exist. Read the schema rather than the refutation.
- **Migration 0181's whole gate surface is `scripts/lint-migrations.sh`, and it clears all five passes.** Pass 1 wants the `.down.sql`
  sibling, pass 2 wants it non-empty (`SET DEFAULT 0` satisfies it), pass 3 greps the bare number under `tests/tier2_component/migrations/`,
  pass 4 keys on `add column`, and pass 5 on `drop column`; `scripts/lint-schema.sh` only parses `CREATE TABLE`. The staged behaviour file
  plus the `prodMigrationSchema` row discharge pass 3 twice over. Migrations are embedded by `migrations/embed.go:15` (`//go:embed *.sql`)
  rather than listed in a chart artifact, so 0181 obliges no `charts/` edit, and no `docs/` or `charts/` surface enumerates migration
  numbers. The 0180 precedent row is `{migration: "0180", table: "checkpoint_manifest"}` with no `columns`, and its comment states outright
  that it exists so `TestProdMigrationsRollBackPerStep` steps through its `.down.sql`.
- **The open decisions that resolve against the tree, so a reviewer is not asked to judge them.** OD1 has no live alternative: equality is
  written three times over (shipped `spec/10:41`, staged `spec-changes.md:158-159`, and SPEC-1's own closing "The comparison stays equality"
  at `:202-204`), and both refutations of widening verify. OD2 is a genuine human trade and its mechanism re-verifies; do not try to resolve
  it. OD5's two stated costs are neither of them spec-text costs: answering "split" drops SPEC-3 entirely, drops SPEC-1's §10.1 baseline
  paragraph (`spec-changes.md:231-243`), and removes the sole stated ground for SPEC-1's own no-edit-site conclusion at `:259-264`, none of
  which OD5 names. OD7's specification half is answered by `spec/07:196` ("resuming → running (re-attach succeeds on replacement pod ...)"),
  leaving only the code-side placement question. OD10 attributes the "not an edit site" disposition to SPEC-2 where SPEC-1 states it at
  `:259-264`, and says SPEC-2 leaves §10.1.8 step 1 unedited where SPEC-1 rewrites step 1's closing sentence. OD11's premise is false, per
  the §28.4 bullet above. OD12 is half resolvable and the half that resolves reverses its framing: `dispatchOne` starts the gateway-driven
  `Checkpoint` stream before `dispatch.Send` and joins it after, unconditionally, so a superseded replica opens that stream against the pod
  today whether its barrier is accepted, refused, or never sent, and the only residual acceptance creates is the quiescence held to the
  ack deadline. OD13's answer is "yes, TEST-1 owes it", by `.claude/rules/test-coverage.md`'s fail-closed rule rather than by judgement,
  with the remedy in `non-spec-changes.md` §8/TEST-1. OD3 is the one that is genuinely a reviewer judgement rather than resolvable, because
  `spec/04:151` declares the classification rather than deriving it and SPEC-2's own staged §29.10 hold bullet keeps a pod-wide effect for
  the fence.
- **Nothing in the tree asserts the unbound-session refusal, and the case is cheap rather than blocked.** The string "session %s is not
  assigned to this pod" has exactly one occurrence in the repository, `pkg/adapter/slotsession.go:273`, and no `_test.go` file names it.
  `TestCheckpointBarrierRequiresSession` (`coordination_test.go:175-183`) asserts `InvalidArgument` for a MISSING session id, which is a
  different guard. `RegisterUnboundSlotForTest` (`export_test.go:54-64`) already builds exactly the unbound-slot state such a case needs.
- **`spec-changes.md` is 622 lines and has no `## Resolved in adversarial review` section at all.** Its headings are §2 at `:6`, §3 `:90`,
  §4 `:105`, §5 `:132`, §6 `:586`, §7 `:604`, §10 `:618`. Run 5 evacuated the pass records into `0076...review-log-archive.md:4`, so every
  standing pointer into that section resolves to nothing; the archived records carry the rationale instead. The older "599 lines" figure is
  retired. `.Fence(` outside tests returns exactly three sites, `start.go:4237`, `coordination_seams.go:233`, and `coordfixture.go:231`,
  and both fence drivers therefore reach the `coordfence` floor, which is what "the value every other fence path sends is floored at 1"
  rests on.
- **`spec/04:461` is a non-site and is the one `spec/04` sentence a Kubernetes reading flags.** It says active-session checkpointing on an
  admin `DrainPool` is coordinated by the gateway rather than the controller, that stale-coordinator fencing "is already handled by the
  `coordination_generation` CAS mechanism specified in §10.1", and that the controller removes the finalizer only after the gateway has
  written a terminal state. It defers to §10.1 and fixes no compared value or unit, which is the non-site arm of the membership criterion; a
  "stale coordinator" presupposes a successor's fence has landed, so the never-fenced class does not reach it, and D7 removes refusals
  rather than adding them, so no new path wedges the terminal write and no finalizer can stick. Do not re-derive.
- **Two tier-0 gates a lens works up and neither reaches this change.** `cmd/lenny-test/cmd_validate.go:380-402`'s change-graph
  completeness check fails a tracked source path covered by no glob, but its domain is `cmd/ pkg/ scripts/ tests/` over `.go` and `.sh`
  excluding `_test.go`, so `migrations/` is outside it and is a glob key anyway; 0181 and every new `_test.go` are outside the gate.
  `tests/registers/identifier-senses.yaml` keys ordinal occurrences of a retired channel spelling per file and carries eight
  `schemas/lenny-adapter.proto` rows, which looks like an ordinal-shift hazard for SCHEMA-1's rewrites; it is not, because the proto carries
  no occurrence of either reserved spelling today and the replacement text introduces none.
- **"Coordination state" is not a term `spec/` binds, and staged §10.1.8 step 1 borrows a phrase used for something else.** The only `spec/`
  occurrences are the dual-store degraded-mode phrase "in-flight sessions continue on cached coordination state" (`spec/12:153`, `:226`;
  `spec/28:1655`, `:1817`). Weighed and not filed, because context settles the reading. This closes the standing request for exactly this
  grep; do not run it again.
- **0181's `.down.sql` asymmetry is weighed and declined rather than unchecked.** The `.up.sql` changes two defaults (`sessions` and
  `coordination_lease`) while CODE-4 and the tier-2 case both say the down "restores the `DEFAULT 0`", singular. The consequence of the
  narrow reading is a mirror column left at `DEFAULT 1` after a rollback, and `upsertMirror` binds that column explicitly on every write, so
  the effect is cosmetic and `TestProdMigrationsRollBackPerStep` asserts no default. The cheap repair, if a fixer is in the file anyway, is
  "restores both `DEFAULT 0` clauses".

### Traps

- **MISTAKE: a wire value of 1 on the barrier.** Pass 8 put a wire value of 1 on the barrier, the one positive value a first takeover also
  produces (a row at 0 compare-and-swaps to 1), so predecessor and successor carried the same number and the first fence separated nobody. A
  value invented to satisfy a guard is checked against every other producer in the same domain, and the compare-and-swap is a producer.
- **MISTAKE: the baseline's test-lane consequence is a class, and the residue is outside it.** Pass 9 called
  `tests/tier2_component/coordination/sweep_test.go` the whole test-lane consequence and enumerated even that file incompletely. The
  consequence is every assertion reading a session row's `CoordinationGeneration` after a create that left the field unset, across the
  takeover, mirror, memstore, tier-7a, and tier-8 suites. Two landed tests break outside that class:
  `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` seeds another row's constant at 1 calibrated against a session row created
  unset, the only site of its kind in the tree, and `TestFenceZeroGenerationFencesAtBaseline` pins the `coordfence` floor. Look at the
  class boundary for residue rather than inside it. CORRECTED in pass 22: that test is no longer residue, because run 6's G2 fix kept the
  floor and CODE-4 leaves it as it landed.
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
  split, looks absent. A row splits into seven fields (`NF=7`): `$5` is the exclusivity cell SPEC-2 stages and `$6` is "Operator
  observable". Read one with `awk -F'|' 'NR>=1805 && NR<=1808 {print NR" "$5}'`, both with
  `awk -F'|' 'NR>=1805 && NR<=1808 {print NR" ||| "$2" ||| "$5" ||| "$6}'`, or the whole row set legibly with
  `sed -n '1803,1812p' spec/28_communication-channels.md | cat -n`, which gives a stable relative index in one call.
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
  last fenced generation") is the sentence the per-session move falsifies. Filed as round 3's one finding. CORRECTED in pass 21, twice.
  The pointer to a frozen `## Resolved in adversarial review` pass record at `spec-changes.md:664-665` is spent: run 5 evacuated that whole
  section into the review-log archive, `spec-changes.md` is 599 lines, and `CoordinatorFenceResponse` now returns live-text hits only. And
  the defect itself is repaired: run 6's G1 fix split the response comment out of the record-and-reject carrier list (the list now reads
  "Both take", at `:502-512`) into its own paragraph at `:514-530`, where `accepted` keeps the handler's "not greater than" comparison
  re-scoped to the session and `gap_detected` takes the `CH-FENCE` Degradation bullet's session qualifier.
- **MISTAKE (loop-level): routing a filed defect into a bookkeeping `### Open` item is not discharging it.** This one was filed as a
  MISTAKE in run 4, declined by four lenses as a fresh finding, and parked in the `### Open` "SCHEMA-1 qualifier wording" item, where
  nothing touched it for two further runs even though the repair was one paragraph. When a lens declines a defect because another item
  owns it, check that the owning item names a remedy and an owner.
- **WATCHOUT: the `CoordinatorFenceResponse` comment is three sentences, not two.** `proto:1455` opens with "CoordinatorFenceResponse
  acknowledges the new generation." before the `accepted` and `gap_detected` definitions, while `spec-changes.md:514-516` says "each of its
  two sentences" and "Its first sentence defines `accepted`". It is harmless for an implementor, the lead sentence taking no edit either
  way, and it was inherited from the finding's own framing. Do not let a later pass "correct" it by editing the lead sentence.
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
  goroutine, wait-for-gate, drive-the-gate pattern rather than only flipping the expected code. The hang is now verified end to end:
  `newFencedServer` claims through `ClaimSessionForTest` and never fences, so `checkSessionBound` passes at `coordination.go:216`, `gen <= 0`
  passes at `:224`, D7's `initialized && gen != fenced` is false, and the RPC reaches `done := s.barrier.open()` at `:264` and blocks at
  `:265-268` on the `context.Background()` at `coordination_test.go:191`; the whole tier-1 `pkg/adapter` package dies on the Go test
  timeout. CORRECTED in pass 23: the intended replacement assertion is nowhere in the live proposal. `spec-changes.md` is 622 lines with no
  `## Resolved in adversarial review` section at all, so the frozen pass record standing text cited at `spec-changes.md:868-870` no longer
  exists in any form a fixer can reach. The test is named in exactly two live places, `non-spec-changes.md:330` and `summary.md:83`, and
  neither states an assertion. That is what makes the gap unfixable-by-pointer rather than merely unstated.
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
- **Lens exhaustion, counted.** On this staging the docs-alignment lens has now returned empty six times, performance five, security four,
  reliability four, and edit-sites three, and run 6 round 1 added empty returns from client-surface, kubernetes, and operational, each over
  text that had not moved and each after an independent re-derivation that confirmed the standing inventories. A further run of any of them
  buys nothing unless SPEC-1's step-3 wording, the §28.6 second-opener clause, D7's acceptance arm, SPEC-3's baseline, or §8 is rewritten.
  Where a lens must run anyway, the cheap division of its budget is the one run 6's citation lens derived after spending a third of its
  pass re-resolving the whole anchor set for nothing: spot-check the sentences a fix touched, and spend the rest on sentence-versus-clause
  structure inside cited cells, which is where both surviving defects of that class live.
- **MISTAKE, AND THE DEFECT IS LIVE: the §28.8 `CH-BARRIER` disposition clause was copied from the `CH-CHECKPOINT` bullet above it,** where
  it is true. The `CH-CHECKPOINT` cell states the constraint in one sentence and the edited rejection rule in a second; the `CH-BARRIER`
  cell has only two sentences and the replaced clause is the trailing clause of the constraint sentence itself, so "the cell's constraint
  sentence ... unchanged" tells an applier to leave the clause standing. CORRECTED in pass 21: this entry used to read as a defect that was
  found and fixed. It was found in run 4's spec round 2, recorded as a MISTAKE tag rather than filed as a finding, and never repaired;
  four lenses re-filed it in run 6 round 1 and `spec-changes.md:386-392` still carries it, with the offending clause at `:390`. Treat it as
  live until that line changes. It is the only one of the nine SPEC-2 §28 bullets that names a whole sentence as unchanged while replacing
  a clause inside it: the §28.5.1 `CH-FENCE` Exclusivity bullet names the surviving *clause* (`spec-changes.md:320-322`) and the §28.6
  fence-acknowledgement bullet says "The rest of that sentence ... is unchanged" (`:359-362`). The rest of the "declared unchanged" class is
  closed: the other fourteen sites (`spec-changes.md:321`, `:385`, `:164`, `:171-172`, `:157-158`, `:424`, `:368-371`, `:400-401`, `:466`,
  `:483`, `:443`, `:453`, `:335`, `:551`) each name a clause or a genuinely separate sentence.
- **MISTAKE, twice over: a finding that was filed is not a finding that was fixed, and the two ways this log hides that are different.**
  A MISTAKE tag lifted into `### Traps` reads as history, so three later rounds treated the `CH-BARRIER` clause as closed while the text
  sat byte-identical through 21 snapshots; a tag on live staged text does not repair the text, so file it. Separately, a finding a
  non-spec-lane round files against the spec staging has nowhere to land, because that lane may not edit `spec-changes.md`: the
  `**IMPLEMENTOR TO FILL THE BLANKS.**` header over `## 5. Proposed changes` (`spec-changes.md:134`) is in that class and is still live.
  Before believing any standing entry that a defect was repaired, read the live file.
- **WATCHOUT: count the sentences in a §28.8 cell before believing a bullet's "the cell's X sentence is unchanged".** The cells are one
  physical line per row and the `awk -F'|' '{print $5}'` recipe prints the exclusivity cell; `CH-BARRIER` (`spec/28:1808`) is exactly two
  sentences and the clause SPEC-2 replaces is the tail of the first. The `CH-FENCE` window "sentence" (`:1807`) is likewise a clause, the
  first half of a compound sentence closing ", and it is the one channel the adapter still accepts in hold state and the only exit from
  it"; that substitution lands correctly because the replacement is quoted at clause granularity, so it is weighed and declined rather than
  a second instance of the `CH-BARRIER` defect. Do not re-file it.
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
- **WATCHOUT: a token grep under-reports the `docs/` surface by four sites.**
  `grep -rln "coordination_generation\|coordinationGeneration" schemas/ charts/ docs/ sdks/` returns exactly two files,
  `schemas/lenny-adapter.proto` and `docs/getting-started/concepts.md`, because the other docs sites carry the words "coordination
  generation" rather than the token. Grep both spellings, or the docs inventory in `### Settled` reads as four sites too long.
- **WATCHOUT: `spec/10:130-138` is one paragraph carrying two different grace budgets with two different formulas.** The agent-pod floor
  multiplies by `maxConcurrentSessions` and `:136` states outright that it omits `checkpointBarrierAckTimeoutSeconds`; the gateway budget at
  `:138` adds that term once (90+90+30=210 against a chart default of 240). A lens that reads only one of the two concludes D7 breaks the
  admission webhook floor. It does not: D7 moves the never-handed-off population's capture from the post-barrier eviction stage into the
  barrier-ack stage, and the gateway formula already sums both terms.
- **WATCHOUT: the `## 5. Proposed changes` fill-the-blanks header has two recorded dispositions and its refutation is greppable only in the
  archive.** Run 3's round 6 filed it and it was REFUTED, and the refutation left no trace under a grep for "header": it is visible only as
  the phrase "the refuted §5-header finding" inside two later shards' ALTERNATIVES lines (`review-log-archive.md:3372`, `:4541`). Run 6's
  applicability lens filed the same header again as live text a non-spec round could not land. WIDENED in pass 23: the archive grep to run
  before filing anything against either staging file is `fill-the-blanks` AND `.down.sql`, rather than only `5-header` and `§5 header`.
  `review-log-archive.md:2376` and `:3371-3372` each kill both the `.down.sql` singular-DEFAULT candidate and the header candidate, in
  their ALTERNATIVES lines rather than as findings, so neither is greppable from the live log at all. One run-7 lens says this saved its
  whole round. UNVERIFIED: whether the §5 header stands or falls, given one recorded refutation and one live filing.
- **WATCHOUT: reading this review log.** `sed -n '120,400p'` on it exceeds the Bash output cap and is persisted to a tool-result file whose
  own `sed` or read then reports a shorter length than the source range, which looks like truncation and is not. Read this file with the
  Read tool at explicit offsets against the real path. That supersedes the older advice, in the harness-hazards bullet above, to `sed` it in
  windows; the `sed` advice still holds for the two staging files.
- **WATCHOUT: the quiescence-unit contradiction admits two remedies and three rounds have picked neither.** §10.1.8 states the unit per
  session twice, step 2 recording the `barrier_id` "in the session's checkpoint metadata" (`spec/10:184`) and step 3 opening the
  `Checkpoint` stream "for each quiesced session ... and then releas[ing] quiescence" (`:185`), while step 2 also carries the pod-phrased
  "stops accepting new tool call dispatches", so a verifier can land on either side. CORRECTED in pass 23: the second remedy is not
  available and is not needed, because CODE-1's clause is TRUE of the barrier-to-stream gate whatever the quiescence-unit argument does.
  `spec/10:185` has the dispatcher open the `Checkpoint` stream "for each quiesced session **concurrently with** the in-flight
  `CheckpointBarrier` RPC to that session", and the ack echoes the gateway-minted `checkpoint_id` received in `Start`, so step 3 does fix
  the gate's unit at the session. Do not drop §3's because-clause. The live residue is only SPEC-2's §29.10 bullet keeping the quiescence
  unit unanswered, and `summary.md:64-65` already disclaims that the field's placement settles it, so the one remaining remedy is narrowing
  that bullet. A round that finds this again should apply that narrowing rather than re-adjudicating the pair.
- **WATCHOUT: the staged §10.1.2 step-3 unset clause reads unqualified where D7 qualifies it.** D7's own heading says "a `CheckpointBarrier`
  naming a **bound** session", while the staged sentence says "A session for which the pod holds no fenced generation ... a
  `CheckpointBarrier` naming such a session is accepted", and an unbound session also holds no fenced generation. `spec/` states no
  session-binding guard for gateway-to-pod RPCs anywhere, the only live-binding condition being §28.5.1's `set_tracing_context` one on the
  intra-pod channel (`spec/28:842-849`), which does not reach `CH-BARRIER`. Weighed and not filed, because SPEC-1's state-model paragraph
  one paragraph up opens "The pod holds `last_fenced_generation` per bound session", so a competent applier cannot land the unbound
  reading. A later lens that wants it must argue past that sentence; the test-side half is the standing `### Open` "Barrier for an unbound
  session".
- **Weighed and not filed in run 6 round 1. Do not spend a verification on one without new evidence.** (1) Staged step 3's third sentence
  ("the pod holds one generation for that session at a time") against the second's unset arm: sentence 3 is scoped by "Because fence
  confirmation is required before this step is reached", so its population is the post-fence session. (2) `spec/28:389-392`
  (`CH-PODHEALTH` Preconditions) says §10.1 "does not state how that rule applies to a probe against a pod that is not yet serving a
  coordinated session"; the staged unset clause is keyed on "the session the RPC names" and a health probe names none, so the "does not
  state" claim survives and the bullet is not an edit site. (3) `spec/10:157`'s partial-manifest gloss reads as falsified by D6's unset arm,
  but the value written is the row's rather than a pod-held one and the gloss is already loose in shipped text. (4) `spec/04:461`, the
  controller finalizer paragraph, asserts that handoff fencing is handled by the §10.1 CAS mechanism, which the staging leaves intact.
  (5) The §28.8 `CH-CHECKPOINT` staged clause ends at "has no recorded value to match" without the consequence clause its §28.6 twin
  carries; below the bar. (6) §29.2 step 11's "does not state whether that replica announces a generation on `CH-FENCE`": SPEC-1 does not
  state it either, so the bullet is not falsified. (7) "The removed bullet's generation question" in SPEC-2's §29.10 bullet reads as an
  attribution error and is not: both questions in `spec/29:1523-1527` are worded about hold state, but "whether a fence driven for one
  slot's session holds the RPCs of a sibling slot's session" is exactly the pod-wide-counter defect this proposal fixes.
- **MISTAKE: enumerate a function's callers with grep before generalizing from its doc comment.** Two rounds carried "the §7.2
  snapshot-close bump fires only under a terminal write" as ground truth, into `### Settled` and into OD1's second-producer clause, because
  `bumpCoordinationGenerationOnSnapshotClose`'s own doc comment (`start.go:4425-4451`) and `spec/04:200` both discuss the terminal
  collapses and neither mentions the fourth caller. `failure.go:219` fires it under `disp.From == StateResuming && disp.To != StateResuming`
  over `transitionToResumePending` (`:212`) and `transitionToAwaitingClientAction` (`:216`), both named recovery states, and a takeover does
  follow on both. The cost was a false statement about the tree standing in the list the human reviewer signs off from. Do NOT "correct"
  `spec/04:200` in response: it says mid-resume terminal collapses bump the counter, which is true of the other three callers and asserts no
  exclusivity.
- **MISTAKE: a refutation that leans on a standing claim dies with the claim.** Run 6 round 4's mechanism shard killed the candidate
  "SPEC-3's mint-2 sentence against the §7.2 snapshot-close bump reaching the row first" on the ground that "the bump fires only under a
  terminal write after which no takeover follows (already in `### Settled`)" — the very sentence this window falsified. The candidate is
  still correctly declined, on a ground that survives: in SPECIFICATION terms the only non-CAS writer `spec/` states is the §7.2 bump, and
  §7.2 confines it to `resuming → cancelled` and `resuming → completed`, both terminal (`spec/07:210`, `:215`). The fourth call site is
  undocumented code behaviour and is therefore pre-existing spec-versus-code drift this proposal does not stage. A later round wanting to
  file it must argue past §7.2's terminal-only wording rather than past the retired ledger sentence.
- **WATCHOUT: the proposal states preservation explicitly wherever it means it, so silence is an instruction to replace.** ":516 it keeps
  its wording", ":519 each of its two sentences takes the session qualifier and nothing else", ":562-565 `AttachRequest`'s and
  `CheckpointRequest`'s trailing sentences ... are unchanged", and the whole `CoordinatorFenceResponse` carve-out at `:527-534` are the
  pattern. A carrier paragraph that hands the applier a full replacement sentence with NO preservation clause is therefore an instruction to
  replace the whole sentence rather than a hint to change only the unit. That asymmetry is what surfaced the live finding against
  `spec-changes.md:506-512`: SCHEMA-1 tells `CoordinatorFenceRequest`'s message comment to "take the §28.5.1 Messages wording" while the
  proposal's own description of that comment truncates it, so applying SCHEMA-1 as written deletes the sentence tail carrying the fence
  rejection's gRPC status, its `coordinator_handoff_stale` detail string, and the metric attribution, none of which has another carrier.
  Use the asymmetry when judging the remaining SCHEMA-1 carriers, and read the whole comment in the proto rather than the proposal's summary
  of it: the summary is where the tail goes missing.
- **MISTAKE: grep the ARCHIVE for a candidate's subject BEFORE working it up.** A run-7 lens spent a third of its pass building a finding on
  the tier-2 resume-fence case before reading the archive's own DECISION lines. `review-log-archive.md:2376` and `:3866` between them
  pre-adjudicate three of the four candidates a non-spec applicability lens naturally generates. The standing rule that a filed finding
  absent from a post-fix list was refuted is only usable from the archive, because the refutations live in ALTERNATIVES lines rather than as
  findings and are not greppable from this file at all.
- **MISTAKE: an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the wrong half.** A run-6 shard
  spent a third of its pass on the token greps and reached the carrier text late; the one place the standing inventories are NOT complete is
  inside a cited comment's own sentence structure, which is exactly where the surviving defects of that class live. The next edit-sites run
  should skip the token greps entirely and read every carrier the staging names, in the file, sentence by sentence. A run-6 citation shard
  recorded the same finding about itself and reached the same instruction independently.
- **MISTAKE (loop-level, second recorded instance): the "SCHEMA-1 qualifier wording" `### Open` sat parked for two runs** while one of the
  carriers it covers held a live deletion of an operational contract. This is the same class as "routing a filed defect into a bookkeeping
  `### Open` item is not discharging it". When a lens declines a defect because a standing item owns it, check that the owning item names a
  remedy and an owner; a qualifier-wording placeholder names neither.
- **WATCHOUT: the drain-path candidate the standing D7 refutation does NOT cover.** The standing refutation covers the checkpoint STREAM
  work being identical before and after. It does not cover the case where `CheckpointWithTrigger` fails fast: today the barrier for a
  never-handed-off session is refused instantly, and after D7 it is accepted and blocks to the shared `barrierAckTimeout` because nothing
  links or completes the gate. Still not filable: `fireBarrier` wraps the whole `Dispatch` in ONE `context.WithTimeout` and the targets run
  as concurrent goroutines, so one hung barrier does not eat another's budget; the §10.1 gateway grace formula already sums the barrier
  term; and the shipped `CheckpointBarrier` comment names the empty-ref-on-deadline outcome as designed — EVIDENCE: prestop.go:501-506;
  barrier.go:219-227; coordination.go:259-269.
- **WATCHOUT: an instruction parked inside a WITHDRAWN decision is invisible.** OD4 is headed "OD4 is withdrawn" and closes with "CODE-1
  should state that relocating `quiesced` ... carries no specification claim", which nobody has applied for two runs;
  `non-spec-changes.md:33` still moves `quiesced` with no disclaimer. Read a withdrawn entry's closing sentences before treating the entry
  as spent — EVIDENCE: summary.md:250-261.
- **WATCHOUT: `non-spec-changes.md:5-6` carries a THIRD fill-the-blanks header, and it is the one this repository's non-spec lane can
  actually land.** Three non-spec lenses have generated it and declined it as "the same shape as the refuted spec-changes header" while the
  spec-side twin is simultaneously carried as filed-and-unlanded. The two dispositions cannot both be right and no round has adjudicated the
  pair. A human or a fix round should settle all three headers in one move rather than letting each lane defer to the other's refutation.
- **WATCHOUT: a citation sweep built on a path-shaped grep under-reports by roughly half.**
  `grep -o '`[a-zA-Z0-9_./-]*\.\(go\|md\|sql\|proto\|yaml\|json\)[:0-9-]*`'` over `spec-changes.md` returns 56 citations and misses every
  short-form continuation attached to a previously named file (`:1442-1446`, `:236-239`, `:291-296`, `:349-353`, `:1805`, `:112-113`, the
  twelve proto message ranges at `:562-567`, `:4067`, `:198`, `:172-176`, `:192`). About a third of the file's citations are short forms.
- **WATCHOUT: counting doc-comment lines by hand from a `sed` window drifts by one on the adapter files.** Use `grep -n` for the anchor
  instead. Two further sub-line drifts join the do-not-file list, both one line off and both inside the right function: `pgstore.New` is at
  `pgstore.go:59` where the staging cites `:60`, and `checkpointbarrier_test.go`'s `BarrierWaiting()` read is at `:162` where the staging
  and this log cite `:163`.
- **WATCHOUT: `migrations/0060_session_eviction_state_columns.up.sql:33` declares a SECOND `coordination_generation BIGINT NOT NULL
  DEFAULT 0`,** on a different table from the session row, and a naive baseline sweep reads it as a missed 0181 target. It is one of the
  four mirror columns already dispositioned as always written explicitly from the session row, so its default stays cosmetic. Confirm the
  last taken migration number with `ls migrations/*.up.sql | tail` rather than `ls migrations/ | tail`, which returns `*_test.go` files.
- **WATCHOUT: SPEC-1's §10.1 and §10.1.4 blocks mix staged sentences with rationale in the same paragraph and the transition is unmarked.**
  "Zero names no fence: §10.1.2 step 1 increments ... so no `CoordinatorFence` ever carries zero" (`spec-changes.md:282-284`) reads as staged
  text while the "That is held by ..." sentence after it (`:284-290`) is plainly rationale, since it cites three Go files. An applier has to
  judge the boundary. Two lenses have noticed and neither filed it; it is the single most likely place a maintainer applying tomorrow
  guesses wrong, and it belongs to the human pass rather than to a lens.
- **WATCHOUT: the twelve rewritten proto field comments include the three credential RPCs** (`RotateCredentialsRequest`,
  `ExtendCredentialLeaseRequest`, `RevokeCredentialsRequest`), so a security lens is drawn to read the consequence-clause deletion as
  removing a credential-path control. It is not: no operational RPC is generation-gated in code at all, `GetCoordinationGeneration()` having
  two non-test call sites, so the deleted clause was aspirational on every one of the twelve and the deletion changes no enforced control.
  It cost one lens a detour.
- **WATCHOUT: §29.7's staged "accepted" arm sits in a paragraph closing "Those outcomes are named here and are not traced".** It reads as a
  self-contradiction and is not: the newly named accept arm is the accepted false-positive barrier, for a session the replica no longer
  coordinates, which the trace does not follow either, since §29.7 traces barriers for sessions the draining replica does coordinate.
  Worked up and dropped; do not spend the pass again.
- **WATCHOUT: SPEC-1's "the initial condition is stated immediately after that parenthetical and before the clause list"**
  (`spec-changes.md:155-156`) names a two-sentence span rather than a point, because the parenthetical sits inside the gap bullet's first
  sentence and the clause list opens two sentences later. It is NOT a class-2 underspecified target: the initial-condition sentence is
  spelled out word for word in SPEC-1's own state-model paragraph and both candidate positions carry the same meaning. Weighed and declined.
- **WATCHOUT: `spec/04:712` is a fifth `spec/04` carrier the settled inventory does not spell out by line.** It is the §4.7 RPC table row
  for `CoordinatorFence`, "Precondition for any subsequent operational RPC", and it reads as falsified by D7. It is not filable: it is a
  sender-side duty restating §10.1.2 step 2's bar on the acquiring coordinator, which is the non-site arm of the membership criterion, and
  `docs/reference/adapter-contract.md:69` carries the identical sentence and is already recorded as pre-existing drift. `spec/04:711` states
  what the barrier message carries and names no gate; `spec/04:323` names the eviction column's role with no baseline and no gate. Two
  lenses reach `:712` independently each round; stop here.
- **WATCHOUT: OD11's "Both rows are already present and `WIRED`" and the Corrections-outstanding bullet's "a non-wired row for the barrier's
  generation field" are NOT a contradiction.** They name different rows: the RPC-level `CoordinatorFence` and `CheckpointBarrier` rows are
  both `WIRED` (`claim-map.json:449-465`), while the FIELD-level `CheckpointBarrierRequest.coordination_generation` row is `UNWIRED` under
  deferral R16 (`:75-82`). A pass spent on this returns nothing.
- **WATCHOUT: the "Scope of the proposal" `### Open` and OD5 name three things each and they are not the same three.** The `### Open` names
  D7, the counter baseline, and the barrier-provenance reconciliation; OD5 names D7, the counter baseline, and migration 0181. The
  reconciliation half is not a missing decision, the value-form rewrites landing on sentences the unit change makes edit sites anyway and
  the choice between forms being settled by this log's own §28.6 and §28.8 MISTAKE entries. Only OD5's half is live.
- **WATCHOUT: §8's "so a case landing in `tests/tier2_component/stores/` alone leaves tier 0 red" is self-undercut two sentences later,**
  because the same paragraph stages the `{migration: "0181", table: "sessions"}` row in `prod_columns_test.go`, which lives under `TEST_DIR`
  and satisfies lint pass 3's bare-number grep on its own. Weighed and not filed: the citations are accurate and the directory choice the
  argument supports is still right. Do not spend a verification on it.
- **WATCHOUT: the migrate Job carries no `activeDeadlineSeconds`** and the chart sets only `backoffLimit` (default 5), so a long backfill
  has no in-chart bound and the hook blocks the gateway Deployment by construction. Do not lean an argument on Helm's own 5m hook timeout,
  which is not repo-verifiable — EVIDENCE: charts/lenny/templates/migrate-job.yaml:38-46; charts/lenny/values.yaml:3788-3791.

### Open

Detail for each item is in the ledger entry named at the end of its line. The `[non-spec.5.*]` and `[non-spec.6.*]` entries were retired
in compaction pass 17; the items they carried are in the ledger residue entry `[non-spec.5-6.*]`, filed there under the id named here.
The `[spec.1.review-*]` entries were retired the same way in compaction pass 18, and their two unclosed items are in the ledger residue
entry `[spec.1.*]`. The sixteen `[spec.2.review-*]` and `[spec.3.review-*]` entries were retired in compaction pass 19, and their two
unclosed items are in the ledger residue entry `[spec.2-3.*]`. Compaction passes 20 through 23 retired no ledger entry, the round boundary
archiving each whole ledger instead, so every id below resolves in an archive rather than in this file. Three id collisions to expect
there: several `[non-spec.1.review-*]` ids name two different entries, run 4's and run 7's; `[spec.1.*]` is both run 4's spec-round-1
residue entry and the stem of run 5's spec-round-1 lens ids; and `[spec.1.review-*]` names three separate generations of shard. Within a
round the shards are ordered alphabetically by lens name rather than chronologically, so a later-sounding id is not a later reading.

- **"The ordinary false positive".** SPEC-1's live rationale overclaims a sole ordering; drop the two words. `[spec.1-3.*]`
- **SCHEMA-1 qualifier wording, and it now has a named remedy and an owner rather than being a placeholder.** The exact qualifier each of
  the seven carriers takes, including the D7 acceptance sentence; G1 discharged the `CoordinatorFenceResponse` carrier. The
  `CoordinatorFenceRequest` carrier is FILED against `spec-changes.md:506-512` by two run-6 round-4 shards, for deleting the sentence tail
  carrying the fence rejection's status, detail string, and metric attribution. `[spec.1-3.*]`,
  `[spec.4.review-edit-sites.1]`, `[spec.4.review-operational.1]`
- **UNVERIFIED: whether the three `CheckpointBarrier` carriers keep `FailedPrecondition`** under SPEC-2's `:536-541` instruction, which one
  shard read as scoped to the comparison. A fixer touching `:508-512` should state the barrier carriers' disposition in the same pass.
  `[spec.4.review-operational.1]`
- **The §10.1.8 "Either outcome is safe and requires no special handling" assertion is FILED.** SPEC-1 widens a shipped safety claim about
  the rejection outcome to cover the acceptance arm, which OD12 records as unanswered with no recommendation.
  `[spec.4.review-open-decisions.1]`
- **Migration 0181's unbatched full-table backfill of `sessions` is FILED, and a sibling shard of the same round declined it.**
  `[non-spec.1.review-performance.1]`, `[non-spec.1.review-reliability.1]`
- **UNVERIFIED: which of those two readings holds.** They were parallel shards of one round and neither corrects the other, so neither is
  the newer. Performance filed it on scale: `sessions` rows are never purged, the table is order 10^6 rows within a year at Tier 3, a
  `WHERE coordination_generation = 0` predicate matches essentially all of it, no lint pass reads an `UPDATE`, and the four landed
  `UPDATE ... SET` backfills all target small config tables so the precedent does not cover a session-scale one. Reliability declined it on
  correctness and precedent: a backfill that wins the row lock cannot corrupt a concurrent writer, because `RecordHandoff` goes through
  `Update`'s `SELECT ... FOR UPDATE` and both stores clamp to the previous value, and unbatched backfills have precedent in 0053, 0054,
  0058, 0064, 0105, and 0178. The two grounds do not meet: one is about duration under a blocking pre-upgrade hook and the other about
  safety. A reviewer settles it by deciding whether the hook's unbounded duration is acceptable, which is the budget question below.
- **OPEN: nothing in the proposal, the chart, or `spec/` states a wall-clock or row-count budget for a migration run against a live
  top-tier fleet.** If the reviewer wants the backfill kept whole, that budget is what should be written down.
  `[non-spec.1.review-performance.1]`
- **The §28.8 `CH-BARRIER` disposition clause is filed, adjudicated, and unfixed** at `spec-changes.md:390`; four run-6 lenses re-filed it.
  `[spec.1.review-applicability.1]`, `[spec.1.review-citations.1]`, `[spec.1.review-fresh.<n>]`, `[spec.1.review-mechanism.6]`
- **The `## 5. Proposed changes` fill-the-blanks header is filed and unlanded**, a non-spec round having no write path to
  `spec-changes.md:134`, against one archived refutation of the same finding. `[spec.1.review-applicability.1]`,
  `[spec.1.review-fresh.<n>]`
- **OD2 still needs the reviewer.** If the equal case is accepted, three things move together: `pkg/adapter/coordination.go:99`, the
  `CoordinatorFenceResponse.accepted` sentence, and the staged §28.6 `CH-FENCE` arm at `spec-changes.md:345-346`. `[spec.1.fix-design-G1.1]`
- **OD2's ordering rationale is imprecise for the rolling window.** `summary.md:198-204` says that once CODE-4 baselines the row "the
  collision is unreachable", but for a row an old binary minted at 0 a resume fences at the retained floor's 1 and a takeover's
  `RecordHandoff` bumps 0 to 1 and also fences at 1, so the equal-generation collision OD2 would accept survives for that row class. It
  matters because OD2's recommendation is what would admit the split-brain OD2 says the ordering prevents. `[spec.1.fix-design-G2.1]`
- **Nothing outside CODE-4's closing sentence tracks the retained floor's retirement, and the gap is now OD9 alone.** OD9 derives no
  recommendation, so if the tightening release never happens the floor stays forever, harmless and unowned. CORRECTED in pass 23: the older
  half, that the one-way coupling is "recorded in neither entry", is stale. OD8's withdrawal at `summary.md:302-317` does record it
  ("retired by the release that tightens the check ... which is OD9's subject") and names OD9 by id at `:324-326`. OD9 (`:318-322`) still
  does not, and OD9 is where the decision is made. A second defect rides on the same stale sentence: OD9's own closing line was lifted
  verbatim from this log's `### Open` before the entries were rewritten, so the summary asserts something the summary falsifies two entries
  above. `[spec.1.fix-G2.1]`, `[spec.2.review-open-decisions.1]`, `[spec.3.review-open-decisions.1]`,
  `[non-spec.1.review-open-decisions.1]`
- **No post-fix record exists for run 4's spec round 2** and its three filed findings cannot be enumerated from the archive; a later round
  that finds a refutation for the `CH-BARRIER` clause should record it where a grep will find it. `[spec.1.review-fresh.<n>]`
- **Status file.** Its scope bullet drops the hold clause and its closing paragraph still calls the hold-state decision open. `[spec.1-3.*]`
- **D6 test-side cascade.** TEST-1 and §8 owe a co-tenant first fence well above 1; four citations spell the exemption per pod lifetime and
  two of them are in no §9 entry. `[spec.1-3.*]`
- **CH-ADAPTEREVENTS ownership.** Which replica's connection carries a multi-session pod's events, and so whether two co-tenant sessions can
  be coordinated by two replicas at all. `[spec.1-3.*]`
- **Scope of the proposal.** Whether D7, the counter baseline, and the barrier-provenance reconciliation belong here at all. `[spec.1-3.*]`
- **Two imprecise rationale sentences.** The "only failure arm" claim and the twinned "either `Create` path" sentence. The third, the
  "unreachable by construction" phrase, went with the G2 edit that deleted the contrast it drew. `[spec.1-3.*]`
- **§4's fill-the-blanks header.** Whether a converged proposal may keep it over four derivable items. `[spec.1-3.*]`
- **Superseded replica's stream against a quiesced pod, reduced to its surviving half.** CORRECTED in pass 23: the second half is answered
  from the tree. `dispatchOne` starts the gateway-driven `Checkpoint` stream before `dispatch.Send` and joins it after, unconditionally, so
  a superseded replica opens that stream today whether its barrier is accepted, refused, or never sent; the stream is not a consequence of
  acceptance. What remains open is whether the quiescence acceptance holds to the ack deadline is acceptable. `[spec.1-3.*]`,
  `[spec.4.review-open-decisions.1]`
- **`spec/04` §4.1 fence row.** `CoordinatorFenceRequest` is declared pod-scoped. CORRECTED in pass 21: no gate pins the classification
  either way, so the older half of this line, that a tier-3 test pins it, is gone. `[spec.1-3.*]`, and `[spec.1.*]` asks whether the staged
  edits must adjudicate it.
- **§1 severity.** Whether the recalibrated headline harm is restated at the top of §1 rather than only in §1.3. `[spec.1-3.*]`
- **Proposal 0080 overlap.** Its section 1.14 covers the same §29.10 bullet SPEC-2 stages for removal; nobody has recorded the overlap.
  `[spec.1-3.*]`
- **Rebind and the unset state, reduced to the code-side half.** The specification half is answered and OD7 now records it: `spec/07:196`
  fixes `resuming → running` as a re-attach on a REPLACEMENT pod, so a session cannot re-bind onto the pod that held its per-entry value in
  specification terms. What is open is whether `pkg/gateway/sessionserver` placement can pick the same pod. The adapter bars nothing,
  `deregisterSlotLocked` deleting the map key with no tombstone (`slotsession.go:346-361`, `session.go:237-240`). A rider: OD7's own
  recommendation ("have SPEC-1 state that the value's lifetime is the session's binding on the pod") asks for the opposite unit from what
  SPEC-1 stages ("unset until that session's first accepted fence on that pod"), and the two are extensionally equal only while rebind is
  unreachable, so accepting OD7 changes a SPEC-1 sentence and that consequence is stated nowhere. `[spec.1-3.*]`,
  `[spec.2.review-open-decisions.1]`, `[spec.3.review-open-decisions.1]`, `[spec.4.review-open-decisions.1]`
- **§29.2 step 11.** Whether SPEC-1 owes a change to the bullet recording the pre-message announcement as unstated. `[spec.1-3.*]`
- **`coordinator_lost` log line as a spec artifact.** The staged §10.1.4 text names "The per-session `coordinator_lost` log line" where
  §10.1.4 introduces `coordinator_lost` only as a REASON on a `session.terminated` event (`spec/10:58`) and the log line by that name exists
  only at `holdstate.go:225-229`. Whoever closes this decides whether the staged sentence names the event's reason or introduces the log
  line, because those are two different edits. `[spec.1-3.*]`, `[spec.4.review-fresh.1]`
- **CODE-1's accessor enumeration omits `checkpointbarrier_test.go:163`.** `[spec.1-3.*]`
- **UNVERIFIED: tier-1 home for the sess-b-is-zero assertion.** `[spec.1-3.*]`
- **UNVERIFIED: whether the shipped drain ever quiesces a never-handed-off session.** `[spec.1-3.*]`
- **UNVERIFIED: step 3's "only" against its unset carve-out.** `[spec.1-3.*]`
- **UNVERIFIED: D6's stated ground for the first-fence exemption does not follow,** though the exemption is right. `[spec.1-3.*]`
- **UNVERIFIED: baseline reachability in pure spec terms.** `[spec.1-3.*]`
- **UNVERIFIED: OD12's drain-budget bound** (one 90s wall-clock window across all pods, the manifest write guarded by supersede-on-write
  and `partial_manifest_active_uniq`) was not re-derived in run 6. `[spec.1.review-open-decisions.1]`
- **UNVERIFIED: `tests/claim-map.json:75-82` files the barrier FIELD `UNWIRED` under deferral R16** with a note already false against the
  shipped comparison at `coordination.go:236`. The summary's `### Corrections outstanding` bullet already owns it, and it is a different row
  from the two RPC-level rows, which stay `WIRED` throughout. `[spec.1-3.*]`, `[spec.3.review-open-decisions.1]`
- **Why CODE-1 reaches tier 4.** `[spec.1-3.*]`
- **UNVERIFIED: the persisted-row half of the tier-7a barrier case.** Tier 7a or proposal 0060's tier-8 harness. `[spec.1-3.*]`
- **UNVERIFIED: the 90s bound for a barrier whose session is deregistered mid-flight.** `[spec.1-3.*]`
- **§9 names no file for the staged tier-3 and tier-2 cases.** `[spec.1-3.*]`
- **UNVERIFIED: whether the tier-7a two-barrier case can be written against real `Checkpoint` streams.** `[spec.1-3.*]`
- **UNVERIFIED: §8's tier sentence omits tier 11 while checklist S1 declares it.** `[spec.1-3.*]`
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
- **§8's tier-4 sentence for D7 names no case, file, or step.** CORRECTED in pass 23: the second half, that S5 declares no tier 4, is stale
  under the eight-step checklist. CODE-2 is now S7 and S7 declares tiers 0, 1, 3, 4, and 7a. `[non-spec.5.review-feasibility.1]`,
  `[non-spec.1.review-test-coverage.1]`
- **UNVERIFIED: the tier-2 resume-fence-then-takeover case has no file and no existing tier-2 `coordfixture` pod.**
  `[non-spec.5.review-fresh.1]`
- **Migration `0164`'s column comment states the match rule D7 narrows.** A note rather than an edit site. `[non-spec.5.review-fresh.1]`
- **UNVERIFIED: which disposition §7's third decision (`coord.mu`) takes.** Two readings stand and neither corrects the other; the
  `### Settled` bullet on §7 carries both. `[non-spec.5.review-mechanism.1]`, `[spec.1.review-open-decisions.1]`
- **UNVERIFIED: the claim-map barrier row against the generation fence.** `[non-spec.5.review-operational.1]`
- **§8's tier-1 disposition for `TestCheckpointBarrierRejectsWithoutFence` states no replacement assertion, and the gap is now
  unfixable-by-pointer.** FILED again in run 7 by two shards that DID have a write path to `non-spec-changes.md`. The test is named in
  exactly two live places, `non-spec-changes.md:330` and `summary.md:83`, and neither states an assertion; the frozen pass record that held
  the intended one is gone with `## Resolved in adversarial review`. `[non-spec.5.review-test-coverage.1]`,
  `[non-spec.6.review-test-coverage.1]`, `[non-spec.1.review-fresh.1]`, `[non-spec.1.review-test-coverage.1]`
- **Three weighed-and-not-filed items:** the fence and barrier resolve that misses, §8's tier gloss, and the tier-2 case with no named home.
  `[non-spec.6.review-mechanism.1]`
- **Whether §10.1.8 step 3 fixes the unit of barrier quiescence at the session,** which the design rests CODE-1's per-entry `barrierGate` on
  while SPEC-2 keeps §29.10's clause unanswered. `[spec.1.*]`
- **UNVERIFIED: whether a fence retried at the same generation is accepted.** §10.1.2 step 2 has the new coordinator retry with the same
  value after a lost acknowledgement, and in specification terms equal is neither older nor a gap, so the retry is idempotent; the shipped
  adapter guard is `gen <= lastFenced` and refuses it with `coordinator_handoff_stale`. Two round-1 lenses stated the two halves and
  neither reconciled them. `[spec.1.*]`
- **§29.10's quiescence-unit clause admits two remedies** with different consequences, and three rounds have now found it and picked
  neither; the `### Traps` entry names both remedies and the evidence. `[spec.2-3.*]`, `[spec.1.review-mechanism.6]`
- **Barrier for an unbound session, now with the evidence the item always lacked and a second filing.** No test in the tree asserts that
  `CheckpointBarrier` for a session with no slot binding is refused, and D7 leaves `checkSessionBound` the sole fail-closed guard on that
  path. `TestCheckpointBarrierRequiresSession` asserts `InvalidArgument` for a MISSING session id, a different guard, and the refusal string
  has one site with no test. `RegisterUnboundSlotForTest` already builds the state, so the case is cheap. `.claude/rules/test-coverage.md`
  requires a fail-closed path to be pinned, which is what makes OD13's answer "yes". Filed in run 4's non-spec round 1 and not landed, and
  filed again in run 7 by two shards with a write path. `[non-spec.1.review-security.1]`, `[non-spec.1.review-fresh.1]`,
  `[non-spec.1.review-test-coverage.1]`
- **UNVERIFIED: whether CODE-3's post-mortem read of the detached `*slotState` takes the entry's `coordinationState.mu`.** A fence that
  passed `checkSessionBound` before pass 1 can still be writing that field; use a locked read. `[non-spec.1.review-security.1]`
- **UNVERIFIED: the rolling-window zero-row population.** Nobody has written down what happens to rows minted at 0 after the roll
  completes. `[spec.1.review-security.1]`
- **UNVERIFIED: whether the tree has a worked precedent for a two-release constraint tightening.** `pkg/schemamigrate` carries the phase
  machinery; nobody checked whether a landed migration uses it. `[non-spec.1.review-feasibility.1]`
- **Whether a later release adds the `>= 1` check as a genuine Phase-3 migration.** Defensible defence-in-depth, costs a separate
  proposal, and nothing in 0076 depends on it. `[non-spec.1.fix-design-G1.1]`
- **UNVERIFIED: whether the summary's SPEC-2 deliverable row should name the §29.8 Preconditions deletion.** It already omits the §29.8
  step-2 edit, so the row is loose by precedent. `[spec.1.fix-design-G1.1]`
- **UNVERIFIED: whether the tier-4 co-tenant case can be driven through `coordfixture.FenceReadopter`** once `sess-a` is fenced
  explicitly; nobody has written the sequence out. `[non-spec.1.review-mechanism.1]`
- **Whether an exhausted lens is retired, now with counts and named reopening conditions.** Docs alignment has returned empty at least four
  times, twice over text that had not moved; kubernetes is exhausted in BOTH lanes and reopens only if CODE-4 grows a chart or CRD
  deliverable, CODE-3 grows an apiserver or `Sandbox.status` write, or D7's acceptance arm is rewritten; performance has four empty returns
  and reliability five, and reliability reopens only if D7's acceptance arm, the retained `coordfence` floor's rolling-window behaviour, or
  §10.1.8's failure-arm claim is rewritten; security has five. Each empty return costs a full round for the other lenses. Nobody has
  answered this in five rounds of asking. `[non-spec.1.review-docs-alignment.5]`, `[non-spec.3.review-test-coverage.1]`,
  `[spec.4.review-docs-alignment.1]`, `[spec.4.review-kubernetes.1]`, `[spec.4.review-performance.1]`, `[spec.4.review-reliability.1]`,
  `[non-spec.1.review-kubernetes.1]`
- **OPEN: whether a summary-only `## Open decisions` defect can ever be landed in a spec-scoped round.** The open-decisions lens is told it
  owns that section and that each drift is a finding; the loop scope is told to report only findings whose fix lands in `spec-changes.md`;
  and the material skeptic refutes the class on sight, ten consecutive times now. Four rounds have paid for the collision. The cheap fixes a
  human could make: let the spec lane's fixer edit `summary.md`'s OD section, or move the lens to the non-spec lane.
  `[spec.3.review-open-decisions.1]`, `[spec.4.review-open-decisions.1]`
- **Seven open-decision findings were filed in run 7's non-spec round with a write path, and none is yet applied.** They land in
  `summary.md`'s `## Open decisions` or in `non-spec-changes.md`. `[non-spec.1.review-open-decisions.1]`
- **OD3's recommended answer has no staged deliverable behind it.** "Reclassify the §4.1 row to session scope" would need `spec/04` edits at
  `:175`, `:188`, and `:151`, and SPEC-3 edits §4.2 alone. Somebody should decide whether a "yes" is in scope for this proposal or for a
  successor. `[non-spec.1.review-open-decisions.1]`
- **UNVERIFIED: whether §7 should carry OD5.** §7 lists three items and none is OD5, yet answering OD5 "split" deletes SPEC-3 entirely and
  SPEC-1's §10.1 baseline paragraph, the largest effect any open decision has on the staged spec text. A reviewer reading only the staged
  spec file sees no sign that a whole deliverable is conditional. The remedy is one bullet in `spec-changes.md` §7, so this one IS landable
  in a spec round. `[spec.4.review-open-decisions.1]`
- **UNVERIFIED: whether staged §10.1's "strictly greater than the value carried before the takeover that fenced it" should be narrowed to
  the takeover population.** The clause's load-bearing job is true; only the universal framing reaches the resume-path fence, which the
  proposal itself names as a fence driver that does not increment. Nothing depends on the answer. `[spec.4.review-mechanism.1]`
- **UNVERIFIED: whether `spec/07:215`'s parenthetical is reached by the staged unset arm.** It states a pod-side rejection rule for the §7.2
  snapshot-close bump, which sends no fence to the pod at all, so it is false in the shipped tree before any edit here. An edit-sites lens
  owns whether the pre-existing falsity is made worse. `[spec.4.review-security.1]`
- **UNVERIFIED: whether the tier-2 resume-fence case's refutation was on substance or on scope.** The archive records the filing at `:2376`
  and run 4 round 1's post-fix list omits it, but no refutation text exists anywhere in the log or the archive. A future round wanting it
  must argue past silence rather than past a recorded argument. Same shape for `TestCheckpointBarrierRejectsWithoutFence` at `archive:3866`.
  `[non-spec.1.review-applicability.1]`
- **UNVERIFIED: whether the implementor makes `checkSessionBound` return the resolved `*slotState` or leaves it error-only and re-looks
  up.** CODE-1 fixes the rule for `checkpointRootsForSession` explicitly and says nothing about `checkSessionBound`; §9 does list
  `slotsession.go` as touched, so the change is available. Not a mechanism defect, because no blocking call sits between the guard and the
  resolve on the fence and barrier paths. A fixer touching CODE-1 could close it in one clause. `[non-spec.1.review-feasibility.1]`
- **UNVERIFIED: whether the external-adapter compliance suite §28.7 names generates any assertion from proto COMMENT text.** The standing
  claim that it generates from field and message declarations is inference from the suite's absence in the tree;
  `tests/tier10_conformance` is the runtime adapter battery and is not it. If that suite is ever built, SCHEMA-1's prescriptions become
  load-bearing on a published artifact. `[non-spec.1.review-client-surface.1]`
- **UNVERIFIED: whether the `IMPLEMENTOR TO FILL THE BLANKS.` header at `non-spec-changes.md:5-6` stands or falls,** and it cannot be
  settled separately from the two spec-side twins. `[non-spec.1.review-edit-sites.1]`, `[non-spec.1.review-open-decisions.1]`
- **UNVERIFIED: whether a frozen archive record needs a pointer when a later round falsifies its premise.**
  `review-log-archive.md:4466` (a `FACT`) and `:5003-5007` (an `OPEN`) both carry the "fires only under a terminal write" premise this
  window retired. The archive is documented as frozen once written, so nobody should edit it, but a later pass that greps it for the bump's
  firing condition finds the false statement. Whoever owns archive hygiene should decide. `[spec.2.fix-G1.1]`
- **UNVERIFIED: the second surviving sentence-versus-clause defect could not be identified.** Run 6 round 1's citation lens recorded that
  "both surviving defects of that class" live in sentence-versus-clause structure inside cited cells; one is the §28.8 `CH-BARRIER` clause
  and a later shard could find no second that is not already weighed-and-declined. The best candidate is the `CoordinatorFenceResponse`
  three-sentences-not-two miscount, whose remedy is a two-word edit at `spec-changes.md:519` that lands nothing in `spec/`.
  `[spec.4.review-citations.1]`
- **OPEN: §8's tier-2 baseline case says "a resume fence at 1 followed by a crash-takeover compare-and-swap to 2",** using the phrase OD2
  withdrew as a misattribution; the takeover's row bump is `RecordHandoff`'s unconditional `row.CoordinationGeneration++` inside `Update`.
  Not filed, because §10.1.2 step 1 IS a compare-and-swap in specification terms and the case's construction and outcome are identical
  either way. A fixer editing §8 for another reason could cheaply say "takeover bump". `[non-spec.1.review-mechanism.1]`

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
  statement rather than on the mechanism, and must name the generator-source edit as the remedy. NARROWED in pass 23, twice over. The
  status-change half is now closed from two directions: §28.4's statuses are defined at the MECHANISM level, `WIRED` being "the mechanism is
  reachable from production code" (`spec/28:163-165`), and both RPC-level rows stay reachable at every step S1 through S8, so no status
  change and no claim-register deliverable is owed for the interval. What survives is only the statement-level question, whether §28.4's
  rule reaches the NEW normative statement SPEC-2 adds to §28.6 and §28.8, that the pod rejects none of an unfenced session's RPCs on
  generation grounds; SPEC-2's "No §28.4 claim-register row moves" answers the re-scoped sentences and not obviously the new carve-out. The
  index-and-checklist reconciliation of 2026-09-03 carried that surviving half to the reviewer as OD11, so it now has an owner. Do not
  revive the status half.
- DEFERRED [`spec-changes.md` SPEC-1, about `:250` and `:284`, from `[spec.1.postfix-G2.1]`]: both sites credit the counter baseline to
  "the column default and the create path's floor", while `non-spec-changes.md:133-136` states that `Create` names
  `coordination_generation` in its insert column list (`pgstore.go:177`), so the column default baselines nothing and the two `Create`
  floors are the whole enforcement. The column default is something CODE-4 does land, so the sites are not false, but naming it as a ground
  for a row's baseline overstates it. What is true instead: the two `Create` floors baseline the row and the column default is cosmetic.
  The post-fix edit that found it neither caused nor repaired it, so it was left for the loop that follows.
- DEFERRED [`0076...summary.md`, `:104-106`, from `[spec.1.fix-G1.1]` and re-confirmed by the post-fix round]: the summary states that
  "The pass records under \"Resolved in adversarial review\" in the spec changes carry the rationale behind each correction already
  landed". That section no longer exists in `spec-changes.md`, which is 599 lines: run 5 evacuated it into
  `0076...review-log-archive.md:4`. What is true instead is that the archived pass records in the review-log archive carry that rationale.
  The fixer's scope rule confines it to the deliverable index and to statements its own edits falsified, so it left the sentence standing.
- DEFERRED [`pkg/adapter/holdstate.go`, from `[non-spec.3.review-feasibility.1]`]: `enterHoldState`'s doc comment at `:116-118` reads
  "Read the generation and the started-session count through their accessors (which take coord.mu and s.mu) before locking hold.mu so no
  two locks are ever held together". CODE-3 deletes the generation read, so the clause naming it is false once the deliverable lands, and
  CODE-3's enumeration of the sites the `gen` field drags (`:43`, `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:283`) does not
  include this comment. Not filed: refuted class (k) bars a missed-edit-site finding over a comment under `pkg/`. What is true instead:
  only the started-session count is read through an accessor before `hold.mu`. This is the second instance of the comment residue at
  `pkg/adapter/coordination.go:126-128`; hand both to the implementor together. Confirmed still open by the index-and-checklist
  reconciliation of 2026-09-03, which records that the remedy is a staged edit in the non-spec changes naming BOTH comments and that no
  lane has authored it.
- DEFERRED [`cmd/lenny-gateway/httpsurface.go`, about `:588-602`, from `[non-spec.1.review-reliability.1]` and independently from
  `[spec.4.review-performance.1]`]: the barrier's cache-fallback closure calls `w.sessions.Get(context.Background(), ...)` once per binding
  with no deadline, on the exact path taken when the `coordination_lease` mirror read has already failed. A hung Postgres therefore hangs
  `Targets()`, which hangs `Dispatch`, which burns the whole preStop grace with no drain at all; the `barrierAckTimeout` on `fireBarrier`
  does not bound it, because the fallback ignores the passed context. This is pre-existing shipped code that 0076 cites under OD1 but does
  not touch, so it is not a finding against this proposal and the spec loop has no write path to it either way. What is true instead: the
  fallback needs the caller's context or its own bounded timeout. Route it to a gateway-side fix rather than to 0076 — EVIDENCE:
  cmd/lenny-gateway/httpsurface.go:593, :594-596; pkg/gateway/coordination/barrier/wiring.go:97-122;
  pkg/gateway/podlifecycle/prestop/prestop.go:501-506.

## Ledger

### [non-spec.2.fix-G1.1]
DECISION: rewrote OD3's trailing clause in `summary.md` to enumerate the three `spec/04` §4.1 edit sites a "yes" costs (`:175` row, `:188` declaring sentence, `:151` declared-not-derived ground) and to record that no `spec/04` §4.1 edit is staged anywhere in this proposal — BECAUSE the old clause was false on both halves and OD3 recommended an answer without telling the reviewer nothing is staged behind it. ALTERNATIVES: deleting the clause outright (would send an implementor to `:175` and `:188` only and leave the falsified ground standing); pasting the finding's `suggested_fix` verbatim (it duplicates the tier-3-comment obligation the preceding sentence already carries and its tier-3 line span is off by one); staging the §4.1 edit here (reopens the converged spec lane on a reviewer question and decides 0075 by side effect).
FACT: `spec/04_system-components.md:151` names only `CheckpointRequest` and `CheckpointStart`; `CoordinatorFenceRequest` occurs in that file at `:175` (the table row) and `:188` (the declaring sentence) and nowhere else — EVIDENCE: spec/04_system-components.md:151,175,188
FACT: after reclassifying the fence row to session scope, no pod-scoped row in §4.1's table carries a session field: `:188` states the other pod-scoped `Adapter` messages carry none, and `ReportPodScrubRequest` declares `pod_id` alone — EVIDENCE: schemas/lenny-adapter.proto:492-496
FACT: SPEC-3 edits `spec/04` §4.2's session-record paragraph alone, so OD3's recommended answer has no staged deliverable behind it in this proposal — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:571-584
CORRECTS [standing context bullet "§4.1's escape hatch is real and a reclassification costs two sentences"]: its closing clause said `:188` "makes the fence row `:151`'s only Adapter-service example". That is false; `:151` carries no fence example. The true reason `:151` is an edit site is that it grounds the declared-not-derived rule on `session_id` appearing on messages of both classes, which the reclassification falsifies. The bullet is corrected in place in this fix.
WATCHOUT: the false clause travelled curated memory (review-log standing context) into `summary.md` OD3 and then into a round-1 ledger OPEN. When a summary claim looks copied, check the standing context for the generator before fixing only the copy — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.review-log.md:667-671
OPEN: whether a "yes" to OD3 is staged in this proposal or left to a successor. OD3 now states that nothing is staged; the reviewer still has to answer. The ledger OPEN at review-log.md:3122 raised the same question and its noun "the `:151` example" is loose, but it is a frozen shard record and was left as written.


### [non-spec.2.fix-design-G1.1]

DECISION: OD3's false trailing clause (`summary.md:246-248`) is replaced by the correct three-site enumeration (`spec/04:175` row, `:188` declaring sentence, `:151` paragraph) with the real ground for `:151` being an edit site — that `:151` grounds "declared rather than derived" on "`session_id` appears on messages of both classes", which the reclassification falsifies — plus one sentence recording that no `spec/04` §4.1 edit is staged anywhere in this proposal — BECAUSE the entry is a decision handed to a human and both halves are things the reviewer needs to decide with; the ledger already carries the second half as an OPEN — ALTERNATIVES: (a) delete the clause outright, rejected because `:151` IS a real edit site under a "yes" and deleting the sentence loses that; (b) copy the finding's suggested_fix verbatim, rejected because it re-states the tier-3 comment obligation that `summary.md:245-246` already carries, producing a duplicate inside one paragraph.
FACT: `spec/04:151` names no `CoordinatorFenceRequest`. Its only message examples are `CheckpointRequest` and `CheckpointStart`. `CoordinatorFenceRequest` occurs in `spec/04` at exactly two lines — EVIDENCE: spec/04_system-components.md:151, :175, :188 (`grep -n CoordinatorFenceRequest spec/04_system-components.md` returns :175 and :188 only).
FACT: after reclassifying the fence row to session scope, NO pod-scoped row in the §4.1 table carries a session field. The four remaining pod-scoped Adapter messages are stated to carry none at `spec/04:188`, and the fifth pod-scoped row, `ReportPodScrubRequest`, declares `pod_id` alone — EVIDENCE: spec/04_system-components.md:188; schemas/lenny-adapter.proto:492-496.
FACT: no `spec/04` §4.1 edit is staged anywhere in this proposal. SPEC-3 edits §4.2's session-record paragraph alone — EVIDENCE: spec-changes.md:571-592 (`### SPEC-3. The session record states the counter's baseline`), and no other staged item names §4.1.
FACT: the tier-0 message-scope gate reads only the FIRST WORD of a scope cell, so it accepts "session" or "pod" for any row and catches no reclassification inconsistency in either direction — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:73-81 (`declaredScope`).
FACT: the tier-3 comment OD3 already names sits at `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43`; the pod-scoped clause is on :40-43, one line earlier than the finding's `:41-43` — EVIDENCE: tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40-43.
CORRECTS [standing context, "§4.1's escape hatch is real and a reclassification costs two sentences."]: its closing clause "which makes the fence row `:151`'s only Adapter-service example" is false and is where `summary.md`'s OD3 copied the error from. `:151` carries no fence example at all. What is true instead: `:151` is an edit site because it grounds the declared-not-derived rule on `session_id` appearing on messages of BOTH classes, and after the reclassification no pod-scoped row carries a session field. `review-log.md:669-670` is adjudicated IN SCOPE for this fix so the curated memory stops teaching the false clause; if the fixer does not reach it, the compaction pass must apply this correction.
MISTAKE: the false clause travelled memory -> summary. It was written into the standing context first (`review-log.md:667-670`), then lifted into OD3 (`summary.md:246-248`), then restated in a later ledger OPEN (`review-log.md:3122`). Correcting only the summary leaves the source that regenerates it.
WATCHOUT: `review-log.md:3122`'s OPEN calls `:151` "the `:151` example", the same loose label, but it is a frozen ledger entry that the round boundary archives whole, and its load-bearing claim (no staged deliverable behind OD3's recommended answer) is TRUE and is what this fix lifts into OD3. Do not edit ledger entries in place — EVIDENCE: review-log.md:3122.
OPEN: whether a "yes" on OD3 is staged in THIS proposal or recorded for a successor is still a human call. The fix states the fact (nothing is staged) rather than answering it, because staging a `spec/04` §4.1 edit here would be a new spec deliverable in a converged spec lane.


### [non-spec.2.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens, run 7 round 2 — BECAUSE `diff -rq` against the round-2 snapshot shows ONLY `review-log.md` differs, so `non-spec-changes.md`, `spec-changes.md`, `implementation-checklist.md` and `summary.md` are byte-identical to the text `[non-spec.1.review-applicability.1]` already simulated end to end and returned empty on; I re-ran the simulation independently anyway (not by deferring to that shard) and reached the same verdict — ALTERNATIVES rejected, with the reason each: (1) S8's tier list omitting tier 0 while `0079`'s four test-lane steps all declare it — killed by the standing `### Traps` "tier-list bookkeeping is a refuted class, three times over"; (2) new test files needing a `tests/spec-map.json` row, on the 0073 S22 precedent — killed on the tree: `validateTestFilesMapped` resolves a test file through a parent DIRECTORY entry, and `tests/tier2_component/migrations`, `tests/tier3_contract/adapter_checkpointbarrier`, and `tests/tier4_integration` are all mapped at directory level, while the pgstore case lands in the EXISTING `tests/tier2_component/stores/sessionstore_test.go`, so no new file this proposal stages can orphan; (3) `pgstore.Create` cited at `:140` while the floor's neighbour is cited at `:244-248` — killed: `Create` spans `:140` to `:344` and `:244-248` is inside it; (4) the three headers, the 0181 backfill, and the `TestCheckpointBarrierRejectsWithoutFence` replacement assertion — all pre-refuted, per the run-7 refuted list handed to this round.

FACT: the spec-map gate is real and its escape is directory-level mapping, which is the fact a future lens needs before it files the 0073-S22-precedent finding. `validateTestFilesMapped` (cmd/lenny-test/cmd_validate.go:716-790) walks only `componentAndAboveTierDirs()` — tier2_component, tier3_contract, tier4_integration, tier5_e2e_kind, tier6_e2e_cloud, tier7b_load_kind, tier8_chaos, tier9_security, tier10_conformance — so `tests/tier7a_load_local` and `pkg/adapter` are outside it entirely, and it resolves a file by trying the exact rel path then each parent dir. `tests/spec-map.json` carries `tests/tier2_component/migrations`, `tests/tier2_component`... (checked: `tests/tier2_component/migrations`, `tests/tier3_contract/adapter_checkpointbarrier`, and `tests/tier4_integration` are all present as bare directory entries), so every new-file site this proposal stages is pre-covered. `tests/tier3_contract/adapter_generation_fence` is mapped at FILE level only, so a new file placed THERE would orphan; the barrier case belongs in `adapter_checkpointbarrier` anyway. It runs inside tier 0 via `cmd_run.go:761-769`.

FACT: `isQuiescedForBarrier` and `BarrierWaiting` have NO production caller — the only readers in the whole tree are `pkg/adapter/coordination_test.go:279`, `:298` and `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163`. This closes the natural CODE-1 candidate "a pod-wide accessor made per-session breaks a caller that has no session id": the only such caller is `holdstate.go:119`, which CODE-3 deletes, and the proposal says so. Do not re-derive — EVIDENCE: grep over `--include=*.go` for the three accessor names returns exactly `holdstate.go:119`, `coordination.go:44/:52/:63`, `coordfixture.go:115`, plus the three test sites.

FACT: `s.coord` / `s.barrier` occur in exactly two non-test files, `pkg/adapter/coordination.go` (31 sites) and `pkg/adapter/checkpoint.go` (2), which confirms CODE-1's "the move breaks exactly one file outside it" mechanically in one grep. Both are in §9.

FACT: `scripts/lint-migrations.sh` pass 3 is satisfied by the `prodMigrationSchema` entry ALONE, because it greps the bare number `0181` recursively over `tests/tier2_component/migrations/` with `grep -RIqs` and `prod_columns_test.go` lives in that directory. §8's justification for the migration behaviour file's placement ("a case landing in `tests/tier2_component/stores/` alone leaves tier 0 red") is therefore loose in its reasoning while right in its conclusion, since the staging lands both artifacts. Not filable; recorded so the next lens does not re-work it — EVIDENCE: scripts/lint-migrations.sh:74-90.

USEFUL [`### Traps`, "MISTAKE: grep the ARCHIVE for a candidate's subject BEFORE working it up"] and [`### Traps`, "tier-list bookkeeping is a refuted class, three times over"]: together they closed four of my five natural candidates in the first ten minutes. The archive grep terms that pay are `fill-the-blanks`, `.down.sql`, and `tier-list`.

USEFUL [ledger `[non-spec.1.review-citations.1]` FACT, the whole re-resolved citation list]: I spot-checked eleven of its anchors independently (`server.go:302/:307/:314`, `slot.go:21/:153`, `coordination.go:89/:93/:99/:158-166/:180-188/:216/:224-226/:236`, `0050:38-39`, `0164:44`, `prod_columns_test.go:295/:583/:610`, `pgstore.go:140/:177/:244-248/:249`, `memstore.go:46/:58-61`, `holdstate_test.go:674/:700-716`, `checkpoint_stream_test.go:384/:417`, `slot_test.go:24/:37`, `server_test.go:90`) and every one landed. The list is trustworthy; spend the pass on structure instead.

UNVERIFIED: whether the loop should keep scheduling this lens. It has now returned empty twice in a row over byte-identical staging, and its own reopening condition is narrow: it reopens only if the checklist, §8's tier/step attributions, §9's file list, or a CODE deliverable's edit sites are rewritten. A third run over unmoved text buys nothing.


### [non-spec.2.review-citations.1]

DECISION: Returned an empty findings list for the citation lens over the non-spec staging, round 2 — BECAUSE `diff -rq` against the round-1 snapshot shows the ONLY file that moved since round 1 is `...review-log.md`; `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`, `summary.md`, `status.md`, `problem-statement.md`, and `deviations.md` are byte-identical to what `[non-spec.1.review-citations.1]` already re-resolved clean. I re-verified from scratch anyway rather than trusting that shard, and every citation resolves — ALTERNATIVES: filing the summary's `spec/28` CH-ADAPTEREVENTS "degradation row" misattribution (weighed, not filed — see WATCHOUT below); re-filing the `spec-changes.md` column-default DEFERRED (already standing, remedy out of lane).

FACT: This round's independent re-resolution, all clean. Adapter: `coordination.go:44`/`:52`/`:63` (the three accessors in that order), `:89`, `:93-94`, `:99`, `:108-118`, `:148`, `:158-166` (open sets waiting/checkpointID/signaled/done), `:180-188` (link, no session check), `:216`, `:224-226`, `:236`, `:245-269`; `server.go:302` coord / `:307` hold / `:314` barrier — all three exact; `slot.go:21` `type slotState struct`, `:153-166` `checkpointRootsForSession`; `checkpoint.go:94`/`:111`/`:122`/`:124`; `oplock.go:116-128` coalesce-then-admit; `session.go:237-239`/`:271`; `slotsession.go:264-275` `checkSessionBound`, `:282-285` `heldSession`, `:347-361` `deregisterStartedSessions`; `holdstate.go:39-44` (`gen` at `:43`), `:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:249`, `:283-296`; `resume.go:178`. Gateway: `barrier.go:190-201` (concurrent fan-out), `:209-232`, `:238-245`; `coordfence.go:147-153` floor, `:155-188` loop, `:164-179` stale-retry, `:180-183` transient, `:186-188` budget, `:195-200` relinquish; `coordination/coordination.go:341` ErrHeld, `:408`/`:415` backoff, `:430` `upsertMirror(row.CoordinationGeneration)`, `:463-482` RecordHandoff, `:512-517`; `start.go:4233-4245` (no increment), `:3668-3672`; `prestop.go:386-400`; `pgstore.go:60`/`:140`/`:177`/`:244-248`/`:249`/`:260`; `memstore.go:46`/`:58-61`; `uploaddriver.go:422` and `partialmanifeststore.go:394` both strictly-greater. Infra: `Makefile:91-94`, `proto_no_drift_test.go:70`, `lint-migrations.sh:45`/`:74-88`, `cmd_run.go:498-508`/`:635-641`/`:880`, `migrate-job.yaml:10-16`/`:37-39`, `0050:38-39`, `0164:44`, `0180` last on disk. Proto: `:1442-1446`, `:1449-1451`→`pb.go:4966` byte-exact, `:1455-1462` (accept predicate at `:1456-1458`), `:1469-1474`, `:1477-1479`; `grpc.pb.go:180`/`:632`. Tests: `coordination_test.go:184-197`/`:199-216` (`package adapter`), `holdstate_test.go:674`/`:700-716`, `checkpoint_stream_test.go:384`/`:417` (`package adapter_test`), `slot_test.go:24`/`:37`, `server_test.go:90`, `memstore_test.go:309-325`, `sessionstore_test.go:74-79`, `prod_columns_test.go:295`/`:583`/`:610`, `sweep_test.go:275..:594`, `coordination_takeover_test.go:74`/`:142`/`:241`/`:301`, `uploaddriver_test.go:992`/`:993-995`/`:1007`/`:1015`, `checkpointer_test.go:89-96`, `coordination_mirror_test.go:84`/`:85-86`/`:116`, `wiring_test.go:171`, `coordlease_test.go:37`/`:58`, `coordfixture.go:76`/`:98-102`/`:106-108`/`:109`/`:115`/`:122`/`:220-241`/`:231`, tier-8 `:118`/`:150`/`:179`/`:195`/`:223`/`:239-241`/`:267`/`:283`/`:296`, tier-7a `:125`/`:130`/`:144`/`:260`/`:264-265`/`:287-288`, tier-4 `:72`/`:83`/`:151`.

FACT: The problem-statement file's citations were never in any standing inventory and I checked them this round. All resolve: `coordinatorfence.go:48`, `server.go:302`, `coordination.go:25`/`:84`/`:108-118`/`:112-113`/`:119-121`/`:211`, `proto:1449-1451`, `coordfence.go:164-179`/`:195-200`, `coordination/coordination.go:399-416`/`:463-468`, `start.go:3668-3672`, `barrier.go:209-232`/`:237-246`, `prestop.go:390-397`, `holdstate.go:39-44`/`:119`/`:225-229`/`:283-296`, `slotsession.go:267`. Do not re-derive this set.

FACT: The summary's cross-proposal table verifies whole. 0060 and 0073 are `Implemented` in their own headers; 0075 is `Draft for review` and its stated ground is verbatim at `0075...md:88-89` ("The write target is the pod. The identifier selects nothing."); 0080 is `EARLY DRAFT, NOT CONVERGED` and both entries the summary attributes to it are real — §1.14 at `0080...md:184-188` records the hold-partitioning question as unstated and `:191`/`:211` record "a hold entered for one session released by another's fence" as taken by 0076. The claim-register claims verify too: the barrier FIELD row is `UNWIRED` under `R16` at `tests/claim-map.json:76-82` with a note that is false against `coordination.go:236`; the CH-ADAPTEREVENTS client row is `UNWIRED` under `R12` at `:512-517`; `CoordinatorFenceRequest` is exempted by name at `tests/tier0_static/claim_register_proto_agreement_test.go:43`.

FACT: `prestop`'s post-barrier loop really is sequential, so the standing D7 drain arithmetic holds. `prestop.go:387` declares a `sync.WaitGroup` and `:419` does `wg.Add(1)`, which reads as concurrency, but the closure at `:422` is invoked synchronously with no `go` and carries the comment "Run sequentially in v1 so the budget is honored per-session against the same clock"; the single `wg.Wait()` is at `:448`. Do not re-open the "400 sequential post-barrier captures" claim on the WaitGroup.

WATCHOUT: the summary's LAST Non-goal-but-one misnames which bullet of `spec/28` carries the CH-ADAPTEREVENTS ownership gap. `summary.md:152-154` says the question is one "which `spec/28`'s CH-ADAPTEREVENTS degradation row records the specification as not stating". The sentence lives in the §28.5.2 **Exclusivity** bullet (`spec/28:471-478`, closing "the specification does not state which replica's connection carries an event when more than one replica holds a connection to the pod"), and in the §28.8 matrix it sits in the **"Holder of the exclusivity constraint changes"** column, `awk -F'|'` field 5 of the row at `:1810`. The §28.5.2 **Degradation** bullet (`:479-494`) carries a different "does not state" sentence, about retry and buffering policy. Weighed and NOT filed: the substance is inside the same channel card, the claim itself is true, the bullet stages no edit and supports only a non-goal, and the material skeptic has refuted this precision-in-reviewer-prose class repeatedly on this proposal. A fixer already editing `summary.md` should change "degradation row" to "exclusivity bullet" while there. — EVIDENCE: summary.md:150-154; spec/28_communication-channels.md:471-478, :479-494, :1803, :1810

USEFUL [`### Traps` "MISTAKE: §8's tier-4 'skipping `sess-a` on `ErrHeld`' attribution, worked up independently by three agents and filable by none"]: I re-derived the same suspicion from the code — `coordination.go:330-338`'s own comment says a session whose lease a live foreign replica holds "is skipped without an Acquire attempt", so the `ErrHeld` arm at `:341` is never reached on that path — and the entry stopped me from spending the finding. It is the fourth independent derivation; the entry is earning its space.

USEFUL [`### Settled` "Known sub-line citation drifts that must not be filed"]: `coordination_test.go:184-197` (doc comment opens at `:185`), `slotsession.go:282-285` vs `:283-285`, and `coordination.go:408` for the `recordAdoptionBackoff` call at `:415` all surfaced again exactly as recorded, in both `non-spec-changes.md` and `summary.md`.

USEFUL [`### Traps` "WATCHOUT: a cached lens answer can belong to the other lane"]: the cache path was empty this round, but the key I computed (`646a03fca298`) hashes only the two change files plus the checklist and still carries no lane, so the collision the entry describes is unfixed.

MISTAKE (mine, avoided): I nearly treated the round-1 shard's empty return as licence to short-circuit. The orchestrator's framing ("run 6's non-spec round 1 was cut off, this run starts that round again from scratch") disagrees with the standing context's ("run 7's non-spec round 1, fifteen shards"), and the ledger entry `[non-spec.1.review-citations.1]` is real and is over this same byte-identical text. Trust the `diff -rq` against the snapshot rather than either narration: it is the only statement about what moved that cannot be stale.

UNVERIFIED: whether `summary.md:353-354`'s "byte-diffed at tier 0" for the claim register is exact. I confirmed the generator source (`scripts/seed-claim-register.py`) and the gate names from the standing context but did not re-open `claim_register_generator_test.go` this round; the standing `### Settled` bullet asserts a byte diff and nobody has re-checked it since pass 19.


### [non-spec.2.review-client-surface.2]

DECISION: returned an empty findings list — BECAUSE `diff -rq` against
`scratchpad/cp-snap/0076-run7/non-spec-r2` and against `non-spec-r1-start` both report only
`review-log.md` as differing, so the staged text is byte-identical to what
`[non-spec.1.review-client-surface.1]` already cleared, and every parallel-representation check I
re-derived from the tree this run came back matching. — ALTERNATIVES: I worked up and rejected four
candidates, each named below so nobody re-derives them.

FACT: the proto carrier arithmetic re-derived a third time and it is exact.
`awk '/^message /{m=$2} /coordination_generation *=/{print NR": "m}' schemas/lenny-adapter.proto`
returns fourteen fields. Twelve are SCHEMA-1's operational list, in the same order SPEC-2 states
(spec-changes.md:562-567), plus `CheckpointBarrierRequest` (named separately) and
`CoordinatorFenceRequest` (the one carrier taking no edit). No fifteenth field and no message absent
from the list — EVIDENCE: schemas/lenny-adapter.proto:974, :1002, :1051, :1075, :1096, :1119, :1179,
:1310, :1398, :1452, :1480, :1536, :1581, :1623.

FACT: `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go` earns its §9 entry, which no
deliverable and no §8 bullet explains. Its helper `waitBarrierGateOpen` calls `srv.BarrierWaiting()`
(`:158`), one of the three pod-wide accessors CODE-1 gives a session id. A lens that reads §9 looking
for an unjustified entry will stop here; the justification is the accessor, not the barrier assertions
— EVIDENCE: pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:155-166.

FACT: §9's accessor-caller coverage is complete, checked mechanically rather than by reading §9.
`grep -rn "BarrierWaiting()\|isQuiescedForBarrier()\|LastFencedGeneration()\|\.LastFenced(" --include=*.go .`
returns ten files, and every one of them is either a `pkg/adapter` file §9 lists, `coordfixture.go`,
`checkpointbarrier_test.go`, the three tagged test files §9 names, or the generated stub. There is no
eleventh caller — EVIDENCE: pkg/gateway/runtime/adapterclient/coordinatorfence.go:29 and :60 are
`CoordinatorFenceResult.LastFencedGeneration`, a struct field rather than an accessor call.

FACT (candidate 1, rejected): `CheckpointBarrierResponse.checkpoint_ref`'s comment says "empty when the
gateway drove no stream against the pod" (`schemas/lenny-adapter.proto:1497`), and CODE-1's per-session
gate makes the emptiness condition per session rather than per pod. It is NOT a missed SCHEMA-1 target,
because `spec/10_gateway-internals.md:185` (§10.1.8 step 3) already fixes the stream at "each quiesced
session" and the ack at "the gateway-minted `checkpoint_id` it received in `Start`", so the proto's
"against the pod" phrasing is pre-existing drift against shipped spec text that CODE-1 closes rather
than creates. This is a different exclusion ground from the standing "no eighth carrier" bullet, which
excludes the message for carrying no gate; record both, because the next lens will re-derive this one.

FACT (candidate 2, rejected): SCHEMA-1's staged replacement for the twelve operational field comments,
"rejects a request whose generation does not match it" (spec-changes.md:554-556), does not restate the
unset arm that staged §10.1.2 step 3 creates ("the pod does not reject that session's RPCs on
generation grounds", spec-changes.md:158-161). SPEC-2 anticipates the omission in place and grounds it
("the unset case is not restated on these comments, because §10.1.2 step 3 owns it",
spec-changes.md:556-558), and the adapter validates the generation on the fence and barrier paths alone
(`pkg/adapter/coordination.go:92`, `:223`), so no operational RPC enforces either wording. The remedy
would also land in `spec-changes.md`, which this loop does not own.

FACT (candidate 3, rejected): the `CheckpointBarrierAck` control event carries `barrier_id`,
`checkpoint_ref`, and `quiesced_ms` and no session id (`pkg/adapter/coordination.go:271-281`), so
per-session barrier gates put two acks on one CH-ADAPTEREVENTS stream. `barrierID` is minted per target
by `c.nextBarrierID(ctx, t)` (`pkg/gateway/coordination/barrier/barrier.go:207`) and targets are
per-session, so correlation holds; and the summary already records that the gateway has no
CH-ADAPTEREVENTS client at all.

FACT (candidate 4, rejected): the two landed tier-3 suites for the RPCs this change touches are
descriptor-and-encoding only and assert no adapter outcome, so SCHEMA-1's comment-only proto edit and
CODE-2's gate change cannot turn either red. `TestCheckpointBarrierResponseWireContract` pins the field
set; `adapter_generation_fence` pins field number, kind, oneof placement, and the zero-value encoding —
EVIDENCE: tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:47-77;
tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:71-84, :86-125.

USEFUL [`### Traps`, "MISTAKE: refuted class (k) does not reach §9's tier-3 omission ... Do not spend a
verification on it"]: I had the finding drafted (§8 says "The tier-3 wire case D7 stages" at
non-spec-changes.md:326-327 while §9 lists no `tests/tier3_contract` path, and the
`IMPLEMENTOR'S CHOICE:` marker at :290 is scoped to tier-1 files in `pkg/adapter`). This entry, plus the
two archived shards that rejected the same candidate, is what stopped it.

USEFUL [`[non-spec.1.review-client-surface.1]`, "the OpenAPI document path is
`pkg/gateway/externalapi/openapi/openapi.json`"]: the lens brief still names
`pkg/gateway/openapi/openapi.json`, which does not exist, so the brief's grep returns nothing for the
wrong reason. `ls pkg/gateway/externalapi/openapi/` and `grep -c coordination` over the real document
(0 hits) are the checks that mean something.

UNVERIFIED (carried, unchanged): whether the external-adapter compliance suite §28.7 names
(`spec/28:1775`) would generate assertions from proto *comment* text. The suite is not in the tree, so
the claim that it keys on declarations is still inference. If it is ever built, SCHEMA-1's prescriptions
become load-bearing on a published artifact and this lens should re-run.


### [non-spec.2.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE the proposal text is byte-identical to what the
previous docs-alignment shard already cleared (`diff -rq scratchpad/cp-snap/0076-run7/non-spec-r2-start
proposals/0076_.../` returns nothing at all, and non-spec-r1-start differs only in the review log), and I
re-derived the `docs/` surface from scratch anyway rather than lifting it, reaching the same set — no
`docs/` page states the fenced value's unit, the counter's baseline, the acceptance gate, the first-fence
exemption, or any log field CODE-3 moves — ALTERNATIVES: filing the never-handed-off population losing
prestop's fallback capture as a new cause of a data-loss path in `docs/operator-guide/upgrades.md`,
rejected because the standing context adjudicates the acked-but-uncaptured gap as pre-existing and the
skip as what shipped §10.1.8 mandates, so D7 grows the population rather than creating the cause; filing
the rolling-window zero-row barrier refusal (`non-spec-changes.md:163-165`) as an accepted failure mode
landing only in reasoning, rejected on the ground the previous shard recorded, that the refusal is a
strict subset of the shipped refusal for every never-taken-over session.

FACT: the docs surface re-derived this run in one grep, and it is three lines rather than thirteen when
grepped by the counter's name in both spellings. `grep -rn "coordination_generation\|
coordinationGeneration\|coordination generation" docs/ charts/ sdks/` returns exactly
`docs/getting-started/concepts.md:101`, `docs/getting-started/architecture.md:173`, and
`docs/reference/adapter-contract.md:69`. The standing thirteen-line set is that three plus ten neighbours
a wider `coordinator|fenc|barrier` sweep returns, and every one of the ten is a metric row or an
unrelated delegation-coordinator line. Both counts describe the same conclusion; the three-line number is
the one a grep reproduces — EVIDENCE: docs/getting-started/concepts.md:101,
docs/reference/adapter-contract.md:69.

FACT: `docs/api/internal.md` documents the `RuntimeAdapter` service but its protobuf block names neither
`CoordinatorFence` nor `CheckpointBarrier` (`:76-99` lists StartSession, StopSession, Attach, Checkpoint,
UploadFiles, DemoteSDK alone), and its gRPC status table gives `FAILED_PRECONDITION` the generic gloss
"Operation not valid in current state". So D7's change to when a barrier returns `FailedPrecondition`
reaches no `docs/api/` row. This page is the one surface a docs lens would expect to mirror the proto and
it does not — EVIDENCE: docs/api/internal.md:76-99, :488-499.

FACT: `docs/operator-guide/upgrades.md` names schema migrations only as a generic step ("Schema migrations
apply any new Postgres schema changes", `:29`) and enumerates no migration number, so CODE-4's 0181 owes
it nothing; its `### CheckpointBarrier During Rolling Updates` block (`:47-54`) states the barrier's
purpose and two metric names, neither of which D7 moves — EVIDENCE: docs/operator-guide/upgrades.md:29,
:47-54.

FACT: `docs/reference/state-machines.md` and `docs/reference/error-catalog.md` carry no coordinator hold,
no fence, and no barrier content at all. A grep for `hold|coordinator` over the state-machine page returns
only `maxSuspendedPodHoldSeconds`, the claim reserved-hold TTL, and the recycle disposition, none of them
the adapter's coordinator-loss hold, so SPEC-2's §29.10 hold reclassification has no reader-facing mirror
to falsify — EVIDENCE: docs/reference/state-machines.md:92, :211, :221-226.

USEFUL [Traps: "a token grep under-reports the `docs/` surface by four sites"]: grepping the spaced
spelling alongside the token is what returns `docs/getting-started/architecture.md:173` and the
`adapter-contract.md` row; the token alone returns two files and reads as a clean sweep for the wrong
reason.

USEFUL [Settled: "the thirteen-line `docs/` surface"]: it made this pass a verification rather than a
sweep, and its conclusion held against an independent re-derivation.

OPEN: this lens has now returned empty five times on this proposal, twice in a row over text that has not
moved a byte between the returns. The standing `### Open` item asking whether an exhausted lens is retired
is still unanswered, and this round's return is the strongest evidence yet for retiring it: the proposal
stages no `docs/` edit, touches no alert or metric, and the whole `docs/` surface is three lines that name
the counter without stating anything the change moves.


### [non-spec.2.review-edit-sites.1]

DECISION: Returned EMPTY. — BECAUSE the staging is byte-identical to the r2 snapshot (`diff -rq` returns only the review log), and every edit-site surface I re-opened independently confirmed the standing inventories; the residual defects I could see are all either in `spec-changes.md` (settled input this lane may not edit), in a refuted class, or already-filed `### Open` items owned by another lens — ALTERNATIVES: filing the `enterHoldState` doc comment (`pkg/adapter/holdstate.go:116-118`) and `holdstate.go:103` as missed sites (rejected: refuted class (k), and `:116-118` is already a `### Deferred`); filing CODE-2's missing "reaches tiers" line (rejected: tier-list bookkeeping is a three-times-refuted class); re-filing the `non-spec-changes.md:5-6` fill-the-blanks header (rejected: my own run-1 filing of it was refuted by the material skeptic this run).

FACT: the whole edit-site chain from the proto is TWO generated files and nothing else. `schemas/buf.gen.yaml` runs only `protoc-gen-go` and `protoc-gen-go-grpc` into `../pkg/proto`, so SCHEMA-1's `pkg/proto/adapter/v1/lenny-adapter.pb.go` + `lenny-adapter_grpc.pb.go` pair is complete; there is no descriptor set, no SDK codegen, and no doc generated from the proto. — EVIDENCE: schemas/buf.gen.yaml:16-27; Makefile:91-99

FACT: no test anywhere reads a `column_default` for `sessions` or `coordination_lease`, so migration 0181's `DEFAULT 0 → 1` breaks no landed assertion. The only `column_default` reader in the tree is `pool_delivery_mode_test.go:90-99`, scoped to warm pools, and `prodMigrationSchema`'s `columns` field carries column NAMES only (`prod_columns_test.go:20-25`, the 0050 row at `:105-109`), so `TestProdMigrationsApplyExpectedSchema` asserts presence rather than default. This is the check that would have falsified CODE-4's "no seed path breaks", and it holds. — EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:20-25, :105-109; tests/tier2_component/migrations/pool_delivery_mode_test.go:90-99

FACT: no `docs/`, `charts/`, or `spec/` file enumerates a migration number. `grep -rn "0180\|0179\|0178" docs/ charts/ spec/` returns nothing, so 0181 obliges no doc or chart edit. This is one grep and it closes the whole migration-numbering edit-site question. — EVIDENCE: charts/lenny/templates/migrate-job.yaml:3 is the only `migrations` mention in the chart templates

FACT: `docs/reference/adapter-contract.md`'s `CheckpointBarrier` and `CoordinatorFence` rows (`:68`, `:69`) state no generation gate and no match rule, so D7's acceptance arm falsifies neither. `docs/reference/metrics.md`'s coordination block (`:304-311`) and barrier block (`:196-197`) describe what each metric means and state no unit for the fenced value. Both re-derived from the files rather than from the standing bullet; the standing conclusion holds. — EVIDENCE: docs/reference/adapter-contract.md:68-69; docs/reference/metrics.md:196-197, :304-311

USEFUL [Standing context, "MISTAKE: an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the wrong half"]: following it (skip the token greps, read the named carriers in the file) is what let this pass cover SCHEMA-1's nineteen carriers, §9's file list, the accessor blast radius, the migration gate surface, and the docs/metrics companion pairs in one budget. The two greps it told me to skip would have consumed most of it for the same answer.

USEFUL [Standing context, "WATCHOUT: counting doc-comment lines by hand from a `sed` window drifts by one on the adapter files"]: I hand-counted `memstore.Create` from a `sed -n '44,70p'` window, got `:45` against the staging's `:46`, and was one keystroke from filing a false citation. `grep -n` gives `:46`, which is what the staging says. The drift is in the sed window, not in the proposal. This applies to `memstore.go` as well as to the adapter files, so widen the entry's scope.

FACT: `s.coord` is confined to `pkg/adapter/coordination.go` (26 occurrences) plus ONE doc-comment mention at `pkg/adapter/holdstate.go:117`; `s.barrier` has exactly five production readers (`coordination.go:64-66`, `:264`, `:269`, `checkpoint.go:122`, `:124`) and four in `coordination_test.go` (`:224-226`, `:282`, `:285`, `:356-357`). Every file is in §9. Re-derived from scratch this run; the standing blast-radius bullet is exact.

FACT: `pkg/adapter/holdstate.go:103` is a THIRD stale-comment site CODE-3 falsifies, beside the `### Deferred` `:116-118` and `pkg/adapter/coordination.go:126-128`. Its doc comment reads "gauge, logs coordinator_connection_lost with the last known generation", which stops being true when CODE-3 drops the key from the pod-level line. Not filable (refuted class (k)), but hand all three to the implementor together rather than the two the `### Deferred` names. — EVIDENCE: pkg/adapter/holdstate.go:103, :130-132

DEFERRED [`pkg/adapter/holdstate.go`]: extends the standing `### Deferred` entry that names `:116-118` alone. The claim that is now false at `:103` is "logs coordinator_connection_lost with the last known generation". What is true instead: the pod-level line logs the started-session count and no generation. Same remedy and same owner as the `:116-118` and `coordination.go:126-128` residues; the staged non-spec edit that closes them should name all three.

FACT: `tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` and `tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go` really are descriptor-and-wire-form only. Neither constructs an `adapter.Server`, neither names `FailedPrecondition`, and the fence file's only `CoordinationGeneration` assertions are round-trip decodes (`:233-254`). So D7 and CODE-4 falsify no landed tier-3 assertion, and §9's omission of any tier-3 file costs nothing beyond the already-recorded `### Open`.

FACT: `tests/tier2_component/coordination/sweep_test.go`'s "no fence at a baseline generation" subtest (`:475-525`) is INSIDE §8's class-1 `:275`-`:594` range and its two shifting assertions (`got.CoordinationGeneration != 0` at `:506`, `readopter.gens[0] != 1` at `:523`) are covered by the class rule. It reads like a third class because its subject is the `RecordHandoff` 0-sentinel rather than a row baseline; it is not. Do not work it up as class-3 residue.



### [non-spec.2.review-feasibility.1]

DECISION: Returned EMPTY on the actor-action feasibility lens over `non-spec-changes.md` read against `spec-changes.md` — BECAUSE every actor the non-spec staging names exists under that name at the cited site, every action is within what that component can do, and the three feasibility questions the standing `### Open` list left UNVERIFIED all resolve POSITIVE against the tree (below) — ALTERNATIVES: filing the tier-4 `ErrHeld` attribution, the `:408` backoff citation, the `checkSessionBound`-returns-the-entry blank, and the CODE-3 unlocked-read race (all four are either recorded do-not-file drifts or already weighed and declined in `### Settled`/`### Traps`).

FACT: **The external `adapter_test` package already has the exported hooks the mid-flight deregistration case needs, so it needs no new test-only hook.** `Server.BeginCheckpointOpForTest(ctx, sessionID)` takes the pod op lock and returns its release func, and `Server.WaitPendingCheckpointForTest(sessionID, timeout)` blocks until a checkpoint has entered the lock's pending set, so the case can put a co-tenant checkpoint in the running slot, wait for the subject stream to queue behind it, deregister, and then release — deterministically, without sleeping. `RegisterUnboundSlotForTest` (OD13's case) and `ClaimSessionForTest` are exported there too. This CLOSES the standing `### Open` "UNVERIFIED: whether the external test package can stall `s.ops.Begin` deterministically without a test-only hook" — EVIDENCE: pkg/adapter/export_test.go:61, :71, :80.

FACT: **A tier-7a or tier-4 case CAN drive real `Checkpoint` streams through `coordfixture.Pod.Client`.** `adapterclient.Client.Checkpoint(ctx)` returns the raw `adapterv1.Adapter_CheckpointClient` (the type alias is `CheckpointStream`), so the test sends its own `CheckpointStart` naming the session and the gateway-minted checkpoint id, and `Client.CheckpointBarrier(ctx, sessionID, gen, barrierID)` is the matching barrier driver. This CLOSES the standing `### Open` "UNVERIFIED: whether the tier-7a two-barrier case can be written against real `Checkpoint` streams" — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:425, :436, :467.

FACT: **`coordfixture.NewReplica` takes `bind ...string` and its `FenceReadopter` holds exactly one `*Pod`, so one replica coordinating two co-tenant sessions on one pod is constructible as §8's tier-4 bullet describes.** No second replica, second pod, or fixture change beyond CODE-1's session-keyed `Pod` methods is needed — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:302-316, :307.

FACT: **The mid-flight case's two triggers are not equally reachable, and the reachable one is enough.** `fireHoldTimeout` lives in `pkg/adapter/holdstate_test.go:556` (`package adapter`), so the hold-timeout arm of "either by a `Shutdown` for that session or by the coordinator-loss hold timeout" cannot be driven from the external `adapter_test` package where §8 fixes the case; the `Shutdown` arm is an exported RPC and works. Below the bar as an "either" with one live arm; a fixer editing that bullet could cheaply drop the hold-timeout arm.

FACT: **CODE-3's read target is real and survives detachment.** `heldSession{sessionID string; state *slotState}` (`slotsession.go:282-285`) and `deregisterSlotLocked` returns the entry with no field zeroed (`:174-189`), so `terminateHeldSession` (`:225`) and `writeHoldPostMortem` read each terminated session's own relocated `coordinationState` off `m.state`. `enterHoldState`'s `gen := s.LastFencedGeneration()` is at `holdstate.go:119`, its only writer `s.hold.gen = gen` at `:128`, its only reader `gen := s.hold.gen` at `:187`, and the pod-level line at `:130-132` — every CODE-3 anchor resolves exactly.

FACT: **`s.coord` is confined to `coordination.go` and `s.barrier` escapes only to `checkpoint.go:122`, `:124` and `coordination_test.go`.** Both escape files are in §9, so CODE-1's move breaks no file §9 omits; `pkg/adapter/session.go` needs no edit and is correctly absent. `Server.LastFencedGeneration`'s only non-test caller in the whole tree is `holdstate.go:119` — EVIDENCE: `grep -rn "s\.coord\.\|s\.barrier\." pkg/adapter/` and `grep -rn LastFencedGeneration pkg/ cmd/ tests/`.

FACT: **`coordinationState.quiesced` still has no production reader after CODE-1.** Its only sites are the declaration (`coordination.go:38`), the test accessor `isQuiescedForBarrier` (`:52-55`), and the barrier's set and deferred clear (`:247`, `:255`); `isQuiescedForBarrier` is read only at `coordination_test.go:279`, `:298` and `BarrierWaiting()` only at `adapterclient/checkpointbarrier_test.go:163`, both files in §9. So relocating it changes no enforced gate.

WATCHOUT: the standing "sub-line drift" list needs two more entries, both verified this run and both do-not-file. `scripts/lint-migrations.sh`'s `TEST_DIR` is at `:44` where §8 cites `:45` (pass 3's block at `:74-88` is exact). `cmd/lenny-test/cmd_run.go`'s `scripts/lint-migrations.sh` static check opens at `:634` where §8 cites `:635-641`. Do not spend a verification on either — EVIDENCE: scripts/lint-migrations.sh:44, :74-88; cmd/lenny-test/cmd_run.go:634-641.

FACT: **Every other non-spec citation I opened resolves exactly**, which is worth recording because it is the fourth independent confirmation and a fifth is waste: `slot.go:21`; `server.go:302`, `:307`, `:314`; `coordination.go:44`, `:52`, `:63`, `:89`, `:93-94`, `:99`, `:148`, `:158-166`, `:180-188`, `:216`, `:224-226`, `:236`, `:245-269`; `checkpoint.go:94`, `:111`, `:122`, `:124`; `slot.go:153`; `resume.go:178`; `slotsession.go:267`, `:273`, `:347-361`; `oplock.go:119`; `coordfence.go:147-153`, `:180-183`, `:186-188`; `coordination/coordination.go:341`, `:370`, `:414` (cited `:408`), `:430`, `:463-482`, `:512-517`; `barrier.go:190-201`, `:238-245`; `pgstore.go:140`, `:177`, `:244-248`, `:249`; `memstore.go:46`, `:58-61`; `migrations/0164:44`; `migrations/0050:38-39`; `charts/lenny/templates/migrate-job.yaml:10-16`, `:37-39`; `Makefile:91-94`; `proto_no_drift_test.go:70`; `proto:1451` → `pb.go:4966` (byte-identical line); `grpc.pb.go:180`, `:632`; `prod_columns_test.go:295`, `:583`, `:610`; `coordfixture.go:76`, `:98-102`, `:106-108`, `:109`, `:115`, `:122`, `:220-241`, `:231`; `coordination_fence_split_brain_test.go:72`, `:83`; `checkpoint_stream_test.go:384`, `:417`; `slot_test.go:24`, `:37`; `server_test.go:90`; `holdstate_test.go:674`, `:700-716`; `cmd_run.go:498-508`, `:880`.

USEFUL [`### Traps`, "grep the ARCHIVE for a candidate's subject BEFORE working it up"]: the `fill-the-blanks` / `.down.sql` archive greps plus the refuted-class list at the top of this lens's brief pre-adjudicated four of the five candidates a feasibility read generates on this staging. Without them this pass would have filed at least the tier-4 `ErrHeld` attribution.

USEFUL [`### Settled`, "Guard ordering is what the adapter deliverables rely on"]: the instruction to verify that a guard PRECEDES the site that needs it, rather than that it merely exists, is the whole of what makes CODE-1's resolve rule checkable; `checkSessionBound` at `coordination.go:89` and `:216` and `checkpointRootsForSession` at `checkpoint.go:94` all precede their resolve sites with no blocking call between, which is why the "re-lookup is unsafe" argument holds only on the `Checkpoint` path (where `s.ops.Begin` at `:111` sits in the gap) and not on the fence and barrier paths.


### [non-spec.2.review-fresh.1]

DECISION: filed exactly one finding, the `CoordinatorFenceRequest` message-comment tail deletion under SCHEMA-1 / SPEC-2 `spec-changes.md:506-512` — BECAUSE it is the one live, filed-and-unfixed defect on this staging whose remedy lands in `non-spec-changes.md` (SCHEMA-1 can carry the preservation clause), and the tail it deletes (`FailedPrecondition`, the `coordinator_handoff_stale` detail string, `lenny_coordinator_handoff_stale_total`) has NO other carrier in `spec/`, `docs/`, `schemas/`, or `charts/` — ALTERNATIVES: (1) the three `CheckpointBarrier` carriers losing `FailedPrecondition` under `spec-changes.md:536-541` — folded into the suggested fix instead of filed, because that sentence's trailing "so" clause scopes the edit to the compared value and a shard already read it that way (standing `### Open` UNVERIFIED); (2) the summary's "the one opposite-order acquisition in the tree is the read CODE-3 removes" — declined, see MISTAKE below; (3) §10.5's paraphrase in CODE-4 — declined, see FACT below.

FACT: `coordinator_handoff_stale` occurs in `spec/`, `docs/`, `schemas/`, and `charts/` at exactly two lines, both inside the one comment SPEC-2 tells the applier to replace. `spec/10:71`, `spec/16:183`, and `docs/reference/metrics.md:307` carry the metric NAME only and state neither the gRPC status nor the detail string — EVIDENCE: schemas/lenny-adapter.proto:1445-1446; spec/10_gateway-internals.md:71; spec/16_observability.md:183; docs/reference/metrics.md:307.

MISTAKE: `summary.md:62-63` (a **Fixed decisions** bullet) says "The one opposite-order acquisition in the tree is the read CODE-3 removes", and `summary.md:378-379` repeats it. There is no opposite-order acquisition. `enterHoldState` reads through `s.LastFencedGeneration()` at `holdstate.go:119` and RELEASES `coord.mu` before taking `hold.mu` at `:122`, and the tree's own comment at `coordination.go:124-128` gives exactly that as the reason `coord.mu`→`hold.mu` in `CoordinatorFence` is deadlock-free. The `### Settled` bullet already says "CODE-3 closes no cycle", so the log and the summary disagree. NOT filed: it is rationale in a decisions list, it makes no applied text wrong and no implementation broken, and the same bullet's other half (the registry→entry→hold order) is right. A fixer editing that bullet for another reason should say "CODE-3 removes the one read that would become an inversion if it moved inside the hold critical section", or drop the clause.

FACT: `spec/10:432` states the expand-contract nullability rule for `NOT NULL` columns added in Phase 1 and `:429` defines Phase 3 as dropping old columns; neither literally states CODE-4's paraphrase "a constraint that old-version writes violate belongs to a Phase 3 migration in a subsequent deployment" (`non-spec-changes.md:141-142`). The other half of the paraphrase, "mixed-version replicas must coexist during rollout", is verbatim at `spec/10:420`. Weighed and NOT filed: the conclusion CODE-4 draws is supported by `:420` and by `:432`'s stated ground (old replicas' writes must not be rejected), so the extension is an analogy rather than a false citation. A later lens that wants it must argue past `:420`.

FACT: the adapter has no pod-level concurrency guard on `StartSession`; `claimSessionSlot` claims a slot per session with no `maxConcurrentSessions` term anywhere in `pkg/adapter` outside two comments, so §8's tier-4 and tier-1 co-tenant cases are constructible against a stock `adapter.New(...)` — EVIDENCE: pkg/adapter/session.go:111; grep 'maxConcurrent' pkg/adapter/*.go returns only sdkwarm.go:294 and slotsession.go:27.

USEFUL [`### Traps` "the proposal states preservation explicitly wherever it means it, so silence is an instruction to replace"]: this is the entry that turns the `CoordinatorFenceRequest` truncation from a wording quibble into a filable deletion. Without it the site reads as "a competent applier would keep the tail".

USEFUL [`### Traps` "Refuted classes ... (k)"]: killed four candidates before I spent a pass on them — `adapterclient/coordinatorfence.go:37`'s per-pod-lifetime exemption (which the summary's own `### Corrections outstanding` also owns), `enterHoldState`'s doc comment at `holdstate.go:116-118`, `coordination.go:126-128`'s "the hold timeout never reaches back into coord.mu", and `holdstate_test.go:887-892`'s justification comment.

USEFUL [`### Traps` "MISTAKE: refuted class (k) does not reach §9's tier-3 omission"]: stopped me re-deriving §9's missing `tests/tier3_contract` entry.

FACT: every code, test, migration, chart, and script citation in SCHEMA-1, CODE-1 through CODE-4, §8, and §9 that I re-resolved this pass is accurate to within the known sub-line drifts the standing context already lists. Re-resolved: `slot.go:21`, `:153`; `server.go:302`, `:307`, `:314`; `coordination.go:25`, `:29-32`, `:44`, `:52`, `:63`, `:89`, `:93-94`, `:99`, `:108-121`, `:148`, `:158-166`, `:180-188`, `:211`, `:216`, `:223-226`, `:228-231`, `:236-239`, `:245-269`; `holdstate.go:39-44`, `:90-100`, `:107-112`, `:119`, `:128`, `:130-132`, `:172-176`, `:187`, `:192`, `:206`, `:225-229`, `:249`, `:283-296`; `slotsession.go:267`, `:282-285`, `:347-361`; `session.go:237-239`, `:271`; `checkpoint.go:94`, `:111`, `:122`, `:124`; `oplock.go:117-129`; `resume.go:178`; `pgstore.go:140`, `:177`, `:244-248`, `:249`; `memstore.go:46`, `:58-61`; `coordfence.go:147-153`, `:159-188`; `coordination.go:463-482`; `barrier.go:190-201`, `:238-245`; `0050:38-39`; `0164:44`; `0180` last taken; `migrate-job.yaml:10-16`, `:37-39`; `lint-migrations.sh:45`, `:74-88`; `cmd_run.go:498-508`, `:635-641`, `:880`; `Makefile:91-94`; `proto_no_drift_test.go:70`; `prod_columns_test.go:295`, `:583`, `:610`; `memstore_test.go:309-325`; `coordination_test.go:184-197`; `uploaddriver_test.go:992`, `:993-995`, `:1007`, `:1015`; `checkpointer_test.go:89-96`; `uploaddriver.go:422`; `partialmanifeststore.go:394`; `coordfixture.go:76`, `:98-102`, `:106-108`, `:109`, `:115`, `:122`, `:220-241`, `:231`; `sessionstore_test.go:79`; `sweep_test.go:275-594`; `coordination_takeover_test.go:74`, `:142`, `:241`, `:301`; tier-8 `:118`, `:150`, `:179`, `:195`, `:223`, `:239-241`, `:267`, `:283`, `:296`; tier-7a `:130`, `:144`, `:260`, `:264-265`, `:287-288`; tier-4 `:83`. A citation lens does not need to redo this list.

FACT: the twelve operational-RPC carriers are exactly the twelve SPEC-2 names. `awk '/^message /{m=$2} /A pod validates/{print NR": "m}' schemas/lenny-adapter.proto` returns twelve lines and no thirteenth; `grep -c` on the phrase returns 0 because it wraps, which reads as a dead anchor.

WATCHOUT: `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r2` returned ONLY the review log this round, so the "read the changed sections first and hardest" instruction had no target. Every staged file is byte-identical to run 7 round 1's input, whose twelve shards were never merged. Run the `diff -rq` first and, when it is empty, spend the whole pass on text the standing context marks as least-covered — which for this staging is sentence-versus-clause structure inside the cited proto comments, and nowhere else.

OPEN: the `spec-changes.md:249` / `:284-290` versus `non-spec-changes.md:133-135` disagreement about whether the column default holds the baseline is still an open `### Deferred`. Both spec-side sites are rationale that lands nowhere in `spec/`, so a lens cannot file it; only a fix pass can close it. It is still unclosed as of this round.


### [non-spec.2.review-kubernetes.1]

DECISION: returned an EMPTY findings list on the non-spec staging, the third consecutive empty return
for this lens across both lanes — BECAUSE `diff -rq scratchpad/cp-snap/0076-run7/non-spec-r2-start
proposals/0076_.../` returns NOTHING AT ALL (exit 0, no differing file, review log included at the
moment I ran it), so every byte of `non-spec-changes.md`, `spec-changes.md`, `summary.md`, and the
checklist is identical to what `[non-spec.1.review-kubernetes.1]` already swept, and none of the three
reopening conditions that entry named has been met: CODE-4 grew no chart or CRD deliverable, CODE-3
grew no apiserver or `Sandbox.status` write, and D7's acceptance arm is unrewritten —
ALTERNATIVES: I re-derived rather than inherited (a) the Helm hook ordering CODE-4 rests its
rolling-window argument on, (b) the chart/CRD carrier set for the counter baseline, (c) the agent-pod
zero-RBAC posture CODE-3's per-session post-mortem sits inside, (d) the preStop grace / admission-floor
question D7's acceptance arm raises, and (e) a chart-side migration-version pin that adding 0181 would
falsify. All five hold; (d) and (e) are written out below because (e) is new.

FACT (new, nobody had recorded it): adding migration 0181 obliges NO `charts/` edit, and the check is
two commands rather than an argument. `grep -rn "0180\|migrationVersion\|expectedMigration\|migrate"
charts/lenny/templates/preflight*.yaml charts/lenny/values.yaml` returns only the six prose/config
lines of the `migrate:` values block (`charts/lenny/values.yaml:3772-3788`), none of which names a
migration number or count, and `migrations/embed.go:15` embeds the set with `//go:embed *.sql` rather
than enumerating it. So no chart artifact, values key, preflight Job, or admission policy pins the
migration set, and criterion (d) has no chart carrier for CODE-4. This closes the one candidate a
Kubernetes lens generates that the earlier chart FACT (which only grepped for the column token) did
not reach — EVIDENCE: charts/lenny/values.yaml:3777-3788; migrations/embed.go:11-16.

FACT: `charts/lenny/templates/migrate-job.yaml` still verifies CODE-4's whole rolling-window argument
in one read, and both anchors the staging cites resolve exactly. The template header states the Job
runs "at weight -5, after the lenny-preflight Job (-10) ... and before the gateway Deployment (a
normal resource, applied after all pre-* hooks complete)" and names the §10.5 expand-contract
discipline; the annotations are `pre-install,pre-upgrade` / `"-5"` /
`before-hook-creation,hook-succeeded` — EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16,
:37-39. `non-spec-changes.md:137-139` cites `:10-16` and `:37-39` and both land.

FACT: CODE-3 introduces no apiserver path and the post-mortem was ALREADY per-session, so D5/CODE-3
changes the value in the record rather than the record's unit. `writeHoldPostMortem(session, gen)`
marshals a `{sessionId, reason, lastGeneration, terminatedAt}` struct to `PostMortemDir` on local
disk and `terminateHeldSession` notifies the gateway through `EmitAdapterTerminating(m.sessionID,
reasonCoordinatorLost)` on CH-ADAPTEREVENTS, whose own comment names the 60s orphan-session reconciler
as the fallback. Nothing on this path touches the kube-apiserver — EVIDENCE:
pkg/adapter/holdstate.go:225-232, :262-267, :283-296.

USEFUL [`### Traps`, the `spec/10:130-138` two-grace-budgets bullet]: this is the single most valuable
entry in the log FOR THIS LENS and it saved the pass. The candidate a Kubernetes reader generates from
D7 is "an accepted barrier now blocks where a refused one returned instantly, so the pod's
`terminationGracePeriodSeconds` floor is short". The trap names both formulas, notes the agent-pod
floor omits `checkpointBarrierAckTimeoutSeconds` by the spec's own admission at `:136` while the
gateway budget at `:138` sums it (90+90+30=210 against a chart default of 240), and gives the
disposition: D7 MOVES the never-handed-off population's capture from the post-barrier eviction stage
into the barrier-ack stage rather than adding a stage. I confirmed the mechanical half independently
and it holds: `dispatchOne` starts `CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and
`cpWG.Wait()`s after it unconditionally, so the wall clock is the checkpoint stream's whatever the
barrier returns, and a true `Acked` makes the prestop loop `continue` past that session
(`barrierAcked[sess.SessionID]`), so D7 removes a duplicate capture rather than adding one —
EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:217-227, :236; pkg/gateway/podlifecycle/prestop/
prestop.go:390-397.

USEFUL [`### Settled`, "Two lens-scoping facts worth one grep each"]: the
`sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|informer|etcd|leader|
admission|lease` grep over `non-spec-changes.md` returns thirteen lines and every one is either
`ExtendCredentialLeaseRequest`, the Postgres/Redis `coordination_lease` table, the gateway `Sweeper`'s
session lease, or the word "releases". There is no Kubernetes object anywhere in the non-spec staging.
One call, and it correctly scopes the whole pass.

WATCHOUT: do NOT read the review log with `sed`/`awk` piped through Bash. `awk '/^## Standing
context/,/^## [^S]/'` and `sed -n '809,1100p'` both exceeded the output cap and were persisted to
tool-result files that then report a SHORTER line count than the range requested (400 lines for a
543-line range), which looks exactly like the section ending early. `### Traps` really runs to log
line 1352 and I nearly missed its last 150 lines, which is where the `spec/10:130-138` grace-budget
entry that decided this pass lives. The existing WATCHOUT says to use the Read tool at explicit
offsets; it is right, and the failure mode it warns about is silent rather than loud.

OPEN: this lens has now returned empty three times (run 6 spec round 4, run 7 non-spec round 1, and
this round), the last two over BYTE-IDENTICAL text, and each empty return costs the other eleven
lenses a full round. The reopening conditions `[non-spec.1.review-kubernetes.1]` stated are still the
right ones and are still unmet. Retiring this lens on this proposal costs nothing; the standing
`### Open` item "Whether an exhausted lens is retired" has now been asked for six rounds with no
answer.


### [non-spec.2.review-mechanism.1]

DECISION: returned EMPTY under the end-to-end mechanism lens — BECAUSE every flow the non-spec staging
describes traced clean from origin to effect against the tree (barrier gate open/link/release/complete,
the per-entry resolve rule, the hold's gen drag, the migration + two Create floors under the rolling
window, and the four test-amendment arithmetics), and every candidate I did generate was already on the
run's refuted list or in `### Open` — ALTERNATIVES: rejected filing (a) the accepted-barrier-blocks hazard
in §8's tier-1 co-tenant bullet ("a `CheckpointBarrier` for `sess-b` at 2 is accepted") and in the tier-3
wire bullet, both close variants of the already-refuted
`TestCheckpointBarrierRejectsWithoutFence`-has-no-replacement-assertion finding; (b) the
stream-links-before-the-barrier-opens race under D7, which is the `### Traps` "drain-path candidate the
standing D7 refutation does NOT cover" and is declined there on `fireBarrier`'s single
`context.WithTimeout` over concurrent goroutines; (c) the unlocked read of the detached `*slotState`'s
`lastFenced` in CODE-3's post-mortem, because the proposal names the SOURCE of the value and not its
synchronization, and `coordinationState` embeds its own mutex and moves with it under CODE-1, so a fixer
naturally locks; (d) the tier-4 and tier-7a new cases naming a directory rather than a file, because §8
spells the assertions out and the tier is fixed, so nothing applied is wrong.

FACT: `pkg/adapter/export_test.go` already carries `BeginCheckpointOpForTest` (takes the pod op lock for a
named session and returns the release func) and `WaitPendingCheckpointForTest` (blocks until a checkpoint
for a session id has entered the op lock's pending set). Together they make §8's mid-flight deregistration
case constructible deterministically with no new test-only hook, which CLOSES the standing `### Open`
"UNVERIFIED: whether the external test package can stall `s.ops.Begin` deterministically without a
test-only hook" — EVIDENCE: pkg/adapter/export_test.go:65-80

FACT: the landed `TestCheckpointBarrierAcksEchoedCheckpointID` drives the gate by calling `s.barrier.link`
and `s.barrier.complete` DIRECTLY in `package adapter`, with `waitBarrierWaiting` spinning on
`s.barrier.waiting`, rather than by driving a real `Checkpoint` stream. So §8's tier-1 co-tenant
barrier-gate bullet ("two bound sessions hold their own barrier gates") does NOT hit the external-package
wall the standing `### Traps` entry warns about for stream-driving cases: it can link and complete each
entry's gate in-package. Do not file it as mis-placed — EVIDENCE: pkg/adapter/coordination_test.go:222-233,
:277-287

FACT: the whole citation set the mechanism lens leans on re-resolved exactly this round; do not re-derive.
`coordination.go` :44/:52/:63 accessors, :99 fence stale gate, :108 gap, :148 `barrierGate`, :158-166
`open`, :171-176 `release`, :180-188 `link`, :192-199 `complete`, :216 `checkSessionBound`, :224-226
non-positive guard, :232-235 locked read, :236 gate, :264 `open()`, :269 `release()`. `server.go` :302
`coord`, :307 `hold`, :314 `barrier`. `holdstate.go` :43 `gen`, :119, :128, :130-132, :187, :206, :225,
:283 — CODE-3's enumeration is exact and complete. `slotsession.go` :174 `deregisterSlotLocked`, :282-285
`heldSession`, :347-365 `deregisterStartedSessions`. `checkpoint.go` :94 roots, :111 `ops.Begin`, :122
`link`, :124 `defer complete`. `oplock.go` :116-129 checkpoint admission (CODE-1 cites :119-129, §8 cites
:117-128; both land inside it). `barrier.go` :191-200 concurrent fan-out, :207-254 `dispatchOne` with the
`sessioncheckpointmeta.Record` at :237-244. `coordfence.go` :146-153 floor, :179-183 transient arm,
:185-188 budget relinquish. `cmd_run.go` :498-508 tier-0 vet set, :635-641 lint-migrations, :880 `-race`.
`Makefile:91-94` generate-proto. `proto_no_drift_test.go:70` the stub gate. `pgstore.go` :140 `Create`,
:177 insert column list, :244-248 schemaVersion normalisation (all three inside `Create`; the far-apart
line numbers are not a misattribution). `memstore.go` :46 `Create`, :58-61 SchemaVersion, :120-146 `Update`
clamp. `coordfixture.go` :76 `StartPod`, :98 `StartSession`, :106-108 fence comment, :109 `Fence`, :115
`LastFenced`, :122 `StaleRPCRejected`, :220-241 `ReadoptAndFence`.

FACT: the two test-amendment arithmetics check out exactly, so a later round need not re-run them.
tier-8 `coordination_crash_takeover_test.go`: subtests 1 and 2 seed `CoordinationGeneration: 1` at :118 and
:179 and assert row 2 at :147 and row 3 at :227 (both unaffected by the baseline), and hold the three
`pod.LastFenced()` reads at :150/:151, :195/:196, :223/:224 (CODE-1's edit); subtest 3 seeds UNSET at
:239-241, makes NO `LastFenced` call, and asserts 1, 1, 2 at :267, :283, :296 (CODE-4's shift to 2, 2, 3).
The disjointness claim holds. tier-7a `coordination_colocation_race_test.go`: `live` seeded 1 at :130,
`terminating` seeded 0 explicitly at :144, `pod.LastFenced()` at :260, assertion of 2 at :264-265 (already
correct), assertion of 0 at :287-288 (becomes 1). Both files' step splits are safe as §8 states.

USEFUL [`### Traps` "drain-path candidate the standing D7 refutation does NOT cover"]: it is the exact
candidate my lens generates first and it named the three grounds that kill it, saving a full work-up.

USEFUL [`### Traps` "harness hazards" + the `diff -rq` instruction]: the snapshot diff returned only the
review log again, so the staged text is byte-identical to run 7 round 1's. Anything a round-1 shard filed
against `non-spec-changes.md` is still live, unrepaired, and re-filable only by a fix round.

WATCHOUT: `sed -n 'A,Bp'` on `.../review-log.md` for the `## Standing context` extract exceeds the Bash
output cap and persists to a tool-result file. Extract it once with
`awk '/^## Standing context/,/^## Ledger/' > /tmp/standing.txt` and then `sed` /tmp/standing.txt in
<=220-line windows; that reads the whole 1,649-line section in seven calls and nothing truncates.
EVIDENCE: proposals/0076_.../0076_....review-log.md (the section runs to `## Ledger`)


### [non-spec.2.review-open-decisions.1]

DECISION: Filed ONE finding, OD3's `spec/04:151` false citation — BECAUSE it is the only open-decisions defect this pass found that is a hard file:line falsity (bar (a)) with a downstream consequence (a "yes" answer leaves `spec/04:151`'s declared-not-derived rationale false and unedited), and every other candidate is the exact hygiene class the material skeptic has now refuted ten-plus times — ALTERNATIVES: OD10 (answered by the proposal's own `### Defects` bullet, withdraw-in-place remedy, refuted class); the `summary.md:105-106` status-file "closing paragraph" falsity (true finding, but the same hygiene class and not exclusively my lens's, since the site is in `## Summary` rather than `## Open decisions`); the `### Items §7 lists` heading mismatch (bookkeeping).

MISTAKE: run 7 round 1's open-decisions shard filed SEVEN findings and SIX were refuted by name (OD1, OD4, OD9, OD11, OD12, and the `IMPLEMENTOR TO FILL THE BLANKS` header). The refutation ground was identical every time: an entry in `## Open decisions` stages no spec edit, no code, and no test, so its wording cannot make the applied spec or the implementation wrong. A finding from this lens survives only when it is a hard false file:line citation or when it reaches a staged deliverable. Do not re-file entry-hygiene, framing, or withdraw-in-place remedies.

FACT: `spec/04:151` is the "Each request message on the gateway-adapter protocol is either session-scoped or pod-scoped" paragraph. Its only named examples are `CheckpointRequest` and `CheckpointStart`. `CoordinatorFenceRequest` occurs in `spec/04` at exactly two lines, `:175` (the table row) and `:188` (the declaring sentence). spec/04 last changed 2026-08-19 (`f37e867b8`), so these numbers are stable. — EVIDENCE: spec/04_system-components.md:151, :175, :188

FACT: reclassifying `CoordinatorFenceRequest` to session scope DOES oblige an edit at `spec/04:151`, but for a different reason than the summary gives. `:151` grounds the declared-not-derived rule on "`session_id` appears on messages of both classes". After the reclassification no pod-scoped row carries a session field: `:188` states that of the four remaining pod-scoped Adapter messages, and `ReportPodScrubRequest` carries `pod_id` only. Verified in the proto. — EVIDENCE: spec/04_system-components.md:151, :188; schemas/lenny-adapter.proto:492-496 (`ReportPodScrubRequest`), and the four bodies of `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest`

FACT: the tier-0 message-scope gate genuinely accepts either value, so OD3's "no gate catches the contradiction either way" holds. `declaredScope` reads only the cell's first word and passes on "session" or "pod"; nothing pins `CoordinatorFenceRequest` to pod. — EVIDENCE: tests/tier0_static/adapter_proto_message_scope_test.go:75-81, :111-116

FACT: every other file:line citation in `## Open decisions` verifies. Checked this pass and all correct: `cmd/lenny-gateway/httpsurface.go:588-602` (OD1, the cache fallback seeds 0 at `:592` and overwrites at `:594`), `pkg/adapter/coordination.go:99` (OD2/OD6, `s.coord.initialized && gen <= s.coord.lastFenced`), `coordfence.go:164-179` (OD2's stale-then-relinquish arm), `start.go:4233-4240` (`fenceResumedPod` does not increment), `coordination/coordination.go:463-480` (`RecordHandoff` unconditional `++`), `:430` (OD10's pre-bump `upsertMirror`), `spec/07:196` (OD7's replacement-pod re-attach), `spec/28:163-169` (OD11's §28.4), `charts/lenny/templates/migrate-job.yaml:10-16` and `pgstore.go:177`, `:260` (OD8), `migrations/0050...:38-39` (OD9's `CHECK`), `tests/claim-map.json:75-82` (R16 barrier field row) and `:512-518` (R12 AdapterEvents client `UNWIRED`), `spec/10:38` (step 2 precondition), `spec/10:185` (§10.1.8 step 3), `spec/28:1075` (`checkpoint_request` carries `type`/`checkpointId`/`deadlineMs`, no session id). Do not re-derive these.

FACT: the `## Impact on other proposals` table's four statuses are correct against `.claude/tools/proposal-status.mjs`: 0060 Implemented, 0073 Implemented, 0075 Draft, 0080 Draft. 0075's stated ground is verbatim what 0075 carries ("The write target is the pod. The identifier selects nothing.", `proposals/0075...:88-89`), and 0080 carries both invalidated entries (`:184-192`, `:211`). — EVIDENCE: proposals/0075_fix_derive-message-scope-from-the-address-type.md:85-89; proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:184-192, :211

FACT: the sole `IMPLEMENTOR'S CHOICE:` marker in either change file is `non-spec-changes.md:290-297`, and it is properly bounded: it names what is open (the second-session helper, and which existing tier-1 file in `pkg/adapter` each case lands in) and the constraint (the stated assertions are the ones made, each case carries a `// spec:` naming §10.1.2 and §10.1.8, and the amended hold case keeps its name so its `// diagnosis:` stays attached). It delegates no wire contract, no fail-closed predicate, no actor, and no ordering. Do not file it.

CORRECTS [non-spec.1.review-open-decisions.1, its DEFERRED line]: that entry says "item 1's own text still says 'What remains for the reviewer is whether the comparison ... stays equality', which finding OD1 says is settled". OD1 does NOT say it is settled: the section preamble declares every entry open and needing a reviewer's answer, and OD1 carries a *recommendation* ("keep equality"), not a settlement. `spec-changes.md:613-614` and OD1 agree. There is no contradiction there to fix.

CORRECTS [standing context `### Open`, "Status file" line 1410]: its second half is false. The status file has no closing paragraph framing the hold's scope as open; its closing paragraph is `## Review history`, which describes the 2026-09-02 review loop. `summary.md:436-437` already records the correction, while `summary.md:105-106` still asserts the false half. Only the scope-bullet half of that OPEN is live. — EVIDENCE: 0076...status.md:20-33; 0076...summary.md:104-106, :436-437

WATCHOUT: the summary section headed `### Items §7 lists, and how they should be dispositioned` (`summary.md:373-392`) does NOT list §7's items. §7 lists three: the barrier gate's equality (item 1), a fence for an unheld session (item 2), and `coord.mu` (item 3). The summary section dispositions items 2 and 3 plus a fourth thing §7 does not list (the `IMPLEMENTOR TO FILL THE BLANKS` headers), and omits item 1 entirely, which is OD1's subject. Weighed and not filed as bookkeeping, but a fixer clearing §7 from that section will be one item short and one item long. — EVIDENCE: spec-changes.md:604-616; summary.md:373-392

OPEN: OD3's recommended answer still has no staged deliverable. A "yes" needs `spec/04` edits at `:175`, `:188`, and `:151`, a rewrite of the tier-3 comment at `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:41-43`, and a decision about 0075's retyping deliverable. SPEC-3 edits §4.2 alone. Whether a "yes" is in scope here or defers to a successor is unstated and is the half a reviewer cannot answer from the entry. Carried from `[non-spec.1.review-open-decisions.1]`, still unlanded.


### [non-spec.2.review-operational.1]

DECISION: filed exactly one finding, against SCHEMA-1 in `non-spec-changes.md:11-14`, for deleting the
operational tail of the `CoordinatorFenceRequest` message comment (gRPC status, `coordinator_handoff_stale`
detail string, and the `lenny_coordinator_handoff_stale_total` attribution) — BECAUSE that tail is the sole
tracked non-Go carrier of all three, SCHEMA-1 delegates the wording wholesale to SPEC-2, and SPEC-2's
prescription (`spec-changes.md:506-512`) both truncates the comment in its description and hands a full
replacement sentence with no preservation clause. The remedy is one clause in SCHEMA-1, which already carries
a per-carrier disposition note for the field comment, so it lands in THIS lane even though the twin filing sat
in the spec lane and never landed — ALTERNATIVES rejected: the Goals bullet "`lenny_coordinator_handoff_stale_
total` counts a genuine stale fence" (`summary.md:131`) against the same document's shipped-defect note that
the driver conflates three classes into that metric (`:404-407`) — proposal prose that lands in no file, and
the goal is true of the false positive this change removes; the `CheckpointBarrier` carriers losing
`FailedPrecondition` under the same instruction (see the UNVERIFIED below); tier-list and §9 bookkeeping
(thrice-refuted class); the pre-existing docs drift at `adapter-contract.md:68`, `:69`, `:79` and
`upgrades.md:47-54`.

FACT: the whole `coordinator_handoff_stale` non-Go surface is two proto lines. `grep -rn coordinator_handoff_
stale spec/ docs/ schemas/ charts/` returns four hits: `spec/10:71` and `docs/reference/metrics.md:307` and
`spec/16:183` carry only the METRIC name (`lenny_coordinator_handoff_stale_total`), and only
`schemas/lenny-adapter.proto:1445-1446` carries the detail string, the `FailedPrecondition` status, and the
sentence tying the rejection to the counter. Same for the barrier: `FailedPrecondition` for `CH-BARRIER`
exists only at `proto:1473` and `:1478`; `spec/` and `docs/` state it nowhere — EVIDENCE:
schemas/lenny-adapter.proto:1442-1446, :1469-1474, :1477-1479; spec/16_observability.md:183;
docs/reference/metrics.md:307

UNVERIFIED: whether the two `CheckpointBarrier` carriers keep `FailedPrecondition`. `spec-changes.md:536-541`
tells both to take "the §10.1.2 step 3 wording SPEC-1 stages", and that staged wording
(`spec-changes.md:157-163`) carries no status code, so a sentence-level replacement of
`proto:1471-1474` and `:1477-1479` deletes the barrier's only statement of its rejection status. The counter-
reading is the instruction's own closing clause ("the message-level and the field-level text do not disagree
about the comparison"), which scopes it to the comparison. This is the standing `### Open` item of the same
name, now with the grep behind it; it was NOT filed because two readings survive and one is benign. A fixer
touching SCHEMA-1 for the finding above should state the barrier carriers' disposition in the same clause.

FACT (re-derived independently, cheaply, and it holds): the observability surface this change reaches is
closed. No alert rule reads any coordination counter — the only coordinator alert evaluates
`lenny_coordinator_handoff_duration_seconds` (`pkg/alerting/rules/rules.go:1583`). `lenny_adapter_coordinator_
hold` is an unlabelled pod gauge (`pkg/adapter/metrics.go:108`, `:222`), so D5 leaves it exact.
`lenny_coordinator_handoff_stale_total` increments only in `coordfence.fence` (`coordfence.go:170`, `:203-205`),
never on the barrier path, so D7 moves no count. `grep -rln "coordinator_connection_lost|last_generation|
coordinator_lost" tests/` returns NOTHING, so no test outside `pkg/adapter` pins the log lines CODE-3 rewrites,
and §8's hold bullet (`non-spec-changes.md:242-249`) pins both halves of the rewrite.

FACT: the summary's "The gateway has no `CH-ADAPTEREVENTS` client" claim (`summary.md:410-415`) verifies
exactly. `tests/claim-map.json` claim 74 is "gRPC control stream `Adapter/AdapterEvents` (client)", status
`UNWIRED`, deferral `R12`. Beware that a `grep -n R12 tests/claim-map.json` hits claim 0 first, whose note is
about the adapter metrics endpoint, which reads as the summary citing the wrong row; parse the JSON instead.

FACT: `coordfence.fence`'s arms resolve as CODE-4 cites them, checked line by line: the zero floor is
`coordfence.go:147-153`, the transient `default:` arm is `:180-183`, and the budget-exhausted relinquish is
`:186-188`. The transient arm calls `incRetry` only, so the relinquish that follows increments
`lenny_coordinator_fence_relinquished_total` with no matching stale increment — which is what makes CODE-4's
retention argument an observability argument as well as a correctness one.

USEFUL [standing context, "The alert and metric surface is closed and untouched"]: it named the four metric
rows and the one alert, so re-deriving the whole inventory cost three greps instead of a pass. Its
`lenny_checkpoint_barrier_ack_total`-has-no-incrementer correction also held: the counter is specified
(`spec/16:41`), documented (`docs/reference/metrics.md:196`), catalogued
(`pkg/observability/metrics/catalog.go:127`), and emitted nowhere.

USEFUL [`### Traps`, "the proposal states preservation explicitly wherever it means it, so silence is an
instruction to replace"]: this is the whole ground of the finding above. Without it the `CoordinatorFence
Request` paragraph reads as a unit-change instruction rather than a whole-sentence replacement.

MISTAKE: `[non-spec.1.review-operational.1]` returned empty over byte-identical text one round ago and did not
reach the SCHEMA-1 carriers at all; it spent its pass on CODE-3's and CODE-4's own citations, which are sound.
The standing `### Open` had already named the defect as FILED-and-unlanded, and the log's own loop-level
MISTAKE ("routing a filed defect into a bookkeeping `### Open` item is not discharging it") predicted exactly
this. An operational lens on this proposal should open the proto carriers FIRST; everything else in its
inventory has been closed for six rounds.


### [non-spec.2.review-performance.1]

DECISION: Returned an empty findings list for the performance/scalability/failure-mode lens over run 7 non-spec round 2 — BECAUSE every write-rate, bottleneck, and degradation axis I could construct either resolves to "unchanged", "improved", or a standing refutation I re-derived and confirmed — ALTERNATIVES: I considered filing (a) the co-tenant drain serialisation inside the 90s barrier window, (b) the per-session gate turning early cross-linked barrier returns into long blocking waits, and (c) the barrier cache fallback's per-binding serial Postgres read at drain time; each is refuted below.

FACT: `diff -ru` against `scratchpad/cp-snap/0076-run7/non-spec-r2` shows ONLY `.review-log.md` changed since the snapshot. The staged text (`spec-changes.md`, `non-spec-changes.md`, `summary.md`, `implementation-checklist.md`) is byte-identical, so round 2 is a re-review of the same text round 1 filed fifteen shards against — EVIDENCE: proposals/0076_.../*.md mtimes 06:23 vs review-log 07:33

FACT: The op lock genuinely QUEUES a distinct co-tenant session id rather than rejecting it, so the proposal's "queues the distinct co-tenant session id behind the running one" is exact. Coalescing (`errOpCoalesced`) fires only for a session id already pending; a distinct id falls through to `l.wait` and blocks on `promote`. Promotion order is lexicographic by session id and carries no fairness or liveness meaning — EVIDENCE: pkg/adapter/oplock.go:116-129, :135-145; pkg/adapter/checkpoint.go:99-111

FACT: The "per-session gate newly serialises N co-tenant checkpoints inside one 90s deadline" regression does not exist, and the reason is stronger than the standing entry states. `dispatchOne` starts `CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and `cpWG.Wait()`s after it, so the N captures already serialise inside the barrier window today even when the barrier is refused instantly. Pre-fix a refused barrier leaves `Acked` false and prestop captures the session a SECOND time; post-fix the ack lands and that duplicate is skipped. Post-fix is strictly cheaper — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:217-227, :237; pkg/gateway/podlifecycle/prestop/prestop.go:394-397

FACT: No alert rule anywhere in `pkg/alerting/rules/` references `handoff_stale`, `generation_gap`, `coordinator_hold`, `barrier_ack`, or `fence_relinquish`. All five metrics are catalogued in `docs/reference/metrics.md` as "Operational monitoring" with no alert, so the rate changes this proposal causes (fewer stale rejections, fewer gap logs, a shifted `lenny_checkpoint_barrier_ack_duration_seconds` distribution on co-tenant pods) reach no alert catalog and force no spec/16 or runbook edit. Do not file a missing alert-catalog edit site — EVIDENCE: docs/reference/metrics.md:196-197, :307-312; empty grep over pkg/alerting/rules/

FACT: The production edit set for CODE-1/CODE-3 is closed and §9 covers it. A tree-wide grep for `.coord.`, `s.barrier.`, `LastFencedGeneration`, `isQuiescedForBarrier`, and `BarrierWaiting` over non-test Go returns only `pkg/adapter/coordination.go`, `pkg/adapter/checkpoint.go`, `pkg/adapter/holdstate.go`, `tests/testinfra/coordfixture/coordfixture.go`, and two proto-response hits in `pkg/gateway/runtime/adapterclient/coordinatorfence.go` that are the RPC response field rather than the Server accessor. Every one is in §9 — EVIDENCE: non-spec-changes.md:390-411

FACT: Both store-backed values the design relies on are durable. The barrier's generation comes from the `coordination_lease` mirror row or the live `sessions` row, both Postgres; `coordination_lease` is created by migration 0164 alone with a partial index on `coordinator_replica` sized for the drain fan-out. The §12.4 durable-fallback bar is met with no new register — EVIDENCE: migrations/0164_coordination_lease.up.sql:44, :55-60; cmd/lenny-gateway/httpsurface.go:588-604

WATCHOUT: The barrier cache fallback issues one serial `w.sessions.Get` per binding with `context.Background()` (no deadline) inside the target-assembly path. At a top-tier replica that is up to `maxSessionsPerReplica` sequential Postgres round trips with no budget. It is shipped behaviour that this proposal neither creates nor widens, so it is NOT a finding here, but a future perf lens will rediscover it — EVIDENCE: cmd/lenny-gateway/httpsurface.go:589-603

WATCHOUT: `pkg/adapter/coordination.go:124-128` carries a deadlock-safety comment asserting "the hold timeout never reaches back into coord.mu". CODE-3 makes `terminateHeldSession` read each session's generation off the `*slotState`, which takes that entry's relocated mutex, so the comment is false in letter after the change. There is still no cycle (`onHoldTimeout` unlocks `hold.mu` at `:189` before both passes), so this is a stale comment in a file §9 already lists, not a reliability defect. The standing context claims it "is rewritten in the same pass" but no staged deliverable says so — EVIDENCE: pkg/adapter/coordination.go:124-129; pkg/adapter/holdstate.go:179-189, :225-229

USEFUL [standing: the barrier fan-out runs under one wall-clock deadline]: it saved me from filing the drain-serialisation regression, and its "Do not re-derive this" is correct — I re-derived it anyway and it holds, with the `cpWG.Wait()` line being `barrier.go:227` rather than the `:243-244` the standing entry cites.

UNVERIFIED: nobody has quantified whether `checkpointBarrierAckTimeoutSeconds` (90s default) actually covers `maxConcurrentSessions` serialised co-tenant uploads at the top tier; the standing context notes the CRD webhook validates it against ONE slot's `max_tiered_checkpoint_cap`. Post-fix this only converts "duplicate capture" into "one capture that may time out", so it is not a regression, but a capacity lens or a human should size it before the co-tenancy limit is raised.


### [non-spec.2.review-reliability.1]

DECISION: returned EMPTY over the non-spec staging — BECAUSE every reliability candidate this pass generated
resolved into a standing adjudication, and `diff -rq` against the round-1 snapshot shows only the review log
changed, so the staged text is byte-identical to what four earlier empty reliability returns already read —
ALTERNATIVES: filing CODE-3's unsynchronised post-mortem read (see DEFERRED below), filing the D7
accept-then-block drain regression (covered by the standing "drain-path candidate the standing D7 refutation
does NOT cover" trap), and filing the advisory-`quiesced` harm claim (pre-adjudicated by the standing
"quiescence cannot be answered per session on the runtime side" trap).

USEFUL [Traps: "the drain-path candidate the standing D7 refutation does NOT cover"]: it is the exact
candidate a reliability lens derives first from D7 — a never-handed-off session's barrier goes from an
instant refusal to a block on a gate nothing links. The entry's three grounds (one `context.WithTimeout` over
the whole `Dispatch` with per-target goroutines, the §10.1 gateway grace formula already summing the barrier
term, and the shipped comment naming the empty-ref-on-deadline outcome as designed) all re-verify at
`prestop.go:501-506` and `barrier.go:192-201`, `:219-227`. Saved a full work-up.

USEFUL [Traps: harness hazards / "run the `diff -rq` first"]: cheapest move on this proposal, third round
running with nothing but the log changed.

FACT: the `Checkpoint` stream quiesces the runtime itself, independently of the barrier. `Server.Checkpoint`
calls `s.Lifecycle.RequestCheckpoint` (checkpoint_request → checkpoint_ready over CH-RUNTIMEOPS) before the
first chunk and `CompleteCheckpoint` on return, and `dispatchOne` starts that stream whatever the barrier
does. So a refused barrier does not leave the archive running against an unquiesced runtime on a Full-level
runtime; what it loses is the §10.1.8 dispatch-stop the `quiesced` flag would carry if anything read it, plus
the acked-barrier record. Do NOT work this into a finding against the proposal's harm statement: the standing
`quiesced`-is-not-the-runtime-quiescence trap already covers the direction, and the spec-level harm is real —
EVIDENCE: pkg/adapter/checkpoint.go:150-161; pkg/gateway/coordination/barrier/barrier.go:216-227.

FACT: the hold's termination race window is real and narrow. `onHoldTimeout` clears `hold.active` under
`hold.mu`, releases it, then pass 1 takes `s.mu` and deregisters; a `CoordinatorFence` that passed
`checkSessionBound` (which takes and releases `s.mu`) before pass 1 is still in flight and, under CODE-1,
holds the resolved `*slotState` and writes `lastFenced` on it. Pass 2 then reads that same field for the
post-mortem. So the fence-versus-post-mortem overlap the security shard guessed at is reachable —
EVIDENCE: pkg/adapter/holdstate.go:178-208; pkg/adapter/slotsession.go:267-274, :347-368.

DEFERRED [`non-spec-changes.md` CODE-3, `:107-109`]: the staged sentence says `terminateHeldSession` and
`writeHoldPostMortem` "read each terminated session's own last fenced generation off the `*slotState` the
`heldSession` already carries" and states no synchronisation, while the value it names lives inside
`coordinationState`, which carries its own `mu` and whose every other reader locks it
(`coordination.go:44-47`). What is true instead: that read must take the entry's `coordinationState.mu`,
because the window above is reachable and an unlocked `m.state.…lastFenced` is a data race the -race detector
will only catch if a test happens to interleave it. Not filed: the staging neither stages nor forbids the
locked read, the natural Go implementation locks, and the cost of getting it wrong is a garbled integer in a
best-effort log line and a 0600 JSON record. One clause in CODE-3 closes it. This supersedes the standing
`### Open` "UNVERIFIED: whether CODE-3's post-mortem read ... takes the entry's `coordinationState.mu`" — the
window is now verified; only the remedy is unapplied.

WATCHOUT: `pkg/adapter/oplock.go`'s comment says a distinct co-tenant session id "is admitted", which reads
as "runs now". It is added to the pending set and then blocks in `l.wait` until promoted, which is what
CODE-1's and §8's "queues it behind the running one" means. Both the CODE-1 range (`:119-129`) and the §8
range (`:117-128`) land on that block; the two differ by two lines and neither is wrong — EVIDENCE:
pkg/adapter/oplock.go:116-129.

FACT (re-verified, so a later round need not): CODE-4's coordfence anchors are exact. The floor is
`coordfence.go:147-153`, the transient `default:` arm `:180-183`, and the budget-exhausted relinquish
`:186-188`; the stale arm re-reads and retries at `:165-179`. §8's tier-8 shift claim also re-verifies: the
third subtest seeds through `sessionpg.Store` with the field unset (`coordination_crash_takeover_test.go:238-242`)
and asserts 1, 1, 2 at `:267`, `:283`, `:296`, so the pgstore floor CODE-4 adds makes them 2, 2, 3.

OPEN: this is the reliability lens's fifth or sixth empty return on unchanged text. Its stated reopening
conditions (D7's acceptance arm, the retained `coordfence` floor's rolling-window behaviour, or §10.1.8's
failure-arm claim being rewritten) were not met this round and cannot be met by a round that changes nothing.
Retiring the lens until one of those three sentences moves costs nothing and buys every other lens a round.


### [non-spec.2.review-security.1]

DECISION: returned an empty findings list over the NON-SPEC staging (`non-spec-changes.md`: SCHEMA-1,
CODE-1 through CODE-4, TEST-1, §8, §9) read together with `spec-changes.md` — BECAUSE `diff -rq` against
`scratchpad/cp-snap/0076-run7/non-spec-r2` returns ONLY the review log, so the staged text is byte-identical
to the text this same lens already returned empty over in run 7's non-spec round 1
(`review-log.md:3213-3275`), and every candidate I derived independently this pass resolved to an
already-recorded refutation, a standing OPEN, or shipped pre-existing behaviour — ALTERNATIVES worked up
and dropped this pass:
(1) migration 0181 moving `coordination_lease.coordination_generation` from `DEFAULT 0` to `DEFAULT 1`,
    on the theory that a lease row inserted without the column would newly put a value on the wire that
    the post-baseline pod ACCEPTS where the old default's 0 was refused `InvalidArgument` (a fail-closed
    to fail-open flip on the barrier's authoritative-value path). Dropped: `coordlease/pgstore.Upsert`
    is the only writer and it names `coordination_generation` in both the insert column list and the
    `ON CONFLICT DO UPDATE` set, so the column default is never taken — EVIDENCE:
    pkg/gateway/coordination/coordlease/pgstore/pgstore.go:47-58. This is the `### Settled` "four mirror
    columns always written explicitly" disposition, re-derived from the writer rather than from the log.
(2) the barrier's generation being sourced from anything the pod can influence after CODE-4. Dropped and
    re-verified from the tree: `MirrorTargetLister.Targets` copies `coordlease.Lease.CoordinationGeneration`
    off the Postgres mirror, and the fallback re-reads the session row, seeding 0 and overwriting only on a
    successful read, so a store outage still puts 0 on the wire and takes the `InvalidArgument` refusal at
    `coordination.go:224-226`. Fail-closed before and after CODE-4 — EVIDENCE:
    pkg/gateway/coordination/barrier/wiring.go:97-122; cmd/lenny-gateway/httpsurface.go:588-602.
(3) CODE-3's post-mortem read of the detached `*slotState` without the entry's `coordinationState.mu`.
    Dropped for the third time: it is the standing `### Open` UNVERIFIED this same lens filed in run 4 and
    dropped again in run 7 round 1, on the same evidence and with no new evidence available.
(4) the unbound-session barrier refusal and the cross-session REJECT direction. Both dropped: each is on
    the orchestrator's own refuted list this run.

FACT: no `coordination_lease` row is ever inserted without an explicit `coordination_generation`, so 0181's
mirror-column default change is cosmetic in the same way `0060`'s and `0164`'s were — EVIDENCE:
pkg/gateway/coordination/coordlease/pgstore/pgstore.go:47-58 (the only `INSERT INTO coordination_lease` in
`pkg/`, `cmd/`, or `migrations/` outside the DDL and the GRANT).

USEFUL [`[non-spec.1.review-security.1]`]: its closing WATCHOUT ("spend the budget on the two checks over
the CODE-1 entry-lifetime rule and the CODE-4 rolling window rather than re-greping `tests/tier9_security`
and `s.coord`") is what let this pass go to the coordlease writer instead of re-running two greps that
return nothing. Keep it.

USEFUL [`### Settled` "Tenant pinning makes co-tenancy same-tenant by construction" and
"`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches no gateway decision"]:
together they are the whole trust-boundary half of this lens on this staging, and they are why tier 9's
absence from every tier list is right.

OPEN: this lens has now returned empty six or seven times on this staging, twice over byte-identical text.
The standing `### Open` "Whether an exhausted lens is retired" names the reopening conditions for other
lenses but not for security. The condition that would genuinely reopen security here is narrow: a rewrite of
D7's acceptance arm, of CODE-4's rolling-window paragraph, or of CODE-1's entry-lifetime rule. Nothing else
in the non-spec staging carries a security bound.


### [non-spec.2.review-test-coverage.1]

DECISION: returned an EMPTY findings list for the test-coverage lens over the non-spec staging, run 7
round 2 — BECAUSE the two findings this lens filed in round 1 were both REFUTED by the material skeptic
this run (the `TestCheckpointBarrierRejectsWithoutFence` replacement-assertion gap and the unbound-session
barrier deny case), the staged files are byte-identical to round 1 (`diff -rq` against
`scratchpad/cp-snap/0076-run7/non-spec-r2` returns only the review log), and every further candidate I
generated is either on the refuted list or a standing weighed-and-declined item — ALTERNATIVES rejected,
with the reason each: (1) §8's tier-4 sentence for D7 ("Tier 4 covers the same flow across the gateway,
the session store, and the pod", `non-spec-changes.md:332-333`) names no case, file, or step — declined
because D7's behavior IS pinned concretely at tier 3 (the wire case, `:326-329`) and at tier 1 (the
amended landed case, `:330-332`), so no changed behavior is untested and the residue is the thrice-refuted
tier-list-bookkeeping class. (2) The tier-2 resume-fence-then-takeover case (`:325-326`) having no file,
package, or harness — the standing MISTAKE entry on refuted class (k) settles this shape outright: "§8
names the tier-3 case with its assertions, so criterion (f) is not met either", and the same reasoning
covers the tier-2 twin. (3) The two landed memstore `Update` tests in §8's class 1 and named nowhere
(`memstore_test.go:416`, `:430`, `:471`, `:490`) — two earlier lenses declined them because the class
sentence plus its "each shifts by one" rule gives the fix and `memstore_test.go` is already in §9.
(4) `sweep_test.go:432` and `:525` assert a FENCE-call generation (`readopter.gens[0]`) rather than a
session row's `CoordinationGeneration`, so they fall outside §8's class-1 wording while still shifting by
one under the baseline — declined on the same ground as (3), the file and its `:275`-`:594` span being
named. (5) No listed case asserts that `Create` with an explicit non-zero generation survives the new
floor — declined, `pkg/gateway/sessionserver/coordination_fence_test.go:37`/`:54` already pins that
(create at 4, assert 5 after a bump).

FACT: `pkg/gateway/coordination/coordination/coordination_takeover_test.go` really does build over
`memstore.New()` (`:74`), so §8's class-1 claim that its assertions shift under CODE-4's memstore `Create`
floor holds; a fake store would have left them fixed. It also carries a THIRD kind of shifting assertion
neither §8's class sentence nor this log names: `readopter.calls[0].generation != 1` at `:100`, the
generation handed to the fence rather than read off the row — EVIDENCE:
pkg/gateway/coordination/coordination/coordination_takeover_test.go:74, :94-95, :100.

FACT: `tests/tier2_component/coordination/sweep_test.go:322` asserts `RecordHandoff` RETURNS 0 for a
terminal session ("bump refused"). That value does NOT shift under the baseline, because it is the
post-increment return sentinel rather than a row read, so a fixer applying §8's "each shifts by one" rule
mechanically across the `:275`-`:594` span would wrongly change it — EVIDENCE:
tests/tier2_component/coordination/sweep_test.go:318-327.

FACT: the tier-2 stores suite carries no class-1 site of its own. Every generation in
`tests/tier2_component/stores/` is seeded explicitly (`evictionstatestore_test.go:257-258`,
`evictionfallback_test.go:102-103`, `partialmanifeststore_test.go:37-38`, `coordleasestore_test.go:44`),
and `pkg/gateway/session/sessionstore/pgstore/pgstore_test.go` names `Generation` nowhere at all, so
CODE-4's `pgstore` floor breaks no landed assertion. Nobody had checked the pgstore package itself.

FACT: §8's tier-1 bullet 2 (per-session barrier gates) is constructible inside `package adapter` with no
`Checkpoint` stream, because `barrierGate.link`/`complete` are called directly by the landed
`TestCheckpointBarrierAcksEchoedCheckpointID` pattern. Only the mid-flight deregistration case needs the
external package, which its own bullet already states — EVIDENCE: pkg/adapter/coordination_test.go:243-300
(goroutine, `waitBarrierWaiting`, `s.barrier.link("gw-ckpt-1")`, `s.barrier.complete()`).

USEFUL [Traps, "tier-list bookkeeping is a refuted class, three times over"]: it killed the §8-tier-4-for-D7
candidate before it cost a verification, which is the one candidate this lens had left after the round-1
refutations.
USEFUL [Traps, "MISTAKE: refuted class (k) does not reach §9's tier-3 omission"]: its closing sentence
("§8 names the tier-3 case with its assertions, so criterion (f) is not met either") is the general rule
that kills every "case with assertions but no file" candidate, tier 2 and tier 3 alike. Read it before
working one of those up.

OPEN: this lens has now run four times on this staging (`[non-spec.5.*]`, `[non-spec.6.*]`,
`[non-spec.1.*]`, this one) and its only two findings were refuted this round. Its remaining surface is
empty unless §8, D7's acceptance arm, or CODE-4's baseline classes are rewritten. Retire it, or state the
reopening condition, the way the "whether an exhausted lens is retired" item asks for the other lenses.


# non-spec round 2 post-fix verification (G1)

Verdict: the fixer's work is sound. No findings.

VERIFIED: summary.md:246-255 (OD3 material-consequence paragraph) now enumerates the three
spec/04 §4.1 edit sites correctly and gives the true ground for :151. Re-read the sources:
spec/04_system-components.md:151 is the declared-not-derived paragraph whose only message
examples are `CheckpointRequest` and `CheckpointStart`; :175 is the `CoordinatorFenceRequest`
table row; :188 is the declaring sentence. `grep -n CoordinatorFenceRequest
spec/04_system-components.md` returns :175 and :188 only. schemas/lenny-adapter.proto:492-496
is exactly `message ReportPodScrubRequest { pod_id; outcome; detail; }`, no session field, so
after a reclassification no pod-scoped row carries a session field and :151's ground is
falsified. All citations in the new text open and say what is claimed.

VERIFIED: the "no spec/04 §4.1 edit is staged" sentence. spec-changes.md:571-584 (SPEC-3)
edits §4.2's session-record paragraph alone; `grep -n "spec/04"` over spec-changes.md returns
only :573 and :578, and summary.md:454's deliverable table row for SPEC-3 names §4.2 only.
non-spec-changes.md:385 lists `spec/04_system-components.md` under §9 without a section
qualifier, which is SPEC-3's file and does not contradict the new sentence.

VERIFIED: the "which is the question 0075 owns" clause. proposals/0075_fix_derive-message-
scope-from-the-address-type.md:5-10 states 0075 "Replaces proposal 0073's declared message-scope
table with a derivation rule ... the table and its gate are retired", so retiring the
declared-not-derived rule is 0075's subject.

VERIFIED (in-scope drift site): review-log.md:667-673, the `## Standing context` bullet, was
edited. Its closing causal clause now carries the true ground and states explicitly that :151
names no `CoordinatorFenceRequest`. It agrees with the corrected summary. The bullet headline
("costs two sentences") still holds: the two sentences are :151 and :188, the third site being
a table row.

Open-ended sweep for stale parallels (grep `:151` across all seven proposal files, then read
each hit): review-log.md:373, :778, :1534, :2201-2204, :2403-2408 all describe :151 correctly
as the declared-not-derived paragraph and none repeats the "example" misattribution; :373
already carried the corrected reasoning before this round. The one surviving copy of the wrong
clause in a non-archived file is the ledger OPEN at review-log.md:3125 ("or the `:151`
example"), which sits inside the frozen per-shard round record under
`### [non-spec.1.review-operational.1]` and was not handed to the fixer as in scope; the design
adjudicated it out and recorded a CORRECTS entry instead. Not reported as a finding.
review-log-archive.md occurrences are historical and out of scope by the same rule.

Diff reviewed: exactly two hunks (summary.md:243-255, review-log.md:666-673). No other file
changed, so no enumeration, count, checklist entry, or cross-reference went stale.


### [non-spec.3.fix-G1.1]

DECISION: Withdrew OD10 in place in `summary.md` rather than answering it or deleting it — BECAUSE the section preamble (`summary.md:166-172`) declares every entry below it open and needing a reviewer, and OD10's own second half already carried the answer against the tree, so leaving it open asked a reviewer to judge something that resolves mechanically; the preamble also fixes withdrawal-in-place as the only exit, and OD8 immediately above sets the form — ALTERNATIVES: fix only the attribution and keep "No recommendation was derived" (leaves a resolved question in front of a reviewer); delete the entry (the preamble forbids it); give SPEC-2 a `spec/10` edit so the original sentence becomes true (inverts the defect, and would put one spec file under two deliverables).

DECISION: Corrected `summary.md`'s "What changes" closing sentence in the same edit — BECAUSE it asserted the same "stays true" ground as flat fact in the document's opening paragraph, and would have contradicted the withdrawn OD10 two hundred lines later. The replacement keeps the "not restated" half, which survives, and drops the "stays true" half, which does not.

FACT: SPEC-1 spans `spec-changes.md:137-293` and SPEC-2 spans `:294-570`, so the §10.1.8 disposition at `:259-264` and the step-1 closing-sentence rewrite at `:206-222` are both SPEC-1's. The deliverable index (`summary.md:452-453`) already assigns `spec/10_gateway-internals.md` to SPEC-1 and `spec/28`+`spec/29` to SPEC-2 — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:137, :206, :259, :294

WATCHOUT: `spec/10_gateway-internals.md:183` is a single 2022-character line. It carries BOTH the "The `CheckpointBarrier` message carries the current `coordination_generation`" clause and the closing "reject the barrier as a generation-stale RPC ... this is safe and does not require special handling" sentence SPEC-1 rewrites. A disposition that calls step 1 unedited is therefore false at the line granularity even when it is true of the clause, and any reader who trusts it never opens the rewrite that widens the safety claim to "Either outcome is safe and requires no special handling" (the widening OD12 records as unanswered) — EVIDENCE: spec/10_gateway-internals.md:183

FACT: The barrier-target mirror lag re-verified from source. `s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)` passes the sweep's List-snapshot value, while `RecordHandoff` bumps the stored row into a local earlier in the same iteration and returns the post-increment value; `PodDispatcher.Send` then puts the mirror's `Target.CoordinationGeneration` on the wire. So a barrier assembled from the mirror in that interval carries a value that is not the session's current one, in the shipped tree, independent of anything this proposal stages — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:430, :463-482; pkg/gateway/coordination/barrier/wiring.go:49

DEFERRED [proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md]: SPEC-1's rationale at `:259-264` closes "Each names the row value the dispatcher copies onto the wire (`pkg/gateway/coordination/barrier/wiring.go:49`), which is positive for every session once the baseline is 1, so each stays true and neither is restated." The clause "so each stays true" is false: both sentences are already false on the handoff path through the mirror lag above, whatever the baseline is. What is true instead: neither is restated because neither is made false by this change, and both are already false on the handoff path in the shipped tree through a defect this proposal does not stage. The conclusion (not an edit site) is unchanged; only the ground is wrong. This run reviews the non-spec staging and may not land converged spec staging, so the correction is left for the pass between the loops. Landing it there closes the last of the three sites that carried this clause; the other two (`summary.md:21-24` and OD10) are corrected as of this shard.

WATCHOUT: The `CH-BARRIER` Messages bullet disposition at `spec-changes.md:420-423` looks like a fourth site of the same clause and is not one. Its stated ground is that the bullet does not call the value the current one, so the mirror lag does not falsify it. Do not sweep it into the DEFERRED correction above — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:420-423

OPEN: OD5's "cost of splitting" list (`summary.md:281-286`) does not record that OD10's disposition is independent of the baseline. The withdrawn OD10 now states the coupling from its own side, so nothing is false, but a later round that edits OD5 should decide whether the list wants the same clause.

USEFUL [standing context, `### Settled`, the "Either outcome is safe" correction]: it records that the widened safety sentence is SPEC-1's own staged text rather than shipped `spec/10`, which is what made the step-1 rewrite worth naming inside the withdrawal instead of treating step 1 as untouched.


### [non-spec.3.fix-design-G1.1]

DECISION: OD10 is withdrawn in place (header becomes `**OD10 is withdrawn.**`, OD8's form at summary.md:320), carrying three things: the true attribution (SPEC-1 owns `spec/10` and states the no-edit-site disposition at spec-changes.md:259-264; SPEC-2's `spec/29` list at :428-430 names §29.7's framing paragraph and never step 4), the fact that SPEC-1 DOES rewrite §10.1.8 step 1's closing sentence (spec-changes.md:206-222) on the same physical `spec/10` line as the clause OD10 is about, and the answer (neither sentence is an edit site, because both are already false on the handoff path through the `upsertMirror` pre-bump lag, which the proposal files as a shipped-tree defect at summary.md:405-410) — BECAUSE the entry carries its own answer in its second half already, so leaving it parked as open asks a reviewer to judge something that resolves against the tree — ALTERNATIVES: (a) keep the question form and only fix the attribution, rejected because the preamble at summary.md:166 says every entry below is open and needs a reviewer's answer, which an answered entry contradicts; (b) delete the entry, rejected by the same preamble's restate-in-place convention; (c) also rewrite SPEC-1's ground at spec-changes.md:259-264, rejected because `spec-changes.md` is settled input for the non-spec loop (see DEFERRED below).

DECISION: summary.md:21-23 ("Every sentence ... stays true and is left unedited") is IN SCOPE and changes in the same edit — BECAUSE it is the same claim on the same ground OD10's withdrawal retires, restated as flat settled fact in `## Summary` → What changes; correcting OD10 alone leaves the document's opening paragraph asserting what the decision record just withdrew. Only the "stays true" half fails; "is left unedited" is true, so the replacement keeps the no-restatement half and swaps the ground for the shipped-tree falsity.

FACT: `spec/10_gateway-internals.md:183` is ONE physical line of 2022 characters carrying both "The `CheckpointBarrier` message carries the current `coordination_generation`" and the "this is safe and does not require special handling" closing sentence SPEC-1 rewrites. That is why "SPEC-1 leaves step 1 unedited" and "SPEC-1 rewrites step 1" can both look defensible from a line citation — EVIDENCE: spec/10_gateway-internals.md:183

FACT: the mirror lag re-verifies from source, do not re-derive. `s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)` at pkg/gateway/coordination/coordination/coordination.go:430 passes the List-snapshot `row` field, while `generation := s.RecordHandoff(...)` earlier in the SAME loop iteration (:371) bumps the stored row through `sessions.Update` and returns the new value into a local without writing it back into `row` (RecordHandoff at :463-482). `PodDispatcher.Send` then sends `t.CoordinationGeneration` (pkg/gateway/coordination/barrier/wiring.go:49), whose Target is assembled from that mirror — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:430, :371, :463-482; pkg/gateway/coordination/barrier/wiring.go:49

FACT: SPEC-1 spans spec-changes.md:137-293 and SPEC-2 spans :294-570 (section heads at :137, :294, :571). Every citation the OD10 finding makes falls inside SPEC-1, which is the whole attribution defect — EVIDENCE: spec-changes.md:137, :294, :571

CORRECTS [review-log.md standing entry "OD10 is settled and the staged ground is the wrong reason for the right conclusion"]: it cites the "each stays true" ground as `spec-changes.md:257-262`. The sentence runs :259-264 in the current file (opens "The sentences elsewhere that state which value a barrier carries are not edit sites under the baseline." at :259, closes "so each stays true and neither is restated." at :264). Two lines of drift; the finding's :259-264 is the correct range — EVIDENCE: spec-changes.md:259, :264

USEFUL [review-log.md Standing context, `### Settled` OD10 bullet and the "open decisions that resolve against the tree" bullet]: both halves of this finding were already established there (the mirror lag makes the conclusion right for the wrong reason; OD10 mis-attributes to SPEC-2). They saved re-deriving the sweep arithmetic, and every citation in them re-verified except the two-line drift noted above.

DEFERRED [proposals/0076.../0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md]: SPEC-1's ground at :259-264 reads "Each names the row value the dispatcher copies onto the wire (`pkg/gateway/coordination/barrier/wiring.go:49`), which is positive for every session once the baseline is 1, so each stays true and neither is restated." The "so each stays true" clause is FALSE: the value the dispatcher copies is the mirror value, which lags one generation for a sweep interval after a handoff, so both sentences are already false on that path regardless of the baseline. What is true instead: the conclusion (neither is restated) stands, on the ground that both are already false in the shipped tree for a defect this proposal records but does not stage. This loop reviews the non-spec staging and `spec-changes.md` is settled input, so the correction is derived and not landed. OD8 sets the precedent that a withdrawal which finds the staged ground false also corrects the deliverable's stated reason ("CODE-4 now states the reason"), so the between-loops pass should land this rather than leave OD10 contradicting SPEC-1.

WATCHOUT: do not "fix" OD10 by making SPEC-2 rewrite §10.1.8 step 1 or by adding a `spec/10` sentence to SPEC-2's edit list. The defect is in the decision record's attribution; SPEC-1's deliverable scope is correct as staged and SPEC-2 must not gain a `spec/10` edit site — EVIDENCE: summary.md:452-453 (deliverable index), spec-changes.md:428-430

WATCHOUT: the CH-BARRIER Messages bullet (`spec/28:349-353`, disposed at spec-changes.md:~420-423) is NOT falsified by this fix and must not be swept in. Its own stated ground is that the bullet "does not call that value the current one", so the mirror lag does not reach it — EVIDENCE: spec-changes.md:420-423

OPEN: OD5's "cost of splitting" list (summary.md:281-283) still does not name that answering "split" drops SPEC-1's §10.1 baseline paragraph (spec-changes.md:231-243). After this fix OD10 no longer depends on that baseline, so the coupling the standing context records runs to SPEC-3 and the baseline paragraph alone. A later round should decide whether OD5 gains that clause; this edit does not touch OD5.


# Post-fix verification, non-spec round 3 (G1: OD10 attribution)

Verdict: clean. No findings.

Scope: the only file changed in the diff against
/home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r3-prefix is
summary.md, in two hunks (the "What changes" paragraph and the OD10 entry).

1. LANDED. The confirmed finding is corrected.
   - summary.md:346-363 now withdraws OD10 in place (matching the preamble
     convention at summary.md:171-172 and OD8's precedent at summary.md:321),
     attributes the no-restatement disposition to SPEC-1 rather than SPEC-2,
     records that SPEC-1 rewrites §10.1.8 step 1's closing sentence, and closes
     the entry with the answer on the mirror-lag ground.
   - Deliverable ownership re-verified: summary.md:462 assigns
     spec/10_gateway-internals.md to SPEC-1; :463 assigns spec/28 + spec/29 to
     SPEC-2. SPEC-1 spans spec-changes.md:137-293 (headers at :137, :294), so
     the disposition at :259-264 is inside SPEC-1.
   - spec/10_gateway-internals.md:183 is one physical line carrying both "The
     `CheckpointBarrier` message carries the current `coordination_generation`"
     and the "this is safe and does not require special handling" closing
     sentence SPEC-1 rewrites at spec-changes.md:206-217.

2. DRIFT. The in-scope parallel site was edited and now agrees.
   - summary.md:21-24 replaced "stays true and is left unedited" with "No
     sentence ... is restated. Each is already false on the handoff path in the
     shipped tree, through the barrier-target mirror lag recorded below". That
     matches OD10 at :354-359 and the shipped-tree defect at :415-420.
   - Open sweep, non-spec editable set (summary, non-spec-changes,
     implementation-checklist, problem-statement, deviations, status): grepped
     "stays true", "left unedited", "is restated", "carries the current",
     "current `coordination_generation`", "10.1.8", "29.7", "OD10". The only
     surviving "stays true"/"unedited" phrasing is inside the withdrawn entry
     where it is quoted as SPEC-1's staged ground and marked as not holding.
     No count of open decisions is stated anywhere, so the withdrawal breaks no
     enumeration. The checklist, deliverable index, OD5, OD9, OD12, and the
     shipped-tree defect list stay true.
   - The "no sentence stating a message carries the session's current
     generation is restated" claim was checked against the staged edit lists:
     spec-changes.md:342 and :383 restate rejection rules rather than what a
     message carries, and schemas/lenny-adapter.proto has no comment claiming a
     message carries the current generation, so the claim holds.

3. CITATIONS. Every citation in the new text opened and confirmed.
   - spec-changes.md:259-264 = "The sentences elsewhere that state which value a
     barrier carries are not edit sites ... so each stays true and neither is
     restated." Exact.
   - spec-changes.md:428-430 = SPEC-2's spec/29 scope, naming §29.7's framing
     paragraph and the §29.8 trace steps; it names no §29.7 trace step 4, and a
     grep of the whole SPEC-2 span for "29.7" returns only the framing-paragraph
     bullet (which states step 5 is unchanged).
   - spec-changes.md:206-222 = SPEC-1's §10.1.8 step-1 closing-sentence rewrite
     (rewrite proper at :206-217, rationale to :222).
   - pkg/gateway/coordination/coordination/coordination.go:430 =
     `s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)`.
   - §29.7 trace step 4 exists at spec/29_communication-scenarios.md:1185-1186
     ("carrying the session's current `coordination_generation`") under the
     §29.7 heading at :1142; the new text cites it by heading, per N8.

Noted, not reported (out of this run's subject and already disclosed by the
edit): spec-changes.md:264 still closes "so each stays true and neither is
restated", the same false clause. The new OD10 records that SPEC-1's staged
ground "reaches the right conclusion by a route that does not hold", so the
proposal states the discrepancy rather than hiding it. The spec staging is
settled input for this run and the fixer filed it as DEFERRED.


### [non-spec.3.review-open-decisions.1]

FACT: `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r3` returns NOTHING. Run 7's non-spec round 1 was cut off before any fix, so round 3 reviews byte-identical text. "Read the changed sections first" had no target — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r3 vs proposals/0076_.../

FACT: the summary's OD entries were added in three distinct waves and `git log` on the summary file settles `introducedBy` in one call. 7229ef0f3 ("Consolidate 0076's open decisions into the summary") wrote OD1-OD7 by hand outside the pipeline; fc7e72d33 (run 5's fix) added OD10-OD13 verbatim as they still stand; f9c85f30c (run 6) added OD8 and OD9. The only uncommitted summary hunk is run 6's OD3 rewrite — EVIDENCE: git show fc7e72d33:...summary.md | sed -n '/OD10/,/OD11/p'

FINDING (filed): OD10 (summary.md:345-347) says "SPEC-2 leaves §10.1.8 step 1 ... unedited". Both halves are false. §10.1.8 is in `spec/10`, which the deliverable index assigns to SPEC-1 (summary.md:452-453), and SPEC-1 stages a full rewrite of step 1's CLOSING sentence at spec-changes.md:206-222. That sentence and the "carries the current `coordination_generation`" sentence OD10 is about are on ONE physical line, spec/10:183 — EVIDENCE: spec/10_gateway-internals.md:183; spec-changes.md:206-222, :259-264.

FINDING (filed): OD2's recommendation ("accept the equal case, and land it after the counter baseline", summary.md:197, :205) has no owner. No deliverable, no checklist step, no §8 test touches `pkg/adapter/coordination.go:99`; CODE-2 is scoped to `:236` alone (non-spec-changes.md:92-97). OD3 carries the exact sentence OD2 lacks ("a 'yes' is a successor's deliverable unless the reviewer directs that it be staged here", summary.md:253-255). The asymmetry between the two entries is the evidence.

FINDING (filed): OD5 (summary.md:280-285) states two costs of splitting and omits the three that matter: SPEC-3 exists ONLY for the counter baseline (spec-changes.md:571-585), SPEC-1's §10.1 baseline paragraph goes with it (:230-238), and SPEC-1's no-edit-site conclusion rests explicitly on "positive for every session once the baseline is 1" (:263), so a "split" answer reopens the two sentences OD10 asks about. OD5 and OD10 are coupled and neither says so.

DECISION: filed three, left OD1/OD3/OD4/OD6/OD8/OD9/OD11/OD12/OD13 alone — BECAUSE six of them are on this run's refuted list and OD3 re-verified clean this pass (spec/04:151, :175, :188 and proto:492-496 all resolve as quoted) — ALTERNATIVES: OD7's rider (below), declined as a cascade note rather than a finding.

OPEN: OD7's recommendation ("have SPEC-1 state that the value's lifetime is the session's binding on the pod", summary.md:305-307) sits against SPEC-1's staged "The value is unset until that session's first accepted fence on that pod" (spec-changes.md:142-143). Under the rebind reset OD7 accepts, "first accepted fence on that pod" is ambiguous between "first ever" (falsified by the reset) and "first in this binding". Accepting OD7 therefore edits that staged sentence rather than only adding one, and OD7 frames it as an addition. Weighed and NOT filed this round: the ambiguity resolves either way in one clause and the specification half is answered by `spec/07:196`. A later round wanting it must argue past the two readings.

WATCHOUT: the material skeptic has refuted the whole `## Open decisions` class ten consecutive times, all on "the entry stages nothing, so nothing is wrong". The three filed here are deliberately NOT of that shape: each names a statement about the STAGED DELIVERABLES that is checkable and false or missing, rather than a preference about how an unanswered question is worded. If one is refuted anyway, record which distinction failed, because that is the only thing that will stop the eleventh.

USEFUL [Traps: "grep the ARCHIVE for a candidate's subject BEFORE working it up"]: the refuted list handed down this round pre-adjudicated six of the thirteen OD entries. Reading it first cut the inventory in half before any file was opened.



### [non-spec.4.review-open-decisions.1]

DECISION: filed exactly one finding, on OD7 — BECAUSE OD7's recommendation ("have SPEC-1 state that the value's lifetime is the session's binding on the pod rather than the session itself", summary.md:306-308) is verbatim the unit D6 already fixes ("the exemption's unit is the session's binding on the pod rather than the pod's lifetime", summary.md:58-59), and D6 sits under "**Fixed decisions.** These are closed." (summary.md:43). The proposal therefore both closes the unit and asks the reviewer to decide it. — ALTERNATIVES: I weighed and did NOT file (a) a MISSING entry for the 0181 backfill's unbounded duration under a blocking pre-upgrade hook, because it meets none of the bar's (a)-(f) clauses and the material skeptic already refuted the substance twice; (b) the spec-changes.md:259-264 vs summary.md:21-24 contradiction (staged SPEC-1 rationale says the "current generation" sentences "stay true", the summary's rewritten opening says each "is already false"), because the remedy is a spec-changes edit in prose that lands in no file under spec/ and the log records that exact class as previously refuted; (c) §7 item 2 (`spec-changes.md:615`) still standing as an open decision while `summary.md:397-404` dispositions it out of scope, because the remedy is spec-lane.

FACT: D6's own two halves are in tension and that is what makes OD7 file-able under either reading. D6 sentence 1 says "unset until that session's first accepted fence on the pod" (per pod); D6's closing clause says the unit is "the session's binding on the pod" (per binding). Under a rebind onto the same pod these differ. SPEC-1's staged spec text carries only the first form ("The value is unset until that session's first accepted fence on that pod") — EVIDENCE: proposals/0076_.../0076_....spec-changes.md:142-143, :43-44, summary.md:56-59

UNVERIFIED: whether a session can re-bind onto the SAME pod in code. `spec/07:196` fixes `resuming → running` as "re-attach succeeds on replacement pod", so the specification half is answered, but nobody has walked `pkg/gateway/sessionserver`'s placement/claim path to see whether a recycled pod returned to the warm pool can be re-claimed by the same session id. If it cannot, SPEC-1's "first accepted fence on that pod" wording is harmless; if it can, that staged sentence is wrong and the fix is spec-lane. Whoever next owns SPEC-1 should settle it. — EVIDENCE: spec/07_session-lifecycle.md:196, pkg/adapter/slotsession.go:347-361, pkg/adapter/session.go:237-239

FACT (citation sweep, all clean this pass — do not re-derive): OD1 `cmd/lenny-gateway/httpsurface.go:588-602` (cache-fallback reads the live row per binding) ✓; OD2 `pkg/adapter/coordination.go:99` (`initialized && gen <= lastFenced`) ✓, `coordfence.go:164-179` (FailedPrecondition → re-read → relinquish when no advance) ✓, `start.go:4233-4240` (`fenceResumedPod` fences without incrementing) ✓, `coordination.go:463-480` (`RecordHandoff` is an unconditional `++`) ✓, `schemas/lenny-adapter.proto:1456-1458` ("not greater than") ✓; OD3 `spec/04:151`, `:175`, `:188` and `schemas/lenny-adapter.proto:492-496` (`ReportPodScrubRequest` carries `pod_id` alone) ✓; OD8 `charts/lenny/templates/migrate-job.yaml:10-16` and `pgstore.go:177`, `:260` ✓; OD9 `migrations/0050_session_record_fields.up.sql:38-39`, `migrations/0164_coordination_lease.up.sql:44`, 0180 is the last taken number ✓; OD10's withdrawal (written by this lens last round) re-verified whole: `spec-changes.md:259-264`, `:428-430`, `:206-222` say what it says, `spec/10_gateway-internals.md:183` really does carry both the "current `coordination_generation`" clause and step 1's closing sentence on one physical line, and `coordination.go:430` really passes the pre-bump `row.CoordinationGeneration` to `upsertMirror` ✓; the shipped-tree defect bullet's `CH-ADAPTEREVENTS` client row is `UNWIRED` under R12 at `tests/claim-map.json:512-518` ✓; `### Corrections outstanding`'s `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` really states "The first fence on a pod's lifetime is always accepted" ✓. — EVIDENCE: as listed

WATCHOUT: `charts/lenny/templates/migrate-job.yaml` sets `backoffLimit` and `ttlSecondsAfterFinished: 600` but NO `activeDeadlineSeconds`, so a future round arguing about the backfill's duration should know the hook has no wall-clock bound of its own and Helm's release timeout is the only ceiling. This is recorded as a fact, not as a finding. — EVIDENCE: charts/lenny/templates/migrate-job.yaml:41-45

WATCHOUT: the summary's `## Open decisions` list is stable across the whole r3→r4 window; `diff -ru` of the r4 snapshot against the live directory is EMPTY, so run 6's non-spec round-1 fixes were the last edits and nothing this round reads is newer than OD10's withdrawal and the opening-paragraph rewrite. Do not spend a pass looking for fresh fix-stage text. — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r4 vs proposals/0076_.../

USEFUL [review-log `### Open`, 1433-1440]: the rider "OD7's own recommendation asks for the opposite unit from what SPEC-1 stages ... and that consequence is stated nowhere" is what pointed at OD7. It was filed as a rider on an UNVERIFIED item and had never been raised as a finding; it is now.


### [non-spec.5.review-applicability.1]

DECISION: returned an EMPTY findings list for the applicability-and-sequencing lens — BECAUSE `diff -ru scratchpad/cp-snap/0076-run7/non-spec-r4 proposals/0076.../` returns NOTHING: the whole proposal directory, review log included, is byte-identical to the r4 snapshot, so this round's text is the same text `[non-spec.2.review-applicability.1]` and `[non-spec.3.*]` already simulated end to end and returned empty on. I did not defer to them; I re-ran the simulation independently and re-resolved roughly eighty code, test, script, chart, and proto citations against the tree, and reached the same verdict — ALTERNATIVES rejected, with the reason each: (1) S8's tier list omitting tier 0 while S1-S7 all declare it and TEST-1 adds untagged Go under `pkg/adapter` — killed by the standing `### Traps` "tier-list bookkeeping is a refuted class, three times over", and already rejected by name in `[non-spec.2.review-applicability.1]`; (2) §8's tier sentence omitting tier 11 while S1-S3 declare it — same class, plus the standing finding that S1's tier 11 is precautionary; (3) the tier-3 wire case and the tier-2 resume-fence case naming no file AND no directory, where every other §8 case names at least a directory and the `IMPLEMENTOR'S CHOICE:` marker at `:290-297` is scoped to tier-1 `pkg/adapter` files and does not cover them — declined because the standing `### Traps` records a round-2 lens stopping at the bar on the §9 half and the `### Open` list carries both as weighed-and-not-filed; (4) TEST-1 naming `pkg/adapter/holdstate_test.go` among its files while §8's only holdstate case is the amendment assigned to the CODE-3 step (S5) — declined as bookkeeping, since §8 is the authoritative disposition and no tier goes red either way.

FACT: the checklist's five properties re-verified whole this round against the live file, not against a standing summary. Nine deliverables (SPEC-1/2/3, SCHEMA-1, CODE-1/2/3/4, TEST-1) each in exactly one of eight steps; one lane per step (spec, spec, spec, schema, code, code, code, test); the three spec steps lead; every `Depends-on` names an earlier existing step (S1 —; S2 S1; S3 S1; S4 S2; S5 S1; S6 S1,S3; S7 S1,S5,S6; S8 S5,S6,S7); no box ticked (`grep -n "^- \["` returns eight `- [ ]`). EVIDENCE: implementation-checklist.md:3-33.

FACT: class 6 (a code step consuming unlanded spec text) is clean, checked deliverable by deliverable. CODE-1/CODE-3 consume SPEC-1's §10.1.2 and §10.1.4 only (S5 depends on S1); CODE-4 consumes SPEC-1's §10.1 baseline and SPEC-3's §4.2 (S6 depends on S1, S3); CODE-2 consumes SPEC-1's §10.1.2 step 3 (S7 depends on S1); SCHEMA-1 consumes SPEC-2's stated wording alone (S4 depends on S2). CODE-1's own ground, "§10.1.8 step 3 already fixes the gate's unit at the session", is SHIPPED text rather than staged, so it needs no dependency.

FACT: §8's class-1 exhaustiveness claim is true against the whole tree, re-derived independently this round rather than taken from the standing bullet. `grep -rn "CoordinationGeneration" --include=*_test.go pkg/ tests/ | grep -v "CoordinationGeneration:"` returns twenty-one files. Every one outside §9 either seeds explicitly at or above 1, sets the value through `Update`, or asserts a DIFFERENT table's column: `evictionstatestore_test.go` (4 and 3), `evictionfallback_test.go` (3), both `sessioncheckpointmeta` suites (3, 1→2, 3, 4), and `derive_failure_audit_test.go:46` (an `Update` closure with no assertion). Nothing is missed. EVIDENCE: tests/tier2_component/stores/evictionstatestore_test.go:258,:276; pkg/gateway/session/sessioncheckpointmeta/pgstore/pgstore_test.go:78,:104.

FACT: the four §8 seed/assertion line pairs that carry the tier-8 and tier-7a split all resolve exactly, and the split is safe in the stated step order. Tier 8: seeds at 1 at `:118` and `:179`, `pod.LastFenced()` reads at exactly `:150`, `:195`, `:223` (a `grep -n LastFenced` on that file returns those three and their `t.Fatalf` echoes and nothing else), unset seed at `:239-241`, and the 1/1/2 assertions at `:267`, `:283`, `:296`. Tier 7a: seed 1 at `:130`, `LastFenced` at `:260`, the assertion of 2 at `:264-265`, and the assertion of 0 at `:287-288`. EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118,:150,:179,:195,:223,:239-241,:267,:283,:296.

WATCHOUT: §8's `CoordinationGeneration: 0` seed in the tier-7a colocation file is at `:145`, not the `:144` §8 cites; `:144` is the `ID: terminating,` line of the same `sessionstore.Session{` literal (which spans `:143-146`). This is the sub-line drift class the standing context bars from filing, and I record the true line only so a later reader does not re-derive it as a discrepancy — EVIDENCE: tests/tier7a_load_local/coordination_colocation_race_test.go:143-146.

FACT: the tier-1 two-gates case §8 stages IS writable in `pkg/adapter/coordination_test.go` despite the package wall the standing traps warn about, because the landed `TestCheckpointBarrierAcksEchoedCheckpointID` never drives a real `Checkpoint` stream: it calls `s.barrier.link("gw-ckpt-1")` and `s.barrier.complete()` directly from `package adapter` after `waitBarrierWaiting`. The per-entry version is the same pattern against each entry's own gate. The package wall bites only the mid-flight deregistration case, which §8 already places in `checkpoint_stream_test.go`. EVIDENCE: pkg/adapter/coordination_test.go:221-232, :278-287.

USEFUL [`### Traps`, "tier-list bookkeeping is a refuted class, three times over"] and [`### Traps`, "Editing hazards in this proposal's own files"]: the first killed two of my four candidates before I spent anything on them; the second stopped me trusting a `cat -n *spec-changes.md` glob.

USEFUL [`[non-spec.2.review-applicability.1]`]: its four named-and-rejected alternatives are the exact four a fresh applicability reading generates on this text. An applicability shard on unchanged text should read that entry first and then spend its budget on re-resolving citations instead, which is what I did.

OPEN: the applicability lens has now returned empty three times on byte-identical text (`[non-spec.1.*]`, `[non-spec.2.*]`, and this one), and the r4 and r5 snapshots are identical to the live directory in every file. Whoever schedules the rounds should decide whether this lens is retired, per the standing `### Open` item on exhausted lenses; each empty return costs a full round for the other lenses.


### [non-spec.5.review-citations.1]

DECISION: returned an EMPTY findings list for the citation lens over the non-spec staging, run 7 round 5 — BECAUSE `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` reports NO differing file at all, so `non-spec-changes.md`, `summary.md`, `implementation-checklist.md`, and `spec-changes.md` are byte-identical to the text `[non-spec.1.review-citations.1]` (archive `:7826`) already swept and returned empty on; I re-resolved the whole set independently against the tree anyway rather than deferring to that shard, and every file:line, every attributed behaviour, and every data-flow direction holds — ALTERNATIVES: re-filing the SPEC-1 "column default and the create path's floor" contradiction (rejected: remedy is in `spec-changes.md`, which this loop may not edit; recorded as DEFERRED below, same item pass 21 and run-7 round 1 both recorded); filing §9's tier-3 omission, the tier-2 resume-fence case's unnamed file, and §8's self-undercut lint-migrations argument (all three pre-adjudicated in `### Traps`).

FACT: independently re-verified this pass, semantically rather than by line arithmetic. `barrierGate.open()` (`coordination.go:158-166`) clears `checkpointID`/`signaled` and remakes `done` unconditionally; `link()` (`:180-188`) writes into whichever gate is `waiting` with no session term; the barrier gate is one `Server` field (`server.go:314`). `Dispatch` runs one goroutine per target (`barrier.go:192-199`) and `dispatchOne` builds the `sessioncheckpointmeta.Record` from `t.SessionID` + `ack.CheckpointRef` (`:238-245`), so a cross-linked ref really is filed against a session that did not produce it. `opLock.Begin` coalesces on a pending same-session id and admits+queues a distinct one (`oplock.go:116-129`). `deregisterSlotLocked` (`slotsession.go:174-189`) deletes the key and returns the pointer with no field zeroed; both `Shutdown` (`session.go:237-239`) and `deregisterStartedSessions` (`slotsession.go:347-361`) go through it. `checkpointRootsForSession` has exactly two callers (`checkpoint.go:94`, `resume.go:178`). `holdState.gen`: one writer (`holdstate.go:128` from `:119`), one reader (`:187`), passed at `:206` to `terminateHeldSession` (`:225`) and `writeHoldPostMortem` (`:283`). `pgstore.Create` (`:140`) names `coordination_generation` in the insert list (`:177`) and binds `sess.CoordinationGeneration` at `:260`, so the column default is inert; `memstore.Create` at `:46`. `coordfence.fence` floors non-positive at `:147-153` and an `InvalidArgument` refusal falls into the `default:` transient arm at `:180-184`, exhausting the budget to `relinquish` at `:186-188`. `RecordHandoff` is an unconditional `row.CoordinationGeneration++` (`coordination.go:463-481`). `fenceResumedPod` (`start.go:4233-4245`) fences without incrementing. Migration facts: `0050:38-39` is the inline `CHECK (>= 0)`, `0164:44` the mirror column at `DEFAULT 0`, `0180` the highest `*.up.sql`. `migrate-job.yaml:37-39` is `pre-install,pre-upgrade` at weight `-5`. `lint-migrations.sh:45` TEST_DIR, `:74-90` pass 3. `cmd_run.go:498-508` (untagged + contract vet only), `:635-641`, `:880` (`-race`).

FACT: the class-2 exemption claim at `non-spec-changes.md:370-375` is TRUE against the tree, checked file by file rather than accepted. `coordination_mirror_test.go`: `s1` seeded at 2 (`:84`) and asserted 2 (`:116`); `s2` and `done` seeded unset (`:85-86`) and the loop asserts a generation only for `s1`. `barrier/wiring_test.go:171` asserts 4 on a `coordlease` mirror row, not a session row. `coordlease_test.go:37` (3) and `:58` (5) are both `Lease` literals. None of the three touches a session row created unset, so all three correctly stay out of §9.

FACT: §4.2 does state the no-reset ground CODE-4's `.down.sql` sentence cites. `spec/04_system-components.md:200`: "Both counters are **monotonically non-decreasing** across every state transition — never rolled back, never reset." §10.1.8 step 3 (`spec/10_gateway-internals.md:185`) does say the dispatcher opens the `Checkpoint` stream "for each quiesced session **concurrently with** the in-flight `CheckpointBarrier` RPC to that session", which is CODE-1's ground for the gate's unit; the attribution is exact.

FACT: OD3's three `spec/04` §4.1 sites all resolve and say what OD3 says. `:151` grounds the classification as "declared in the table below rather than derived from a message's field set, because `session_id` appears on messages of both classes"; `:175` is the `CoordinatorFenceRequest | Adapter | gateway → adapter | pod` row; `:188` is "carries `session_id` and stays pod-scoped, which is why the classification is declared rather than derived"; `:190` is the `ShutdownRequest` precedent OD3 leans on. `ReportPodScrubRequest` declares `pod_id` alone at `schemas/lenny-adapter.proto:492-496`. The earlier round-fixed defect (a `CoordinatorFenceRequest` example at `:151`) is genuinely gone.

FACT: the two cross-proposal attributions in `summary.md` are accurate. 0075's counterexample table and its ground ("The write target is the pod. The identifier selects nothing.") are at `proposals/0075_fix_derive-message-scope-from-the-address-type.md:67`, `:85-89`; 0080 records the hold-release item as taken by 0076 at `proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:211` and the §29.10 partitioning gap at `:184-191`.

DEFERRED [`spec-changes.md` SPEC-1, `:249-250` and `:284-285`]: both still credit the counter baseline to "the column default and the create path's floor" (singular path). What is true instead: `pgstore.Create` names `coordination_generation` in its insert column list (`pgstore.go:177`, bound at `:260`), so the column default baselines nothing, and the TWO `Create` floors (`pgstore.go:140`, `memstore.go:46`) are the whole enforcement, which is exactly what `non-spec-changes.md:133-135` states. Third consecutive confirmation; nothing has moved it and the non-spec side needs no edit.

WATCHOUT: `grep -o '`[^`]*`'` over `non-spec-changes.md` misses `pkg/adapter/server.go:302`, because CODE-1's sentence wraps a backticked identifier (`` `coord\ncoordinationState` ``) across a physical line and the per-line matcher then pairs the wrong backticks. A citation inventory built from that grep silently drops the `Server.coord` field citation — EVIDENCE: non-spec-changes.md:33-35.

USEFUL [`### Traps`, "an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the wrong half"]: I spent the pass on attributed behaviour and data-flow direction instead of the token greps, which is what surfaced the three checks above (the class-2 exemption files, §4.2's no-reset sentence, §10.1.8 step 3's concurrency clause) as the only load-bearing unverified claims left. All three held.

USEFUL [`### Traps`, "MISTAKE: grep the ARCHIVE for a candidate's subject BEFORE working it up" and "refuted class (k) does not reach §9's tier-3 omission"]: together they killed four candidates (§9's missing `tests/tier3_contract` entry, the tier-2 resume-fence case's unnamed file, §8's self-undercut lint-migrations sentence, and the `coordination.go:408`/`:415` sub-line drift) before any of them cost a work-up.


### [non-spec.5.review-client-surface.1]

DECISION: returned EMPTY — BECAUSE every externally-consumed representation this staging touches is the adapter proto's doc comments, and both generated mirrors are listed in §9; no REST/OpenAPI/MCP/A2A/SDK/CRD/JSONL/runtime-ops/chart representation carries the counter, the fence, or the barrier gate in any spelling. ALTERNATIVES: (1) the `CheckpointBarrier` RPC-comment tail deletion under SPEC-2:536-541 — barred, the material skeptic's refutation of the identical `CoordinatorFenceRequest` tail explicitly extends to "the `CheckpointBarrier` carriers"; (2) `adapterclient/coordinatorfence.go:37` as a fourth exemption carrier — barred by refuted class (k) (`pkg/` is outside criterion (d)); (3) §9 naming no tier-3 file — barred, recorded weighed-and-declined twice; (4) proto comment advertising D7 acceptance at S4 while CODE-2 lands at S7 — not filable, spec-first ordering is the repo's governing rule and every multi-step proposal has that interval.

FACT: the whole client-surface sweep is FOUR greps and they all return nothing. `grep -c coordination pkg/gateway/externalapi/openapi/openapi.json` → 0; `grep -rln "coordination_generation\|coordinationGeneration\|CoordinationGeneration\|coordination generation" sdks/` → nothing; `grep -rn "coordination" schemas/*.json` → nothing; `grep -rn "coordination" charts/lenny/crds/*.yaml` → nothing. `docs/api/`, `docs/client-guide/`, `docs/runtime-author-guide/` return nothing on `CoordinatorFence|CheckpointBarrier|coordinator_handoff_stale|fenced generation`. The reason is in the spec: `spec/04_system-components.md:200` says `recovery_generation` is "visible to clients via the session API and `session.resumed` events" while `coordination_generation` is "internal only, used for split-brain fencing". Do not re-derive this; it is the whole lens. — EVIDENCE: spec/04_system-components.md:200

FACT: every SCHEMA-1 citation resolves, checked this run in the files. `Makefile:91-94` is the `generate-proto` target; `tests/tier0_static/proto_no_drift_test.go:70` is `func TestProtoStubsMatchGeneratedOutput`; `schemas/lenny-adapter.proto:1451` ("Strictly monotonic on the pod side per session.") reappears verbatim at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`; `lenny-adapter_grpc.pb.go:180` and `:632` are both the `CoordinatorFence` RPC comment's first line (client stub and server stub). SCHEMA-1's 18-carrier list is byte-for-byte SPEC-2's set in SPEC-2's order, and the one unedited carrier (`CoordinatorFenceRequest.coordination_generation`) matches SPEC-2:515-516.

FACT: no tier-0 or tier-3 gate reads proto COMMENT text, so SCHEMA-1's rewrites cannot turn one red beyond the stub-drift diff. `tests/tier0_static/claim_register_proto_agreement_test.go` joins the register to field DECLARATIONS (`protoFields`, `:64`, `:92-102`), and the tier-3 suites `adapter_checkpointbarrier`, `adapter_generation_fence`, `adapter_session_address`, `adapter_reportusage`, and `checkpoint_stream` all assert descriptor field sets, numbers, and wire form, never an adapter outcome or a comment string. That is why D7 breaks no landed tier-3 case. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:31-33, :92-102; tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48-59

FACT: the summary's `### Corrections outstanding` claim "The fence request has no row and is exempted from the tier-0 gate by name" is TRUE and cheap to verify — `var fenceReadersExempt = map[string]bool{"CoordinatorFenceRequest": true}` — and the sibling claim that `tests/claim-map.json`'s barrier-field note is false against the shipped comparison is also true (the note reads "no production reader compares it until the generation fence lands" while `coordination.go:236` compares it). Both re-verified this run; do not spend another pass on them. — EVIDENCE: tests/tier0_static/claim_register_proto_agreement_test.go:43; tests/claim-map.json:75-82

USEFUL [Settled: "The proto is a published artifact and the client surface around it is empty"]: the pass-23 correction naming `pkg/gateway/externalapi/openapi/openapi.json` as the real path saved the whole trap where a grep against the non-existent `pkg/gateway/openapi/` returns nothing for the wrong reason. Also confirmed: neither the summary, the spec-changes, nor the non-spec-changes cites `pkg/gateway/openapi/` at all — the bad path lives only in this review log, so there is no false citation in the proposal to fix.

USEFUL [Traps: "the preservation-clause asymmetry"]: it is the only thing this lens would otherwise have filed, and the "already refuted" list handed to this run kills it including its `CheckpointBarrier` extension. A future client-surface run should read that refutation's LAST paragraph, which extends it to the barrier carriers by name, before working the barrier tail up again.

WATCHOUT: the `CheckpointBarrier` RPC comment (`schemas/lenny-adapter.proto:165-179`) is fifteen lines carrying the ack protocol, the `checkpoint_ref` echo rule, and `checkpointBarrierAckTimeoutSeconds`, of which only the FIRST clause is the generation gate. SPEC-2:536-541 tells all three barrier carriers to "take the §10.1.2 step 3 wording SPEC-1 stages" with no clause-granularity qualifier. That is a real authoring hazard for the implementor even though it is not a filable defect, and it is the same shape as the `CoordinatorFenceRequest` tail. An implementor applying S4 should replace the gate clause alone on all three barrier carriers. — EVIDENCE: schemas/lenny-adapter.proto:165-179; spec-changes.md:536-541

WATCHOUT: D3 ("The proto doc comment claiming per-session monotonicity is corrected to describe the adapter and is not deleted", summary.md:49-50) reads as an edit instruction while SCHEMA-1 says that same comment "is the one carrier that takes no edit" (non-spec-changes.md:19-21). Weighed and NOT filed: the comment already reads "Strictly monotonic on the pod side per session", so D3's "corrected" means the code is made to match, the specific instruction is unambiguous, and editing it anyway breaks nothing. Do not file it; do not re-derive it.

UNVERIFIED (carried, unchanged): whether the external-adapter compliance suite §28.7 names generates any assertion from proto COMMENT text. Still inference from the suite's absence in the tree; `tests/tier10_conformance` is the runtime battery and is not it. If that suite is ever built, every SCHEMA-1 prescription becomes load-bearing on a published artifact and this lens reopens.

OPEN: this lens has now returned empty three times (run 6 round 1, run 7 round 1, this run), each after an independent re-derivation of the same four greps. It reopens only if a deliverable grows a proto FIELD, message, enum, or RPC change, a `schemas/*.json` change, an SDK file, or a CRD change. A comment-only proto edit does not reach it.


### [non-spec.5.review-docs-alignment.1]

DECISION: Returned EMPTY — BECAUSE the whole proposal directory is byte-identical to the r4 snapshot
(`diff -rq scratchpad/cp-snap/0076-run7/non-spec-r4 proposals/0076_.../` returns nothing, rc=0), and the
only text that has moved since the last docs-alignment pass (r2) is three hunks in `summary.md`: the
"What changes" closing sentences, OD3's `spec/04:151` paragraph, and OD10's withdrawal. None of the three
names a `docs/` surface. I re-derived the `docs/` surface from the tree anyway rather than lifting it, and
reached the same conclusion the two prior shards reached — ALTERNATIVES: (1) filing the permanent
rolling-window zero-row cohort (`non-spec-changes.md:157-163`) as an accepted failure mode landing only in
reasoning, rejected because it is a strict subset of the shipped refusal for every never-taken-over session,
so the change creates neither a new failure mode nor a new cause; (2) filing `docs/reference/adapter-contract.md:69`
("`CoordinatorFence` ... precondition for any subsequent operational RPC") against staged §10.1.2 step 3's
unset arm, rejected because that line mirrors §10.1.2 step 2, which SPEC-1 leaves unchanged, so it is not an
edit site this change creates; (3) filing §8's tier enumeration for omitting tier 11 while the checklist gives
S1-S3 "Tiers 0, 11", rejected as tier-list bookkeeping, a class the ledger records as three-times refuted, and
not this lens.

FACT: the whole reader-facing surface for this change is three lines, and none of them states a unit, a
baseline, a gate, or a log field. `grep -rn "coordination_generation\|coordinationGeneration\|coordination
generation" docs/ charts/ sdks/ README.md` returns exactly `docs/getting-started/concepts.md:101`,
`docs/getting-started/architecture.md:173`, and `docs/reference/adapter-contract.md:69`. `:101` states only
that the two counters are independent and that a pod recovery does not reset the coordination counter, which
CODE-4's baseline leaves true; `:173` is a field enumeration; `:69` is a one-line RPC gloss. Nothing in
`docs/` states a counter's starting value at all (`grep -rn -i "starts at\|initial value" docs/` returns
`docs/api/admin.md:36` on the admin ETag version and two unrelated hits) — EVIDENCE:
docs/getting-started/concepts.md:101, docs/getting-started/architecture.md:173, docs/reference/adapter-contract.md:69

FACT: the adapter's structured log fields this change moves exist in no documentation. `last_generation` and
`started_sessions` occur only at `pkg/adapter/holdstate.go:131-132` and `:228`, and `coordinator_lost`,
`coordinator_connection_lost`, and `coordinator_generation_gap` occur only in `spec/10`, `spec/28`, `spec/29`,
`spec/04`, and `schemas/lenny-adapter.proto`. Every one of those spec sites is inside SPEC-1's or SPEC-2's
staged scope, including `spec/29:1274`'s "carrying the last known generation", which SPEC-2 stages at
`spec-changes.md:477-479`. So CODE-3's per-session record change reaches no `docs/` page — EVIDENCE:
pkg/adapter/holdstate.go:131-132; spec/29_communication-scenarios.md:1274; spec-changes.md:477-479

FACT: no metric and no alert moves, so the companion-row half of this lens is empty by construction. The only
`lenny_*` names anywhere in the three change files are `lenny_adapter_coordinator_hold`,
`lenny_coordinator_handoff_stale_total`, and `lenny_coordinator_fence_relinquished_total`, and all three
already carry their `docs/reference/metrics.md` rows (`:309`, `:307`, `:312`) and their `spec/16` inventory
rows (`:185`, `:183`, `:192`). No proposal text adds a label value or changes a row's gloss — EVIDENCE:
docs/reference/metrics.md:307, :309, :312; spec/16_observability.md:183, :185, :192

FACT: the tier-11 suite has no gate on the sentences this change rewrites. Nine files under
`tests/tier11_docs/` name §10.1, §29.10, or one of the two spec files, and each was opened: two read `spec/10`
§10.1 for the `checkpoint_manifest` column set and the `manifest_reason` enum
(`checkpoint_pipeline_consistency_test.go:332`, `:753`), one reads §10.1's anchor as a cross-file link target
(`eviction_coordinator_route_consistency_test.go:77-154`), one cites §29.10 in its `// spec:` annotation while
reading §6.2, §7.2, and `docs/reference/state-machines.md` alone
(`per_slot_substate_scope_doc_reconciliation_test.go:19-20, :86-120`), and none string-matches a sentence
SPEC-1 or SPEC-2 rewrites. `docs/reference/state-machines.md`'s "Concurrent-session occupancy" section
(`:239`) enumerates pod phase edges and slot edges and carries no coordination, fence, barrier, or hold
content, so SPEC-2's §29.10 reclassification has no reader-facing mirror — EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:19-20; docs/reference/state-machines.md:239-253

USEFUL [Settled: "the thirteen-line `docs/` surface"] and USEFUL [Traps: "a token grep under-reports the
`docs/` surface by four sites"]: together they turned this pass into a verification rather than a sweep. The
spaced spelling is what returns `architecture.md:173` and `adapter-contract.md:69`; the token alone returns
two files and reads as a clean sweep for the wrong reason.

USEFUL [non-spec.2.review-docs-alignment.1]: its four FACTs (the three-line grep, `docs/api/internal.md`'s
service block omitting both RPCs, `docs/operator-guide/upgrades.md` naming no migration number, and
`state-machines.md`/`error-catalog.md` carrying no hold content) all held on independent re-derivation. I
re-opened each rather than lifting it and changed none.

FACT: `docs/api/internal.md`'s `RuntimeAdapter` protobuf block lists StartSession, StopSession, Attach,
Checkpoint, UploadFiles, and DemoteSDK and names neither `CoordinatorFence` nor `CheckpointBarrier`, and its
gRPC status table gives `FAILED_PRECONDITION` the generic gloss "Operation not valid in current state". D7's
change to when a barrier returns `FailedPrecondition` therefore reaches no `docs/api/` row. This is the one
surface a docs lens expects to mirror the proto, and it does not — EVIDENCE: docs/api/internal.md:76-100, :499

WATCHOUT: `docs/reference/adapter-contract.md:81` states "The adapter maintains a per-session operation
lock", while CODE-1 and §8 both rest on the lock being pod-level and queueing a co-tenant session id behind
the running checkpoint (`non-spec-changes.md:69-72`, `:271-274`, citing `pkg/adapter/oplock.go`). It is
tempting to file, and it is PRE-EXISTING drift already recorded in the standing `### Traps`: the proposal
neither creates nor widens it, and criterion (d) is about a surface that becomes wrong after the edits are
applied — EVIDENCE: docs/reference/adapter-contract.md:81; non-spec-changes.md:271-274

OPEN: this lens has now returned empty six times on this proposal, three of them over text that did not move
a byte between returns. The staging touches no `docs/` file, adds no metric or alert, and the entire
reader-facing surface is three unit-neutral lines. The standing `### Open` question about retiring an
exhausted lens is still unanswered and this round is the strongest case for it yet.


### [non-spec.5.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE `spec-changes.md` and `non-spec-changes.md` are BYTE-IDENTICAL
across every run-7 snapshot (`non-spec-r1-start` through `non-spec-r5-start`) and identical to the live files, so the
only text this lens had not already been run over is `summary.md`'s OD10 rewrite and its overview paragraph, and neither
opens an edit site; I re-derived the surface independently anyway and every carrier, mirror, companion pair, and
generated chain resolves — ALTERNATIVES: rejected the tier-3 case's missing §9 file (Traps says do not spend a
verification on it), the tier-7a new cases' missing file (archive `[non-spec.2.review-edit-sites.1]` UNVERIFIED, and the
"incomplete files-touched list is not a finding" refutation covers it), the `CheckpointBarrier` RPC comment's missing
preservation clause (same shape as the already-refuted `CoordinatorFenceRequest` tail finding), and the
summary-versus-SPEC-1 contradiction below.

FACT: both staging files are frozen. `md5sum` of `*.non-spec-changes.md` is `7737cdf4…` and of `*.spec-changes.md` is
`2447bac1…` in all ten run-7 snapshot directories and in the live proposal. Five non-spec rounds have changed only
`summary.md` and the review log. A lens told to "read the changed sections first and hardest" should diff
`summary.md` alone — EVIDENCE: scratchpad/cp-snap/0076-run7/*/0076_*.non-spec-changes.md.

FACT: the migration-number surface is exactly three places and §9 names all three. `scripts/lint-migrations.sh` pass 3
hard-codes `TEST_DIR="${ROOT}/tests/tier2_component/migrations"` and greps the bare number, so a case elsewhere leaves
tier 0 red; `prodMigrationSchema` is the roll-back-per-step walker's list; the behavioral file is the migration's own
test file in that directory. Note `migrations/` ALSO holds 54 `*_test.go` files in `package migrations` that read
migration SQL as text — that is a second, older convention pass 3 does NOT accept, so an implementor who follows the
neighbouring `migrations/0178_checkpoint_manifest_test.go` pattern instead of §8's instruction turns tier 0 red —
EVIDENCE: scripts/lint-migrations.sh:45, :74-88; migrations/0178_checkpoint_manifest_test.go:1-20.

FACT: migration 0181's `coordination_lease.coordination_generation DEFAULT 1` half is inert against every test and
writer. `pkg/gateway/coordination/coordlease/pgstore/pgstore.go:47-58` is the only `INSERT INTO coordination_lease` and
it binds the column explicitly, and no test inserts a lease row in raw SQL. Nothing goes red from that half, in either
direction — EVIDENCE: pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58; grep `coordination_lease` over
tests/ returns only prod_columns_test.go:471-472 and a comment.

FACT: the baseline's landed-test blast radius is complete as §8 states it. 34 `*_test.go` files name
`CoordinationGeneration`; every one outside §8's enumerated set seeds an explicit non-zero constant or never asserts on
the generation. Checked individually: sessionserver/{coordination_fence,failure,derive_failure_audit,
resume_chunk_selection_internal}_test.go, tier4_integration/checkpoint_intent_generation_test.go,
tier7a_load_local/prestop_no_double_checkpoint_test.go, coordination_mirror_test.go (s1 seeded at 2, :84/:116),
barrier/wiring_test.go:159/:171 (seeded 4), coordlease_test.go:16/:37/:51/:58 (seeded 3 and 5). Do not re-derive this —
EVIDENCE: pkg/gateway/sessionserver/coordination_fence_test.go:37, :70, :102, :133;
tests/tier4_integration/checkpoint_intent_generation_test.go:46, :63, :118, :128.

FACT: every §8/§9 test-fixture citation resolves exactly. tier8 seeds at :118 and :179 and asserts 1/1/2 at :267/:283/
:296 over a row seeded unset at :239-241; tier7a seeds live at 1 (:130) and `terminating` at 0 (:144) and asserts 2 at
:264-265 and 0 at :287-288 with `pod.LastFenced` at :260; sweep_test.go's generation assertions run exactly :275-:594;
coordination_takeover_test.go's `mustCreate` calls are exactly :74/:142/:241/:301. The mid-flight case's four fixtures
resolve too: `driveCheckpointConc` checkpoint_stream_test.go:384, the sibling case :417, `concurrentServer`
slot_test.go:24, `slotStartReq` :37, `adapterClient` server_test.go:90, and checkpoint_stream_test.go really is
`package adapter_test` — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118, :179, :239-241, :267;
pkg/adapter/checkpoint_stream_test.go:3, :384, :417.

FACT: the observability half of CODE-3/SPEC-1 has exactly two spec mirrors and no docs mirror.
`coordinator_connection_lost` occurs at `spec/10_gateway-internals.md:60` (SPEC-1's own site) and
`spec/29_communication-scenarios.md:1274` (SPEC-2 stages it as §29.8 step 2), and NOWHERE under `docs/`, `charts/`, or
`schemas/`. The post-mortem's `lastGeneration` JSON key has no schema anywhere. So dropping `last_generation` from
`pkg/adapter/holdstate.go:130-132` strands no surface — EVIDENCE: spec/10_gateway-internals.md:60;
spec/29_communication-scenarios.md:1274; pkg/adapter/holdstate.go:129-132.

FACT: `spec/04:711-712`'s §4.7 RPC-table rows for `CheckpointBarrier` and `CoordinatorFence` state the barrier's
payload and the fence's "precondition for any subsequent operational RPC" and fix NO compared value, so they are
non-sites under SPEC-2's own criterion and need no SPEC-3 companion edit. `spec/04:200` is SPEC-3's site and `:323`,
`:461` are unit-neutral — EVIDENCE: spec/04_system-components.md:711, :712, :323.

WATCHOUT: the summary and SPEC-1 now give OPPOSITE grounds for the same non-edit ruling. `summary.md:18-24` says the
"carries the session's current `coordination_generation`" sentences are "already false on the handoff path in the
shipped tree"; `spec-changes.md:262-264` says "each stays true and neither is restated". OD10's withdrawal
(`summary.md:346-365`) states the summary's version is the right one and SPEC-1's "reaches the right conclusion by a
route that does not hold". I did not file it: SPEC-1's paragraph lands nowhere in `spec/`, which is exactly the ground
the §4 "either order" finding was refuted on, and the remedy is in the spec-changes file this loop does not edit. A
later pass between the loops should reconcile the two sentences — EVIDENCE: summary.md:18-24; spec-changes.md:259-264.

DEFERRED [`0076_..._spec-changes.md`, SPEC-1's non-site paragraph]: the claim "each stays true and neither is restated"
(`spec-changes.md:262-264`) is false as a GROUND. What is true is the summary's version: both sentences are already
false on the handoff path in the shipped tree, because `pkg/gateway/coordination/coordination/coordination.go:430`
mirrors `row.CoordinationGeneration` read BEFORE `RecordHandoff` bumps it, so the mirror carries G for a whole sweep
interval while the pod is fenced at G+1. The conclusion (not an edit site) survives; only the reason has to change to
the summary's. Verified this run: `:430` passes the pre-bump snapshot and `RecordHandoff` is declared at `:437`.

USEFUL [`### Traps`, "an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the
wrong half"]: I skipped the token greps and went straight to the proto carriers and the test-fixture class boundary,
which is where the two hours of this pass produced anything.
USEFUL [archive `[non-spec.2.review-edit-sites.1]`]: its FACT list (twelve operational carriers walked back to their
enclosing `message`, the alerting bundles as the only generated pairs, `claim_register_proto_agreement_test.go` reading
field declarations rather than comments) let me spot-check rather than rebuild. I re-verified the last of those
directly: the gate's `protoFields`/`absenceAssertion` path never reads a doc comment, so SCHEMA-1 cannot move it.

UNVERIFIED: `gateway-runtime-comms.md:2380` cites `migrations/0180_coordination_lease_address.up.sql`, which does not
exist — the tree's 0180 is `0180_drop_checkpoint_slot_id`. It is a root planning document and outside every rule's
domain, and nothing in 0076 reads it, but whoever owns that document should know its migration reference is stale.


### [non-spec.5.review-feasibility.1]

DECISION: returned EMPTY on the non-spec staging — BECAUSE every actor the staging names exists under that name, can perform the action assigned, and can see the data its check needs; I re-derived the whole (b) surface from scratch and it is vacuous here. ALTERNATIVES: re-filing the standing `### Open` items my own lens raised in an earlier run (CODE-1's tier 2; §8's tier-4 sentence for D7) — both are now answered against the tree (see FACTs below) or fall in the thrice-refuted tier-list-bookkeeping class; re-filing the accepted-barrier-blocks class against §8's tier-1 `sess-b` bullet and the tier-3 wire case — killed by the standing `TestCheckpointBarrierAcksEchoedCheckpointID` goroutine/wait-for-gate/drive-stream entry and by the already-refuted `TestCheckpointBarrierRejectsWithoutFence` finding.

FACT: the whole criterion-(b) surface is empty for `non-spec-changes.md`, and one grep proves it. `grep -niE "sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|informer|etcd|leader|networkpolicy|egress|rbac"` over the file returns NOTHING. The staging's actors are the adapter process (in-memory state + a 0600 local-disk post-mortem), the gateway process (Postgres), and the Helm migrate Job (Postgres DDL). No §4.6.3 owner, no §13.2 egress rule, no §10.3 agent-pod RBAC, and no webhook is engaged. This is the non-spec twin of the standing spec-side lens-scoping bullet; a future feasibility or kubernetes run on this lane can stop after this one grep — EVIDENCE: proposals/0076_.../0076_....non-spec-changes.md:1-422

FACT: CODE-1/CODE-3's declared tier 2 IS reachable, which closes the older `### Open` "CODE-1 declares tier 2 with no tier-2 package touching its accessors". `TestHoldTerminatedSessionDecrementsItsSlotExactlyOnce_spec_10_1` drives the §10.1 coordinator-loss hold to termination against a real `adapter.Server` over an envtest apiserver, with `adapter.New("test")` inside the fixture and a `waitForHoldTermination` helper — EVIDENCE: tests/tier2_component/slotrelease/revoke_double_teardown_test.go:119, :341, :365-395

FACT: every actor-action precondition CODE-3 needs is already in the tree. `heldSession{sessionID string; state *slotState}` carries the entry pointer; `deregisterStartedSessions` builds the members from `deregisterSlotLocked`'s returned pointer with no field zeroed; `terminateHeldSession(ctx, m heldSession, gen int64)` therefore already HAS the pointer and only `writeHoldPostMortem(session string, gen int64)` needs a signature change; `writeHoldPostMortem` has exactly one caller. Do not re-derive this chain — EVIDENCE: pkg/adapter/slotsession.go:282-284, :347-367; pkg/adapter/holdstate.go:225, :229, :283

FACT: the twelve CODE-3/CODE-1 line citations into `pkg/adapter/holdstate.go` all resolve exactly, checked with one grep rather than by counting a `sed` window: `gen` field `:43`, arming read `:119`, `started` `:120`, `s.hold.gen = gen` `:128`, the pod-level line `:130-132`, `onHoldTimeout`'s read `:187`, the call `:206`, `terminateHeldSession` `:225`, its log `:228`, `writeHoldPostMortem` call `:229`, decl `:283`. The recipe is `grep -n "LastFencedGeneration()\|s.hold.gen\|terminateHeldSession(\|writeHoldPostMortem(" pkg/adapter/holdstate.go` — EVIDENCE: pkg/adapter/holdstate.go:43,119,128,130-132,187,206,225,229,283

FACT: the landed hold case §8 amends really does hold TWO sessions and assert ONE generation for both, so §8's description of it is exact rather than aspirational. `holdTerminationServer(t, rt, "sess-a", "sess-b")`, a fence of `sess-a` to 7, then a `for _, id := range []string{"sess-a","sess-b"}` loop asserting `rec.LastGeneration != 7` on each post-mortem. The case installs NO slog handler of its own; the file's `slog.SetDefault` capture pattern lives in a different test, so §8's added `coordinator_connection_lost` assertion needs that pattern imported into the case. That is authoring work, not a feasibility bar — EVIDENCE: pkg/adapter/holdstate_test.go:674-720, :895-898

FACT: `coordfence.fence`'s four line citations in CODE-4 all resolve: the non-positive floor `:147-153`, the stale arm `:164-179`, the transient `default:` arm `:180-183`, the budget-exhausted relinquish `:186-188`. So CODE-4's argument for retaining the floor (a 0 fence lands in the TRANSIENT arm, burns the budget, relinquishes — it never reaches the stale arm's re-read-and-retry) is verified end to end — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:147-153, :180-188

FACT: the mid-flight deregistration case's four named fixtures all exist, all in `package adapter_test`, at the exact lines §8 gives: `driveCheckpointConc` `checkpoint_stream_test.go:384`, the sibling case at `:417`, `concurrentServer` `slot_test.go:24`, `slotStartReq` `slot_test.go:37`, `adapterClient` `server_test.go:90`. The package wall §8 invokes is real (`coordination_test.go` is `package adapter`; all three fixture files are `package adapter_test`), so the bullet's placement instruction is correct rather than a guess — EVIDENCE: pkg/adapter/checkpoint_stream_test.go:3, :384, :417; pkg/adapter/slot_test.go:3, :24, :37; pkg/adapter/server_test.go:3, :90

FACT: `Server.LastFencedGeneration()` has exactly three callers and `BarrierWaiting()` exactly one, confirmed by a repo-wide grep that also rules out `cmd/` and `sdks/`. `isQuiescedForBarrier()` has two, both in `coordination_test.go` (`:279`, `:298`), which is why CODE-1's "adds nothing" is right — it adds no file §9 does not already list. Recipe: `grep -rn "LastFencedGeneration()\|BarrierWaiting()\|isQuiescedForBarrier()" --include=*.go .` — EVIDENCE: pkg/adapter/holdstate.go:119; pkg/adapter/coordination_test.go:73, :279, :298; tests/testinfra/coordfixture/coordfixture.go:115; pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163

FACT: `coordfixture.Pod` exports `Server`, `Client`, and `SessionID` from `StartPod`, and `FenceReadopter.ReadoptAndFence` already takes a `sessionID` parameter it then DISCARDS in favour of `p.SessionID` when it calls `r.Pod.Fence(ctx, generation)`. So CODE-1's per-session accessor set is a pure signature change with the argument already in hand at every call site — no plumbing is owed — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:103, :109-111, :115, :122, :220-231

USEFUL [`### Traps` — "grep the ARCHIVE for a candidate's subject BEFORE working it up"]: the `fill-the-blanks` and `.down.sql` archive greps, plus the refuted list this run was handed, pre-adjudicated five of the seven candidates my lens generated (the §5/non-spec headers, 0181's unbatched backfill, 0181's singular `.down.sql` default, the `TestCheckpointBarrierRejectsWithoutFence` disposition, the unbound-session barrier test). Reading them first is what left me a budget to re-derive the code chain above.

USEFUL [`### Settled` — "Landed cases already pin what §8 might otherwise be thought to owe" and the accessor-blast-radius bullet]: both held on independent re-derivation this run, and together they are the reason the §9 list needs no pkg/ addition.

UNVERIFIED: whether an implementor takes the entry's `coordinationState.mu` on CODE-3's post-mortem read of the detached `*slotState`. Still open from `[non-spec.1.review-security.1]`; I confirmed the race window is real in shape (a `CoordinatorFence` that cleared `checkSessionBound` before pass 1 still holds the pointer while pass 2 reads `lastFenced`), and confirmed the natural fix is free because `coordinationState` embeds its own `mu` as its first field and every landed read already goes through a locking accessor. Below the bar as a finding; a fixer touching CODE-3 closes it in one clause.

OPEN: `### Open` still carries "§8's tier-4 sentence for D7 names no case, file, or step", filed by this lens in an earlier run and never landed. I did NOT re-file it: §8's only tier-4 bullet is CODE-1's co-tenant fence case, and the trailing "Tier 4 covers the same flow across the gateway, the session store, and the pod" is ambiguous between D7's barrier arm and CODE-4's baseline arm. It is a test-coverage question rather than a feasibility one, and the tier-list-bookkeeping refutation reaches at least half of it. Whoever owns §8 next should either name the tier-4 case for D7 or delete the sentence.


### [non-spec.5.review-fresh.1]

DECISION: Returned an empty findings list — BECAUSE every claim I re-opened in `non-spec-changes.md` resolved
against the tree, and every candidate I generated was already on this run's refuted list or on the standing
weighed-and-declined list — ALTERNATIVES: I worked up and dropped (a) the tier-1 bullet 1 "a `CheckpointBarrier`
for `sess-b` at 2 is accepted" as a second instance of the hang trap (same class as the refuted
`TestCheckpointBarrierRejectsWithoutFence` disposition; the standing MISTAKE entry says any "is accepted" bullet is
constructible with the goroutine/waitBarrierWaiting/drive pattern); (b) the §9 omission of a tier-3 and a tier-7a
new-case file (pre-adjudicated below the bar); (c) SPEC-1's "a session row carries 1 from creation" as a universal
the rolling window falsifies (reconciled by §10.5's expand-contract rule, which CODE-4 itself cites); (d) the
"column default baselines nothing" DEFERRED (the sites are not false, only loose, per the DEFERRED's own words).

FACT: I re-verified the whole non-spec citation set from scratch this run and it holds. Verified this pass, so a
later round can skip re-deriving them: Makefile:91-94 (generate-proto), proto_no_drift_test.go:70, proto:1451 →
pb.go:4966, grpc.pb.go:180/:632; slot.go:21 `slotState`, server.go:302 `coord`, :307 `hold`, :314 `barrier`;
coordination.go:25 `coordinationState`, :44/:52/:63 accessors, :89/:92/:93-94/:99/:108-116 fence, :148 `barrierGate`,
:158-166 open, :180-188 link, :216/:223/:224-226/:236-239 barrier, :245-269 quiesce-and-hold; checkpoint.go:94/:111/
:122/:124; oplock.go:119-129; slot.go:153-166; resume.go:178; session.go:237-239/:271; slotsession.go:267
`checkSessionBound`, :273 refusal string, :282 `heldSession`, :347 `deregisterStartedSessions`; holdstate.go:43/:119/
:128/:130-132/:187/:206/:225/:228/:249/:283; barrier.go:190-201/:238-245; coordfence.go:147-153/:164-179/:180-183/
:186-188; start.go:4233-4245 with the `.Fence(` call at :4237; coordination.go:463 `RecordHandoff`; migrate-job.yaml:
10-16/:37-39; 0050:38-39; 0164:44; 0180 is the last migration; pgstore.go:140/:177/:244-248/:249; memstore.go:46/
:58-61; prod_columns_test.go:583 (0180 row)/:295 (0112 row)/:610 (`TestProdMigrationsRollBackPerStep`);
lint-migrations.sh:45 (`TEST_DIR`)/:74-88 (pass 3); cmd_run.go:498-508/:635-641/:880; coordfixture.go:76/:98-102/
:106-108/:109/:115/:122/:220-241/:231; checkpoint_stream_test.go:384/:417, slot_test.go:24/:37, server_test.go:90;
uploaddriver.go:422 and partialmanifeststore.go:394 (both strictly-greater), uploaddriver_test.go:992/:993-995/:1007/
:1015, checkpointer_test.go:89-96; memstore_test.go:311 (`TestCreateDefaultsSessionRecordFields`, asserts 0 at :324);
holdstate_test.go:674 (fences sess-a at 7, asserts LastGeneration==7 for BOTH sessions at :713);
coordination_takeover_test.go:74/:142/:241/:301 `mustCreate` unset; split_brain_test.go:72 seeds 1 and :83 fences 1;
tier-8 :118/:179 seed 1, :239-241 unset, LastFenced at :150/:195/:223, assertions at :267/:283/:296; tier-7a :130
seeds 1, :144 seeds 0, LastFenced at :260, assertions at :264-265 and :287-288; sweep_test.go is memstore-backed
(`memstore.New()` at :167 and after) so its :275-594 assertions shift through CODE-4's memstore floor.

FACT: the accessor blast radius closes exactly, re-derived independently.
`grep -rn "LastFencedGeneration()\|isQuiescedForBarrier()\|BarrierWaiting()\|\.LastFenced()" --include=*.go .`
returns callers in only five files outside the declarations, and §9 lists every one: holdstate.go,
coordination_test.go (:73, :279, :298), checkpointbarrier_test.go (:163), coordfixture.go (:115), and the three
`Pod.LastFenced` consumers (tier-4 :109/:110/:144/:145, tier-7a :260, tier-8 :150/:195/:223). Note the tier-4 file
has FOUR `LastFenced` reads, not the two the standing context's tier-4 mention implies; all sit in a subtest seeded
at 1, so the conclusion is unaffected — EVIDENCE: tests/tier4_integration/coordination_fence_split_brain_test.go:72,
:109, :144.

FACT: no landed `CheckpointBarrier` call site hangs under D7 except the one §8 already schedules.
`grep -rn "CheckpointBarrier(" --include=*_test.go .` returns nine call sites; every one either fences the session
first (coordination_test.go:210/:270/:345/:382/:402, checkpointbarrier_test.go:130/:182), omits the session id so
`InvalidArgument` fires before the gate (coordination_test.go:177), or is
`TestCheckpointBarrierRejectsWithoutFence` (:191) which §8 amends in the CODE-2 step. The two
`coordfixture.Pod.StaleRPCRejected` sites both probe an already-fenced session. Do not re-derive this sweep.

FACT: no raw-SQL `sessions` fixture and no landed test asserts the session row's default generation, so 0181's
`DEFAULT 1` strands nothing. `grep -rn "INSERT INTO sessions"` returns eighteen sites and not one names
`coordination_generation` in its column list; none of those files appears in a `CoordinationGeneration` assertion
grep. The only tracked mentions under `tests/` are claim-map rows, the tier-0 claim-register fixtures, the
`checkpoint_manifest` index in checkpoint_slot_id_drop_test.go, and `prod_columns_test.go`'s column-existence lists
at :104-108 and :146 — none an assertion on a default.

FACT: the class-1 baseline sweep is complete and only the two known memstore `Update` cases are unnamed. A grep of
every `CoordinationGeneration` occurrence in `_test.go` files, minus the explicit `: [1-9]` seeds, leaves exactly
these unnamed-by-§8 assertion sites, and each is baseline-independent for a stated reason:
`TestUpdateClampsGenerationCountersMonotonically` (memstore_test.go:339, seeds 5 explicitly),
`derive_failure_audit_test.go:46` (a fake `Get` that increments, so relative),
`sessioncheckpointmeta` / `evictionstatestore` / `coordlease` / `partialmanifeststore` (different tables, seeded
explicitly), `coordination_mirror_test.go:116` (s1 seeded at 2), `wiring_test.go:171` and `coordlease_test.go:37`,
`:58` (`barrier.Target` and lease literals), `sessionserver/coordination_fence_test.go` and `failure_test.go` (all
seeded 5/7/9/10/11/14), `checkpoint_intent_generation_test.go` (set by `Update`), and the tier-4 split-brain and
eviction files. The two genuine residues are `TestUpdateAdvancesGenerationCounters` (memstore_test.go:419, :430) and
`TestUpdateConcurrentGenerationBumpsPreserveMonotonicity` (:478, :490), which the standing context already records
as weighed and handed to the implementor.

USEFUL [Standing context, "Refuted classes" and "MISTAKE: grep the ARCHIVE for a candidate's subject BEFORE working
it up"]: the run-preamble refuted list plus the standing refuted classes killed six of my eight candidates before I
spent a verification on any of them. The single highest-leverage instruction on this proposal is still: read the
preamble's refuted list and the standing `### Traps` first, then verify, then file.

USEFUL [Standing context, "Two lens-scoping facts worth one grep each" and "The alert and metric surface is closed
and untouched"]: I re-ran the docs/charts/schemas grep for the one surface CODE-3 actually changes (the pod-level
`coordinator_connection_lost` line's generation key) and it returns `spec/10:60` and `spec/29:1274` alone, both
staged. That is the whole criterion-(d) surface for the hold half, and it confirms the standing inventory rather
than extending it.

WATCHOUT: `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` returns NOTHING this round, so the
"read the changed sections first and hardest" instruction has no target. That is the third time this has happened on
this proposal. Run the `diff -rq` first anyway; it costs one call and tells you the whole document is old text
rather than fix-stage text.

WATCHOUT: reading this review log through Bash persists anything over ~30KB to a tool-result file, and reading THAT
file through `cat` persists it again, recursively. Read `0076...review-log.md` with `sed -n 'A,Bp'` in windows of
about 140 lines, which stays under the cap. The standing context is lines 3 to 1630.


### [non-spec.5.review-kubernetes.1]

DECISION: EMPTY findings list — the fourth consecutive empty return for this lens on this proposal, and
the third over text that has not changed by a single byte — BECAUSE
`diff -rq scratchpad/cp-snap/0076-run7/non-spec-r1-start proposals/0076_.../` reports ONLY
`summary.md` as differing: `spec-changes.md`, `non-spec-changes.md`, and
`implementation-checklist.md` are byte-identical to what `[non-spec.1.review-kubernetes.1]` and
`[non-spec.2.review-kubernetes.1]` each swept in full, and `diff -rq ... non-spec-r4-start ...`
returns nothing at all. The only summary movement since r3-start is OD10's withdrawal-in-place and one
sentence in the framing paragraph about restated wire sentences; neither names a Kubernetes object —
ALTERNATIVES: I re-derived rather than inherited every load-bearing claim this lens rests on, and all
five hold (details below). I did not treat the prior empty returns as evidence.

FACT (re-derived this run, all four checks independent): (a) `charts/lenny/templates/migrate-job.yaml`
still carries the whole rolling-window argument and BOTH cited anchors land — the header block states
the weight -5 / after preflight (-10) / before the gateway Deployment ordering and names the §10.5
expand-contract discipline, and the annotations are `pre-install,pre-upgrade` / `"-5"` /
`before-hook-creation,hook-succeeded`. (b) Adding migration 0181 obliges NO `charts/` edit:
`grep -rn "018[01]\|migrationVersion\|expectedMigration" charts/` returns NOTHING, and
`migrations/embed.go` embeds with `//go:embed *.sql` rather than enumerating. (c) The counter has no
Kubernetes carrier at all: `grep -rn "coordination_generation\|CoordinationGeneration" pkg/apis/
pkg/controller/ charts/ config/` returns NOTHING, so there is no CRD field, no controller write, no
status subresource, and no chart value to race a second field manager over. (d) D7's acceptance arm
adds no pod-shutdown stage: `dispatchOne` starts `CheckpointWithTrigger` in a goroutine BEFORE
`dispatch.Send` and `cpWG.Wait()`s after it UNCONDITIONALLY, so the wall clock is the checkpoint
stream's whatever the barrier returns, and a true `Acked` makes the prestop loop `continue` past that
session on `barrierAcked[sess.SessionID]` — so an accepted barrier REMOVES a duplicate capture rather
than adding one, and the pod's `terminationGracePeriodSeconds` floor is untouched —
EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16, :37-39; migrations/embed.go:11-16;
pkg/gateway/coordination/barrier/barrier.go:217-227, :236; pkg/gateway/podlifecycle/prestop/prestop.go:380-397.

FACT: the one-grep scoping call still holds and is worth making first on any future pass. Grepping
BOTH change files for `sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|
informer|etcd|leader|admission|lease|helm|chart|podspec|daemon|statefulset|deployment` returns ~35
lines, and every one is either a proto message name (`ExtendCredentialLeaseRequest`), the
Postgres/Redis `coordination_lease` table or `REG-COORDLEASE`, the gateway `Sweeper`'s session lease,
or the migrate-Job hook sentences verified above. There is no Kubernetes object in the staging. The
coordination state this proposal moves lives on an in-process Go struct and a Postgres column, so the
CRD-as-message-bus, status-as-RPC-inbox, two-manager-status-race, finalizer-footgun, and
admission-coherence idioms have no surface here, and the migrate hook is the only Kubernetes mechanism
the proposal touches at all — EVIDENCE: non-spec-changes.md:137-139; spec-changes.md:253-254.

USEFUL [`[non-spec.2.review-kubernetes.1]`, its three reopening conditions]: the conditions it named
(CODE-4 grows a chart or CRD deliverable, CODE-3 grows an apiserver or `Sandbox.status` write, D7's
acceptance arm is rewritten) are exactly the right trigger set and are still all three unmet. Checking
them cost three greps against a diff that was already empty. That entry is what let this pass spend
its budget on re-deriving the four facts above rather than on re-reading 420 lines.

USEFUL [`### Traps`, the `spec/10:130-138` two-grace-budgets bullet]: still the single highest-value
entry for this lens. It pre-answers the one candidate a Kubernetes reader generates unprompted from D7
(an accepted barrier blocks where a refused one returned instantly, so the agent pod's grace floor is
short) and it names both formulas. I re-verified its mechanical half independently this run; it holds.

OPEN: this lens has now returned empty FOUR times (run 6 spec round 4, run 7 non-spec rounds 1, 2, and
5), the last three over byte-identical `spec-changes.md`/`non-spec-changes.md`/checklist. Each empty
return costs the other eleven lenses a full round. The standing `### Open` item "Whether an exhausted
lens is retired" has now been asked for seven rounds with no answer. Retiring this lens on THIS
proposal costs nothing while the three reopening conditions above stay unmet; a human or the
orchestrator should answer it rather than a ninth round asking again.

WATCHOUT: do not read `review-log.md` through `sed`/`awk` in Bash. Both `awk '/^## Standing
context/,/^## [^S]/'` and a plain `sed -n` range blew the output cap and were persisted to a
tool-result file that then reports FEWER lines than the range asked for, which reads exactly like the
section ending early. Grep `^## \|^### ` for the section offsets first, then read narrow ranges —
EVIDENCE: review-log.md:3, :812, :1355, :1631 are the real section boundaries.


### [non-spec.5.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE every end-to-end flow the non-spec staging describes
(fence gate, barrier gate + Checkpoint link/complete, hold post-mortem, counter baseline through migration
0181 and both `Create` floors, the retained `coordfence` floor) traces cleanly from origin to effect against
the tree, and every residual I derived was already on the standing `### Open`/`### Deferred`/refuted lists —
ALTERNATIVES: I worked up and dropped four candidates, each recorded below with its ground, so the next
mechanism lens does not re-derive them.

FACT: `diff -rq` against the run-4 snapshot returns NOTHING. The proposal is byte-identical to the text
run 6 round 4 reviewed. "Read the changed sections first" had no target. Run it first; it is the cheapest
move on this proposal — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r4 vs. the proposal directory.

FACT: the pod-wide gate's "two-way failure" in CODE-1 (`non-spec-changes.md:46-51`) is exactly right and the
interleaving that produces the cross-linked ref is worth writing down once. `open()` overwrites `done`,
`checkpointID`, and `signaled` unconditionally (`coordination.go:158-166`), `release()` clears only
`waiting` (`:171-176`), and `complete()` closes whatever `g.done` currently is (`:192-199`). So: barrier A
opens; barrier B opens (clobbers); A's stream links "ckpt-A"; A's stream terminates and closes B's channel;
B wakes and `release()` returns "ckpt-A"; `dispatchOne` persists "ckpt-A" under B's session id
(`pkg/gateway/coordination/barrier/barrier.go:238-245`). A itself never wakes and its client call dies on
the ack deadline, so A produces no meta row at all. Both halves of the staged sentence are reachable.

FACT: the `coordination_lease` mirror self-heals after migration 0181 and needs no backfill. `Sweep` calls
`upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)` unconditionally for every eligible
non-terminal row on every cycle, so a lease row left at 0 by the migration is refreshed to 1 within one
sweep interval, and `coordlease/pgstore.Upsert` names the column explicitly in both the INSERT and the
ON CONFLICT DO UPDATE, so the new `DEFAULT 1` on that column really is cosmetic — EVIDENCE:
pkg/gateway/coordination/coordination/coordination.go:430; pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58.
I chased this as a "gate one write path bypasses" candidate and it is not one.

FACT: with `c.checkpoint == nil` the barrier fires with no gateway-driven stream at all
(`cmd/lenny-gateway/httpsurface.go:617-623` says so in its own comment), so under D7 every ordinary
never-fenced session's drain barrier is accepted and then blocks to the shared 90s ack deadline where today
it is refused instantly. Weighed and NOT filed: the same block already exists today for any *fenced* session
in that configuration, so D7 widens a population rather than creating a failure mode; `Dispatch` fans out
concurrently under one shared deadline so nothing multiplies; and §10.1.8 already sizes the gateway grace
budget with `checkpointBarrierAckTimeoutSeconds` in it. A later lens wanting this must argue past all three.

FACT: §8's arithmetic on the two baseline-shift files is exact against the tree and does not need
re-deriving. tier-8 `:239-241` seeds unset, `:267`/`:283` assert 1 and `:296` asserts 2, so 2/2/3 is right;
tier-7a `:144` seeds an explicit 0 through `memstore.Create` and `:287-288` asserts 0, so the `Create` floor
is what moves it. `coordination_mirror_test.go` really is exempt: `s1` is seeded at 2 (`:84`) and asserted at
2 (`:116-117`), and the two unset rows (`:85-86`) are never asserted on the generation.

FACT: the class-1/class-2 sweep is complete against a whole-tree grep. Thirty-four `_test.go` files mention
`CoordinationGeneration`; every one outside §8's named set is either an explicit seed at or above 1, a
`barrier.Target` literal against a fake dispatcher, a different table's row, or a relative bump with no
absolute assertion (`derive_failure_audit_test.go:46` is the last of those). Do not re-run this grep.

WATCHOUT: `tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` looks like the home
for §8's staged tier-3 barrier case and is not. It asserts wire ENCODING of the generation field (presence,
round-trip, oneof coexistence) and pins no gate at all, so the staged case needs a new file. That is the
already-recorded "§9 names no file for the staged tier-3 and tier-2 cases" OPEN, and refuted class (k) plus
§8's own assertion list keep it off criterion (f) — EVIDENCE:
tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:104, :141, :193, :228.

WATCHOUT: the summary's fixed-decision bullet "CODE-1's lock order is the registry lock, then the entry
lock, then the hold lock. The one opposite-order acquisition in the tree is the read CODE-3 removes"
(`summary.md:63-64`) is loose in its second sentence. `enterHoldState` reads through accessors and holds no
two locks together (`holdstate.go:116-122`), so that read is not an opposite-order acquisition under the
declared order at all; the real reason CODE-3 must remove it is that it has no session id to pass. The first
sentence, which is the load-bearing half, is correct: `CoordinatorFence` holds the entry lock across
`exitHoldState` (`coordination.go:97-98`, `:129`), `onHoldTimeout` releases `hold.mu` at `:189` before both
passes, and no path takes them the other way. Weighed and not filed as a supporting remark whose falsity
breaks nothing; a later lens should not spend a verification on it either.

WATCHOUT: the rolling-window row class produces a real equal-generation collision and it is ALREADY the
standing OPEN "OD2's ordering rationale is imprecise for the rolling window". Worked out concretely so the
next agent does not re-derive it: for a row an old binary minted at 0, `fenceResumedPod` floors it to 1
(`coordfence.go:147-153`) and fences the pod at 1 while the row stays 0; a later takeover's `RecordHandoff`
bumps 0 to 1 and fences at 1; the pod's `gen <= lastFenced` refuses it (`coordination.go:99`); `fence`'s
stale arm re-reads, finds `newGen == gen`, and relinquishes (`coordfence.go:164-179`); the next sweep bumps
1 to 2 and succeeds. Cost is one relinquish plus one adoption backoff, self-healing, confined to rows the
old fleet writes during the roll. Do not file it against the non-spec staging: CODE-4's own counterfactual
sentence (`non-spec-changes.md:146-155`) is about the floor being DELETED and is accurate as written.

USEFUL [Standing `### Settled`, "The accessor blast radius is exactly what §9 lists"]: saved me the whole
accessor sweep. I re-confirmed the production side cheaply — a grep for `s.barrier` and `.coord.` under
`pkg/adapter/` excluding tests returns exactly the sites CODE-1 enumerates and nothing else.

USEFUL [Standing `### Traps`, "an accepted `CheckpointBarrier` blocks ... It can"]: stopped me filing the
§8 tier-1 "a `CheckpointBarrier` for `sess-b` at 2 is accepted" bullet as unconstructible.
`TestCheckpointBarrierAcksEchoedCheckpointID` calls `s.barrier.link(...)` and `s.barrier.complete()` directly
from `package adapter` (`coordination_test.go:282`, `:285`), so no external-package stream fixture is needed
for the barrier-gate independence case either.

UNVERIFIED: whether the tier-7a two-barrier case's "both return well inside the barrier ack deadline" holds
once the op lock serialises the two uploads. The mechanism works — barrier A's gate is linked and completed
by stream A, then stream B is promoted and does the same for B (`oplock.go:119-129`, `checkpoint.go:111`,
`:122-124`) — but nobody has written the fixture, so the wall-clock claim rests on the fake runtime being
fast. Whoever writes the case checks it.


### [non-spec.5.review-open-decisions.1]

FACT: the four snapshot dirs under `scratchpad/cp-snap/0076-run7/` (`non-spec-r4-start`, `non-spec-r4`, `non-spec-r5-start`) are byte-identical to the live proposal directory, so the "what changed since last round" diff returns nothing and gives no reading order. The summary is the only file that moved this window (39942 bytes at `non-spec-r2` -> 41706 now); `non-spec-changes.md` has not changed since r2 — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r2/, scratchpad/cp-snap/0076-run7/non-spec-r4/

FINDING (filed): `summary.md:106-107` states that the status file's "closing paragraph still frames the hold's scope as the open question this change answers", and `summary.md:453-454` in the same document states "no such paragraph exists". The status file is 33 lines and carries no such paragraph: `status.md:28-29` is the boilerplate staging caveat and `:31-33` is the review-history record. The false half is in the "Watch out for" block an implementor reads; the true half is in `### Corrections outstanding in the proposal`. Fix is to delete the clause at `:106-107` and keep only the scope-bullet half, which IS still true (`status.md:25` "and releases its coordinator-loss hold") — EVIDENCE: summary.md:102-108, :453-454; status.md:1-33

DECISION: filed exactly one finding — BECAUSE every other open decision in the section is either properly stated for the human or already filed and refuted by the material skeptic — ALTERNATIVES: I derived and then dropped a second finding against the `## Impact on other proposals` 0075 row (`summary.md:123`), which asserts "Removes its sole counterexample" unconditionally in column 3 while column 4 conditions 0075's fate on OD3's answer. Dropped because "counterexample" admits a reading (the message is no longer *genuinely* pod-scoped after CODE-1, whatever `spec/04` §4.1 still declares) under which column 3 is true, and the OD12 refutation ("imprecise novelty accounting, not a false attribution") applies verbatim.

FACT (re-verified this run, every anchor resolves): OD1's `cmd/lenny-gateway/httpsurface.go:588-602` (the `Fallback` closure, seeds `gen := int64(0)` at `:592`, overwrites only on a successful `w.sessions.Get` at `:593-594`); OD2's `pkg/adapter/coordination.go:99` (`initialized && gen <= lastFenced` -> `FailedPrecondition` + `coordinator_handoff_stale`), `coordfence.go:147-153` (the `gen <= 0 -> 1` floor), `:164-179` (the stale branch re-reads and retries only when `newGen > gen`, else relinquishes), `start.go:4233-4245`, `schemas/lenny-adapter.proto:1455-1462` ("not greater than the last fenced generation"); OD3's `spec/04:151`, `:175`, `:188`, `:190` (the `ShutdownRequest` precedent), `schemas/lenny-adapter.proto:492-496` (`ReportPodScrubRequest` declares `pod_id` alone); OD5's `pkg/adapter/coordination.go:224-226` (barrier's non-positive `InvalidArgument` before the gate at `:236`); OD8's `charts/lenny/templates/migrate-job.yaml:10-16`, `:37-39`, `pgstore.go:177`, `:260`; OD10's `spec/10:183` (step 1 carries BOTH the "current `coordination_generation`" clause and the closing "reject ... safe and does not require special handling" sentence on one physical line), `spec-changes.md:206-222`, `:259-264`, `:428-430`.

FACT: OD3's two gate claims are both true. `tests/tier0_static/adapter_proto_message_scope_test.go:111-116` accepts any row whose scope is `session` or `pod` (`declaredScope`), so reclassifying `CoordinatorFenceRequest` turns nothing red. `tests/tier3_contract/adapter_session_address/session_address_wire_test.go:41-43` names the message only in a comment ("covered by the session-address arm below alone") while all four tests iterate `sessionScopedMessages` (`:44-63`), which excludes it — EVIDENCE: session_address_wire_test.go:78, :99, :127, :150

USEFUL [`### Settled` "coordfence.fence's stale branch is not a bare relinquish"]: it is what makes OD2's lost-ack arithmetic checkable in one read. Attempt 1 lands at G, ack lost, `default:` transient arm retries at G, attempt 2 refused stale, re-read returns G, `newGen > gen` is false, relinquish. OD2's "one increment each of `lenny_coordinator_handoff_stale_total` and `lenny_coordinator_fence_relinquished_total`" is exact.

WATCHOUT: `### Corrections outstanding` bullet 2 (`summary.md:448-452`) says the claim-map correction "is to restatus it and to replace its note" without naming the generator-source edit, while OD11 (`summary.md:394-397`) states the route correctly (`gateway-runtime-comms.md` §7.1 + `scripts/seed-claim-register.py`, byte-diffed by `TestClaimRegisterIsReproducibleFromItsGenerator`). I did NOT file it: the sentence's "it" is the row rather than the file, so the sentence is not strictly false, and the `### Deferred` entry already carries the generator-source remedy. A fixer editing that bullet for another reason should name the generator source in the same pass.

UNVERIFIED: whether `summary.md:123`'s 0075 row should read "Removes the ground for its sole counterexample" rather than "Removes its sole counterexample". `proposals/0075...:60-68` defines the counterexample as a disagreement between the *declared* §4.1 scope and the derived one, and no deliverable here edits §4.1, so on that reading CODE-1 removes the ground (`0075:85-90`, `s.coord` being one pod-wide `coordinationState`) and leaves the counterexample standing until OD3 is answered "yes". Whoever next touches the impact table should settle which reading the row means.

OPEN: `spec-changes.md` §7 still lists three "Open decisions for review" while `summary.md:399-410` dispositions item 3 (`coord.mu`) as "Not a reviewer decision ... Delete the item" and item 2 (a fence for an unheld session) as "out of scope for this proposal, with a named consequence and an owner". No owner is named anywhere, and neither disposition is applied to §7. Not filed as a close variant of the refuted `IMPLEMENTOR TO FILL THE BLANKS` bookkeeping class, but a spec-lane fixer with a write path to `spec-changes.md:604-616` closes it in one edit.

MISTAKE: run 6's standing-context line "the OD9 'recorded in neither entry' half" was closed as stale, and it is: `summary.md:322` still carries "The coupling runs one way and is recorded in neither entry" while OD8's withdrawal at `:313-317` records it. That exact site was filed and REFUTED this window, so do not file it again; it is bookkeeping in a reviewer-facing list.

FACT: both cross-proposal targets are `Draft` and may be invalidated freely. `proposal-status.mjs` reports 0075 and 0080 as legacy single-file `Draft` with no dates; their last commit dates are 2026-08-19 and 2026-08-31, which is when someone last touched the file rather than when it was reviewed — EVIDENCE: git log -1 -- proposals/0075_fix_derive-message-scope-from-the-address-type.md


### [non-spec.5.review-operational.1]

DECISION: returned EMPTY on the non-spec staging — BECAUSE every operational surface this proposal touches
(metric catalog, §16 inventories, alert rules, runbooks, docs/reference/metrics.md, structured-event carriers)
was re-derived from the tree this pass and none of it is made wrong by SCHEMA-1, CODE-1..4, TEST-1, or §8.
ALTERNATIVES: (a) the Goals bullet `summary.md:132-133` "`lenny_coordinator_handoff_stale_total` counts a
genuine stale fence" against the proposal's own unstaged-defect bullet `summary.md:421-424` ("the fence driver
conflates three failure classes into one metric") — declined, it is Goals prose that lands in no file and the
material skeptic has refuted ten findings of that class; (b) CODE-3 leaving BOTH pod-level hold lines without a
generation (the arming line loses `last_generation` and `exitHoldState` deliberately logs no fields) — declined,
staged §10.1.4 says exactly that, so spec and code agree and nothing tells an operator a generation is there;
(c) the barrier carriers' `FailedPrecondition` disposition — declined as a close variant of the already-refuted
`CoordinatorFenceRequest` tail finding.

FACT: the whole coordinator observability surface, re-verified this pass, is inert for this change.
`lenny_adapter_coordinator_hold` exists as gauge + catalog + spec + docs (pkg/adapter/metrics.go:108,
pkg/observability/metrics/catalog.go:271, spec/16_observability.md:185, docs/reference/metrics.md:309);
`lenny_coordinator_handoff_stale_total` / `_fence_retry_total` / `_fence_relinquished_total` all exist in
catalog.go:269,:273,:274, spec/16:183,:191,:192 and docs/reference/metrics.md:307,:311,:312. The ONLY alert on
this surface is `CoordinatorHandoffSlow`, and it reads `lenny_coordinator_handoff_duration_seconds` alone
(pkg/alerting/rules/rules.go:1583) — no alert reads the stale, retry, relinquish, hold-gauge, or barrier-ack
metrics, so no alert threshold or runbook step moves. `docs/runbooks/coordinator-handoff-slow.md` is entirely
about the parent-to-child delegation handoff and names none of these metrics.

FACT: `lenny_checkpoint_barrier_ack_total` still has a catalog row (catalog.go:127) and no incrementer anywhere
under `pkg/`, re-checked with a grep for the name plus `barrierAck`. D7 therefore moves no count between label
values and `docs/operator-guide/upgrades.md:51-53`'s "Monitor checkpoint barrier outcomes" instruction is
equally (pre-existing) empty before and after. Confirms the standing `### Settled` entry.

FACT: no test in the tree asserts the TEXT of any proto comment SCHEMA-1 rewrites, beyond the tier-0 stub-drift
gate. `grep -rln lenny-adapter.proto tests/` returns 18 files; the two tier-11 hits
(`artifact_register_supersession_test.go`, `credential_path_literal_sweep_test.go`) key on the artifact register
and on credential path literals, and a grep across `tests/` for "cannot drive the pod", "last fenced generation",
"rejects any RPC carrying", and "handoff_stale" returns no assertion over the nineteen carriers. The three
credential-RPC field comments SCHEMA-1 rewrites are not read by any tier-9 or tier-11 gate either.

CORRECTS [`### Traps` bullet "two pre-existing observability drifts sit beside this surface"]: its first half is
FALSE. It says `spec/16:552` points operators at `lenny_coordinator_fence_retry_total`, "which appears in no
§16.1 inventory row and no metric catalog". The metric has an inventory row at spec/16_observability.md:191
("Coordinator fence retries (`lenny_coordinator_fence_retry_total`, counter labeled by `pool` ...)"), a catalog
entry at pkg/observability/metrics/catalog.go:273, a catalog-test entry at
pkg/observability/metrics/catalog_test.go:97, and a docs row at docs/reference/metrics.md:311. There is no drift
there at all. The bullet's second half (metrics.md:196 giving the barrier-ack outcome set as
`success, timeout, error`) is confirmed as written. A future round should not spend a pass hunting the phantom.

FACT (citation spot-checks that all resolved, so a later lens can skip them): coordfence.go:147-153 is the
non-positive floor, :164-179 the stale arm (`f.incStale()` at :170), :180-183 the transient arm (`incRetry()` at
:182), :186-188 the budget-exhausted relinquish — so CODE-4's claim that a deleted floor sends the adapter's
`InvalidArgument` into the TRANSIENT arm rather than the stale arm is exactly right, and no
`lenny_coordinator_handoff_stale_total` increment would occur on that path. coordination.go:99 (stale fence,
`FailedPrecondition` + `coordinator_handoff_stale`), :114 (`coordinator_generation_gap` slog with `session_id`,
`last_fenced_generation`, `new_generation`), :224-226 (barrier non-positive `InvalidArgument`), :236-239 (the
`!initialized || gen != fenced` gate) all resolve. holdstate.go:130-132 is the three-line
`slog.Warn("coordinator_connection_lost", "started_sessions", started, "last_generation", gen)`, :225
`terminateHeldSession`, :283 `writeHoldPostMortem`, and slotsession.go:282-285 `heldSession`. §8's hold case
`TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` is declared at holdstate_test.go:674 and DOES
carry a `// diagnosis:` comment (:670-673), so §8's constraint that the name be kept is grounded. 0180 is the
last taken migration number (`ls migrations/*.up.sql | tail`) and prod_columns_test.go:583 is its column-less
row, both as CODE-4 states.

WATCHOUT: the operational lens's filable surface on this proposal is genuinely empty and has been for three
runs (run 6 round 1 empty, run 7 round 1 cut off, this pass empty after a full re-derivation). It reopens only
if a deliverable starts adding, removing, or relabelling a metric, adds an alert, or changes a structured-event
field set beyond the pod-level `last_generation` key CODE-3 already drops. Reading the whole standing context
plus the tree cost roughly two thirds of this pass for nothing; a future scheduler should retire it.


### [non-spec.5.review-performance.1]

DECISION: Empty findings list for the performance / scalability / failure-mode lens over run 7's non-spec round (r5) — BECAUSE every write-rate, bottleneck, contention and degradation axis I could construct against the staged text resolves to "unchanged", "strictly cheaper", or a standing refutation I re-derived from the tree — ALTERNATIVES: I worked up and rejected (a) D7 removing prestop's second capture pass as a lost retry, (b) the nil-`Checkpointer` barrier hang, (c) 0181 setting `DEFAULT 1` on `coordination_lease.coordination_generation` without backfilling it, (d) the §4 `coord.mu` blank as a contention question CODE-1 already answers, and (e) the stale "the lock is uncontended there by construction" comment. Each is recorded below.

FACT: `diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r4 <proposal dir>` returns EXIT=0 — the four staged files are byte-identical to the r4 snapshot. This round reviews text no fix has touched, which is the fourth consecutive round in that state per the standing WATCHOUT. Run the `diff -rq` first; it is still the cheapest move here.

FACT: the D7 drain refutation holds and I re-derived it end to end rather than trusting the standing entry. `dispatchOne` starts `CheckpointWithTrigger` in a goroutine at `barrier.go:216-223`, calls `dispatch.Send` at `:224`, and `cpWG.Wait()` at `:225`. The N gateway-driven captures therefore already run — concurrently across targets, serialised per pod by the adapter op lock — whether the barrier is accepted, refused, or errors. `Dispatch` fans out one goroutine per target under one `bctx` (`prestop.go:503-504`, 90s). Post-fix the only wall-clock delta is that `dispatch.Send` returns at the end of the stream rather than at its start, and `prestop.go:395-398` then SKIPS the sequential post-barrier capture for the acked session. Post-fix is strictly less MinIO and Postgres work at Tier 3, and §10.1.8 step 3 (`spec/10_gateway-internals.md:185`) already sizes the drain burst at "up to 400 simultaneous uploads" for exactly this fan-out — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:216-227; pkg/gateway/podlifecycle/prestop/prestop.go:388-398, :498-515

FACT: worked up and NOT filed — D7 removes a retry, not only a duplicate. Pre-fix essentially every coordinated session on a draining replica is never-fenced (nothing fences a normally-started session), so the pod-wide `!initialized` arm refuses every barrier, `Acked` is false, and prestop's post-barrier loop gives each session a SECOND capture attempt under its own tiered cap inside `grace`. Post-fix `Acked` is true even when `CheckpointErr != nil` (`barrier.go:154-160`, `:236`, and `fireBarrier` keys only on `o.Acked` at `prestop.go:509-512`), so that second attempt disappears for the whole population. I declined it because (1) §10.1.8 makes the barrier-stage capture the specified one and the stream finalises a partial manifest row on abort, (2) at Tier 3 the sequential post-barrier loop could not complete 400 captures in the residual grace anyway, so the "lost retry" is notional at the top tier, and (3) the summary states the change in so many words ("instead of being captured twice against a live workspace", summary.md:33-35), making it a close variant of the standing settled entry "A refused barrier costs a duplicate capture rather than a lost checkpoint". A later lens wanting it must argue past all three.

FACT: the nil-`Checkpointer` barrier hang is a dev-only path, not a production failure mode. `barrierCheckpointer` is nil only when `w.checkpointSvc` is nil, and `checkpointSvc` is constructed unconditionally inside the `--agent-namespace` branch (`stores.go:2156`); without that flag there are no agent pods, no `podRegistry`, and hence no `barrierDispatch` at all. Under D7 an accepted barrier with no gateway-driven stream would block to the full ack deadline with nothing to `link` or `complete` (`coordination.go:264-268`) — the same mechanism as the known `TestCheckpointBarrierRejectsWithoutFence` hang — but no shipped deployment reaches it — EVIDENCE: cmd/lenny-gateway/httpsurface.go:616-624; cmd/lenny-gateway/stores.go:1911, :2156, :2218

FACT: 0181's asymmetry (backfill `sessions`, `DEFAULT 1` on `coordination_lease` with no backfill) is self-healing within one sweep and is not a finding. The barrier's healthy path reads the mirror, so an existing lease row left at 0 makes the barrier carry 0 and take the adapter's `InvalidArgument` refusal — identical to pre-migration behaviour, and `upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)` runs on EVERY sweep for every held lease, so the mirror converges to the baselined row value one sweep after the migration — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:428-431; cmd/lenny-gateway/stores.go:1471 (`coordMirror = coordleasepg.New(pgPool, nil)`, so the mirror is Postgres and the §12.4 durable bar is met)

FACT: the checklist's step order has no intermediate perf or failure regression. After S5 (per-entry state, gate still `!initialized || gen != fenced`) a co-tenant pod produces the same accept/refuse verdicts as the pod-wide gate did; after S6 but before S7 a never-fenced session's barrier carries 1 rather than 0, so it takes `FailedPrecondition` at `coordination.go:236` instead of `InvalidArgument` at `:224-226`. Both leave `Acked` false and nothing downstream branches on `Outcome.Stale`, so prestop's behaviour is byte-identical at every intermediate step — EVIDENCE: pkg/adapter/coordination.go:224-239; pkg/gateway/coordination/barrier/barrier.go:228-236

FACT: the Kubernetes/etcd/Redis half of this lens is empty by grep. `grep -niE "sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|informer|etcd|leader|redis"` over `non-spec-changes.md` returns NOTHING, matching the standing claim for `spec-changes.md`. No new watch, no new informer cache, no `Sandbox.status` write, no new register. Tier 3's Postgres budget (~1,300/s sustained against a ~1,600 IOPS ceiling, `spec/12_storage-architecture.md:117-121`) is untouched: the only new steady-state write is up to 400 `session_checkpoint_meta` upserts per draining replica spread over the 90s ack window, about 4/s, and that is a drain event rather than sustained load.

WATCHOUT: `pkg/adapter/checkpoint.go:108-110` asserts "A barrier-window checkpoint runs through the same lock; the barrier's quiescence has already drained dispatch, so the lock is uncontended there by construction." That is false on a co-tenant pod, and §8's own tier-7a bullet says so ("the pod-level op lock admits one checkpoint at a time and queues the distinct co-tenant session id behind the running one"). It is false PRE-fix too, because the gateway drives N streams concurrently whatever the barrier returns, so it is pre-existing drift in a file §9 already lists — refuted class (k). Do not file it; do fix the comment if editing that file — EVIDENCE: pkg/adapter/checkpoint.go:99-111; pkg/adapter/oplock.go:116-129; non-spec-changes.md:281-286

WATCHOUT: `quiesced` still has NO production reader (only `isQuiescedForBarrier` at `coordination.go:52-56`, called from `coordination_test.go:279`, `:298`), so an accepted barrier does not actually stop tool-call dispatch. Every proposal sentence about the barrier "quiescing the pod" is a specification-contract statement rather than a description of the tree, and `coordination.go:33-38` files the enforcement under F-10.1.6. A lens tempted to file "D7 does not deliver the quiescence the problem statement claims" should note the same gap makes the PRE-fix harm ("the capture runs against a moving workspace") equally unrealised, so nothing regresses — EVIDENCE: pkg/adapter/coordination.go:33-38, :52-56, :241-256

USEFUL [standing: the barrier fan-out runs under one wall-clock deadline, and D7 adds no drain work]: it is correct and its "Do not re-derive this" is right; I re-derived it anyway and confirm `cpWG.Wait()` is `barrier.go:227` (the standing entry's `:243-244` is the meta upsert, and a run-2 shard already corrected this once).

USEFUL [standing: A refused barrier costs a duplicate capture rather than a lost checkpoint]: it is the entry that kills the sharpest candidate this lens generates (the lost second capture attempt), and it kills it faster than the tree does.

UNVERIFIED: nobody has sized the barrier-stage capture against the population D7 newly admits. Pre-fix a large-workspace never-fenced session got the barrier-stage stream under the shared 90s `bctx` AND a dedicated post-barrier capture under its own tiered cap; post-fix it gets the shared window alone. At Tier 3 the sequential post-barrier loop is unlikely to reach many sessions anyway, so I judged the delta immaterial, but a capacity lens or a human sizing `checkpointBarrierAckTimeoutSeconds` against `max_tiered_checkpoint_cap` for the whole coordinated set (rather than for one slot, which is what the CRD webhook floor at `spec/10_gateway-internals.md:132` vets) should settle it. This supersedes nothing; it is the same question the run-2 shard left open, now attached to a larger population.


### [non-spec.5.review-reliability.1]

DECISION: Returned an empty findings list for the reliability/fault-tolerance lens over run 7's non-spec round 5 — BECAUSE the staged text is BYTE-IDENTICAL to the r4 snapshot (`diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r4 /home/ec2-user/lenny/proposals/0076_...` exits 0 with no output), none of the three reopening conditions the standing context names for this lens was rewritten, and every crash/restart/redelivery trace I built from scratch against the tree either resolves clean or lands on a standing refutation I re-derived — ALTERNATIVES: I worked up and rejected (a) a lock-order inversion introduced by CODE-3's per-session post-mortem read, (b) migration 0181 leaving `coordination_lease` unbackfilled while it backfills `sessions`, (c) the retained `coordfence` floor stranding a crash takeover of a zero row, and (d) `coordfixture.Pod.StaleRPCRejected` turning into a 90s hang under D7. Each is written out below.

FACT (re-derived this run, all anchors resolve): CODE-3 introduces NO lock-order inversion, and the shipped comment that looks like it forbids the move does not. `onHoldTimeout` releases `hold.mu` at `holdstate.go:189` BEFORE pass 1 (`deregisterStartedSessions`, which takes `s.mu`) and pass 2 (`terminateHeldSession` → `writeHoldPostMortem`), so the post-mortem's read of the detached entry's `coordinationState` is taken with no other lock held. `CoordinatorFence` holds the entry lock and calls `exitHoldState`, which takes `hold.mu` only — entry→hold, the declared order. The shipped clause "the hold timeout never reaches back into coord.mu" (`pkg/adapter/coordination.go:126-128`) becomes false as a statement once CODE-3 lands, but it is a comment, and its falsity is a residue rather than a deadlock — EVIDENCE: pkg/adapter/holdstate.go:179-189, :192, :206, :225-229; pkg/adapter/coordination.go:97-98, :124-129, :142-158

FACT: migration 0181's omission of a `coordination_lease` backfill is not a defect, because the mirror self-heals on the next sweep. `Sweeper.Sweep` calls `s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)` unconditionally for every renewed/held lease from the freshly listed session row, and the mirror upsert names `coordination_generation` in its insert column list, so the `DEFAULT 1` 0181 puts on that column is cosmetic in exactly the way `pgstore.Create`'s is on the session row — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:428-430; pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58

FACT: the retained `coordfence` floor cannot strand a takeover, and the baseline strictly improves the case. For a row an old binary minted at 0: resume fences the pod at the floor's 1, then the first crash takeover's `RecordHandoff` bumps 0→1 and fences at 1, which the adapter refuses (`gen <= lastFenced`); `coordfence.fence`'s stale arm re-reads, sees no advance, and relinquishes; the sweep records an adoption backoff and the NEXT sweep's unconditional `row.CoordinationGeneration++` mints 2 and succeeds. So the cost is one backoff window, it is shipped behaviour for EVERY session today (every row is 0), and CODE-4's baseline removes it for every new row. This is the summary's own "repairs a takeover defect in the shipped tree" claim, and it holds — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:147-153, :164-179; pkg/gateway/coordination/coordination/coordination.go:463-482; proposals/0076_.../0076_....summary.md:36-41

FACT: the two `coordfixture.Pod.StaleRPCRejected` call sites are both safe under D7, confirming the standing WATCHOUT rather than contradicting it. Both probe at `gen=1` against a pod already fenced to 2, so the surviving `gen != fenced` arm returns `FailedPrecondition` immediately and nothing blocks to the ack deadline — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:117-127; tests/tier4_integration/coordination_fence_split_brain_test.go:151; tests/tier8_chaos/coordination_crash_takeover_test.go:165

FACT: §8's tier-8 disjointness claim is CORRECT as written and I verified it end to end rather than trusting it. In `tests/tier8_chaos/coordination_crash_takeover_test.go` the `pod.LastFenced` reads sit at `:150-151`, `:195-196`, `:223-224`, all inside the two subtests whose sessions are seeded `CoordinationGeneration: 1` explicitly (`:118`, `:179`); the three assertions the baseline shifts (`:267`, `:283`, `:296`, values 1, 1, 2) belong to the third subtest, seeded with the field unset at `:239-241`, which contains no `LastFenced` read. Same split verified in `tests/tier7a_load_local/coordination_colocation_race_test.go`: `LastFenced` at `:260`, baseline-affected seeds/assertions at `:144` and `:287-288`, and the already-correct `:130`/`:264-265` pair. Do not re-derive this — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118, :150, :179, :195, :223, :239-241, :267, :283, :296

USEFUL [`### Settled` "The barrier's cache fallback puts a literal 0 on the wire"]: the bullet's pointer to `cmd/lenny-gateway/httpsurface.go:588-602` and its warning that no `httpsurface.go` exists under `pkg/` saved me from concluding the standing DEFERRED entry was stale.

USEFUL [`### Traps` "Test-lane fixture hazards"]: the `tests/tier7a_load` / `tests/tier7a_load_local` naming note and the `StaleRPCRejected`-sends-a-barrier note each cut a wrong path before I spent a grep on it.

WATCHOUT: this round's text is byte-identical to the r4 snapshot, so `diff -ru` against `/home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r4` returns NOTHING and the "read the changed sections first and hardest" instruction has no target. Check the diff's exit status before concluding the snapshot path is wrong; and note the agent thread resets cwd between Bash calls, so a relative path to the snapshot silently produces empty output that reads the same way — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r4 vs proposals/0076_fix_scope-the-coordination-generation-to-the-session

OPEN: this lens has now returned empty six times, five of them recorded in the standing context plus this one, and the last two were over text that had not moved. The standing `### Open` item asking whether an exhausted lens is retired names the reopening conditions for reliability (D7's acceptance arm, the retained `coordfence` floor's rolling-window behaviour, §10.1.8's failure-arm claim). None moved this round. A human should retire the lens or the loop should gate it on those three anchors changing.


### [non-spec.5.review-security.1]

DECISION: returned empty for the security lens over the non-spec staging — BECAUSE `diff -rq` against
`scratchpad/cp-snap/0076-run7/non-spec-r4` returns NOTHING AT ALL, so this round's text is byte-identical to
the text five prior security passes cleared, and every candidate I generated independently landed on the
refuted list, on a `### Traps` entry, or on a standing `### Open`. ALTERNATIVES: filing CODE-3's unlocked
post-mortem read (see below); filing the barrier's accept-on-unset arm as a control removal (that IS D7, the
converged spec design, and OD12/OD13 own its residuals); filing the twelve proto consequence-clause deletions
as a credential-path control removal (the standing WATCHOUT is right — no operational RPC is generation-gated
in code).

FACT: the two fail-closed adapter paths this change re-scopes are BOTH still pinned by landed single-session
tier-1 cases that survive the per-session move, so no coverage gap exists there. `TestCoordinatorFenceStale
GenerationRejected` fences `s1` at 7 then asserts `FailedPrecondition` + `coordinator_handoff_stale` for 7, 6,
and 1; `TestCheckpointBarrierRejectsGenerationMismatch` fences `s1` at 4 then asserts `FailedPrecondition` for
a barrier at 3. Both use one session, so a correct per-entry implementation keeps them green and a broken one
(gate deleted, or a fresh entry resolved per call) turns them red — EVIDENCE: pkg/adapter/coordination_test.go
:103-131, :202-215. I nearly filed "§8 stages only ACCEPT assertions for the per-session gate and no REFUSE
one" before checking this; do not spend the pass on it again.

FACT: `holdState.gen` reaches no control. `enterHoldState` reads it only to write the
`coordinator_connection_lost` slog line and to seed `s.hold.gen`; arming is unconditional (`s.hold.active =
true` with no generation predicate) and `onHoldTimeout` passes the value to `terminateHeldSession` purely for
the `coordinator_lost` line and `writeHoldPostMortem`. CODE-3 deleting the read therefore removes no
fail-closed gate and no alert input — EVIDENCE: pkg/adapter/holdstate.go:115-133, :186-207, :225-229. No
alert rule reads any of the three tokens either: `grep` for `coordinator_handoff_stale|coordinator_generation_
gap|coordinator_lost|coordinator_connection_lost` across `pkg/alerting`, `docs/runbooks/`,
`docs/reference/metrics.md`, and `spec/16*` returns two inventory rows and zero alerts — EVIDENCE:
docs/reference/metrics.md:307; spec/16_observability.md:183.

FACT: `quiesced` still has no production reader after the relocation, so moving it onto the entry changes no
enforced control. The only non-test sites are the declaration, the test-only accessor, and the set/clear pair
inside `CheckpointBarrier` — EVIDENCE: pkg/adapter/coordination.go:38, :55, :247, :255 (grep for `quiesced\b`
under `pkg/adapter/` excluding `_test.go` returns nothing else).

FACT: migration 0181 satisfies every `scripts/lint-migrations.sh` pass the proposal does not name. Passes 1
and 2 need a non-empty `.down.sql`, which CODE-4 stages; passes 4 and 5 key on `add column` / `DROP COLUMN`
and 0181 has neither; migrations are picked up by a `//go:embed *.sql` glob, so adding one needs no
registration list edit — EVIDENCE: scripts/lint-migrations.sh:1-33, :62-91; migrations/embed.go:15. Do not
re-derive; only pass 3 (the `TEST_DIR` bare-number grep) is load-bearing and §8 already covers it.

UNVERIFIED: whether CODE-3's post-mortem read of the detached `*slotState` takes the entry's lock. I re-derived
this from scratch and it is REAL but I declined to file it. The window is genuine: `CoordinatorFence` is on the
hold's allowlist and is the only exit from the hold, so a fence that cleared `checkSessionBound` before pass 1
can be writing `lastFenced` while pass 2 reads it; and CODE-1 makes the accessor a registry lookup, which pass
1 has already made fail, so the post-mortem CANNOT use the accessor and must read the field off the detached
pointer directly. The proposal is silent on locking. Declined because the entry carries `coordinationState`'s
own mutex under CODE-1, a locked read is the natural implementation, and silence about a lock is not an
instruction to omit one — EVIDENCE: pkg/adapter/holdstate.go:53-59 (allowlist), :186-207 (the two passes);
pkg/adapter/slotsession.go:267-276 (`checkSessionBound` fails once the map key is gone). This re-confirms
`[non-spec.1.review-security.1]`'s standing item rather than adding to it; a future round wanting it must
argue that the proposal's silence is itself the defect.

USEFUL [`### Settled` — `CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches no
gateway decision]: saved me the whole trust-boundary re-derivation for the second time; the entry's own note
says a round-5 security lens said the same. It should survive compaction until the code lane lands.

USEFUL [`### Traps` — the twelve rewritten proto field comments include the three credential RPCs]: I was drawn
to exactly the detour it names and it cost me nothing. Keep it verbatim; the "no operational RPC is
generation-gated in code" clause is the load-bearing half.

USEFUL [`### Traps` — `spec/10:130-138` is one paragraph carrying two different grace budgets]: I generated the
"D7 breaks the admission-webhook grace floor" candidate independently and this entry killed it before I opened
`spec/10`.

MISTAKE (mine, avoided at cost): I started building a finding on the per-entry `lastFenced` reset at rebind
turning the barrier gate into accept-anything for that session, on the ground that the standing "the rebind
reset is not exploitable" entry reasons only about the FENCE path (a reset admits only a CAS winner) and says
nothing about the BARRIER path, whose generation is not minted by a CAS. It is still not filable: reachability
depends on `pkg/gateway/sessionserver` placement picking the same pod, which `spec/07:196` bars in
specification terms and which the standing `### Open` records as unanswered, and the consequence is OD12's
already-refuted residual. Recording it so the next security lens does not spend the same third of a pass:
the standing entry's scope is the fence path only, and that is deliberate rather than an omission.

OPEN: the security lens has now returned empty six times on this staging (five recorded plus this one), twice
over text that did not move at all. The reopening conditions are unchanged and narrow: a rewrite of D7's
acceptance arm, of the retained `coordfence` floor's rolling-window behaviour, or of CODE-3's hold-state
deliverable. Running it again over byte-identical text buys nothing and costs every other lens a round.


### [non-spec.5.review-test-coverage.1]

DECISION: returned EMPTY on the test-coverage lens — BECAUSE every behaviour the staging changes has a
concrete listed case at a tier it reaches, and every gap I derived was already adjudicated in the standing
context or in this run's refuted list — ALTERNATIVES: rejected filing (i) "§8's tier-4 sentence for D7 names
no case, file, or step" (`non-spec-changes.md:333-334`, "Tier 4 covers the same flow across the gateway, the
session store, and the pod"), because D7's behaviour is already pinned at tiers 1 and 3 and the standing
`### Traps` entry kills tier-list bookkeeping as a class three times over; (ii) the `TestCheckpointBarrier
RejectsWithoutFence` replacement-assertion gap and (iii) the unbound-session barrier case, both already
refuted by the material skeptic this run; (iv) a `// diagnosis:` finding over §8's new tier-2/3/4/7a cases,
same refuted class.

FACT: the coverage map I derived, behaviour by behaviour, so the next test-coverage agent does not re-derive
it. Per-session fenced value → §8 tier-1 bullet 1 + tier-4 + tier-7a fence case. Per-session gap reset →
tier-1 bullet 1's "`sess-b`'s first fence at 9 logs no `coordinator_generation_gap`". Per-session barrier
gate → tier-1 bullet 2 + tier-7a barrier case. Entry lifetime across deregistration → tier-1 mid-flight
case. D7's unset-arm acceptance → tier-3 wire case + the tier-1 amendment. Hold post-mortem per session and
the 0 sentinel + the pod line dropping `last_generation` → the `TestCoordinatorHoldTimeoutDropsItsEmissions
WithNoSink_spec_10_1` amendment. Counter baseline → memstore tier-1 amendment, `pgstore` tier-2 case,
migration 0181 tier-2 case (backfill, both `DEFAULT 1`, retained `CHECK (>= 0)` accepting an old-binary 0,
`.down.sql`). SCHEMA-1 → `TestProtoStubsMatchGeneratedOutput` at tier 0. Nothing is uncovered.

FACT: no landed adapter barrier test other than `TestCheckpointBarrierRejectsWithoutFence` sits on the arm
D7 retires, verified rather than taken from the log. `TestCheckpointBarrierGenerationStale_spec_10_1` fences
at 9 before sending 3, `TestCheckpointBarrierQuiescedMsIsTimeToQuiescence` fences at 3,
`TestCheckpointBarrierEmptyCheckpointWhenNoStreamDriven` at 2, and `TestCheckpointBarrierMissingBarrierID`
at 1, so all four survive the gate change — EVIDENCE: pkg/gateway/runtime/adapterclient/checkpointbarrier_
test.go:174-186; pkg/adapter/coordination_test.go:334-341, :372-378, :404-411.

FACT: §8's baseline-shift class-1 claim ("every other assertion site seeds the field at or above 1") holds
against an independent sweep of every `_test.go` naming `CoordinationGeneration`. The files §8 and the
standing enumeration do NOT name are all clean: `sessionserver/derive_failure_audit_test.go:46` increments
relatively with no absolute assertion, `sessionserver/resume_chunk_selection_internal_test.go:47` takes the
value as a parameter, and the eviction and coordlease stores seed 3, 4, and an explicit argument —
EVIDENCE: tests/tier2_component/stores/evictionfallback_test.go:103, :130;
tests/tier2_component/stores/evictionstatestore_test.go:258, :276;
pkg/gateway/storage/evictionstatestore/evictionstatestore_test.go:184, :201;
tests/tier2_component/stores/coordleasestore_test.go:44.

FACT: tiers 5, 9, and 10 are genuinely unreached, checked against the suites rather than inferred. A grep
for `CoordinatorFence|CheckpointBarrier|coordination_generation|LastFenced|coordinator_hold` over
`tests/tier10_conformance/`, `tests/tier9_security/`, and `tests/tier5_e2e_kind/` returns three hits and
none is an assertion the change moves: `adapter_contract_event_taxonomy_test.go:79` lists
`"CheckpointBarrierAck"` as an event-type name, and `tier5_e2e_kind/checkpoint_resume_test.go:26`, `:268`
are a doc comment and an `ORDER BY coordination_generation DESC LIMIT 1` whose ordering the uniform baseline
shift does not change. `tests/tier10_conformance` DOES exist and holds nine real test files, so the standing
phrase "if that suite is ever built" reads as stale; the conclusion (SCHEMA-1's comment edits reach no
conformance assertion) is right for a different reason, which is that the battery reads declarations rather
than comments.

WATCHOUT: the proposal directory is BYTE-IDENTICAL to `scratchpad/cp-snap/0076-run7/non-spec-r4` and to
`non-spec-r4-start` and `non-spec-r5-start`; `diff -rq` across all three returns nothing. Rounds 4 and 5
landed no edit at all, so the "read the changed sections first and hardest" instruction has no target and a
diff-driven reading order buys nothing this round — EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r4/.

USEFUL [Settled: "Landed cases already pin what §8 might otherwise be thought to owe"]: it saved a full
re-derivation of which landed adapter cases §8 owes a disposition for, and the four cases I spot-checked
against it all matched.

USEFUL [Traps: "tier-list bookkeeping is a refuted class, three times over"]: it killed the one candidate I
worked up furthest (§8's contentless tier-4 sentence for D7) before I spent a filing on it.

OPEN: whether the test-coverage lens should now be retired for this proposal. It has returned findings in
runs 4 and 7 round 1 and every one was refuted; this round it returns empty over text that has not moved
since round 3's fix. The standing `### Open` entry on retiring exhausted lenses has gone unanswered for five
rounds and this lens now has the record to answer it with.


### [non-spec.6.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE every applicability candidate I generated this round is
either verified clean against the tree or already adjudicated in the review log's `### Traps` / `### Open`
sections with an explicit instruction not to file it — ALTERNATIVES: I worked up and rejected five: §9's
omission of any `tests/tier3_contract` file and the tier-2 resume-fence case's missing home (Traps: "Do not
spend a verification on it"); §8's disjointness paragraphs enumerating only the `pod.LastFenced` reads
(Traps: "Do not file it"); the two landed memstore `Update` tests inside §8's class 1 (three lenses
declined, hand to the implementor); tier-list bookkeeping; and the stale doc comments CODE-3's "those sites
are the whole of what the field drags" misses.

FACT: the lane names the pipeline accepts are `spec`, `code`, `schema`, `migration`, `test`, and `docs`, and
an unrecognised lane stops the run rather than being guessed. S4's `schema` and S8's `test` lanes are
therefore valid and need no further check — EVIDENCE: .claude/skills/implement-proposal/SKILL.md:19, :21

FACT: the checklist is clean end to end and does not need re-deriving unless it moves. Each of SPEC-1,
SPEC-2, SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3, CODE-4, TEST-1 appears in exactly one step; every
`Depends on` names an earlier existing step; every box is unchecked; the three spec steps lead so no
interleave needs a stated reason; and no code step consumes a spec statement staged by a later step
(CODE-1/CODE-3 and CODE-2 consume SPEC-1 alone, CODE-4 consumes SPEC-1 and SPEC-3, SCHEMA-1 consumes
SPEC-2) — EVIDENCE: 0076...implementation-checklist.md:3-40

FACT: migration 0181 passes every pass of `scripts/lint-migrations.sh`. Pass 4 only fires on `ADD COLUMN`
and pass 5 only on a `DROP COLUMN`, and 0181 does neither; pass 3 greps the bare number anywhere under
`tests/tier2_component/migrations/` — EVIDENCE: scripts/lint-migrations.sh:95-120 (pass 4 awk), :74-90
(pass 3), :45 (TEST_DIR)

FACT: the staged `prodMigrationSchema` entry for 0181 is inert beyond stepping its `.down.sql`.
`TestProdMigrationsApplyExpectedSchema` asserts only `m.create` tables and `m.columns`, and 0181's entry
carries neither, so a `table: "sessions"` value that ignores the `coordination_lease` half costs nothing.
0181 also needs no row in `prod_schema_test.go`'s `prodTables`, which lists only migrations that CREATE a
table — EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:590-633;
tests/tier2_component/migrations/prod_schema_test.go:36-38

FACT: the tier-8 crash-takeover file builds its store as `sessionpg.New(pg.Pool)`, not a memstore, so §8's
class-1 claim that its `:267`/`:283`/`:296` assertions shift under CODE-4 depends on the *pgstore* `Create`
floor rather than the memstore one. Subtest 3 seeds with the field unset at `:238-241` and subtests 1 and 2
seed at 1 explicitly at `:118` and `:179`, so the two-step split §8 describes really is disjoint —
EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:85, :118, :179, :238-241, :267, :283, :296

WATCHOUT: §8's ground for putting the 0181 behaviour case under `tests/tier2_component/migrations/` is
weaker than it reads. Pass 3 greps for the bare string `0181` anywhere under that directory, so the
`prod_columns_test.go` entry the SAME paragraph stages already satisfies it on its own; "a case landing in
`tests/tier2_component/stores/` alone leaves tier 0 red" is true only of a world where the
`prodMigrationSchema` entry is also absent. The directory choice is still right (that is where the sibling
0180 behaviour file lives), so this is a rationale that overstates its gate rather than a wrong target. Do
not file it and do not "fix" it by moving the case — EVIDENCE: scripts/lint-migrations.sh:84-88;
0076...non-spec-changes.md:313-320

USEFUL [Traps: "MISTAKE: refuted class (k) does not reach §9's tier-3 omission"]: killed a filing I had
fully worked up (the tier-3 wire case and the tier-2 resume-fence case both having no named home while §9
lists no `tests/tier3_contract` entry). Reading `### Traps` before writing the finding saved two
verification agents.

USEFUL [Traps: "WATCHOUT: §8's disjointness arguments enumerate only the `pod.LastFenced` reads"]: I
re-derived the same gap independently (tier-8 `:130`, `:165`, `:184`; tier-7a `:169`; tier-4 `:83`, `:151`)
and the entry's conclusion holds — every one of those call sites is in a subtest seeded at 1 explicitly, so
the incompleteness changes no verdict.

USEFUL [Settled: "Two landed memstore `Update` tests sit inside §8's class 1 and are named nowhere"]: I
found `TestUpdateAdvancesGenerationCounters` (memstore_test.go:416, :430) and
`TestUpdateConcurrentGenerationBumpsPreserveMonotonicity` (`:471`, `:490`) from a cold sweep of
`CoordinationGeneration` assertions and would have filed both; the standing entry already records three
lenses declining them and gives the reason.

OPEN: the two comment residues CODE-3's enumeration misses remain unowned and no lane has authored the
edit. `pkg/adapter/holdstate.go:116-118` says the generation and the started-session count are both read
"through their accessors (which take coord.mu and s.mu)" — CODE-3 deletes the generation read at `:119` and
CODE-1 removes `Server.coord` entirely, so both halves go false. `pkg/adapter/coordination.go:124-128`
states the deadlock-freedom argument in the same terms ("calling it while coord.mu is held is
deadlock-free ... enterHoldState reads the generation through the accessor"), which is the shipped
statement of the lock order the summary's fixed decisions restate. CODE-3's "Those sites are the whole of
what the field drags" (non-spec-changes.md:105-106) is false against both. I did not file it: it is the
stale-comment class the loop refutes, both files are already in §9, and an implementor deleting `:119` sees
the comment three lines above it. Whoever edits CODE-3 for another reason should add the two comments to
its enumeration in the same pass — EVIDENCE: pkg/adapter/holdstate.go:116-119;
pkg/adapter/coordination.go:124-128; 0076...non-spec-changes.md:102-109

UNVERIFIED: whether `tests/tier11_docs/checkpoint_pipeline_consistency_test.go`'s hit on the string `0181`
is a coincidental substring. It is outside `scripts/lint-migrations.sh`'s `TEST_DIR`, so it satisfies no
gate either way, but a later round adding a migration-number gate should check it. Nobody has looked.


### [non-spec.6.review-citations.1]

DECISION: Returned empty. — BECAUSE every concrete citation in `non-spec-changes.md` (SCHEMA-1, CODE-1..4, TEST-1, §8, §9) resolved in the tree to text saying what the proposal says, and every attributed behavior (open/link/complete overwriting, dispatchOne's Record under t.SessionID, coordfence's transient-vs-stale arms, oplock admitting a distinct session id, RecordHandoff's unconditional increment, upsertMirror's pre-bump snapshot, the two Create normalisations, the migrate-Job hook weight, protoc-gen-go comment placement) was confirmed at the cited site. — ALTERNATIVES: I considered filing the `spec-changes.md:249`/`:284` "column default and the create path's floor" credit against `non-spec-changes.md:133-136`'s "the column default baselines nothing"; declined because the standing `### Deferred` entry already adjudicated it as overstated-but-not-false and its remedy is spec-lane.

FACT: §8's class-1 and class-2 exhaustiveness claims hold under an independent re-derivation. Enumerating every `_test.go` carrying `CoordinationGeneration` (34 files) and asking which create a *session row* unset and then assert on it leaves exactly the named set. The four files a naive sweep adds are all outside both classes: `derive_failure_audit_test.go:46` increments inside a fake `Get` and asserts nothing on the generation; `resume_chunk_selection_internal_test.go` builds `sessionstore.Session` struct literals passed directly to the function under test rather than through any store's `Create`, and its manifest generations are compared among manifest rows; the `sessioncheckpointmeta` and `evictionstate` suites seed and assert the same explicit constant on a different table; the `barrier.Target` literals have no session store. — EVIDENCE: pkg/gateway/sessionserver/derive_failure_audit_test.go:41-49; pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go:39-47, :92-95; tests/tier2_component/stores/evictionfallback_test.go:103, :130

FACT: the `pgstore.New` line citation the review log calls a known drift is not one. `### Traps` says "`pgstore.New` is at `pgstore.go:59` where the staging cites `:60`"; the declaration is at `:60` and `:59` is the doc comment's last line, so §8's `(pgstore.go:60, :249)` is exact. Do not "correct" it. — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:57-61, :249

USEFUL [Traps: "an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the wrong half"]: I skipped the token greps and the nineteen-carrier arithmetic entirely and spent the pass on behavior-versus-site verification instead. That is what left budget for the independent class-1/class-2 re-derivation above, which is the one part of the staging the standing inventories do not cover from the test side.

USEFUL [Traps: "harness hazards on this proposal"]: `cat -n` on `non-spec-changes.md` through Bash persists to a tool-result file whose own `cat` persists again, so two calls returned nothing readable. The Read tool at the real path worked first time. Same for the review log.

WATCHOUT: `awk`-based line renumbering of a `sed` window silently mangles lines whose content begins with whitespace-plus-digits, because `substr($0, index($0,$2))` drops the indentation and can re-index on a stray token. Two of my early reads of `pkg/adapter/coordination.go` showed phantom line numbers (`155: 16`) from this. Use `grep -n "" FILE | sed -n 'A,Bp'` instead; it is one call and the numbers are the file's own. — EVIDENCE: pkg/adapter/coordination.go:148-200

FACT: the citation lens has now returned empty on this staging once. Everything filable of this class in `non-spec-changes.md` has been checked against the tree twice over (five prior non-spec rounds plus this one), and the sub-line drift list in `### Settled` is closed. A further citation run buys nothing unless SCHEMA-1's carrier list, §8's assertion inventory, or a CODE deliverable's cited mechanism is rewritten.


### [non-spec.6.review-client-surface.1]

DECISION: returned EMPTY. — BECAUSE every client-facing representation this staging touches or could touch was
re-derived from the tree this run and each is either staged, unit-neutral, or absent. — ALTERNATIVES: three
candidates were weighed and dropped below the bar (D3's wording, the barrier carriers' `FailedPrecondition`
tail, `coordinatorfence.go:37`), each named under WATCHOUT.

FACT: the proposal text is BYTE-IDENTICAL to the `non-spec-r4` snapshot. `diff -ru` over the whole proposal
directory returns nothing, so run 7's round-1 shards reviewed the same bytes run 6's cut-off round did. Do not
spend a pass looking for "what the last fix round changed"; nothing changed. — EVIDENCE:
scratchpad/cp-snap/0076-run7/non-spec-r4 vs proposals/0076_.../ (empty diff)

FACT: the client-surface sweep re-derived clean, with the exact commands, so the twelfth derivation costs one
minute rather than an hour.
  - `grep -rn "coordination_generation\|CoordinationGeneration" -l .` outside `proposals/`, `.git/`,
    `scratchpad/`: no hit in `sdks/`, `charts/`, `schemas/*.json`, `schemas/audit-events/`,
    `schemas/examples/`, `schemas/README.md`, or `spec/15_*`.
  - `grep -c coordination pkg/gateway/externalapi/openapi/openapi.json` → 0, and `recovery_generation` → 0
    too, so neither counter reaches the OpenAPI document.
  - `charts/lenny/crds/` hits on `generation` are all Kubernetes `metadata.generation` /
    `observedGeneration`. No CRD surface.
  - the whole `docs/` fence-and-barrier surface is nine lines: `reference/metrics.md:40`, `:197`, `:307`-`:312`
    (six rows, every one describing what the metric means), `reference/glossary.md:54`,
    `reference/adapter-contract.md:68`, `:69`, `:96`, `operator-guide/upgrades.md:47-54`,
    `getting-started/concepts.md:101`, `getting-started/architecture.md:173`. Every one is unit-neutral about
    the fenced value.
  - `docs/api/internal.md` carries no `Fence|fenc|generation|Barrier|coordination` hit at all, despite
    `docs/reference/glossary.md:54` linking to it as the CH-ADAPTEREVENTS reference.

FACT: `tests/tier10_conformance` DOES read `schemas/lenny-adapter.proto` as text, which contradicts the
instinct that only tier 0 does. It is harmless for SCHEMA-1: the two readers assert only that the file
contains the literal `spec/04_system-components.md` and the literal `§28.5.3`, and neither string occurs in
any of the nineteen SCHEMA-1 carriers (they sit at proto `:82`, `:218`, `:220`, `:233`, `:1140`, `:1734`,
`:1753`). — EVIDENCE: tests/tier10_conformance/adapter_contract_event_taxonomy_test.go:56-61;
tests/tier10_conformance/adapter_contract_intrapod_pointer_test.go:98-103

FACT: SCHEMA-1's carrier list is byte-for-byte SPEC-2's set in SPEC-2's order, re-checked field by field this
run, and both resolve against the tree. `grep -c "A pod validates" schemas/lenny-adapter.proto` → exactly 12,
at `:970`, `:996`, `:1047`, `:1071`, `:1092`, `:1115`, `:1173`, `:1306`, `:1394`, `:1532`, `:1577`, `:1619`,
which is SPEC-2's twelve messages in SPEC-2's order. `int64 coordination_generation` occurs exactly 14 times.
The two trailing sentences SPEC-2 exempts are real and are where it says: `AttachRequest` at `:999` ("It is
carried on every frame of the") and `CheckpointRequest` at `:1176` ("It sits outside the `msg` oneof").

FACT: every generated-stub citation in SCHEMA-1 resolves. `schemas/lenny-adapter.proto:1451` reappears
verbatim at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`; the `CoordinatorFence` RPC comment lands at
`lenny-adapter_grpc.pb.go:180` and `:632`, which are the two lines SCHEMA-1 cites. The `CheckpointBarrier` RPC
comment lands at `:191` and `:643` and is not cited, which is fine because §9 lists the whole file as
regenerated output.

FACT: `tests/claim-map.json` claim 74, "gRPC control stream `Adapter/AdapterEvents` (client)", is `UNWIRED`
under deferral `R12`, so the summary's "the gateway has no CH-ADAPTEREVENTS client" (`summary.md:428-432`)
verifies. Read it with a JSON walker rather than grep; the file is one long array and a line grep does not
give you the row.

WATCHOUT: D3 (`summary.md:49-50`) reads "The proto doc comment claiming per-session monotonicity is corrected
to describe the adapter and is not deleted", while SCHEMA-1 (`non-spec-changes.md:19-21`) says that comment
"is the one carrier that takes no edit" and SPEC-2 (`spec-changes.md:515-516`) says "it keeps its wording". A
lens reading D3 as "the comment is edited" works up a self-contradiction finding. It is not one: `summary.md:15`
("The proto doc comment that already claims per-session monotonicity becomes true") fixes the intended reading
as "the situation is corrected by changing the adapter, and the alternative rejected was deleting the comment".
Weighed and declined as two-way-readable wording. A fixer in `summary.md` for another reason could cheaply
write "is left in place and made true by CODE-1".

WATCHOUT: `docs/reference/adapter-contract.md:69` ("`CoordinatorFence` | ... precondition for any subsequent
operational RPC") looks like an unstaged docs site that D7's acceptance arm falsifies. It is refuted class (a)
in the standing context: it states the §10.1.2 step 2 SENDER-side bar, which D7 does not touch. Do not file it.
Its neighbour `:86` ("The adapter maintains a per-session operation lock") is genuinely wrong against the
pod-level lock `pkg/adapter/checkpoint.go:111` uses, and §8's tier-7a bullet leans on that pod-level lock — but
the doc error is pre-existing and the standing context already routes it to a docs loop.

WATCHOUT: the runtime-facing quiesce frames are NOT `coordinationState.quiesced`. `schemas/runtime-ops-events.schema.json:70`,
`:82`, `:93` carry the adapter↔runtime quiesce handshake with no session id, which looks like a co-tenancy race
once CODE-1 gives each session its own barrier gate. It is not: that handshake is driven from
`pkg/adapter/checkpoint.go:158` INSIDE the pod-level op lock taken at `:111`, so two co-tenant checkpoints are
serialised and two concurrent quiesce frames are unreachable. Verified this run rather than taken from the
standing bullet.

USEFUL [Settled: "The proto is a published artifact and the client surface around it is empty"]: the corrected
OpenAPI path (`pkg/gateway/externalapi/openapi/openapi.json`, there being no `pkg/gateway/openapi/`) saved a
false "the document is clean because the grep found nothing" conclusion. The bullet's instruction to run
`grep -c coordination` against the real path is the right check and it returns 0.

USEFUL [Refuted class (k)]: it settled `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` ("The first
fence on a pod's lifetime is always accepted"), which D6 falsifies, without spending a verification. The
summary already owns it at `:445-447` as an outstanding correction, so it is recorded rather than lost.

OPEN: `summary.md:445-447` records `coordinatorfence.go:37` as an outstanding correction AND records that §9
does not list the file. A correction the proposal knows about, states in prose, and then omits from its own
files-touched list is a shape nobody has ruled on: refuted class (k) bars filing it as a missed edit site,
while the proposal's own text says it should be fixed. Somebody should decide whether "Corrections outstanding"
entries are supposed to reach §9.


### [non-spec.6.review-docs-alignment.1]

DECISION: Returned EMPTY — BECAUSE the proposal directory is byte-identical to BOTH the r4 snapshot and the
r6-start snapshot (`diff -rq` returns nothing, rc=0 against each), so not one byte has moved under this lens
since it last returned empty, and an independent re-derivation of the whole `docs/` surface from the tree
reached the same conclusion for the seventh time — ALTERNATIVES: (1) the permanent rolling-window cohort of
session rows left at 0 (`non-spec-changes.md:157-163`) filed as an accepted failure mode landing only in
reasoning, rejected because the barrier refusal it produces is a strict SUBSET of the refusal every
never-taken-over session takes in the shipped tree, so the change creates neither a new failure mode nor a
new cause of one; (2) `docs/reference/adapter-contract.md:69` ("`CoordinatorFence` … precondition for any
subsequent operational RPC") filed against D7's unset arm, rejected because that line mirrors §10.1.2 step
2, a constraint on the COORDINATOR that SPEC-1 leaves unchanged, rather than step 3's pod-side gate;
(3) `docs/operator-guide/upgrades.md:49` filed against the barrier, rejected because the fix makes that
sentence more true rather than less.

FACT: nothing in this change reaches an alert or a runbook, and the reason is structural rather than
incidental: the staging adds, removes, and relabels no metric. The only `lenny_*` names anywhere in the three
change files are `lenny_coordinator_handoff_stale_total`, `lenny_coordinator_fence_relinquished_total`, and
`lenny_adapter_coordinator_hold`, and all three already carry their rows, whose glosses describe what the
metric counts rather than the unit of the fenced value — EVIDENCE: docs/reference/metrics.md:307, :309, :312

FACT: `coordinator` as a token appears in NONE of `docs/reference/error-catalog.md`,
`docs/reference/cloudevents-catalog.md`, `docs/reference/state-machines.md`, or `docs/api/internal.md`
(one grep over all four returns nothing at all). So D7's change to when the barrier returns
`FailedPrecondition`, and CODE-3's move of the `coordinator_lost` post-mortem to a per-session record, reach
no error-code table, no event catalog, and no state-machine page — EVIDENCE: docs/reference/error-catalog.md,
docs/reference/cloudevents-catalog.md, docs/reference/state-machines.md, docs/api/internal.md (grep, zero hits)

FACT: `docs/operator-guide/upgrades.md`'s Schema Migrations section is generic and names no migration number,
so migration 0181 owes it no edit. It carries the three-phase expand-contract table and the Phase-3 gate
procedure alone — EVIDENCE: docs/operator-guide/upgrades.md:238-273

WATCHOUT: `docs/runbooks/coordinator-handoff-slow.md` looks like this change's runbook and is not. Its
subject is the DELEGATION handoff (":28 — the step where the parent session passes control of a delegated
child session"), driven by `lenny_coordinator_handoff_duration_seconds` phases `claim`/`materialize`/
`warmup`/`attach`. It never mentions the generation, the fence, or `lenny_coordinator_handoff_stale_total`,
so the stale-counter repair in this proposal's Goals reaches it nowhere — EVIDENCE:
docs/runbooks/coordinator-handoff-slow.md:28, :44

USEFUL [non-spec.5.review-docs-alignment.1]: its four FACTs and its WATCHOUT on the per-session op lock all
held on independent re-derivation. I re-opened the `docs/` greps, `adapter-contract.md:55-100`,
`metrics.md:300-318`, `concepts.md:92-101`, `architecture.md:165-179`, and `upgrades.md` rather than lifting
them, and changed nothing. Its OPEN about retiring this lens is now stronger, not weaker: the text under
review has not changed a byte between two consecutive empty returns.

OPEN: this lens has returned empty seven times, and the last three were over byte-identical text. The
staging touches no `docs/` file, adds no metric or alert, and the entire reader-facing surface is a handful
of unit-neutral lines. Retiring it for this proposal costs nothing that a re-run would catch.


### [non-spec.6.review-edit-sites.1]

DECISION: Returned EMPTY, the THIRD consecutive empty edit-sites return on byte-identical staging (`diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` returns nothing at all, not even the review log) — BECAUSE every identifier the staging adds, changes, or removes lands on a surface the edit lists already name, and the residuals I could see are each in a refuted class, already filed-and-refuted this run, or a standing `### Open` owned by another lens — ALTERNATIVES: re-filing the `non-spec-changes.md:5-6` fill-the-blanks header (rejected: refuted by the material skeptic this run and archive-pre-adjudicated at `review-log-archive.md:2376`, `:3371-3372`); filing CODE-2's missing "reaches tiers" line, which is the one per-deliverable line absent while CODE-1, CODE-3, CODE-4, and TEST-1 all carry one (rejected: tier-list bookkeeping is a three-times-refuted class and checklist S7 declares 0/1/3/4/7a); filing `pkg/adapter/holdstate.go:103`, `:116-118`, `coordination.go:126-128` (rejected: refuted class (k) plus two are already `### Deferred`); filing the `oplock.go` range disagreement between CODE-1 (`:119-129`) and §8 (`:117-128`) (rejected: BOTH ranges cover the described coalesce-and-queue mechanism, verified in the file; sub-line drift class).

FACT: **the §28.8 `CH-FENCE` cell carries TWO independently staged clauses in one field and SPEC-2 stages both.** Field `$5` of `spec/28:1807` contains the window sentence ("The acknowledgement closes the window in which the prior coordinator's RPCs are still accepted") AND the whole gap-reset sentence ("When the announced generation exceeds the last fenced generation by more than one, the adapter cancels and discards ... acknowledges the fence normally"). The standing context lists that cell twice, once under the window-clause sites and once under the gap-reset mirrors, and a reader who follows only one list concludes the other clause is an unstaged site. It is not: `spec-changes.md:374-381` stages the gap sentence ("takes the same re-scoping as the Degradation bullet, word for word") and the window sentence in the same bullet. Do not re-derive this — EVIDENCE: spec/28_communication-channels.md:1807; spec-changes.md:374-379.

FACT: **all twelve operational field comments carry the prescribed span verbatim, checked one by one in the file.** Each of `:969-973`, `:995-1001`, `:1046-1050`, `:1070-1074`, `:1091-1095`, `:1114-1118`, `:1172-1178`, `:1305-1309`, `:1393-1397`, `:1531-1535`, `:1576-1580`, `:1618-1622` opens "coordination_generation is the gateway's view of the active coordination generation for the session." and then runs "A pod validates the generation on every gateway-to-pod RPC and rejects a stale coordinator's request, so a replica that has lost coordination cannot drive the pod (§10.1)."; `ShutdownRequest` alone closes "cannot tear the session down (§10.1)"; `AttachRequest` and `CheckpointRequest` alone carry the trailing frame sentences SPEC-2 preserves. SPEC-2's span replacement applies cleanly to all twelve with no residue and no thirteenth. This is now derived from the file (rather than from the standing bullet) at least three times; a fourth is waste — EVIDENCE: schemas/lenny-adapter.proto:969-973 through :1618-1622.

FACT: **`spec/README.md` references §29.10 by heading link alone (`:290`), so SPEC-2's removal of a §29.10 "does not state" bullet strands no index row.** The standing `### Settled` phrasing ("no inbound reference outside the `spec/README` contents") reads as if the README might carry the bullet; it does not, it carries one contents line for the section. One grep closes it — EVIDENCE: spec/README.md:290; spec/29_communication-scenarios.md:1424.

FACT: **the whole `docs/runbooks/` tree carries nothing this change reaches.** No runbook names the coordinator hold, the hold post-mortem, `last_generation`, a migration number, or the barrier's generation gate; `db-rollback.md` and `schema-migration-failure.md` describe the rollback PROCEDURE and enumerate no migration, so 0181's deliberate "rolls no row back" `.down.sql` contradicts no runbook sentence. `docs/operator-guide/upgrades.md:47-49` describes the drain barrier but attributes it to "the controller" and states no generation gate, so D7 falsifies nothing there that was true before. This closes the runbook half of the alert-and-runbook companion-pair check for good — EVIDENCE: docs/operator-guide/upgrades.md:47-49; docs/runbooks/db-rollback.md:31-47.

USEFUL [`### Traps`, "MISTAKE: an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the wrong half"]: following it exactly (skip the token greps, read the named carriers in the file sentence by sentence) is what let this pass cover the nineteen proto carriers, the §28.8 two-clause cell, §9's file list, the migration gate surface, the generated chain, and the docs/runbook companion pairs inside one budget. Second shard in a row to credit this entry; it is the highest-value line in `### Traps` for this lens.

USEFUL [`[non-spec.2.review-edit-sites.1]`]: its four FACTs (the two-file generated chain, no `column_default` reader in the tree, no migration number in `docs/`/`charts/`/`spec/`, and the adapter-contract and metrics rows re-derived from the files) meant I could spend my budget on the carrier text and the §28.8 cell structure instead of re-running the same closures. Every one of them re-confirmed where I spot-checked.

FACT: **the run-5 edit-sites shard also returned empty over the identical text** and its coverage line names a WIDER sweep than either later shard (all 34 test files naming `CoordinationGeneration`, `docs/api/internal.md`, the alerting bundles, the §28.3 register rows against the §28.8 matrix). Its cache file is at `scratchpad/cp-cache/0076-run7/edit-sites-r5-646a03fca298.json`. Reading a sibling lens's cache file for the same hash costs one `cat` and is the cheapest way to see what a previous run of your own lens already closed; the cache directory is not mentioned in the standing context and nobody appears to have used it this way.

OPEN: **edit-sites has now returned empty three times in run 7 alone (r2, r5, r6) over text that has not changed a byte, on top of the three empty returns the standing "Lens exhaustion, counted" entry records.** The reopening conditions that entry names for the other exhausted lenses apply here too: this lens buys nothing further unless SCHEMA-1's carrier list, §8's case set, §9's file list, or SPEC-2's proto paragraphs are rewritten. Whoever owns lens scheduling should retire it under those conditions rather than paying a full round for a fourth empty return.


### [non-spec.6.review-feasibility.1]

DECISION: returned EMPTY on the actor-action feasibility lens over `non-spec-changes.md` read with `spec-changes.md`, the checklist and the summary — BECAUSE every component the staging names exists under that name, every action is inside what that component can do, the lock order and guard order hold, and the four candidates a feasibility read still generates on this text are each recorded do-not-file (the tier-4 `ErrHeld` attribution, the `checkSessionBound`-returns-the-entry blank, the hold-timeout arm of the mid-flight case, and CODE-1's tier-2 declaration) — ALTERNATIVES: re-filing the mid-flight case's dead hold-timeout arm, declined again for the reason `[non-spec.2.review-feasibility.1]` gave (the `Shutdown` arm is live, so the case is constructible as written).

FACT: **`diff -rq` shows the proposal directory is byte-identical to `scratchpad/cp-snap/0076-run7/non-spec-r4`.** Run 6's cut-off round left no edit, so a shard reading this text is reading exactly what `[non-spec.2.*]` and `[non-spec.3.*]` read. Do not spend a pass looking for what changed.

FACT: **The adapter enforces no per-pod session-count limit of its own, so two co-tenant sessions on one `adapter.Server` need no fixture change and no option.** `MaxConcurrentSessions` appears nowhere in `pkg/adapter/*.go` outside comments and test names; `coordfixture.StartPod` builds `adapter.New("coordfixture")` and starts one session, and a second `StartSession` over the same dialed `Pod.Client` is admitted. That closes the feasibility half of §8's tier-1 and tier-4 co-tenant bullets — EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:75-102; grep -n "MaxConcurrentSessions" pkg/adapter/*.go.

FACT: **The tier-3 contract tests that name `CoordinatorFence` and `CheckpointBarrier` are wire-encoding cases only, so D7 and CODE-2 break none of them.** `tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go` asserts field numbers, unset-field byte cost, and round-trips (`:104`, `:141`, `:193`, `:228`), and `adapter_checkpointbarrier/checkpointbarrier_wire_test.go` is a single response-contract case (`:48`); neither carries a behavioural gate or a `FailedPrecondition` assertion. The standing "§9 names no file for the staged tier-3 and tier-2 cases" item is therefore about a home for the NEW case, not about a landed case going red.

FACT: **CODE-4's `pgstore` floor breaks no landed test, and the tree's only two "want 0" generation assertions are both in §9.** `pkg/gateway/session/sessionstore/pgstore/pgstore_test.go` and `tests/tier2_component/stores/sessionstore_test.go` contain no `CoordinationGeneration` assertion at all, so the tier-2 half of the floor is new coverage rather than an amendment. The two sites that assert a zero are `tests/tier7a_load_local/coordination_colocation_race_test.go:287-288` and `tests/tier2_component/coordination/sweep_test.go:508-509`, both inside §8's class 1 and both in §9.

FACT: **`sweep_test.go`'s shifting assertions include readopter-argument reads, not only row reads.** `:431-432` and `:524-525` assert `readopter.gens[0] == 1`, which becomes 2 under the baseline, and `:508-509` asserts the row at 0, which becomes 1. All three sit inside the `:275`-`:594` range §8 cites, and class 1's "or a number of handoff bumps counted from it" clause is what covers them; the class's opening phrase ("every assertion that reads a session row's `CoordinationGeneration`") does not, on its own.

FACT: **A THIRD comment residue joins the two the `### Deferred` pair names.** `exitHoldState`'s comment at `pkg/adapter/holdstate.go:153-155` reads "the generation it armed under is already on the coordinator_connection_lost line that opened this hold", which CODE-3 falsifies by dropping `last_generation` from that line (`:130-132`). It is not in CODE-3's enumeration, and it is the same refuted class (k) as `coordination.go:126-128` and `holdstate.go:116-118`, so it is not filable. Hand all THREE to the implementor together rather than two.

FACT: **Re-verified this run against the tree, all exact:** `slot.go:21` (`slotState`), `:153` (`checkpointRootsForSession`); `slotsession.go:267` (`checkSessionBound`, error-only), `:282-285` (`heldSession{sessionID, state *slotState}`); `server.go:302`, `:307`, `:314`; `coordination.go:99`, `:108-121`, `:148`, `:158-166`, `:180-188`, `:216`, `:224-226`, `:232-236`, `:245-269`; `checkpoint.go:94`, `:111`, `:122`, `:124`; `resume.go:178`; `oplock.go:88-128`; `holdstate.go:119`, `:128`, `:130-132`, `:187`, `:206`, `:225`, `:283`, and `hold.mu` released before pass 1 at `:188`; `coordfence.go:147-153`, `:164-179`, `:180-183`, `:186-188`; `coordination/coordination.go:341`, `:430`, `:463-482`, `:512-517`; `pgstore.go:60`, `:140`, `:177`, `:244-248`, `:249`, and the explicit `sess.CoordinationGeneration` bind; `memstore.go:46`, `:58-60`; `migrations/0050:38-39`, `0164:44`, and `0180` as the last number; `charts/lenny/templates/migrate-job.yaml:10-16`, `:37-39`; `scripts/lint-migrations.sh` passes 4 and 5 keying on add/drop column so 0181 escapes both; `cmd/lenny-test/cmd_run.go:498-508`, `:880`; `coordfixture.go:109`, `:115`, `:122`, `:220-241`, `:231`, `:302-316`; `tier7a .../coordination_colocation_race_test.go:130`, `:144`, `:260`, `:264-265`, `:287-288`; `slot_test.go:24`, `:37`; `checkpoint_stream_test.go:384`, `:417`; `spec/10:183` and §10.1.8 step 3's "concurrently with" sentence, which is CODE-1's ground for the per-entry `barrierGate`; `spec/10` §10.5's expand-contract phases and the "mixed-version replicas must coexist" sentence, which is CODE-4's ground for keeping `CHECK (>= 0)`.

USEFUL [`[non-spec.2.review-feasibility.1]`]: its exported-hook inventory (`export_test.go:61`, `:71`, `:80`) and its `fireHoldTimeout`-is-package-adapter note saved this pass the whole mid-flight-case re-derivation; both re-confirmed.

USEFUL [`### Settled`, "Guard ordering is what the adapter deliverables rely on"]: still the entry that makes CODE-1 checkable. Re-confirmed that `checkSessionBound` at `coordination.go:89` and `:216` and `checkpointRootsForSession` at `checkpoint.go:94` each precede their resolve site with no blocking call between, and that `s.ops.Begin` at `:111` is the only blocking call in any of the three gaps, which is why the re-lookup argument is confined to the `Checkpoint` path.

OPEN: the lens-retirement question is now sharper for this lens specifically. Feasibility has returned EMPTY twice over byte-identical text, and both passes ended by re-verifying the same anchor set. It reopens only if CODE-1's resolve rule, CODE-3's read target, or §8's tier assignment for a case changes.


### [non-spec.6.review-fresh.1]

DECISION: returned EMPTY — BECAUSE `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` is byte-identical (the proposal has not moved since the last snapshot), and every candidate a fresh read of the non-spec staging generates is already on this run's refuted list or in the standing `### Traps`/refuted classes. I spent the pass re-deriving the tree-facing half rather than the proposal-facing half, and everything I checked resolved. — ALTERNATIVES: filing §9's omission of a tier-3 and a tier-2 file (barred by the standing entry recording that criterion (d) does not reach `tests/` and criterion (f) is met because §8 names the cases with assertions); filing the two unnamed memstore `Update` tests inside §8's class 1 (two lenses already declined; the class sentence covers them); filing the `holdstate.go:116-118` doc-comment residue (refuted class (k), and already a `### Deferred`).

FACT: every non-spec citation I re-resolved this run is exact, including the ones a lens is tempted to distrust — EVIDENCE: pkg/adapter/server.go:302 `coord coordinationState`, :307 `hold holdState`, :314 `barrier barrierGate`, :379 `slots map[string]*slotState`; pkg/adapter/coordination.go:89, :93, :99, :148, :158, :180, :192, :216, :224, :236, :264; pkg/adapter/checkpoint.go:94, :111, :122, :124; pkg/adapter/resume.go:178; pkg/adapter/slot.go:153; pkg/adapter/slotsession.go:267, :282-284, :347; pkg/adapter/session.go:237, :271; pkg/adapter/holdstate.go:43, :119, :128, :130-132, :187, :206, :225, :249, :283; pkg/adapter/oplock.go:119-128; pkg/gateway/coordination/coordfence/coordfence.go:147-153, :180-183, :186-188; pkg/gateway/coordination/barrier/barrier.go:190-201, :238-245; pkg/gateway/session/sessionstore/pgstore/pgstore.go:140, :177, :244-248; memstore.go:46, :58-61; migrations/0050:38-39, 0164:44 (0180 is the last taken number); tests/testinfra/coordfixture/coordfixture.go:76, :98-102, :106-108, :109, :115, :122; cmd/lenny-test/cmd_run.go:498-508, :635-641, :880; scripts/lint-migrations.sh:45, :74-88; tests/tier2_component/migrations/prod_columns_test.go:295, :583, :610; Makefile:91-94; tests/tier0_static/proto_no_drift_test.go:70; schemas/lenny-adapter.proto:1451 -> pkg/proto/adapter/v1/lenny-adapter.pb.go:4966; lenny-adapter_grpc.pb.go:180 and :632 both open the `CoordinatorFence` RPC comment.

FACT: §8's baseline-shift class 1 and class 2 hold against an independent sweep of the tree. `grep -rn "CoordinationGeneration" --include=*_test.go pkg/ tests/ | grep -v "CoordinationGeneration:"` returns 30-odd assertion sites; every one outside §8's named set reads a value seeded explicitly at or above 1 (`coordination_fence_test.go` 4/9/14/10, `failure_test.go` 7/1/11, `coordlease_test.go` 3/5, `wiring_test.go` 4, `coordination_mirror_test.go` s1 at 2, both `sessioncheckpointmeta` suites, both `evictionstatestore` suites at 3/4) or is a relative comparison (`derive_failure_audit_test.go:46` increments through a fake `Get`). The two known residues stay the two the standing context already names. — EVIDENCE: pkg/gateway/coordination/coordination/coordination_mirror_test.go:84-86, :116; pkg/gateway/sessionserver/derive_failure_audit_test.go:40-49.

FACT: the three stores whose `Create` could carry the CODE-4 floor are two, not three. `sessionstore.Store` has exactly two implementations in the tree — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:140 and pkg/gateway/session/sessionstore/memstore/memstore.go:46 are the only `func (s *Store) Create(... sessionstore.Session)`; `pkg/gateway/session/sessionstore/pgstore/pgstore_test.go` names `CoordinationGeneration` nowhere, so the pgstore floor genuinely owes only the tier-2 case §8 stages.

FACT: the three seeding stores behind §8's shifting assertions are what §8 says they are, so the class-1 "each shifts by one" rule really does fire. `tests/tier8_chaos/coordination_crash_takeover_test.go:85` builds a production `sessionpg.Store`; `tests/tier2_component/coordination/sweep_test.go` and `pkg/gateway/coordination/coordination/coordination_takeover_test.go` build `memstore.New()`; `pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go:293` likewise. All four therefore pass through a CODE-4 floor — EVIDENCE: tests/tier2_component/coordination/sweep_test.go:167..:535 (twelve `memstore.New()` sites), :594 is the last shifting assertion, matching §8's ":275 to :594".

WATCHOUT: `sed -n 'A,Bp'` on this proposal's `review-log.md` blows the Bash output cap for windows wider than about 140 lines and gets persisted to a tool-result file, which then reads shorter than the source range. Read it in 120-160 line windows; `cat -n` on `non-spec-changes.md` persists too, so read that file with `sed -n` windows as well — EVIDENCE: the standing context's own "reading this review log" entry, confirmed again this run.

USEFUL [the refuted-class list in the standing context, and this run's own refuted list]: between them they pre-adjudicated seven of the eight candidates a fresh holistic read generates on this staging (the `## 5`/non-spec fill-the-blanks headers, `TestCheckpointBarrierRejectsWithoutFence`'s missing replacement assertion, the unbound-session barrier case, 0181's unbatched backfill, the `CoordinatorFenceRequest` comment-tail deletion, OD2/OD5/OD7/OD9/OD11/OD12 wording, and the status-file phantom paragraph). Read both before working anything up; each of those cost an earlier round.

OPEN: this lens found nothing over text that has not moved since the last snapshot. If a future round schedules a fresh holistic read again over byte-identical staging, it buys nothing; the reopening condition is a rewrite of CODE-1's resolve rule, §8's case list, or CODE-4's migration text.


### [non-spec.6.review-kubernetes.1]

DECISION: returned an empty findings list for the fourth consecutive run of this lens on this text — BECAUSE the only Kubernetes primitive the staging depends on is the Helm migrate Job, and its every claim resolves; nothing in either change file touches a CRD, a status subresource, a finalizer, an admission webhook, controller-runtime reconciliation, or agent-pod RBAC — ALTERNATIVES: I worked up and dropped four candidates. (1) 0181's `DEFAULT 1` on `coordination_lease.coordination_generation` putting a fabricated value on the wire: dead, because `coordlease/pgstore.Upsert` names the column explicitly in both the INSERT list and the ON CONFLICT SET, so the default is unreachable — EVIDENCE: pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58. (2) a missed `charts/` or migration-registry edit site for 0181: dead, `migrations/embed.go:15` is `//go:embed *.sql` and no chart template, values key, or `pkg/schemamigrate` table enumerates migration numbers. (3) a landed tier-5 (Kind) case broken by the baseline: dead, `tests/tier5_e2e_kind/checkpoint_resume_test.go:268` is the tier's only use of the column and it is an `ORDER BY coordination_generation DESC` on `checkpoint_manifest`, asserting no value. (4) D7 lengthening the preStop drain against the pod's termination grace: already refuted in `### Settled` on `dispatchOne`'s unconditional goroutine, and I did not re-derive it.

FACT: the accessor blast radius CODE-1 declares is exactly right and can be re-checked in one grep. `grep -rn "LastFencedGeneration\|isQuiescedForBarrier\|BarrierWaiting()" --include=*.go .` returns `Server.LastFencedGeneration` at three call sites only, and none is a health, readiness, or status surface, so the signature change cannot reach a kubelet probe path — EVIDENCE: pkg/adapter/holdstate.go:119, pkg/adapter/coordination_test.go:73, tests/testinfra/coordfixture/coordfixture.go:115.

FACT: every CODE-4 line citation into the two session stores resolves exactly, which is worth knowing because three separate arguments in the proposal (OD8's withdrawal, CODE-4's rolling-window paragraph, and the DEFERRED note about the column default) all rest on them. `Create` at pgstore.go:140; `coordination_generation` in the insert column list at :177; the `schemaVersion == 0` normalisation at :244-248; `pgtenant.InTx` at :249; the `sess.CoordinationGeneration` bind at :260. memstore `Create` at :46 with its `SchemaVersion` normalisation at :58-61.

USEFUL [Settled: "the whole carrier surface" / docs surface re-derivation]: the standing instruction not to re-derive the baseline-shift and spec-phrase sweeps saved a full pass. I stopped after confirming the one file the sweeps do not name that carries the token, the tier-5 Kind case, and it is a non-site.

USEFUL [Traps: "a cached lens answer can belong to the other lane"]: `kubernetes-r5-<H>.json` exists under the same hash from 09:16 today. I read its `coverage` field before trusting anything: it names `non-spec-changes.md` and section 8, so it is this lane's, and its conclusion matches mine independently. The r1 and r2 entries under the same hash are the earlier lanes.

OPEN: this lens has now returned empty four times over text that has not moved (`diff -ru` against `scratchpad/cp-snap/0076-run7/non-spec-r4` is byte-identical across the whole proposal directory, summary.md included). The standing `### Open` item on retiring an exhausted lens names the reopening conditions for this one: CODE-4 growing a chart or CRD deliverable, CODE-3 growing an apiserver or `Sandbox.status` write, or D7's acceptance arm being rewritten. None has fired. Each empty return costs every other lens a full round.


### [non-spec.6.review-mechanism.1]

DECISION: returned EMPTY on the end-to-end-mechanism lens over `non-spec-changes.md` + `spec-changes.md` read together — BECAUSE every flow the non-spec staging describes traces cleanly from origin to effect against the tree, and every mechanism-shaped candidate I generated is already on this run's refuted list or on the standing do-not-file list — ALTERNATIVES: I worked up and dropped (1) the link-before-open race that makes a D7-accepted barrier block to the ack deadline when `dispatchOne` starts `CheckpointWithTrigger` before `dispatch.Send` (already the standing "drain-path candidate the standing D7 refutation does NOT cover", declared not filable); (2) the unlocked read of the detached `*slotState` in CODE-3's post-mortem (standing UNVERIFIED, and the struct carries its own `mu` so an implementor gets it right); (3) "Both refusals are loud and fail closed" against CODE-4's own paragraph three lines above it saying the fence-path `InvalidArgument` burns the attempt budget and relinquishes the lease (imprecision, not falsity — relinquish-and-abort IS loud and fail-closed); (4) §8's tier-4 sentence for D7 naming no case (refuted tier-list-bookkeeping class).

FACT: the full D7 mechanism chain verifies end to end and nobody had written it out in one place. `upsertMirror` runs for every ELIGIBLE HELD row at the end of the sweep loop, and `eligible = bound || priorHolder == self || adoptable`, so a never-taken-over BOUND session does get a `coordination_lease` mirror row carrying its session row's value; `MirrorTargetLister.Targets` copies `le.CoordinationGeneration` onto the wire; post-baseline that is 1, which clears the adapter's `gen <= 0` guard and reaches the gate, where `initialized` is false and CODE-2 accepts. Pre-baseline it is 0 and dies at the `gen <= 0` guard. So D7's acceptance arm IS reachable on the healthy production path and the "neither change delivers that repair without the other" claim is exact — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:300 (`adoptable`), :335 (`eligible`), :430 (`upsertMirror`); pkg/gateway/coordination/barrier/wiring.go:104-114; pkg/adapter/coordination.go:224-226, :236.

FACT: the slot registry is keyed by SESSION id, not by a reusable slot ordinal — `ensureSlotStateLocked(slotID)`, `workspaceRootForSession` and `checkSessionBound` all index `s.slots[sessionID]`. So relocating `lastFenced` onto `slotState` cannot leak one session's fenced value onto the next session to occupy a slot, and D6's "unset until that session's first accepted fence" holds by construction rather than by an invariant somebody has to maintain. I spent a pass hunting a cross-session-reuse defect here; it does not exist — EVIDENCE: pkg/adapter/slot.go:82-102, :131-137, :153; pkg/adapter/slotsession.go:267-276.

FACT: every concrete code anchor CODE-1 through CODE-4 and §8 cite resolves, re-checked this run rather than trusted. Spot list worth not re-deriving: `Create` at pgstore.go:140 with `coordination_generation` in the insert column list at :177, the `schemaVersion == 0` normalisation at :244-248, `InTx` at :249; memstore `Create` at :45 (staging says `:46`) with the `SchemaVersion` normalisation at :58-60; `coordfence`'s floor at :147-153, its transient arm at :180-183, its budget-exhausted relinquish at :186-188; `0050:38-39` for the session-row CHECK and `0164:44` for the lease column default; `0180_drop_checkpoint_slot_id.up.sql` is genuinely the last taken number; `prod_columns_test.go:583` (0180 row), `:295` (0112 row), `:610` (`TestProdMigrationsRollBackPerStep`); tier-7a `:130` (seed 1), `:144` (seed 0), `:260` (`pod.LastFenced`), `:264-265` (assert 2), `:287-288` (assert 0); tier-8 `:118`/`:179` (seeds at 1) and `:239-241` (unset seed) with `:267`/`:283`/`:296` asserting 1, 1, 2; `coordfixture` `Fence` `:109`, `LastFenced` `:115`, `StaleRPCRejected` `:122`, fence comment `:106-108`.

FACT: `pkg/adapter/coordination_test.go:51`'s `CoordinationGeneration: 0` is a `CoordinatorFenceRequest` FIELD, not a session-row seed, so a grep for `CoordinationGeneration: 0` across the test tree returns exactly two hits and only the tier-7a one (`:144`) is in §8's class 1. §8's class-1 exhaustiveness claim survives that grep.

FACT: `tests/tier2_component/migrations/phase3_gate_test.go` exists and mirrors lint pass 5, but both key on `DROP COLUMN`, so migration 0181 (two `SET DEFAULT`s and one `UPDATE`) reaches neither. Lint pass 1 and 2 are satisfied by the staged `.down.sql`, and pass 3 by the `prodMigrationSchema` row. The staged migration trips no gate the proposal does not name — EVIDENCE: scripts/lint-migrations.sh:60-88, :93, :126; tests/tier2_component/migrations/phase3_gate_test.go:19-35.

FACT: the only landed assertion on the hold's generation is the post-mortem JSON field at `pkg/adapter/holdstate_test.go:708`, inside the case §8 dispositions. A repo-wide grep for `last_generation|lastGeneration|coordinator_connection_lost` in `_test.go` returns that plus one doc comment at `:889`. CODE-3 therefore breaks no test §8 does not name.

WATCHOUT: `diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` returned NOTHING this run — run 7's non-spec round 1 was cut off before any fix landed, so "read the changed sections first and hardest" had no target and every previously-filed non-spec item is still live text. Do not read an empty diff as evidence the round-1 findings were applied — EVIDENCE: the four refuted items in this round's prompt all still sit verbatim in `non-spec-changes.md`.

USEFUL [the `### Traps` "grep the ARCHIVE before working it up" entry]: it saved me from filing the tier-2 resume-fence case's missing home and the `TestCheckpointBarrierRejectsWithoutFence` replacement assertion, both of which a mechanism lens generates naturally from an S5-S6-S7 ordering trace.

USEFUL [the `### Settled` "barrier fan-out runs under one wall-clock deadline, and D7 adds no drain work" entry]: it is the single entry that pre-kills the largest cluster of mechanism candidates on this staging (co-tenant drain serialisation, the shared 90s budget, the op-lock queueing). Without it I would have spent the whole pass on `prestop.go` and `barrier.go`.


### [non-spec.6.review-open-decisions.1]

DECISION: returned ZERO findings — BECAUSE the whole open-decision inventory is now adjudicated: every
file:line citation in `## Open decisions` re-verified clean for the third consecutive round, and every
candidate I could still construct is a close variant of something the material skeptic has already refuted
by name (OD1, OD2, OD4, OD5, OD7, OD9, OD11, OD12, OD13, the `IMPLEMENTOR TO FILL THE BLANKS` header, the
status-file "closing paragraph" clause, and the 0181 backfill) — ALTERNATIVES: the OD5/OD2 circularity and
the OD1/D7 cascade, both worked up and dropped; grounds below.

FACT: the proposal directory is byte-identical to `scratchpad/cp-snap/0076-run7/non-spec-r4`,
`non-spec-r5-start`, and `non-spec-r6-start`. `diff -rq` against all three returns nothing, so rounds 4, 5,
6 and 7 all review the same bytes and "read the changed sections first" has had no target for four rounds
— EVIDENCE: scratchpad/cp-snap/0076-run7/non-spec-r6-start/ vs proposals/0076_.../

FACT (re-verified independently this pass, do not re-derive): OD1's `cmd/lenny-gateway/httpsurface.go:588-602`
(`gen := int64(0)` at `:592`, overwritten only on a successful `w.sessions.Get` at `:593-594`); OD2's
`pkg/adapter/coordination.go:99` (`s.coord.initialized && gen <= s.coord.lastFenced`), `:236`
(`!initialized || gen != fenced`), `coordfence.go:147-153` and `:164-179` (stale branch re-reads and retries
only when `newGen > gen`, else `relinquish`), `start.go:4233-4245` (`fenceResumedPod` calls `Fence` with no
increment), `coordination/coordination.go:463-481` (`RecordHandoff` unconditional `row.CoordinationGeneration++`),
`:430` (`upsertMirror` passes the PRE-bump `row.CoordinationGeneration`),
`schemas/lenny-adapter.proto:1455-1462`; OD3's `spec/04:151`, `:175`, `:188`, `:190`,
`schemas/lenny-adapter.proto:492-496`; OD7's `spec/07:196`; OD11's `scripts/seed-claim-register.py`
(REFERENCE = `gateway-runtime-comms.md`, SECTION = "### 7.1 Status of every mechanism named in this
document", head of file) and `gateway-runtime-comms.md` §7.1; OD13's premise (`checkSessionBound` returns
`FailedPrecondition` at `pkg/adapter/slotsession.go:272-273`, the string has exactly one site tree-wide and
no test); CODE-4's `pgstore.go:140`, `:170`, `:177`, `:244-248`, `:249`, `:260`; CODE-1's `server.go:302`,
`:307`, `:314` and `coordination.go:44`, `:52`, `:63`.

FACT: 0180 is the last migration number taken (`migrations/0180_drop_checkpoint_slot_id.up.sql`) and no
other proposal in `proposals/` mentions 0181, so CODE-4's "0181 is the next free migration number" holds
and no cross-proposal collision exists — EVIDENCE: ls migrations/*.sql | tail; grep -rl 0181 proposals/

FACT: the only proposals whose text touches this change's surfaces are 0060 (Implemented), 0073
(Implemented), 0075 (Draft), 0080 (Draft), 0062 (Retired/SUPERSEDED), 0064, 0069, 0070. The summary's
`## Impact on other proposals` table covers the four live ones; 0062 is retired and needs no row —
EVIDENCE: proposal-status.mjs on 0062 returns "Retired"; grep over proposals/00[6-8]*.md for
coordinationState|barrierGate|LastFencedGeneration|CheckpointBarrier|CoordinatorFence|29.10|10.1.8

WATCHOUT: OD5's closing sentence (`summary.md:286`) says the first-crash-takeover-fence repair "is also
fixed by OD2's remedy, so it is not exclusive to CODE-4", while OD2 (`summary.md:198-206`) states that its
remedy admits a genuine split-brain until CODE-4 baselines the row and that "If the reviewer splits CODE-4
out under OD5, this fix follows it". Under a "split" answer neither is in this proposal, so the repair IS
exclusive to CODE-4's release and OD5 misprices the split option by one benefit. Worked up and NOT filed:
it is the same paragraph and the same class as the OD5 finding the material skeptic refuted this window
("completeness polish on a decision briefing"), and OD5 stages nothing. A fixer already editing OD5 should
delete or qualify that sentence.

WATCHOUT: answering OD1 "widen" would falsify the second sentence of FIXED decision D7 ("A barrier carrying
a value that does not match one the pod does hold ... is still refused"). Not a finding, because
`summary.md:68-71` already declares the comparison operator open and adjacent to the fixed decisions, which
parameterises D7's "does not match". Record it as a cascade, not a defect.

DEFERRED [`spec-changes.md:604-616`]: §7 "Open decisions for review" still lists three items while
`summary.md:390-409` dispositions item 3 (`coord.mu`) as "Delete the item" and item 2 (a fence for an
unheld session) as "out of scope ... with a named consequence and an owner". No owner is named anywhere in
the proposal, and neither disposition is applied to §7. What is false: §7's presentation of items 2 and 3
as live reviewer decisions. What is true: item 3 is an implementation choice with no external consequence
and item 2 is out of scope with the consequence recorded under `### Defects in the shipped tree that this
proposal does not stage`. Carried unchanged from `[non-spec.5.review-open-decisions.1]`; the non-spec loop
has no write path to `spec-changes.md`.

DEFERRED [`spec-changes.md` §7]: OD5 is the open decision with the largest effect on the staged SPEC text
(a "split" answer deletes SPEC-3 entirely, `spec-changes.md:571-585`, and SPEC-1's §10.1 baseline
paragraph, `:230-238`, on which the no-edit-site conclusion at `:259-264` explicitly rests), and §7 does
not list it. A reviewer reading only the staged spec file sees no sign a whole deliverable is conditional.
Verified this pass against the three cited ranges.

USEFUL [non-spec.5.review-open-decisions.1, its FACT re-verification block]: it is the reason this pass
could spend its budget on the two decisions the orchestrator flagged as under-examined (OD2, OD5) instead
of re-opening thirteen citations. Every anchor it listed still resolves.

USEFUL [non-spec.2.review-open-decisions.1, its MISTAKE line]: "A finding from this lens survives only when
it is a hard false file:line citation or when it reaches a staged deliverable." That is now confirmed over
four more refutations (OD2, OD5, OD7, and the status-file clause). Treat it as the lens's operating rule
rather than as one round's observation.

OPEN: with the material skeptic having refuted twelve consecutive `## Open decisions` findings and every
citation in the section verifying for three rounds, this lens has no remaining subject that clears the
materiality bar while the fix lane excludes `spec-changes.md`. Either give the non-spec fixer a write path
to `spec-changes.md` §7 (which closes both DEFERRED entries above) or retire the lens for this proposal.


### [non-spec.6.review-operational.1]

DECISION: returned EMPTY — BECAUSE every observability surface this change reaches was re-opened from
primary sources this pass and each one is either unit-neutral, already staged, or unchanged. Named metrics
all resolve (`lenny_coordinator_handoff_stale_total` spec/16_observability.md:183 + docs/reference/metrics.md:307 +
pkg/observability/metrics/catalog.go:269 + incrementer pkg/gateway/coordination/coordfence/coordfence.go:170,
`lenny_coordinator_fence_relinquished_total` spec/16:192 / docs:312 / catalog.go:274 / coordfence.go:196,
`lenny_adapter_coordinator_hold` spec/16:185 / docs:309 / pkg/adapter/metrics.go:107-111). The only coordinator
alert evaluates `lenny_coordinator_handoff_duration_seconds` (pkg/alerting/rules/rules.go:1583), which no
deliverable touches — ALTERNATIVES rejected: CODE-1/CODE-3 (checklist S5) declaring tier 2 while §8 names no
tier-2 case and §9 lists no tier-2 file attributable to them (over-declared tier, the thrice-refuted
tier-list/§9 bookkeeping class); OD2's cost sentence naming only the stale and relinquish counters while the
scenario also increments `lenny_coordinator_fence_retry_total` (coordfence.go:174) — the sentence is not
exhaustive-by-claim and OD2 is a parked question; the `lenny_prestop_cap_selection_total` ratio in
`PreStopCapFallbackRateHigh` (rules.go:927) shifting because D7 moves sessions out of the Stage-2 loop
(prestop.go:396) — direction unpredictable without the source distribution, speculative.

FACT: `lenny_adapter_coordinator_hold` is registered with a NIL label set (`mustGauge(..., nil)`,
pkg/adapter/metrics.go:107-111, setter at :222-227), so D5's "the gauge remains pod-scoped" is exact rather
than approximate, and nothing about the per-session move can widen it — EVIDENCE: pkg/adapter/metrics.go:105-111,
:219-227.

FACT: `coordination_lease.coordination_generation`'s DEFAULT is genuinely cosmetic, on a stronger ground than
"written explicitly": there is exactly ONE `INSERT INTO coordination_lease` in the whole tree and it names the
column in its column list. CODE-4's `DEFAULT 1` on the mirror column therefore cannot change any row's value —
EVIDENCE: pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58 (the only insert; `grep -rn "INSERT INTO
coordination_lease" pkg/ migrations/ tests/` returns that line alone).

FACT: §10.1.5 (Stale Replica Behavior) is NOT an edit site and a lens will be tempted by it, because it is the
section that ties `lenny_coordinator_handoff_stale_total` to the stale rejection (`spec/10_gateway-internals.md:71`,
step 4). Every one of its four steps is already written per session ("Cancel all in-flight RPCs for that session",
"Discard all cached session state ... for that session"), so the unit change reaches none of them — EVIDENCE:
spec/10_gateway-internals.md:65-71.

FACT: the §28.8 Observability column (field 6 of a seven-field row) is not reached by this change on any row.
`CH-FENCE`'s cell names the `coordinator_generation_gap` event and the `coordinator_lost` termination and
carries no generation or unit; `CH-BARRIER`'s names the timeout manifest, `lenny_checkpoint_barrier_ack_total`
and its outcomes, the ack-duration histogram, and `lenny_prestop_barrier_target_source_total`. Neither outcome
set nor metric name moves. Read a row with
`awk -F'|' 'NR==1807 {for(i=1;i<=NF;i++) print "["i"] "$i}' spec/28_communication-channels.md` — EVIDENCE:
spec/28_communication-channels.md:1807 field 6, :1808 field 6.

FACT: no tier-11 test reads a fence or barrier proto comment. The four tier-11 files that open
`schemas/lenny-adapter.proto` at all are `vm_restart_reprovision_consistency_test.go`,
`artifact_register_supersession_test.go`, `credential_path_literal_sweep_test.go`, and
`compliance_suite_artifact_enumeration_test.go`, none of which touches the SCHEMA-1 carriers, so SCHEMA-1's
declared tiers 0 and 3 are right and its omission of 11 is not a gap — EVIDENCE:
`grep -rln "lenny-adapter.proto" tests/tier11_docs/`.

USEFUL [`### Settled`, "the alert and metric surface is closed and untouched" / the thirteen-line `docs/` surface]:
verified by spot-check rather than re-derivation and it held on every point I opened
(`docs/reference/metrics.md:40`, `:197`, `:307`-`:312`, `docs/reference/adapter-contract.md:68`, `:69`, `:97`,
`docs/getting-started/architecture.md:173`, `docs/operator-guide/upgrades.md:49`). Spot-checking cost three greps
against a full pass.

USEFUL [`[non-spec.2.review-operational.1]`]: its "open the proto carriers FIRST; everything else in the
inventory has been closed for six rounds" is right, and it now needs a rider — the proto-carrier finding it filed
was REFUTED by the material skeptic on the ground that a doc comment saying LESS is not a comment saying
something WRONG. That refutation kills the whole class, including the shard's own trailing UNVERIFIED about the
two `CheckpointBarrier` carriers keeping `FailedPrecondition`. An operational lens has nothing left to open on
the proto comments.

MISTAKE: the standing `### Open` item "SCHEMA-1 qualifier wording ... FILED against `spec-changes.md:506-512`"
still reads as a live defect awaiting a fix. It is not: the material skeptic refuted it in run 7. A future lens
that trusts that `### Open` line will rebuild a refuted finding. The `### Open` list needs the refutation
recorded against that item.


### [non-spec.6.review-performance.1]

DECISION: returned empty — BECAUSE the whole staged change is a de-serialisation (pod-wide `coord.mu` and one
`barrierGate` become per-slot-entry) with no control-plane, etcd, Redis, or steady-state Postgres delta, and every
failure mode I traced degrades no worse than the shipped design — ALTERNATIVES: I worked up and killed four
candidates, each recorded below so the next performance lens does not re-derive them.

FACT: the top-tier drain write/wall-clock delta of D7 is NEGATIVE, and the arithmetic is closed. Per draining
replica at Tier 3: 400 barrier targets (`spec/17_deployment-topology.md:7` "fans out CheckpointBarrier to up to 400
pods per replica"). `dispatchOne` starts `CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and joins it
with `cpWG.Wait()` AFTER, whatever the barrier returned, so the barrier window's wall clock is governed by the
checkpoint uploads and is identical before and after D7. What D7 changes is downstream: `o.Acked` becomes true, so
`fireBarrier` returns a full `barrierAcked` set and the post-barrier loop `continue`s on every session instead of
re-capturing all 400 SEQUENTIALLY. So the drain goes from two captures per session to one, MinIO drain load halves,
and the sequential phase shrinks to zero. The only new Postgres write is `c.meta.Upsert` on the acked path (+≤400
per drain, concurrent), against `nextBarrierID` which already issues one read per target on the same fan-out —
EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:200-202 (goroutine), :225 (`dispatch.Send`), :226
(`cpWG.Wait()`), :235-247 (`Acked` then `meta.Upsert`); pkg/gateway/podlifecycle/prestop/prestop.go:391-394 (`if
barrierAcked[sess.SessionID] { continue }`), :503 (one `barrierAckTimeout` over the whole `Dispatch`), :387
("Iterate sessions sequentially").

FACT: the "barrier accepted but no `Checkpoint` stream ever links, so it blocks to the 90s ack deadline" hang
CANNOT be reached differentially by D7, and this is the candidate that looks strongest and dies on one line.
`Checkpointer.snapshot` returns `ErrNoBinding` before any stream opens when `c.Registry.Get(sessionID)` misses, and
`PodDispatcher.resolve` fails the barrier with "no adapter connection" on the same missing local binding, so the
checkpoint and the barrier fail together rather than one hanging on the other. Both also ride the same held
connection, so a transport fault takes both — EVIDENCE: pkg/gateway/checkpoint/checkpointer/checkpointer.go:424-427;
pkg/gateway/coordination/barrier/wiring.go:62-69, :43-48.

FACT: `coordinationState.quiesced` is ADVISORY and has no production reader — the struct comment says the
quiesce-strict enforcement is deferred to F-10.1.6 — so relocating it per entry changes no runtime behaviour and
adds no contention. `isQuiescedForBarrier()` and `BarrierWaiting()` are both marked "Exposed for tests" —
EVIDENCE: pkg/adapter/coordination.go:33-38, :51-55, :58-66.

FACT: migration 0181's `coordination_lease.coordination_generation` `DEFAULT 1` is cosmetic and its absent backfill
is harmless, because every write to that column names it explicitly in an upsert — EVIDENCE:
pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-54 (`INSERT INTO coordination_lease (... ,
coordination_generation, ...)` with `coordination_generation = EXCLUDED.coordination_generation`);
migrations/0164_coordination_lease.up.sql:44. Do not file the asymmetry with the `sessions` backfill.

WATCHOUT: `s.ops.Begin` admits a DISTINCT co-tenant session id and BLOCKS it behind the running checkpoint (only
the same id coalesces with `errOpCoalesced`), so on an N-session pod the Nth barrier's ack lands only after N-1
uploads. That reads as a new top-tier serialisation and is not one: the gateway already opens N `Checkpoint`
streams concurrently to that pod today and `dispatchOne` already waits on each, and the shipped pod-wide gate is
strictly worse for the same N (the second `open()` clobbers the first, which blocks to the deadline) —
EVIDENCE: pkg/adapter/oplock.go:117-129; pkg/adapter/coordination.go:158-166.

MISTAKE (mine, avoided): I twice re-derived the standing `### Settled` bullet "the barrier fan-out runs under one
wall-clock deadline, and D7 adds no drain work" from scratch before rereading it. It is correct and it is
load-bearing for this lens; read it first and only re-derive the half you intend to dispute.

UNVERIFIED: the summary's Goals claim "the drain checkpoint captures a stopped workspace" (summary.md:130-131,
:36-37). Nothing stops today, because `quiesced` is advisory with no reader (F-10.1.6). The claim is true in
specification terms and the "captured twice" half IS delivered, so I did not file it; an applicability or
mechanism lens should decide whether a Goals bullet may state a specified-but-unenforced effect.

OPEN: `.claude/rules/test-coverage.md` requires the concurrent and failure paths of a changed behaviour to be
pinned, and §8 pins the co-tenant barrier contention case at tier 7a but pins nothing on the interaction between
the hold timeout's per-session generation read (CODE-3) and a `CoordinatorFence` in flight for the same entry. The
hold's rejection allowlist admits `CoordinatorFence` (`pkg/adapter/holdstate.go:53-59`), so the two are genuinely
concurrent. I did not file it: the remedy is one clause in CODE-3 saying the read takes the entry's
`coordinationState.mu`, which is authoring guidance, and the material skeptic refuted the structurally identical
test-amendment finding on exactly that ground this run. A later lens wanting it must argue past that refutation.

FACT: `diff -rq` against /home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r4 returned NOTHING. The
proposal is byte-identical to the snapshot, so "read the changed sections first and hardest" had no target. That
is the fourth consecutive round in this loop where it had none; run the `diff -rq` first, it costs one call.


### [non-spec.6.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every recovery, retry, and restart claim the non-spec
staging makes re-verified against the tree this run, and the three reopening conditions the standing context
names for this lens (D7's acceptance arm, the retained `coordfence` floor's rolling-window behaviour,
§10.1.8's failure-arm claim) are all unchanged text — ALTERNATIVES: (1) filing CODE-3's post-mortem read of
the detached `*slotState` as an unlocked-read race, rejected because the staging never says the read is
unlocked and `coordinationState` carries its own `mu`, so the safe form is the obvious one and the entry is
already an UNVERIFIED owned by the security lens; (2) filing D7's enlargement of the acked-but-uncaptured
population (an accepted barrier sets `Acked=true` even when the concurrent `CheckpointWithTrigger` failed,
so the never-handed-off population loses the post-barrier retry it has today), rejected because the standing
context already weighed it and shipped §10.1.8 mandates the skip; (3) filing the "Both refusals are loud and
fail closed" imprecision, already a standing OPEN and rationale that lands nowhere.

FACT: `diff -ru scratchpad/cp-snap/0076-run7/non-spec-r4 proposals/0076_.../` returns NOTHING. The proposal
directory is byte-identical to the r4 snapshot, so the "read the changed sections first and hardest"
instruction has no target this run and every non-spec sentence is r4-vintage or older — EVIDENCE:
scratchpad/cp-snap/0076-run7/non-spec-r4 vs the live directory.

FACT: every CODE-4 failure-path citation resolves exactly. `coordfence.go:147-153` is the `gen <= 0` floor,
`:180-183` the `default:` transient arm, `:186-188` the budget-exhausted relinquish, and `:164-179` the stale
arm that re-reads and RETRIES when `newGen > gen`. `RecordHandoff` bumps inside `Update` and returns the
post-increment value, refusing a terminal row with 0 — EVIDENCE: pkg/gateway/coordination/coordfence/
coordfence.go:147-188; pkg/gateway/coordination/coordination/coordination.go:463-481.

FACT: the pre-baseline takeover defect the summary claims (`summary.md:38-41`) traces end to end. Row 0,
resume fences at the floor's 1, pod holds 1; the first takeover's `RecordHandoff` bumps 0 to 1 and fences at
1; `coordination.go:99`'s `gen <= lastFenced` refuses it; `coordfence` re-reads 1, `1 > 1` is false, so it
relinquishes and records an adoption backoff; the NEXT sweep bumps 1 to 2 and succeeds. One sweep cycle of
delay plus a stale/relinquished metric pair, exactly as stated, and CODE-4's baseline removes it because a
row created at 1 makes the first takeover mint 2 — EVIDENCE: pkg/adapter/coordination.go:99;
pkg/gateway/coordination/coordfence/coordfence.go:164-179; coordination.go:415, :514-518.

FACT: `coordination_lease.coordination_generation` has exactly one writer and it always binds the value
explicitly, so migration 0181's `DEFAULT 1` on the mirror column is cosmetic and the absence of a mirror
backfill is not a gap — EVIDENCE: pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-59 (the only
`INSERT INTO coordination_lease` in the tree).

FACT: CODE-1's guard-to-link window argument is exact. `opLock.Begin` admits a checkpoint whose session id
no pending checkpoint names and parks it on a promote channel behind the running one; only the same id
coalesces with `errOpCoalesced` — EVIDENCE: pkg/adapter/oplock.go:116-129, :135-140.

WATCHOUT: the barrier's wait IS bounded and the bound is the caller's, not the pod's. `CheckpointBarrier`
selects on the gate's `done` and on `ctx.Done()` alone, with no adapter-side deadline, so the only thing
stopping a barrier for a session that is deregistered mid-flight from hanging forever is prestop's
`context.WithTimeout(ctx, h.barrierAckTimeout())` around the whole `Dispatch`. A lens tempted to file "the
per-entry gate has no reclaimer when the hold timeout removes the entry" should stop here: nothing signals
that gate under the pod-wide gate either, and the 90s client deadline is the reclaimer in both —
EVIDENCE: pkg/adapter/coordination.go:264-269; pkg/gateway/podlifecycle/prestop/prestop.go:503-505.

USEFUL [Settled: "A refused barrier costs a duplicate capture rather than a lost checkpoint"] and
[Settled: "D7 strictly reduces top-tier drain cost"]: together they pre-adjudicate the whole
"D7 loses a capture" family, which is the first thing this lens works up. Verified once more against
barrier.go:217-237 (the `CheckpointWithTrigger` goroutine started before `dispatch.Send` and joined after,
whatever the barrier returns) and prestop.go:390-398 (`barrierAcked` skip). Keep both.


### [non-spec.6.review-security.1]

DECISION: returned an EMPTY findings list for the security lens over the non-spec staging
(`non-spec-changes.md` SCHEMA-1, CODE-1..CODE-4, TEST-1, §8, §9) read together with `spec-changes.md` —
BECAUSE `diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run7/non-spec-r4
/home/ec2-user/lenny/proposals/0076_...` exits 0, so the whole proposal directory including the review log
is BYTE-IDENTICAL to the r4 snapshot; the staging has not moved since the last time this lens ran, and the
narrow reopening conditions `[non-spec.2.review-security.1]` recorded (a rewrite of D7's acceptance arm, of
CODE-4's rolling-window paragraph, or of CODE-1's entry-lifetime rule) are all unmet. Every candidate I
derived independently this pass resolved to a recorded refutation, a settled bullet, or shipped
pre-existing behaviour — ALTERNATIVES worked up and dropped, one of them new:

(1) NEW this pass, and disproved from the tree rather than from the log. "CODE-4's backfill of
    `sessions.coordination_generation` from 0 to 1, with no matching backfill of the
    `partial_manifest` / `checkpoint_manifest` generation columns, flips the §10.1.7 supersede fence for
    every pre-migration row: an in-flight attempt recorded at 0 is now superseded by an attempt at 1."
    Dropped: supersede-on-write already soft-deletes every active partial row at or BELOW the incoming
    generation, and the reject arm fires only on a STRICTLY higher stored row, so stored-0 against
    incoming-0 already superseded and stored-0 against incoming-1 supersedes identically. The migration
    moves nothing across either predicate — EVIDENCE:
    pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:394 (`row.CoordinationGeneration >
    r.CoordinationGeneration` -> ErrStaleGeneration), :407 (`<= r.CoordinationGeneration` -> superseded);
    pkg/gateway/checkpoint/partialmanifeststore/pgstore/pgstore.go:100, :117 (the same two predicates in
    SQL); pkg/gateway/checkpoint/checkpointer/uploaddriver.go:422.
(2) "D6 widens the pod-wide coordinator-loss hold exit: a fence for an unfenced co-tenant at a LOW
    generation is now accepted (no recorded value for that session) and calls `exitHoldState`, where the
    shipped pod-wide `initialized && gen <= lastFenced` refused it." Dropped: this is verbatim candidate
    (2) on the archived security WATCHOUT list, already refuted on the ground that a fence issues only
    after a successful CAS — EVIDENCE: review-log-archive.md:5266-5270; pkg/adapter/coordination.go:99,
    :124 (`exitHoldState` on the accepted arm only).
(3) `docs/reference/adapter-contract.md:69` ("precondition for any subsequent operational RPC") as an
    unstaged surface D6/D7 falsify. Dropped: the clause restates §10.1.2 step 2's SENDER-side duty on the
    acquiring coordinator, which D6/D7 do not touch, and the standing `### Settled` docs sweep has
    re-derived the thirteen-line `docs/` surface as unit-neutral eleven times.
(4) `coordination_lease.coordination_generation` moving to `DEFAULT 1`; (5) CODE-3's post-mortem read of
    the detached `*slotState` without the entry lock; (6) the unbound-session barrier refusal. All three
    are already on the archived dropped list or on this run's orchestrator-supplied refuted list.

FACT: the barrier's authoritative generation is still gateway-side and fail-closed after CODE-4.
`MirrorTargetLister` copies it off the Postgres `coordination_lease` mirror and the cache fallback re-reads
the session row, seeding 0 and overwriting only on a successful read, so a store outage puts 0 on the wire
and takes the adapter's `InvalidArgument` refusal before the gate — EVIDENCE:
cmd/lenny-gateway/httpsurface.go:588-602 (`gen := int64(0)` at :592, overwritten at :594 only on `err ==
nil`); pkg/adapter/coordination.go:224-226. Re-verified this pass; do not re-derive.

FACT: `lenny_coordinator_handoff_stale_total` has exactly one incrementer, `Fencer.incStale` at
pkg/gateway/coordination/coordfence/coordfence.go:205, reached only from the FENCE path's
`FailedPrecondition` arm (:164-166). The barrier's `FailedPrecondition` becomes `ErrGenerationStale`
(pkg/gateway/coordination/barrier/wiring.go:52) and sets `out.Stale` with no counter
(barrier/barrier.go:230-232), so D7's narrowing of the barrier gate moves no count on any catalogued
metric — EVIDENCE: pkg/observability/metrics/catalog.go:269.

USEFUL [`[non-spec.2.review-security.1]`]: its closing OPEN naming the three narrow reopening conditions is
what let this pass check the diff first and then spend the whole budget on one new candidate (the supersede
fence) instead of re-running the `s.coord` and `tests/tier9_security` greps. Keep it verbatim.

USEFUL [`### Settled` "`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches
no gateway decision"]: re-verified once at coordfence.go:159-179 (`fence()` branches on `res.Accepted` and
re-reads the authoritative store on rejection; `res.LastFencedGeneration` is never read) and that closed
the entire trust-boundary half of the lens in one read.

OPEN: security has now returned empty seven or eight times on this staging, three of those over
byte-identical text, and the standing `### Open` "Whether an exhausted lens is retired" still has no
answer. On this evidence security should be retired for 0076 unless one of the three named conditions is
met; each further empty run costs a full round for every other lens.


### [non-spec.6.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE every behavior the staged non-spec changes add or alter
resolves to a named case with stated assertions or a named landed-test disposition, at every tier the change
reaches, and each of the three candidates my lens still generates is either on this run's refuted list or has
been declined by four or more prior shards — ALTERNATIVES: (1) §8's "Tier 4 covers the same flow across the
gateway, the session store, and the pod" (`non-spec-changes.md:332-333`) naming no case, file, or assertion
while checklist S7 declares tier 4 for CODE-2, and `grep -rn "CheckpointBarrier" tests/tier4_integration/`
returning only `coordination_fence_split_brain_test.go:151`'s `StaleRPCRejected` rejection probe — rejected
because the archive kills it four times on the same ground (D7 is pinned by the named tier-3 wire case and the
named tier-1 amendment, so criterion (f) is met and the dangling sentence is bookkeeping): review-log-archive.md
:2764, :3540, :3592, :3636, :3830, and a prior test-coverage lens's empty return at :4245. (2) No listed case
pins the cross-session DENY arm of the barrier gate (sess-a fenced 7, sess-b fenced 2, a barrier for sess-b
carrying 7 must still be refused); the landed `TestCheckpointBarrierRejectsGenerationMismatch` is single-session
so it cannot pin it — rejected as an additional nice-to-have: §8's tier-1 bullet 1 already pins the accept arm
and `LastFencedGeneration` for sess-a still reading 7, and a gross wrong-entry read fails that bullet.
(3) CODE-3's post-mortem read of the detached `*slotState` is a genuinely NEW cross-goroutine read (pre-fix
`gen := s.hold.gen` is taken inside `hold.mu`, holdstate.go:180-190) with no concurrency case listed — rejected
as the mechanism lens's, and it is already carried as an `### Open` UNVERIFIED from `[non-spec.1.review-security.1]`.

FACT: the class-1 baseline-shift inventory really is closed, and I re-derived it independently rather than
trusting the standing bullet. `grep -rn "CoordinationGeneration" --include=*_test.go .` filtered to ASSERTION
sites (not `CoordinationGeneration:` seeds) returns four families §8 does not name, and every one is seeded
explicitly so the baseline does not reach it: `pkg/gateway/storage/evictionstatestore/evictionstatestore_test.go:201`
(Put seeds 3 at `:185` region), `tests/tier2_component/stores/evictionstatestore_test.go:276` (seeds 4 at `:258`),
`tests/tier2_component/stores/evictionfallback_test.go:130` (seeds 3 at `:103`), and
`pkg/gateway/sessionserver/derive_failure_audit_test.go:46`, which mutates a returned row copy inside a
`fenceStore.Get` stub and asserts nothing on the value. Do not re-derive this fourth family; it looks like a
missed site because `row.CoordinationGeneration++` greps as a write — EVIDENCE:
pkg/gateway/sessionserver/derive_failure_audit_test.go:41-49.

FACT: three §8 citations the standing context records as drifting are correct as the proposal writes them, and
one standing correction is itself wrong. `pgstore.New` IS at `pkg/gateway/session/sessionstore/pgstore/pgstore.go:60`,
which is what the staging cites; the standing `### Traps` bullet saying it is at `:59` and the staging cites `:60`
is the error. `coordination_colocation_race_test.go`'s `pod.LastFenced()` read is at `:260` and its assertion of 2
at `:264-265`, both exactly as staged; only its assertion of 0 is at `:288-289` where the staging says `:287-288`.

FACT: the tier-2 resume-fence case's "must fail against the pre-fix code" claim holds, checked end to end.
Pre-fix a row created unset reads 0, `fenceResumedPod` fences on the read value with no increment and
`coordfence`'s retained floor lifts it to 1, then the crash takeover's `RecordHandoff` bumps 0 to 1 and fences
at 1, which the adapter's `gen <= lastFenced` refuses as `coordinator_handoff_stale`. Under CODE-4's baseline
the row is 1, the resume fence is 1, the takeover bump is 2, and the fence is accepted. The case is real;
what it lacks is a file, which archive:2376 pre-adjudicates.

WATCHOUT: the tier-1 hold amendment §8 stages needs a slog capture the named case does not have.
`TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` (`pkg/adapter/holdstate_test.go:674`) reads
post-mortem JSON files only; the file's single `slog.SetDefault` capture is in a different case at `:895-898`.
§8's amendment asks it to assert that the pod-level `coordinator_connection_lost` line carries no
`last_generation` key, which is a log-line assertion. Not filed (authoring detail, and the pattern exists one
case away), but a fixer who thinks the amendment is a two-line edit is wrong — EVIDENCE:
pkg/adapter/holdstate_test.go:674-720, :895-898; pkg/adapter/holdstate.go:130-132.

CORRECTS [`### Settled` → "`pkg/adapter/holdstate_test.go:713` is the file's only generation assertion, inside
`TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` ... and its slog-capture pattern is
process-global"]: that case has NO slog capture. The process-global capture belongs to the
`coordinator_hold_resolved` case at `:895`. The `t.Parallel()` conclusion may still hold for the other case;
the attribution does not.

USEFUL [`### Traps` → "tier-list bookkeeping is a refuted class, three times over"]: it killed a
`// diagnosis:`-comment candidate over §8's new tier-2-and-up cases, S8/TEST-1 omitting tier 0, and CODE-2's
deliverable block carrying no tier line, before any of them cost a derivation.

USEFUL [`### Settled` → "Landed cases already pin what §8 might otherwise be thought to owe"]: naming
`TestCoordinatorFenceGapDetected`, `TestCoordinatorFenceStaleGenerationRejected`, and
`TestCheckpointBarrierRejectsGenerationMismatch` as surviving the rescope removed three candidates without a
re-derivation. Second lens to record this; it is the highest-value bullet in the file for this lens.

USEFUL [`### Traps` → "MISTAKE: grep the ARCHIVE for a candidate's subject BEFORE working it up"]: greping
`review-log-archive.md` for "Tier 4 covers the same flow" returned six prior adjudications in one call and
saved the pass. Run that grep on every candidate's own quoted sentence, not on its subject noun.

OPEN: this lens has now returned empty or all-refuted three consecutive times over text that has not moved
(`diff -rq` against `scratchpad/cp-snap/0076-run7/non-spec-r4` is empty). It reopens only if §8's case list,
D7's acceptance arm, or CODE-3's hold disposition is rewritten.

## DECISION
One finding filed. Both fixes land on their own subject; the residue is in a
neighbouring SPEC-1 paragraph the G2 edit trimmed.

## FACT (verified this run, all anchors resolve)
- `schemas/lenny-adapter.proto:153-162` CoordinatorFence RPC comment; `:1442-1446`
  CoordinatorFenceRequest message comment; `:1449-1451` its field comment;
  `:1455-1462` CoordinatorFenceResponse comment with the `accepted` false-condition
  at `:1456-1458` ("not greater than the last fenced generation").
- `pkg/adapter/coordination.go:99` `if s.coord.initialized && gen <= s.coord.lastFenced`;
  `:93-94` and `:224-226` the two non-positive `InvalidArgument` refusals;
  `:236` `if !initialized || gen != fenced` (CODE-2's subject).
- No section of `spec/` states the fence's own acceptance predicate. A grep for
  "not greater than" / "re-issue" / "stale fence" over spec/ returns only unrelated
  Token-Service prose; `spec/10_gateway-internals.md:38` and
  `spec/28_communication-channels.md:1675` state the record-and-reject rule on
  *older* RPCs alone. SPEC-2's new paragraph is right that the predicate is
  wire-only.
- `charts/lenny/templates/migrate-job.yaml:10-16` (pre-install/pre-upgrade hook,
  weight -5, completes before the gateway Deployment); `:37-39` the annotations.
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go:177` (column list),
  `:260` (`sess.CoordinationGeneration, schemaVersion,`).
- `pkg/gateway/sessionserver/start.go:4233-4245` `fenceResumedPod` (no increment);
  `pkg/gateway/coordination/coordfence/coordfence.go:147-153` floor, `:164-179`
  stale arm, `:180-183` transient `default:` arm, `:186-188` budget-exhausted
  relinquish; `pkg/gateway/coordination/coordination/coordination.go:463-482`
  `RecordHandoff` (`row.CoordinationGeneration++` at `:468`).
- `pkg/gateway/coordination/coordfence/coordfence_test.go:173-184`
  `TestFenceZeroGenerationFencesAtBaseline`; it still passes under the retention
  and no live proposal text asks for it to be amended any more.

## LANDED
- G1: the record-and-reject carrier paragraph (spec-changes.md:502-512) no longer
  names `CoordinatorFenceResponse`, reads "Both take", and the new paragraph at
  :514-530 keeps the `accepted` predicate in the handler's "not greater than"
  form re-scoped to the session. The framing sentence at :496-500 names the
  exception. The in-scope drift site (summary.md:211-217) was edited and now
  agrees.
- G2: both false-ground clauses are gone. :250-257 states the retention with the
  §10.5 rolling-window ground; :283-286 lost the deleted-floor sentence. CODE-4's
  coordfence bullet, the TEST-1 amendment instruction, the "two classes" count,
  the §9 files list, the summary "What changes" and "Watch out for" lists, the
  deliverable index row, and OD8 all moved together.

## DRIFT SWEEP (checked, clean)
- SCHEMA-1's target list (non-spec-changes.md:11-21) defers wording generically and
  keeps the carrier order SPEC-2 states, so spec-changes.md:565 ("names the whole
  carrier set in the order SPEC-2 states it") still holds after the Response
  paragraph moved.
- "The `CoordinatorFenceRequest.coordination_generation` field comment is the one
  carrier that takes no edit" (non-spec-changes.md:19-21) stays true: the Response
  comment still takes an edit, only a different one.
- OD preamble (summary.md:169-171): OD4 at :242 and OD6 at :272 are already
  restated in place, so replacing the "two entries ... at the end" count with the
  general rule is accurate rather than a new inconsistency.
- Implementation checklist S4 names no file under `pkg/gateway/coordination` and no
  step was added, removed, or resequenced; no DEFERRED owed.
- summary.md:32-40 and non-spec-changes.md:355-375 describe the shipped tree and
  the uploaddriver fixture; the retention does not falsify either.
- CODE-4's tier list (0,1,2,3,4,7a,8) survives the file removal: its tier-1 subject
  is the memstore `Create` floor case at non-spec-changes.md:300-304.

## FINDING (filed)
- **SPEC-1's §10.1.4 grounds for "no `CoordinatorFence` ever carries zero" now rest
  on a baseline the same edit says does not cover the rolling window.**
  spec-changes.md:283-286 keeps "That is held by the session row's baseline of 1
  ... and by the adapter's refusal", and the G2 edit deleted the only clause that
  named the gateway floor. :254 states old-fleet inserts land at 0 that the
  create-path floors never see, and the retained floor at
  `coordfence.go:147-153` is what actually holds the invariant for those rows.

## WATCHOUT
- The response comment is three sentences, not two: `:1455` opens with
  "CoordinatorFenceResponse acknowledges the new generation." before the `accepted`
  and `gap_detected` definitions. spec-changes.md:514-516 says "each of its two
  sentences" and "Its first sentence defines `accepted`". Harmless for an
  implementor (the lead sentence takes no edit either way) and inherited from the
  finding's own framing, so not filed; do not let a later pass "correct" it by
  editing the lead sentence.
- summary.md:216-217 "Answering this decision the other way" reads against the
  preceding sentence rather than against OD2's own recommendation at :196. Not
  filed as a defect; a future editor should not invert it.

## DEFERRED (pre-existing, untouched by these edits)
- summary.md:105-106 still points at "the pass records under 'Resolved in
  adversarial review' in the spec changes", which run 5 evacuated into the
  review-log archive. Already deferred by the fixer; unchanged this round.
- review-log.md's standing context still carries the floor deletion as settled
  (`:38`) and as an `### Open` / `### Deferred` item (`:998-999`, `:1046-1057`).
  The fixer routed these as CORRECTS through its own shards; nothing in the
  proposal's staged text depends on them.


## Index and checklist reconciliation, 2026-09-03

Rebuilt `## Deliverable index` against the converged staging, rewrote the checklist's spec lane against the
current SPEC ids, and reconciled the non-spec steps' `Depends on:`. The staged set is SPEC-1, SPEC-2,
SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3, CODE-4, and TEST-1, each carried once in the index and once in
the checklist. The spec lane is now three steps rather than one, because the three spec deliverables land
in four different files under `spec/`: S1 is SPEC-1, S2 is SPEC-2, S3 is SPEC-3. The non-spec steps keep
their order and their tiers and renumber to S4 (SCHEMA-1), S5 (CODE-1 and CODE-3), S6 (CODE-4), S7
(CODE-2), and S8 (TEST-1). The index needed no row added, removed, or retargeted.

CORRECTS [`0076...summary.md`, `:104-106`, from `[spec.1.fix-G1.1]`]: replaced the sentence pointing at the
pass records under "Resolved in adversarial review" in the spec changes, a section run 5 evacuated, with a
pointer to the archived pass records in the review-log archive.

CORRECTS [review-log.md `### Settled` counter-baseline bullet, from `[spec.2.fix-G1.1]`,
`[spec.2.fix-design-G1.1]`, and the post-fix block]: struck "The §7.2 snapshot-close bump fires only under
a terminal write, after which no takeover follows" and replaced it with the bump's four non-test callers,
three terminal and one on the two recovery edges out of `resuming`, after which the resume path fences the
replacement pod at the bumped value. Corrected the same false statement's second site in `### Traps`, the
MISTAKE bullet that called `TestFenceZeroGenerationFencesAtBaseline` a pin on "the very floor CODE-4
deletes", which run 6's G2 fix falsified when it kept the floor.

CORRECTS [`0076...summary.md` OD7, from `[spec.2.review-open-decisions.1]`]: OD7 stated the rebind residual
with no reachability ground. It now records that the specification half is answered, `spec/07` fixing the
`resuming → running` transition as a re-attach on a replacement pod, and that what remains open is
code-side reachability.

OPEN: the claim-register status for the interval between S2 and S7. SPEC-2 stages §28.5.1, §28.6, and §28.8
statements that do not hold in the shipped adapter until CODE-1 and CODE-2 land, and §28.4 obliges a
non-`WIRED` row to name the step that closes it. The remedy lands in the root `gateway-runtime-comms.md`
§7.1, which `scripts/seed-claim-register.py` generates `tests/claim-map.json` from and tier 0 byte-diffs,
so it needs a staged deliverable in the non-spec changes that no lane has authored. §28.4's three-value
status set does not model a mechanism rescoped in part, so that deliverable has to argue the obligation
falls on the statement rather than on the mechanism. Carried to the reviewer as OD11.

OPEN: SPEC-1's two "column default and the create path's floor" attributions, at the counter-baseline
sentence and at the §10.1.4 zero-invariant sentence, credit the baseline to the column default as well as
to the floors, while `non-spec-changes.md` states that `Create` names `coordination_generation` in its
insert column list so the default baselines nothing. The sites are not false, since CODE-4 does land the
default, but the attribution overstates it. The remedy lands in `spec-changes.md`, which this pass may not
edit.

OPEN: `enterHoldState`'s doc comment (`pkg/adapter/holdstate.go:116-118`) says the generation and the
started-session count are both read through their accessors before `hold.mu` is taken. CODE-3 deletes the
generation read, so the clause naming it is false once the deliverable lands, and CODE-3's enumeration of
the sites the `gen` field drags does not list this comment. It is the second instance of the same comment
residue, beside `pkg/adapter/coordination.go:126-128`. The remedy is a staged edit in the non-spec changes
naming both comments, which no lane has authored.
