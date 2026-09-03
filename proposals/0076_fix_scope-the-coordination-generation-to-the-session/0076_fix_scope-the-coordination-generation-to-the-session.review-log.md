# Review log: Scope the coordination generation to the session

## Standing context

**Compaction pass 21, 2026-09-03.** Read the whole ledger, which covers run 6's spec round 1: its fourteen review shards, the two fix
designs and the two fixes they landed (G1, splitting the `CoordinatorFenceResponse` comment out of SCHEMA-1's record-and-reject carrier
list; G2, keeping `coordfence`'s non-positive floor), the post-fix review, and the post-fix fix that followed it. The ledger is left
untouched, because the round boundary archives it whole and with its ids as soon as this pass returns; every durable line is lifted here.
Lifted into `### Settled`: OD10's settlement of the mirror-lag question, the fence handler's own acceptance predicate and its sole
carrier, the three fence proto carriers and their three different rules, the absence of any gate pinning the §4.1 classification, the
proto's published-artifact status and the empty client surface around it, the barrier fan-out's single wall-clock deadline with the
refutation of the D7 drain regression, the §28.5.1 and §28.8 anchor arithmetic, the `checkSessionBound`-versus-stale-fence code collision,
and the retention of the `coordfence` floor with the conjunction the §10.1.4 zero invariant now rests on. Lifted into `### Traps`: the
§28.8 `CH-BARRIER` disposition clause restated as a LIVE defect, the "a filed finding is not a fixed finding" class in both its forms, the
token grep that under-reports the docs surface, the two grace budgets in one `spec/10` paragraph, the three-sentence response comment, the
§5-header refutation that is greppable only in the archive, and the read hazard on this log.
Honoured six `CORRECTS` against the standing context: the counter-baseline and cache-fallback bullets now state that CODE-4 KEEPS
`coordfence`'s floor; §28.8's "fifth column" is the sixth `awk -F'|'` field, so the standing `$5` recipe reads the exclusivity cell
instead; the `CH-BARRIER` MISTAKE is restated as live rather than as history; the `CoordinatorFenceResponse` MISTAKE loses its pointer to a
frozen pass record that went with `## Resolved in adversarial review`; and the `spec/04` §4.1 `### Open` loses its claim that a tier-3 test
pins the classification, because none does. Two claims disagreed with no correction between them, and the newer was kept with the older
retired under a note in `## Retired`: §7's third open decision (`coord.mu`), and the disposition of the `## 5. Proposed changes`
fill-the-blanks header. Deleted as closed: six `### Open` items, with a seventh replaced by an `UNVERIFIED` pointer, and three
`### Deferred` entries. `### Open` gains ten lines and `### Deferred` gains two whole entries and ends at four.
**The target of 200 lines was not reached and the section grew, to 1,224 lines against pass 20's 1,051**, of which `### Settled` is 607,
`### Traps` 423, `### Open` 122, and `### Deferred` 39. Nothing was dropped to
reach it. This window was one review round over staged text that barely moved, so most of its ledger re-derives inventories that are
already standing; eleven `USEFUL` citations name the bullets that made those re-derivations cheap, and compressing `### Settled` to one
line per entry would delete exactly what the citations name. The overshoot sits where passes 19 and 20 left it. Decline that trade until
the code and test lanes land and the inventories become checkable against a tree.
Mechanical constraints: the Bash write path is denied for this file, so every edit goes through the editor tool and a deletion costs the
full text of what it deletes; run 4's ledger ids repeat across append batches, so `[non-spec.1.review-applicability.1]` and its siblings
each resolve to two different entries; and run 6's ledger is not in chronological order, the `[spec.1.review-*]` shards being round 1's
review, which ran BEFORE `[spec.1.fix-*]` landed G1 and G2 and before the post-fix block at the end of the ledger verified them.

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
- **The barrier's cache fallback puts a literal 0 on the wire and must not be floored.** `httpsurface.go` seeds the target's generation at 0
  and overwrites it only on a successful session-row read, so under a Postgres fault the barrier carries 0 and is refused with
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
  `lenny-adapter-jsonl.schema.json`, `runtime-ops-events.schema.json`, `sdks/`, `charts/`, and `pkg/gateway/openapi/` carrying nothing and
  §15 naming the fence nowhere. The first-fence exemption exists outside `pkg/` on one tracked carrier, `proto:161-162`; `spec/` carries it
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
  value. `lenny_checkpoint_barrier_ack_total`'s outcome set already carries `error`, so D7 moves a count from `error` to `success` inside
  an existing label set and invents no value; §28.8's fifth column, "Operator observable", needs no edit on any of the three staged rows.
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
  applicability lens filed the same header again as live text a non-spec round could not land. Before filing anything about either
  fill-the-blanks header, grep the ARCHIVE for `5-header` and `§5 header`, not only this log. The older reading, that round 6 simply filed
  it, is in `## Retired`. UNVERIFIED: whether the §5 header stands or falls, given one recorded refutation and one live filing.
- **WATCHOUT: reading this review log.** `sed -n '120,400p'` on it exceeds the Bash output cap and is persisted to a tool-result file whose
  own `sed` or read then reports a shorter length than the source range, which looks like truncation and is not. Read this file with the
  Read tool at explicit offsets against the real path. That supersedes the older advice, in the harness-hazards bullet above, to `sed` it in
  windows; the `sed` advice still holds for the two staging files.
- **WATCHOUT: the quiescence-unit contradiction admits two remedies and three rounds have picked neither.** §10.1.8 states the unit per
  session twice, step 2 recording the `barrier_id` "in the session's checkpoint metadata" (`spec/10:184`) and step 3 opening the
  `Checkpoint` stream "for each quiesced session ... and then releas[ing] quiescence" (`:185`), while step 2 also carries the pod-phrased
  "stops accepting new tool call dispatches", so a verifier can land on either side. The remedies are narrowing SPEC-2's §29.10 bullet
  (dropping "the unit of the quiescence a barrier establishes" and leaving the `Interrupt` addressing unanswered), which keeps CODE-1's
  per-entry `barrierGate` grounded, or dropping §3's "because §10.1.8 step 3 already fixes the gate's unit at the session", which reopens
  it. A round that finds this again must pick one.
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

### Open

Detail for each item is in the ledger entry named at the end of its line. The `[non-spec.5.*]` and `[non-spec.6.*]` entries were retired
in compaction pass 17; the items they carried are in the ledger residue entry `[non-spec.5-6.*]`, filed there under the id named here.
The `[spec.1.review-*]` entries were retired the same way in compaction pass 18, and their two unclosed items are in the ledger residue
entry `[spec.1.*]`. The sixteen `[spec.2.review-*]` and `[spec.3.review-*]` entries were retired in compaction pass 19, and their two
unclosed items are in the ledger residue entry `[spec.2-3.*]`. Compaction pass 20 retired no ledger entry, the round boundary archiving
the whole ledger instead, so the `[non-spec.1.*]`, `[non-spec.2.*]`, `[non-spec.3.*]`, and `[spec.1.review-*]` ids below resolve in that
archive rather than in this file. Two id collisions to expect there: several `[non-spec.1.review-*]` ids name two different entries, and
`[spec.1.*]` is both run 4's spec-round-1 residue entry and the stem of run 5's spec-round-1 lens ids. Compaction pass 21 also retired no
ledger entry, so run 6's `[spec.1.review-*]`, `[spec.1.fix-*]`, `[spec.1.postfix-*]`, and `[spec.2.review-mechanism.1]` ids resolve in an
archive too, and `[spec.1.review-*]` now names three separate generations of shard.

- **"The ordinary false positive".** SPEC-1's live rationale overclaims a sole ordering; drop the two words. `[spec.1-3.*]`
- **SCHEMA-1 qualifier wording.** The exact qualifier each of the seven carriers takes, including the D7 acceptance sentence; G1
  discharged the `CoordinatorFenceResponse` carrier and the rest are open. `[spec.1-3.*]`
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
- **Nothing outside CODE-4's closing sentence tracks the retained floor's retirement.** OD9 derives no recommendation, so if that release
  never happens the floor stays forever, which is harmless and unowned; OD8's recommendation is conditioned on OD9 and the coupling is
  recorded in neither entry. `[spec.1.fix-G2.1]`, `[spec.1.review-open-decisions.1]`
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
- **Superseded replica's stream against a quiesced pod.** Whether it is acceptable, and whether an accepted false-positive barrier is
  followed by a stream at all. `[spec.1-3.*]`
- **`spec/04` §4.1 fence row.** `CoordinatorFenceRequest` is declared pod-scoped. CORRECTED in pass 21: no gate pins the classification
  either way, so the older half of this line, that a tier-3 test pins it, is gone. `[spec.1-3.*]`, and `[spec.1.*]` asks whether the staged
  edits must adjudicate it.
- **§1 severity.** Whether the recalibrated headline harm is restated at the top of §1 rather than only in §1.3. `[spec.1-3.*]`
- **Proposal 0080 overlap.** Its section 1.14 covers the same §29.10 bullet SPEC-2 stages for removal; nobody has recorded the overlap.
  `[spec.1-3.*]`
- **Rebind and the unset state.** Whether a session can unbind and re-bind on the same pod, which would lose the per-entry value. The
  adapter bars nothing, `deregisterSlotLocked` deleting the map key with no tombstone (`slotsession.go:346-361`, `session.go:237-240`);
  nobody has checked the gateway side, which is OD7's reachability question. `[spec.1-3.*]`, `[spec.1.review-open-decisions.1]`
- **§29.2 step 11.** Whether SPEC-1 owes a change to the bullet recording the pre-message announcement as unstated. `[spec.1-3.*]`
- **`coordinator_lost` log line as a spec artifact.** The staged §10.1.4 text names it where no section introduces it. `[spec.1-3.*]`
- **CODE-1's accessor enumeration omits `checkpointbarrier_test.go:163`.** `[spec.1-3.*]`
- **UNVERIFIED: tier-1 home for the sess-b-is-zero assertion.** `[spec.1-3.*]`
- **UNVERIFIED: whether the shipped drain ever quiesces a never-handed-off session.** `[spec.1-3.*]`
- **UNVERIFIED: step 3's "only" against its unset carve-out.** `[spec.1-3.*]`
- **UNVERIFIED: D6's stated ground for the first-fence exemption does not follow,** though the exemption is right. `[spec.1-3.*]`
- **UNVERIFIED: baseline reachability in pure spec terms.** `[spec.1-3.*]`
- **UNVERIFIED: OD12's drain-budget bound** (one 90s wall-clock window across all pods, the manifest write guarded by supersede-on-write
  and `partial_manifest_active_uniq`) was not re-derived in run 6. `[spec.1.review-open-decisions.1]`
- **UNVERIFIED: whether "coordination state" is a term `spec/` binds anywhere.** Staged §10.1.8 step 1 says the barrier's generation is
  "read from the session's coordination state"; context settles the reading and a grep would settle the term. `[spec.1.review-feasibility.1]`
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
- **UNVERIFIED: which disposition §7's third decision (`coord.mu`) takes.** Two readings stand and neither corrects the other; the
  `### Settled` bullet on §7 carries both. `[non-spec.5.review-mechanism.1]`, `[spec.1.review-open-decisions.1]`
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
- **§29.10's quiescence-unit clause admits two remedies** with different consequences, and three rounds have now found it and picked
  neither; the `### Traps` entry names both remedies and the evidence. `[spec.2-3.*]`, `[spec.1.review-mechanism.6]`
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
- **UNVERIFIED: whether the summary's SPEC-2 deliverable row should name the §29.8 Preconditions deletion.** It already omits the §29.8
  step-2 edit, so the row is loose by precedent. `[spec.1.fix-design-G1.1]`
- **UNVERIFIED: whether the tier-4 co-tenant case can be driven through `coordfixture.FenceReadopter`** once `sess-a` is fenced
  explicitly; nobody has written the sequence out. `[non-spec.1.review-mechanism.1]`
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
  `pkg/adapter/coordination.go:126-128`; hand both to the implementor together.

## Ledger

### [spec.2.fix-G1.1]

DECISION: Struck OD1's "second, permanent producer" ground from `summary.md` (the `bumpCoordinationGenerationOnSnapshotClose` clause) and replaced it with the one producer that actually reaches a barrier, a successor between its compare-and-swap and its fence acknowledgement, carried onto the wire by the barrier's cache-fallback live-row read — BECAUSE the struck clause was false about the tree and OD1's recommendation (keep equality) never rested on it — ALTERNATIVES: rewriting the clause into a correct account of the bump's call sites (rejected: a correct account argues against OD1's own recommendation, since it shows the bump produces no value the pod lacks, and it imports four call-site citations into an entry whose other grounds need none); narrowing the clause with an "on the terminal call sites" qualifier (rejected: that narrows the ground to the case where the session is over and no barrier is ever assembled, leaving a ground that supports nothing); deleting OD1 from the list (rejected: OD1 also carries the routing of §4's false "either order" ground to human review, and the summary's own preamble says an entry leaving the list is restated as withdrawn rather than deleted).

FACT: `bumpCoordinationGenerationOnSnapshotClose` has four non-test callers, and only three are terminal writes. The fourth fires under `disp.From == session.StateResuming && disp.To != session.StateResuming`, whose two dispositions are `transitionToResumePending` and `transitionToAwaitingClientAction`, both recovery states rather than terminal ones. From either, the resume path calls `fenceResumedPod`, which fences at the value the row now holds with no increment, so the bumped value is installed on the replacement pod and the row does not sit above the pod's fenced value for the session's life. — EVIDENCE: pkg/gateway/sessionserver/failure.go:212-219; pkg/api/v1/session/session.go:171-179; pkg/gateway/sessionserver/start.go:4233-4245; pkg/gateway/coordination/coordfence/coordfence.go:143

FACT: The barrier's cache fallback is the only path that can put a generation above the pod's fenced value on the wire. It reads the live session row per binding, while the mirror path cannot lead the pod, because `Sweeper.upsertMirror` writes `row.CoordinationGeneration` from the sweep's pre-`RecordHandoff` List snapshot and runs only after the re-adopt fence acknowledged. — EVIDENCE: cmd/lenny-gateway/httpsurface.go:588-602; pkg/gateway/coordination/coordination/coordination.go:410-430

WATCHOUT: The three terminal call sites of the snapshot-close bump, and `spec/04_system-components.md:200`'s description of it under mid-resume terminal collapses, are accurate and are NOT falsified by this fix. Do not "correct" `spec/04:200`: it says mid-resume terminal collapses bump the counter under the terminal write, which is true of those three callers and asserts no exclusivity. — EVIDENCE: pkg/gateway/sessionserver/sessionserver.go:2956; pkg/gateway/sessionserver/start.go:3508; spec/04_system-components.md:200

DEFERRED [proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.review-log.md]: The `### Settled` "Counter baseline" bullet closes with "The §7.2 snapshot-close bump fires only under a terminal write, after which no takeover follows." Both halves are false. `failure.go:219` fires the bump on the `resuming → resume_pending` and `resuming → awaiting_client_action` edges (`failure.go:212`, `:216`), neither terminal, and a takeover does follow on both, `fenceResumedPod` fencing the replacement pod at the bumped value (`start.go:4233-4245`). What is true instead: the bump has four non-test callers, three of them terminal writes after which no takeover follows, and one on the two recovery edges from `resuming`, after which the resume path fences the replacement pod at the bumped value. This is independently wrong rather than caused or repaired by the OD1 fix, and nothing in the proposal now rests on it, because run 6's G2 fix kept `coordfence`'s floor on other grounds. It sits in curated ledger prose the compaction pass owns.

UNVERIFIED: `review-log-archive.md` carries the same false premise at two spots (:4466 as a `FACT`, :5003-5007 as an `OPEN`). The archive is documented as frozen once written, so nobody should edit it, but a later pass that greps the archive for the bump's firing condition will find the false statement. Whoever owns archive hygiene should decide whether a frozen record needs a pointer.



### [spec.2.fix-design-G1.1]

DECISION: OD1 keeps its recommendation (equality) and loses its second-producer ground entirely, rather than
gaining a corrected description of `bumpCoordinationGenerationOnSnapshotClose`'s call sites — BECAUSE the
decision does not rest on that ground and re-describing the bump's four call sites inside a reviewer-facing
decision entry is tree detail OD1 does not need; the surviving grounds (§10.1.2 step 2's acknowledged-fence
precondition, the one producer that actually reaches a barrier, and that widening does not fix the
mirror-lag refusal) carry it — ALTERNATIVES: (a) rewrite the clause to say the bump fires on the
resume_pending / awaiting_client_action edges too and a later `fenceResumedPod` closes it, rejected because
it argues against OD1's own recommendation and adds a paragraph to an entry the reviewer reads to decide one
thing; (b) leave the clause and add "on the terminal call sites" as a qualifier, rejected as hair — a
qualifier that narrows a ground to the case where no barrier is ever assembled leaves a ground that supports
nothing; (c) drop OD1 entirely as resolvable, rejected because §4's false "either order" ground is already
routed to human review and the entry is where that routing is recorded.

FACT: the value the barrier carries can sit ABOVE the pod's fenced value on exactly one path, the
cache fallback, and its code lives in `cmd/lenny-gateway/httpsurface.go:588-601` (the `Fallback` closure on
`barrier.MirrorTargetLister`), which reads the LIVE session row per binding and seeds 0 on a read error.
The mirror path cannot produce a value above the pod's: `Sweeper.upsertMirror`
(`pkg/gateway/coordination/coordination/coordination.go:430`) writes `row.CoordinationGeneration` from the
sweep's pre-`RecordHandoff` List snapshot and runs AFTER the re-adopt fence acknowledged (`:424` `publish()`),
so it lags rather than leads — EVIDENCE: cmd/lenny-gateway/httpsurface.go:588-601,
pkg/gateway/coordination/coordination/coordination.go:410-431, :544-556.

WATCHOUT: the standing context's cache-fallback bullet says "`httpsurface.go` seeds the target's generation
at 0" with no directory. There is no `pkg/gateway/sessionserver/httpsurface.go`; the only `httpsurface.go` in
the tree is `cmd/lenny-gateway/httpsurface.go`. A grep under `pkg/` for it returns nothing and reads as the
bullet being stale — EVIDENCE: cmd/lenny-gateway/httpsurface.go:585.

FACT: `bumpCoordinationGenerationOnSnapshotClose` (`pkg/gateway/sessionserver/start.go:4452`) has four
non-test callers, and one of them is NOT a terminal write: `failure.go:219` fires under
`disp.From == StateResuming && disp.To != StateResuming`, whose two dispositions are
`transitionToResumePending` and `transitionToAwaitingClientAction` (`failure.go:212`, `:216`), both recovery
states (`pkg/api/v1/session/session.go:170-180`). From either, the resume path calls `fenceResumedPod`
(`start.go:3975`, `:4067`), which fences at whatever the row now holds (`coordfence.go:143`), so the bumped
value IS installed on the replacement pod. The other three callers (`sessionserver.go:2956`, `:3006`,
`start.go:3508`) are terminal. `spec/04_system-components.md:200` says "In particular, mid-resume terminal
collapses ... bump", which is true of those three and does not claim exclusivity, so no spec site is
falsified — EVIDENCE: pkg/gateway/sessionserver/failure.go:200-221, pkg/gateway/sessionserver/start.go:4233-4245.

DEFERRED [proposals/0076_.../0076_....review-log.md]: the `### Settled` "Counter baseline" bullet ends
"The §7.2 snapshot-close bump fires only under a terminal write, after which no takeover follows"
(review-log.md:42-43). Both halves are false: `failure.go:219` fires the bump on the
`resuming → resume_pending` and `resuming → awaiting_client_action` edges, and a takeover does follow on
both, `fenceResumedPod` fencing the replacement pod at the bumped value. What is true: the bump has four
non-test call sites, three terminal and one on the recovery edges, and on the recovery edge a fence follows.
Nothing in the proposal now rests on the false sentence (G2 kept `coordfence`'s floor on other grounds), so
this is a ledger correction rather than a staged-text one. I left it out of the fix because it is already
wrong independently of the OD1 edit and because curated standing context is the compaction pass's file, not
the fixer's. The compaction pass should strike those two lines from the bullet.

MISTAKE: two rounds carried "the snapshot-close bump fires only under a terminal write" as ground truth —
into `### Settled` (review-log.md:42-43) and into OD1's second-producer clause (summary.md:177-181) — because
the function's doc comment (`start.go:4425-4451`) and `spec/04:200` both discuss the terminal collapses and
neither mentions the fourth caller. The cost was a false statement about the tree standing in the list the
human reviewer signs off from. Enumerate a function's callers with grep before generalizing from its doc
comment.


### [spec.2.postfix-review.1]

DECISION: Round 2's fix for G1 verifies clean; no finding returned — BECAUSE the diff against the pre-fix snapshot is a single hunk in `summary.md` OD1 (:176-181), the false clause is gone in full rather than reworded, the replacement's one citation resolves and says what the new text claims, and no parallel statement in the staged files restated the struck clause.

FACT: The only edit this round is `summary.md` OD1. `diff -ru /home/ec2-user/lenny/scratchpad/cp-snap/0076-run6/spec-r2-prefix <proposal dir>` reports one hunk and no "Only in" lines, so every other proposal file (spec-changes, non-spec-changes, problem-statement, implementation-checklist, status, deviations, review-log, review-log-archive) is byte-identical to the snapshot. — EVIDENCE: diff output, single hunk at summary.md:173-184

FACT: The struck span is fully removed. `grep -n "bumpCoordinationGenerationOnSnapshotClose\|two distinct producers\|no coordinator ever installed\|resuming-to-terminal\|4452"` across the proposal's non-archive files returns exactly one hit, `review-log.md:41`, which is the deferred `### Settled` ledger site rather than OD1. — EVIDENCE: proposals/0076_.../0076_....summary.md:176-181; :review-log.md:41

FACT: The new citation is real and says what the new text claims. `cmd/lenny-gateway/httpsurface.go:588-602` is the `MirrorTargetLister.Fallback` closure; :593 calls `w.sessions.Get(context.Background(), b.TenantID, b.SessionID)` per binding and :594 copies `row.CoordinationGeneration` onto the target, so the fallback does read the live session row per binding. `wiring.go:72-76` names that path `cache_fallback`, so the new text's "cache-fallback path" is the tree's own label. — EVIDENCE: cmd/lenny-gateway/httpsurface.go:588-602; pkg/gateway/coordination/barrier/wiring.go:72-76, :117-121

FACT: No drift. The new ground (the barrier can carry a post-compare-and-swap value the pod has not yet been fenced at) agrees with the two other places the proposal states the same mechanism: `spec-changes.md:82` cites the same closure (`httpsurface.go:592-599`, a narrower span of the same lines) for the cache fallback reading the live session row, and `spec-changes.md:218-224` already stages §10.1.8 text saying the assembly "reads state that can sit on either side of the value the pod holds" and describing the false positive where "the barrier carries the generation the acquiring replica's compare-and-swap wrote and the pod still holds the value the draining replica fenced". OD1's surviving mirror-lag ground (mirror carries a value below the pod's) is not contradicted by it: the two grounds name different source paths. — EVIDENCE: proposals/0076_.../0076_....spec-changes.md:62, :82, :206-224; :summary.md:182-183

FACT: The premise the strike rests on re-verifies. `pkg/gateway/sessionserver/failure.go:210-221` fires the bump under `disp.From == session.StateResuming && disp.To != session.StateResuming` over dispositions `transitionToResumePending` (:212) and `transitionToAwaitingClientAction` (:216); the bump has four non-test callers (failure.go:219, start.go:3508, sessionserver.go:2956, :3006). — EVIDENCE: pkg/gateway/sessionserver/failure.go:200-221; grep of `bumpCoordinationGenerationOnSnapshotClose` across pkg/

DEFERRED [proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.review-log.md:42-43]: The `### Settled` "Counter baseline" bullet still closes with "The §7.2 snapshot-close bump fires only under a terminal write, after which no takeover follows", which `failure.go:210-221` falsifies. I did not raise it as a post-fix finding: it predates the edit, the confirmed finding's `where` and `suggested_fix` were OD1-scoped, and OD1 no longer says anything about the bump, so the two sites do not now contradict each other. It stands as separate-finding work for the ledger's owner. — EVIDENCE: :review-log.md:41-43; pkg/gateway/sessionserver/failure.go:210-221


### [spec.2.review-open-decisions.1]

DECISION: filed two findings, both against the summary's `## Open decisions`, and declined every other
open-decision candidate — BECAUSE the material skeptic has now refuted eight consecutive findings of the
"proposal-internal prose lands nowhere in spec/" shape (§4's either-order bullet, §7 items 2 and 3, the §5
header, OD1's missing third arm, §6/§10 on 0075, SPEC-1's no-edit-site ground, the §28.8 CH-BARRIER
disposition clause), so only a decision entry whose ground is FALSE ABOUT THE TREE and whose recommendation
would change behaviour clears the bar — ALTERNATIVES: filing OD7's missing spec-side ground, OD9's
uncoupled floor-retirement, and SPEC-2's ":549 §7's first open decision" attribution, all of which are
omissions or loose attributions that change no answer and would have been refuted.

FACT: `bumpCoordinationGenerationOnSnapshotClose` does NOT fire only on terminal edges. Its principal
caller fires on `resuming → resume_pending` and `resuming → awaiting_client_action`, and both are named
RecoveryStates rather than terminal ones — EVIDENCE: pkg/gateway/sessionserver/failure.go:216-219;
pkg/api/v1/session/session.go:162-174.

CORRECTS [review-log.md `### Settled` counter-baseline bullet, ":41-43"]: "The §7.2 snapshot-close bump
fires only under a terminal write, after which no takeover follows" is wrong. failure.go:219 fires it on
two non-terminal recovery edges, from which the session later resumes and `fenceResumedPod` fences the
replacement pod at the bumped value. The bullet's conclusion (the bump does not produce a takeover) still
holds; its stated reason does not, and OD1 rests a "permanent producer" claim on the same wrong reason.

FACT: the OD2/OD8 collision. For a session row an old binary minted at 0 during the rolling window,
`coordfence.fence` floors 0 to 1 on the resume fence (coordfence.go:147-153) and `Sweeper.RecordHandoff`
bumps 0 to 1 so the takeover also fences at 1 (coordination.go:463-482). Two replicas therefore carry the
same generation AFTER CODE-4, which is exactly the state OD2 says CODE-4 makes unreachable. The class is
permanent, 0181's backfill being one-shot and pgstore.Create binding the struct value straight through
(pgstore.go:250-261) — EVIDENCE: summary.md:202-204 against summary.md:304-308.

WATCHOUT: OD11's "Both rows are already present and `WIRED`" and the Corrections-outstanding bullet's "the
fence request has no row / a non-wired row for the barrier's generation field" are NOT a contradiction.
They name different rows: the RPC-level `CoordinatorFence` and `CheckpointBarrier` rows are both WIRED
(tests/claim-map.json:449-465), while the FIELD-level `CheckpointBarrierRequest.coordination_generation`
row is UNWIRED under deferral R16 (:75-82). A pass spent on this returns nothing.

FACT: "coordination state" is not a term `spec/` binds. The only `spec/` occurrences are the dual-store
degraded-mode phrase "in-flight sessions continue on cached coordination state" — EVIDENCE:
spec/12_storage-architecture.md:153, :226; spec/28_communication-channels.md:1655, :1817. Staged §10.1.8
step 1's "read from the session's coordination state" therefore borrows a phrase spec/ uses for something
else. Weighed and not filed (context settles the reading, and it is the citations lens's surface); this
closes the standing UNVERIFIED that asked for exactly this grep.

FACT: no gate pins the §4.1 fence classification either way, re-verified this run.
`sessionScopedMessages` excludes `CoordinatorFenceRequest` by name and every tier-3 assertion iterates it;
the tier-0 gate only checks the scope word is one of two — EVIDENCE:
tests/tier3_contract/adapter_session_address/session_address_wire_test.go:37-43, :81, :102, :130;
tests/tier0_static/adapter_proto_message_scope_test.go:136-150.

DEFERRED [`0076...summary.md` OD7, `:286-296`]: OD7 states the rebind residual with no reachability
ground, while the review log's `### Settled` rebind bullet records that `spec/07:196` fixes
`resuming → running` as re-attach on a *replacement* pod, so in specification terms the rebind OD7 asks
the reviewer to accept cannot occur; only the gateway-placement half is genuinely open. What is true
instead: OD7's specification half is answered and only the code-side reachability question remains. Not
filed, because adding a citation changes no answer and the class has been refuted eight times.

OPEN: whether OD9 should name the retained `coordfence` floor. OD8's withdrawal conditions the floor's
retirement on OD9's tightening release (summary.md:312-314) while OD9 says "nothing in this proposal
depends on it" (:321-322). If the reviewer answers OD9 "no", the floor is permanent and unowned. One-way
coupling, recorded in neither entry, and both readings of "depends on" are defensible.

USEFUL [review-log `### Open` "OD2's ordering rationale is imprecise for the rolling window"]: it named the
exact sentence and the exact mechanism, and this run only had to re-derive the two code paths. It had sat
unlanded because the round that derived it was a fix round with no write path to the summary.


### [spec.3.review-open-decisions.1]

DECISION: Filed zero findings and put the whole round's work in the `decisions` array — BECAUSE every
open-decision defect I could substantiate this round has its remedy in `summary.md`'s `## Open decisions`
or in `non-spec-changes.md`, and the material skeptic has now refuted that class four times in this run on
the identical ground ("lands in no file under `spec/`", "proposal hygiene rather than a specification
risk"). Filing another would have cost two verifiers and closed nothing. — ALTERNATIVES: filing OD5's
missing spec-text cascade, OD7's stale ground, OD9's uncorded coupling, and OD10's SPEC-1/SPEC-2
misattribution as four findings; each is real and each is refutable on the standing materiality line.

FACT: `spec/07_session-lifecycle.md:196` answers the reachability half of OD7 in specification terms —
"resuming → running (re-attach succeeds on replacement pod ...)" — so a session cannot re-bind onto the pod
that held its per-entry value. OD7's entry (`summary.md:286-297`) never cites it and argues the reset from
the code side alone. The code-side half (whether `pkg/gateway/sessionserver` placement can pick the same
pod) is still unchecked. — EVIDENCE: spec/07_session-lifecycle.md:196; summary.md:286-297

FACT: OD10 (`summary.md:324-331`) attributes the "not an edit site" disposition for §10.1.8 step 1 and
§29.7 step 4 to SPEC-2. It is SPEC-1 that states it, at `spec-changes.md:259-264` ("The sentences elsewhere
that state which value a barrier carries are not edit sites under the baseline"). SPEC-2 never mentions
§29.7 step 4. OD10 also says SPEC-2 "leaves §10.1.8 step 1 ... unedited" while SPEC-1 rewrites step 1's
closing sentence (`spec-changes.md:212-235`); only the "current `coordination_generation`" sentence inside
step 1 is left. — EVIDENCE: summary.md:324-325; spec-changes.md:259-264, :212-235

FACT: OD5's stated costs of splitting are two, and neither is a spec-text cost. Answering it "split" drops
SPEC-3 entirely, drops SPEC-1's §10.1 baseline paragraph (`spec-changes.md:231-243`), and removes the sole
stated ground for SPEC-1's own no-edit-site conclusion at `:259-264` ("positive for every session once the
baseline is 1, so each stays true"). OD5 names none of these. — EVIDENCE: summary.md:254-270;
spec-changes.md:231-243, :259-264

FACT: OD11 is resolvable against `spec/28_communication-channels.md:163-165`. `WIRED` is defined as "the
mechanism is reachable from production code", and both the `CoordinatorFence` row
(`tests/claim-map.json:460-465`) and the `CheckpointBarrier` row (`:448-453`) stay reachable before and
after S1..S5, so no status change is owed and no claim-register deliverable is. The separate
`CheckpointBarrierRequest.coordination_generation` row (`:75-81`) is `UNWIRED`/R16 with a note already false
against the shipped comparison at `pkg/adapter/coordination.go:236`; that is the pre-existing correction the
summary's `### Corrections outstanding` bullet already owns. — EVIDENCE: spec/28_communication-channels.md:163-165;
tests/claim-map.json:75-81, :448-453, :460-465

FACT: OD13 is resolvable against the project's own rules rather than by a human. D7 removes the generation
gate's refusal for a bound session with no fenced value, leaving `checkSessionBound`
(`pkg/adapter/slotsession.go:267`) the sole fail-closed guard on the barrier path, and
`.claude/rules/test-coverage.md` requires the fail-closed path to be pinned. The answer is "yes, TEST-1 owes
it"; the remedy is in `non-spec-changes.md` §8/TEST-1, which the spec loop may not edit. — EVIDENCE:
summary.md:353-357; pkg/adapter/coordination.go:216, :236

FACT: §7's three citations all resolve. `fenceResumedPod` call sites are `start.go:3975` and `:4067`, the
function is at `:4233`, its `Fence` call at `:4237`; the sweeper path is `coordination.go:399` through
`coordination_seams.go:155-160` and `:233`. A tree-wide `grep "\.Fence("` outside tests returns exactly
`start.go:4237`, `coordination_seams.go:233`, and `coordfixture.go:231`. Do not re-derive. — EVIDENCE:
pkg/gateway/sessionserver/start.go:3975, :4067, :4233, :4237; cmd/lenny-gateway/coordination_seams.go:155-160, :233

FACT: OD1's rewritten ground resolves. `cmd/lenny-gateway/httpsurface.go:588-602` is the
`MirrorTargetLister.Fallback` closure and it does read the live session row per binding
(`:593-594`), so it can carry a successor's post-compare-and-swap value while the pod is fenced one below.
The round-5 `bumpCoordinationGenerationOnSnapshotClose` clause is gone. — EVIDENCE:
cmd/lenny-gateway/httpsurface.go:588-602; summary.md:173-188

FACT: "The staged text already writes equality in every carrier" (OD1, `summary.md:183`) is loose but not
false for OD1's subject. Two staged carriers write "older than" rather than "other than": the §28.5.1
`CH-FENCE` Messages replacement (`spec-changes.md:311-315`) and §28.6's "One holder per session" sentence
(`:337-341`). Both are the fence's record-and-reject rule, both are shipped asymmetries the review log
already adjudicated, and neither is the barrier gate. Do not re-file. — EVIDENCE: spec-changes.md:311-315, :337-341

WATCHOUT: the review-log `### Open` item "Nothing outside CODE-4's closing sentence tracks the retained
floor's retirement ... the coupling is recorded in neither entry" is now half-stale. OD8's withdrawal
(`summary.md:302-317`) does record the coupling ("retired by the release that tightens the check ... which
is OD9's subject"). OD9 (`:318-322`) still does not, and it is where the decision is actually made.
— EVIDENCE: summary.md:302-317, :318-322

WATCHOUT: the "Scope of the proposal" `### Open` names three things ("D7, the counter baseline, and the
barrier-provenance reconciliation") where OD5 names three different ones ("D7, the counter baseline, and
migration 0181"). The reconciliation half is not a missing decision: the value-form rewrites land on
sentences the unit change makes edit sites anyway, and the choice between the value form and an
owner-form-plus-session-qualifier is settled by the log's own §28.6/§28.8 MISTAKE entries. Only OD5's half
is live. — EVIDENCE: review-log.md `### Open`; spec-changes.md:317-333, :353-372

OPEN: whether a summary-only `## Open decisions` defect can ever be landed in a spec-scoped round. This lens
is told it owns that section and that each drift is a finding; the loop scope is told to report only
findings whose fix lands in `spec-changes.md`; and the material skeptic refutes the class on sight. Three
rounds have now paid for that collision. A human should decide whether the open-decisions lens runs in the
non-spec lane instead, or whether the spec lane's fixer is permitted to edit `summary.md`'s OD section.


### [spec.4.review-applicability.1]

DECISION: returned an empty findings list — BECAUSE a full class-1 through class-6 simulation of SPEC-1, SPEC-2, and SPEC-3 over the live tree produced nothing that is not already on the refuted list or the weighed-and-declined list. ALTERNATIVES: re-filing the §28.8 `CH-BARRIER` disposition clause (refuted as immaterial in run 6; the applied bytes are identical either way), the two `IMPLEMENTOR TO FILL THE BLANKS` headers (both refuted), the §29.10 quiescence-unit clause (refuted), and SPEC-1's "each stays true" gloss on the §10.1.8/§29.7 currency sentences (refuted). Each was re-opened against the primary sources this run and each refutation held.

FACT: `diff -rq` against `scratchpad/cp-snap/0076-run6/spec-r4` returns nothing — the staged text is byte-identical to the run-5 post-fix state, so run 6's spec round 2 is a re-review of unchanged text, not a review of new fix-stage prose. Run the `diff -rq` first, as the standing trap says; it is the cheapest move on this proposal — EVIDENCE: proposals/0076_.../0076_....spec-changes.md (622 lines, unchanged since 2026-09-03 03:31).

FACT: every quoted anchor in SPEC-1/2/3 resolves uniquely in the current tree, re-verified mechanically this run rather than trusted from the log. `grep -rc` over `spec/*.md` returns exactly one file with exactly one hit for each of the eight distinctive anchor strings, and the four `prior coordinator's RPCs` sites are disambiguated by their leading clauses ("the acknowledgement of this fence is what closes" at spec/28:330-331, "the fence acknowledgement closes" at :1683-1685, "The acknowledgement closes the window" at :1807, "before the fence closes" at spec/29:1325). All four are staged — EVIDENCE: spec/10_gateway-internals.md:30, :38, :40, :41, :58, :60, :183; spec/28_communication-channels.md:315-316, :330-331, :1675, :1679-1681, :1683-1685, :1806, :1807, :1808; spec/29_communication-scenarios.md:1150-1152, :1259-1261, :1274, :1307-1313, :1322-1326, :1461-1470, :1519-1543; spec/04_system-components.md:200.

FACT: the relocation legs of the §29.10 carve-out are BOTH staged and complete. The removed "does not state" bullet (spec/29:1523-1527) asks two questions; question (i) hold partitioning lands in the new "Shared by the whole pod" hold bullet and question (ii) cross-slot fencing lands in the "Partitioned per slot" coordination bullet, and the removed bullet's own factual sentence (hold rejects every inbound RPC other than `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` detail) is carried into the new hold bullet verbatim in substance. Class-3 content loss does not apply — EVIDENCE: spec/29_communication-scenarios.md:1523-1527 (source), :1464-1470 and :1472-1474 (destinations); spec-changes.md:432-448.

FACT: no tier-0 or tier-11 gate string-matches a sentence the staging rewrites, re-derived independently this run rather than lifted. `grep -rn "matches the fenced value|no window in which|prior coordinator's RPCs|superseded replica is rejected|last known generation" tests/ pkg/ docs/` returns four hits and all four are unrelated credential/token comments plus one adapter doc comment (`pkg/adapter/holdstate.go:103`). `matrix_completeness_test.go` reads only the FIRST column of the §28.8 table via `firstColumnUnder`, so SPEC-2's cell-body edits cannot reach it — EVIDENCE: tests/tier0_static/matrix_completeness_test.go:36-60.

FACT: the staged spec text introduces no reserved N3 phrase and no N8 line citation. A case-insensitive grep of the whole spec-changes file for `lifecycle channel|control channel|lifecycle-channel|control-channel` returns nothing, and every `spec/NN_file.md:NNN` citation in the file sits in the proposal's own rationale prose rather than in text that lands under `spec/`. The naming lint and the citation ratchet are therefore not reached by S1.

WATCHOUT: SPEC-1's instruction "the initial condition is stated immediately after that parenthetical and before the clause list" (spec-changes.md:155-156) names a two-sentence span rather than a point, because the parenthetical sits inside the gap bullet's first sentence and the clause list opens two sentences later (spec/10_gateway-internals.md:40). It is NOT a class-2 underspecified target: the initial-condition sentence is spelled out word for word in SPEC-1's state-model paragraph (:142-145) and both candidate positions carry the same meaning. Weighed and declined this run; do not spend a verification on it.

WATCHOUT: §29.7's staged edit adds an "accepted" arm to a paragraph that closes "Those outcomes are named here and are not traced" (spec/29_communication-scenarios.md:1152). It reads as a self-contradiction and is not: the newly named accept arm is the accepted FALSE-POSITIVE barrier (a session the replica no longer coordinates), which the trace does not follow either, since §29.7 traces barriers for sessions the draining replica does coordinate. Worked up and dropped this run.

USEFUL [Settled: "Derived inventories. Do not re-derive any of these."]: the anchor inventory and the "no tier-0 or tier-11 gate reads the sentences SPEC-1 and SPEC-2 rewrite" bullet each saved a full re-derivation; both were spot-checked against the tree this run and both held exactly.

USEFUL [Traps: "a late pass number in the spec-changes file is not evidence that the staged spec text changed"]: the `diff -rq` returning empty was the first move and set the whole reading order for this pass.


### [spec.4.review-citations.1]

DECISION: Returned empty — BECAUSE the staged text is byte-identical to run 6 round 1 (`diff -rq` against `scratchpad/cp-snap/0076-run6/spec-r4` returns nothing), and an independent mechanical re-resolution of every citation in `spec-changes.md` found no anchor that fails to resolve or that says something materially different — ALTERNATIVES: re-filing the §28.8 `CH-BARRIER` disposition clause (rejected: on this run's refuted list, immaterial because the bullet quotes the clause and its replacement verbatim so the same bytes land either way); filing the `CoordinatorFenceResponse` "each of its two sentences" miscount (rejected: standing `### Traps` records it as weighed, harmless, and explicitly warns a later pass not to "correct" it by editing the lead sentence); filing SPEC-1's "§10.1.2 step 1's compare-and-swap mints 2 on the first takeover" against `failure.go:219` (rejected: see FACT below).

FACT: `bumpCoordinationGenerationOnSnapshotClose` has four non-test call sites and the fourth is NOT terminal, so in the shipped tree a session row can sit above the baseline with no takeover having happened, which would falsify SPEC-1's "a replica coordinating a session no replica has taken over carries that value" and "mints 2 on the first takeover". I declined to file it because in *specification* terms the only non-CAS writer `spec/` states is the §7.2 snapshot-close bump, and §7.2 confines that to `resuming → cancelled` and `resuming → completed`, both terminal, after which no takeover follows. The fourth call site is undocumented code behaviour and therefore pre-existing spec-versus-code drift this proposal does not stage. A later round wanting to file it must argue past §7.2's terminal-only wording. — EVIDENCE: pkg/gateway/sessionserver/failure.go:212-219 (the `disp.From == StateResuming && disp.To != StateResuming` guard over `transitionToResumePending` / `transitionToAwaitingClientAction`); spec/07_session-lifecycle.md:210, :215 ("The `resuming → cancelled` and `resuming → completed` edges ... bumps `coordination_generation` by exactly one")

FACT: the clause-versus-sentence audit is now complete over every cited cell and bullet in SPEC-2, and `CH-BARRIER` is the only outlier. `spec/28:1807` (`CH-FENCE`) and `spec/29:1322-1326` (§29.8 step 9) both quote a *clause* of a compound sentence, but both bullets name the surviving half explicitly ("The cell's other sentences are unchanged" after staging the gap sentence and the window clause; "The rest of the step, which states the acknowledgement as the hard precondition ..."), so the substitution lands. `spec/28:1806` (`CH-CHECKPOINT`) names its surviving clause ("the new holder must complete its fence before it opens one") separately from its constraint sentence. Do not re-derive this. — EVIDENCE: spec/28_communication-channels.md:1806, :1807, :1808; spec/29_communication-scenarios.md:1324-1325; proposals/.../spec-changes.md:374-381, :382-389, :486-494

FACT: reading a §28.8 row is cheapest as `awk -F'|' 'NR>=1805 && NR<=1812 {print NR" "$5}'` for the exclusivity cell, but `sed -n '1803,1812p' <file> | cat -n` prints the whole row set legibly in one call and is what I used; the rows are one physical line each and `cat -n` on the range gives a stable relative index. The `CH-FENCE` cell is four sentences (holder / window+hold compound / gap / citations), the `CH-CHECKPOINT` cell two, the `CH-BARRIER` cell two.

USEFUL [`### Settled` "Derived inventories. Do not re-derive any of these."]: it told me every anchor resolves, which let me spend the pass on structure instead of resolution — but I re-resolved the whole set anyway (about a third of the pass) because the orchestrator's brief said to treat every citation as unverified. The re-resolution confirmed the bullet and found nothing. A future citation lens on unchanged text should trust that bullet and skip straight to structure; the standing lens-exhaustion bullet already says so and it is right.

USEFUL [`### Traps` "MISTAKE: reading the proto"]: `awk '/^message /{m=$2} /coordination_generation *=/{print NR": "m}'` and the warning that a message doc comment sits above the `message` line saved me from mis-pairing the `CoordinatorFenceRequest` / `CoordinatorFenceResponse` / `CheckpointBarrierRequest` comment ranges. Reading `150,180p;1440,1485p` in one `sed` with `cat -n` and then converting relative to absolute indices is reliable; the two ranges concatenate, so compute the offset from the first range's length rather than from its start.

WATCHOUT: `grep -o '`[a-zA-Z0-9_./-]*\.\(go\|md\|sql\|proto\|yaml\|json\)[:0-9-]*`'` over `spec-changes.md` returns 56 citations and MISSES every short-form continuation (`:1442-1446`, `:236-239`, `:291-296`, `:349-353`, `:1805`, `:112-113`, the twelve proto message ranges, `:4067`, `:198`, `:172-176`, `:192`). About a third of the file's citations are short forms attached to a previously named file. A sweep built on that grep alone under-reports by roughly half. — EVIDENCE: proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:562-567 (twelve message ranges, none of which the grep returns)

OPEN: run 6 round 1's citation lens recorded that "both surviving defects of that class" live in sentence-versus-clause structure inside cited cells. One is the §28.8 `CH-BARRIER` clause. I could not identify a second that is not already weighed-and-declined; the best candidate is the `CoordinatorFenceResponse` three-sentences-not-two miscount. If a later round wants that count settled, the entry to read is the `### Traps` bullet "the `CoordinatorFenceResponse` comment is three sentences, not two", and the answer is that its remedy is a two-word edit at `spec-changes.md:519` ("its two sentences" → "the two sentences that define its fields") which lands nothing in `spec/`.


### [spec.4.review-client-surface.1]

DECISION: Returned empty. — BECAUSE `diff -rq` against `scratchpad/cp-snap/0076-run6/spec-r4` is byte-identical
(exit 0, no output), so this is the same text over which the client-surface lens already returned empty in run 6
round 1, and every parallel-representation check I re-derived independently came back matching the standing
inventory. — ALTERNATIVES: I worked up and rejected four candidates, each named below so nobody re-derives them.

FACT: The proto carrier arithmetic re-derived independently and it holds exactly.
`awk '/^message /{m=$2} /coordination_generation *=/{print NR": "m}' schemas/lenny-adapter.proto` returns
fourteen fields; `grep -n "A pod validates"` returns exactly twelve, and they are exactly the twelve messages
SPEC-2 enumerates at spec-changes.md:562-567. The two non-hits are `CoordinatorFenceRequest` (:1449-1451,
worded differently, keeps its wording) and `CheckpointBarrierRequest` (:1477-1479, in the acceptance-rule list).
No message is double-listed and no thirteenth "A pod validates" site exists, so the twelve-comment edit
instruction has an anchor on every one of its targets — EVIDENCE: schemas/lenny-adapter.proto:970, :996, :1047,
:1071, :1092, :1115, :1173, :1306, :1394, :1532, :1577, :1619.

FACT: `CheckpointBarrierResponse` is not an eighth carrier of the acceptance rule. Its comment
(schemas/lenny-adapter.proto:1493-1502) describes `barrier_id`, `checkpoint_ref`, and `quiesced_ms` and states
no gate, no rejection, and no generation. The standing `### Settled` "no eighth carrier" bullet names
`CoordinatorFenceResponse.last_fenced_generation`, the `Checkpoint` RPC comment, and
`CheckpointStart.checkpoint_id` but never named this message, so this closes the one hole in that enumeration.

FACT: The empty-client-surface claim re-verified from scratch and it is exact.
`grep -rn "coordination_generation\|coordinationGeneration\|coordination generation" pkg/gateway/openapi/
docs/api/ docs/client-guide/ docs/runtime-author-guide/ sdks/ charts/ schemas/*.json` returns nothing;
`grep -n "coordination_generation\|CoordinatorFence\|coordination generation" spec/15_*.md` returns nothing;
`grep -rln "lenny-adapter\|CoordinatorFence\|CheckpointBarrier" sdks/` returns nothing;
`grep -rln "coordination_generation\|coordinationGeneration\|coordinationGen" charts/ pkg/apis/ pkg/embedded/`
returns nothing. So the REST/OpenAPI, MCP tool-schema, A2A, CRD, and language-SDK halves of this lens have no
surface on this proposal at all, and `spec/04:200` states why: `recovery_generation` is "visible to clients via
the session API and `session.resumed` events" while `coordination_generation` is "internal only, used for
split-brain fencing", so SPEC-3's baseline of 1 cannot reach a client representation — EVIDENCE:
spec/04_system-components.md:200.

WATCHOUT: `spec/04:712`'s `CoordinatorFence` row ("Precondition for any subsequent operational RPC") reads as an
unstaged §4.7 edit site once D6's unset arm and D7's acceptance land, and `spec/04:711`'s `CheckpointBarrier`
row sits beside it. It is not one: refuted class (a) in the standing context already files `spec/04`'s
`CoordinatorFence` row as a statement of §10.1.2 step 2's sender-side obligation, which SPEC-1 leaves unchanged,
so it coexists with the staged pod-side gate. :711 states what the message carries and the quiescence and names
no gate. Do not spend a verification on either — EVIDENCE: spec/04_system-components.md:711-712;
review-log.md refuted class (a).

WATCHOUT: `spec/04:323` (`session_eviction_state.coordination_generation`) and `docs/reference/adapter-contract.md:68-69`
and `:96` each look like a missed mirror of the changed rule and neither is. :323 names the column's role with no
baseline and no gate; the adapter-contract rows are unit-neutral and their known looseness is pre-existing drift
the standing context already records for a docs loop — EVIDENCE: spec/04_system-components.md:323;
docs/reference/adapter-contract.md:68, :69, :96.

FACT: `schemas/runtime-ops-events.schema.json` carries the adapter-to-runtime quiescence frames
(`:70`, `:82`, `:93`) and none of them carries a session identifier or a generation, which is the wire-level
reason the quiescence-unit question cannot be answered per session on the runtime side. This is the primary
source behind the refuted quiescence-unit finding; read it rather than the refutation's summary.

USEFUL [standing context, `### Settled`, "The proto carrier arithmetic is closed" and "The proto is a published
artifact and the client surface around it is empty"]: these two bullets are what let this pass be a
verification rather than a derivation. Both survived independent re-derivation unchanged, and the second is now
strengthened by the `CheckpointBarrierResponse` FACT above.


### [spec.4.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE every behaviour this proposal changes (per-session
fenced generation, D5/D6/D7, the counter baseline, the `coordinator_connection_lost` field change) was
re-checked against `docs/` from scratch this round and no `docs/` page states the unit, the baseline, the
gate, or any of the failure narratives, so no docs edit is owed and nothing in the staged spec text is
falsified by a docs surface — ALTERNATIVES: filing the `spec/04` §4.1 fence-scope row as a missing edit
site (it is OD3, an open decision the summary already carries with a recommendation, so another lens owns
it); filing OD7's rebind reset as an accepted-behaviour-not-landing item (same reason: it is an open
decision with a recommendation, not a settled disposition); filing the staged §10.1 "every generation a
pod validates is positive" sentence against the cache fallback's literal 0 (refuted class (j),
review-log.md:428).

FACT: the `docs/` surface for this change is five token sites plus three neighbours and none of them is
made wrong by the change. Independently re-derived this round: `docs/getting-started/concepts.md:101` and
`docs/getting-started/architecture.md:173` name the counter with no baseline and no unit;
`docs/reference/adapter-contract.md:68`, `:69`, `:96` name `CheckpointBarrier`, `CoordinatorFence`, and
`CheckpointBarrierAck` with no gate; `docs/reference/metrics.md:307` and `:309` state
`lenny_coordinator_handoff_stale_total` and `lenny_adapter_coordinator_hold` in terms that stay true;
`docs/operator-guide/upgrades.md:47-54` is unit-neutral. — EVIDENCE: docs/getting-started/concepts.md:101,
docs/reference/metrics.md:307

FACT: no runbook and no alert is reached. `grep -rln "barrier" docs/runbooks/ docs/alerting/
pkg/alerting/rules` returns nothing, `coordinator_handoff_stale`, `coordinator_hold`,
`coordinator_connection_lost`, and `coordinator_generation_gap` appear in no `docs/` file at all, and
`docs/reference/error-catalog.md` and `docs/reference/state-machines.md` carry none of the fence or
barrier error details. So the alert-to-runbook half of this lens has nothing to resolve, before and after.
— EVIDENCE: docs/reference/metrics.md:196-197 (the only barrier rows, `outcome` values unchanged)

FACT: no `docs/` or `charts/` surface enumerates migration numbers, so CODE-4's new `migrations/0181_*`
owes no docs edit. `grep -rn "0180\|0179\|migrations/0" docs/ charts/` returns nothing. — EVIDENCE:
proposals/.../non-spec-changes.md:119 (0181 named as the next free number)

FACT: the code already logs what the staged §10.1.4 Observability sentence asserts. `enterHoldState` emits
`coordinator_connection_lost` with `started_sessions` and `last_generation`, so CODE-3 drops a field that
exists rather than adding one that does not. — EVIDENCE: pkg/adapter/holdstate.go:129-132

USEFUL [Settled: "Derived inventories. Do not re-derive any of these."]: its `docs/` sentence names the
exact five token sites and the three neighbours, which is what let this pass be a verification rather than
a sweep. The one correction it already carries (`docs/getting-started/architecture.md`, there being no
`docs/architecture.md`) is right.

USEFUL [Traps / refuted class (j), review-log.md:428]: it stopped a finding on staged §10.1's "every
generation a pod validates is positive" against `cmd/lenny-gateway/httpsurface.go:592` seeding
`gen := int64(0)`. The falsifier is real and permanent (a `sessions.Get` error under the cache fallback
puts a literal 0 on the barrier whatever the baseline is), and the refutation covers it. A future lens
that re-derives the seed will reach the same finding; it is refuted, not unnoticed.

OPEN: this lens has now returned empty on the spec lane over text byte-identical to the r4 snapshot
(`diff -ru scratchpad/cp-snap/0076-run6/spec-r4 proposals/0076_...` is empty), which is at least the third
empty return for docs alignment on this proposal. The standing `### Open` item asking whether an exhausted
lens is retired should be answered before another round pays for it.



### [spec.4.review-edit-sites.1]

DECISION: Filed exactly one finding, on SPEC-2's wire-carrier paragraph (`spec-changes.md:506-512`) telling
`CoordinatorFenceRequest`'s message comment to "take the §28.5.1 Messages wording" when that comment is one
sentence whose tail carries the fence rejection's status code, its `coordinator_handoff_stale` detail
string, and the `lenny_coordinator_handoff_stale_total` attribution — BECAUSE the proposal's own
description of the carrier (":1442-1446, which states that the pod records the new generation and from
that point rejects every RPC carrying a strictly older generation") truncates the sentence, so nothing in
the staging preserves the tail, and `coordinator_handoff_stale` has no other carrier in `spec/`,
`schemas/`, `docs/`, or `charts/` — ALTERNATIVES: filing the parallel exposure on the `CoordinatorFence`
RPC comment's "Deadline: 5 s (hard-coded, §11.3)" sentence (folded into the suggested fix instead: the
deadline is stated in `spec/28:323-324`, §11.3, and §4.7, so losing it from the proto strands nothing);
filing the §4.1 message-scope row (summary OD3 owns it, and the rubric bars filing recorded open
decisions).

FACT: `coordinator_handoff_stale` is a single-carrier wire contract. It appears in `spec/`, `schemas/`,
`docs/`, and `charts/` at exactly one place — EVIDENCE: schemas/lenny-adapter.proto:1445-1446; mirrored
into generated pkg/proto/adapter/v1/lenny-adapter.pb.go:4959-4960; emitted at pkg/adapter/coordination.go:106
and :238; asserted at pkg/adapter/coordination_test.go:119-120. `spec/10:71` and `spec/16:183` carry only
the metric name, never the detail string, and `CheckpointBarrierRequest`'s comment (`proto:1472-1473`)
names `FailedPrecondition` with no detail string.

WATCHOUT: SPEC-2's proto paragraph names a span for some carriers and not others. The twelve
operational-RPC comments get an explicit span ("from 'A pod validates' to the end of the consequence
clause") and a named exception for two trailing sentences; the `CoordinatorFenceResponse` paragraph refuses
the §28.5.1 wording outright because it "would replace the two field definitions". The two `CoordinatorFence`
carriers in the "Both take" sentence get no span at all — EVIDENCE:
spec-changes.md:509-512 against :553-560 and :518-534. When checking a SCHEMA-1 carrier, read the whole
comment in the proto rather than the proposal's summary of it; the summary is where the tail goes missing.

FACT: the whole spec-side edit-site surface re-derived independently this round and it matches the standing
inventory, so the standing `### Settled` inventories can be trusted on this: `coordination_generation`
occurs in `spec/04` (5), `spec/07` (3), `spec/10` (10), `spec/12` (1), `spec/16` (2), `spec/18` (1),
`spec/28` (11), `spec/29` (11); `last fenced|fenced value|fenced generation` returns six `spec/` lines, all
staged; no `CREATE TABLE sessions` DDL exists in `spec/`; nothing in `spec/` states the counter's initial
value, so SPEC-3's §4.2 baseline strands no "starts at 0" claim; `charts/` and `sdks/` carry no token at
all. The `no window in which` deletion strands no mirror: paraphrase greps (`simultaneously issue`,
`both the old and new`, `two coordinators`) return nothing else — EVIDENCE: spec/10_gateway-internals.md:41
is the sole site.

FACT: migration 0181's "both columns" means `sessions.coordination_generation` and
`coordination_lease.coordination_generation`, not `recovery_generation`, so SPEC-3's "`recovery_generation`
keeps its own baseline and this proposal does not touch it" holds. `spec/` never names the
`coordination_lease` table's generation column, so the mirror default needs no spec edit site — EVIDENCE:
non-spec-changes.md:119-122; spec/28_communication-channels.md:138 (the `REG-COORDMIRROR` register row
names the table and no column).

UNVERIFIED: whether §28.4's rule ("Every normative statement this section makes about a mechanism carries a
row in the claim register at `tests/claim-map.json`") reaches the new normative statement SPEC-2 adds to
§28.6 and §28.8, namely that the pod rejects none of an unfenced session's RPCs on generation grounds.
SPEC-2 asserts "No §28.4 claim-register row moves" on the ground that rows name mechanisms rather than
scope, which covers the re-scoped sentences but not obviously the new carve-out. The remedy would land in
`tests/claim-map.json`, which this loop may not edit, so the non-spec loop or a human should settle it —
EVIDENCE: spec/28_communication-channels.md:163-165; spec-changes.md:424-426; spec-changes.md:347-349.

MISTAKE: I spent a third of the pass re-deriving the standing inventories before reaching the carrier text.
The standing context is right that a further empty run of this lens buys nothing on the inventories. The
one place it was NOT complete is inside a cited comment's own sentence structure, which is exactly where
run 6's citation lens said the surviving defects of this class live. Next edit-sites run: skip the token
greps entirely and read every carrier the staging names, in the file, sentence by sentence.


### [spec.4.review-feasibility.1]

DECISION: returned empty — BECAUSE every actor-action claim in the staged spec edits resolves to a component that
exists under that name and can perform the action, and the three tensions I worked up all land in rationale prose
that reaches no line under `spec/`, which is the class this loop's material skeptic has refuted eight times on this
proposal. ALTERNATIVES: filing the §28.6 "The constraint excludes a second replica" disposition (see WATCHOUT below)
and filing the §10.1.1 baseline sentence against the barrier's cache-fallback zero; both rejected as rationale-only.

FACT: the barrier's generation really is fixed at target-set assembly, so staged §10.1.8 step 1's provenance sentence
is exact. `MirrorTargetLister.Targets` copies `le.CoordinationGeneration` off each `coordination_lease` mirror row
into the `Target`, and `PodDispatcher.Send` passes `t.CoordinationGeneration` straight to the RPC — the dispatcher
never re-reads and the draining replica never stamps its own value — EVIDENCE: pkg/gateway/coordination/barrier/wiring.go:49,
:104-114.

FACT: the shipped `coordinator_connection_lost` slog line ALREADY carries `started_sessions` beside `last_generation`,
so SPEC-1's §10.1.4 restatement ("names the number of started sessions and carries no generation") is a deletion of one
key rather than the introduction of a new one, and the adapter already has the accessor — EVIDENCE:
pkg/adapter/holdstate.go:114-132 (`enterHoldState` reads `s.startedSessionCount()` at :119 and emits both keys at :129-132).

FACT: D5's arming claim is exact and cheap to re-check. `onCoordinatorChannelClosed` has exactly one production caller,
the deferred close of the `AdapterEvents` stream handler, and the handler refuses a second concurrent stream with
`FailedPrecondition`, so the pod genuinely has one arming signal and it names no session — EVIDENCE:
pkg/adapter/adapterevents.go:107 (the sole call), :92-96 (the refusal), pkg/adapter/holdstate.go:79-100.

FACT: `bumpCoordinationGenerationOnSnapshotClose` is a bare `row.CoordinationGeneration++` inside a store `Update`,
so SPEC-3's "advances the counter from whatever value the row holds" is exact and the baseline does not perturb it —
EVIDENCE: pkg/gateway/sessionserver/start.go:4452-4456.

FACT: the anchor arithmetic in `spec/10` that SPEC-1 leans on, re-resolved this run: :30 is the §10.1.1 "Generation
counters" bullet, :37 step 1, :38 step 2, :41 step 3, :58 the Hold-state-timeout bullet carrying the local-disk
post-mortem, :60 the Observability bullet, :62 the "Whole-pod connection loss" paragraph, :183/:184/:185 §10.1.8
steps 1/2/3, :198 the closing interruption bound. `spec/04:200` is the §4.2 session-record paragraph and it does
carry the §7.2 snapshot-close bump SPEC-3 names.

WATCHOUT: `spec/04:712` is a fifth `spec/04` carrier the settled docs inventory does not spell out by line — the §4.7
RPC table row for `CoordinatorFence`, which reads "Precondition for any subsequent operational RPC." It reads as
falsified by D7 (a barrier served with no prior fence) and is not a filable site: it is a sender-side duty restating
§10.1.2 step 2's bar on the acquiring coordinator, which is the non-site arm of SPEC-2's own membership criterion, and
`docs/reference/adapter-contract.md:69` carries the identical sentence and is already recorded as pre-existing drift.
Do not spend a verification on it — EVIDENCE: spec/04_system-components.md:712.

WATCHOUT: SPEC-2's ground for leaving §28.6's "The constraint excludes a second replica" alone is that "the lease
alone excludes the second replica for a session the pod holds no generation for", which sits in tension with §10.1.1's
"This prevents split-brain even under lease/lock race conditions" — the stamp exists precisely because the lease can
race, and D6/D7 make the stamp inert for a whole session class. Weighed and not filed: the clause lands in no spec
line, the applied disposition (leave the sentence) is right under either ground, and the substantive half is already
in front of the human as OD12. A later lens that wants it must argue an applied sentence, not the ground —
EVIDENCE: spec/28_communication-channels.md:1685-1686, spec/10_gateway-internals.md:30.

WATCHOUT: the staged §10.1.1 baseline sentence ends "so every generation a pod validates is positive". The barrier's
cache fallback seeds the target's generation at literal 0 and overwrites it only on a successful session-row read, so
a Postgres fault puts a 0 on the wire that the pod's non-positive guard then refuses. Weighed and not filed: the same
paragraph keeps that guard as "the fail-closed backstop for a value the gateway should never send", so the applied
text is coherent, and the standing `### Settled` bullet already records the fallback zero — EVIDENCE:
cmd/lenny-gateway/httpsurface.go:592-599, pkg/adapter/coordination.go:224-226, spec-changes.md:255-257.

USEFUL [`### Settled` "A barrier's generation comes from shared state, never from the sending replica"]: it named
`wiring.go:104-114` as the read site, which turned a provenance re-derivation into two `sed` calls.
USEFUL [`### Traps` "reading this review log"]: reading the log with the Read tool at explicit offsets rather than
`sed` saved the truncation detour it warns about.



### [spec.4.review-fresh.1]

DECISION: Returned an empty findings list on the staged spec edits — BECAUSE every candidate I derived independently resolved to one of three
classes: already-refuted (the §28.8 `CH-BARRIER` disposition clause, the `## 5. Proposed changes` fill-the-blanks header, the "no second
value" clause, the mirror-lag/OD10 ground, the §29.10 quiescence-unit clause), already routed to the human as an open decision (`spec/04`
§4.1's pod-scoped `CoordinatorFenceRequest` row, the barrier gate's operator, `coord.mu`), or explicitly weighed-and-declined in the standing
context (SPEC-1's "each takes the acceptance sentence above" gloss, the §28.8 `CH-FENCE` window-clause granularity, the three-sentence
`CoordinatorFenceResponse` comment, the §29.8 dangling-comma and ordinal slips) — ALTERNATIVES: filing the `coordinator_lost` log line as a
dangling artifact in staged §10.1.4 text, and filing the SPEC-1 `:249-250`/`:284-285` "column default" DEFERRED; both rejected as
rationale-level imprecision the DEFERRED itself calls "not false", which the material skeptic has refuted five times on this proposal.

FACT: `diff -rq` between `scratchpad/cp-snap/0076-run6/spec-r4` and the proposal directory is EMPTY. Run 5's fix round landed nothing new
into the two staging files after the snapshot, so "read the changed sections first and hardest" had no target in this round either. That is
now four consecutive rounds — EVIDENCE: scratchpad/cp-snap/0076-run6/spec-r4 vs proposals/0076_.../ (byte-identical).

FACT (re-derived from scratch, all resolve, do not re-derive): every `spec/` anchor SPEC-1, SPEC-2, and SPEC-3 quote — spec/10:30, :37, :38,
:40, :41, :57, :58, :60, :183, :184, :198; spec/28:237-240, :251-253, :291-296, :314-317, :329-332, :333-336, :349-353, :354-357, :361-365,
:1669-1677, :1679-1690, :1805, :1806, :1807, :1808; spec/29:1150-1152, :1186, :1259-1264, :1274, :1307-1313, :1322-1326, :1461-1470,
:1472-1517, :1523-1527, :1528-1535; spec/04:175, :188, :200, :712. Every proto anchor: :153-162, :161-162, :165-179, :1442-1446, :1449-1451,
:1455-1462, :1469-1474, :1475-1483, :1477-1479. Every code anchor in §2 D5/D6/D7 and §7: adapter/holdstate.go:39-44, :90-100, :107-112,
:172-176, :192; adapter/adapterevents.go:80-96; adapter/coordination.go:89, :92, :93-94, :99, :108, :112-113, :108-121, :216, :223, :224-226,
:228-231, :236-239; adapter/slotsession.go:267; coordfence.go:147-153; start.go:3975, :4067, :4233-4245, :4237; coordination.go:399, :430;
coordination_seams.go:155-160, :233; migrations/0050_session_record_fields.up.sql:38-39.

FACT: the `spec/04` §4.1 escape hatch is worth reading in full before anyone reopens OD3. `:151` says the classification "is declared in the
table below rather than derived from a message's field set", and `:190` shows `ShutdownRequest` declared session-scoped while its handler
runs a whole-pod scrub. So §4.1's labels are declared rather than effect-derived, which is why the per-session fence does not mechanically
falsify the `pod` row at `:175`, and why no earlier lens filed it — EVIDENCE: spec/04_system-components.md:151, :175, :188, :190.

USEFUL [Settled: "Derived inventories. Do not re-derive any of these."]: saved a full re-resolution pass. I spot-checked roughly a third of
it at random (the §28.5.1 card bullet ranges, the §28.8 row cells via `awk -F'|' '{print $6}'`, the §29.8 step-2/7/9 spans, the proto message
comment ranges, the coordfence floor) and found no drift, which is evidence the inventory is trustworthy rather than stale.

USEFUL [Traps: "count the sentences in a §28.8 cell before believing a bullet's 'the cell's X sentence is unchanged'"]: the `awk -F'|'
'NR>=1805 && NR<=1808 {print NR" ||| "$2" ||| "$5" ||| "$6}'` recipe prints channel, exclusivity cell, and operator-observable cell in one
call and is the cheapest way to read those rows.

WATCHOUT: the SPEC-1 §10.1 and §10.1.4 blocks (spec-changes.md:220-292) mix staged sentences with rationale carrying code citations in the
same paragraph, and the transition is unmarked. `Zero names no fence: §10.1.2 step 1 increments ... so no CoordinatorFence ever carries zero`
(`:282-284`) reads as staged text while the `That is held by ...` sentence after it (`:284-290`) is plainly rationale, since it cites three Go
files. An applier has to judge the boundary. Two lenses have now noticed this and neither filed it; it is a hygiene item for the human pass
rather than a defect, but it is the single most likely place a maintainer applying tomorrow guesses wrong — EVIDENCE:
0076...spec-changes.md:275-292.

UNVERIFIED: whether the staged §10.1.4 Observability sentence naming "The per-session `coordinator_lost` log line" leaves a dangling artifact
in the applied spec. §10.1.4 introduces `coordinator_lost` only as a *reason* on a `session.terminated` event (spec/10:58); the log line by
that name exists only in `pkg/adapter/holdstate.go:225-229`. I weighed and declined it as documentation precision. The standing `### Open`
item "`coordinator_lost` log line as a spec artifact" already owns it; whoever closes that item should decide whether the staged sentence
names the event's reason or introduces the log line, because those are two different edits.


### [spec.4.review-kubernetes.1]

DECISION: returned an empty findings list, for the third consecutive run of this lens — BECAUSE the staged
spec text (SPEC-1/2/3) reaches no CRD, controller, status subresource, finalizer, admission webhook, RBAC
binding, informer, work queue, or leader election; the whole change lives on the gateway-to-pod gRPC
surface, the Postgres `sessions.coordination_generation` column, and the Redis/Postgres `REG-COORDLEASE` —
ALTERNATIVES: rejected filing the D7 residual (a superseded draining replica quiesces a live coordinator's
session for up to the 90s ack deadline), because that path has no controller, reconcile, or work queue on
it and the log already routes it to the human-review pass; rejected filing `spec/04:461` as a missed edit
site, see the FACT below.

FACT: `spec/04_system-components.md:461` is the one `spec/04` sentence a Kubernetes reading flags and it is
correctly a non-site. It says active-session checkpointing on an admin `DrainPool` is "coordinated by the
gateway, not the controller", that stale-coordinator fencing "is already handled by the
`coordination_generation` CAS mechanism specified in §10.1", and that "the controller only removes the
finalizer after the gateway has written the session to a terminal state". It defers to §10.1 for the rule
and fixes no compared value or unit, which is exactly SPEC-2's own non-site arm
(spec-changes.md:306-309). A "stale coordinator" in that sentence presupposes a successor's fence has
landed, so the never-fenced class D6/D7 open does not reach it, and D7 removes refusals rather than adding
them, so no new path wedges the gateway's terminal write and no finalizer can stick. Do not re-derive.

FACT: the whole CRD/chart/apis carrier check is one command and it is empty.
`grep -rlnE "coordination_generation|coordinationGeneration" charts/ schemas/ pkg/apis/ docs/` returns
`docs/getting-started/concepts.md` and `schemas/lenny-adapter.proto` and nothing else — EVIDENCE: run in
/home/ec2-user/lenny at run 6 spec round 2. That closes the single-writer / field-manager /
status-as-inbox / CRD-as-message-bus half of this lens outright.

FACT: `spec/06_warm-pod-model.md` and `docs/reference/state-machines.md` contain no match for
`hold state|coordinator_hold|coordinator hold|coordinator_lost|coordinator_connection_lost`, so SPEC-2's
§29.10 reclassification of the hold as pod-shared has no pod-state-machine mirror to falsify. The only
tracked test naming `per_slot_substate` is `tests/tier0_static/spec_map_slot_address_registration_test.go`,
whose sole §29.10 reference is a heading-existence assertion at `:1486`, which SPEC-2 does not move.

FACT: the four `spec/10` anchors SPEC-1 leans on all resolve verbatim: `:30` Generation counters, `:37`
step 1 CAS, `:38` step 2's "the pod still accepts RPCs carrying the previous generation", `:41` step 3's
"The pod accepts only RPCs whose generation matches the fenced value", `:58` the hold-timeout bullet
carrying the local-disk post-mortem, `:60` the Observability bullet, `:62` the whole-pod connection-loss
paragraph SPEC-1's D5 cites for `resume_pending` and the whole-pod replacement trigger, `:183` step 1's
closing "Pods receiving a barrier..." sentence, `:184` step 2, `:198` the interruption-window bound.
Re-checked this run; none had drifted.

WATCHOUT: `diff -rq scratchpad/cp-snap/0076-run6/spec-r4 proposals/0076_.../` returns EXIT=0 — the whole
proposal directory is byte-identical to the round-4 snapshot, so "read the changed sections first and
hardest" pointed at nothing this round. Check `ls scratchpad/cp-snap/0076-run6/` (eight snapshots exist)
rather than concluding the snapshot tooling broke.

USEFUL [spec.3.review-kubernetes.1]: its three FACTs (no CRD carrier, the §10.1.4 zero-RBAC sentences being
untouched, the spec/18 Phase 4 / Phase 8 placement) were each re-verified in a single command this run and
each held, which is most of why this pass was cheap.

USEFUL [standing context, "Two lens-scoping facts worth one grep each"]: the
`sandbox|kube|controller|webhook|finaliz|CRD|status\.|apiserver|reconcil|informer|etcd|leader` grep over
the staged text is the right first move and returns nothing; widening it with `|lease|admission` returns
only `REG-COORDLEASE`, `ExtendCredentialLeaseRequest`, and the word "admission" used of the adapter's fence
handler, none of them Kubernetes objects.

OPEN: this lens has now returned empty on three separate runs over text that has not moved since round 4.
Unless a fixer introduces a CRD, controller, or status surface, it has nothing left to find here.


### [spec.4.review-mechanism.1]

DECISION: returned an empty findings list — BECAUSE `diff -rq` against
`scratchpad/cp-snap/0076-run6/spec-r4` reports NO differing file at all (not even the review log at the
moment I ran it), so the staged text is byte-identical to what `[spec.1.review-mechanism.6]` and
`[spec.2.review-mechanism.1]` already swept, and every candidate I generated from primary sources is
already in `### Settled`, `### Traps`, the refuted-class list, or routed to a summary open decision —
ALTERNATIVES: I worked up five candidates and killed each; they are named below so a later mechanism lens
does not re-spend the round.

FACT: on this proposal the whole-directory `diff -rq` can return literally nothing. Two earlier mechanism
shards recorded "only the review log differs"; this run had zero differing files. When that happens the
honest scope is the standing context plus independent re-derivation of primary sources, and the expected
answer is empty — EVIDENCE: `diff -rq /home/ec2-user/lenny/scratchpad/cp-snap/0076-run6/spec-r4
/home/ec2-user/lenny/proposals/0076_fix_scope-the-coordination-generation-to-the-session` exits silent.

FACT: every anchor and code citation I re-opened resolves exactly, re-derived rather than trusted.
`spec/10_gateway-internals.md`: :30 generation-counter bullet, :37 step 1 CAS, :38 step 2 window, :41 step 3
(three sentences: stamp / "accepts only RPCs whose generation matches the fenced value" / the no-window
claim), :58 hold timeout, :60 Observability, :183 §10.1.8 step 1 (both staged anchors on one physical line).
`spec/28_communication-channels.md`: `CH-FENCE` Messages 314-317, Exclusivity 329-332 (window clause on
330-331), Degradation 333-340; `CH-BARRIER` Messages 349-353, Preconditions 354-357, Exclusivity 361-365;
§28.6 "One holder per session" 1669-1677 and "The second opener" 1679-1690; §28.8 rows 1805 `CH-ATTACH`,
1806 `CH-CHECKPOINT`, 1807 `CH-FENCE`, 1808 `CH-BARRIER`, read with `awk -F'|' '{print $5}'`.
`spec/29_communication-scenarios.md`: §29.7 framing 1150-1152, step 4 :1186, §29.8 Preconditions 1259-1264
(the staged-for-deletion clause is on 1261), step 2 1269-1276, step 7 1307-1313, step 9 1322-1326, §29.10
"Partitioned per slot" ending 1465-1470, the removed "does not state" bullet 1523, the narrowed
`Interrupt`/barrier bullet 1528-1535. `pkg/adapter/coordination.go`: :89 and :216 `checkSessionBound`, :92
and :223 generation reads, :93-94 and :224-226 non-positive guards, :99 `initialized && gen <= lastFenced`,
:108 `gap :=`, :236-239 `!initialized || gen != fenced`. `pkg/gateway/coordination/coordfence/coordfence.go:
147-153` is the `gen <= 0 -> gen = 1` floor; `pkg/gateway/sessionserver/start.go:4237` is the sole
`s.fencer.Fence` on the resume path and it does not increment.

FACT: the five candidates I rejected, with the ground that killed each.
(1) The staged §10.1 sentence "every generation a pod validates is positive and is strictly greater than
the value carried before the takeover that fenced it" against the resume path, which fences at the value it
reads with no increment (`start.go:4233-4245`), so for a resume-fenced-and-never-taken-over session there
is no takeover and the validated generation equals the pre-fence carried value. Killed because `spec/`
describes no fence outside §10.1.2's post-increment sequence, so read as spec text the definite description
always has a referent; the falsification needs the shipped resume path, which is spec-versus-code drift the
proposal does not stage. It is recorded as an UNVERIFIED below rather than filed.
(2) SPEC-3's "the first coordinator handoff for that session mints 2" against `Sweeper.RecordHandoff`
bumping a rolling-window row from 0 to 1: the whole rolling-window row class is the adjudicated OD8/OD2
territory, and §10.5's mixed-version rule already governs a transient population.
(3) SPEC-3's same sentence against the §7.2 snapshot-close bump reaching the row first: unreachable, the
bump fires only under a terminal write after which no takeover follows (already in `### Settled`).
(4) The staged §29.10 "Shared by the whole pod" hold bullet's "Loss of the gateway-to-pod connection is a
whole-pod failure" against the §29.10 transport bullet's "one connection per gateway replica per pod": the
new bullet mirrors `spec/10:58`'s shipped "Whole-pod connection loss when `maxConcurrentSessions > 1`"
paragraph verbatim in substance, so the tension is shipped text rather than staged.
(5) The staged §10.1.8 step-1 provenance sentence ("read from the session's coordination state when the
barrier-target set is assembled") sitting in the same paragraph as the shipped "carries the current
`coordination_generation`": the currency gap is the `upsertMirror` pre-bump snapshot defect (OD10), a code
condition, and as normative spec text the mirror is meant to hold the row's value, so the two sentences do
not conflict inside `spec/`.

FACT: `spec/04_system-components.md:175` classifies `CoordinatorFenceRequest` scope as `pod` and `:188`
states "carries `session_id` and stays pod-scoped, which is why the classification is declared rather than
derived". That is a live tension with D1/SPEC-1 and it is NOT mine to file: summary OD3 routes it to the
human reviewer (`summary.md:219-239`, and `:121` for the 0075 knock-on). A mechanism lens that rediscovers
it should stop at the OD3 pointer.

UNVERIFIED: whether staged §10.1's "strictly greater than the value carried before the takeover that fenced
it" should be narrowed to the takeover population explicitly. The clause's load-bearing job (the first
takeover's fence separates predecessor from successor at 1 versus 2) is true; only the universal framing
over "every generation a pod validates" reaches the resume-path fence, which the proposal itself names as a
fence driver that does not increment (spec-changes.md:55-57). Whoever fixes the OD-lane wording should
decide whether to scope it; nothing depends on the answer. EVIDENCE: spec-changes.md:230-238 for the staged
sentence, pkg/gateway/sessionserver/start.go:4233-4245 for the no-increment fence.

USEFUL [`### Traps`, "WATCHOUT: a late pass number in the spec-changes file is not evidence that the staged
spec text changed" and its `diff -rq` instruction]: it scoped this run in one call, for the third
consecutive mechanism shard.
USEFUL [`### Traps`, "WATCHOUT: the §28.8 rows are single physical lines with pipe-separated cells ... read
it with `awk -F'|' '{print $5}'`"]: this is the only way the exclusivity cells are readable, and it made the
four `CH-*` cell verifications a single command.
USEFUL [`### Settled`, "Known sub-line citation drifts that must not be filed"]: `slotsession.go:267`
(D6) versus `:268` (§4) for one `checkSessionBound` whose doc comment ends at :267 is exactly that class; I
would have spent a verification on it.


### [spec.4.review-open-decisions.1]

DECISION: filed exactly one finding — SPEC-1's staged §10.1.8 replacement asserts "Either outcome is
safe and requires no special handling" over the acceptance arm, which the summary's OD12 records as
unanswered with no recommendation — BECAUSE it is the only open-decision defect this round whose remedy
is a sentence that LANDS IN `spec/`, and every other candidate's remedy is in `summary.md` or
`non-spec-changes.md`, the class the material skeptic has now refuted ten consecutive times on the
ground "lands in no file under `spec/`" — ALTERNATIVES: §7 omitting OD5 (the decision that most changes
the staged spec text and the only one absent from the staged file's own decision list); OD7's
recommendation naming a SPEC-1 sentence SPEC-1 does not stage; OD9 being a decision this proposal does
not need before Approved while its section header says every entry does; OD5's "becomes an ordinary
reachable state" being false because the state is already ordinary. Each is real, each lands in
`summary.md`, each would have been refuted.

FACT: OD12 is HALF RESOLVABLE against the tree, and the half that resolves reverses its framing.
`dispatchOne` starts the gateway-driven `Checkpoint` stream in a goroutine BEFORE `dispatch.Send` and
joins it after, unconditionally and whatever the barrier returns, so the answer to OD12's "whether an
accepted false-positive barrier is followed by a stream at all" is: the stream is not a consequence of
acceptance at all. A superseded replica opens that stream against the pod today, in the shipped tree,
whether its barrier is accepted, refused, or never sent. D7 changes nothing about the stream; the only
residual acceptance creates is the quiescence held to the barrier-ack deadline — EVIDENCE:
pkg/gateway/coordination/barrier/barrier.go:210-226 (comment at :210-217, `cpWG.Add(1)` at :220,
`CheckpointWithTrigger` at :223, `dispatch.Send` at :226).

MISTAKE: the `### Settled` D7 bullet calls the "either outcome is safe" phrasing SHIPPED. It is STAGED.
`spec/10_gateway-internals.md:183` closes "reject the barrier as a generation-stale RPC under the normal
fencing rules — this is safe and does not require special handling", asserting safety of the REJECTION
outcome alone; "Either outcome is safe and requires no special handling" is SPEC-1's own new sentence at
`spec-changes.md:216-217`. Reading the bullet as describing shipped text is what let three rounds treat
the widened assertion as pre-existing and out of scope.

FACT: OD3's premise is contestable rather than settled, and the counter-argument is inside SPEC-2's own
staging. `spec/04_system-components.md:188` reads "`CoordinatorFenceRequest` carries `session_id` and
stays pod-scoped, which is why the classification is declared rather than derived", and §4.1's opening
paragraph (`:151`) declares the classification rather than deriving it from effects. The fence keeps a
genuine pod-wide effect after SPEC-1: SPEC-2's staged §29.10 "Shared by the whole pod" bullet states that
"A successful fence for any one of those sessions exits the hold for the pod"
(`spec-changes.md:443-448`). So "stays pod-scoped" survives on a ground OD3 itself names, and OD3 is a
genuine reviewer judgement rather than a resolvable one. Do not conclude it is resolvable without
re-opening `spec/04:151` and `:188` in full.

FACT: `spec/07_session-lifecycle.md:196` reads "resuming → running (re-attach succeeds on replacement
pod; internal-only transient ...)", so the specification puts a resumed session on a REPLACEMENT pod and
OD7's rebind-onto-the-same-pod is unreachable in specification terms. The code-side placement half is
still unchecked. Re-verified this pass; the earlier ledger entries [spec.2.review-open-decisions.1] and
[spec.3.review-open-decisions.1] are right.

WATCHOUT: OD7's recommendation is "have SPEC-1 state that the value's lifetime is the session's binding
on the pod rather than the session itself" (`summary.md:293-295`), and SPEC-1 as staged says the opposite
unit: "The value is unset until that session's first accepted fence on that pod"
(`spec-changes.md:142-143`), matching D6 (`:31-32`). The two are extensionally equal only while rebind on
the same pod is unreachable. This is the proposal's normal convention (an unaccepted recommendation is
not staged, as OD2's is not), so it is not a defect; a reviewer who accepts OD7 changes a SPEC-1
sentence, and that consequence is stated nowhere.

UNVERIFIED: whether §7 should carry OD5. §7 "Open decisions for review" lists three items and none is
OD5, yet answering OD5 "split" deletes SPEC-3 entirely and SPEC-1's §10.1 baseline paragraph
(`spec-changes.md:231-243`) — the largest effect any open decision has on the staged spec text. A
reviewer reading only the staged spec file sees no sign that a whole deliverable is conditional. Remedy
is one bullet in `spec-changes.md` §7, so this one IS landable in a spec round; not filed because the
summary carries it and the §7-redundancy class has two refutations against it.

OPEN: whether a summary-only `## Open decisions` defect can ever be landed in a spec-scoped round. This
is the fourth consecutive round to record the same collision (see [spec.3.review-open-decisions.1]'s
closing OPEN). The cheap fix a human could make: let the spec lane's fixer edit `summary.md`'s OD
section, or move this lens to the non-spec lane.

USEFUL [`### Settled` D7 bullet, "Round 6 declined to file the §10.1.8 step-1 imprecision ... so it
stands for the human-review pass"]: it named the exact site and routed it to this lens, which is the only
reason the widened safety assertion was found rather than re-derived.

USEFUL [`### Traps` "a filed finding is not a fixed finding"]: reading the live file rather than the
standing entry is what showed the "either outcome" phrasing is staged rather than shipped.


### [spec.4.review-operational.1]

DECISION: Filed exactly one finding, on SCHEMA-1's prescription for the `CoordinatorFenceRequest` message
comment (spec-changes.md:508-512): the proposal paraphrases that comment's record-and-reject sentence
without its operational tail and then hands the applier a complete replacement sentence, so applying
SCHEMA-1 deletes the only tracked non-Go statement of the fence rejection's gRPC status, its
`coordinator_handoff_stale` detail string, and the `lenny_coordinator_handoff_stale_total` counter the
gateway increments for it — BECAUSE `schemas/` is inside criterion (d), the proto is a §28.7 published
artifact for external runtime authors, and `spec/` and `docs/` carry the detail string and the status code
nowhere — ALTERNATIVES: filing the same shape against the three barrier carriers (rejected: SPEC-2's
barrier paragraph at :536-541 scopes its edit to "the comparison" and says the message-level and
field-level text "do not disagree about the comparison", so an applier plausibly keeps
`FailedPrecondition` there); filing the pod-level `coordinator_connection_lost` losing its generation as an
operator-visibility regression (rejected: deliberate under D5/CODE-3, both carriers staged, and the
per-session `coordinator_lost` records carry the value).

FACT: `spec/` and `docs/` state NO gRPC status code and NO error-detail string for the fence or barrier
generation rejection. `grep -rn "FailedPrecondition\|FAILED_PRECONDITION" spec/ docs/` returns only
setup-command hits (spec/07:208, spec/15:1136, docs/api/internal.md:499), and `coordinator_handoff_stale`
appears in `spec/` and `docs/` nowhere; only the metric name does (spec/10:71, spec/16:183,
docs/reference/metrics.md:307). The proto message comments are the sole tracked carriers — EVIDENCE:
schemas/lenny-adapter.proto:1445-1446, :1472-1473, :1478-1479; pkg/adapter/coordination.go:106, :238.

FACT: the observability surface this proposal touches is genuinely closed, re-derived independently this
round. One coordination alert (`CoordinatorHandoffSlow`, pkg/alerting/rules/rules.go:1583-1587) whose
runbook mentions no generation, fence, or hold (docs/runbooks/coordinator-handoff-slow.md:32).
`lenny_adapter_coordinator_hold` is registered with a nil label set, so "the gauge remains pod-scoped"
is true by construction (pkg/adapter/metrics.go:107-111). The whole `docs/` + `charts/` surface for
`coordinator_hold|coordinator_connection_lost|coordinator_generation_gap|last fenced` is ONE line,
docs/reference/metrics.md:309. `coordinator_connection_lost` has exactly two `spec/` carriers
(spec/10:60, spec/29:1274) and SPEC-1 and SPEC-2 stage both.

FACT: §28.8 row fields under `awk -F'|'`: NF=7, so [5] is "Holder of the exclusivity constraint changes"
(the column SPEC-2 stages) and [6] is "Operator observable". Recipe:
`awk -F'|' 'NR>=1805 && NR<=1808 {print NR" "$6}' spec/28_communication-channels.md`. The three
Operator-observable cells SPEC-2's edited rows carry (CH-CHECKPOINT `partial = true` +
`lenny_checkpoint_storage_failure_total`; CH-FENCE `coordinator_generation_gap` + `coordinator_lost`
termination; CH-BARRIER `manifest_reason="timeout"` + the three barrier metrics) all stay true after the
staged edits and after D7. No edit is owed there — EVIDENCE: spec/28_communication-channels.md:1803,
:1805-1808.

WATCHOUT: the proposal states preservation explicitly wherever it means it — ":516 it keeps its wording",
":519 each of its two sentences takes the session qualifier and nothing else", ":562-565 AttachRequest's
and CheckpointRequest's trailing sentences ... are unchanged", and the whole `CoordinatorFenceResponse`
carve-out at :527-534. A carrier paragraph that gives a full replacement sentence with NO preservation
clause is therefore an instruction to replace the whole sentence, not a hint to change only the unit.
Use that asymmetry when judging the remaining SCHEMA-1 carriers — EVIDENCE:
proposals/0076_.../0076_....spec-changes.md:508-512 against :516, :562-565.

MISTAKE: the standing `### Open` item "SCHEMA-1 qualifier wording ... G1 discharged the
`CoordinatorFenceResponse` carrier and the rest are open" has been parked for two runs while one of the
remaining carriers holds a live deletion of an operational contract. This is the second instance of the
log's own "routing a filed defect into a bookkeeping `### Open` item is not discharging it".

UNVERIFIED: whether the three `CheckpointBarrier` carriers (`:165-179`, `:1469-1474`, `:1477-1479`) keep
`FailedPrecondition` under SPEC-2's :536-541 instruction. I read the instruction as scoped to the
comparison and declined to file; a fixer touching :508-512 should state the barrier carriers' disposition
in the same pass rather than leaving the ambiguity.

FACT: `diff -rq` of the whole proposal directory against
`/home/ec2-user/lenny/scratchpad/cp-snap/0076-run6/spec-r4` returned NOTHING this round. The staged text
has not moved, so a "read the changed sections first" instruction had no target, and the standing
"lens exhaustion, counted" bullet applies: this lens returned empty in run 6 round 1 over the same bytes
and the one finding above came from opening the proto comments rather than from any change.


### [spec.4.review-performance.1]

DECISION: Returned empty. — BECAUSE the staged spec text is byte-identical to the r3/r4 snapshots
(`diff -rq scratchpad/cp-snap/0076-run6/spec-r4 proposals/0076_...` is rc=0, and only the review log and
its archive differ from `spec-r3-start`), and re-deriving the quantitative half from primary sources
reproduced the standing `### Settled` "The staging is write-neutral" conclusion rather than contradicting
it. — ALTERNATIVES: filing the barrier-quiescence wall-clock delta D7 introduces (rejected: identical
before and after, see FACT below), and filing the added `sessioncheckpointmeta.Upsert` per acked barrier
(rejected: one extra round trip inside an already-400-wide per-drain fan-out, and D7 deletes a whole
sequential 400-session capture pass in the same drain).

FACT: D7 strictly REDUCES top-tier drain cost; it does not add a barrier-window bottleneck. `dispatchOne`
launches `CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and joins it after, whatever the
barrier returns, so the gateway-driven upload already runs and is already joined inside the single 90s
`Dispatch` deadline for the refused population today. What D7 changes is `out.Acked`, and prestop's
post-barrier loop skips a session iff `barrierAcked[sess.SessionID]` — and that loop is SEQUENTIAL by its
own comment. So a Tier-3 replica draining 400 never-handed-off sessions today runs the 400 concurrent
barrier-window captures AND then 400 sequential post-barrier captures; after D7 it runs the first set only.
That relieves, rather than aggravates, the named failure mode "the gateway preStop drain will exceed its
tier-specific drain timeout (Tier 3: 120s) and the kubelet will SIGKILL pods mid-checkpoint".
— EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:216-226, :243-244, :235-255 (the Upsert on Acked);
pkg/gateway/podlifecycle/prestop/prestop.go:382-397 ("Iterate sessions sequentially", the `barrierAcked`
skip), :503-506 (one `context.WithTimeout(ctx, h.barrierAckTimeout())` around the whole Dispatch);
spec/10_gateway-internals.md:184 (single wall-clock deadline, 400 simultaneous MinIO uploads);
spec/17_deployment-topology.md:1225, :1229 (30 × 400, the 120s Tier-3 drain timeout).

FACT: the §12.4 durable-fallback bar is met for the one register staged §28.6 leaves as sole exclusion
guard for the never-fenced class. `REG-COORDLEASE` is Redis on a compare-and-set with a 60s expiry, and
§28.5.1's `CH-FENCE` and `CH-ATTACH` Exclusivity bullets both name the Postgres fallback explicitly as
`SELECT ... FOR UPDATE SKIP LOCKED` on the session row. So a Redis reset does not leave the never-fenced
population with no exclusion primitive. — EVIDENCE: spec/28_communication-channels.md:137 (register row),
:246-248, :330-331.

FACT: the Postgres-failover trace for the barrier degrades to exactly today's behaviour rather than worse.
`MirrorTargetLister.Fallback` seeds `gen := int64(0)` and overwrites it only on a successful
`w.sessions.Get`, so under a full outage the barrier carries 0, the adapter's non-positive guard refuses it,
`Acked` stays false, and prestop's fallback capture runs. CODE-4's baseline of 1 improves the healthy path
and leaves the fallback path where it is. — EVIDENCE: cmd/lenny-gateway/httpsurface.go:588-602.

WATCHOUT: that same fallback closure calls `w.sessions.Get(context.Background(), ...)` — no deadline — once
per target inside the 90s `Dispatch` budget, so a hung Postgres can consume the drain budget there. It is a
shipped code defect, it is untouched by this proposal, and its remedy is in `cmd/lenny-gateway`, so the spec
loop cannot file it. — EVIDENCE: cmd/lenny-gateway/httpsurface.go:593.

USEFUL [`### Settled` "The staging is write-neutral"]: it named the four things a performance lens must
check (per-unit writes, hot keys, informer caches, single-leader serialisation) and was right on all four;
re-deriving it from `barrier.go` and `prestop.go` cost about half the pass and produced no correction. This
is now the FOURTH empty performance return over byte-identical staged text. Retiring the lens on an empty
return would cost nothing here.


### [spec.4.review-reliability.1]

DECISION: Returned empty, the reliability lens's fifth empty return on this staging — BECAUSE `diff -rq` against `scratchpad/cp-snap/0076-run6/spec-r4` returns nothing at all, so no staged sentence has moved since the last reliability pass, and every recovery-shaped lead I re-derived from primary sources resolves to something already refuted, weighed-and-declined, or pre-existing in the shipped tree — ALTERNATIVES: filing the accepted-false-positive barrier's quiescence of a session the draining replica no longer coordinates (rejected: shipped equality already accepts it whenever the mirror carries the pod's value, so D7 adds only the unset arm, and the review log records the round-6 decision to leave it for human review); filing the acked-but-uncaptured prestop skip widening to the never-handed-off population (rejected: recorded as pre-existing, and the skip is what shipped §10.1.8 mandates); filing the fence-retry-at-the-same-generation non-idempotency (rejected: pre-existing, code-lane remedy, and already a standing `### Open` routed to OD2).

FACT: The D7 acceptance arm cannot wedge the drain. `CheckpointBarrier` blocks in `select { case <-done: case <-ctx.Done() }`, so an accepted barrier whose gateway-driven `Checkpoint` stream never arrives is bounded by the RPC deadline the gateway sets from `checkpointBarrierAckTimeoutSeconds`, and the deferred clear at `:253-257` releases quiescence on any return. A reliability lens tempted to file "accepted-when-unset barrier has no reclaimer" should stop here — EVIDENCE: pkg/adapter/coordination.go:264-269, :253-257.

FACT: `coordfence.fence`'s stale branch is not a bare relinquish. It re-reads the authoritative generation and RETRIES at the new value when `newGen > gen`, relinquishing only when the row has not advanced past the value it sent. The problem statement's §1.3 step 3 describes the no-advance arm correctly, but a lens that reads the stale branch as unconditionally terminal will mis-derive both the co-tenant defect and the lost-ack case — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:164-179.

FACT: The staged §28.6 second-opener and the staged §28.8 `CH-CHECKPOINT` cell gate `CH-CHECKPOINT` on the same per-session value the barrier is gated on, so an accepted barrier's companion `Checkpoint` stream is accepted by the same predicate and the pair cannot disagree. The only interleaving that separates them (a fence landing between the barrier's acceptance and the stream's open) is reachable identically under the shipped equality gate, and in code the stream carries no generation gate at all, `GetCoordinationGeneration()` having two non-test call sites — EVIDENCE: spec-changes.md:344-350, :382-389; pkg/adapter/coordination.go:92, :223.

USEFUL [`### Settled` "A barrier's generation comes from shared state, never from the sending replica"]: it is what makes the false-positive-barrier and stale-mirror-window questions cheap to close, and it saved re-deriving `upsertMirror`'s pre-`RecordHandoff` snapshot read from scratch.

USEFUL [`### Traps` "Refuted classes ... (f) A fencing hole in D7" and "(j) the staged §10.1 invariant 'every generation a pod validates is positive'"]: both are exactly the shape a reliability lens generates first, and having them written with their full bodies rather than their titles is what stopped two speculative filings.

WATCHOUT: The reliability lens now has five empty returns over text that has not moved for two rounds. The only staged surfaces that could produce a new reliability finding are D7's acceptance arm, the retained `coordfence` floor's rolling-window behaviour, and §10.1.8's failure-arm claim; nothing else in the staging touches a recovery, retry, drain, or failover path. Re-running this lens buys nothing unless one of those three is rewritten — EVIDENCE: spec-changes.md:47-88, :249-257, :225-228.


### [spec.4.review-security.1]

DECISION: Returned empty, the fifth consecutive empty security return on this staging — BECAUSE the staged
text is byte-identical to the r4 snapshot (`diff -ru scratchpad/cp-snap/0076-run6/spec-r4 proposals/0076_.../`
prints nothing), and every security-lens surface I re-derived independently resolves the same way the standing
context records — ALTERNATIVES: filing the `spec/10:30` split-brain-consequence asymmetry (SPEC-1 opens the
"Generation counters" bullet to add the baseline while leaving "the stale replica discovers it is no longer the
coordinator. This prevents split-brain even under lease/lock race conditions" standing, on the same page where
SPEC-2 deletes the equivalent "cannot drive the pod" clause from twelve proto comments precisely because it is
false for D6's unset class). Declined: `### Settled` records it as weighed-and-not-filed in spec round 3
(applicability), and I have no new evidence, only a security framing of the same asymmetry.

FACT: the §12.4 durable-fallback bar is met for the one register the staged §28.6 text leaves as sole exclusion
guard for the never-fenced class, and it is stated twice rather than once. `REG-COORDLEASE` is declared Redis in
the §28.3 register-entry table, and both `CH-ATTACH`'s and `CH-FENCE`'s Exclusivity bullets spell the Postgres
fallback inline ("held in Redis on a compare-and-set with a TTL and falling back to a Postgres
`SELECT ... FOR UPDATE SKIP LOCKED` on the session row"; "the session-coordination lease `REG-COORDLEASE` with
its Postgres fallback"). §10.1's own Per-session coordination bullets carry the same Primary/Fallback pair — 
EVIDENCE: spec/28_communication-channels.md:137, :245-248, :329-332; spec/10_gateway-internals.md:28-29.

FACT: §28.6's "One holder per session" paragraph names the guard as `REG-COORDLEASE` *together with* the
`coordination_generation` stamp, so the staged unset arm genuinely reduces a two-guard exclusion to one guard for
the never-fenced class. That is not a finding, because the class is by construction the class no successor has
fenced: the only two `CoordinatorFence` senders are the resume path and the sweeper's crash-takeover re-adopt,
and both install a value before coordinating, so a second replica cannot reach the reduced-guard state without
first leaving it — EVIDENCE: spec/28_communication-channels.md:1669-1677; review-log `### Settled`
("`CoordinatorFence` has two senders and nothing fences a normally-started session").

FACT: the alert-and-runbook surface really is closed against SPEC-1's removal of the generation from
`coordinator_connection_lost`. `pkg/alerting/rules/rules.go` carries exactly one coordination alert
(`CoordinatorHandoffSlow`, a p95 on `lenny_coordinator_handoff_duration_seconds_bucket`), its runbook names no
generation, fence, or hold state, and no metric row in `docs/reference/metrics.md:307-312` carries the fenced
value — EVIDENCE: pkg/alerting/rules/rules.go:1583, :1587; docs/runbooks/coordinator-handoff-slow.md:41;
docs/reference/metrics.md:307-312. USEFUL: the `### Settled` "alert and metric surface is closed" bullet made
this a three-grep confirmation rather than a re-derivation.

FACT: the fail-closed backstop the staging leans on is in the tree exactly where SPEC-1 says it is, on both
paths, and it is what keeps the barrier cache fallback's literal 0 safe. `CoordinatorFence` returns
`InvalidArgument` on `gen <= 0` before it touches any state, and `CheckpointBarrier` does the same before its
`!initialized || gen != fenced` gate — EVIDENCE: pkg/adapter/coordination.go:92-94 (fence), :222-226 (barrier),
:232-239 (the gate). The staged sentence keeping both is at spec-changes.md:264-267.

WATCHOUT: the twelve rewritten proto field comments include the three credential RPCs
(`RotateCredentialsRequest`, `ExtendCredentialLeaseRequest`, `RevokeCredentialsRequest`), so a security lens is
drawn to read the consequence-clause deletion as removing a credential-path control. It is not: no operational
RPC is generation-gated in code at all (`GetCoordinationGeneration()` has two non-test call sites, the fence and
the barrier), so the deleted clause was aspirational on every one of the twelve and the deletion changes no
enforced control — EVIDENCE: spec-changes.md:560-566 (the twelve, named by message);
pkg/adapter/coordination.go:92, :223. Cost me one detour; do not spend another.

UNVERIFIED: nobody has checked whether `spec/07_session-lifecycle.md:215`'s parenthetical ("any subsequent
operational RPC carrying a lower `coordination_generation` is rejected") is reached by the staged unset arm. It
states a pod-side rejection rule for the §7.2 snapshot-close bump, which sends no fence to the pod at all, so it
is false in the shipped tree before any edit here and the review log's sweep classifies `spec/07` as
unit-neutral. An edit-sites lens, not a security lens, owns whether the pre-existing falsity is made worse.

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
