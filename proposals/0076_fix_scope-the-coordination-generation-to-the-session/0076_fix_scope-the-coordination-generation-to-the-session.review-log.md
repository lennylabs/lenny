# Review log: Scope the coordination generation to the session

## Standing context

**Compaction pass 24, 2026-09-04.** Read the whole ledger, which covers run 7's non-spec rounds 2 through 6 (four fix and post-fix
records plus five full lens sweeps over staging that never moved a byte) and run 8's spec round 1 (thirteen shards, six of which filed),
along with the trailing run-6 post-fix block and the index-and-checklist reconciliation of 2026-09-03. The ledger is left untouched; the
round boundary archives it whole and with its ids as soon as this pass returns.
Lifted into `### Settled`: the directory-level escape from the spec-map gate, the exported test hooks that close two long-standing
UNVERIFIED items, `prestop`'s post-barrier loop being sequential despite its `WaitGroup`, the session-keyed slot registry that makes D6
true by construction, the single `INSERT INTO coordination_lease` that makes 0181's mirror default cosmetic, the two-file generated proto
chain, the absence of any `column_default` reader and of any migration number under `docs/`, `charts/`, and `spec/`, §10.1.5's
already-per-session steps, the §28.8 `CH-FENCE` cell's two staged clauses, the barrier rejection status having only proto carriers, and
the `git log` and `diff -rq` checks that price a whole pass in one call. Lifted into `### Traps`: the `RecordHandoff` return sentinel a
mechanical "each shifts by one" sweep would corrupt, the readopter-argument assertions class 1's opening phrase does not cover, the
`## Open decisions` materiality rule twelve refutations established, the summary's phantom opposite-order acquisition, the barrier's
unbounded adapter-side wait, and five reading and grep hazards.
Honoured seven corrections against the standing context: `lenny_coordinator_fence_retry_total` does have an §16.1 row, a catalog entry,
and a docs row, so only one of the two recorded observability drifts is real; `pgstore.New` is at `:60`, which is what the staging cites,
so that drift entry was this log's error rather than the proposal's; the hold case §8 amends carries NO slog capture, the process-global
one belonging to the `coordinator_hold_resolved` case at `:895-898`; `spec/07`'s third fence-surface line is `:398` rather than `:222`;
OD10 is now withdrawn in place on the mirror-lag ground and its cited clause range is `:259-264`; the `docs/` count varies with the grep,
and three, nine, and thirteen all describe the same unit-neutral set; and the hold-allowlist citation `spec/10:56` is one line off.
Closed and deleted: four `### Open` items (the deterministic `s.ops.Begin` stall, the tier-7a real-`Checkpoint`-stream question, CODE-1's
supposedly unreachable tier 2, and the tier-4 co-tenant fixture question), and one more promoted out of `### Open` into `### Deferred`
because its window is now verified and only its remedy is unapplied. `### Deferred` gains four whole entries and ends at nine.
Two subjects disagree with no correction between them and both readings are kept: run 7's material skeptic refuted the
`CoordinatorFenceRequest` tail deletion while three run-8 shards filed it again as live, and the standing `### Settled` bullet says the
§10.1.4 zero-invariant's ground was repaired while the run-6 post-fix block files it as unrepaired. Each carries an `UNVERIFIED` line
naming both readings, and the older reading of each is noted in `## Retired`.
**The target of 200 lines was not reached and the section grew again, to 1,923 lines against pass 23's 1,620**, of which `### Settled` is
870, `### Traps` 627, `### Open` 284, and `### Deferred` 103. Nothing was dropped to reach it. The
window under compaction was five consecutive rounds of empty returns over byte-identical text, so nearly everything it produced is either
a do-not-re-derive fact or a recorded dead end, which is exactly the material that stops the next round costing what this one did.
`### Traps` is where the length lives and it is the section the shards cite: this window credits standing entries by subject more than
forty times, and every citation names a body a one-line summary would delete. Decline the trade until the code and test lanes land and
the inventories become checkable against a tree.
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
  assertion, inside `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` (declared `:674`), which fences `sess-a` at 7 and
  then asserts `rec.LastGeneration != 7` for BOTH sessions in a loop, so §8's description of it is exact. CORRECTED in pass 24: that case
  carries NO slog capture. The file's process-global `slog.SetDefault` capture belongs to the `coordinator_hold_resolved` case at
  `:895-898`, so §8's amendment (assert the pod-level `coordinator_connection_lost` line carries no `last_generation` key) needs that
  pattern imported into the case and is not the two-line edit it reads as. The `t.Parallel()` bar belongs to the capturing case.
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
  ground "each stays true" reaches that conclusion by a route that does not hold. UPDATED in pass 24: OD10 is now WITHDRAWN in place in
  `summary.md`, attributing the no-restatement disposition to SPEC-1 rather than SPEC-2, recording that SPEC-1 rewrites §10.1.8 step 1's
  closing sentence (`spec-changes.md:206-222`) on the same physical `spec/10:183` line as the clause OD10 is about, and closing on the
  mirror-lag ground. `summary.md:21-24` was rewritten in the same edit and no longer asserts that those sentences stay true. The staged
  ground's range is `spec-changes.md:259-264`, not the `:257-262` this bullet used to cite, and the clause is still live in
  `spec-changes.md`, now carried as a `### Deferred`.
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
  class. That register is `t:<tenant>:lease:session:<session>` in Redis, compare-and-set with a 60s expiry, and both the `CH-ATTACH` and
  `CH-FENCE` Exclusivity bullets state the Postgres `SELECT ... FOR UPDATE SKIP LOCKED` fallback explicitly (`spec/28:245-248`,
  `:329-332`). Outside `spec/10`, `spec/28`, `spec/29`, and `spec/04` the fence surface is `spec/18:238`, `spec/07:93`, `:215`, `:398`,
  `spec/11:216`, and `spec/12:160`, none a pod-side gate. CORRECTED in pass 24: the third `spec/07` line is `:398`, a
  `recovery_generation` sentence cross-referencing §4.2, rather than the `:222` this bullet used to name; it is neutral either way, so
  only the line list was wrong.
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
- **The spec-map gate's escape is directory-level mapping, which is why no new file this proposal stages can orphan.**
  `validateTestFilesMapped` (`cmd/lenny-test/cmd_validate.go:716-790`, run inside tier 0 from `cmd_run.go:761-769`) walks only the
  component-and-above tier directories, so `tests/tier7a_load_local` and `pkg/adapter` are outside it entirely, and it resolves a file by
  the exact path and then each parent directory. `tests/tier2_component/migrations`, `tests/tier3_contract/adapter_checkpointbarrier`, and
  `tests/tier4_integration` are all present as bare directory entries in `tests/spec-map.json`, and the `pgstore` case lands in the
  existing `sessionstore_test.go`. `tests/tier3_contract/adapter_generation_fence` is mapped at FILE level, so a new file placed there
  would orphan; the staged barrier case belongs in `adapter_checkpointbarrier` anyway. Do not re-file the 0073-S22-precedent finding.
- **The external `adapter_test` package already has every hook §8's cases need.** `BeginCheckpointOpForTest(ctx, sessionID)` takes the pod
  op lock and returns its release func, `WaitPendingCheckpointForTest(sessionID, timeout)` blocks until a checkpoint enters the lock's
  pending set, and `RegisterUnboundSlotForTest` and `ClaimSessionForTest` are exported beside them (`pkg/adapter/export_test.go:61`,
  `:71`, `:80`). The mid-flight deregistration case is therefore constructible deterministically with no sleep and no new test-only hook.
  A tier-7a or tier-4 case can also drive real `Checkpoint` streams through `coordfixture.Pod.Client`, because
  `adapterclient.Client.Checkpoint(ctx)` returns the raw stream and `Client.CheckpointBarrier(ctx, sessionID, gen, barrierID)` is the
  matching barrier driver (`client.go:425`, `:436`, `:467`). These closed two `### Open` items; do not reopen either.
- **The mid-flight case's two triggers are not equally reachable and the reachable one is enough.** `fireHoldTimeout` lives in
  `pkg/adapter/holdstate_test.go:556` (`package adapter`), so the hold-timeout arm cannot be driven from the external `adapter_test`
  package where §8 places the case; the `Shutdown` arm is an exported RPC and works. A fixer editing that bullet could drop the dead arm.
- **`coordfixture.NewReplica` takes `bind ...string` and its `FenceReadopter` holds exactly one `*Pod`,** so one replica coordinating two
  co-tenant sessions on one pod is constructible as §8's tier-4 bullet describes, with no second replica, second pod, or fixture change
  beyond CODE-1's session-keyed `Pod` methods (`coordfixture.go:302-316`). `ReadoptAndFence` already takes the `sessionID` it discards, so
  CODE-1's accessor rescope is a pure signature change with the argument in hand at every call site.
- **CODE-1 and CODE-3 genuinely reach tier 2 and the target case exists.**
  `TestHoldTerminatedSessionDecrementsItsSlotExactlyOnce_spec_10_1` drives the §10.1 coordinator-loss hold to termination against a real
  `adapter.Server` over envtest (`tests/tier2_component/slotrelease/revoke_double_teardown_test.go:119`, `:341`, `:365-395`). This closes
  the older `### Open` asking whether any tier-2 package touches the accessors.
- **`prestop`'s post-barrier loop really is sequential, and its `WaitGroup` is a red herring.** `prestop.go:387` declares a `sync.WaitGroup`
  and `:419` calls `wg.Add(1)`, which reads as concurrency, but the closure at `:422` is invoked synchronously with no `go` and carries the
  comment "Run sequentially in v1 so the budget is honored per-session against the same clock"; the single `wg.Wait()` is at `:448`. Do not
  reopen the D7 drain arithmetic on the `WaitGroup`.
- **The slot registry is keyed by session id, so D6 holds by construction.** `ensureSlotStateLocked`, `workspaceRootForSession`, and
  `checkSessionBound` all index `s.slots[sessionID]` (`slot.go:82-102`, `:131-137`, `:153`; `slotsession.go:267-276`), so relocating
  `lastFenced` onto `slotState` cannot leak one session's fenced value onto the next session to occupy a slot. A run-7 shard spent a whole
  pass hunting a cross-session-reuse defect here; it does not exist.
- **`coordlease/pgstore.Upsert` is the only `INSERT INTO coordination_lease` in the tree and it names the column in both the insert list
  and the `ON CONFLICT DO UPDATE` set** (`pkg/gateway/coordination/coordlease/pgstore/pgstore.go:47-58`), so 0181's `DEFAULT 1` on the
  mirror column can change no row's value, and the missing mirror backfill self-heals within one sweep because `upsertMirror` runs for
  every eligible held row on every cycle. Five lenses derived this independently; it is closed.
- **The generated chain from the proto is two files and nothing else.** `schemas/buf.gen.yaml:16-27` runs only `protoc-gen-go` and
  `protoc-gen-go-grpc` into `../pkg/proto`, so SCHEMA-1's `lenny-adapter.pb.go` plus `lenny-adapter_grpc.pb.go` pair is complete: no
  descriptor set, no SDK codegen, no generated doc. The `CheckpointBarrier` RPC comment lands at `_grpc.pb.go:191` and `:643`, which
  SCHEMA-1 does not enumerate and does not need to, §9 listing the whole file as regenerated output.
- **No test in the tree reads a `column_default`, and no `docs/`, `charts/`, or `spec/` file enumerates a migration number.** The only
  `column_default` reader is `pool_delivery_mode_test.go:90-99`, scoped to warm pools, and `prodMigrationSchema`'s `columns` field carries
  column NAMES only, so `TestProdMigrationsApplyExpectedSchema` asserts presence rather than default; `grep -rn "0180\|0179\|0178" docs/
  charts/ spec/` returns nothing. Two greps close the whole migration edit-site question for 0181. `prod_schema_test.go`'s `prodTables`
  lists only migrations that CREATE a table, so 0181 needs no row there, and `tests/tier2_component/migrations/phase3_gate_test.go` keys
  on `DROP COLUMN`, so 0181 reaches neither it nor lint pass 5.
- **§10.1.5 (Stale Replica Behavior) is not an edit site, though it looks like the obvious one.** It is the section tying
  `lenny_coordinator_handoff_stale_total` to the stale rejection (`spec/10:65-71`), and every one of its four steps is already written per
  session ("Cancel all in-flight RPCs for that session", "Discard all cached session state ... for that session"), so the unit change
  reaches none of them.
- **The §28.8 `CH-FENCE` cell carries TWO independently staged clauses in one field, and SPEC-2 stages both.** Field `$5` of `spec/28:1807`
  holds the window sentence and the whole gap-reset sentence. This log lists that cell twice, once under the window-clause sites and once
  under the gap-reset mirrors, so a reader who follows only one list concludes the other clause is an unstaged site. It is not:
  `spec-changes.md:374-381` stages both in one bullet.
- **The barrier's rejection status has exactly two carriers and both are proto comments SCHEMA-1 rewrites,** `proto:1472-1473` and
  `:1478-1479`. `FailedPrecondition` occurs in `spec/` only at `spec/07:208` and `spec/15:1136`, both about setup-command exits, and
  nowhere for the fence or the barrier. If both barrier carriers take a step-3 replacement with no preservation clause, the published
  contract loses the status entirely, which is the same defect as the `CoordinatorFenceRequest` tail and has the same one-clause remedy.
- **`lenny_coordinator_handoff_stale_total` has exactly one incrementer,** `Fencer.incStale` (`coordfence.go:205`, reached from the fence
  path's `FailedPrecondition` arm at `:164-170`). The barrier's `FailedPrecondition` becomes `ErrGenerationStale` (`wiring.go:52`) and
  sets `out.Stale` with no counter (`barrier.go:230-232`), so D7 moves no count on any catalogued metric.
- **`lenny_adapter_coordinator_hold` is registered with a nil label set** (`pkg/adapter/metrics.go:105-111`, setter at `:219-227`), so D5's
  "the gauge remains pod-scoped" is exact rather than approximate and nothing about the per-session move can widen it.
- **The migration backfill does not move the §10.1.7 supersede fence.** Supersede-on-write soft-deletes every active partial row at or
  BELOW the incoming generation and the reject arm fires only on a strictly higher stored row, so stored-0 against incoming-0 already
  superseded and stored-0 against incoming-1 supersedes identically (`partialmanifeststore.go:394`, `:407`; `pgstore.go:100`, `:117`;
  `uploaddriver.go:422`). A security shard worked this up new and disproved it from the tree; do not re-derive.
- **The nil-`Checkpointer` barrier hang is a dev-only path.** `barrierCheckpointer` is nil only when `w.checkpointSvc` is nil, and
  `checkpointSvc` is constructed unconditionally inside the `--agent-namespace` branch (`stores.go:2156`); without that flag there are no
  agent pods and no `barrierDispatch` at all. Separately, on a missing local binding the checkpoint and the barrier fail TOGETHER
  (`checkpointer.go:424-427` returns `ErrNoBinding` before any stream opens; `wiring.go:62-69` fails the barrier with "no adapter
  connection"), so the "accepted barrier blocks because nothing links the gate" hang is not differentially reachable on that path.
- **The lane names the pipeline accepts are `spec`, `code`, `schema`, `migration`, `test`, and `docs`,** and an unrecognised lane stops the
  run rather than being guessed (`.claude/skills/implement-proposal/SKILL.md:19`, `:21`), so S4's `schema` and S8's `test` are valid.
- **Three cheap mechanical checks price a whole pass on this proposal.** `git log --oneline -- <spec-changes.md>` returning `f9c85f30c` as
  the newest commit means the spec staging has not moved since run 6's spec loop and every standing spec disposition applies unchanged.
  `diff -rq` against the round's snapshot under `scratchpad/cp-snap/0076-run7/<lane>-r<N>[-start]` says whether any staged byte moved; the
  `md5sum` of both staging files is identical across all ten run-7 snapshots and the live tree. A sibling lens's cached answer for the same
  hash sits at `scratchpad/cp-cache/0076-run7/<lens>-r<N>-<hash>.json` and one `cat` shows what a previous run of your own lens closed;
  read its `coverage` field first, because the key carries no lane.
- **The problem-statement file's citations all resolve and were never in any standing inventory until now.** `coordinatorfence.go:48`,
  `server.go:302`, `coordination.go:25`, `:84`, `:108-118`, `:112-113`, `:119-121`, `:211`, `proto:1449-1451`, `coordfence.go:164-179`,
  `:195-200`, `coordination/coordination.go:399-416`, `:463-468`, `start.go:3668-3672`, `barrier.go:209-232`, `:237-246`,
  `prestop.go:390-397`, `holdstate.go:39-44`, `:119`, `:225-229`, `:283-296`, `slotsession.go:267`. Do not re-derive this set.
- **The cross-proposal table verifies whole, and both targets are freely invalidable.** 0060 and 0073 are `Implemented`, 0075 and 0080 are
  legacy single-file `Draft`s (0080 self-describes as an early draft), 0075's stated ground is verbatim at its `:88-89`, 0080 records the
  hold-partitioning question at `:184-192` and the hold-release item as taken by 0076 at `:211`, and 0062 is Retired and needs no row.
  `tests/claim-map.json` claim 74 (`Adapter/AdapterEvents` client) is `UNWIRED` under `R12`, so "the gateway has no CH-ADAPTEREVENTS
  client" holds. Read the claim map with a JSON walker: a line grep for `R12` hits claim 0 first, whose note is about the adapter metrics
  endpoint, which reads as the summary citing the wrong row.

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
- **Lens exhaustion, counted, with the reopening condition each lens stated for itself.** RECOUNTED in pass 24 across run 7's five
  non-spec rounds and run 8's spec round, in which the staged files did not move a byte: docs-alignment has returned empty seven times,
  security seven or eight, reliability six, edit-sites six, performance five, kubernetes five across both lanes, applicability, operational,
  client-surface, and test-coverage three or four each, mechanism and feasibility twice each over byte-identical text. The conditions the
  shards named: kubernetes reopens only if CODE-4 grows a chart or CRD deliverable, CODE-3 grows an apiserver or `Sandbox.status` write, or
  D7's acceptance arm is rewritten; reliability and security only on D7's acceptance arm, the retained `coordfence` floor's rolling-window
  behaviour, CODE-1's entry-lifetime rule, or §10.1.8's failure-arm claim; client-surface only on a proto field, message, enum, or RPC
  change, a `schemas/*.json` change, an SDK file, or a CRD change, a comment-only proto edit not reaching it; edit-sites and citations only
  on SCHEMA-1's carrier list, §8's case set, §9's file list, or SPEC-2's proto paragraphs; test-coverage and feasibility only on §8's case
  list, D7's acceptance arm, CODE-1's resolve rule, CODE-3's read target, or CODE-4's migration text. Every empty return costs the other
  eleven lenses a full round. A further run of any of them
  buys nothing unless SPEC-1's step-3 wording, the §28.6 second-opener clause, D7's acceptance arm, SPEC-3's baseline, or §8 is rewritten.
  Where a lens must run anyway, the cheap division of its budget is the one run 6's citation lens derived after spending a third of its
  pass re-resolving the whole anchor set for nothing: spot-check the sentences a fix touched, and spend the rest on sentence-versus-clause
  structure inside cited cells, which is where both surviving defects of that class live.
- **MISTAKE, AND THE DEFECT IS LIVE: the §28.8 `CH-BARRIER` disposition clause was copied from the `CH-CHECKPOINT` bullet above it,** where
  it is true. The `CH-CHECKPOINT` cell states the constraint in one sentence and the edited rejection rule in a second; the `CH-BARRIER`
  cell has only two sentences and the replaced clause is the trailing clause of the constraint sentence itself, so "the cell's constraint
  sentence ... unchanged" tells an applier to leave the clause standing. CORRECTED in pass 21: this entry used to read as a defect that was
  found and fixed. It was found in run 4's spec round 2, recorded as a MISTAKE tag rather than filed as a finding, and never repaired;
  four lenses re-filed it in run 6 round 1, and six lenses have now filed it across four rounds without a single fix round touching the
  line. UPDATED in pass 24: it is still live in run 8 and only its offsets moved. The bullet now runs `spec-changes.md:390-400` and the
  offending preservation clause sits at `:394-395`, against the `:386-392`/`:390` this entry used to give. Anchor on the sentence text
  ("The cell's constraint sentence and its closing sentence"), never on the offset, and treat it as live until that text changes. The
  applied consequence is concrete: an applier who honours it leaves `spec/28:1808` carrying "so a barrier from a superseded replica is
  rejected on the stamp", which assigns the pod an action it cannot perform, since `CheckpointBarrierRequest` carries no sender identity. It is the only one of the nine SPEC-2 §28 bullets that names a whole sentence as unchanged while replacing
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
- **WATCHOUT: ONE pre-existing observability drift sits beside this surface and is not made wrong by it.**
  `docs/reference/metrics.md:196` states the barrier-ack outcome set as `success, timeout, error` where `spec/16:41` has
  `partial_captured` as well, with `docs/operator-guide/upgrades.md:52` repeating the docs list. It predates the staging and belongs to a
  docs loop. CORRECTED in pass 24: the other half of this entry was false and cost a lens a hunt. `lenny_coordinator_fence_retry_total`,
  which `spec/16:552` points operators at, HAS an §16.1 inventory row (`spec/16:191`), a catalog entry
  (`pkg/observability/metrics/catalog.go:273`), a catalog-test entry (`catalog_test.go:97`), and a docs row
  (`docs/reference/metrics.md:311`). There is no drift there at all; do not go looking for the phantom.
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
  reading. The test-side half is the standing `### Open` "Barrier for an unbound session". UPDATED in pass 24: run 8's mechanism shard
  argued past that refutation and FILED it, on the ground that the sentence the refutation leans on is PROPOSAL prose at
  `spec-changes.md:140` while none of the three sentences SPEC-1 actually stages into §10.1.2 carries the word "bound", and the step-3
  replacement is quoted word for word so an applier has nothing to add. It also names the propagation: the unqualified form reaches three
  staged sites, `spec-changes.md:159-162`, `:347-349`, and `:384-386`, rather than one. If a verifier refutes it a second time, the
  refutation must say where in the APPLIED spec the binding restriction lives, because that shard could not find one.
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
  instead. CORRECTED in pass 24: the `pgstore.New` entry that used to sit here was this log's own error rather than the proposal's.
  `pgstore.New` IS at `pgstore.go:60`, which is exactly what the staging cites; two shards checked it independently. Do not "correct" it.
  The rest of the do-not-file drift list, each one line off and each inside the right function or literal: `checkpointbarrier_test.go`'s
  `BarrierWaiting()` read at `:162` against a cited `:163`; `scripts/lint-migrations.sh`'s `TEST_DIR` at `:44` against a cited `:45`;
  `cmd/lenny-test/cmd_run.go`'s lint-migrations static check opening at `:634` against a cited `:635-641`; the tier-7a colocation file's
  `CoordinationGeneration: 0` seed at `:145` against a cited `:144` (`:144` is the `ID:` line of the same `:143-146` literal) and its
  zero assertion at `:288-289` against a cited `:287-288`; `memstore.Create`, where a hand count off a `sed` window gives `:45` and
  `grep -n` gives the cited `:46`; the hold allowlist, where this log cites `spec/10:56` and the "Hold state semantics" bullet is at
  `:57`; and `spec/29:1186` for a phrase that sits on `:1185`.
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
- **WATCHOUT: applying §8's class-1 "each shifts by one" rule mechanically across the `:275`-`:594` span corrupts a landed assertion.**
  `tests/tier2_component/coordination/sweep_test.go:322` asserts that `RecordHandoff` RETURNS 0 for a terminal session ("bump refused"),
  which is the post-increment return sentinel rather than a row read, so the baseline does not move it. A fixer sweeping the span by rule
  changes it and turns the case green for the wrong reason — EVIDENCE: tests/tier2_component/coordination/sweep_test.go:318-327.
- **WATCHOUT: class 1's opening phrase does not cover three kinds of shifting assertion that sit inside its own span.** The clause that
  covers them is "or a number of handoff bumps counted from it", not "every assertion that reads a session row's
  `CoordinationGeneration`". The three: `sweep_test.go:431-432` and `:524-525` assert `readopter.gens[0] == 1`, the generation handed to
  the FENCE rather than read off a row; `coordination_takeover_test.go:100` asserts `readopter.calls[0].generation != 1`, a third kind
  again; and `sweep_test.go`'s `:475-525` subtest reads like class-3 residue because its subject is the `RecordHandoff` 0-sentinel while
  it is plain class 1. Do not work any of them up as a missed class.
- **WATCHOUT: the tier-8 crash-takeover file's shift runs through the `pgstore` floor, not the memstore one.** It builds
  `sessionpg.New(pg.Pool)` at `:85`, while `sweep_test.go`, `coordination_takeover_test.go`, and `uploaddriver_test.go:293` build
  `memstore.New()`. A step split that lands one floor without the other therefore moves a different set of files than a reader assumes.
- **MISTAKE: `derive_failure_audit_test.go:46` greps as a missed class-1 site and is not one.** It mutates a returned row copy inside a
  `fenceStore.Get` stub and asserts nothing on the value, but `row.CoordinationGeneration++` greps as a write. Two lenses reached it
  independently. `resume_chunk_selection_internal_test.go` is the same shape: it builds `sessionstore.Session` literals passed straight to
  the function under test rather than through any store's `Create`.
- **MISTAKE: the `## Open decisions` lens has one operating rule and twelve refutations established it.** A finding from that lens
  survives only when it is a hard false file:line citation or when it reaches a staged deliverable. Entry hygiene, framing, completeness
  polish on a decision briefing, and withdraw-in-place remedies are refuted on sight, because an entry in `## Open decisions` stages no
  spec edit, no code, and no test, so its wording cannot make the applied spec or the implementation wrong. One round's shard filed seven
  and six were refuted by name (OD1, OD4, OD9, OD11, OD12, and the fill-the-blanks header); a later round filed three under the rule and
  they landed. Read this before filing from that lens.
- **MISTAKE: the summary's "one opposite-order acquisition in the tree" does not exist.** `summary.md:62-63` and `:378-379` say "The one
  opposite-order acquisition in the tree is the read CODE-3 removes". `enterHoldState` reads through `s.LastFencedGeneration()` at
  `holdstate.go:119` and RELEASES `coord.mu` before taking `hold.mu` at `:122`, and the tree's own comment at `coordination.go:124-128`
  gives exactly that as the reason the `coord.mu`-then-`hold.mu` order in `CoordinatorFence` is deadlock-free, so that read is not an
  inversion under the declared order at all. The real reason CODE-3 must remove it is that it has no session id to pass. Two lenses
  reached this independently and neither filed it (rationale in a decisions list, breaking nothing applied); a fixer already in that
  bullet should say "the one read that would become an inversion if it moved inside the hold critical section", or drop the clause.
- **WATCHOUT: the barrier's wait is bounded by the CALLER's deadline, not by anything the adapter holds.** `CheckpointBarrier` selects on
  the gate's `done` and on `ctx.Done()` alone with no adapter-side timeout (`coordination.go:264-269`), so the only thing stopping a
  barrier for a session deregistered mid-flight from hanging forever is prestop's `context.WithTimeout(ctx, h.barrierAckTimeout())` around
  the whole `Dispatch` (`prestop.go:503-505`). A lens tempted to file "the per-entry gate has no reclaimer when the hold timeout removes
  the entry" should stop here: nothing signals that gate under the pod-wide gate either, and the 90s client deadline is the reclaimer in
  both designs.
- **WATCHOUT: `pkg/adapter/checkpoint.go:108-110` asserts the op lock "is uncontended there by construction",** on the ground that the
  barrier's quiescence has already drained dispatch. That is false on a co-tenant pod, and §8's own tier-7a bullet says so. It is false
  PRE-fix too, because the gateway drives N streams concurrently whatever the barrier returns, so it is pre-existing drift in a file §9
  already lists and refuted class (k) bars filing it. Fix the comment if editing that file.
- **WATCHOUT: three grep and read hazards that each cost a shard part of a pass.** `grep -c "A pod validates"` on the proto returns 0
  because the phrase wraps, which reads as a dead anchor; `awk '/^message /{m=$2} /A pod validates/{print NR": "m}'` returns the twelve.
  `grep -o '`[^`]*`'` over `non-spec-changes.md` silently drops `pkg/adapter/server.go:302`, because CODE-1's sentence wraps a backticked
  identifier across a physical line and the per-line matcher pairs the wrong backticks. `awk`-based renumbering of a `sed` window mangles
  any line whose content starts with whitespace and digits (`substr($0, index($0,$2))` drops the indentation), which produced phantom line
  numbers in two reads of `coordination.go`; use `grep -n "" FILE | sed -n 'A,Bp'` instead.
- **WATCHOUT: `tests/tier10_conformance` exists, holds real test files, and DOES read `schemas/lenny-adapter.proto` as text.** It is
  harmless for SCHEMA-1, because its two readers assert only that the file contains the literals `spec/04_system-components.md` and
  `§28.5.3`, neither of which occurs in any of the nineteen carriers. It is not the external-adapter compliance suite §28.7 names, which
  is still absent from the tree, so the standing UNVERIFIED about comment-derived assertions stands on its own ground rather than on that
  suite being unbuilt — EVIDENCE: tests/tier10_conformance/adapter_contract_event_taxonomy_test.go:56-61.
- **WATCHOUT: `migrations/` holds 54 `*_test.go` files in `package migrations` that read migration SQL as text,** an older convention
  `scripts/lint-migrations.sh` pass 3 does NOT accept, because its `TEST_DIR` is `tests/tier2_component/migrations` alone. An implementor
  who follows the neighbouring `migrations/0178_checkpoint_manifest_test.go` pattern instead of §8's instruction turns tier 0 red.
- **WATCHOUT: the `docs/` surface count varies with the grep and every count describes the same conclusion.** Three lines by the counter's
  own name in both spellings, nine by a fence-and-barrier sweep that adds the glossary and metric rows, thirteen by the wider
  `coordinator|fenc|barrier` sweep. The three-line number is the one a grep reproduces; the extra lines are metric rows and unrelated
  delegation-coordinator text. Every line is unit-neutral about the fenced value, so the conclusion has never moved. Do not file a
  discrepancy between two shards that used different greps.

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
  carrying the fence rejection's status, detail string, and metric attribution, and run 8's spec round widened the same finding to the
  three `CheckpointBarrier` carriers under `:536-541`, which carry the barrier's only statement of `FailedPrecondition`. One preservation
  clause per paragraph fixes both. UNVERIFIED: whether this finding stands. Run 7's material skeptic REFUTED it, on the ground that a doc
  comment saying LESS is not a comment saying something WRONG, and an operational shard recorded that this `### Open` line would otherwise
  make a later lens rebuild a refuted finding; three run-8 shards then filed it again as live text with a named remedy. The two readings
  are parallel and neither corrects the other. Whoever settles it should record which of the two grounds failed, because the class will
  otherwise be re-derived every round. `[spec.1-3.*]`, `[spec.4.review-edit-sites.1]`, `[spec.4.review-operational.1]`,
  `[non-spec.6.review-operational.1]`, `[spec.1.review-edit-sites.1]`, `[spec.1.review-fresh.1]`, `[spec.1.review-mechanism.1]`
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
- **Status file, reduced to its surviving half.** Its scope bullet drops the hold clause. CORRECTED in pass 24: the closing-paragraph half
  is false. `status.md` is 33 lines and has no paragraph framing the hold's scope as open (`:28-29` is the staging caveat, `:31-33` the
  review history), `summary.md:453-454` already says so, and `summary.md:106-107` still asserts the false half in the "Watch out for"
  block an implementor reads. Deleting that clause is the whole fix. `[spec.1-3.*]`, `[non-spec.5.review-open-decisions.1]`
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
- **UNVERIFIED: §8's tier sentence omits tier 11 while checklist S1 declares it.** `[spec.1-3.*]`
- **UNVERIFIED: whether a tier-7a or tier-4 case breaks under a re-lookup implementation.** `[non-spec.5.fix-design-G1.1]`
- **UNVERIFIED: `coordinatorfence_test.go:15-16` spells the exemption per pod lifetime.** `[non-spec.5.review-applicability.1]` and
  `[non-spec.5.review-citations.1]`
- **UNVERIFIED: §8's tier sentence attributes "the registry state" to tier 2.** `[non-spec.5.review-applicability.1]`
- **UNVERIFIED: SPEC-1 calls the "Generation counters" bullet §10.1's while it lives in §10.1.1.** `[non-spec.5.review-citations.1]`
- **`coordinatorfence.go:37` is a fourth exemption site and the only production one.** In no §9 entry and no deliverable.
  `[non-spec.5.review-client-surface.1]`
- **The docs lens has returned nothing twice on a surface re-derived eight times.** The per-lens re-derivation is what costs rounds.
  `[non-spec.5.review-docs-alignment.1]`
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
- **UNVERIFIED: the rolling-window zero-row population.** Nobody has written down what happens to rows minted at 0 after the roll
  completes. `[spec.1.review-security.1]`
- **UNVERIFIED: whether the tree has a worked precedent for a two-release constraint tightening.** `pkg/schemamigrate` carries the phase
  machinery; nobody checked whether a landed migration uses it. `[non-spec.1.review-feasibility.1]`
- **Whether a later release adds the `>= 1` check as a genuine Phase-3 migration.** Defensible defence-in-depth, costs a separate
  proposal, and nothing in 0076 depends on it. `[non-spec.1.fix-design-G1.1]`
- **UNVERIFIED: whether the summary's SPEC-2 deliverable row should name the §29.8 Preconditions deletion.** It already omits the §29.8
  step-2 edit, so the row is loose by precedent. `[spec.1.fix-design-G1.1]`
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
- **UNVERIFIED: whether SPEC-1's §10.1.4 zero-invariant still has a ground for the rolling-window row class.** The run-6 post-fix block
  FILED that `spec-changes.md:283-286` keeps "That is held by the session row's baseline of 1 ... and by the adapter's refusal" while the
  same round's G2 edit deleted the only clause naming the gateway floor, and `:254` states that old-fleet inserts land at 0 that the
  create-path floors never see, so the retained `coordfence.go:147-153` floor is what actually holds the invariant for those rows. The
  standing `### Settled` bullet on that invariant says the post-fix round repaired the ground and that the staged sentence closes "and the
  value every other fence path sends is floored at 1". The two readings disagree and neither corrects the other; the older is noted in
  `## Retired`. A fixer opening `spec-changes.md:280-291` should read the live text before believing either. `[spec.1.postfix]`
- **OD2's recommendation has no owner, and it is FILED.** "Accept the equal case, and land it after the counter baseline"
  (`summary.md:197`, `:205`) is behind no deliverable, no checklist step, and no §8 test; nothing touches
  `pkg/adapter/coordination.go:99`, CODE-2 being scoped to `:236` alone. OD3 carries the exact sentence OD2 lacks ("a 'yes' is a
  successor's deliverable unless the reviewer directs that it be staged here"), and the asymmetry is the evidence.
  `[non-spec.3.review-open-decisions.1]`
- **OD5 states two costs of splitting and omits the three that matter, and it is FILED.** SPEC-3 exists only for the counter baseline,
  SPEC-1's §10.1 baseline paragraph goes with it, and SPEC-1's no-edit-site conclusion rests explicitly on "positive for every session
  once the baseline is 1", so a "split" answer reopens the two sentences OD10 asks about. OD5 and OD10 are coupled and neither says so.
  A rider nobody has settled: OD5's closing sentence says the first-crash-takeover-fence repair "is also fixed by OD2's remedy, so it is
  not exclusive to CODE-4", while under a "split" answer neither is in this proposal, so OD5 misprices the split by one benefit.
  `[non-spec.3.review-open-decisions.1]`, `[non-spec.6.review-open-decisions.1]`
- **OD7 asks the reviewer to decide a unit D6 already closes, and it is FILED.** OD7's recommendation ("state that the value's lifetime is
  the session's binding on the pod rather than the session itself") is verbatim D6's own closing clause, and D6 sits under "Fixed
  decisions. These are closed." D6's two halves are themselves in tension: sentence 1 says "unset until that session's first accepted fence
  on the pod" (per pod) and its closing clause says "the session's binding on the pod" (per binding), which differ under a rebind onto the
  same pod, and SPEC-1's staged text carries only the first form. `[non-spec.4.review-open-decisions.1]`
- **UNVERIFIED: which reading `summary.md:123`'s 0075 row means.** "Removes its sole counterexample" against "Removes the ground for its
  sole counterexample": 0075 defines the counterexample as a disagreement between the declared §4.1 scope and the derived one, and no
  deliverable here edits §4.1, so on that reading CODE-1 removes the ground and leaves the counterexample standing until OD3 is answered.
  `[non-spec.5.review-open-decisions.1]`
- **The summary's CH-ADAPTEREVENTS attribution names the wrong bullet.** `summary.md:150-154` calls it a `spec/28` "degradation row"; the
  sentence lives in the §28.5.2 **Exclusivity** bullet (`spec/28:471-478`) and sits in the "Holder of the exclusivity constraint changes"
  column, field 5 of the row at `:1810`. The §28.5.2 Degradation bullet (`:479-494`) carries a different "does not state" sentence, about
  retry and buffering policy. Weighed and not filed (reviewer prose, claim true, stages nothing); a fixer already in `summary.md` should
  change "degradation row" to "exclusivity bullet". `[non-spec.2.review-citations.1]`
- **The summary's `### Items §7 lists` section does not list §7's items.** §7 lists the barrier gate's equality (item 1), a fence for an
  unheld session (item 2), and `coord.mu` (item 3); the section dispositions items 2 and 3 plus a fourth thing §7 does not list (the
  fill-the-blanks headers) and omits item 1, which is OD1's subject. A fixer clearing §7 from that section will be one item short and one
  long. `[non-spec.2.review-open-decisions.1]`
- **UNVERIFIED: whether "Corrections outstanding" entries are supposed to reach §9.** `summary.md:445-447` records
  `adapterclient/coordinatorfence.go:37` as an outstanding correction AND records that §9 does not list the file. Refuted class (k) bars
  filing it as a missed edit site while the proposal's own text says it should be fixed; nobody has ruled on the shape.
  `[non-spec.6.review-client-surface.1]`
- **UNVERIFIED: `gateway-runtime-comms.md:2380` cites `migrations/0180_coordination_lease_address.up.sql`, which does not exist** (the
  tree's 0180 is `0180_drop_checkpoint_slot_id`). It is a root planning document, outside every rule's domain, and nothing in 0076 reads
  it, but it is also the generator source for the claim register, so whoever owns it should know. `[non-spec.5.review-edit-sites.1]`
- **UNVERIFIED: whether `tests/tier11_docs/checkpoint_pipeline_consistency_test.go`'s hit on the string `0181` is a coincidental
  substring.** It is outside `lint-migrations.sh`'s `TEST_DIR`, so it satisfies no gate either way; a later round adding a
  migration-number gate should check it. Nobody has looked. `[non-spec.6.review-applicability.1]`
- **UNVERIFIED: whether the tier-7a two-barrier case's "both return well inside the barrier ack deadline" holds** once the pod-level op
  lock serialises the two uploads. The mechanism works; the wall-clock claim rests on the fake runtime being fast and nobody has written
  the fixture. `[non-spec.5.review-mechanism.1]`
- **UNVERIFIED: nobody has sized the barrier-stage capture against the population D7 newly admits.** Pre-fix a large-workspace
  never-fenced session got the barrier-stage stream under the shared 90s deadline AND a dedicated post-barrier capture under its own
  tiered cap; post-fix it gets the shared window alone. Judged immaterial at Tier 3, where the sequential post-barrier loop reaches few
  sessions anyway, but sizing `checkpointBarrierAckTimeoutSeconds` against `max_tiered_checkpoint_cap` for the whole coordinated set
  rather than for one slot is a human's call. Start from `spec/10:132` and `:137`: the agent-pod floor the CRD webhook enforces already
  carries the co-tenancy factor, and no CRD field carries the gateway grace period, so no webhook vets that budget.
  `[non-spec.5.review-performance.1]`, `[spec.1.review-kubernetes.1]`
- **UNVERIFIED: whether a Goals bullet may state a specified-but-unenforced effect.** "The drain checkpoint captures a stopped workspace"
  (`summary.md:130-131`, `:36-37`) is true in specification terms and false of the tree, because `quiesced` is advisory with no production
  reader under F-10.1.6. The "captured twice" half IS delivered, and the same gap makes the PRE-fix harm equally unrealised, so nothing
  regresses either way. `[non-spec.6.review-performance.1]`
- **OPEN: §8 pins nothing on the interaction between CODE-3's per-session generation read and a `CoordinatorFence` in flight for the same
  entry.** The hold's rejection allowlist admits `CoordinatorFence` (`holdstate.go:53-59`), so the two are genuinely concurrent, and
  `.claude/rules/test-coverage.md` requires a changed behaviour's concurrent path to be pinned. Not filed: the remedy is the one clause in
  CODE-3 that the `### Deferred` entry already carries. `[non-spec.6.review-performance.1]`

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
  only the started-session count is read through an accessor before `hold.mu`, and after CODE-1 removes `Server.coord` outright both
  halves of the clause go false. WIDENED in pass 24 from two sites to FOUR, each the same class and each outside CODE-3's enumeration, so
  hand all four to the implementor in one edit rather than two: (1) `holdstate.go:116-118` as above; (2)
  `pkg/adapter/coordination.go:126-128` ("the hold timeout never reaches back into coord.mu"), which is the shipped statement of the lock
  order the summary's fixed decisions restate; (3) `holdstate.go:103`, whose doc comment reads "gauge, logs coordinator_connection_lost
  with the last known generation", false once CODE-3 drops the key from the pod-level line at `:130-132`; and (4) `exitHoldState`'s
  comment at `holdstate.go:153-155`, "the generation it armed under is already on the coordinator_connection_lost line that opened this
  hold", false for the same reason. CODE-3's "Those sites are the whole of what the field drags" (`non-spec-changes.md:105-106`) is false
  against all four. Confirmed still open by the index-and-checklist reconciliation of 2026-09-03, which records that the remedy is a
  staged edit in the non-spec changes naming the comments and that no lane has authored it.
- DEFERRED [`cmd/lenny-gateway/httpsurface.go`, about `:588-602`, from `[non-spec.1.review-reliability.1]` and independently from
  `[spec.4.review-performance.1]`]: the barrier's cache-fallback closure calls `w.sessions.Get(context.Background(), ...)` once per binding
  with no deadline, on the exact path taken when the `coordination_lease` mirror read has already failed. A hung Postgres therefore hangs
  `Targets()`, which hangs `Dispatch`, which burns the whole preStop grace with no drain at all; the `barrierAckTimeout` on `fireBarrier`
  does not bound it, because the fallback ignores the passed context. This is pre-existing shipped code that 0076 cites under OD1 but does
  not touch, so it is not a finding against this proposal and the spec loop has no write path to it either way. What is true instead: the
  fallback needs the caller's context or its own bounded timeout. Route it to a gateway-side fix rather than to 0076 — EVIDENCE:
  cmd/lenny-gateway/httpsurface.go:593, :594-596; pkg/gateway/coordination/barrier/wiring.go:97-122;
  pkg/gateway/podlifecycle/prestop/prestop.go:501-506.
- DEFERRED [`non-spec-changes.md` CODE-3, `:107-109`, from `[non-spec.2.review-reliability.1]`]: the staged sentence says
  `terminateHeldSession` and `writeHoldPostMortem` "read each terminated session's own last fenced generation off the `*slotState` the
  `heldSession` already carries" and states no synchronisation, while the value it names lives inside `coordinationState`, which carries
  its own `mu` and whose every other reader locks it (`coordination.go:44-47`). What is true instead: that read must take the entry's
  `coordinationState.mu`. The window is verified rather than hypothetical: `onHoldTimeout` clears `hold.active` under `hold.mu`, releases
  it, then pass 1 takes `s.mu` and deregisters, so a `CoordinatorFence` that passed `checkSessionBound` before pass 1 is still in flight
  and, under CODE-1, holds the resolved `*slotState` and writes `lastFenced` on it while pass 2 reads that same field for the post-mortem;
  `CoordinatorFence` is on the hold's allowlist (`holdstate.go:53-59`) and is the only exit from the hold, and CODE-1 makes the accessor a
  registry lookup that pass 1 has already made fail, so the post-mortem CANNOT use the accessor and must read the detached pointer
  directly. Not filed by any of the four lenses that derived it, because the staging neither stages nor forbids the locked read and the
  natural Go implementation locks; the cost of getting it wrong is a garbled integer in a best-effort log line and a 0600 JSON record.
  One clause in CODE-3 closes it. This supersedes the standing `### Open` UNVERIFIED of the same name, which pass 24 deleted: the window
  is no longer in question, only the remedy is unapplied — EVIDENCE: pkg/adapter/holdstate.go:178-208; pkg/adapter/slotsession.go:267-274,
  :347-368.
- DEFERRED [`spec-changes.md` SPEC-1, `:259-264`, from `[non-spec.3.fix-G1.1]`, `[non-spec.3.fix-design-G1.1]`, and independently from
  `[non-spec.5.review-edit-sites.1]`]: the rationale closes "Each names the row value the dispatcher copies onto the wire
  (`pkg/gateway/coordination/barrier/wiring.go:49`), which is positive for every session once the baseline is 1, so each stays true and
  neither is restated." The clause "so each stays true" is FALSE. The value the dispatcher copies is the mirror value, and
  `coordination/coordination.go:430` passes the pre-bump `row.CoordinationGeneration` from the sweep's List snapshot while `RecordHandoff`
  (`:463-482`) bumps the stored row into a local earlier in the same iteration, so the mirror carries G for a whole sweep interval while
  the pod is fenced at G+1 and both sentences are already false on the handoff path whatever the baseline is. What is true instead: the
  conclusion (neither is an edit site, neither is restated) stands, on the ground that both are already false in the shipped tree through
  a defect this proposal records but does not stage. The summary now says this and SPEC-1 does not, so the two documents give opposite
  grounds for the same ruling; OD10's withdrawal states outright that SPEC-1 "reaches the right conclusion by a route that does not hold".
  OD8 is the precedent that a withdrawal finding the staged ground false also corrects the deliverable's stated reason. The non-spec loop
  has no write path to `spec-changes.md`, so three rounds derived it and none could land it. Do NOT "fix" it by giving SPEC-2 a `spec/10`
  edit site, and do not sweep in the `CH-BARRIER` Messages bullet at `spec-changes.md:420-423`, whose own stated ground is that it does
  not call the value the current one.
- DEFERRED [`spec-changes.md:604-616`, from `[non-spec.5.review-open-decisions.1]` and `[non-spec.6.review-open-decisions.1]`]: §7 "Open
  decisions for review" still lists three items while `summary.md:390-409` dispositions item 3 (`coord.mu`) as "Not a reviewer decision
  ... Delete the item" and item 2 (a fence for an unheld session) as "out of scope for this proposal, with a named consequence and an
  owner". What is false: §7's presentation of items 2 and 3 as live reviewer decisions. What is true instead: item 3 is an implementation
  choice with no external consequence, and item 2 is out of scope with its consequence recorded under the summary's shipped-tree defects.
  No owner is named anywhere in the proposal, which is the half a reviewer cannot supply. §4's detailed-design bullets duplicate items 2
  and 3 verbatim, so the fix touches both sites. A spec-lane fixer with a write path closes it in one edit; the non-spec loop has none.
- DEFERRED [`spec-changes.md` §7, from `[non-spec.6.review-open-decisions.1]`]: OD5 is the open decision with the largest effect on the
  staged spec text and §7 does not list it. A "split" answer deletes SPEC-3 entirely (`spec-changes.md:571-585`) and SPEC-1's §10.1
  baseline paragraph (`:230-238`), on which the no-edit-site conclusion at `:259-264` explicitly rests. What is true instead: a reviewer
  reading only the staged spec file sees no sign that a whole deliverable is conditional. The remedy is one bullet in `spec-changes.md`
  §7, verified against all three cited ranges, and it is the one item of this class that IS landable in a spec-scoped round.

## Ledger

### [spec.2.fix-G1.1]

DECISION: Swapped SPEC-1's ground for excluding the two "carries the current `coordination_generation`" sentences from the false "the row value is positive once the baseline is 1" account to the mirror-lag account, and dropped "under the baseline" from the paragraph's opening sentence so the ruling no longer reads as conditional on the baseline — BECAUSE both halves of the retired ground are false against the code it cites: the dispatcher copies `Target.CoordinationGeneration`, which `MirrorTargetLister.Targets` fills from the `coordination_lease` mirror row (`pkg/gateway/coordination/barrier/wiring.go:104-114`, `:49`), not from the session row, and positivity of a row value does not establish currency in any case. The correct ground was already written twice in `summary.md` (the "What changes" paragraph and the barrier-target-mirror-lag bullet under the shipped-tree defects), so the proposal was giving two opposite grounds for one ruling and a spec applier read the false one — ALTERNATIVES: deleting the ground and asserting the conclusion bare (leaves an applier with no test for whether §10.1.8 step 1 and §29.7 step 4 need editing, which is the same reader failure); making either sentence an edit site or giving SPEC-2 a `spec/10` edit site (inverts the defect and puts `spec/10_gateway-internals.md` under two deliverables); staging a repair for the mirror lag (out of scope, and it would remove the ground the corrected paragraph rests on).

DECISION: Cited `RecordHandoff` as `coordination.go:463-481` rather than the finding's and the design's `:463-482` — BECAUSE the function's closing brace is at `:481` and `:482` is the blank line after it — ALTERNATIVES: carrying `:463-482` forward for fidelity to the design, which would have staged a citation one line long.

DECISION: In `summary.md` OD10 I wrote ":354" as "on the ground SPEC-1 now states" without repeating the `spec-changes.md` line range the design's edit list appended — BECAUSE the same range is already cited two sentences earlier at ":349" in the same paragraph, and a duplicated line range is a second site that drifts every time the SPEC-1 paragraph grows. The design's substance (OD10 now describes the ground SPEC-1 states rather than a ground it does not) is applied in full.

FACT: the SPEC-1 paragraph grew from six lines to thirteen, so it now spans `spec-changes.md:259-271`, and every line after it shifted by seven. The only citations of `spec-changes.md` line ranges anywhere in the proposal outside the review logs are three, all in OD10: `:259-264` (now `:259-271`), `:428-430` (now `:435-437`, the `spec/29_communication-scenarios.md` files-touched bullet), and `:206-222` (unchanged, it precedes the edit). All three were corrected or re-verified this pass — EVIDENCE: summary.md:349, :350, :352

FACT: the barrier's wire value has three provenances and only one of them is the session row. Healthy path: the `coordination_lease` mirror row (`wiring.go:104-114`). Cache fallback: the live session row (`cmd/lenny-gateway/httpsurface.go:588-602`). The mirror is written by the sweep from its pre-bump `List` snapshot (`coordination.go:430`, snapshot from `:271`/`:275`) in the same loop iteration in which `RecordHandoff` bumps the stored row and returns the post-increment value the pod is then fenced at (`:371`, `:399`, `:463-481`), so after a cross-replica takeover the mirror carries G for a whole sweep interval while the pod holds G+1. Do not write text attributing the barrier's wire value to the session row — EVIDENCE: pkg/gateway/coordination/barrier/wiring.go:49, :104-114; pkg/gateway/coordination/coordination/coordination.go:271, :371, :399, :430, :463-481

CORRECTS [standing-context trap "three sentences read as citation defects and are not"]: item (1) of that trap says SPEC-1's "Each names the row value the dispatcher copies onto the wire (`wiring.go:49`)" survives because "the mirror is seeded from the row", and routes the currency question to a standing OPEN. That reading is wrong on both counts and this round's finding was confirmed by two independent verifiers. The mirror is seeded from the *pre-bump snapshot* of the row, not from the row, so it is not merely a currency question but a value the row does not hold at that moment; and the sentence attributed the wire value to the session row, which the code contradicts on the healthy path. The clause is gone; items (2) and (3) of that trap stand.

WATCHOUT: `spec-changes.md:240-241` ("there is no second value to keep in agreement with the row") looks like a fourth site of the same retired account and is not. Read in its own paragraph (`:236-247`) it is a statement about the rejected fixed-positive-wire-constant drafting alternative rather than about the runtime mirror path. Two prior passes raised and declined it; a standing `### Settled` bullet independently calls it falsified by the mirror lag, which is the disagreement to be aware of before touching it. Do not edit it under this finding's mandate — EVIDENCE: spec-changes.md:236-247

WATCHOUT: the §28.5.1 `CH-BARRIER` Messages bullet disposition (now `spec-changes.md:427-429`) is also not a site of this account, and this round's design ruled it out explicitly. Its operative ground is that the bullet does not call the value the current one, which the mirror lag does not falsify. Its trailing clause ("the baseline SPEC-1 and SPEC-3 state leaves it true") does still lean on the baseline, so a later round may want to look at that clause on its own terms; it was not touched here — EVIDENCE: spec-changes.md:427-429

UNVERIFIED: whether the barrier-target mirror lag is worth its own proposal. It is recorded in `summary.md` under the shipped-tree defects this proposal does not stage, and two sentences of `spec/` (`spec/10_gateway-internals.md:183`, `spec/29_communication-scenarios.md:1186`) are false because of it. Nobody owns the repair. A later spec or code proposal should either fix `upsertMirror` to take the post-bump generation or edit those two sentences.


### [spec.2.fix-design-G1.1]

DECISION: Land the long-standing DEFERRED correction at `spec-changes.md:259-264` by swapping SPEC-1's ground from "the row value the dispatcher copies onto the wire ... is positive once the baseline is 1, so each stays true" to the mirror-lag ground the summary already carries, AND by dropping "under the baseline" from the paragraph's opening sentence — BECAUSE both halves of the staged ground are false against the cited code (the dispatcher copies the `coordination_lease` mirror value, not the session row's; the mirror is written from the sweep's pre-bump List snapshot), and because leaving "under the baseline" in the opening sentence keeps the ruling hanging on the baseline, which contradicts OD10's explicit "this disposition does not rest on the baseline" — ALTERNATIVES: delete the ground and assert the conclusion bare (rejected: a spec applier is left with no test, and OD10 cascades anyway); make either sentence an edit site or give SPEC-2 a `spec/10` edit site (rejected: inverts the defect and puts one spec file under two deliverables); stage a repair for the mirror lag (rejected: it is a recorded shipped-tree defect this proposal does not stage, with its own code blast radius).

DECISION: `summary.md:354` and `:359-361` (OD10's withdrawal) are IN SCOPE and change in the same edit, following OD8's form at `summary.md:320-336` — BECAUSE OD10 currently says "Neither sentence is an edit site, on a ground SPEC-1 does not state" and "SPEC-1's staged ground ... reaches the right conclusion by a route that does not hold", and after the fix SPEC-1 states exactly that ground. OD8 sets the precedent: the withdrawal narrates the false ground in the past tense and records that the deliverable "now states the reason" — ALTERNATIVES: leave OD10 alone (rejected: it would assert the proposal does not state a ground it states); delete OD10 (rejected by the `## Open decisions` preamble at `summary.md:166-172`, which fixes restate-in-place as the only exit).

FACT: `proposals/` is excluded from the line-citation ratchet's read domain (`readExcludedPrefix = "proposals/"`), so the spec line citations inside this proposal are the existing convention and N8 does not force them to headings in this edit — EVIDENCE: scripts/specshift/scope/scope.go:99

FACT: the healthy barrier path reads the mirror, not the session row. `MirrorTargetLister.Targets` fills `Target.CoordinationGeneration` from `Mirror.ListHeldByReplica`, which selects `coordination_generation` from `coordination_lease`; `PodDispatcher.Send` puts that field on the wire. Only the cache fallback (`cmd/lenny-gateway/httpsurface.go:588-602`) reads the session row — EVIDENCE: pkg/gateway/coordination/barrier/wiring.go:104-115, :49; pkg/gateway/coordination/coordlease/pgstore/pgstore.go:76-94

WATCHOUT: three sites look like parallels of this clause and are NOT. (1) `spec-changes.md:240-241` "there is no second value to keep in agreement with the row" is about the rejected fixed-wire-constant spec-drafting alternative, and two prior passes declined it. (2) `spec-changes.md:420-423`, the §28.5.1 `CH-BARRIER` Messages bullet, rests on a different ground (the bullet does not call the value the current one) that the mirror lag does not reach. (3) `summary.md:21-24` was already corrected to the mirror-lag ground and needs nothing — EVIDENCE: spec-changes.md:229-247, :420-423; summary.md:21-24

USEFUL [review-log.md 2733, 2750, 2758, 2760, 2762]: the whole correction, its true-instead text, the OD8 precedent, and both do-not-sweep warnings were already derived by the non-spec rounds and parked as DEFERRED for this loop. Re-verifying the sweep arithmetic and the wiring cost one grep each; deriving them would have cost a pass.

OPEN: after this edit, SPEC-1 itself states that the ruling is independent of the baseline, which strengthens the OD5 "cost of splitting" gap already recorded at review-log.md:2764. A later round editing OD5 should decide whether its list gains that clause. This edit does not touch OD5.


### [spec.2.review-applicability.1]

DECISION: Returned EMPTY. — BECAUSE `diff -rq` against `scratchpad/cp-snap/0076-run8/spec-r2` shows only the review log
moved, so the spec staging is byte-identical to run 8 round 1, where this same lens filed four findings and the material
skeptic refuted every one (§28.8 `CH-BARRIER` preservation clause, the `CoordinatorFenceRequest` tail deletion, the
`coordinator_lost` log line, and the `## 5. Proposed changes` fill-the-blanks header). Re-filing any of them is barred and
nothing else in the staging reaches the bar. — ALTERNATIVES: three candidates worked up and dropped, listed below.

FACT: the whole applicability surface of this staging is small and I re-resolved it in one pass. SPEC-1/2/3 create no
file, no heading, no anchor, and no identifier, so class 2's heading-and-anchor machinery has nothing to bite on: every
staged edit is a sentence or clause substitution inside an existing paragraph. That is why five applicability-shaped
classes (forward reference, relocation, anchor ambiguity, gate state, code-against-unlanded-spec) collapse to almost
nothing here, and it is the reason to budget the next applicability run at a fraction of a full pass.
EVIDENCE: spec-changes.md:137-292 (SPEC-1), :294-569 (SPEC-2), :571-585 (SPEC-3)

FACT: anchors re-resolved this round, all verbatim and unique, so a sixth derivation is never owed:
`spec/28:315-316` (CH-FENCE Messages), `:330-331` (Exclusivity window clause), `:333-336` (Degradation first sentence,
hold from `:336`), `:1675` (§28.6 One-holder CH-FENCE clause), `:1679-1681` (second-opener first sentence),
`:1683-1685` (fence-acknowledgement sentence), `:1806`/`:1807`/`:1808` (the three §28.8 cells, one physical line each);
`spec/29:1150-1152` (§29.7 framing), `:1259-1261` (§29.8 Preconditions clause), `:1269-1275` (step 2's
`coordinator_connection_lost` clause), `:1461-1470` (Partitioned per slot), `:1472-1517` (Shared by the whole pod),
`:1519-1543` (does-not-state list, four bullets, first is the one SPEC-2 removes); `spec/10:30`, `:38`, `:40`, `:41`,
`:57-60`, `:183`, `:184-198`; `spec/04:200`.

FACT: no existing gate hard-fails at any intermediate step. Greps over `tests/ scripts/ docs/ charts/ pkg/ cmd/` for the
seven most distinctive strings SPEC-1 and SPEC-2 delete ("rejected on the stamp", "prior coordinator's RPCs are still
accepted", "matches the fenced value", "no longer holds the current generation", "last known generation", "is the
generation the pod last fenced", "hold state is partitioned per slot") return exactly one hit, a Go doc comment at
`pkg/adapter/holdstate.go:103` that refuted class (k) bars and that the `### Deferred` block already owns. This is the
cheap form of the class-4 check; one command prices it.

WATCHOUT: SPEC-1's design paragraph claims "That text lands in sentences §10.1.2 already carries" (spec-changes.md:151),
but one clause of that text — "so a handoff for one session neither fences nor unfences another" (`:141-142`) — lands in
none of the six sentences SPEC-1 then enumerates. It lands only in SPEC-2's mirrors (§28.5.1 Messages' new second
sentence at `:314`, §29.10's Partitioned-per-slot addition at `:436-439`), which makes SPEC-2's own closing claim that
"no mirror states more or less than the section it mirrors" (`:496-498`) loose in the same direction. Worked up and
DROPPED: both sentences are proposal rationale landing nowhere under `spec/`, the applied text is a true consequence of
the per-session unit rather than a contradiction, and the shape is the already-weighed "SPEC-1 gloss versus SPEC-2's
actual bullets" class. Do not spend a verification on it without a new argument.

WATCHOUT: SPEC-1 at `:271-273` says SCHEMA-1 carries onto the barrier-side wire comments "the acceptance sentence ...
together with the unset-value clause", while SPEC-2's actual prescription for those three carriers (`:536-541`) carries
no unset clause and `:556-557` states the unset case is deliberately not restated on wire comments. Worked up and
DROPPED: same class as the two SCHEMA-1 findings this run's material skeptic already refuted (operative instruction is
SPEC-2's paragraph, the applied `spec/` is untouched, and no test asserts a proto comment). Recorded so the next
applicability run does not rebuild it.

USEFUL [standing `### Traps`, "count the sentences in a §28.8 cell"]: the `awk -F'|' 'NR>=1803 && NR<=1809 {print NR"
||| "$2" ||| "$5}'` recipe printed all three staged cells in one call and let me confirm the `CH-CHECKPOINT` bullet names
its surviving clause explicitly while `CH-BARRIER` names a whole sentence. Without it the cells read as absent.

USEFUL [standing `### Settled`, "Three cheap mechanical checks price a whole pass"]: `diff -rq` against the round's
snapshot cost one call and settled the whole round's scope before I opened a spec file.



### [spec.2.review-citations.2]

DECISION: filed exactly one finding, the `spec-changes.md:259-264` mirror-provenance ground — BECAUSE `diff -rq` against the `spec-r2` snapshot shows only the review log moved this round, so "read the newest text hardest" again had no spec-lane target, and the one live defect in old text with a spec-lane remedy is the one three earlier non-spec rounds derived and none could land. ALTERNATIVES: rejected filing §4's "either order" bullet (`spec-changes.md:118-121`), because `review-log-archive.md:3093-3095` records that exact filing as "refuted on disposition rather than on substance" — the claim IS false but is deliberately routed to human review under OD1; rejected the §28.8 `CH-FENCE` cell's "window sentence" gloss swallowing the cell's hold clause (`spec/28:1807` is one sentence, "The acknowledgement closes the window ... and it is the one channel the adapter still accepts in hold state"), because it is the same descriptive-gloss class the material skeptic refuted on the `CH-BARRIER` bullet this run; rejected the §28.6 "closing sentence" mislabel of "The constraint excludes a second replica" (`spec/28:1686`, followed by two more sentences) as position-label wording; rejected `spec-changes.md:249` / `:284` crediting the baseline to the column default, because the standing `### Deferred` entry itself says "the sites are not false".

FACT (full citation sweep, every anchor in the spec-changes file re-resolved this round, all clean — do not re-derive): `spec/10:30`, `:37`, `:38`, `:41`, `:58`, `:60`, `:183`, `:184`, `:198`; `spec/28:237-240`, `:251-253`, `:291-296`, `:330-331`, `:349-353`, `:361-365`, `:1675`, `:1679-1681`, `:1683-1685`, `:1805`, `:1806`, `:1807`, `:1808`; `spec/29:1150-1152`, `:1160-1162`, `:1186`, `:1259-1261`, `:1274`, `:1307-1313`, `:1322-1326`, §29.10 bullets at `:1464-1470`, `:1523-1527`, `:1528-1535`; `spec/04:200`; `schemas/lenny-adapter.proto:153-162`, `:161-162`, `:165-179`, `:1442-1446`, `:1449-1451`, `:1455-1462`, `:1469-1474`, `:1475-1483`, `:1477-1479` and all twelve operational field-comment ranges (each range is exactly the comment block above its `int64 coordination_generation` line, and each enclosing message name matches the one SPEC-2 lists); `pkg/adapter/adapterevents.go:80-96`, `holdstate.go:39-44`, `:90-100`, `:107-112`, `:172-176`, `:192`, `coordination.go:29-32`, `:89`, `:92`, `:93-94`, `:99`, `:108-121`, `:112-113`, `:216`, `:223`, `:224-226`, `:228-231`, `:236-239`, `slotsession.go:267`; `barrier.go:229-246`, `wiring.go:49`, `:51-53`, `:104-114`, `coordfence.go:147-153`, `coordination/coordination.go:399`, `:430`, `prestop.go:390-397`, `:395`, `:510`, `start.go:3975`, `:4067`, `:4233-4245`, `:4237`, `coordination_seams.go:155-160`, `:233`, `httpsurface.go:592-599`, `migrations/0050_session_record_fields.up.sql:38-39`, `charts/lenny/templates/migrate-job.yaml:10-16`.

FACT: `GetCoordinationGeneration()` has exactly two non-test call sites in the whole adapter (`pkg/adapter/coordination.go:92`, `:223`), confirmed by `grep -rn 'GetCoordinationGeneration()' pkg/adapter/ | grep -v _test`, which is one call and settles D7's "the barrier is the only gateway-to-pod RPC the adapter validates on the generation gate".

FACT: `checkSessionBound` has seven non-test call sites (`attach.go:41`, `lifecycle.go:30`, `:80`, `coordination.go:89`, `:216`, `session.go:186`, `usage.go:266`), so D6's "The pod admits no session-scoped RPC for a session before it is bound" is a true general claim about the tree rather than a two-handler claim. Do not file it as over-broad.

WATCHOUT: the §29.7 / §29.8 boundary is `spec/29:1244`. Every anchor the SPEC-2 `spec/29` bullets cite lands on the correct side (`:1150-1152` and `:1186` are §29.7; `:1259-1261`, `:1274`, `:1307`, `:1322-1326` are §29.8), so a section-attribution finding there is dead — EVIDENCE: spec/29_communication-scenarios.md:1142, :1244.

USEFUL [standing `### Deferred`, `spec-changes.md` SPEC-1 `:259-264`, from `[non-spec.3.fix-G1.1]`]: it named the one defect in this file with a spec-lane remedy and saved the whole re-derivation. Its instruction "Do NOT fix it by giving SPEC-2 a `spec/10` edit site, and do not sweep in the `CH-BARRIER` Messages bullet at `spec-changes.md:420-423`" is carried into the filed finding's suggested fix verbatim.

USEFUL [standing `### Traps`, "§4's 'either order' claim is false and routes to human review"]: read on its own it reads as an unfiled live defect. `review-log-archive.md:3093-3095` is the entry that stops the filing, and the two are 3,000 lines apart. Read them together before spending a verification.


### [spec.2.review-client-surface.1]

DECISION: Returned empty. — BECAUSE `diff -rq scratchpad/cp-snap/0076-run8/spec-r2 proposals/0076_.../` reports ONLY the review log differing, so no staged byte moved between run-8 spec rounds 1 and 2, and my own lens's round-1 answer over the identical hash (646a03fca298) is cached and empty. I re-derived the load-bearing claims from the tree anyway rather than returning the cache blind, and every one held. — ALTERNATIVES: filing the SCHEMA-1 unset-clause tension between spec-changes.md:271-273 ("SCHEMA-1 carries the acceptance sentence ... together with the unset-value clause") and :536-541 (which names only "the §10.1.2 step 3 wording") — rejected, because step 3's staged wording contains the unset clause, so the two instructions resolve to the same text, and two SCHEMA-1 instruction-granularity findings were already refuted this run as editorial precision.

FACT: The whole client-facing half of this proposal is empty and it is cheap to confirm in one call. `grep -rn 'coordination_generation|CoordinationGeneration|coordinator_handoff_stale|last_fenced' sdks/` → nothing across all six SDK trees; `grep -c coordination pkg/gateway/externalapi/openapi/openapi.json` → 0; no `schemas/*.json`, no `charts/`, no `docs/api/`, no `docs/client-guide/`, no `docs/runtime-author-guide/` hit. The reason is one sentence of shipped spec. — EVIDENCE: spec/04_system-components.md:200 ("`coordination_generation` is incremented on coordinator handoff across gateway replicas (internal only, used for split-brain fencing)" against `recovery_generation` "visible to clients via the session API and `session.resumed` events").

FACT: The §4.7 adapter-contract surface a runtime author reads is unit-neutral in both carriers and is correctly outside every edit list. `spec/04:711` states the barrier carries `coordination_generation` and `barrier_id` and states no gate; `spec/04:712` states the fence is a precondition for subsequent operational RPCs and says "Gap detection handled on the adapter side"; `docs/reference/adapter-contract.md:68`, `:69`, `:96` mirror both at the same neutrality. D7 (barrier accepted when the pod holds no value) and the per-session rescope falsify none of them. — EVIDENCE: spec/04_system-components.md:711, :712, :746; docs/reference/adapter-contract.md:68, :69, :96.

FACT: `CheckpointBarrierResponse`'s message comment states no gate, no rejection, and no generation, which is why D7 needs no edit there even though it changes when an ack is produced. It documents only `barrier_id`, `checkpoint_ref`, and `quiesced_ms` and the "only after that gateway-driven stream terminates" ordering. — EVIDENCE: schemas/lenny-adapter.proto:1493-1502.

WATCHOUT: The runtime SDKs' "quiesce" vocabulary is a different mechanism from the barrier's quiescence and a lens that greps for the word works up a false SDK finding. The SDK and `docs/runtime-author-guide` hits are the §15.4.3 `checkpoint_request` → `checkpoint_ready` → `checkpoint_complete` handshake on CH-RUNTIMEOPS, which carries no session id and no generation and is driven by the `Checkpoint` stream rather than by `CheckpointBarrier`'s gate. — EVIDENCE: docs/runtime-author-guide/integration-levels.md:208; sdks/runtime/go/runtime/lifecycle.go:192; sdks/runtime/python/lenny_runtime/lifecycle.py:193; sdks/runtime/typescript/src/lifecycle.ts:177.

USEFUL [Standing context, "The proto is a published artifact and the client surface around it is empty"]: its pass-23 correction that the OpenAPI document is at `pkg/gateway/externalapi/openapi/openapi.json` and that `pkg/gateway/openapi/` does not exist saved a wasted grep that returns clean for the wrong reason. The lens brief itself names the non-existent path, so every future client-surface shard will hit this.


### [spec.2.review-docs-alignment.1]

DECISION: returned an EMPTY findings list over the staged spec edits under the docs-alignment lens — BECAUSE
`diff -ru scratchpad/cp-snap/0076-run8/spec-r2 proposals/0076_.../` reports exactly ONE changed file, the
review log itself, so `spec-changes.md`, `non-spec-changes.md`, `summary.md`, and the checklist are
byte-identical to the text run 8 round 1 already swept; and because every docs-lens candidate I derived
independently resolved either to pre-existing drift, to a surface the change does not reach, or to a remedy
that lands in `docs/` and is therefore out of this loop's scope — ALTERNATIVES worked up and dropped below.

FACT: the docs-lens scope collapse is the single most useful thing to know before spending a budget here.
This loop's scope line ("report only findings whose fix lands in the staged spec edits") and the lens's own
guardrail 1 ("a finding here is always a missing or wrong docs edit, never a spec or code edit") intersect in
almost nothing. The ONLY docs-lens category that survives into scope is the accepted/deferred-failure-mode
one, whose remedy the lens explicitly allows to be "a staged spec and/or doc edit". Every "docs/ page X is
now wrong" finding is out of scope by construction in the spec loop. A future docs-lens agent on a SPEC
round should start from the accepted/deferred-outcome list and skip the mirroring sweep entirely.

FACT: the change adds, removes, and renames NO metric, alert, error code, endpoint, flag, or CloudEvent, so
the whole companion-row half of the lens is vacuous. Independently re-derived rather than taken from the
log: `grep -rn "coordinator_connection_lost|coordinator_lost|coordinator_generation_gap|last_fenced|
coordinator_hold|coordinator_handoff_stale" docs/ charts/` returns TWO lines, both unit-neutral metric-table
rows (`docs/reference/metrics.md:307`, `:309`), and neither states the fenced value's unit, baseline, or
gate — EVIDENCE: docs/reference/metrics.md:307, :309.

FACT: `coordinator_connection_lost` has exactly TWO spec carriers and SPEC-1 and SPEC-2 stage both. The
event occurs at `spec/10_gateway-internals.md:60` (the §10.1.4 Observability bullet, staged by SPEC-1) and
`spec/29_communication-scenarios.md:1274` (§29.8 step 2, staged by SPEC-2). There is no third carrier in
`spec/`, `docs/`, `schemas/`, or `charts/`, so the field change (drop the generation, add the started-session
count) strands no mirror — EVIDENCE: spec/10_gateway-internals.md:60; spec/29_communication-scenarios.md:1274.

FACT: no structured-log-event inventory exists anywhere in `docs/`. `docs/operator-guide/observability.md`
carries only the general correlation-field paragraph, and there is no per-event field table for any component,
so an adapter log line's field set has no docs companion to update — EVIDENCE:
docs/operator-guide/observability.md:316; spec/16_observability.md:379 (the only field requirement, and it
predates this change: the pod-level event names no session today either, at spec/10_gateway-internals.md:60).

FACT: `spec/12` carries NO DDL for the `sessions` table's counter columns, so SPEC-3's baseline of 1 has no
storage-architecture mirror to falsify. The complete `coordination_generation` surface outside
`spec/10`, `spec/28`, and `spec/29` is `spec/04:200`, `:323`, `:461`, `:711`, `:712`, plus unit-neutral lines
in `spec/07`, `spec/16`, `spec/18`, and one at-risk-writes row in `spec/12:160`. Only `spec/04:200` is a
site, and SPEC-3 stages it — EVIDENCE: spec/04_system-components.md:200 ("Both counters are **monotonically
non-decreasing** ... never rolled back, never reset"), which the baseline of 1 does not disturb.

FACT: §28.4 in `spec/` carries NO claim-register ROWS. It is four paragraphs defining the register's status
set and delegating the rows to `tests/claim-map.json`, so "No §28.4 claim-register row moves" cannot be
falsified by a missing `spec/28` edit site — EVIDENCE: spec/28_communication-channels.md:161-175.

WATCHOUT: the strongest-looking docs-lens candidate on this text is a trap the orchestrator's own refuted
list already closes. SPEC-1's staged §10.1.2 step 3 ends "and a `CheckpointBarrier` naming such a session is
accepted and records no value", with NO "on generation grounds" qualifier, while the proposal explicitly
retains the adapter's non-positive `InvalidArgument` guard that refuses such a barrier before the gate
whenever the carried value is 0, which the cache fallback puts on the wire under a store outage
(spec-changes.md:255-256 "The adapter's refusal of a non-positive generation ... on the barrier path
(`:224-226`) is unchanged"; cmd/lenny-gateway/httpsurface.go:592-599). I worked this up as a
category-one accepted-failure-mode finding and dropped it: the refuted entry "The staged unset arm licenses
accepting a barrier for a session the pod is not running" was refuted on precisely this reasoning — "Both
the step-3 sentence and its §28.6 twin state the consequence as ... 'on generation grounds'. A reader takes
that as the generation predicate's behavior, not as a waiver of every other admission guard." The barrier
clause sits inside that same sentence and inherits the same frame. Do not re-file it.

WATCHOUT: the proposal has NO `## Edge cases and accepted failure modes` section at all, so the lens's
"cross-check every row against the staged edits" instruction has no table to read. Its absence is not
filable — it makes neither the applied spec nor the implementation wrong, and eight runs have not filed it.
The accepted/deferred outcomes are scattered across §2 (D5, D6, D7), §6 Non-goals, and the summary's
"Defects in the shipped tree that this proposal does not stage". I checked each against staged spec text and
every one lands: D5's co-tenant-freeze residual in SPEC-2's new §29.10 "Shared by the whole pod" hold bullet
plus the existing §10.1.4 whole-pod paragraph; the unimplemented gap reset in the §28.4 register's R16
`ABSENT` row; the absent CH-ADAPTEREVENTS client in R12 `UNWIRED`.

USEFUL [`### Settled` "Derived inventories. Do not re-derive any of these."]: its thirteen-line `docs/`
surface list is correct and complete, and re-verifying all thirteen lines cost one `sed` call instead of a
whole sweep. I confirmed `getting-started/concepts.md:101`, `getting-started/architecture.md:173`,
`reference/adapter-contract.md:68`, `:69`, `:96`, the seven `reference/metrics.md` rows, and
`operator-guide/upgrades.md:47-54` are each unit-neutral and untouched by the baseline, D5, D6, or D7.

USEFUL [`### Traps` "the docs pages a lens is most tempted to file are all pre-existing drift"]: it named
`docs/reference/adapter-contract.md:68`/`:69` and `docs/runbooks/coordinator-handoff-slow.md:28` before I
opened either. The runbook really does define the coordinator handoff as the parent-to-child DELEGATION
handoff, an unrelated mechanism, so it is not a mirror of §10.1 at all and no edit to it follows from this
change — EVIDENCE: docs/runbooks/coordinator-handoff-slow.md:27.

DEFERRED [docs/reference/adapter-contract.md]: `:69` states `CoordinatorFence` as the "precondition for any
subsequent operational RPC". Under staged §10.1.2 step 3 the POD-side half of that is false for a session it
holds no fenced generation for, and it is already false in the shipped tree because the adapter reads
`coordination_generation` on the fence and barrier paths alone. What is true is the sender-side duty §10.1.2
step 2 states: the ACQUIRING coordinator sends no operational RPC until its fence is acknowledged. The
remedy is a docs edit this loop may not make, and it was dropped once already this run
(review-log.md:4342-4345) on the ground that the clause restates step 2. Recording it so the docs loop can
decide whether to qualify the row rather than re-derive the question a third time.


### [spec.2.review-edit-sites.2]

DECISION: filed exactly ONE finding, the `spec-changes.md:259-264` "so each stays true" clause — BECAUSE it is the only
`### Deferred` item whose remedy lands in `spec-changes.md`, is independently verified from the tree, AND is now a hard
self-contradiction against `summary.md:354-361` since OD10's withdrawal landed. ALTERNATIVES rejected: (1) the twin
Deferred at `:250`/`:284` about the column default — the Deferred entry itself concedes "the sites are not false", so it
is redundancy polish; (2) the §7 open-decision framing Deferreds at `:604-616` and the OD5-not-listed one — the review
brief bars filing on how an open decision is framed; (3) the §29.10 quiescence-unit `### Open` — three rounds picked
neither remedy and the gate-versus-quiescence distinction makes both readings live; (4) the `summary.md:104-106`
Deferred — its remedy lands in `summary.md`, out of this loop's scope.

FACT: the staging text did NOT move between run-8 spec rounds 1 and 2. `diff -ru` of the r2 snapshot against the
proposal directory returns ONE changed file, the review log (compaction pass 24). Every round-1 finding was refuted and
no fix ran, so `spec-changes.md`, `non-spec-changes.md`, and the checklist are byte-identical to round 1. Price the whole
"what changed" step at one `diff -ru`. — EVIDENCE: scratchpad/cp-snap/0076-run8/spec-r2

FACT: OD10's withdrawal (landed in `summary.md` before the r2 snapshot, mtime 18:00) is what turned an old imprecision
into a filable contradiction. `summary.md:354-361` now says outright that "SPEC-1's staged ground, that each stays true
because the baseline makes the row value positive, reaches the right conclusion by a route that does not hold", and
`summary.md:22-24` gives the mirror-lag ground instead. `spec-changes.md:263-264` still gives the old ground. The two
documents now state opposite grounds for the same ruling. — EVIDENCE:
proposals/0076_.../0076_....summary.md:22-24, :354-361; proposals/0076_.../0076_....spec-changes.md:263-264

FACT: the mirror lag is re-derivable in three reads and I confirmed it independently. `coordination.go:371` assigns the
POST-bump value to a local `generation`, `:399` fences with it, and `:430` writes the PRE-bump snapshot field
`row.CoordinationGeneration` into `upsertMirror`; `RecordHandoff` (`:463-482`) bumps the stored row via `Update` and
returns the new value without touching `row`. `wiring.go:104-114` copies `le.CoordinationGeneration` off the mirror into
`Target`, and `:49` puts that on the wire. So after a takeover the mirror carries G while the pod is fenced at G+1 for a
whole sweep interval. — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:371, :399, :430, :463-482;
pkg/gateway/coordination/barrier/wiring.go:49, :104-114

USEFUL [`### Traps`, "an edit-sites or citation lens that re-derives the standing inventories has spent its pass on the
wrong half"]: I skipped the token greps and went straight to carriers and to the `### Deferred` list. The Deferred list
is where this loop's landable spec-side work actually sits; two of its nine entries name `spec-changes.md` as the file
and say the spec loop is the only lane with a write path. A future edit-sites shard should read `### Deferred` BEFORE
`### Open`.

FACT (negative, re-confirmed so nobody re-derives it): the four spec/ mirror classes are enumerated completely by the
staging. Gap reset: `spec/10:40`, `spec/28:335`, `spec/28:1807`, `spec/29:1311` — a grep for `coordinator_generation_gap`
over `spec/` returns exactly those four and all four are staged. Record-and-reject: `spec/10:38`, `spec/28:315-316`,
`spec/28:1675`, `spec/29:1307-1308` — all staged. Window clause: `spec/28:330-331`, `spec/28:1684-1685`, `spec/28:1807`,
`spec/29:1324-1326` — all staged. `last_fenced_generation`/"last fenced generation"/"fenced value" over
`spec/ docs/ charts/ schemas/` returns twelve lines, every one either staged or a named SCHEMA-1 carrier. "last known
generation" returns exactly `spec/10:60` and `spec/29:1274`, both staged. The pod-lifetime exemption has exactly one
carrier outside `spec/`, `schemas/lenny-adapter.proto:161-162`, and SCHEMA-1 has it.

FACT: `spec/29` carries SIX unit-neutral "the pod validates the `coordination_generation` stamp ... and rejects a stale
one" sentences outside the staged set (`:450`, `:622`, `:790`, `:812`, `:819`, `:1012`), plus `spec/28:237-240`, `:274`,
`:354`, `:390`, `:447-448`. Every one falls under SPEC-2's declared non-site arm ("states the pod's validation without
fixing the compared value and defers to §10.1"). A lens that greps `generation.*reject` will surface all eleven; none is
an edit site. Do not re-derive.

FACT: `spec/04:323` is the `session_eviction_state` mirror column in §4.4.5, not a session-row field list, so SPEC-3's
baseline has no obligation there. `spec/28` §28.4 is prose about the register and carries no rows, so no claim-register
cell inside `spec/` moves. `spec/README.md:290` links §29.10 by heading only, so removing a bullet from its "does not
state" list strands nothing.

WATCHOUT: `awk -F'|' '{print $5}'` on a `spec/28` §28.8 row gives the "Holder of the exclusivity constraint changes"
cell and `$6` the "Operator observable" cell. Field 6 of the four coordination rows (`:1805`-`:1808`) is neutral about
the fenced value's unit and about the connection-lost event's payload, so the §10.1.4 Observability rewrite strands no
§28.8 cell. One `for` loop over the four rows settles it.

OPEN: the `### Open` line "Whether §10.1.8 step 3 fixes the unit of barrier quiescence at the session" is still live and
I declined it. The reason to record: §3's clause (`spec-changes.md:94-96`) is about the barrier GATE (the
`barrierGate` carrying `done`/`checkpointID`/`signaled`), while SPEC-2's narrowed §29.10 bullet (`:449-453`) keeps the
QUIESCENCE unit unanswered. Those are two different objects — `coordinationState.quiesced` has no production reader and
`barrierGate` is the ack link — so the pair is not obviously a contradiction. Whoever settles it should say which object
§29.10's "the unit of the quiescence a barrier establishes" denotes, because that is the whole disagreement.


### [spec.2.review-feasibility.1]

DECISION: returned an empty findings list — BECAUSE `diff -rq` shows only `review-log.md` changed since the r2 snapshot, so the staged spec text is byte-identical to what run-8 spec round 1's thirteen shards read, and my own end-to-end actor-action re-derivation over SPEC-1/2/3 found every assigned actor present under the cited name and able to perform the assigned action — ALTERNATIVES: filing the §10.1 baseline sentence against the non-terminal `resuming → resume_pending` bump (rejected, see FACT below); filing the staged §29.10 hold bullet's five-method allowlist drift (rejected, pre-existing and adjudicated in `### Settled`).

FACT: the feasibility chain behind D7 is fully verified in the tree and does not need re-deriving. A never-handed-off session DOES land in the barrier-target set: the sweep's `held++` arm runs for `bound` sessions (renew) as well as takeovers, and `upsertMirror` is called unconditionally at the end of that arm with `row.CoordinationGeneration`, so `coordination_lease` carries a row for a session no replica ever took over — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:335 (`eligible := bound || priorHolder == s.replicaID || adoptable`), :430 (`s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)`), :431; pkg/gateway/coordination/coordlease/pgstore/pgstore.go:76-81 (`ListHeldByReplica`).

FACT: the hold path can supply a per-session generation at pass 2 without any new plumbing, so SPEC-1's §10.1.4 Observability text is feasible for the adapter. `onHoldTimeout` releases `hold.mu`, then pass 1 returns `heldSession{sessionID, state *slotState}` and pass 2's `terminateHeldSession` already receives the member — EVIDENCE: pkg/adapter/holdstate.go:187-206, :225-228, :283; pkg/adapter/slotsession.go:282-285 (`heldSession` carries `state *slotState`).

FACT: `enterHoldState` already emits `started_sessions` on the `coordinator_connection_lost` line beside `last_generation`, so SPEC-1's staged pod-level event ("names the number of started sessions … and carries no generation") is a deletion of one key rather than a new capability — EVIDENCE: pkg/adapter/holdstate.go:119-120, :130-132.

FACT: the adapter reads `coordination_generation` on exactly two paths, and a grep over the whole non-test adapter package confirms it, so step 3's "only handler that enforces the gate" claim is exact — EVIDENCE: `grep GetCoordinationGeneration pkg/adapter/*.go` returns only pkg/adapter/coordination.go:92 and :223.

WATCHOUT: SPEC-1's staged §10.1 sentence ("a replica coordinating a session no replica has taken over carries that value [1] on its gateway-to-pod messages for it") is false against the TREE but true against the SPEC, and the distinction is what keeps it off the findings list. `bumpCoordinationGenerationOnSnapshotClose` fires on the non-terminal `resuming → resume_pending` branch as well as the terminal one, so a session that failed a resume and was never taken over carries 2. `spec/` states the bump only under a terminal write, so the applied spec stays internally consistent and this is a code-vs-spec drift — EVIDENCE: pkg/gateway/sessionserver/failure.go:211-212, :218-219; spec/07_session-lifecycle.md:215 ("Under the same Postgres transaction that writes the terminal state (step 5)"); spec/04_system-components.md:200. Pass 22 of the ledger already adjudicated this ("nothing in the proposal rests on it").

WATCHOUT: the `coordination_lease` mirror is written from the sweep's PRE-bump `row.CoordinationGeneration` (coordination.go:430 reads the List snapshot, while `RecordHandoff` at :371 bumps the store copy), so after a takeover the mirror lags the pod's fenced value for a whole sweep interval. Do not file it here: it is a code defect, it is the ground on which OD10 was withdrawn, and no staged spec sentence asserts the mirror agrees with the row.

USEFUL [standing context `### Settled`, "Derived inventories"]: the statement that every SPEC-1/SPEC-2 anchor resolves verbatim held on every one I re-checked (spec/10:30, :37, :38, :40, :41, :57, :58, :60; spec/28:315-317, :330-331, :333-335, :1675, :1679-1681, :1683-1685, :1806, :1807, :1808; spec/29:1150-1152, :1186, :1259-1261, :1274, :1307-1313, :1322-1326; spec/04:200). Re-deriving them cost me a pass I could have skipped.


### [spec.2.review-fresh.1]

DECISION: Returned an empty findings list for run 8 spec round 2 — BECAUSE `diff -ru` against the round-2 snapshot shows ONLY `.review-log.md` changed; `spec-changes.md`, `non-spec-changes.md`, and `implementation-checklist.md` are byte-identical to the text round 1's thirteen shards swept and whose six findings were all refuted. I re-derived the anchors independently anyway (see FACTs) and found nothing new above the bar. ALTERNATIVES: filing the four sub-bar candidates listed below; rejected because each is wording precision inside proposal gloss rather than a defect in what lands in `spec/`.

FACT: Every `spec/` anchor SPEC-1, SPEC-2, and SPEC-3 quote resolves verbatim at the cited line, re-verified independently this round: `spec/10` :30, :37, :38, :40, :41, :58, :60, :183, :184, :198; `spec/28` :237-240, :251-253, :291-296, :315-316, :329-332, :333-335, :349-353, :361-365, :1675, :1679-1681, :1683-1685, :1805, :1806, :1807, :1808; `spec/29` :1150-1152, :1186, :1193-1196, :1259-1264, :1274, :1307-1313, :1322-1326, :1467-1470, :1523-1527, :1528-1535, :1540-1543; `spec/04` :200. Do not re-derive.
FACT: Every code citation in D5, D6, D7, and SPEC-1 resolves too: `pkg/adapter/coordination.go` :29-32, :89, :92, :93-94, :99, :108, :112-113, :121, :216, :223, :224-226, :228-231, :236-239; `pkg/adapter/slotsession.go:267`; `pkg/adapter/holdstate.go` :39-44, :53-59, :90-100, :107-112; `pkg/adapter/adapterevents.go:80-96`; `pkg/gateway/sessionserver/start.go:4233-4245`; `cmd/lenny-gateway/coordination_seams.go:233`; `pkg/gateway/coordination/coordination/coordination.go:430`; `cmd/lenny-gateway/httpsurface.go:588-602`; `pkg/gateway/coordination/barrier/wiring.go:49`, :97-124.

WATCHOUT: `spec/04_system-components.md:175` and `:188` classify `CoordinatorFenceRequest` as **pod**-scoped ("carries `session_id` and stays pod-scoped, which is why the classification is declared rather than derived"), which SPEC-1 falsifies, and NO `spec/04` §4.1 edit is staged. This looks like a textbook unstaged-edit-site finding and is NOT one: the summary owns it as **OD3** (`summary.md:228-254`), an open decision with a recommendation, explicitly recording "No `spec/04` §4.1 edit is staged in this proposal." Open decisions are settled outside the adversarial review. EVIDENCE: spec/04_system-components.md:175, :188; summary.md:228-254.
WATCHOUT: `spec-changes.md` §7 lists only three open decisions while the summary carries OD1-OD13. §7 is not the canonical open-decision list; the summary's is. Do not file the mismatch. EVIDENCE: spec-changes.md:604-616 vs summary.md:228+.

FACT: `spec/18` puts the fence and the `coordination_generation` CAS in Phase 4 (`spec/18_build-sequence.md:238`, under §18.11 at :218) and `CheckpointBarrier` in Phase 8 (`:404`, under §18.22 at :398), so S6/CODE-4's baseline (Phase 4) precedes S7/CODE-2's barrier gate (Phase 8). No phase inversion. Do not re-derive.
FACT: No `spec/` surface outside `spec/10`, `spec/28`, `spec/29`, and `spec/04` states a pod-side fenced generation. A grep for `last fenced|last_fenced|fenced generation|generation stamp|generation-stale` over `spec/` excluding those three files returns two hits and both are unit-neutral (`spec/16:183` metric row, `spec/07:93` derive CAS fence). `spec/07:215`, `spec/12:160`, `spec/16:199/:208`, `spec/04:461/:711/:712`, and `spec/18:238/:404` are all unit-neutral. There is no `sessions` DDL anywhere under `spec/`, so the 0-to-1 column-default move has no spec DDL mirror.
FACT: `tests/claim-map.json` carries no row whose status the staged spec edits move. The gap reset's row is `"In-flight RPC cancellation on a generation gap"` at `:174-178` (`ABSENT`, deferral `R16`), the fence's is `"CoordinatorFence"` at `:461-465` (`WIRED`), and the twelve `<Message>.coordination_generation generation fence field` rows are `UNWIRED` on the same `R16`. Re-scoping a sentence changes no status and moves no `spec_anchor` heading, so SPEC-2's "No §28.4 claim-register row moves" holds.

MISTAKE: Nothing this round. The four candidates I derived and dropped, so the next fresh-read lens does not spend a pass on them: (1) `spec-changes.md:407` calls "The constraint excludes a second replica" §28.6's paragraph **closing** sentence when `spec/28:1686-1690` carries one more sentence after it — verbatim quote makes the target unambiguous, same imprecision class as the already-refuted §28.8 `CH-BARRIER` gloss. (2) Staged §10.1.2 step 3's third sentence opens "the pod holds one generation for **that session** at a time" whose nearest antecedent is the preceding sentence's *unset* session — grammatical antecedent collision, resolvable from S1. (3) SPEC-1 says step 2's sentence merely "gains `for that session`" (`spec-changes.md:151-153`) without fixing whether the qualifier attaches to the recording clause or the rejection clause, while its §28.5.1 mirror gets a fully explicit two-sentence replacement (`:311-314`) — placement is recoverable from that mirror. (4) `spec-changes.md:405-407` calls §28.8's `CH-ATTACH` hold cell a "while the adapter is in hold state" cell when `spec/28:1805` spells it "the adapter enters hold state" — gloss.

UNVERIFIED: `spec/10_gateway-internals.md:157` describes the partial manifest's `coordination_generation` as "the coordinator's fenced generation at intent-row INSERT time". Under D6 a never-handed-off session's coordinator has fenced nothing yet carries the baseline, so "fenced" is loose there. I judged this pre-existing (today the same session records 0, which is equally not a fenced value) and therefore not made wrong by the staged edits, so I did not file it. A later lens that disagrees should show the edits are what falsify it.


### [spec.2.review-kubernetes.2]

DECISION: returned an EMPTY findings list over `spec-changes.md`, second consecutive empty on this lane —
BECAUSE `diff -rq` against the run-8 round-1 snapshot shows ONLY `review-log.md` differs, so the staged spec
text is byte-identical to what `[spec.1.review-kubernetes.1]` already swept, and I re-derived that sweep
independently rather than trusting it — ALTERNATIVES: I re-verified the four candidates round 1 dropped and
found no fifth. (1) The one Kubernetes citation in the whole staged file, `spec-changes.md:253-254`
("the migrate Job completes before the gateway Deployment rolls"), resolves verbatim: the chart's header
comment states hook weight -5, "before the gateway Deployment (a normal resource, applied after all pre-*
hooks complete)", and the expand-contract discipline that "keeps a Phase-1 migration backward-compatible so
old replicas coexist with the new schema during the rolling deploy", which is exactly the premise CODE-4
rests the retained `coordfence` floor on. (2) D7 lengthening a drain past a grace period: the agent-pod floor
the `SandboxWarmPool` webhook enforces omits `checkpointBarrierAckTimeoutSeconds` by its own words
("belongs to the gateway pod's grace period ... which the agent pod's own preStop drain does not incur"), the
gateway budget already carries that term, and an acked barrier makes the prestop loop `continue` past the
second capture, so D7 removes drain work. (3) SPEC-1's §10.1.4 Observability rewrite introducing a pod-side
apiserver write: the record it re-scopes is the local-disk post-mortem at `spec/10:58`, and the same bullet
states agent pods have "zero RBAC bindings and no network path to the kube-apiserver", with the terminal
transition owned by the gateway's orphan session reconciler at `:59`. (4) A CRD/status/finaliser/field-manager
surface anywhere in SPEC-1/2/3: none exists.

FACT: the whole Kubernetes lens over the SPEC lane is one grep, now confirmed by two independent passes with
different alternations. `grep -niE 'sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|
informer|etcd|leader|admission|networkpolicy|egress|rbac|helm|chart|deployment|statefulset|daemonset|podspec|
configmap|secret|lease|watch|queue|owner'` over `spec-changes.md` returns 22 lines. Every one is
`REG-COORDLEASE` / "lease mirror" / "lease protocol" (the Redis coordination lease, never
`coordination.k8s.io`), the word "owner" used of the fence-window OWNER FORM the proposal retires,
"admission" used of a session, `ExtendCredentialLeaseRequest` as a proto message name, or the migrate-Job
sentence. There is no Kubernetes object in the staged spec edits, and there is nothing for the lens to bite
on — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:253-254;
charts/lenny/templates/migrate-job.yaml:10-16

FACT: `spec/10_gateway-internals.md` line anchors for the §10.1.4 surface this proposal touches, re-derived
this pass because two earlier rounds cited them one line off: `:53` is the §10.1.4 heading, `:57` Hold state
semantics, `:58` Hold state timeout (the local-disk post-mortem and the zero-RBAC / no-`Sandbox.status.phase`
sentence), `:59` Orphan session reconciliation, `:60` the Observability bullet SPEC-1 rewrites, `:62` the
whole-pod connection-loss paragraph D5 leans on. SPEC-1 cites `:58` for the post-mortem and that is right —
EVIDENCE: spec/10_gateway-internals.md:53-62

FACT: the two prestop citations D7's consequence argument rests on resolve exactly, so the "an accepted
barrier removes a second capture" reasoning is sound rather than plausible: `prestop.go:395`
`if barrierAcked[sess.SessionID] { ... continue }` skips the eviction checkpoint, and `:510` `if o.Acked`
is the only writer of that map — EVIDENCE: pkg/gateway/podlifecycle/prestop/prestop.go:390-398, :505-514

USEFUL [`[spec.1.review-kubernetes.1]`]: its four dropped candidates and its grep were correct and saved this
pass most of a re-derivation; I widened the alternation with `watch|queue|owner` and it surfaced no new
Kubernetes hit.

USEFUL [Traps, "a late pass number in the spec-changes file is not evidence that the staged spec text
changed" / "Run the `diff -rq` first; it is the cheapest move on this proposal"]: one call established that
nothing in the staged text moved since round 1, which is what made the rest of the pass a verification rather
than a re-derivation.

OPEN: this lens has now returned empty on both lanes across runs 6, 7, and 8, over text that has not moved
since commit f9c85f30c. The reopening conditions the standing `### Open` item names (CODE-4 growing a chart
or CRD deliverable, CODE-3 growing an apiserver or `Sandbox.status` write, D7's acceptance arm being
rewritten) still have not fired. Two consecutive independent empty returns on byte-identical text is the
strongest case the loop is going to get for retiring it on the spec lane.


### [spec.2.review-mechanism.1]

DECISION: returned an EMPTY findings list for run 8 spec round 2 — BECAUSE `diff -rq` against the round-1 snapshot shows only `review-log.md` changed, so `spec-changes.md` is byte-identical to the text round 1's six shards worked over and all six of that round's findings were refuted; every mechanism candidate I derived independently resolved to an entry already adjudicated in `### Traps` or routed to a human as an open decision — ALTERNATIVES: weighed and declined seven, listed below with the ground that killed each.

FACT: the whole round-2 reading order collapsed to one call. `diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run8/spec-r2 <proposal dir>` returned exactly one differing file, the review log. This is now the fourth consecutive round on this proposal where "read the changed sections first and hardest" had no target. Run it first; it prices the whole pass. — EVIDENCE: scratchpad/cp-snap/0076-run8/spec-r2

FACT: every spec citation in the staged text that a mechanism argument leans on checks out at the line given. Verified this pass: `spec/10:30` (Generation counters), `:37` (step 1 CAS), `:38` (step 2 window clause), `:41` (step 3 "matches the fenced value"), `:58` (hold timeout / post-mortem), `:60` (Observability), `:183` (step 1, a single ~2000-char line carrying BOTH the "carries the current `coordination_generation`" clause and the closing false-positive sentence SPEC-1 rewrites), `:184`, `:198`; `spec/28:237-240`, `:251-253`, `:291-296`, `:330-331`, `:333-335`, `:349-353`, `:354-357`, `:361-365`, `:1675`, `:1679-1681`, `:1683-1685`, `:1805`-`:1808`; `spec/29:1150-1152`, `:1186`, `:1259-1264`, `:1274`, `:1307-1313`, `:1322-1326`, `:1467-1470`, `:1523-1527`; `spec/04:200`. Also verified the three code anchors D7's arithmetic rests on: `migrations/0050_session_record_fields.up.sql:38-39` (`BIGINT NOT NULL DEFAULT 0 CHECK (coordination_generation >= 0)`), `pkg/gateway/coordination/barrier/wiring.go` (`Send` copies `t.CoordinationGeneration`; `FailedPrecondition` → `ErrGenerationStale`; `MirrorTargetLister.Targets` reads `le.CoordinationGeneration`), and `cmd/lenny-gateway/httpsurface.go:585-603` (cache fallback reads `row.CoordinationGeneration`, defaulting to `int64(0)` when the session read errors). Do not re-derive this citation sweep; it is complete for the spec lane.

FACT: the two barrier predicates are drift-free across all five staged sites. `spec-changes.md` §10.1.2 step 3, §10.1.8 step 1, §28.8 `CH-BARRIER`, §29.7's framing paragraph, and §29.10's narrowed bullet all state the same rule — reject when the pod holds a value for the named session that the barrier does not carry, accept otherwise. A predicate-drift lens can stop after checking those five; the drift, if any, is not there.

WATCHOUT: the generic-RPC gate really does carry two operators across the applied §28.6, and it is NOT filable. After SPEC-2, `spec/28`'s "One holder per session" paragraph says the pod "rejects every RPC carrying a generation **older than** the one it holds for that session" (`:1675`) while the very next paragraph, "The second opener on those channels", says an RPC "carrying a generation **other than** the one the pod holds ... is rejected" (`:1679-1680`). The newer-than case that separates them is reachable, and the proposal's own §10.1.8 rationale names it (a barrier carrying the successor's CAS value while the pod still holds the predecessor's). I declined to file it: the same asymmetry is already in the shipped text (`:1675` "an older generation" against `:1679` "without holding the current generation"), the "older than" form states no acceptance for anything else so it is incomplete rather than contradictory, and §7's first open decision reserves the comparison operator for the human. A later lens that wants it must argue that re-anchoring both sentences on the same referent (the value the pod holds) converts pre-existing looseness into a contradiction, and must say why that is more than the equality-vs-older drift `spec/10:38` and `:41` already carry. — EVIDENCE: spec/28_communication-channels.md:1675, :1679-1680

WATCHOUT: staged §10.1.8 step 1 will state two sources for the barrier's generation on one physical line. The unchanged clause says the message "carries the current `coordination_generation`" and the staged closing sentence says the generation "is read from the session's coordination state when the barrier-target set is assembled". They differ exactly when the value moves between assembly and send, which is the mirror-lag window OD10 is about. Declined: "of the form" already marks step 1's quoted SQL (`SELECT session_id FROM coordination_lease ...`, which selects no generation column at all) as illustrative, the second sentence reads as a refinement of the first rather than a denial, and OD10's in-place withdrawal already records that SPEC-1 rewrites this line. — EVIDENCE: spec/10_gateway-internals.md:183

WATCHOUT: the gap-reset mirrors do NOT gain D6's initial condition and §10.1.2 does. After SPEC-1/SPEC-2, §10.1.2's gap bullet states the unset initial condition while its three mirrors (§28.5.1 `CH-FENCE` Degradation, §28.8 `CH-FENCE` field 5, §29.8 step 7) state the gap predicate against "that session's last fenced generation" with no carve-out for a session that has none. Declined as incompleteness rather than contradiction: the mirrors defer to §10.1, the exemption is absent from every one of them in the shipped text too (D6 records that only `proto:161-162` carries it), and the proposal states the mirror rule explicitly ("each mirror keeps the level of detail it carries today"). A lens wanting this must argue that a mirror stating a predicate whose operand the owning section says may be unset is wrong rather than merely thinner.

WATCHOUT: three further candidates I derived cold and killed on the standing record, so do not spend a pass regenerating them. (1) `spec/10:30`'s "This prevents split-brain even under lease/lock race conditions" against the unset arm — the standing `### Traps` symmetry bullet forecloses it, the rejection there being conditional. (2) `spec/04:175`/`:188` declaring `CoordinatorFenceRequest` pod-scoped, which SPEC-1 falsifies — that is OD3's entire subject, with the recommendation, the three edit sites, and the 0075 consequence already written out at `summary.md:228-256`. (3) §10.1.8's "Either outcome is safe and requires no special handling" widening — OD12 owns it and at least three prior shards recorded declining it on that ground.

FACT: `§29.5` step 2 (`spec/29:812`, "the coordination lease and the generation stamp exclude a second replica") is the unnamed twin of the §28.6 sentence SPEC-2 explicitly checks and leaves standing (`spec/28:1686`, "The constraint excludes a second replica"). SPEC-2's §29 list does not mention it. It is a non-site under SPEC-2's own criterion (states the constraint and its guard, names no rejection outcome), so its absence from the list is not a missed edit site — but a later edit-sites lens will find it and should know it was reached and classified rather than overlooked. — EVIDENCE: spec/29_communication-scenarios.md:812, spec/28_communication-channels.md:1686

FACT: the §29.10 bullet surgery closes both halves of the bullet it deletes. The removed "does not state" bullet (`spec/29:1523-1527`) asks whether hold state is partitioned per slot and whether a fence for one slot's session holds a sibling's RPCs; SPEC-2's new "Shared by the whole pod" hold bullet answers the first and the pod-wide-exit half of the second, and the "Partitioned per slot" addition answers the generation half. The bullet at `:1540-1543`, which the "Partitioned per slot" closing cross-reference points at, is a different question and survives. Checked end to end this pass; no dangling reference results.

USEFUL [`### Traps`, the "diff -rq is the cheapest move" bullet]: it is why this pass cost one call instead of a full re-read of a 622-line staging file.
USEFUL [`### Traps`, the awk -F'|' recipe for §28.8 rows]: `awk -F'|' 'NR>=1803 && NR<=1811 {print NR" ||| "$2" ||| "$5}'` renders all seven exclusivity cells legibly in one call and is what let me confirm the `CH-FENCE` cell carries both staged clauses in field 5.


### [spec.2.review-operational.2]

DECISION: returned EMPTY on the staged spec edits — BECAUSE `spec-changes.md` is byte-identical to the round-1 snapshot (`diff -ru scratchpad/cp-snap/0076-run8/spec-r2 proposals/0076_.../` reports ONE changed file, the review log), the single finding this lens filed in round 1 (the §28.8 `CH-BARRIER` disposition clause) was refuted by the material skeptic, and the reopening conditions `[spec.1.review-operational.1]` named (a rewrite of SPEC-1's §10.1.4 Observability paragraph, D7's acceptance arm, or the §28.8 cells) have not fired — ALTERNATIVES rejected this pass: §16.6's operational-events catalog as an unstaged site (it enumerates CloudEvents `dev.lenny.*` short names and carries no structured-log event, so `coordinator_connection_lost` losing its generation field reaches it not at all, `spec/16:654-660`); §16.1's `coordinator.handoff` span attribute `generation` (`spec/16:366`, unit-neutral); §16.1:199's supersede condition, which is already keyed `(session_id)`; `docs/operator-guide/upgrades.md:47-54` (unit-neutral, and its `success, timeout, error` outcome list already disagrees with `spec/10:190`'s `timeout`/`partial_captured` — pre-existing drift, not reached by any staged edit); `docs/runbooks/double-claim-verification.md:19`, whose "fencing defect" is the `SandboxClaim` webhook and unrelated.

FACT: the whole non-Go carrier set for the two structured log events this proposal moves is four lines, re-derived this pass with one grep over `spec/ docs/ schemas/ charts/ pkg/alerting/ pkg/observability/`: `coordinator_connection_lost` at `spec/10_gateway-internals.md:60` and `spec/29_communication-scenarios.md:1274` (both staged, SPEC-1 and SPEC-2); `coordinator_generation_gap` at `spec/10:40`, `spec/28:335`, `spec/28:1807`, `spec/29:1311`, `schemas/lenny-adapter.proto:160`, `:1461` (all six staged across SPEC-1, SPEC-2, SCHEMA-1). `coordinator_lost` as a reason appears at `spec/04:747`, `spec/10:58`, `spec/28:338`, `spec/28:1807`, `spec/29:1255` — every one is the `session.terminated`/`AdapterTerminating` REASON, not the per-session log line, so none is falsified by the generation moving.

FACT: `lenny_adapter_coordinator_hold` is unlabelled in all four of its carriers, so SPEC-1's "remains pod-scoped" statement creates no edit site anywhere — EVIDENCE: spec/16_observability.md:185; docs/reference/metrics.md:309 (labels column is `--`); pkg/observability/metrics/catalog.go:271; pkg/observability/metrics/catalog_test.go:96.

FACT: `spec/28` §28.8 is titled "Failure and degradation matrix" (`spec/28_communication-channels.md:1785`) and its column order is `Peer absent | Transport fails mid-stream | Holder of the exclusivity constraint changes | Operator observable` (`:1803`). Two consequences worth carrying: the §6 Non-goals phrase "`spec/28`'s CH-ADAPTEREVENTS degradation row" (`spec-changes.md:600`) resolves correctly to the CH-ADAPTEREVENTS row's holder-change cell and is NOT a false citation; and no staged edit touches an "Operator observable" cell, so the matrix's operator-facing column is untouched by this proposal.

WATCHOUT: `spec/10:190`'s BarrierAck-timeout paragraph says `lenny_checkpoint_barrier_ack_total{outcome}` carries `timeout` and `partial_captured`, while `docs/operator-guide/upgrades.md:52` tells operators to monitor it "by outcome: success, timeout, error". That reads like a defect D7 introduces and is not: it predates this proposal, the counter has no incrementer in the tree at all, and no staged sentence touches either carrier — EVIDENCE: spec/10_gateway-internals.md:190; docs/operator-guide/upgrades.md:49-53.

USEFUL [spec.1.review-operational.1]: its closing "Lens exhaustion, counted" note named the exact three reopening conditions for this lens. Checking them against a `diff -ru` of the snapshot cost one call and converted the whole pass from rediscovery into verification. Keep that entry until SPEC-1's §10.1.4 paragraph, D7's acceptance arm, or a §28.8 cell is rewritten.

USEFUL [Settled, "Derived inventories. Do not re-derive any of these."]: the thirteen-line `docs/` surface and the "no alert, runbook, or tier-11 test is reached" conclusion held on spot-check (`pkg/alerting/rules/rules.go`'s only coordinator alert is `CoordinatorHandoffSlow` on `lenny_coordinator_handoff_duration_seconds`, `spec/16:552`). Spot-checking three of its members instead of re-deriving the set saved most of a pass.


### [spec.2.review-performance.1]

DECISION: Empty findings list for the performance / scalability / failure-mode lens over run 8 spec round 2 — BECAUSE
`diff -rq` shows the ONLY file that moved since the r2 snapshot is the review log, so `spec-changes.md` is byte-identical
to the text this lens (and five predecessors) already cleared, and every write-rate, bottleneck, hot-key, informer, and
degradation axis I reconstructed from primary sources resolves to unchanged, strictly cheaper, or a named refuted class —
ALTERNATIVES: I worked up and rejected (a) D7 newly serialising N co-tenant captures inside one 90s deadline, (b) the
staged §10.1.8 step-1 two-outcome enumeration being false on the cache-fallback-zero path, and (c) the accepted barrier's
quiescence widening the §10.1.8 closing interruption bound. Each is recorded below.

FACT: `Barrier.Dispatch` fans out CONCURRENTLY — `for i, t := range targets { go func(){ outcomes[i] = c.dispatchOne(...) }}`
then `wg.Wait()` — and `dispatchOne` starts `CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and `cpWG.Wait()`s
AFTER it, unconditionally. So `dispatchOne`'s wall clock is already >= the capture duration today even when the barrier is
refused instantly, and D7 changes only `out.Acked`. `prestop`'s post-barrier loop then `continue`s on `barrierAcked[id]`.
At Tier 3 (400 sessions per draining replica, PDB `maxUnavailable: 1`) the pre-fix drain is 400 concurrent barrier-window
captures PLUS 400 sequential post-barrier captures; post-fix it is the first set alone. D7 is strictly cheaper at the top
tier, and candidate (a) does not exist — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:190-201, :217-227, :243-244;
pkg/gateway/podlifecycle/prestop/prestop.go:382-397, :501-506; spec/17_deployment-topology.md:1110 (10,000 active sessions),
spec/10_gateway-internals.md:184 (400 pods per replica, one wall-clock deadline).

FACT: the only new store write D7 creates is `sessioncheckpointmeta.Upsert` on the newly-acked population — up to 400 rows
per draining replica spread over the 90s ack window (~4.5/s), against the Tier-3 Postgres budget. One replica drains at a
time under the flat `maxUnavailable: 1` PDB, so it does not compound — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:235-246;
spec/17_deployment-topology.md:7.

WATCHOUT: candidate (b) is a close variant of a NAMED refuted class and must not be re-filed. Staged §10.1.8 step 1
(`spec-changes.md:206-222`) enumerates two outcomes for "a false-positive surviving the cache fallback" and closes "Either
outcome is safe". On a real Postgres failover the fallback closure seeds `gen := int64(0)` and overwrites it only on a
successful `w.sessions.Get`, so the barrier carries 0 and the adapter refuses with `InvalidArgument` on the non-positive
guard — an outcome neither arm names. The standing refuted-class list already kills this as class (j) ("the staged §10.1
invariant 'every generation a pod validates is positive' as falsified by the cache fallback's literal 0 ... the
already-refuted 'unreachable by construction' class"), and a run-8 operational shard separately rejected the sibling
"a value the gateway should never send" sentence at `spec-changes.md:290` on the same ground. The outcome is fail-closed
either way — EVIDENCE: cmd/lenny-gateway/httpsurface.go:591-599; pkg/adapter/coordination.go:224-226;
review-log.md:468-470, :4780.

FACT: candidate (c) is backwards. §10.1.8's closing sentence bounds the interruption to "at most one in-flight tool call
per session (the one executing when the barrier fires)", and the bound is on ABANDONMENT rather than on stall duration —
"To prevent in-flight tool calls from being abandoned". A refused barrier establishes no step-2 quiescence at all, so D7's
acceptance makes that sentence true for MORE of the population rather than falsifying it — EVIDENCE:
spec/10_gateway-internals.md:184 (step 2), :198 (closing sentence).

USEFUL [`### Settled` "The staging is write-neutral"]: its enumeration of the three `sessions` counter writers and its
"the per-session fenced value is adapter process memory bounded by `maxConcurrentSessions`" line let me spend the pass on
the drain arithmetic instead of re-deriving the write inventory. Re-confirmed against the tree; no correction.

USEFUL [`### Settled` "The barrier's cache fallback puts a literal 0 on the wire"]: its warning that no `httpsurface.go`
exists under `pkg/` is what let me find the closure at all.

OPEN: this lens now has six empty returns on this staging and the text has not moved in two of them. The standing
`### Open` item asking whether an exhausted lens is retired is still unanswered, and it names the reopening conditions for
this one (D7's acceptance arm rewritten, the retained `coordfence` floor's rolling-window behaviour, or §10.1.8's failure
arm). None moved this round.


### [spec.2.review-reliability.1]

DECISION: Returned EMPTY on the spec staging — BECAUSE `diff -rq scratchpad/cp-snap/0076-run8/spec-r2 proposals/0076_.../` reports only
`review-log.md` differing, so both staged files are byte-identical to the text this lens has already returned empty on five or six times,
and none of the four reopening conditions this lens recorded for itself (D7's acceptance arm, the retained `coordfence` floor's
rolling-window behaviour, CODE-1's entry-lifetime rule, §10.1.8's failure-arm claim) was rewritten — ALTERNATIVES: re-deriving the
redelivery/restart trace over SPEC-1's §10.1.2 step 3, §10.1.4, §10.1.8 step 1, and SPEC-2's mirrors, which I spot-checked rather than
re-derived, on the standing instruction that a lens re-deriving a closed inventory has spent its pass on the wrong half.

FACT: the two live claims this lens turns on both verify in the tree, so a future reliability run need not re-open them.
(1) The §10.1.4 zero-sentinel invariant has its ground in the live staged text: `spec-changes.md:283-291` closes "and the value every
other fence path sends is floored at 1" and names the retained gateway floor explicitly, and that floor is real —
EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:147-153 (`if gen <= 0 { ... gen = 1 }`).
(2) D7's accepted-but-never-linked barrier cannot wedge: the adapter's wait is `select { case <-done: case <-ctx.Done() }` under the RPC
context deadline the gateway sets from `checkpointBarrierAckTimeoutSeconds`, so the acceptance arm is bounded rather than unbounded —
EVIDENCE: pkg/adapter/coordination.go:264-268, and the comment at :259-263 states the empty-`checkpoint_ref` outcome as designed.

CORRECTS [standing `### Open`, "UNVERIFIED: whether SPEC-1's §10.1.4 zero-invariant still has a ground for the rolling-window row class"]:
the disagreement is resolvable by reading the live file and it resolves in favour of the `### Settled` reading. `spec-changes.md:283-291`
does carry both the "floored at 1" clause and the named `coordfence.go:147-153` floor kept "for a row an old binary wrote at 0 during the
rolling window". The run-6 post-fix block's contrary reading is stale against the live text.

USEFUL [standing `### Traps`, "Three cheap mechanical checks price a whole pass on this proposal"]: the `diff -rq` against the round
snapshot cost one call and settled the whole pass. Run it before reading anything.


### [spec.2.review-security.1]

DECISION: returned EMPTY over run-8 spec round 2 — BECAUSE `diff -rq scratchpad/cp-snap/0076-run8/spec-r2
proposals/0076_.../` returns exactly ONE differing file, `...review-log.md`, so `spec-changes.md`,
`non-spec-changes.md`, `implementation-checklist.md`, `summary.md`, `status.md`, `problem-statement.md`, and
`deviations.md` are byte-identical to the text `[spec.1.review-security.1]` already returned empty on, and
none of the reopening conditions the standing lens-exhaustion bullet names (SPEC-1 step-3 wording, the §28.6
second-opener clause, D7's acceptance arm, SPEC-3's baseline, §8) has been rewritten. This is the seventh
security pass over this text. I re-ran both lens checks against primary sources rather than deferring —
ALTERNATIVES weighed and rejected: (1) the §28.6 second-opener "rejects none of that session's RPCs on
generation grounds" widened across `CH-ATTACH` and `CH-CHECKPOINT` as a control regression — rejected, the
adapter reads `coordination_generation` on the fence and barrier paths alone
(`pkg/adapter/coordination.go:92`, `:223`), so on those two channels the staged sentence describes shipped
behaviour and removes no gate; (2) the §28.5.1/§28.6/§28.8/§29.8-step-9 window clause moving from "the prior
coordinator's RPCs" to "RPCs carrying the prior generation" as an exclusivity relaxation — rejected, the
residual it widens over (a superseded draining replica quiescing a session it no longer coordinates for up to
the 90s ack deadline) is recorded as OD12 in `summary.md:376-382` with no recommendation, and open decisions
are settled outside this review; (3) the §29.10 hold bullet — rejected, it mirrors `spec/10:56` verbatim in
substance.

FACT (re-verified this run from the tree, so the next security lens need not): §28.6's guard sentence really
does name `REG-COORDLEASE` *together with* the `coordination_generation` stamp, which is what makes the
staging's "the lease alone excludes the second replica for a session the pod holds no generation for"
disposition sound; and `REG-COORDLEASE` clears the §12.4 durable-fallback bar, being Redis
`t:<tenant>:lease:session:<session>` compare-and-set with a 60s expiry AND a stated Postgres fallback on the
`CH-FENCE` Exclusivity bullet — EVIDENCE: spec/28_communication-channels.md:1670-1677, :137, :329-331.

FACT: no value bounding a security property is touched anywhere in the staged spec edits. A grep of
`spec-changes.md` for `maxTasksPerPod|scrubFailure|quota|RBAC|NetworkPolicy|egress|admission|ServiceAccount|
SPIFFE|mTLS|credential` returns only incidental hits on the word "acknowledged" (fence acknowledgement) and
none on a residual-state limit, isolation counter, or reuse ceiling. Check (2)'s trust-boundary half is
therefore vacuous on this staging beyond the already-settled `CoordinatorFenceResponse.last_fenced_generation`
self-report, which reaches no gateway decision — EVIDENCE: proposals/0076_.../…spec-changes.md (grep).

FACT: the staged §29.10 removal of the first "does not state" bullet is answered on both halves it asks.
Shipped `spec/29_communication-scenarios.md:1522-1528` asks (i) whether hold state is partitioned per slot and
(ii) whether a fence driven for one slot's session holds a sibling slot's RPCs; SPEC-2's new "Shared by the
whole pod" hold bullet answers (i) pod-wide and (ii) "A successful fence for any one of those sessions exits
the hold for the pod". The fourth "does not state" bullet (two replicas coordinating two slots,
`spec/29:1544-1548`) is NOT removed, which is what keeps the "Partitioned per slot" bullet's closing
cross-reference resolvable — EVIDENCE: spec/29_communication-scenarios.md:1522-1528, :1544-1548;
spec-changes.md:431-448.

USEFUL [`[spec.1.review-security.1]` FACT, the git-log-one-command check]: the `diff -rq` form of it priced
this whole pass in one call, and the standing `### Traps` refuted-class list plus the run preamble's refuted
list killed four candidates before any cost a work-up.

USEFUL [`### Settled`, "Tenant pinning makes co-tenancy same-tenant by construction"] and [`### Settled`,
"`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches no gateway decision"]:
between them these close the whole cross-tenant leg and the whole trust-boundary half of this lens. Keep both.

OPEN: the standing `### Open` line "UNVERIFIED: the rolling-window zero-row population"
(`[spec.1.review-security.1]`) was worked end to end by that shard and found below the bar. It has now been
carried a seventh round with no new evidence and nobody has closed it. A human or the next compaction pass
should delete it rather than pay for it again.

OPEN: security has now returned empty seven times, six of them over byte-identical text, and this pass added
nothing a future round can use beyond the three FACTs above. The lens-retirement question the standing
`### Open` list has asked for five rounds is live and unanswered; on this proposal the honest reopening
conditions are exactly the five the `### Settled` bullet names.

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


### [spec.2.fix-G1.1] (2026-09-04, corrections to the previous fixer's own pass)

DECISION: qualified the previous fixer's new provenance sentence in SPEC-1's rationale with "On the healthy
path", so the mirror row is named as the source on the path where it is the source and the cache-fallback
source the same document already names is not excluded — BECAUSE the sentence as written said the dispatcher
copies the mirror value "rather than the session row's" without qualification, which contradicts
`spec-changes.md:79-82` two hundred lines earlier ("either the lease mirror row ... or the live session row
on the cache fallback"). ALTERNATIVES: reverting the whole replaced paragraph (throws away the mirror-lag
ground, which is correct and which three earlier rounds derived and could not land); widening the sentence to
enumerate both sources (restates `:79-82` and adds nothing to the ground, which needs only one path on which
the excluded sentences are false).
FACT: `MirrorTargetLister.Targets` returns the mirror set with `SourcePostgres` only when the mirror read
succeeds; on a read error it falls through to `l.Fallback()` with `SourceCacheFallback` — EVIDENCE:
pkg/gateway/coordination/barrier/wiring.go:106-115, :120-121
FACT: the fallback closure reads the generation from the live session row, `gen = row.CoordinationGeneration`
off `w.sessions.Get`, and puts it on the `barrier.Target` — EVIDENCE: cmd/lenny-gateway/httpsurface.go:585-604
FACT: the exclusivity was new this round. The pre-edit text said only "Each names the row value the
dispatcher copies onto the wire (`wiring.go:49`)" and made no claim about which row — EVIDENCE: the pre-edit
paragraph in `git diff` of `spec-changes.md` for this run
FACT: the standing context already carried the qualifier the fixer dropped ("on the healthy path the
dispatcher copies the mirror value") — EVIDENCE: review-log.md:1164
DRIFT SWEEP (checked, clean): no other file in the proposal restates the provenance sentence. A grep for
"mirror row", "copies onto the wire", and "wiring.go:104-114" over `summary.md`, `problem-statement.md`, and
`non-spec-changes.md` returns nothing. `summary.md:416-421` already scopes the mirror-lag defect to the
healthy path, so it agrees with the corrected sentence. `summary.md:21-24` states the no-restatement ruling
without naming a source row, so the qualifier does not reach it. The deliverable index adds and removes no
SPEC id this round, so it needed no edit.
WATCHOUT: the paragraph's conclusion (neither §10.1.8 step 1 nor §29.7 step 4 is an edit site) rests only on
the existence of a path on which those sentences are false. Do not let a later pass read the "healthy path"
qualifier as weakening the ruling and re-open the two sentences as edit sites.
DEFERRED [the proposal has no `## Resolved in adversarial review` section]: run 5 evacuated it into the
review-log archive, and no live pass subsection for the previous fixer exists anywhere in the proposal, so
there is nothing to append a correction bullet to and this shard carries the pass record instead. The
summary's pointer at `:106-108` was already redirected to the archive by `[spec.1.fix-G1.1]` and needs no
further edit.


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




## Index and checklist reconciliation, 2026-09-04

Reconciled `## Deliverable index`, the checklist's spec lane, and the non-spec steps' `Depends on:` against
the staging as it stands after run 8's spec loop, which ended with findings still open. The staged set is
unchanged from the 2026-09-03 reconciliation: SPEC-1, SPEC-2, SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3,
CODE-4, and TEST-1, each carried once in the index and once in the checklist, and no deliverable appears in
the index that the staging does not carry. The one staging edit since that pass is the mirror-lag ground
swapped into SPEC-1's rationale paragraph, which adds, removes, and retargets no deliverable, so no index
row changed and no checklist step was added, removed, resequenced, or re-tiered. The spec lane remains the
leading block S1 (SPEC-1), S2 (SPEC-2), S3 (SPEC-3), in the order the spec edits must be applied, and the
non-spec steps keep S4 (SCHEMA-1), S5 (CODE-1 and CODE-3), S6 (CODE-4), S7 (CODE-2), and S8 (TEST-1) with
their tiers. Each `Depends on:` was re-read against the current SPEC ids and still names the spec step
staging the statements its work implements: S4 on S2, S5 on S1, S6 on S1 and S3, S7 on S1, S5, and S6, and
S8 on S5, S6, and S7. Every box is unchecked.

CORRECTS: none. Every `DEFERRED` entry in this log whose remedy lands in one of the four files this pass may
edit is already closed. The `summary.md:104-106` "Resolved in adversarial review" pointer was closed by the
2026-09-03 reconciliation and the current text points at the review-log archive. The `spec-changes.md`
`:259-264` "so each stays true" clause was closed by `[spec.2.fix-G1.1]` and `[spec.2.fix-design-G1.1]`,
which swapped SPEC-1's ground to the mirror lag. The `tests/claim-map.json` entry was closed by the
2026-09-03 reconciliation, which carried its surviving statement-level half to the reviewer as OD11; its
status half is settled and must not be revived. The `## Resolved in adversarial review` entry records that
the summary pointer it names needs no further edit. No statement in the summary, the checklist, or the
non-spec changes was found false against the staging as it now stands.

OPEN: SPEC-1's two "column default and the create path's floor" attributions, at the counter-baseline
sentence and at the §10.1.4 zero-invariant sentence, credit the baseline to the column default while
`non-spec-changes.md` states that `Create` names `coordination_generation` in its insert column list, so
the default baselines nothing and the two `Create` floors are the whole enforcement. The sites are not
false, since CODE-4 does land the default, but the attribution overstates it. The remedy lands in
`spec-changes.md`, which this pass may not edit. Carried forward for the third pass running.

OPEN: `spec-changes.md` §7 "Open decisions for review" still presents three live reviewer decisions while
the summary dispositions item 3 (`coord.mu`) as an implementation choice and item 2 (a fence for an unheld
session) as out of scope with a named consequence, and §4's detailed-design bullets duplicate items 2 and 3
verbatim. The remedy lands in `spec-changes.md`, which this pass may not edit.

OPEN: `spec-changes.md` §7 does not list OD5, the open decision with the largest effect on the staged spec
text, so a reviewer reading only the staged spec file sees no sign that SPEC-3 and SPEC-1's §10.1 baseline
paragraph are conditional. The summary's OD5 now states that consequence; the §7 bullet the log calls
landable in a spec round is still unwritten, and the remedy lands in `spec-changes.md`.

OPEN: four `pkg/adapter` doc comments go false when CODE-3 lands and none is in CODE-3's enumeration of the
sites the `gen` field drags: `holdstate.go:116-118`, `coordination.go:126-128`, `holdstate.go:103`, and
`holdstate.go:153-155`. CODE-3's "Those sites are the whole of what the field drags" is false against all
four. The remedy is a staged code edit naming the four comments, which lands in `non-spec-changes.md`
CODE-3 and which no non-spec lens has authored, so this pass carries it rather than writing it.

OPEN: CODE-3's staged sentence has `terminateHeldSession` and `writeHoldPostMortem` read each terminated
session's last fenced generation off the detached `*slotState` and states no synchronisation, while that
value lives inside `coordinationState`, whose every other reader takes its `mu`. The window is verified: a
`CoordinatorFence` admitted by the hold's allowlist can write `lastFenced` on the same entry while pass 2
reads it for the post-mortem, and the accessor cannot be used because pass 1 has already deregistered the
session. The remedy is one clause requiring the locked read, which lands in `non-spec-changes.md` CODE-3 as
new staged code content that no non-spec lens has authored.

OPEN: `docs/reference/adapter-contract.md:69` states `CoordinatorFence` as the "precondition for any
subsequent operational RPC". The pod-side half is false under staged §10.1.2 step 3 for a session the pod
holds no fenced generation for, and it is already false in the shipped tree. What is true is the
sender-side duty §10.1.2 step 2 states. The remedy is a docs edit, which lands outside this proposal's
staged set and outside the files this pass may edit.

OPEN: the barrier's cache-fallback closure at `cmd/lenny-gateway/httpsurface.go:588-602` calls
`w.sessions.Get(context.Background(), ...)` once per binding with no deadline, on the path taken when the
mirror read has already failed, so a hung Postgres burns the whole preStop grace with no drain. This is
pre-existing shipped code that OD1 cites and that no deliverable here touches. It is routed to a
gateway-side fix rather than to this proposal.

OPEN: the summary's "Watch out for" block still says the status file's "closing paragraph still frames the
hold's scope as the open question this change answers", which the summary's own `### Corrections
outstanding` section falsifies and which pass 24 confirmed against the 33-line status file. Deleting that
clause is the whole fix. It is recorded in this log's standing `### Open` rather than as a `DEFERRED`, so
this pass surfaces it rather than applying it.

Decisions carried into `summary.md` `## Open decisions`, from this log's standing open list. Each states the
ground the log entry gives, and each says that no recommendation was derived, because none was.

- **OD14**, whether migration 0181's backfill runs unbatched and what bounds a migration's run time. Two
  readings of the full-table `UPDATE` under a blocking `pre-install,pre-upgrade` hook stand in the log,
  filed on scale and declined on correctness and precedent, and neither corrects the other. The budget half
  is the same entry's closing question and it reached no file a reviewer reads.
- **OD15**, whether `checkpointBarrierAckTimeoutSeconds` is sized for the sessions D7 admits. The log
  records the effect as immaterial at Tier 3 and the sizing as a human's call, and it reached no file a
  reviewer reads.
- **OD2** gains the sentence its log entry says it lacks and OD3 already carries: no deliverable stages the
  code change the recommendation asks for, CODE-2 being scoped to `pkg/adapter/coordination.go:236` while
  nothing touches the fence guard at `:99`.
- **OD5** gains the cost of splitting that the log records and OD5 omitted: a split answer deletes SPEC-3
  entirely and SPEC-1's §10.1 baseline paragraph.
- **OD7** gains the rider the log records: its recommendation names the session's binding on the pod while
  SPEC-1 stages D6's first form, unset until that session's first accepted fence on the pod, and the two
  coincide only while a rebind onto the same pod is unreachable, so accepting the recommendation changes a
  SPEC-1 sentence.


### [f1.human-decisions]

DECISION: OD2 stays the human's and its entry is rewritten so it is answerable in one sitting. Written into
`summary.md` `## Open decisions`, OD2, replaced in place and keeping the identifier: the question (does this
proposal stage the pod's acceptance of a fence retried at the generation it already recorded, or does a
successor own it), the recommendation (a successor owns it, the staging stands, confidence moderate), the
ground, one weighed alternative, the permanent residual a "yes" accepts, and the cost of the recommendation.
No answer is staged anywhere.

FACT: every anchor the rewritten entry carries was re-verified against the tree this firing.
`spec/10_gateway-internals.md:39` orders the retry "with the same generation value";
`pkg/adapter/coordination.go:99` refuses `gen <= lastFenced` and `:102-106` returns the response body
alongside the `FailedPrecondition` status; `coordfence.go:164-179` relinquishes when the re-read shows no
advance and `:147-153` keeps the non-positive floor; `schemas/lenny-adapter.proto:1456-1458` carries the
`accepted` predicate; `BUILD-GAPS.md` finding F-4.7.2 is closed and its resolution records the handler as
recording a strictly-monotonic generation and rejecting equal-or-lower.

FACT (new): the gateway cannot classify the rejection on its own.
`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56` returns a zero-valued `CoordinatorFenceResult`
on any error, so `last_fenced_generation` never reaches the fence driver on the rejection path, and a
driver-only remedy would have to parse the detail string or change what the adapter returns. This is the
alternative the entry now records as weighed and lost.

CORRECTS [`summary.md`, OD2]: "nothing in this proposal touches the fence guard at `:99`" was false and is
deleted. CODE-1 removes `Server.coord` and moves `lastFenced` and `initialized` onto the slot entry
(`non-spec-changes.md` CODE-1), so the `:99` predicate is rewritten by this proposal whichever way OD2 is
answered and the remedy is one comparison operator on a line CODE-1 already edits. The entry now states that.
`spec-changes.md:539-542` states the different and still-true claim that no deliverable stages the code
change, so it needed no edit.

WATCHOUT: this log's index-and-checklist reconciliation of 2026-09-04 directs OD2 to gain the sentence that
nothing touches the fence guard at `:99`. That direction rests on the false claim the CORRECTS above deletes
and is superseded; the entry carries the accurate form instead.

WATCHOUT: the withdrawn `RecordHandoff` compare-and-swap rationale and the "land it after the counter
baseline" ordering paragraph are gone from OD2, which both readings of the item directed. The load-bearing
half of the ordering argument survives as the residual paragraph, and OD5's line crediting OD2's remedy with
the first crash-takeover fence rejected at 1 still resolves.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable, so
the implementation checklist stands as it is.

### [f1.human-decisions]

DECISION: OD3 stays the human's and its entry is rewritten so it is answerable in one sitting. Written into
`summary.md` `## Open decisions`, OD3, replaced in place at `:242-294` and keeping the identifier. The entry
now splits the fused question into Question A (after CODE-1, is `CoordinatorFenceRequest` session-scoped;
recommendation yes, reclassify the row and rewrite the declaring sentence naming the pod-scoped hold exit as
the one pod-wide effect that survives under D5, confidence moderate) and Question B (does the `spec/04` §4.1
edit land here or in a successor; recommendation successor, the staging stands, confidence moderate), states
plainly that answering A decides proposal 0075, records the one weighed alternative to A and why it lost,
and prices all three other answers. No answer is staged anywhere.

FACT: every anchor the rewritten entry carries was re-verified against the tree this firing.
`spec/04_system-components.md:175` is the pod row; `:188` is the "carries `session_id` and stays pod-scoped"
sentence; `:151` grounds declaring rather than deriving on `session_id` appearing on messages of both
classes; `:190` is the `ShutdownRequest` precedent with "neither operation is selected by a field's presence
standing in for a scope". `ReportPodScrubRequest` declares `pod_id` alone at
`schemas/lenny-adapter.proto:492-496`. `spec/10_gateway-internals.md:57` makes an accepted `CoordinatorFence`
the only exit from hold state and `:58` has the hold timeout terminate every session the adapter started on
the pod, which is the alternative reading's whole ground.

FACT: the two gate citations are exact. `declaredScope`
(`tests/tier0_static/adapter_proto_message_scope_test.go:75-79`) accepts either word in the scope cell, and
the tier-3 suite's `sessionScopedMessages` map begins at
`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:44` under the comment at `:38-43`
that claims the session-address arm covers the fence. Neither test file appears in
`non-spec-changes.md` `## 9. Files touched on application`, so the entry's claim that a "yes, here" drags
unnamed test files still holds.

CORRECTS [`summary.md`, OD3]: "§6's row files this as a rename" named no file and called a bullet a row. The
statement lives in `spec-changes.md` `## 6. Non-goals`, first bullet ("If both land, whichever is second
rebases onto the first"), and the entry now attributes it there.

WATCHOUT: the summary's impact table row for 0075 says this change "Removes its sole counterexample", while
the rewritten OD3 says CODE-1 removes 0075's stated ground and a "yes" to Question A removes the
counterexample itself. The standing context carries that distinction as UNVERIFIED. The row was left as it
stands because the impact table is another item's home, and OD3 asserts nothing that contradicts it.

WATCHOUT: `spec-changes.md` `## 10. Dependencies` says this proposal "is independent of proposal 0075". That
is still true as an application-ordering claim, since 0075 is an unimplemented draft, and OD3's statement
that answering it decides 0075's fate does not falsify it. Do not file the pair as a contradiction.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable, so
the implementation checklist stands as it is.

### [f1.human-decisions]

DECISION: OD5 stays the human's and its entry is rewritten so it is answerable in one sitting. Written into
`summary.md` `## Open decisions`, OD5, replaced in place at `:308-356` and keeping the identifier. The entry
now opens with the deliverable arithmetic (a "split" deletes SPEC-3 in full, SPEC-1's §10.1 baseline
paragraph, CODE-4, migration 0181, and both session-store `Create` floors), states the recommendation and
its confidence (land here, moderate), gives the forcing chain as its ground, records two weighed
alternatives and why each lost, prices both answers, and names what follows the answer. No answer is
staged.

FACT: the forcing chain's second half holds in the shipped tree. `pkg/adapter/coordination.go:224-226`
refuses a non-positive `coordination_generation` with `InvalidArgument` before the gate at `:236-239` is
reached, so D7's acceptance arm is unreachable for an ordinary never-handed-off session whose row still
reads 0. `charts/lenny/templates/migrate-job.yaml:10-16` is the `pre-install,pre-upgrade` hook at weight -5
that completes before the gateway Deployment rolls.
`migrations/0180_drop_checkpoint_slot_id.up.sql` is the last number taken, so 0181 is free under either
answer, and `grep -rn 0181 proposals/` returns only this proposal.

FACT: a migration carried by a behaviour proposal has precedent here, verified first-hand. Proposal 0049
(`proposals/0049_fix_persist-a-deny-credential-propagation-marker-and-suppress-ll.md`) names
`migrations/0179_sessions_credential_deny.{up,down}.sql` in its own scope paragraph. The entry cites it and
states that precedent does not rank the trade. `.claude/rules/spec-driven-development.md:23` states the
spec-ahead-of-code ordering as the normal one, so a split's interval is not a rule violation either.

CORRECTS [`summary.md`, OD5]: the reading that reached this item recorded a cascade onto OD2's ordering
condition, quoting "If the reviewer splits CODE-4 out under OD5, this fix follows it". The earlier apply in
this firing rewrote OD2 and that sentence is gone; the rewritten OD2 recommends a successor and states that
CODE-4's baseline does not reclaim the split-brain class. The cascade was therefore not written. OD5 now
records only the two cascades that hold against the current text: OD14 travels with 0181, and OD10's
withdrawal stands under either answer on its own statement.

WATCHOUT: OD5 and OD14 are coupled in one direction only. A "split" answer to OD5 moves OD14's subject into
the successor without answering it, so a reviewer who splits has not thereby disposed of the unbatched
backfill question. Do not record OD14 as closed by an OD5 answer.

WATCHOUT: `spec-changes.md` `## 7. Open decisions for review` lists three items and none is OD5, so a
reviewer reading the staged spec changes alone saw no sign that SPEC-3 is conditional. This firing added a
one-line pointer at the end of SPEC-3 (`spec-changes.md:592-594`) naming OD5 and what a "split" deletes.
§7 was left as it stands: its three items are other agents' subjects.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
so the implementation checklist stands as it is.

### [f1.human-decisions]

Item OD7, rebind and the unset state. Disposition `human`, gated. Written into `summary.md`
`## Open decisions`, OD7, replaced in place at `:372-434` and keeping the identifier, superseding the
previous 18-line entry. Nothing was migrated: `spec-changes.md` `## 7. Open decisions for review` lists
three items and none is OD7, so there was no staged-file home to empty.

DECISION: the entry now states both halves of the question separately (is the reset accepted, and does
SPEC-1 state the lifetime as the session's binding on the pod), the recommendation with a split confidence
(moderate on accepting the reset, high on the binding form), the ground for each half, three named
alternatives with why each lost, what each answer costs, and the one unverified fact — BECAUSE the phase
gated OD7 to a person and this firing's job is to make it answerable without reading the proposal.
No answer is staged: `spec-changes.md:142` still carries the "first accepted fence on that pod" form.

FACT (verified this run against the tree, every citation in the entry resolves): the value's carrier is the
slot registry entry and every path ending a binding deletes it — `Shutdown` at
`pkg/adapter/session.go:238` and the hold teardown at `pkg/adapter/holdstate.go:192` through
`pkg/adapter/slotsession.go:361` both call `deregisterSlotLocked`, which does `delete(s.slots, sessionID)`
at `pkg/adapter/slotsession.go:174-188`, and the start and resume rollback paths reach the same deletion
through `releaseSessionSlot` at `:214-215` (callers in `resume.go`, `sdkwarm.go`, `session.go:133`, `:147`,
`:157`). The entry is created on first reference to the slot identifier at `pkg/adapter/slot.go:82-101`.

FACT: the shipped pod-wide field is never cleared. `s.coord.lastFenced` is assigned only at
`pkg/adapter/coordination.go:120` and `initialized` only at `:121`, both on the accepted-fence path, and no
teardown touches either. Its readers are the fence's stale and gap predicates (`:99`, `:108`), the barrier
gate (`:233-239`), the hold's report of the generation it holds (`pkg/adapter/holdstate.go:119`), and the
exported test hook `LastFencedGeneration` (`pkg/adapter/coordination.go:44-47`). No operational RPC is
gated on it.

FACT: shipped §10.1.2 states no initial condition for the value. `spec/10_gateway-internals.md:40` is the
"Gap detection on the pod" bullet and carries no lifetime clause, so SPEC-1 writes new text under either
answer. The only staged site carrying the "first accepted fence on that pod" form is
`spec-changes.md:142`, with D6's own restatement at `spec-changes.md:33` and `summary.md:56`; SPEC-1's
§10.1.2 instruction at `spec-changes.md:155-156` says only that the initial condition is stated after the
parenthetical, without repeating the words, and SPEC-2 carries SPEC-1's statements into `spec/28` and
`spec/29` rather than restating a unit. One sentence therefore moves all of them.

CORRECTS [`summary.md` OD7]: the previous entry cited D2 as `summary.md:47-49`; D2 is `:47-48` and `:49` is
D3. Corrected in the rewritten entry.

WATCHOUT: reading 1 recommended moving the code-side reachability question into
`### Defects in the shipped tree that this proposal does not stage`. This firing did not, because that
section opens "Each was confirmed against the working tree" and the reachability of a same-pod rebind is
exactly what nobody has confirmed. The question is stated inside OD7 with a sentence saying why it stays
there.

OPEN: whether `pkg/gateway/sessionserver` placement can put a session back on the pod it unbound from.
Unanswered in this pass and in the log. `spec/07_session-lifecycle.md:196` settles the specification half
(`resuming → running` is a re-attach on a replacement pod) and the adapter bars nothing. It bounds how often
the residual can be incurred; it changes neither half of the recommendation, because the binding form holds
under both answers.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and `grep -i "rebind\|OD7\|unbind"` over the implementation checklist returns nothing.

### [f1.human-decisions]

Item OD9, whether a later release adds `CHECK (coordination_generation >= 1)` as a Phase-3 migration.
Disposition `human`, gated. Written into `summary.md` `## Open decisions`, OD9, replaced in place at
`:456-504` and keeping the identifier, superseding the previous 8-line entry. Nothing was migrated:
`spec-changes.md` `## 7. Open decisions for review` lists three items and none is OD9, and
`non-spec-changes.md` carries no `## Open decisions for review` section at all, so there was no staged-file
home to empty.

DECISION: the entry now opens by saying it is about a later release and that nothing staged here moves
either way, states the question as one sentence a person can answer, gives the ground for the deferral, the
consequence that makes the question worth asking, two alternatives with what each costs, and an explicit
statement that no recommendation is offered with the reason — BECAUSE the phase gated OD9 to a person and
both readings declined to derive a recommendation on the ground that §10.5 leaves the tightening
discretionary. No answer is staged: `non-spec-changes.md:136-155` is untouched and 0181 still leaves the
`>= 0` check alone.

FACT (verified this run, every citation in the entry resolves): the check is
`migrations/0050_session_record_fields.up.sql:38-39`; §10.5's Phase 3 rule is a permission and an ordering
(`spec/10_gateway-internals.md:429`, `:432`), the preflight gate is `:434` and the minimum inter-phase wait
`:433`; the retained floor is `pkg/gateway/coordination/coordfence/coordfence.go:147-153` and CODE-4's
sentence conditioning its retirement is `non-spec-changes.md:154-155`.

FACT: nothing outside this proposal owns the floor's retirement or the tightening.
`grep -rn "coordination_generation >= 1" proposals/ spec/ docs/ migrations/` returns only 0076's own files
and its log archive, `PROPOSAL-QUEUE.md` carries no successor, and the only `coordfence` hits in
`BUILD-GAPS.md` and `TEST-GAPS.md` are the closed F-11.3.14 fence-driver work and the T-16.3.8
`coordinator.handoff` span gap. `grep -rn "CHECK (" spec/*.md` returns nothing, so `spec/` carries no DDL
constraint text for this column.

FACT (the qualification written into the entry, verified rather than taken from the readings): tightening
the Postgres check would not on its own discharge the floor. The fence reads through
`coordfence.GenerationReader` (`coordfence.go:69-72`), wired to `sessionGenerationReader{store: w.sessions}`
(`cmd/lenny-gateway/metricsbackfill.go:76-82`, `cmd/lenny-gateway/main.go:373-380`), and `w.sessions` is
`sessionpg.New` only when a pool exists (`cmd/lenny-gateway/stores.go:1015`) and otherwise `memstore.New()`
(`:1035`), whose §17.4 restore replaces the map wholesale from JSON without passing through `Create`
(`pkg/gateway/session/sessionstore/memstore/snapshot.go:27-37`).

CORRECTS [`summary.md` OD9]: the previous entry closed "The coupling runs one way and is recorded in neither
entry", which the current text falsifies — OD8's withdrawal at `summary.md:450-452` states the coupling and
CODE-4 states it at `non-spec-changes.md:154-155`. The sentence is gone from the rewritten entry, and
reading 2's instruction to add the coupling to OD8 is therefore already discharged by the text as it stands.

CORRECTS [`summary.md` OD9]: both readings described the baseline's enforcement as the two `Create` floors
"and both stores' `Update` clamps". No `Update` clamp is staged: `grep -n "clamp\|Update"` over
`non-spec-changes.md` returns nothing, and the staging says the two `Create` floors are the whole
enforcement (`:130-135`). The entry says only what is staged.

WATCHOUT: the entry cites `spec/10_gateway-internals.md` by line, as every other open-decision entry in this
file does. The line-citation migration's write domain excludes the staged proposals by construction
(`scripts/specshift/line/line.go:43-47`), so those citations are not sites the ratchet will demand, but they
drift if §10.5 gains or loses lines. §10.5's Phase 3 bullets are not edited by this proposal.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and `grep -in "check\|0181\|coordfence"` over the implementation checklist returns nothing that this edit
makes wrong.

### [f1.human-decisions]

DECISION: OD15 (whether `checkpointBarrierAckTimeoutSeconds` is sized for the sessions D7 admits) stays a
human decision and its entry is rewritten in place at `summary.md:566-624`, superseding the previous 9-line
entry. The identifier is kept verbatim. The entry now carries the question standalone, a recommendation
(accept the residual and record it), the ground, three alternatives with why each lost, what the other
answer costs, and a split confidence: high on the ground, moderate on the recommendation. Nothing was
migrated from a staged change file: `spec-changes.md` `## 7. Open decisions for review` lists three items and
none is OD15, and `non-spec-changes.md` carries no such section.

FACT: the across-pods half of OD15 is answered by the shipped specification and both readings' entries
missed it. `spec/10_gateway-internals.md:185` states the single wall-clock deadline is deliberate ("waits
for `CheckpointBarrierAck` from all of them under a single wall-clock deadline ... not per-pod") and gives
the reason the gateway budget "adds `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30`
once rather than multiplying by session count", naming up to 400 simultaneous uploads at Tier 3 and tying
them to the §17.8.2 "burst, max workspace" row, whose derivation at
`spec/17_deployment-topology.md:1218` sizes the store for every active session uploading 512 MB.

FACT: the same-pod half is not answered and the two sections do not meet.
`spec/05_runtime-registry-and-pool-model.md:546` is normative that eviction checkpoints for
concurrent-session pods "are serialized across slots (not fully parallel)", that the adapter "processes one
slot's checkpoint upload at a time", and that the pod budget is the sum of the per-session caps. §10.1.8's
justification for the one window is parallelism across pods. D7 with CODE-1's per-slot gate is what puts two
co-tenant slots of one pod into a window whose floor at `spec/10_gateway-internals.md:140` carries no slot
multiplier, unlike the agent-pod floor at `:133`.

FACT: the residual is bounded rather than a lost capture. `out.Acked = true` is reached only after a
successful send (`pkg/gateway/coordination/barrier/barrier.go:237`), the post-barrier loop skips only acked
sessions (`pkg/gateway/podlifecycle/prestop/prestop.go:395`), and that loop opens a fresh grace-length
context after `fireBarrier` returns (`prestop.go:380`, `fireBarrier`'s own single window at `:503`). A
session that enters the window and does not ack still gets its own capture under its own tier cap.

CORRECTS [`summary.md` OD15]: the previous entry rested the whole question on the floor asymmetry between
`spec/10:133` and `:140` without citing `:185`, which states the asymmetry as a reasoned design rather than
a gap. It also closed "No recommendation was derived", which the rewritten entry replaces. Its sentence "The
review judged the effect immaterial at Tier 3, where the sequential post-barrier loop reaches few sessions"
is dropped: the loop reaches every unacked session, and how many that is under a co-tenant slow upload is
the unpriced quantity the entry now names.

WATCHOUT: a reading that answers OD15 "yes, widen the floor" pulls in `spec/10_gateway-internals.md:140`
and `pkg/admission/pool_config_validator/validator.go:626-649`. §9's file list
(`non-spec-changes.md:380-425`) carries no CRD field, no chart value, no admission code, and no timeout
constant, so that answer is a scope change rather than an edit to staged text.

WATCHOUT: the entry cites `spec/05`, `spec/10`, and `spec/17` by line, as every other open-decision entry in
this file does. Those are long single-line paragraphs, so the citations survive edits inside them and drift
only when a section gains or loses whole lines.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and nothing in the implementation checklist mentions the barrier ack deadline, the BarrierAck floor, or the
pool-config webhook.

### [f1.human-decisions]

DECISION: the review log's `### Open` migration-budget item is written into `summary.md` OD14 rather than as
a new entry, because OD14 already carried the same question as one trailing clause with no recommendation.
That clause is replaced by a labelled second question carrying its own ground, a confidence, and the cost of
each answer, at `summary.md` `## Open decisions`, OD14. The identifier OD14 is kept verbatim and OD14's
batching half is untouched, so an agent holding that half writes over nothing this edit added.

FACT: nothing in the repository states a wall-clock or row-count budget for a migration run.
`charts/lenny/templates/migrate-job.yaml` renders `backoffLimit` (`:42`) and `ttlSecondsAfterFinished: 600`
(`:45`) and no `activeDeadlineSeconds`; the `migrate` values block exposes `backoffLimit: 5` only
(`charts/lenny/values.yaml:3791`); §10.5 gives phasing, `golang-migrate`, hook execution, the advisory lock,
and partial-completion re-runnability, and its one row-count number (`spec/10_gateway-internals.md:434`) is
scoped to the Phase 3 `COUNT(*)` gate; `docs/runbooks/schema-migration-failure.md` states no run time; and
`docs/operator-guide/upgrades.md:22` passes `--wait --timeout 10m` as an example command.

FACT: `activeDeadlineSeconds` occurs in `charts/lenny/templates/` at `preflight-job.yaml:259`,
`crd-validate-job.yaml:81`, `backup-job.yaml:134`, and `restore-test-cronjob.yaml:141`, and in `spec/` at
`spec/17_deployment-topology.md:571` (120) and `spec/25_agent-operability.md:3995` (7200) and nowhere else.

CORRECTS [this firing's own first draft of the entry]: it said the migrate Job is the one unbounded hook Job.
That is false. `bootstrap-job.yaml`, `minio-bucket-lifecycle-job.yaml`, and `deployment-config-sync-job.yaml`
are `post-install,post-upgrade` hook Jobs with no `activeDeadlineSeconds` either. The landed text says the
chart bounds a Job where the spec gives it a number and leaves it unbounded otherwise, and names the four.

FACT: nothing owns the migrate Job's deadline. `BUILD-GAPS.md`'s migrate-job finding is F-10.5.2
(`:14170-14180`), which created the template and is closed, and its two `activeDeadlineSeconds` hits
(`:35976`, `:35987`) belong to the closed preflight finding F-17.6.11. `PROPOSAL-QUEUE.md` carries no row.

WATCHOUT: the `### Open` line this item came from (`:1580-1582`) sits inside `## Standing context`, which
this firing may not edit, so it stands as a duplicate of OD14's second question until the round-boundary
pass closes it. Its premise was re-verified in full above and holds.

WATCHOUT: answering "write the budget here" is a scope change rather than an edit to staged text. The remedy
lands in §10.5 or in `charts/lenny/templates/migrate-job.yaml`, and `non-spec-changes.md`'s
`## 9. Files touched on application` carries no chart file and no §10.5 subsection.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and the implementation checklist mentions no migration budget or Job deadline.

### [f1.out-of-scope-defects]

DECISION: the barrier-target mirror lag stays in the tree and this proposal only records it
(`out-of-scope-stands`). The existing entry under `### Defects in the shipped tree that this proposal does
not stage` named the defect and one file:line but gave no ground for declining the repair, so it was
rewritten in place in `summary.md`. It now carries the mechanism with both sides of the handoff iteration
cited, the value's path to the wire, the bound on the window, the fail-safe outcome inside it, and the
reasons the repair is not staged. No decision was opened and no repair was staged.

FACT (re-verified this firing against the tree): `coordination.go:371` assigns the post-handoff value from
`RecordHandoff` to a local used for the fence, and `:430` passes the untouched List-snapshot
`row.CoordinationGeneration` to `upsertMirror`; `barrier/wiring.go:104-114` copies the mirror row's value
onto `Target` and `:49` puts it on the `CheckpointBarrier` RPC.

FACT: the window is one sweep. `upsertMirror` runs in the `held++` arm for every lease the replica holds on
every sweep from a fresh List row, and the cadence defaults to 15s (`coordination.go:182-185`).

FACT: the outcome inside the window fails safe. `barrier.go:230-237` sets `Stale` and leaves `Acked` false
on `ErrGenerationStale`, and `prestop.go:395` skips only sessions the barrier acked, so a refused session is
captured by the post-barrier per-session loop rather than dropped.

FACT: `spec/10_gateway-internals.md:183` and `spec/29_communication-scenarios.md:1186` both still read
"current `coordination_generation`", and `spec-changes.md:259-271` is where SPEC-1 rules them out as edit
sites on the ground that the lag already falsifies them. That ruling is why staging the repair here would
reopen two edit sites and the withdrawn OD10 (`summary.md:506`).

FACT: no other proposal and no `BUILD-GAPS.md` finding owns the repair. This closes the log's earlier
`:1947` observation that nobody owns it: the ownership gap is now stated in the summary entry itself rather
than left for a reader to infer.

WATCHOUT: the entry's ground is that the lag is present. A later change that repairs `upsertMirror` must
also revisit SPEC-1's no-edit-site ruling for §10.1.8 step 1 and §29.7 step 4, because the ruling's only
surviving ground is the lag.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.out-of-scope-defects]

DECISION: the fence driver's conflation of three failure classes into
`lenny_coordinator_handoff_stale_total` stays in the tree and this proposal only records it
(`out-of-scope-stands`). The existing entry under `### Defects in the shipped tree that this proposal does
not stage` named the branch and one file:line and gave neither the three refusal sites nor a ground for
declining the repair, so it was rewritten in place in `summary.md`. It now carries the mechanism with all
three refusal sites cited, the wire reason the driver cannot discriminate, what this change does remove,
the cross-reference to OD2, and the reasons the repair is not staged. No decision was opened and no repair
was staged.

FACT (re-verified this firing against the tree): `coordfence.go:164` matches
`status.Code(ferr) == codes.FailedPrecondition` (with `ferr == nil && !res.Accepted` at `:165`) and calls
`f.incStale()` at `:170` before any discrimination; `incStale` at `:203-205` is the only caller of
`IncCoordinatorHandoffStale` outside the metrics package and its own fake.

FACT: all three classes really return the same code. `checkSessionBound` refuses at
`slotsession.go:271-273`; the stale predicate `gen <= s.coord.lastFenced` is `coordination.go:99` and its
error is `:105-106`; a re-fence at the recorded value reaches that same predicate.

FACT: the driver cannot discriminate on the wire. The adapter returns its `CoordinatorFenceResponse`
alongside a non-OK status (`coordination.go:102-106`) and the client drops the body on a non-nil error
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56`).

FACT (new to this log): `spec/16_observability.md:183` states the counter increments on a generation-stale
rejection, so the shipped tree already contradicts a §16.1 row for two of the three classes. `spec/16` is
not in §9's file list, so this proposal does not touch that row. Staged spec text remains silent on the
counter's population: `spec-changes.md` has one `handoff_stale|split-brain|lenny_coordinator` hit, at
`:78`, and it describes the adapter's refusal detail rather than the metric.

FACT: no deliverable reaches the stale arm. §9's file list (`non-spec-changes.md:381-421`) names no file
under `pkg/gateway/coordination/coordfence/`, and CODE-4 cites `coordfence.go:147-153` only to keep the
existing non-positive floor (`non-spec-changes.md:146-155`).

CORRECTS: the third Goal at `summary.md:132` read "`lenny_coordinator_handoff_stale_total` counts a
genuine stale fence", which this entry falsifies in the same document. It now states that the change stops
the co-tenant lower-generation class from counting as a stale fence and points at this entry for the two
producers that survive.

WATCHOUT: the entry's ground is that no staged sentence asserts the counter's population. A later round
that stages a §16.1 or §10.1.5 sentence about what the counter counts turns the conflation into a
statement this proposal would land false, and the disposition would have to be re-read.

WATCHOUT: OD2 and this entry describe the same third class from opposite ends. OD2's closing paragraph
points here for the standing cost and this entry points at OD2 for the ownership. An edit to either must
keep both halves pointing at each other.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.out-of-scope-defects]

DECISION: the tier-3 session-address suite's false coverage claim over `CoordinatorFenceRequest` stays in
the tree and this proposal only records it (`out-of-scope-stands`). The three-line entry under `### Defects
in the shipped tree that this proposal does not stage` was rewritten in place in `summary.md` (now
`:756-789`). It names the clause with its file:line, shows that no arm reaches the fence, states that the
exclusion itself is correct and why, gives the ground for declining the repair, and records that the entry
is conditional on OD3. No decision was opened and no repair was staged.

FACT: no arm covers the fence, and the previous entry's "every assertion iterates a map keyed by the
retired duplicate-address field number" was wrong twice. Three of the four assertions iterate
`sessionScopedMessages` (`session_address_wire_test.go:81`, `:102`, `:130`); the fourth,
`TestTheRetiredAddressWrapperIsGone_spec_15_4` at `:150-158`, walks every message in the file for
`lenny.adapter.v1.SlotId` and names no request. The map is keyed by message NAME and carries the retired
field number as its value (`:44-63`).

FACT: the exclusion is correct on its own ground. `CoordinatorFenceRequest` declares `session_id = 1` and
`coordination_generation = 2` and reserves nothing (`schemas/lenny-adapter.proto:1447-1453`), and
`01d19af01`, the commit that introduced `slot_id`, put it on `InterruptRequest`, `SignalDeadlineRequest`,
`ResumeRequest`, `CheckpointBarrierRequest`, and `ReportUsageRequest` alone. The false half is the coverage
clause at `:40-43`, nothing else in the comment.

FACT: nothing staged moves the clause either way. §9's file list (`non-spec-changes.md:380-421`) names no
path under `tests/tier3_contract/`; the comment's membership rule is §4.1's message-scope table; that row
still reads `pod` (`spec/04_system-components.md:175`); and the only `spec/04` edit staged is SPEC-3's on
§4.2 (`spec-changes.md:580-589`). `declaredScope` accepts either class word
(`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`), so the tier-0 gate is unpinned too.

CORRECTS: OD3's clause at `summary.md:266` read "the map every assertion iterates", which the same
`:150-158` case falsifies. It now reads "the map its scope and reservation assertions iterate". OD3's
questions, recommendations, confidences, and identifier are untouched; only that relative clause moved,
because leaving it would put two disagreeing accounts of the same suite in one document.

WATCHOUT: this entry and OD3 describe the same file from opposite ends, as OD2 and the fence-driver entry
do. The entry points at OD3's Question B (`summary.md:283-289`) for the conditionality and OD3 names the
comment rewrite among a "yes, here" answer's costs. An edit to either must keep both halves pointing at
each other, and the `:283-289` range moves whenever an entry above OD3 grows.

WATCHOUT: the repair's content is not determined while OD3 is open. On "no" the fix is deleting a false
clause; on "yes" it is adding the fence to the suite, which has to settle what value a map recording each
member's retired field number holds for a message that never declared one. A later round that stages the
§4.1 reclassification here makes this file an edit site and must add it, plus the tier-0 scope test, to §9.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.out-of-scope-defects]

DECISION: the missing `CH-ADAPTEREVENTS` client stays an out-of-scope defect. Its entry under
`### Defects in the shipped tree that this proposal does not stage` (`summary.md:794-827`) is rewritten
from five lines to a three-paragraph entry that names the defect, cites where it is, and says why nothing
is staged. No other entry in the section moved.

FACT: the claim register carries `gRPC control stream "Adapter/AdapterEvents" (client)` as `UNWIRED` under
deferral `R12` (`tests/claim-map.json:513-518`), beside a `WIRED` server row (`:519-524`). Both read with a
JSON walker; a line grep on the file returns only the two `claim` keys.

FACT: no production opener exists. `grep -rn AdapterEvents --include=*.go pkg/gateway cmd sdks` outside the
generated code returns one comment (`pkg/gateway/runtime/adapterclient/client.go:464`), and the only
`adapterv1.NewAdapterClient` construction on a non-test path is `:38`.

FACT: the hold has one arm. `enterHoldState` (`pkg/adapter/holdstate.go:115`) has exactly one non-test
caller, `onCoordinatorChannelClosed` (`:99`), which has exactly one caller, the `defer` in
`Server.AdapterEvents` (`pkg/adapter/adapterevents.go:100-108`). There is no timer, health-check, or
Kubernetes-side second arm.

CORRECTS: the old entry's "unreachable outside unit tests" was wrong about the tiers. Tier 2 opens the real
stream through the generated client and drops it
(`tests/tier2_component/slotrelease/revoke_double_teardown_test.go:309-336`) and drives the hold timeout
through `terminateHeldSession` (`pkg/adapter/holdstate.go:225`), where CODE-3's `coordinator_lost` line and
post-mortem live (`:278-307`); tier 9 opens and drops the same stream
(`tests/tier9_security/adapter_hold_termination_surface_test.go:87-90`). What is unreachable is the
deployed path. The entry now says so.

CORRECTS: the goal bullet (`summary.md:138-142`) and the "What is fixed" list (`:31-33`) stated the
coordinator-lost repair without qualification. Both now carry one sentence naming the R12 dependency and
pointing at the defects section, which is where reading 1 asked the disclosure to be carried.

WATCHOUT: the entry cites the residual by document position, "§6 of the staged spec changes"
(`spec-changes.md:606-612`), and the summary's own Non-goals bullet (`:157-161`) states the same residual.
An edit to either must keep both accounts of a pod whose stream holder crashes freezing co-tenant sessions
in agreement.

WATCHOUT: the §28.8 citation is a degradation-matrix row (`spec/28_communication-channels.md:1810`) in the
widest table in the file. It is the row that records the specification as not stating which replica's
connection carries an event when more than one holds a connection, and it is the ground for calling the
client a whole channel implementation rather than a dial.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.
