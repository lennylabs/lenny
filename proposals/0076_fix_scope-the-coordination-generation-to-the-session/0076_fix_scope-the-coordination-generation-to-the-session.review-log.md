# Review log: Scope the coordination generation to the session

## Standing context

**Compaction pass 25, 2026-09-05.** Read the whole ledger, which covers run 8's spec round 2 (two fix shards and thirteen lens shards,
eleven of them empty over byte-identical text), run 10's spec round 1 (one fix pair and thirteen lens shards, three filing), the trailing
run-6 post-fix block, a 2026-09-04 fix-correcting-the-fixer shard, the index-and-checklist reconciliations of 2026-09-03 and 2026-09-04,
and the `[f1.*]` finalisation phase, which rewrote every open-decision entry, every out-of-scope-defect entry, and the whole structure of
`summary.md`. The ledger is left untouched; the round boundary archives it whole and with its ids as soon as this pass returns.
Lifted into `### Settled`: the restructured `summary.md` and every anchor that moved with it, the retirement of the OD4, OD6, OD8, and
OD10 identifiers, the eight-step checklist's `Depends on:` map, the seven non-test callers of `checkSessionBound`, the full guard order on
both generation-reading paths, the eleven unit-neutral `spec/29` and `spec/28` validation sentences outside the staged set, the empty
`generation [0-9]` sweep over `spec/` and `docs/`, the single-statement-batch behaviour of both migration runners, the absence of any
wall-clock bound on a migration run, and the four out-of-scope defects now carried in the summary with grounds. Lifted into `### Traps`:
the `older than` versus `other than` operator asymmetry across applied §28.6, the "silence is an instruction to replace" asymmetry now
verified at its new offsets, the snapshot baselines that make `diff -rq` return a false "unchanged", the `spec/29` and §28.5.1 anchors that
wrap and grep as absent, D7's rationale naming the wrong refusal arm, and the tier-3 suite's map being keyed by message name.
Honoured six corrections against the standing context. The trap "three sentences read as citation defects and are not" loses item (1): the
mirror is seeded from the sweep's PRE-bump snapshot rather than from the row, so SPEC-1's clause was false rather than loose, and the fix
that landed says so; items (2) and (3) stand. The §28.8 `CH-BARRIER` preservation clause is FIXED, so the `### Traps` entry that said to
treat it as live is rewritten to record the repair and the sibling bullets it must not be harmonised toward. The §10.1.4 zero-invariant
disagreement is settled from the live file in favour of the `### Settled` reading, and the run-6 post-fix contrary reading is stale. OD2's
recorded ground "nothing in this proposal touches the fence guard at `:99`" was FALSE and is deleted: CODE-1 rewrites that predicate
whichever way OD2 is answered. The claim that both stores' `Update` clamps enforce the baseline is corrected: the clamps exist in the tree
but NO `Update` clamp is staged, and the two `Create` floors are the whole staged enforcement. The staged §29.10 removal bullet is
`spec-changes.md:439-442`, not `:443-446`.
Closed and deleted: the §28.8 `CH-BARRIER` `### Open` item (fixed), the `:259-264` mirror-provenance `### Deferred` (landed), the
`summary.md:104-106` pointer `### Deferred` (landed), the §10.1.4 zero-invariant UNVERIFIED (settled), the rolling-window zero-row
population UNVERIFIED (worked end to end, below the bar, carried seven rounds), the migration-budget OPEN and the barrier-ack-sizing
UNVERIFIED (both now carried to the reviewer as OD14 and OD15), and four items whose subjects the `[f1.*]` phase rewrote out of existence.
The three fill-the-blanks headers move from `### Open` to `### Deferred` with a disposition and a corrected ground.
**The target of 200 lines was not reached and the section grew to 2,129 lines against pass 24's 1,923,** of which `### Settled` is 993,
`### Traps` 681, `### Open` 286, and `### Deferred` 126, with a further 33 in the new `## Retired` section outside the standing context.
Nothing was dropped to reach the target, and the trade is still the wrong one. The window under compaction was two spec rounds that between them moved nine lines of staged text and
returned twenty-four shards, twenty of them empty, plus a finalisation phase that rewrote `summary.md` end to end. Almost everything the
window produced is a do-not-re-derive fact, a recorded dead end, or a line-anchor correction, and every one of those is what stops the
next round paying what this one did. `### Traps` is where the length lives and it is the section the shards cite: this window credits
standing entries by subject more than thirty times, and every citation names a body a one-line summary would delete. Decline the trade
until the code and test lanes land and the inventories become checkable against a tree.
Mechanical constraints: the Bash write path is denied for this file, so every edit goes through the editor tool and a deletion costs the
full text of what it deletes; ledger ids collide across runs, `[spec.1.*]` and `[spec.2.*]` each naming several generations of shard, and
`[spec.2.*]` in this window is run 8 while `[spec.1.*]` is run 10, so a lower number is the LATER round; and the shards within a round are
ordered alphabetically by lens name rather than chronologically, so a later-sounding id is not a later reading.

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
  ground's range was `spec-changes.md:259-264`. CORRECTED in pass 25: the clause is no longer live. Run 8's fix pair swapped SPEC-1's ground
  to the mirror lag and dropped "under the baseline" from the paragraph's opening sentence, and the paragraph now spans `:259-271`, so the
  two documents give one ground for one ruling and the `### Deferred` that carried this is retired.
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
- **`summary.md` was restructured end to end and is 826 lines against 934, so every standing citation into it below `:73` is stale.**
  New anchors: `## Open decisions for human to make` (renamed from `## Open decisions`) opens at `:153`, was `:177`; the defects section
  (promoted from `###` to `## Defects in the shipped tree that this proposal does not stage`) at `:663`; `## Impacts on other proposals`
  (promoted out of the summary container) at `:796`; `## Deliverable index`, preserved line for line, at `:814`. Lines 1-4 and 45-73 are
  unmoved, so OD7's `:49-50`, `:58`, and `:60-61` still resolve; OD1 through OD3 sit 24 lines above where they were and OD3's
  self-citation is now `:259-265`. `## Summary` now holds `**Problem statement.**` (renamed from `**What is fixed.**` and moved above),
  `**What changes.**`, `**Decisions.**` (renamed from `**Fixed decisions.**`), and `**Watch out for.**`. Three blocks are gone and each
  was dispositioned: `### Items §7 lists`, `### Cross-proposal consequences`, and `### Corrections outstanding in the proposal`.
- **OD4, OD6, OD8, and OD10 are retired identifiers. Do not reuse one and do not re-derive the four as open decisions.** OD4 was withdrawn
  as mis-stated (its live remedy survives as a `**Decisions.**` bullet); OD6 was replaced, its premise refuted because
  `coordination.go:99` compares generations and carries no replica term, so the pod-wide field is a cross-session interlock rather than a
  replica exclusion; OD8 was withdrawn with the floor kept and its coupling carried into OD9; OD10 was withdrawn on the mirror-lag ground
  and its carry-forward now sits in OD5. The reviewer's live set is OD1, OD2, OD3, OD5, OD7, OD9, OD11, OD12, OD13, OD14, and OD15, each
  rewritten in the `[f1.*]` phase to be answerable in one sitting: question, recommendation, confidence, ground, weighed alternatives, and
  the cost of each answer. `spec-changes.md` §7 is not the canonical list and never was.
- **The `Depends on:` map, re-reconciled 2026-09-04 and unchanged since.** S4 on S2, S5 on S1, S6 on S1 and S3, S7 on S1, S5, and S6, and
  S8 on S5, S6, and S7. Every box is unchecked and the `## Deliverable index` matches the checklist row for row.
- **The §28.8 `CH-BARRIER` preservation clause is FIXED.** Run 10's fix replaced the sentence-granular clause with a clause-granular
  disposition naming what survives (the rest of the constraint sentence, plus the cell's second sentence) and stating that only the
  trailing clause of the constraint sentence is replaced. The staged replacement predicate was not touched. Do NOT "harmonise" the sibling
  §28 bullets toward the sentence-granular wording this removed: they are correctly clause-granular already, and the `CH-CHECKPOINT`
  bullet's "as are the cell's constraint sentence and the rest of the row" is accurate because the sentence it replaces there is a
  separate sentence.
- **SPEC-1's `:259-264` mirror-provenance ground is LANDED, and a second pass qualified it.** The paragraph now gives the mirror-lag ground
  rather than "the row value is positive once the baseline is 1, so each stays true", drops "under the baseline" from its opening sentence,
  and names the mirror as the source "On the healthy path" so it does not exclude the cache fallback the same file already documents at
  `:79-82`. The paragraph grew from six lines to thirteen and now spans `:259-271`; every line after it shifted by seven. `summary.md`
  OD10's withdrawal moved with it. Do not read the "healthy path" qualifier as weakening the ruling: the conclusion rests only on the
  existence of a path on which those two sentences are false.
- **`checkSessionBound` has seven non-test call sites** (`attach.go:41`, `lifecycle.go:30`, `:80`, `coordination.go:89`, `:216`,
  `session.go:186`, `usage.go:266`), so D6's "The pod admits no session-scoped RPC for a session before it is bound" is a true general
  claim about the tree rather than a two-handler claim. Do not file it as over-broad.
- **The guard order on both generation-reading paths, verified twice.** Fence: `session_id != ""` → `checkSessionBound` → `gen <= 0` →
  `initialized && gen <= lastFenced` (`coordination.go:86-99`). Barrier: `session_id != ""` → `checkSessionBound` → `barrier_id != ""` →
  `gen <= 0` → `!initialized || gen != fenced` (`:211-239`). Every predicate SPEC-1 and D7 describe sits where they say it does.
- **`spec/29` carries six unit-neutral "the pod validates the stamp and rejects a stale one" sentences outside the staged set**
  (`:450`, `:622`, `:790`, `:812`, `:819`, `:1012`), plus `spec/28:237-240`, `:274`, `:354`, `:390`, and `:447-448`. All eleven fall under
  SPEC-2's declared non-site arm. A lens that greps `generation.*reject` surfaces all of them; none is an edit site. `spec/29:812` (§29.5
  step 2) is the unnamed twin of the §28.6 sentence SPEC-2 checks and leaves standing (`spec/28:1686`); its absence from SPEC-2's list is
  classification rather than oversight.
- **`grep -rn "generation [0-9]\|generation = [0-9]" spec/ docs/` returns NOTHING,** so SPEC-3's baseline falsifies no worked example
  anywhere in `spec/` or `docs/`. `spec/README.md:290` links §29.10 by heading only, so removing a bullet from its "does not state" list
  strands nothing.
- **No `Update` clamp is staged.** The clamps exist in the tree, but `grep -n "clamp\|Update"` over `non-spec-changes.md` returns nothing
  and the staging says the two `Create` floors are the whole enforcement (`non-spec-changes.md:130-135`). Any standing sentence crediting
  the staged baseline to "both stores' `Update` clamps" is describing the tree rather than the deliverable.
- **Both migration runners execute a migration file as one statement batch.** `cmd/lenny-migrate/main.go:226` and
  `pkg/schemamigrate/schemamigrate.go:382` both build the driver as `migratepg.WithInstance(db, &migratepg.Config{})`, so multi-statement
  splitting is off: batching inside 0181 would commit nothing between batches and release no locks, and shortening the lock hold means
  splitting 0181 across migration files or changing the runner. This is ground for OD14's batching half and does not answer it.
- **Nothing in the repository bounds a migration run's wall clock, and nothing owns the gap.** `charts/lenny/templates/migrate-job.yaml`
  renders `backoffLimit` (`:42`) and `ttlSecondsAfterFinished: 600` (`:45`) with no `activeDeadlineSeconds`; `values.yaml:3791` exposes
  `backoffLimit: 5` alone; §10.5's one row-count number (`spec/10:434`) is the Phase 3 `COUNT(*)` gate; `docs/operator-guide/upgrades.md:22`
  passes `--wait --timeout 10m` inside an example command. `activeDeadlineSeconds` appears in the chart only at `preflight-job.yaml:259`,
  `crd-validate-job.yaml:81`, `backup-job.yaml:134`, and `restore-test-cronjob.yaml:141`, and in `spec/` only at `spec/17:571` and
  `spec/25:3995`. The migrate Job is NOT the only unbounded hook Job: `bootstrap-job.yaml`, `minio-bucket-lifecycle-job.yaml`, and
  `deployment-config-sync-job.yaml` are `post-install,post-upgrade` hooks with no deadline either. `BUILD-GAPS.md`'s migrate-job finding
  F-10.5.2 is closed and `PROPOSAL-QUEUE.md` carries no row.
- **Four out-of-scope defects are now recorded in the summary with mechanisms, grounds, and ownership, so none needs re-deriving.**
  (1) The barrier-target mirror lag, one sweep wide at a 15s cadence (`coordination.go:182-185`), failing safe because a refused session is
  captured by the post-barrier loop; no other proposal and no `BUILD-GAPS.md` finding owns the repair, and repairing `upsertMirror` would
  reopen SPEC-1's no-edit-site ruling for §10.1.8 step 1 and §29.7 step 4. (2) The fence driver's conflation of three refusal classes into
  `lenny_coordinator_handoff_stale_total`: `checkSessionBound` (`slotsession.go:271-273`), the stale predicate (`coordination.go:99`,
  error at `:105-106`), and a re-fence at the recorded value all return `FailedPrecondition`, and the client drops the response body on a
  non-nil error (`adapterclient/coordinatorfence.go:55-56`), so `spec/16:183` is already contradicted for two of the three. (3) The tier-3
  session-address suite's false coverage claim over `CoordinatorFenceRequest`, conditional on OD3. (4) The absent `CH-ADAPTEREVENTS`
  client, `UNWIRED` under `R12` (`claim-map.json:513-518`) beside a `WIRED` server row (`:519-524`).
- **The hold has exactly one arm, and the tiers do reach it.** `enterHoldState` (`holdstate.go:115`) has one non-test caller,
  `onCoordinatorChannelClosed` (`:99`), which has one caller, the `defer` in `Server.AdapterEvents` (`adapterevents.go:100-108`). There is
  no timer, health-check, or Kubernetes-side second arm. CORRECTED: "unreachable outside unit tests" was wrong about the tiers. Tier 2
  opens the real stream through the generated client and drops it
  (`tests/tier2_component/slotrelease/revoke_double_teardown_test.go:309-336`) and tier 9 does the same
  (`tests/tier9_security/adapter_hold_termination_surface_test.go:87-90`). What is unreachable is the deployed path.
- **Cross-proposal rows, settled.** The 0080 row names §1.14 (`0080:184-192`), the shipped bullet it records (`spec/29:1523-1527`), the
  staging that removes it (`spec-changes.md:439-442`, NOT `:443-446`), and §2's misattribution (`0080:211`, restated at `:191-192`); 0080's
  §1.12 claim-register rows and §1.13 §29.10 spec-map exception are untouched and §29.10's `Interrupt`-and-barrier bullet is narrowed
  rather than removed. The 0073 row is WITHDRAWN: 0073 is Implemented, so nothing staged can invalidate it, and the dependency it restated
  is already carried by `## 10. Dependencies` and by D4. The impacts table is the only place this proposal states anything about another
  proposal's continued validity.
- **The tier-3 session-address suite's map is keyed by message NAME with the retired field number as its value** (`:44-63`), and three of
  its four assertions iterate `sessionScopedMessages` (`:81`, `:102`, `:130`) while the fourth,
  `TestTheRetiredAddressWrapperIsGone_spec_15_4` (`:150-158`), walks every message for `lenny.adapter.v1.SlotId` and names no request. The
  earlier reading, that "every assertion iterates a map keyed by the retired duplicate-address field number", was wrong twice. The
  exclusion of the fence is itself correct: `CoordinatorFenceRequest` declares `session_id = 1` and `coordination_generation = 2` and
  reserves nothing (`proto:1447-1453`), and commit `01d19af01` put `slot_id` on five other messages. The false half is the coverage clause
  at `:40-43` and nothing else in the comment.
- **The gateway cannot classify a fence rejection on its own.** `adapterclient/coordinatorfence.go:55-56` returns a zero-valued
  `CoordinatorFenceResult` on any error, so `last_fenced_generation` never reaches the fence driver on the rejection path and a
  driver-only remedy would have to parse the detail string or change what the adapter returns. This is the alternative OD2 records as
  weighed and lost.
- **Tightening the Postgres check would not on its own discharge the retained `coordfence` floor.** The fence reads through
  `coordfence.GenerationReader` (`coordfence.go:69-72`), wired to `sessionGenerationReader{store: w.sessions}`
  (`cmd/lenny-gateway/metricsbackfill.go:76-82`, `main.go:373-380`), and `w.sessions` is `sessionpg.New` only when a pool exists
  (`stores.go:1015`) and otherwise `memstore.New()` (`:1035`), whose §17.4 restore replaces the map wholesale from JSON without passing
  through `Create` (`memstore/snapshot.go:27-37`).
- **No gate hard-fails at any intermediate step, and one command prices the check.** Greps over `tests/ scripts/ docs/ charts/ pkg/ cmd/`
  for the seven most distinctive strings SPEC-1 and SPEC-2 delete ("rejected on the stamp", "prior coordinator's RPCs are still accepted",
  "matches the fenced value", "no longer holds the current generation", "last known generation", "is the generation the pod last fenced",
  "hold state is partitioned per slot") return exactly one hit, the Go doc comment at `pkg/adapter/holdstate.go:103` that refuted class (k)
  bars and that `### Deferred` already owns.
- **The four spec/ mirror classes are enumerated completely by the staging.** Gap reset: `spec/10:40`, `spec/28:335`, `:1807`,
  `spec/29:1311`. Record-and-reject: `spec/10:38`, `spec/28:315-316`, `:1675`, `spec/29:1307-1308`. Window clause: `spec/28:330-331`,
  `:1684-1685`, `:1807`, `spec/29:1324-1326`. "prior coordinator's RPCs are still accepted": `spec/28:331`, `:1685`, `:1807`,
  `spec/29:1325`. "matches the fenced value" has one carrier (`spec/10:41`) and "rejected on the stamp" two (`spec/28:1806`, `:1808`). All
  staged. `last fenced generation` / `fenced value` over `spec/ docs/ charts/ schemas/` returns twelve lines, every one staged or a named
  SCHEMA-1 carrier, and "last known generation" returns exactly `spec/10:60` and `spec/29:1274`.
- **`s.mirror.Upsert` has exactly one non-test caller** (`coordination/coordination.go:548`, reached only from `upsertMirror` at `:430`),
  which is the whole evidentiary basis for SPEC-1's mirror-lag paragraph and re-derives in two greps.
- **The applicability surface is small by construction and the next run should be budgeted at a fraction of a pass.** SPEC-1, SPEC-2, and
  SPEC-3 create no file, no heading, no anchor, and no identifier: every staged edit is a sentence or clause substitution inside an
  existing paragraph, so the forward-reference, relocation, anchor-ambiguity, gate-state, and code-against-unlanded-spec classes all
  collapse to nothing.
- **The docs lens has almost no filable surface in a spec-scoped loop, and this is structural rather than incidental.** The loop's scope
  line (report only findings whose fix lands in the staged spec edits) and the lens's own guardrail (a finding here is always a missing or
  wrong docs edit) intersect only in the accepted-or-deferred-failure-mode category, whose remedy the lens allows to be a staged spec edit.
  Every "docs page X is now wrong" finding is out of scope by construction. Start a docs run from the accepted-outcome list and skip the
  mirroring sweep. The proposal has no `## Edge cases and accepted failure modes` section at all, so that instruction has no table to read;
  its absence is not filable and eight runs have not filed it. The accepted outcomes live in §2 (D5, D6, D7), §6 Non-goals, and the
  summary's defects section, and each was checked against staged spec text and lands.
- **`spec/28` §28.4 in `spec/` carries no claim-register rows,** being four paragraphs defining the status set and delegating the rows to
  `tests/claim-map.json`, so "No §28.4 claim-register row moves" cannot be falsified by a missing `spec/28` edit site. No
  structured-log-event inventory exists anywhere under `docs/`, so an adapter log line's field set has no docs companion. `spec/12` carries
  no DDL for the counter columns, so SPEC-3's baseline has no storage-architecture mirror.

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
- **WATCHOUT: two sentences read as citation defects and are not.** CORRECTED in pass 25: the item that used to head this list, SPEC-1's
  "Each names the row value the dispatcher copies onto the wire (`wiring.go:49`)", was WRONG and is deleted. Its ground was that "the
  mirror is seeded from the row". The mirror is seeded from the sweep's PRE-bump `List` snapshot of the row, so it carries a value the row
  does not hold at that moment, and the sentence attributed the wire value to the session row where the code contradicts it on the healthy
  path. Two independent verifiers confirmed the finding and the fix landed. The two that stand: (2) SPEC-1 calls the "Generation counters" bullet §10.1's while it lives in §10.1.1, a subsection of §10.1, so the attribution is loose
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
- **MISTAKE, NOW REPAIRED AFTER SEVEN FILINGS ACROSS FIVE ROUNDS: the §28.8 `CH-BARRIER` disposition clause was copied from the
  `CH-CHECKPOINT` bullet above it,** where it is true. The `CH-CHECKPOINT` cell states the constraint in one sentence and the edited rejection rule in a second; the `CH-BARRIER`
  cell has only two sentences and the replaced clause is the trailing clause of the constraint sentence itself, so "the cell's constraint
  sentence ... unchanged" tells an applier to leave the clause standing. CORRECTED in pass 21: this entry used to read as a defect that was
  found and fixed. It was found in run 4's spec round 2, recorded as a MISTAKE tag rather than filed as a finding, and never repaired;
  four lenses re-filed it in run 6 round 1, and six lenses have now filed it across four rounds without a single fix round touching the
  line; its offsets drifted through `:386-392`, `:390-400`, and `:397-402` while the text stood. FIXED in run 10: the sentence-granular
  preservation clause is replaced by a clause-granular disposition naming what survives, and the staged replacement predicate was not
  touched. The lesson survives the repair and is why this entry is kept: a MISTAKE tag lifted into `### Traps` reads as history, so three
  rounds treated the clause as closed while the text sat byte-identical; a tag on live staged text does not repair the text. The applied
  consequence it would have had is concrete: an applier who honoured it would have left `spec/28:1808` carrying "so a barrier from a
  superseded replica is rejected on the stamp", which assigns the pod an action it cannot perform, since `CheckpointBarrierRequest` carries
  `session_id`, `coordination_generation`, and `barrier_id` and no sender identity (`proto:1475-1483`). Do not now harmonise the sibling
  §28 bullets toward the removed wording, and do not restate the replacement predicate: it is cross-referenced from `spec-changes.md:83-85`
  and from the deliverable index, so editing it cascades. It is the only one of the nine SPEC-2 §28 bullets that names a whole sentence as unchanged while replacing
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
- **WATCHOUT: the generic-RPC gate carries two operators across the APPLIED §28.6, and it is not filable.** After SPEC-2, the "One holder
  per session" paragraph says the pod "rejects every RPC carrying a generation **older than** the one it holds for that session"
  (`spec/28:1675`) while the next paragraph says an RPC "carrying a generation **other than** the one the pod holds ... is rejected"
  (`:1679-1680`). The newer-than case that separates them is reachable and the proposal's own §10.1.8 rationale names it. Declined: the
  same asymmetry is already in the shipped text (`:1675` "an older generation" against `:1679` "without holding the current generation"),
  the "older than" form states no acceptance for anything else so it is incomplete rather than contradictory, and §7's first open decision
  reserves the comparison operator for the human. A later lens that wants it must argue that re-anchoring both sentences on the same
  referent converts pre-existing looseness into a contradiction, and must say why that is more than the equality-versus-older drift
  `spec/10:38` and `:41` already carry.
- **WATCHOUT: choosing the wrong snapshot baseline makes `diff -rq` report a false "unchanged".** `scratchpad/cp-snap/0076-run10/spec-r1-start`
  is byte-identical to the live directory, so diffing against it returns nothing and reads as "nothing has moved since run 8", which is
  false. Run 8 holds `spec-r1-start`, `spec-r2`, `spec-r2-prefix`, `spec-r2-start`, `spec-r3`, and `spec-introspect-r2`; runs 9 and 10 hold
  `spec-r1-start` alone. Diff against `0076-run9/spec-r1-start` or `0076-run8/spec-r3` to see what run 9 did. The two hunks run 9 landed are
  the `CoordinatorFenceResponse` paragraph losing its OD2 cross-reference (now "No deliverable here changes what the handler does with a
  fence carrying the generation the pod already holds") and the 0075 non-goal being restated as an independence statement; neither reaches
  a mechanism.
- **WATCHOUT: the "silence is an instruction to replace" asymmetry is verifiable in one read, and its offsets have moved.** The
  twelve-comment paragraph names its span ("from 'A pod validates' to the end of the consequence clause") and preserves the two trailing
  sentences by name; the `CoordinatorFenceResponse` paragraph carves itself out explicitly; the `CoordinatorFenceRequest` paragraph and the
  three-`CheckpointBarrier`-carrier paragraph hand over full replacement wording with no preservation clause. That is what makes the two
  SCHEMA-1 carrier findings real rather than editorial. Current anchors: `spec-changes.md:517-523`, `:542-547`, `:559-566`.
- **WATCHOUT: a wrapped anchor greps as absent, and this bites on §29.10 and §28.5.1.** SPEC-2's §29.10 anchor "so each slot's session
  carries its own lease and its own generation" returns zero on a single-line grep because it wraps at `spec/29:1467-1468`. Grep a fragment
  rather than the clause, or `\s+`-join the words. The §28.5.1 Messages and Exclusivity quotes all wrap the same way. Four anchors read as
  missing to one lens before it noticed.
- **UNVERIFIED, and recorded here because it reads as a citation defect: SPEC-1's D7 rationale names the wrong refusal arm.** It says the
  shipped barrier gate "refuses it on the absence of a recorded value for the named session (`:236-239`)", but the shipped `initialized`
  and `lastFenced` are pod-wide (`coordination.go:232-239`), so on a pod that has already fenced a co-tenant the refusal comes from the
  `gen != fenced` arm rather than the absence arm. Judged proposal rationale rather than applied spec text and not filed; a citations lens
  should decide whether it clears the bar.
- **WATCHOUT: D7's "That baseline is 0 today, because the column is declared `NOT NULL DEFAULT 0`" attributes the zero to the wrong
  mechanism.** The production zero comes from `pgstore.Create` inserting the column explicitly (`pgstore.go:170`, with
  `coordination_generation` in the `:177` column list). The cited line does say `NOT NULL DEFAULT 0`, the conclusion is true, and CODE-4
  lands both the default and the create-path floor, so nothing downstream rests on the loose "because". Two shards declined it. Do not
  spend a verification on it.
- **WATCHOUT: the CH-BARRIER finding had a split record, and the resolution rule it produced is the durable part.** One shard recorded that
  the material skeptic refuted it; the `### Traps` entry written afterwards said it was live and must be filed. The next mechanism agent
  resolved in favour of the Traps entry because the defect was verifiable in one call and the refutation's ground was recorded nowhere, and
  the fix that followed proved it right. The rule: when a refutation and a standing entry disagree, the refutation must record its ground
  where a grep finds it, or the standing entry wins and the class is re-derived every round.
- **MISTAKE (avoided, and the bar is a lens's own prior DECISION): a fresh-read lens nearly re-filed the `## 5. Proposed changes`
  fill-the-blanks header** because the live `### Open` line records it as "filed and unlanded, a non-spec round having no write path", and
  the run it was reading DID have a write path. What stopped it was its own lens's earlier DECISION recording that the material skeptic
  refuted the header along with three siblings and that re-filing was barred. A standing `### Open` line that says "filed" is not a
  statement that the item is still open to filing; read the live text and the lens's own record before spending a work-up.
- **WATCHOUT: three grep-shaped closures that each price a whole sub-lens in one call.** `grep -rn "coordinator_connection_lost\|coordinator_lost\|last known generation" spec/ docs/ schemas/ charts/`
  returns `spec/10:58`, `:60`, `spec/04:747`, `spec/28:338`, `:1807`, `spec/29:1255`, `:1274`; `coordinator_generation_gap` returns
  `spec/10:40`, `spec/28:335`, `:1807`, `spec/29:1311`, `proto:160`, `:1461`; and `coordinator_handoff_stale|FailedPrecondition` over the
  same trees puts the fence detail string at `proto:1445-1446` alone and the barrier status at `:1473` and `:1478` alone, with `spec/10:71`
  and `spec/16:183` carrying the metric name only and `docs/api/internal.md:499` a generic gRPC-code table.

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
- **Migration 0181's unbatched backfill and the absent migration budget are now ONE reviewer decision, OD14,** which carries both halves,
  the correctness-versus-duration split, the single-statement-batch ground, and three options with their costs. Both readings are recorded
  there and neither corrects the other. `[non-spec.1.review-performance.1]`, `[non-spec.1.review-reliability.1]`, `[f1.human-decisions]`
- **OD2 still needs the reviewer.** If the equal case is accepted, three things move together: `pkg/adapter/coordination.go:99`, the
  `CoordinatorFenceResponse.accepted` sentence, and the staged §28.6 `CH-FENCE` arm at `spec-changes.md:345-346`. `[spec.1.fix-design-G1.1]`
- **OD2's ordering rationale is imprecise for the rolling window.** `summary.md:198-204` says that once CODE-4 baselines the row "the
  collision is unreachable", but for a row an old binary minted at 0 a resume fences at the retained floor's 1 and a takeover's
  `RecordHandoff` bumps 0 to 1 and also fences at 1, so the equal-generation collision OD2 would accept survives for that row class. It
  matters because OD2's recommendation is what would admit the split-brain OD2 says the ordering prevents. `[spec.1.fix-design-G2.1]`
- **Nothing outside CODE-4's closing sentence tracks the retained floor's retirement, and the gap is now OD9 alone.** OD9 derives no
  recommendation, so if the tightening release never happens the floor stays forever, harmless and unowned. CORRECTED in pass 23: the older
  half, that the one-way coupling is "recorded in neither entry", is stale. CORRECTED again in pass 25: OD9 was rewritten in the `[f1.*]`
  phase, the stale closing line is gone, and the coupling is stated by OD8's withdrawal and by CODE-4 at `non-spec-changes.md:154-155`.
  What survives is only the substantive question, which is OD9's own and carries no recommendation because §10.5 leaves the tightening
  discretionary: if the tightening release never happens the floor stays forever, harmless and unowned, and nothing outside this proposal
  owns either the floor's retirement or the tightening. `[spec.1.fix-G2.1]`, `[spec.2.review-open-decisions.1]`,
  `[spec.3.review-open-decisions.1]`, `[non-spec.1.review-open-decisions.1]`, `[f1.human-decisions]`
- **No post-fix record exists for run 4's spec round 2** and its three filed findings cannot be enumerated from the archive; a later round
  that finds a refutation for the `CH-BARRIER` clause should record it where a grep will find it. `[spec.1.review-fresh.<n>]`
- **Status file: promoted out of `### Open` into `### Deferred`,** because the `[f1.cleanup]` phase confirmed both halves and the summary's
  own errata paragraph is gone. `[spec.1-3.*]`, `[non-spec.5.review-open-decisions.1]`, `[f1.cleanup]`
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
- **UNVERIFIED: whether the tier-3 session-address suite's exclusion comment needs the fence added or the false clause deleted,** which is
  not determined while OD3 is open: on "no" the fix is deleting a false coverage clause; on "yes" it is adding the fence to the suite, which
  has to settle what value a map recording each member's retired field number holds for a message that never declared one. A round that
  stages the §4.1 reclassification here makes that file an edit site and must add it, plus the tier-0 scope test, to §9. `[f1.out-of-scope-defects]`
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
- **The three-header deadlock is DISPOSITIONED and now sits in `### Deferred`:** all three come out, and the ground the earlier draft gave
  for dropping them is false. `[non-spec.1.review-edit-sites.1]`, `[non-spec.1.review-open-decisions.1]`, `[f1.cleanup]`
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
- **OD2, OD5, and OD7 were each rewritten in the `[f1.*]` phase and their standing findings are discharged.** OD2 now recommends that a
  successor owns the equal case, states the ground, records the gateway-only fix as the alternative that lost, and carries the residual;
  OD5 gained the deliverable arithmetic, the pre-deployment price, and the OD14 coupling; OD7 gained both halves stated separately, a split
  confidence, three alternatives, and the rider that its recommendation names a different unit from what SPEC-1 stages. What remains open
  in each is the reviewer's answer. `[non-spec.3.review-open-decisions.1]`, `[non-spec.4.review-open-decisions.1]`,
  `[non-spec.6.review-open-decisions.1]`, `[f1.human-decisions]`
- **CORRECTED, and it matters for whoever answers OD2: `pkg/adapter/coordination.go:99` IS touched by this proposal.** The standing claim
  that "nothing touches the fence guard at `:99`, CODE-2 being scoped to `:236` alone" was false. CODE-1 removes `Server.coord` and moves
  `lastFenced` and `initialized` onto the slot entry, so that predicate is rewritten whichever way OD2 is answered and the remedy is one
  comparison operator on a line CODE-1 already edits. The 2026-09-04 reconciliation's direction to write the false form into OD2 is
  superseded. `spec-changes.md`'s different and still-true claim, that no deliverable stages the code change the recommendation asks for,
  needed no edit. `[f1.human-decisions]`
- **UNVERIFIED: which reading `summary.md:123`'s 0075 row means.** "Removes its sole counterexample" against "Removes the ground for its
  sole counterexample": 0075 defines the counterexample as a disagreement between the declared §4.1 scope and the derived one, and no
  deliverable here edits §4.1, so on that reading CODE-1 removes the ground and leaves the counterexample standing until OD3 is answered.
  `[non-spec.5.review-open-decisions.1]`
- **The summary's CH-ADAPTEREVENTS attribution names the wrong bullet.** `summary.md:150-154` calls it a `spec/28` "degradation row"; the
  sentence lives in the §28.5.2 **Exclusivity** bullet (`spec/28:471-478`) and sits in the "Holder of the exclusivity constraint changes"
  column, field 5 of the row at `:1810`. The §28.5.2 Degradation bullet (`:479-494`) carries a different "does not state" sentence, about
  retry and buffering policy. Weighed and not filed (reviewer prose, claim true, stages nothing); a fixer already in `summary.md` should
  change "degradation row" to "exclusivity bullet". `[non-spec.2.review-citations.1]`
- **CLOSED: the summary's `### Items §7 lists` block is gone and each of its three items is accounted for.** `coord.mu` is an
  implementation choice and the lock order it owes is a `**Decisions.**` bullet; a fence for a session the pod holds no entry for is out of
  scope with its consequence carried by the fence-driver conflation entry; the fill-the-blanks headers are a `### Deferred` below. What
  survives is the `spec-changes.md` §7 half, which is its own `### Deferred`. `[non-spec.2.review-open-decisions.1]`, `[f1.cleanup]`
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
- **The barrier-ack sizing question is now the reviewer's, as OD15, with a recommendation and two bounding facts the standing item
  lacked.** The across-pods half is answered by shipped `spec/10:185`, which states the single wall-clock deadline as deliberate and
  explains why the gateway budget adds the term once rather than multiplying by session count. The same-pod half is not answered and the
  two sections do not meet: `spec/05:546` is normative that a concurrent-session pod's eviction checkpoints serialize across slots and that
  the pod budget is the sum of the per-session caps, while §10.1.8's justification for the one window is parallelism ACROSS pods, and the
  BarrierAck floor at `spec/10:140` carries no slot multiplier where the agent-pod floor at `:133` does. The case predates this proposal
  for co-tenant pairs whose generations coincide; D7 with CODE-1's per-slot gate widens it to every co-tenant pair. The standing "immaterial
  at Tier 3" reading is RETIRED: the post-barrier loop reaches every unacked session, and how many that is under a co-tenant slow upload is
  the unpriced quantity. `[non-spec.5.review-performance.1]`, `[spec.1.review-kubernetes.1]`, `[f1.human-decisions]`
- **UNVERIFIED: whether a Goals bullet may state a specified-but-unenforced effect.** "The drain checkpoint captures a stopped workspace"
  (`summary.md:130-131`, `:36-37`) is true in specification terms and false of the tree, because `quiesced` is advisory with no production
  reader under F-10.1.6. The "captured twice" half IS delivered, and the same gap makes the PRE-fix harm equally unrealised, so nothing
  regresses either way. `[non-spec.6.review-performance.1]`
- **OPEN: §8 pins nothing on the interaction between CODE-3's per-session generation read and a `CoordinatorFence` in flight for the same
  entry.** The hold's rejection allowlist admits `CoordinatorFence` (`holdstate.go:53-59`), so the two are genuinely concurrent, and
  `.claude/rules/test-coverage.md` requires a changed behaviour's concurrent path to be pinned. Not filed: the remedy is the one clause in
  CODE-3 that the `### Deferred` entry already carries. `[non-spec.6.review-performance.1]`
- **The SCHEMA-1 carrier-tail finding was FILED again in run 10 and is still unlanded,** at the new offsets `spec-changes.md:517-523`,
  `:542-547`, and `:559-566`; one preservation clause per paragraph fixes both the `CoordinatorFenceRequest` and the three
  `CheckpointBarrier` carriers. `[spec.1.review-edit-sites.1]`, `[spec.1.review-fresh.1]`
- **The unqualified barrier-acceptance clause was FILED again in run 10 with its full propagation.** The four staged sites stating the
  acceptance arm are `spec-changes.md:161-162` (§10.1.2 step 3), `:214` (§10.1.8 step 1), `:401` (§28.8 `CH-BARRIER`), and `:466` (§29.7);
  none carries a binding qualifier and only the proposal's own prose at `:140` does, so a fix must touch all four. A second refutation must
  say where in the APPLIED spec the binding restriction lives. `[spec.1.review-fresh.1]`
- **UNVERIFIED: whether OD5's cost list should gain the clause that SPEC-1 now states the no-edit-site ruling independently of the
  baseline.** The mirror-lag fix strengthened the OD5 gap; the fix that landed did not touch OD5. `[spec.2.fix-design-G1.1]`
- **OPEN: whoever settles the §29.10 quiescence-unit clause must say which object "the unit of the quiescence a barrier establishes"
  denotes.** §3's clause is about the barrier GATE (`barrierGate`, carrying `done`, `checkpointID`, and `signaled`) while SPEC-2's narrowed
  §29.10 bullet leaves the QUIESCENCE unit unanswered, and `coordinationState.quiesced` has no production reader. Those are two different
  objects, so the pair is not obviously a contradiction, which is why four rounds have now declined it. `[spec.2.review-edit-sites.2]`
- **OPEN: the `## 5. Proposed changes` header pair has deadlocked for four runs and a lens cannot settle it.** One archived refutation
  (greppable only from `review-log-archive.md:2376`, `:3371-3372`, and `:11118`) stands against four filings, and `[f1.cleanup]` has since
  dispositioned all three headers as coming out with a corrected ground. A human, or a fixer instructed to break the tie, should settle
  `spec-changes.md:107`, `:134`, and `non-spec-changes.md:6` in one move and record the refutation's ground where a grep finds it.
  `[spec.1.review-applicability.1]`, `[spec.1.review-fresh.1]`, `[f1.cleanup]`
- **OPEN: whether `pkg/gateway/sessionserver` placement can put a session back on the pod it unbound from is still untraced,** and the
  `[f1.*]` pass deliberately did NOT move it into the summary's defects section, because that section opens "Each was confirmed against the
  working tree" and this is exactly what nobody has confirmed. It bounds how often the rebind reset can be incurred and changes neither half
  of OD7's recommendation. `[f1.human-decisions]`
- **UNVERIFIED: the four lenses that have returned empty across three runs on unmoved text are still unretired.** Counted over this window:
  kubernetes three consecutive empties across runs 6, 8, and 10 on both lanes; security eight, six of them over text that moved only in
  non-security clauses; performance nine; reliability and docs-alignment repeatedly. Every reopening condition each named for itself is
  unfired. Each empty return costs the other twelve lenses a full round, and nobody has answered the question in six rounds of asking.
  `[spec.2.review-kubernetes.2]`, `[spec.2.review-security.1]`, `[spec.1.review-kubernetes.1]`, `[spec.1.review-performance.1]`

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
  §7, verified against all three cited ranges, and it is the one item of this class that IS landable in a spec-scoped round. UPDATED in
  pass 25: a run-9 firing did add a one-line SPEC-3 pointer naming OD5 at `spec-changes.md:592-594`, and it has since been removed —
  `grep -n OD5` over `spec-changes.md`, `non-spec-changes.md`, and `problem-statement.md` now returns nothing. The entry stands.
- DEFERRED [`spec-changes.md`, `:107` and `:134`; `non-spec-changes.md`, `:6`, from `[f1.cleanup]`]: the three
  `**IMPLEMENTOR TO FILL THE BLANKS.**` headers come out, and the correction they carry comes out with them. What is false: the ground an
  earlier draft gave for dropping them, that every item beneath is derived or settled. The second item under the §4 header is OD1's
  subject, which the summary declares open, and it carries the rationale OD1 corrects. What is true instead: they come out because a
  converged proposal's staged sections are verbatim staging, and a header calling them indicative targets tells a maintainer applying
  tomorrow not to apply them as written. This is the whole live disposition of the deadlock the `### Open` item records, and it settles all
  three headers in one move rather than letting each lane defer to the other's refutation.
- DEFERRED [`spec-changes.md`, §7 `Open decisions for review`, from `[f1.cleanup]`]: §7 still lists three reviewer decisions while the
  summary's `## Open decisions for human to make` is the canonical list. Item 1, the barrier gate's comparison, is OD1. Item 2, a fence for
  a session the pod holds no bound entry for, is out of scope for this proposal, with its consequence recorded under the defects section as
  one `FailedPrecondition` standing for three distinct refusals. Item 3, `coord.mu`, is an implementation choice rather than a reviewer
  decision, and the lock order it owes is already a `**Decisions.**` bullet. §4's detailed-design bullets duplicate items 2 and 3 verbatim,
  so the fix touches both sites. A spec-lane fixer with a write path closes it in one edit.
- DEFERRED [`non-spec-changes.md`, `## 9. Files touched on application`, from `[f1.cleanup]` and `[non-spec.5.review-client-surface.1]`]:
  `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` states the exemption as "The first fence on a pod's lifetime is always
  accepted", which SPEC-1's per-session rule falsifies and which is already false today for a non-positive generation and for an unbound
  session. It is the only production site of the exemption outside the proto, and §9 lists no path under
  `pkg/gateway/runtime/adapterclient/` beyond `checkpointbarrier_test.go`. Refuted class (k) bars filing it as a missed edit site while the
  proposal's own text says it should be fixed; nobody has ruled on that shape.
- DEFERRED [`non-spec-changes.md`, the §28.4 claim-register work, from `[f1.cleanup]`]: `tests/claim-map.json:75-82` already carries the
  `CheckpointBarrierRequest.coordination_generation` row as `UNWIRED` under deferral `R16`, and its note reads that "no production reader
  compares it until the generation fence lands", which is false against the adapter's current comparison at `pkg/adapter/coordination.go:236`.
  What is true instead: the correction is to restatus that row and replace its note. `CoordinatorFenceRequest` carries no row at all and is
  exempted from the tier-0 gate by name; whether that exemption survives SPEC-2's new sentences is the half OD11 leaves open. Remember that
  the file is generator output from root `gateway-runtime-comms.md` §7.1 and tier 0 byte-diffs it, so the edit site is the generator's
  source document plus a regeneration.
- DEFERRED [`0076...status.md`, from `[f1.cleanup]`]: the scope bullet states that one session's coordinator handoff "releases its
  coordinator-loss hold". D5 refutes that: the hold stays pod-scoped and a fence from any bound session is the correct exit from it, so the
  released-hold clause is the one item of that bullet's list this proposal does not repair. What is true instead: the hold is shared by the
  whole pod and a successful fence for any one of its sessions exits it. The second half of the older reading is FALSE and must not be
  re-derived: `status.md` is 33 lines and carries no closing paragraph framing the hold's scope as the open question this change answers
  (`:28-29` is the staging caveat and `:31-33` the review history), and the summary's errata paragraph that asserted it has since been
  deleted.
- DEFERRED [`docs/reference/adapter-contract.md`, `:69`, from `[spec.2.review-docs-alignment.1]`]: it states `CoordinatorFence` as the
  "precondition for any subsequent operational RPC". Under staged §10.1.2 step 3 the POD-side half of that is false for a session the pod
  holds no fenced generation for, and it is already false in the shipped tree, the adapter reading `coordination_generation` on the fence
  and barrier paths alone. What is true instead: the sender-side duty §10.1.2 step 2 states, that the ACQUIRING coordinator sends no
  operational RPC until its fence is acknowledged. The remedy is a docs edit no spec-scoped or non-spec-scoped loop here may make, and it
  has been derived and dropped three times on the ground that the clause restates step 2. Recorded so the docs loop can decide whether to
  qualify the row rather than re-derive the question a fourth time. `spec/04:712` carries the identical sentence and is already recorded as
  a non-site.

## Ledger

### [spec.2.review-edit-sites.1]
DECISION: returned EMPTY — BECAUSE `diff -ru scratchpad/cp-snap/0076-run10/spec-r2 <proposal> --exclude='*review-log*'` prints nothing, so the staged text is byte-identical to round 1's post-fix state, and none of the four reopening conditions this lens recorded for itself (SCHEMA-1's carrier list, §8's case set, §9's file list, SPEC-2's proto paragraphs) has moved — ALTERNATIVES: re-deriving the token inventories, rejected because the standing `### Settled` block already closes them and the log's own MISTAKE entry says an edit-sites lens that re-derives them has spent the pass on the wrong half.
FACT: only the review-log changed this round; the spec staging did not. One `diff -ru ... --exclude='*review-log*'` returning nothing prices the whole "what moved" question — EVIDENCE: scratchpad/cp-snap/0076-run10/spec-r2 vs proposals/0076_.../*.spec-changes.md (mtime 01:34, snapshot 01:35)
FACT: re-verified independently anyway, all clean: every §28/§29/§10 anchor SPEC-1 and SPEC-2 quote resolves verbatim (spec/28:330-331, :333-335, :1669-1685, :1806-1808; spec/29:1150-1152, :1261, :1274, :1309-1311, :1322-1326; spec/10:30, :40, :41, :58, :60, :183); the whole `coordination_generation` surface outside spec/10, /28, /29, /04 is unit-neutral (spec/07:93, :215, :398, spec/12:160, spec/16:199, :208, spec/18:238); `grep -rn "generation.*zero\|generation 0" spec/ docs/ schemas/ charts/` finds no baseline statement SPEC-3 falsifies; the proto's fourteen `coordination_generation` fields are exactly the twelve operational comments plus the fence request and the barrier request, matching SCHEMA-1's list.
USEFUL [standing ### Traps, "count the sentences in a §28.8 cell"]: it names the §28.8 `CH-FENCE` window clause as weighed-and-declined (the replacement is quoted at clause granularity, so the trailing ", and it is the one channel the adapter still accepts in hold state" survives). I re-derived that candidate from the file before reading the trap; the entry saved the write-up.
USEFUL [standing ### Traps, "the generic-RPC gate carries two operators across the APPLIED §28.6"]: the `older than` / `other than` asymmetry between staged §28.6 "One holder per session" and the staged second opener is pre-existing and reserved to §7's first open decision. Second candidate this lens generated and dropped on the trap.



### [spec.2.review-fresh.1]
DECISION: Returned an empty findings list for the fresh-holistic lens on run 10's spec round 2 — BECAUSE `diff -rq` shows the ONLY file that changed since the snapshot is `review-log.md`; `spec-changes.md`, `non-spec-changes.md`, and the checklist are byte-identical to the text three lens sweeps already cleared, and every candidate I worked up independently was already a standing `### Traps` entry, a refuted class, or a pre-existing tree defect — ALTERNATIVES: filing D7's "wrong refusal arm" imprecision (rationale, not applied spec, standing UNVERIFIED, two shards declined) and filing `spec/07:215`'s parenthetical as an unstaged site (rejected on the ground below).
FACT: `spec/07_session-lifecycle.md:215`'s §7.2 snapshot-close parenthetical ("any subsequent operational RPC carrying a lower `coordination_generation` is rejected") is NOT an unstaged edit site, and the reason is worth not re-deriving: the §7.2 bump is a Postgres-only terminal write with no `CoordinatorFence` to the pod, and `spec/10:38` states in terms that "the generation increment in Postgres alone does not close this window". The parenthetical is therefore already false in the shipped spec, independent of SPEC-1's unset arm, so it fails the "would BECOME wrong after the edits" test in (d). The review log carries this as an unverified `### Open` at the "whether `spec/07:215`'s parenthetical is reached by the staged unset arm" line; this shard answers it: it is not reached, because it never held. — EVIDENCE: spec/07_session-lifecycle.md:215, spec/10_gateway-internals.md:38
FACT: the closure grep for surfaces outside `spec/` is one call and returns almost nothing, so the "missed edit site" sub-lens is cheap to price. `grep -rln "coordination_generation" schemas/ charts/` returns `schemas/lenny-adapter.proto` alone, and `grep -rn "fenced\|coordination_generation\|generation stamp\|CoordinatorFence" docs/` returns only `docs/reference/adapter-contract.md:69`, `docs/getting-started/concepts.md:101`, and `docs/reference/metrics.md:310`, none of which states a pod-side compared value or a counter baseline. The staged carrier set (spec/04, spec/10, spec/28, spec/29, the proto) is closed. — EVIDENCE: docs/getting-started/concepts.md:101, docs/reference/adapter-contract.md:69
USEFUL [### Traps]: the entries "the `older than` versus `other than` operator asymmetry across applied §28.6", "`spec/04:712` is a fifth `spec/04` carrier", "§29.10's 'does not state' list already carries bullets that assert positive facts", and the three grep-shaped closures each killed a candidate I had already started working up. Reading `### Traps` before the proposal, rather than after, is what made this pass cheap.
WATCHOUT: the round's snapshot at `scratchpad/cp-snap/0076-run10/spec-r2` differs from the live directory in `review-log.md` ONLY. A lens that reads the "read the changed sections first and hardest" instruction literally will find nothing staged to read; the correct reading is that the staged text is unmoved and the whole document is the surface. Run `diff -rq` first and say so in the shard, so the next agent does not spend a pass hunting for a fix-stage hunk that does not exist.


### [spec.2.review-mechanism.2]

DECISION: returned an EMPTY findings list — BECAUSE `diff -ru scratchpad/cp-snap/0076-run10/spec-r2 <proposal dir>` shows only `review-log.md` changed, so `spec-changes.md` is byte-identical to the text run 10 round 1's mechanism shard worked over; its one finding (the §28.8 `CH-BARRIER` preservation clause) is FIXED and now reads clause-granular at `spec-changes.md:397-412`; and every mechanism candidate I re-derived cold resolved to an entry already adjudicated in `### Traps`, refuted by the material skeptic, or routed to a human open decision — ALTERNATIVES: seven declined, listed below.

FACT: the §28.8 `CH-BARRIER` fix landed and is correct. `spec-changes.md:397-412` now says "Only the trailing clause of the constraint sentence is replaced" and names what survives (the rest of the constraint sentence and the cell's second sentence), which matches `spec/28_communication-channels.md:1808` field 5 being exactly two sentences with the replaced clause as sentence 1's tail. Do not re-open it.

FACT: re-verified independently this pass that every anchor the staged text quotes resolves: `spec/10:30`, `:37`, `:38`, `:40`, `:41`, `:58`, `:60`, `:183`, `:184`, `:198`; `spec/28:237-240`, `:251-253`, `:291-296`, `:315-317` (the `CH-FENCE` Messages record-and-reject sentence, the ONLY hit in the file for "records the generation"), `:330-331`, `:333-336`, `:349-353`, `:361-365`, `:1669-1677`, `:1679-1681`, `:1683-1685`, `:1805`-`:1808`; `spec/29:1150-1152`, `:1186`, `:1259-1264`, `:1274`, `:1307-1313`, `:1322-1326`, `:1461-1470`, `:1519-1543`; `spec/04:200`. The "word for word" claim in the §28.8 `CH-FENCE` bullet is exact: `spec/28:333-336` and `:1807`'s gap sentence are the same string.

FACT: `migrations/0060_session_eviction_state_columns.up.sql:33` also declares `coordination_generation BIGINT NOT NULL DEFAULT 0`, but on `session_eviction_state`, not `sessions`, so D7's cite of `migrations/0050_session_record_fields.up.sql:38-39` for the session row's baseline is the right one and 0060 is not a competing declaration. Whether CODE-4 must also move that column's default is a CODE-lane question this loop cannot file; the 0060 header calls it "used for §10.1 / §7.2 coordinator fencing on resume". — EVIDENCE: migrations/0060_session_eviction_state_columns.up.sql:30-36, migrations/0050_session_record_fields.up.sql:33-40

FACT: `grep -rn "coordination_generation" docs/ charts/ schemas/*.json` returns exactly one hit, `docs/getting-started/concepts.md:101`, which is unit-neutral and states no baseline, so SPEC-3's baseline strands no docs mirror.

WATCHOUT: candidates I derived cold and killed — do not respend a pass on any of them. (1) The `older than` / `other than` operator split across applied §28.6 `:1675` vs `:1679` (declined, §7 OD1 owns the operator). (2) Staged §10.1.8 step 1 naming two sources for the barrier's generation on one physical line (declined in `[spec.2.review-mechanism.1]`; step 1's SQL is marked "of the form"). (3) The three gap-reset mirrors not carrying D6's unset initial condition (incompleteness, not contradiction). (4) `spec/10:30`'s "This prevents split-brain even under lease/lock race conditions" against the unset arm (the rejection there is conditional; the `### Traps` symmetry bullet forecloses it, and the §28.5.1 `CH-ATTACH` Preconditions non-site ruling treats the same sentence class the same way). (5) §29.8 step 7's staged reset changing its referent from "since that generation" to "since its last fenced coordinator" (wording, the two referents are coextensive). (6) The staged §10.1 sentence "every generation a pod validates is positive" against the barrier cache fallback's literal-0 seed at `cmd/lenny-gateway/httpsurface.go:588-602` (the `### Settled` bullet already records the outcome as fail-closed and the claim as inexact rather than wrong-making, and the staged rationale itself calls the adapter guard a backstop "for a value the gateway should never send"). (7) §4's "either order" claim (OD-owned).

UNVERIFIED (carried, unchanged, still nobody's): SPEC-1's rationale at `spec-changes.md:186-188` says the shipped barrier gate "refuses it on the absence of a recorded value for the named session (`:236-239`)". I confirmed in the tree that `coordinationState` is one pod-wide struct on `Server` with a single `lastFenced`/`initialized` pair (`pkg/adapter/coordination.go:25-39`) and the gate is `!initialized || gen != fenced` (`:236-239`), so "for the named session" imports a scope the shipped code does not have, and on a co-tenant pod that has already fenced a sibling the refusal comes from the `gen != fenced` arm — or the barrier is wrongly ACCEPTED when the sibling's fenced value happens to equal the carried one. I declined to file for the same reason two shards declined D7's "because the column is declared `NOT NULL DEFAULT 0`": the conclusion (the ordinary single-session pod's drain barrier meets two refusals today) is true, nothing staged rests on the clause, and the precedent puts a loose paraphrase in rationale prose below the bar. If a citations lens wants it, the fix is deleting "for the named session" from that clause. — EVIDENCE: pkg/adapter/coordination.go:25-39, :232-239; proposals/0076_.../0076_....spec-changes.md:186-188

USEFUL [`### Traps`, "diff -rq is the cheapest move"]: one call priced the whole reading order again; this is the fifth consecutive round where the changed-sections instruction had no target in the staged file.
USEFUL [`[spec.1.review-mechanism.1]`'s seven-candidate decline list]: it is what let this pass spend its budget on fresh surfaces (the migrations sweep, the docs/charts sweep, the `spec/04`/`spec/07`/`spec/16` "fenced" sweep) instead of re-deriving the same seven.

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

### [f1.human-decisions]

DECISION: OD2 (the pod's refusal of a fence carrying the generation it already recorded) stays with the
human and its entry stands. `summary.md` `## Open decisions` already carries exactly one OD2 entry stating
the question, the recommendation that a successor owns it with confidence moderate, the ground, the one
alternative that lost (a gateway-only fix), and the cost of each answer. Its substance was not rewritten.

DECISION: the identifier reference to OD2 in `spec-changes.md` is removed. The clause "Whether that equal
case should be accepted is the summary's open decision OD2 ... until that decision is answered" is replaced
with "No deliverable here changes what the handler does with a fence carrying the generation the pod already
holds, so the wire text keeps the comparison the shipped handler performs." The staged text now states the
requirement in its own terms and carries no trace of the question, which lives in the summary alone.

FACT: every citation in the OD2 entry was re-derived against the tree and holds. `spec/10_gateway-internals.md:39`
carries the same-generation retry sentence; `pkg/adapter/coordination.go:99` refuses `gen <= s.coord.lastFenced`
and `:102-106` returns the response body alongside the FailedPrecondition status;
`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56` drops that body;
`pkg/gateway/coordination/coordfence/coordfence.go:164-179` increments the stale counter at `:170` before it
discriminates and relinquishes at `:179`, and the zero-row floor is at `:147-153`; both metric names appear in
`pkg/observability/metrics/catalog.go:269` and `:274`; `BUILD-GAPS.md:1490` records F-4.7.2 as CLOSED.

FACT: one citation had drifted and is corrected in the entry. The `accepted` sentence of the
`CoordinatorFenceResponse` comment begins at `schemas/lenny-adapter.proto:1455`, not `:1456`, so the entry
now cites `:1455-1458`. `spec-changes.md`'s own `:1455-1462` span for the whole comment was already right.

WATCHOUT: the summary's section is headed `## Open decisions`, not `## Open decisions for human to make`.
The heading was left as it stands because every other entry lives under it and renaming it would land
ungated for the agents holding those entries.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.human-decisions]

DECISION: OD3 (whether `CoordinatorFenceRequest` becomes session-scoped in `spec/04` §4.1 after CODE-1, and
whether this proposal or a successor stages that edit) stays with the human and keeps its identifier. The
entry in `summary.md` `## Open decisions` already carried both questions, the recommendations (yes on A,
successor on B, each at moderate confidence), the ground, the alternative that lost, and the cost of each
other answer. Its substance was not rewritten.

DECISION: the one tightening the readings owed was landed. OD3's closing clause said the staged spec changes
file the 0075 interaction "under §6 as a rename whichever proposal lands second rebases onto, which
understates it", which left the reviewer answering against a staged sentence the entry itself called wrong.
The clause is deleted from `summary.md`, and `spec-changes.md` §6's non-goal no longer asserts the rebase:
"If both land, whichever is second rebases onto the first." is replaced with "This proposal stages no change
to that field's name or type, and no deliverable here depends on 0075 landing." The staged file states what
an implementor builds and carries no conditional on the open decision; §10's "It is independent of proposal
0075" agrees with it.

FACT: every citation in the OD3 entry was re-derived against the tree and holds. `spec/04_system-components.md:151`
is the declared-rather-than-derived paragraph, `:175` is the `CoordinatorFenceRequest` row with cell `pod`,
`:188` is the declaring sentence, and `:190` is the `ShutdownRequest` paragraph;
`schemas/lenny-adapter.proto:492-496` is `ReportPodScrubRequest` declaring `pod_id` alone;
`tests/tier0_static/adapter_proto_message_scope_test.go:75-79` is `declaredScope`, which accepts either word;
`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:38-44` is the comment excluding the
fence from the iterated map.

WATCHOUT: `spec/10_gateway-internals.md:57` and `:58` in OD3's alternative are correct as written. `:57` is the
hold-state-semantics bullet carrying "the only way to exit hold state" and `:58` is the hold-state-timeout
bullet carrying "terminates every session the adapter has started on that pod". The standing context's note
that a hold citation is one line off refers to the allowlist citation elsewhere and does not apply here; a
mechanical decrement of these two would break them.

WATCHOUT: `spec-changes.md` `## 7. Open decisions for review` still carries three numbered items. None of them
is OD3, and they were left untouched for the agents holding them.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable, and
touches no implementation-checklist step.

### [f1.human-decisions]

DECISION: OD5 (whether D7, the session-row counter baseline, and migration 0181 land here or move together
into a successor) stays with the human and keeps its identifier. The entry at `summary.md` `## Open decisions`
already carried the question, the recommendation with its confidence (land here, moderate), the forcing chain
as its ground, two weighed alternatives with why each lost, the price of each answer, and what follows the
answer, so its substance was not rewritten. One sentence pair was added to its cost paragraph.

DECISION: the cost paragraph now states the deploy state the "production migration" price rests on. Added
after "which no artifact in the tree spends on their behalf": the platform is recorded as pre-deployment with
no deployments in the wild (`.claude/rules/code-best-practices.md:62`), so landing here costs the reviewer's
attention to a schema change and two create paths rather than exposure of a live fleet; and that reading is
unsettled, because OD9 prices the retained `>= 1` check against a still-running old fleet inserting an
explicit zero during the roll (`summary.md`, OD9, the rolling-window paragraph). A reviewer who reads the
entry without this weighs one side at a price the repository's own rule contradicts.

FACT: every citation in the OD5 entry was re-derived against the tree and holds.
`pkg/adapter/coordination.go:224-226` refuses a non-positive `coordination_generation` with `InvalidArgument`
before the gate at `:236-239`; `charts/lenny/templates/migrate-job.yaml:10-16` is the `pre-install/pre-upgrade`
hook at weight -5 that completes before the gateway Deployment;
`migrations/0180_drop_checkpoint_slot_id.{up,down}.sql` is the last number taken and no 0181 exists, so the
number is free under either answer; proposal 0049 names `migrations/0179_sessions_credential_deny.{up,down}.sql`
in its own scope paragraph (`proposals/0049_fix_persist-a-deny-credential-propagation-marker-and-suppress-ll.md:5`,
with the deliverable at `:88-110`); `.claude/rules/spec-driven-development.md:23` carries the quoted
spec-ahead-of-code sentence verbatim; `non-spec-changes.md:335-367` is the test-amendment inventory the entry
cites for the successor's residual work.

FACT: the pre-deployment rule is at `.claude/rules/code-best-practices.md:62`, not `:61`. The gate's note cited
`:61`; the sentence beginning "No backward-compatibility shims" is on `:62`.

WATCHOUT: no OD5 reference remains in a staged change file. An earlier run recorded adding a one-line pointer
at the end of SPEC-3 naming OD5; `grep -n OD5` over `spec-changes.md`, `non-spec-changes.md`, and
`problem-statement.md` now returns nothing, so it has since been removed and nothing was migrated this firing.
`spec-changes.md` `## 7. Open decisions for review` lists three items and none is OD5; it was left as it stands
because its items are other agents' subjects.

WATCHOUT: OD5 and OD14 are coupled in one direction only. A "split" moves OD14's subject into the successor
without answering it. Do not record OD14 as closed by an OD5 answer.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable, and
touches no implementation-checklist step.

### [f1.human-decisions]

DECISION: OD7 (rebind and the unset state) stays with the human and keeps its identifier. The summary's
`## Open decisions` already carried the entry in the form this phase requires: both halves of the question
stated so a reader can answer them without the proposal, the recommendation (accept the reset, take the
binding form), the ground for each half, the three alternatives with why each lost, the cost of each answer,
and a split confidence (moderate on the first half, high on the second). No answer was staged and no staged
change file was touched.

FACT: every code and specification citation in OD7 was re-verified against the tree and holds:
`pkg/adapter/session.go:238` and `pkg/adapter/slotsession.go:361` both call `deregisterSlotLocked`, whose
`delete(s.slots, sessionID)` is at `pkg/adapter/slotsession.go:179` inside the `:174-188` span;
`releaseSessionSlot` reaches it at `:214-215`; `s.coord.lastFenced` is written only on the accepted-fence
path at `pkg/adapter/coordination.go:120-121`; the three readers are `:99`, `:108`, the barrier gate at
`:233-239`, and `pkg/adapter/holdstate.go:119`; `ensureSlotStateLocked` spans `pkg/adapter/slot.go:82-101`;
the resume path's fence sites are `pkg/gateway/sessionserver/start.go:3975`, `:4067`, and `:4233`;
`spec/07_session-lifecycle.md:196` fixes `resuming → running` as a re-attach on a replacement pod; and
`spec/10_gateway-internals.md:40` carries the gap bullet with no lifetime clause.

FACT: three self-referential citations inside OD7 had drifted against the summary's own fixed-decision list
and were corrected in place. D6's closing clause is at `summary.md:60-61` rather than `:56-59`, D2 is at
`:49-50` rather than `:47-48`, and D6's first sentence is at `:58` rather than `:56`. `spec-changes.md:142`
(SPEC-1's "unset until that session's first accepted fence on that pod") and `spec-changes.md:33` (D6's first
sentence) both still resolve and were left as written.

FACT: OD7 appears in no staged change file. `spec-changes.md` `## 7. Open decisions for review` lists three
items and none is OD7, so nothing was migrated out of a staged file for this item; those items belong to other
agents.

WATCHOUT: OD7's second half is an alignment rather than a reopening. D6 sits under "Fixed decisions. These
are closed." and its own closing clause already states the binding form, while SPEC-1 at `spec-changes.md:142`
and D6's first sentence at `spec-changes.md:33` and `summary.md:58` state the other. Answering the second half
makes D6 state one form; it does not reopen D6.

OPEN: whether `pkg/gateway/sessionserver` placement can put a session back on a pod it unbound from is still
untraced. The entry records it as unverified and neither recommendation waits on it, because the binding form
holds under both answers. It bounds how often the reset can be incurred.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable, and
touches no implementation-checklist step.

### [f1.human-decisions]

Item OD12, disposition `human`. The entry was rewritten in place in `summary.md` (`## Open decisions`, entry
OD12, replacing the seven-line "No recommendation was derived" paragraph), keeping the identifier verbatim.
It now states the question so a person can answer it without reading the proposal, carries a recommendation
with a confidence, names three alternatives with why each lost, and states what deciding otherwise costs.
No migration was owed: OD12 appears in no staged change file.

DECISION: the entry recommends accepting the behaviour and the staged sentence, at moderate confidence,
because the acceptance arm is a normative claim no shipped sentence makes even though the behaviour it
describes is shipped.
FACT: the pod's barrier gate carries no coordination-status term before or after D7 (`pkg/adapter/coordination.go:236`,
`!initialized || gen != fenced`), and D7's new unset arm is unreachable for a superseded replica, because
being superseded means a successor's fence was recorded and the pod therefore holds a value.
FACT: the gateway-driven `Checkpoint` stream is NOT consequent on barrier acceptance.
`pkg/gateway/coordination/barrier/barrier.go:220-227` starts it in a goroutine before `c.dispatch.Send` and
joins it after, unconditionally for every target, and `:228` records its error without un-acking. The entry's
old second question, whether an accepted false-positive barrier is followed by a stream at all, was answered
by the tree the other way round and is now stated as such.
FACT: the adapter starts no timer on the quiesced hold (`pkg/adapter/coordination.go:264-268` waits on the
stream or on the RPC context), so the only reclaimer is the gateway's RPC deadline set from
`checkpointBarrierAckTimeoutSeconds` at `pkg/gateway/podlifecycle/prestop/prestop.go:503`, one wall-clock
window across all of a replica's pods (`spec/10_gateway-internals.md:138`).
WATCHOUT: shipped `spec/10_gateway-internals.md:183` asserts safety of the REJECTION outcome alone. The staged
§10.1.8 step 1 rewrite's "Either outcome is safe and requires no special handling"
(`spec-changes.md:216-217`) is a new normative claim over the acceptance arm, which is what the reviewer's
answer ratifies. A later reading that treats it as pre-existing text will mis-price the entry.
WATCHOUT: `spec/10_gateway-internals.md:41` (§10.1.2 step 3) asserts there is "no window in which both the
old and new coordinator can simultaneously issue accepted RPCs to the pod". The accepted false-positive
barrier is such a window, because the barrier's generation is read at target assembly rather than stamped by
the sender. That sentence is not staged for edit here and no entry names the tension.

No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.human-decisions]

DECISION: OD14 stays the human's and is rewritten in place in `summary.md` (identifier kept verbatim),
replacing the former two-question, fifty-line entry at lines 609-665. It now leads with the OD5 coupling,
puts one answerable question ("accept the unbatched form and the absent budget, or commission a successor
that writes a budget for the migrate Job?"), states that correctness is settled so the reviewer ranks
upgrade duration against scope, then carries the ground, the three options with their costs, no
recommendation, and confidence high in the framing.
FACT: both migration runners build the driver as `migratepg.WithInstance(db, &migratepg.Config{})`
(`cmd/lenny-migrate/main.go:226`, `pkg/schemamigrate/schemamigrate.go:382`), so multi-statement splitting is
off and a migration file executes as one statement batch. Batching inside 0181 would commit nothing between
batches and release no locks; shortening the lock hold means splitting 0181 across migration files or
changing the runner. This is written into the entry as ground on the batching half and does not answer it.
FACT: re-verified for the rewritten entry: `charts/lenny/templates/migrate-job.yaml:42` renders
`backoffLimit` and `:45` `ttlSecondsAfterFinished: 600` with no `activeDeadlineSeconds`;
`charts/lenny/values.yaml:3791` is `backoffLimit: 5`; `preflight-job.yaml:259` and `crd-validate-job.yaml:81`
render the deadline from `preflight.timeoutSeconds` and `backup-job.yaml:134` renders 7200, with
`spec/17_deployment-topology.md:571` and `spec/25_agent-operability.md:3995` the only two spec numbers;
`spec/10_gateway-internals.md:434` is the Phase 3 gate's `COUNT(*)` rule; `docs/operator-guide/upgrades.md:22`
is `--wait --timeout 10m` inside an example command; the clamp citations `pgstore.go:460`, `:475-477` and
`memstore.go:129`, `:144-146` all resolve.
FACT: neither staged change file carries an OD14 entry or a reference to it, so nothing was migrated out of
`## Open decisions for review` in `spec-changes.md` and that section is left as it stands for its own items.
Reconciled the one statement the rewrite falsified: OD5's "What follows the answer" paragraph now restates
OD14 by its new title.
No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.human-decisions]

DECISION: OD15 stays `human` and keeps its identifier. The entry already carried the question, the
recommendation (accept the residual and record it), the ground, the three alternatives, the cost of the
other answer, and a confidence, so it was not rewritten. Two bounding facts the gate's falsifier raised and
the entry did not weigh were added to it in `summary.md`, `## Open decisions`, entry OD15.
FACT: the shipped specification designs the missed-window outcome rather than leaving it undefined. A
session that does not ack within `checkpointBarrierAckTimeoutSeconds` enters the BarrierAck-timeout
partial-capture path (CPS-007), which finalises its partial-manifest intent row and preserves every
committed chunk, and falls back to the last periodic checkpoint only where no intent-row state exists
(`spec/10_gateway-internals.md:187`, `:198`). Added to OD15's "The residual is bounded" paragraph, which
previously rested on the `prestop.go` fall-through alone.
FACT: the co-tenant-inside-one-unmultiplied-window case predates this proposal. `coordinationState` is
pod-wide (`pkg/adapter/coordination.go:25-32`), the barrier gate compares against that pod-wide value
(`pkg/adapter/coordination.go:236`), and co-tenant checkpoints already serialize on the pod's op lock
(`pkg/adapter/checkpoint.go:111`), so two co-tenant sessions whose generations coincide already enter one
90-second window today. D7 with CODE-1's per-slot gate widens the affected set from that class to every
co-tenant pair. Added as a closing paragraph to OD15.
FACT: every citation the entry already carried was re-verified and all resolve: the BarrierAck floor at
`spec/10_gateway-internals.md:140`, the across-pods single-window justification at `:185`, §5.2's
serialization and sum-of-caps rules at `spec/05_runtime-registry-and-pool-model.md:546`, and the §17.8.2
burst derivation at `spec/17_deployment-topology.md:1218`.
FACT: no migration was owed. `spec-changes.md`'s `## 7. Open decisions for review` carries three items, none
of them OD15, and neither staged change file mentions the BarrierAck floor as a decision or names OD15.
FACT: the problem statement needs no correction for this item. Its `spec/10_gateway-internals.md:190`
citation for the intent-row finalisation with `manifest_reason = "timeout"` resolves to rule 2 of the
CPS-007 path, which is the rule it describes.
No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.other-proposals] Proposal 0080's row

DECISION: the 0080 row in the summary's impact table is kept and sharpened, so each invalidated entry is
named by its own section number and the reason is the mechanism rather than the conclusion. Written at
`summary.md`, `**Impact on other proposals.**` table, the 0080 row.
FACT: the row now names §1.14 (`0080...:184-192`), the shipped bullet it records
(`spec/29_communication-scenarios.md:1523-1527`), the staging that removes it
(`0076...spec-changes.md:439-442`), and §2's misattribution (`0080...:211`, restated at `:191-192`), and it
states that §1.12's claim-register rows and §1.13's §29.10 spec-map exception are untouched and that
§29.10's `Interrupt`-and-barrier bullet is narrowed rather than removed.
WATCHOUT: the reading behind this item cited the staged §29.10 removal as `spec-changes.md:443-446`. That
range is the following bullet, which gains the pod-side generation rule. The removal bullet is `:439-442`.
The row carries the corrected range.
FACT: the duplicate 0080 bullet under `### Cross-proposal consequences` (four lines asserting the same two
invalidations) is deleted, because the impact table is the only place this proposal states another
proposal's continued validity and the table's own opening sentence says so. The 0075 bullet in that section
is left alone; it belongs to another item.
FACT: 0080's status and date check out. Its own front matter carries `EARLY DRAFT, NOT CONVERGED` and an
inventory-rather-than-design self-description at `:3-6`, its `**Date:** 2026-08-31` at `:7`, and
`git log -1` on the file returns the same date.
FACT: the problem statement needs no correction for this item; it makes no claim about proposal 0080.
No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.other-proposals]

DECISION: the impacts table's 0073 row is withdrawn. 0073 is Implemented, so nothing staged can invalidate
it, and the row asserted no effect on its validity: it restated the dependency already carried by `## 10.
Dependencies` (`spec-changes.md:625-627`) and by D4 (`summary.md:52-54`), plus the project-wide rule that a
landed proposal is not edited.
FACT: verified before withdrawing. 0073's header reads Implemented (2026-08-31) at
`proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:3`; `checkSessionBound` is live at
`pkg/adapter/slotsession.go:267` and is called at `pkg/adapter/coordination.go:89` and `:216`; and a grep
for `proposals/` across both staged-change files returns no path under `proposals/`, so no deliverable
edits a file 0073 owns.
WATCHOUT: withdrawing the row alone would have left the table's opening claim, that it is the only place
this proposal states anything about another proposal's continued validity, contradicted by the sentence
"Proposal 0073 is converged and is not reopened; this proposal sequences after it." in the hold-scope
paragraph above it. That clause was deleted and its content moved into a "checked and unaffected" note
under the table, which names 0073, its status and date, what this change builds on, that no staged
deliverable edits a file under its directory, and where the sequencing is stated.
FACT: the problem statement needs no correction for this item. It names 0073 four times (`:92`, `:99`,
`:102`, `:107`), each as the history of the guard rather than as a claim about 0073's continued validity,
and each checks out against `pkg/adapter/slotsession.go:267`.
No `DEFERRED` line is owed: the edit adds, removes, merges, splits, and resequences no staged deliverable,
and touches no implementation-checklist step.

### [f1.cleanup]

DECISION: `summary.md` is rewritten to this phase's section list. `## Summary` now holds
`**Problem statement.**` (renamed in place from `**What is fixed.**` and moved above `**What changes.**`),
`**What changes.**`, `**Decisions.**` (renamed in place from `**Fixed decisions.**`), and
`**Watch out for.**`. The file then runs `## Goals`, `## Non-goals`,
`## Open decisions for human to make` (renamed from `## Open decisions`),
`## Defects in the shipped tree that this proposal does not stage` (promoted from `###`, its entries
byte-identical), `## Impacts on other proposals` (promoted out of the summary container from the
`**Impact on other proposals.**` part), and `## Deliverable index`, which is preserved line for line in
last position.
DECISION: OD4, OD6, OD8, and OD10 leave `## Open decisions for human to make`, because that section
carries the decisions still open for the human and only those, and each of the four had already been
restated in place as withdrawn or replaced. The preamble sentence that fixed restate-in-place as the only
exit is replaced by one stating that a withdrawn or replaced entry leaves the section, that its record is
kept here, and that the identifier it held is not reused. OD1, OD2, OD3, OD5, OD7, OD9, OD11, OD12, OD13,
OD14, and OD15 keep their identifiers and their text.
WATCHOUT: the identifiers OD4, OD6, OD8, and OD10 are retired. Do not reuse one for a new entry, and do
not re-derive the four as open decisions: each is recorded below with the disposition it left with.
FACT: OD4 was withdrawn as mis-stated. It asked whether to state the barrier quiescence unit or to delete
a design claim. The design's claim is that §10.1.8 step 3 fixes the gate's unit at the session, which is
about the `barrierGate` rather than the `quiesced` flag; that claim is true and CODE-1 depends on it, and
SPEC-2 already stages the narrowing that leaves §29.10's clause unanswered. The strongest ground for not
stating the unit is that the only quiescence primitive the specification defines, §28's
`checkpoint_request` frame, carries no session identifier and is delivered to the pod's shared runtime
process. The one live remedy the entry produced, that relocating `quiesced` onto the entry carries no
specification claim about the quiescence unit, is a bullet under `**Decisions.**` and stays.
FACT: OD6 was replaced. Its central claim, that the pod-wide field is an accidental mutual exclusion
holding a pod to one coordinating replica, was refuted: the predicate at `pkg/adapter/coordination.go:99`
compares generations and carries no replica term, so it is a cross-session interlock, and two replicas
coordinating co-tenant sessions on one pod is reachable in the shipped tree. Its replacement
recommendation, to keep the co-tenancy question a non-goal and to record that this change removes a
cross-session interlock that delayed and mis-metered co-tenant handoffs whatever the number of replicas
involved, is the `## Non-goals` bullet on co-tenancy, which stays. That bullet's trailing `(OD6)` pointer
is dropped with the entry.
FACT: OD8 was withdrawn. It asked whether CODE-4 deletes the gateway fence path's floor of a non-positive
row value at `pkg/gateway/coordination/coordfence/coordfence.go:147-153`. The ground for deleting it, that
a session row can no longer carry a non-positive value, is false for the rolling window, and CODE-4 now
states why the floor is kept. The coupling the entry recorded, that the floor is retired by the release
tightening the check to `>= 1`, survives inside OD9, whose sentence naming it now reads "which the
withdrawn OD8 also recorded".
FACT: OD10 was withdrawn. It asked whether the sentences calling a barrier's generation the current one
are edit sites, and it named the wrong deliverable; SPEC-1 owns `spec/10_gateway-internals.md` and states
the ruling, on the barrier-target mirror lag rather than on the counter baseline. That lag is recorded
under `## Defects in the shipped tree that this proposal does not stage`. OD5's sentence carrying OD10
forward now reads "The withdrawal of OD10, which the review log records, does not rest on the baseline, so
it stands under either answer".
FACT: the `### Items §7 lists, and how they should be dispositioned` block is dropped, and each of its
three items is accounted for. `coord.mu` is not the human's: the summary dispositions it as an
implementation choice with no external consequence, and the lock order it owes, the registry lock then the
entry lock then the hold lock, is a bullet under `**Decisions.**`. A fence for a session the pod holds no
entry for is not the human's either: it is out of scope here, and the consequence it names, one
`FailedPrecondition` standing for three distinct refusals, is the fence-driver conflation entry under the
defects section. The `IMPLEMENTOR TO FILL THE BLANKS` headers are an instruction to the two staged-change
files, which this pass may not edit, so they are `DEFERRED` below.
FACT: the `### Cross-proposal consequences` block is dropped. Its one remaining bullet said that this
change removes proposal 0075's stated justification rather than merely conflicting with its rename, which
the 0075 row of the impacts table already carries in the same terms. The table is the only place this
proposal states anything about another proposal's continued validity, so the bullet was a second site for
one claim and the two did not disagree.
FACT: the `### Corrections outstanding in the proposal` block is dropped and its four bullets are
dispositioned. Three are corrections owed to files this pass may not edit and are `DEFERRED` below. The
fourth, that §7's heading numbering jumps from 7 to 10 because §8 and §9 live in the non-spec changes, is
an artifact of the folder layout rather than a defect and owes nothing.
FACT: the `**Watch out for.**` paragraph reporting a deliverable-side correction outstanding in the status
file is dropped from the summary and is `DEFERRED` below, so the correction is recorded once against the
file that owes it rather than as errata inside the summary.
FACT: line offsets after the rewrite. `summary.md` runs 826 lines against 934. Lines 1 to 4 and 45 to 73
are unmoved, so OD7's citations `summary.md:49-50`, `:58`, and `:60-61` into the D-list still resolve.
`## Open decisions for human to make` opens at `:153`, was `:177`, and OD1 through OD3 sit 24 lines above
where they were; OD3's self-citation was corrected from `:283-289` to `:259-265`. The defects section
opens at `:663`, the impacts section at `:796`, and the deliverable index at `:814`.
DEFERRED [`0076...status.md`]: the scope bullet states that one session's coordinator handoff "releases
its coordinator-loss hold". D5 refutes that: the hold stays pod-scoped and a fence from any bound session
is the correct exit from it, so the released-hold clause is the one item of that bullet's list this
proposal does not repair. The earlier draft also named a closing paragraph framing the hold's scope as the
open question this change answers; no such paragraph exists in the file, so nothing is owed for it.
DEFERRED [`0076...spec-changes.md`, `:107` and `:134`; `0076...non-spec-changes.md`, `:6`]: the three
`**IMPLEMENTOR TO FILL THE BLANKS.**` headers come out, and the correction they carry comes out with them.
The ground the earlier draft gave for dropping them, that every item beneath is derived or settled, is
false: the second item under the §4 header is OD1's subject, which the summary declares open, and it
carries the rationale OD1 corrects.
DEFERRED [`0076...spec-changes.md`, §7 `Open decisions for review`]: §7 still lists three reviewer
decisions while the summary's `## Open decisions for human to make` is the canonical list. Item 1, the
barrier gate's comparison, is OD1. Item 2, a fence for a session the pod holds no bound entry for, is out
of scope for this proposal, with its consequence recorded under the defects section. Item 3, `coord.mu`,
is an implementation choice rather than a reviewer decision.
DEFERRED [`0076...non-spec-changes.md`, `## 9. Files touched on application`]:
`pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` states the exemption as "The first fence on a
pod's lifetime is always accepted", which SPEC-1's per-session rule falsifies and which is already false
today for a non-positive generation and for an unbound session. It is the only production site of the
exemption outside the proto, and §9 lists no path under `pkg/gateway/runtime/adapterclient/` beyond
`checkpointbarrier_test.go`.
DEFERRED [`0076...non-spec-changes.md`, the §28.4 claim-register work]: `tests/claim-map.json:75-82`
already carries the `CheckpointBarrierRequest.coordination_generation` row as `UNWIRED` under deferral
`R16`, so the correction is to restatus that row and to replace its note, which reads that "no production
reader compares it until the generation fence lands" and is false against the adapter's current
comparison at `pkg/adapter/coordination.go:236`. `CoordinatorFenceRequest` carries no row at all and is
exempted from the tier-0 gate by name; whether that exemption survives SPEC-2's new sentences is the half
OD11 leaves open.
OPEN: nothing. Every block the section list does not name reached one of the destinations above.


## Index and checklist reconciliation, 2026-09-05

Reconciled `## Deliverable index`, the checklist's spec lane, and the non-spec steps' `Depends on:` against
the staging as it stands after run 10's spec loop, which ended with findings still open. The staged set is
unchanged from the 2026-09-04 reconciliation: SPEC-1, SPEC-2, SPEC-3, SCHEMA-1, CODE-1, CODE-2, CODE-3,
CODE-4, and TEST-1, each carried once in the index with the file it lands in and once in the checklist, and
the index carries no deliverable the staging does not stage. The staging edits since that pass are the
narrowed §28.8 `CH-BARRIER` replacement clause in SPEC-2, the removal of the OD2 pointer from SPEC-2's
`CoordinatorFenceResponse` paragraph, and the rewritten 0075 non-goal. None adds, removes, retargets,
merges, or splits a deliverable, so no index row changed and no checklist step was added, removed,
resequenced, or re-tiered.

The spec lane remains the leading block S1 (SPEC-1, `spec/10_gateway-internals.md`), S2 (SPEC-2,
`spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`), and S3 (SPEC-3,
`spec/04_system-components.md`), in the order the spec edits must be applied, and the non-spec steps keep S4
(SCHEMA-1), S5 (CODE-1 and CODE-3), S6 (CODE-4), S7 (CODE-2), and S8 (TEST-1) with their tiers. One lane per
step throughout. Each `Depends on:` was re-read against the current SPEC ids and still names the spec steps
staging the statements its work implements: S4 on S2, S5 on S1, S6 on S1 and S3, S7 on S1, S5, and S6, and
S8 on S5, S6, and S7. No step interleaves ahead of a remaining spec step. Every box is unchecked.

CORRECTS: none. Every `DEFERRED` entry in this log whose remedy is a repair landing in one of the four files
this pass may edit is already closed, and each was re-checked against the current text this pass. The
`summary.md` "Resolved in adversarial review" pointer is gone from the file, as is the `**Watch out for.**`
clause reporting the status file's closing paragraph, which the `[f1.cleanup]` phase dropped and which the
2026-09-04 reconciliation had carried as `OPEN`; neither is owed further. The `tests/claim-map.json` entry's
status half stays settled and must not be revived, and its statement-level half is with the reviewer as
OD11. No statement in the summary, the checklist, or the non-spec changes was found false against the
staging as it now stands.

OPEN: the three `**IMPLEMENTOR TO FILL THE BLANKS.**` headers at `spec-changes.md:107` and `:134` and
`non-spec-changes.md:6`. The `[f1.cleanup]` disposition, that all three come out, is one instruction across
three sites, two of them in `spec-changes.md`, which this pass may not edit; applying it to the third alone
would leave the two staged-change files describing themselves differently. The header's own condition is
also unmet, this run's spec loop having ended with findings open, so the ground the disposition rests on
does not yet hold. Carried to the reviewer as OD16, which states the disposition, the standing refutation
against it, and the convergence fact, and offers no recommendation.

OPEN: SPEC-1's two "column default and the create path's floor" attributions, at the counter-baseline
sentence and at the §10.1.4 zero-invariant sentence, credit the baseline to the column default while
`non-spec-changes.md` states that `Create` names `coordination_generation` in its insert column list, so the
default baselines nothing and the two `Create` floors are the whole enforcement. The sites are not false,
since CODE-4 does land the default, but the attribution overstates it. The remedy lands in
`spec-changes.md`, which this pass may not edit. Carried forward for the fourth pass running.

OPEN: `spec-changes.md` §7 "Open decisions for review" still presents three live reviewer decisions while
the summary dispositions item 3 (`coord.mu`) as an implementation choice and item 2 (a fence for an unheld
session) as out of scope with a named consequence, and §4's detailed-design bullets duplicate items 2 and 3
verbatim. The remedy lands in `spec-changes.md`, which this pass may not edit.

OPEN: `spec-changes.md` §7 does not list OD5, the open decision with the largest effect on the staged spec
text, so a reviewer reading only the staged spec file sees no sign that SPEC-3 and SPEC-1's §10.1 baseline
paragraph are conditional. The remedy is one bullet in `spec-changes.md`, which this pass may not edit.

OPEN: four `pkg/adapter` doc comments go false when CODE-3 lands and none is in CODE-3's enumeration of the
sites the `gen` field drags: `holdstate.go:116-118`, `coordination.go:126-128`, `holdstate.go:103`, and
`holdstate.go:153-155`. CODE-3's "Those sites are the whole of what the field drags" is false against all
four. The remedy is a staged code edit naming the four comments, which lands in `non-spec-changes.md`
CODE-3 as content no non-spec lens has read, so this pass carries it rather than writing it.

OPEN: CODE-3's staged sentence has `terminateHeldSession` and `writeHoldPostMortem` read each terminated
session's last fenced generation off the detached `*slotState` and states no synchronisation, while that
value lives inside `coordinationState`, whose every other reader takes its `mu`. The window is verified: a
`CoordinatorFence` admitted by the hold's allowlist can write `lastFenced` on the same entry while pass 2
reads it for the post-mortem, and the accessor cannot be used because pass 1 has already deregistered the
session. The remedy is one clause requiring the locked read, which lands in `non-spec-changes.md` CODE-3 as
staged code content no non-spec lens has read.

OPEN: `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` states the first-fence exemption as "The
first fence on a pod's lifetime is always accepted", which SPEC-1's per-session rule falsifies and which is
already false today for a non-positive generation and for an unbound session. It is the only production
site of the exemption outside the proto, and `## 9. Files touched on application` lists no path under
`pkg/gateway/runtime/adapterclient/` beyond `checkpointbarrier_test.go`. The remedy is a staged code edit
and a §9 entry in `non-spec-changes.md`, which no non-spec lens has read.

OPEN: `tests/claim-map.json:75-82` carries the `CheckpointBarrierRequest.coordination_generation` row as
`UNWIRED` under deferral `R16` with a note that is false against the adapter's current comparison at
`pkg/adapter/coordination.go:236`, and `CoordinatorFenceRequest` carries no row and is exempted from the
tier-0 gate by name. The file is generator output from root `gateway-runtime-comms.md` §7.1 and tier 0
byte-diffs it, so the remedy is a staged deliverable editing the generator's source and regenerating, which
lands in `non-spec-changes.md` and which no lane has authored. The statement-level half of whether §28.4
obliges it at all is with the reviewer as OD11.

OPEN: `docs/reference/adapter-contract.md:69` states `CoordinatorFence` as the "precondition for any
subsequent operational RPC". The pod-side half is false under staged §10.1.2 step 3 for a session the pod
holds no fenced generation for, and it is already false in the shipped tree. The remedy is a docs edit,
which lands outside this proposal's staged set and outside the files this pass may edit.

OPEN: the barrier's cache-fallback closure at `cmd/lenny-gateway/httpsurface.go:588-602` calls
`w.sessions.Get(context.Background(), ...)` once per binding with no deadline, on the path taken when the
mirror read has already failed, so a hung Postgres burns the whole preStop grace with no drain. This is
pre-existing shipped code that OD1 cites and that no deliverable here touches. It is routed to a
gateway-side fix rather than to this proposal.

OPEN: `0076...status.md`'s scope bullet states that one session's coordinator handoff "releases its
coordinator-loss hold", which D5 refutes: the hold stays pod-scoped and a fence from any bound session is
the correct exit from it. The remedy lands in the status file, which this pass may not edit.

### [f2.out-of-scope-defects]

DECISION: the §10.1.2 gap-path cancel-and-reset stays unimplemented and this proposal stages no fix.
Written into `summary.md` `## Defects in the shipped tree that this proposal does not stage` as a new
final bullet, "The adapter does not perform the §10.1.2 cancel-and-reset on a generation gap". No open
decision was opened and no deliverable was added, so the deliverable set and the checklist are unchanged.
FACT (verified this run against the working tree): the gap branch logs, sets `GapDetected`, and records
the new value on the shared path (`pkg/adapter/coordination.go:108-121`); the concession is in the code
twice, at `:80-81` and `:112-113`; the absence is registered as `"In-flight RPC cancellation on a
generation gap"`, status `ABSENT`, deferral `R16` (`tests/claim-map.json:173-178`).
FACT: the register row's `surface` reads `coordination.go:81-82` while the concession it names sits at
`:80-81`; line 82 is the blank comment line. The row is unchanged by this pass, and the drift is recorded
in the summary bullet.
FACT: SCHEMA-1's wire lane, not SPEC-1 alone, carries the compliance assertion. `schemas/lenny-adapter.proto:158`
states the adapter cancels and discards every in-flight RPC and resets transient tool-call state, and
`:1458-1462` defines `gap_detected` as reporting that it "reset transient tool-call state per §10.1"; the
staged text re-scopes both to the session rather than deleting them (`spec-changes.md:523-524`, `:535-537`).
Both sentences are false about the tree before and after this change. The summary bullet says so.
FACT: the problem statement's citations for this defect (`problem-statement.md:70-74`, naming
`coordination.go:112-113` and `:119-121`) resolve against the tree as written, so no correction was owed
there and none was made.
WATCHOUT: CODE-1 rewrites the `CoordinatorFence` doc comment that holds the `:80-81` concession and the
claim-register row's `surface` pointer. The concession sentences must survive that rewrite and the pointer
must be re-resolved when CODE-1 lands.

### [f2.other-proposals-0075]

DECISION: the `## Impacts on other proposals` row for proposal 0075 stays and is tightened. It now carries
0075's status date, the deliverable split, and the anchor drift, at `summary.md:862`. No other file changed,
no open decision was opened or withdrawn, and no deliverable moved, so the checklist is unchanged.
FACT (verified this run against the working tree): 0075 is `**Status:** Draft for review.` with
`**Date:** 2026-08-19` (`proposals/0075_fix_derive-message-scope-from-the-address-type.md:3-4`), and
`git log -1` on that path returns 9beebfbc9, 2026-08-19, so the header date is also the last-commit date.
FACT: 0075's stated ground for its sole counterexample is that the handler "mutates `s.coord` ... a single
`coordinationState` ... for the whole adapter process" and "The identifier selects nothing"
(`0075...:85-89`). That is true of the tree today: `pkg/adapter/server.go:302` declares
`coord coordinationState` on `Server`, and `CoordinatorFence` uses the session id only for
`checkSessionBound` (`pkg/adapter/coordination.go:89`) before reading and writing `s.coord.lastFenced` and
`s.coord.initialized` with no session key (`:98-121`). CODE-1 deletes that field
(`non-spec-changes.md:33-36`).
FACT: 0075 carries a second, independent argument at `:91-95` that the identifier stays load-bearing as a
staleness guard against pod reuse across recycle boundaries. CODE-1 does not touch that argument, so the row
says its SCHEMA-1, CODE-1, and DOCS-1 lose the ground 0075 states for them rather than that they are void.
FACT: the deliverable split the row asserts resolves. SCHEMA-1 at `0075...:162-166`, SPEC-1 at `:169-172`,
CODE-1 at `:174-177`, TEST-1 at `:179-181`, DOCS-1 at `:183-186`, and the checklist steps S1 through S5 at
`:37-49`. Both DOCS-1 pages exist (`docs/reference/adapter-contract.md`, `docs/reference/metrics.md`).
FACT: the effect runs one way. `spec-changes.md:597-598` and `:630` state that no deliverable here depends
on 0075 landing, and no staged deliverable reads a renamed field or a guard type.
WATCHOUT: 0075's proto and code anchors have drifted independently of this proposal. `SessionId` is at
`schemas/lenny-adapter.proto:589` rather than the `:580` 0075 cites, `CoordinatorFenceRequest` is at
`:1447-1448` rather than `:1403-1404`, and `s.coord` is at `pkg/adapter/server.go:302` rather than `:304`.
The row records this so a rebase of 0075 is not mistaken for a consequence of this change.
WATCHOUT: OD3's cost paragraph (`summary.md:262`) says a "yes" leaves 0075's retyping deliverable "without a
subject", which is stronger than the row's "lose the ground 0075 states for them". OD3 belongs to another
item and was left as it stands; if a later pass harmonises the two, the row is the section that governs.

### [f2.other-proposals-0080]

DECISION: the 0080 impact row stands and is sharpened. Both halves were re-verified against the live files
rather than against the readings, and no new impact was found. Edited in place at `summary.md:863`; no other
file changed.
FACT: SPEC-2 removes the exact bullet 0080 §1.14 records. `spec/29_communication-scenarios.md:1523-1527` is
the first bullet of §29.10's "What the specification does not state" list, it asks both halves §1.14 names,
and `spec-changes.md:439-442` stages its removal.
FACT: §29.10's list carries four bullets today (`spec/29_communication-scenarios.md:1523`, `:1528`, `:1536`,
`:1540`), so 0080 §1.14's "the first of the five gaps" (`0080...:186`) is already stale before this change
and drops to three on application. This is the clause added to the row.
FACT: the claim-register row nearest this change stays `ABSENT`. `In-flight RPC cancellation on a generation
gap` (`tests/claim-map.json:174-178`) is unmoved, because SPEC-1 re-scopes the gap reset per session and
states that the staged text "does not assert that the adapter meets it" (`spec-changes.md:124-127`), and
`spec-changes.md:434-436` states that no claim-register row moves. The row now names this ground in place of
the bare "§1.12's claim-register rows are untouched".
FACT: D5 declines the §2 item. `0080...:211` records "A hold entered for one session released by another's
fence" as taken by 0076, restated at `:191-192`; `summary.md:8-10` and the staged §29.10 "Shared by the whole
pod" bullet (`spec-changes.md:452-457`) keep the hold pod-scoped with a fence from any bound session as the
correct exit.
WATCHOUT: 0080 is out of bounds for every agent on this proposal and corrects its own entries. The row is the
only record that this proposal declined an item another document says it took, so a later pass must not trim
the §2 half as redundant with `## Non-goals`.

### [f2.cleanup]

DECISION: `summary.md` is left as it stands. It already carries exactly the section list this phase fixes,
in order, so this pass rewrote nothing, relocated nothing, and deleted nothing.
FACT (verified this run against the file): the headings are `# Summary: Scope the coordination generation
to the session`, `## Summary`, `## Goals`, `## Non-goals`, `## Open decisions for human to make`,
`## Defects in the shipped tree that this proposal does not stage`, `## Impacts on other proposals`, and
`## Deliverable index`, with no ninth heading and no heading below level two anywhere in the file.
FACT: `## Summary` carries no prose of its own. Its four labelled parts are `**Problem statement.**` (:5),
`**What changes.**` (:24), `**Decisions.**` (:45), and `**Watch out for.**` (:75), in that order. Neither
`**What is fixed.**` nor `**Fixed decisions.**` survives anywhere in the file, so no rename was owed.
FACT: `## Open decisions for human to make` carries OD1, OD2, OD3, OD5, OD7, OD9, OD11, OD12, OD13, OD14,
OD15, and OD16, each with the identifier it was stamped with. That is one entry for each of the twelve items
this firing classified as the human's, so nothing was added and nothing was dropped. The identifiers of the
withdrawn entries, OD4, OD6, OD8, and OD10, appear nowhere as a heading and were not reused.
FACT: the section carries no `### Retired` block or equivalent. There was none to check entries against and
none to delete.
FACT: `## Defects in the shipped tree that this proposal does not stage` carries the four out-of-scope
entries firing 1 confirmed (the barrier-target mirror lag, the fence driver's conflation of three failure
classes, the tier-3 coverage comment, and the absent `CH-ADAPTEREVENTS` client) plus the §10.1.2
cancel-and-reset entry this firing added, each verbatim as the earlier passes wrote it. None was reworded.
FACT: the fifth out-of-scope marker, the pod-wide scope of the coordinator-loss hold, stands as a
`## Non-goals` bullet (`summary.md:138-142`) rather than as a defect entry. Its firing disposition is
no-edit-needed and `## Non-goals` is a listed section, so it was left where it is.
FACT: `## Impacts on other proposals` carries one row each for 0060, 0075, and 0080, and the paragraph
recording why 0073 carries no row. The 0060 row and the 0073 paragraph were both refuted at the gate this
firing, so both stand unchanged.
FACT: `## Deliverable index` is last and untouched. Its nine rows (SPEC-1 through SPEC-3, SCHEMA-1, CODE-1
through CODE-4, and TEST-1) stand line for line as the reconciliation pass rebuilt them.
WATCHOUT: OD3's cost paragraph and the 0075 impact row assert the same effect at different strengths. The
paragraph says a "yes" leaves 0075's retyping deliverable "without a subject"; the row says SCHEMA-1,
CODE-1, and DOCS-1 "lose the ground 0075 states for them". `[f2.other-proposals-0075]` above recorded the
same disagreement and left OD3 as it stands. This pass did the same, because moving or softening the
paragraph would edit an open decision's statement of what its answer costs, which a format pass may not do.
The row governs if a later pass harmonises the two.
WATCHOUT: `## Summary`'s closing paragraph under `**Decisions.**` (`summary.md:70-73`) names three open
questions adjacent to the fixed decisions D1 through D7, while `## Open decisions for human to make` carries
twelve entries. The sentence is scoped to the questions adjacent to D1 through D7 rather than to the
section's whole contents, and no move this pass made bears on it, so it was left as written.
