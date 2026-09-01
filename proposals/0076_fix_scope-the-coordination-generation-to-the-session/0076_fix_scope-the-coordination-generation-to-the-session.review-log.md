# Review log — 0076_fix_scope-the-coordination-generation-to-the-session

## Standing context

**Compaction pass 15, 2026-09-01.** Aged out the whole of non-spec round 3: two fix shards, the post-fix verification header, three review
lenses, and the verify shard. Lifted six items: the closed two-file class the S3/S4 split turns on, with the two batching directions that
turn a declared tier red; the MISTAKE that CODE-2's link site is not an S3/S5 compile break; the two memstore `Update` tests §8's class-1
enumeration omits; the class-1, class-2, and spec-phrase sweeps that are complete against the tree; the tier-3 tree's behavioural-case
precedent; and the instruction to grep §9 rather than trust that a cited file is a listed file. Three round-3 items nothing closed moved
into the residue entry: §9 naming a file for neither the staged tier-3 nor the staged tier-2 case, whether the tier-7a two-barrier case can
be written against real `Checkpoint` streams, and §8's tier sentence omitting tier 11. Round 3's own OPEN asking whether S4 runs tier 7a is
retired closed, because the same round's fix added 7a to S4, to §8's CODE-4 tier line, and to the per-deliverable line. Nothing was deleted
as superseded; no round-6 marker corrects a standing bullet. Rounds 4, 5, and 6 keep their text.
**The target of 80 lines was not reached, and the section grew: 355 lines against pass 14's 337, at the same line width.** Round 3 was a
fix round, so its residue is mostly MISTAKE and WATCHOUT reasoning rather than findings, and the rules keep all of it. Reaching 80 means
dropping whole bullets, and each is a MISTAKE carrying the reasoning that makes its dead end recognisable, a refuted class whose body a
round-5 lens asked to be kept intact, or a fact a `USEFUL` marker credits with saving a round. The five that can go first, once their
conditions are met: the derived inventories once the code and test lanes land, the carrier enumeration once SCHEMA-1's target list is
settled, the §28/§29 edit-site bullet once SPEC-2 is applied, the tier-0-gates bullet once SCHEMA-1 and migration 0181 have landed with
their stubs and their registry row, and the S3/S4 split bullet once both steps are committed.

- **The session row's counter is baselined at 1.** The row is carried unchanged and §10.1.2 step 1 is untouched, so the first takeover mints
  2; CODE-4 carries migration 0181, both session-store `Create` floors, and the deletion of `coordfence`'s zero floor. The counter has three
  writers (step 1's compare-and-swap, `Sweeper.RecordHandoff`, `bumpCoordinationGenerationOnSnapshotClose`), so any floor repeats on each,
  and `Create` inserts the value explicitly, so a column default baselines nothing. The §7.2 snapshot-close bump fires only under a terminal
  write, after which no takeover follows. MISTAKE: pass 8 put a wire value of 1 on the barrier, the one positive value a first takeover also
  produces (a row at 0 compare-and-swaps to 1), so predecessor and successor carried the same number and the first fence separated nobody. A
  value invented to satisfy a guard is checked against every other producer in the same domain, and the compare-and-swap is a producer.
  MISTAKE: pass 9 then called `tests/tier2_component/coordination/sweep_test.go` the whole test-lane consequence, and enumerated even that
  file incompletely. The consequence is a class: every assertion reading a session row's `CoordinationGeneration` after a create that left
  the field unset, across the takeover, mirror, memstore, tier-7a, and tier-8 suites. Two landed tests break outside that class, which is
  where the residue is. `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1` seeds another row's constant at 1 calibrated against a
  session row created unset, the only site of its kind in the tree, and `TestFenceZeroGenerationFencesAtBaseline` pins the very floor CODE-4
  deletes. Look at the class boundary for residue rather than inside it.
- **The `>= 1` CHECK has one write path to guard.** Both stores' `Update` re-read under lock and clamp `CoordinationGeneration` to the
  previous value (`pgstore.go:460`, `:475-477`; `memstore.go:129`, `:144-146`), so no mutate path can go below the baseline;
  `pgstore.go:170` is the only `INSERT INTO sessions` in the tree and every raw-SQL fixture omits the column, so the tightened check breaks
  no seed path; `RecordHandoff`'s 0-return sentinel survives because 0 stays impossible as a row value. This is what makes CODE-4's "both
  `Create` floors plus the migration in one commit" the complete edit set. A round-5 lens credits it with saving the writer enumeration.
- **The barrier's cache fallback still puts a literal 0 on the wire, and must not be floored.** `httpsurface.go` seeds the target's
  generation at 0 and overwrites it only on a successful session-row read, so under a Postgres fault the barrier carries 0 and is refused
  with `InvalidArgument` whatever the baseline is; the staged "unreachable by construction" claim is not exact, though the outcome is
  fail-closed. The fence path is not symmetric, its reader returning an error rather than 0, so deleting `coordfence`'s floor is safe.
  MISTAKE: CODE-4 floored the value in `dispatchOne` before the `sessioncheckpointmeta.Record` write, so a generation the gateway could not
  read would have been persisted as a fabricated 1 into the column feeding the `MAX(coordination_generation)` resume selector. A floor on a
  value the sender does not hold converts a loud refusal into a silent falsification.
- **The barrier is accepted for a bound session the pod holds no fenced generation for (D7).** CODE-2's gate becomes
  `initialized && gen != fenced` read from the slot entry, and `checkSessionBound` runs before it, so acceptance-when-unset never reaches an
  unbound session. D7 survives the baseline: the barrier carries 1 rather than 0, clears the adapter's non-positive `InvalidArgument` guard,
  and is accepted on the unset arm. Anything in the ledger reading the unset case as a refusal predates D7. Staged §10.1.8 step 1 closes
  "Either outcome is safe and requires no special handling", extending the shipped safety claim from the rejection arm to the acceptance
  arm, where a superseded draining replica quiesces a session it no longer coordinates for up to the 90s ack deadline and drives a
  `Checkpoint` stream against it. Round 6 declined to file that (nothing corrupts, `quiesced` is advisory today, and "safe" in the shipped
  sentence means "requires no special handling"), so it stands as a known imprecision for the human-review pass.
- **`initialized` moves with `lastFenced` or the defect comes back.** The exemption is a separate `initialized` bool on `coordinationState`,
  read by the stale rejection and by the gap test. Left on `Server` while `lastFenced` moves to the slot entry it flips on the first fence
  anywhere on the pod, so every later co-tenant's first fence tests initialized-true against its own zero and reports a gap. D6 is the
  specification half: the pod holds `last_fenced_generation` per bound session, unset until that session's first accepted fence there, both
  predicates applying only against a recorded value. The adapter performs no cancel-and-reset, so staged text may re-scope that requirement
  and may not assert the adapter meets it. `coordinationState` also carries `quiesced`, which has no production reader; the object carrying
  the quiescence is `barrierGate`, which CODE-1 moves onto the slot entry with the state, because §10.1.8 step 3 already fixes the barrier's
  unit at the session and a pod-wide single-slot gate cross-links two co-tenant barriers and blocks the loser to the 90s ack deadline.
- **The gap reset and the record-and-reject rule have four spec mirrors and seven proto carriers.** `spec/28` and `spec/29` restate clauses
  (a) through (d) citing §10.1, and SPEC-2 stages both. The proto carriers, all SCHEMA-1 sites: the `CoordinatorFence` RPC comment (which
  also spells the exemption per pod lifetime), the `CoordinatorFenceRequest` and `CoordinatorFenceResponse` message comments, the request's
  field comment, the `CheckpointBarrier` RPC comment, `CheckpointBarrierRequest`, and its field comment. There is no eighth:
  `CoordinatorFenceResponse.last_fenced_generation` (`proto:1465`) carries no leading comment, and the `Checkpoint` RPC comment and
  `CheckpointStart.checkpoint_id` are session-neutral and stay true under the per-entry gate. MISTAKE: a message's doc comment sits above
  the `message` line, so an evidence range starting at `message X {` misses it, which is how three rounds missed the
  `CoordinatorFenceRequest` comment at a cost of two findings. CORRECTED: the other twelve `coordination_generation` field comments are
  session-scoped but not neutral. Eleven close with "so a replica that has lost coordination cannot drive the pod (§10.1)" and
  `ShutdownRequest`'s with "cannot tear the session down", which SPEC-1's staged unset clause falsifies for exactly the session class D6
  makes ordinary, so SPEC-2's ground for excluding them is false, and a list built from the common phrase alone returns eleven and misses
  `ShutdownRequest`. Read the comments rather than the proposal's description of them; that instruction produced a round-2 lens's only
  finding, so keep it until SCHEMA-1's list is settled.
- **The coordinator-loss hold has no per-session arm and cannot be given one here (D5).** Its sole arm is the close of the pod's single
  CH-ADAPTEREVENTS stream, which names no session, and §10.1.4 fixes the same posture. D5 keeps both whole-pod sentences, drops the
  generation from the pod-level arming line, and has each terminated session's `coordinator_lost` line and post-mortem read its own entry,
  reporting `0` where no coordinator fenced it there. Zero is impossible as a fenced value, so the sentinel costs no wire, JSON, or state
  change, and those records already carry `last_generation` and `started_sessions`. The code's hold allowlist is wider than §10.1.4's "only
  `CoordinatorFence`", which is pre-existing drift rather than this proposal's defect.
- **A barrier's generation comes from shared state, never from the sending replica.** The healthy path reads the `coordination_lease` mirror
  row and the cache fallback reads the live session row, and the value is fixed once, when the barrier-target set is assembled, so a
  superseded replica's barrier can carry the value the pod holds, which the pod accepts, and can carry a value the pod does not hold, which
  it refuses. Both outcomes are reachable and neither is asserted. Do not state a closed enumeration of the values it can carry: the mirror
  can lag below the pod's value, the live-row read sits one above it between a successor's compare-and-swap and its fence, and the
  fallback's zero seed puts a value on the wire that no fence ever installed. Do not re-import the universal pass 12 withdrew, that such a
  barrier is refused only inside the compare-and-swap-to-fence window: the mirror is written from the sweep's pre-`RecordHandoff` snapshot,
  so after a takeover it carries G while the pod is fenced at G+1 for a whole sweep interval rather than a race, which is a code defect and
  falsifies the staged rationale "there is no second value to keep in agreement with the row". The existence of the accepting case is what
  makes §28.8's `CH-BARRIER` cell an edit site and the parallel `CH-CHECKPOINT` cell one too; neither verdict rests on the withdrawn
  universal. §4's second design bullet still claims equality "catches a superseded sender whenever the assembly and the successor's fence
  straddle each other in either order", which is false in the fence-then-assembly direction: the assembly reads G+1, the pod holds G+1, and
  the barrier is accepted. It contradicts SPEC-2's own §28.8 `CH-BARRIER` rationale, lands nowhere in `spec/`, and is the premise §7's first
  open decision hands the human reviewer, so it routes to human review rather than to another spec round. MISTAKE: pass 7 filed both cells
  as non-sites because "a superseded replica is one whose successor's fence the pod has recorded, so the pod holds a value". Holding a value
  is not holding a different one, and rejection is conditioned on a mismatch. Cost: a round, and the non-site record hid the `CH-CHECKPOINT`
  cell from every later sweep.
- **The `spec/28` and `spec/29` edit sites are settled by one membership criterion, and §28.6's second opener is split by channel.** A
  sentence is an edit site when it states what the pod does with an RPC's generation and fixes the value that outcome is measured against,
  and is not one when it states the exclusivity constraint and its guard, a duty on the sending replica, the hold, or the pod's validation
  without fixing the compared value; a sentence doing both falls under the site arm, which is why the `CH-FENCE` Exclusivity bullet is
  staged while §28.5.1's `CH-CHECKPOINT` and `CH-BARRIER` Exclusivity bullets stay non-sites. Three ad-hoc lists with no membership rule
  produced a round's three §28 findings. §29.8 step 9 is the `spec/29` mirror of the window clause by the specification's own
  cross-reference, so it moves into the value form alongside its three `spec/28` twins; round 4's rejection of §29.8's Preconditions
  paragraph as unit-neutral does not reach step 9. §28.6's second opener spans four channels whose gates differ, so it takes one clause per
  gate. Do not give the whole sentence the barrier's equality predicate, since a legitimate handoff fence carries a generation above the
  pod's, and do not keep the row-value relation: between a successor's compare-and-swap and its fence acknowledgement the row reads one
  above the value the pod holds, so that relation rejects exactly where step 2 states acceptance, on a reachable ordinary path. MISTAKE: the
  earlier fix generalised a rationale true only of `CH-FENCE` across all four; a sentence spanning channels whose gates differ takes one
  clause per gate rather than one predicate weakened until it fits none of them. MISTAKE: pass 10 read the paragraph's fence-acknowledgement
  sentence as agreeing with the staged first sentence and left it standing, so the same correction had to be made twice, a round apart. When
  one paragraph carries two statements of a rule, adjudicate both in the pass that touches either. That sentence, §28.5.1's `CH-FENCE`
  Exclusivity bullet, and the §28.8 `CH-FENCE` window cell are the other three sites carrying the owner-phrased window clause. The
  neighbouring "One holder per session" sentence keeps "older than" where the second opener says "other than"; that asymmetry is shipped
  text rather than a defect.
- **§10.1.2 step 3 fixes equality as the operational-RPC gate, and step 2's two halves are load-bearing.** "The pod accepts only RPCs whose
  generation matches the fenced value" is the only acceptance-gate sentence in `spec/` or `docs/`, so loosening the barrier to "at least the
  last fenced generation" is barred by spec text; SPEC-1 changes only the unit of the compared value and the unset case, and §7's first open
  decision owns the operator. Five rounds credit this with keeping a fix off the operator and making the stamp collision visible. Step 2's
  bar, that the new coordinator send no operational RPC until `CoordinatorFence` is acknowledged, is an obligation on the acquiring
  coordinator, and the acceptance window that follows covers the generation the pod already holds. MISTAKE: paraphrasing it as "the pod
  accepts a coordinator's RPCs until that coordinator's fence is acknowledged" states the opposite and cost a finding. MISTAKE: SPEC-1
  attaches the "whole set of gateway-to-pod RPCs" domain claim to step 3's opening sentence ("all subsequent gateway-to-pod RPCs include the
  local generation stamp") when the sentence carrying that domain is the acceptance sentence after it. The opening sentence is scoped by
  step 3's framing to the acquiring coordinator's own RPCs, and the conflation is what makes the barrier's shared-state provenance read as a
  contradiction of step 3 rather than a fact about one RPC. It cost a finding. MISTAKE: ten passes restated step 3's no-window sentence
  without cross-reading it against the barrier acceptance the same passes were settling, even though refuted class (f) and the §28.8
  `CH-BARRIER` entry together said in so many words that a superseded replica's barrier is accepted on the equality arm. A sentence
  re-stated in an edit is read against every other sentence that edit touches.
- **`CoordinatorFence` has two senders and nothing fences a normally-started session.** The resume path and the sweeper's crash-takeover
  re-adopt drive the same `coordfence.Fencer`, and `.Fence(` outside tests returns those two alone; `coordfixture.FenceReadopter` calls the
  `adapterclient` RPC directly. CORRECTED: "no test constructs a `Fencer`" is wrong as written. It holds outside
  `pkg/gateway/coordination/coordfence`, whose own tests construct one over fake generation readers (`coordfence_test.go:164`, `:178`),
  which is why a tier-2 construction of the resume-then-takeover flow is not impossible. The resume path fences without bumping the row, so
  a resumed session's first crash takeover is delayed one sweep cycle rather than blocked, at the cost of a spurious stale-handoff and
  fence-relinquished metric pair. §10.1.2's sequence triggers on lease acquisition, which for a fresh session happens before any pod exists,
  so "no fenced generation" is the ordinary case in the specification too.
- **`CoordinatorFenceResponse.last_fenced_generation` is a pod self-report that reaches no gateway decision.** `adapterclient` copies it into
  `CoordinatorFenceResult` (`coordinatorfence.go:29`, `:60`) and nothing outside tests reads it: `fence()` branches on `res.Accepted` alone
  and on a rejection re-reads the authoritative Postgres value (`coordfence.go:159-179`). A round-5 security lens credits this with saving
  the whole trust-boundary re-derivation, so it stands until the code lane lands.
- **Derived inventories. Do not re-derive any of these.** Every anchor SPEC-1 and SPEC-2 quote resolves verbatim and uniquely, re-checked in
  rounds 4, 5, and 6, as does every code citation in CODE-1 through CODE-4, §8, and §9, re-resolved five times across the non-spec rounds.
  §10.1's no-window claim occurs once in `spec/`, `docs/`, and `schemas/`. The surface outside `spec/10`, `spec/28`, and `spec/29` is five
  `spec/04` sentences plus unit-neutral lines in `spec/07`, `spec/12`, `spec/16`, and `spec/18`, and `spec/18` puts the fence and the
  compare-and-swap in Phase 4 and `CheckpointBarrier` in Phase 8, so there is no phase inversion. The §28.8 matrix enforces one row per
  §28.3 identifier in both directions, so editing a cell is safe and deleting or splitting a row is not; CORRECTED: that gate lives in
  `tests/tier11_docs/`, so a tier-0-only run does not catch a §28 row defect and checklist S1's tier 11 covers it. §29.10's
  successor-pointer gate is satisfied by the opening paragraph's `CH-` link, and the removed §29.10 "does not state" bullet has no inbound
  reference outside the `spec/README` contents and asks two questions the staged edits answer. No `tests/claim-map.json` row moves, because
  the register is generated from root `gateway-runtime-comms.md` §7.1 alone and `claim_register_test.go` is a schema gate; two of its rows
  carry code-line surfaces into `pkg/adapter/coordination.go` that CODE-1 and CODE-2 will shift, and nothing resolves them. Every proto
  message declaring `coordination_generation` is session-addressed, `ShutdownRequest` included, so step 3's "the session the RPC names"
  resolves on every carrier. Two things look like data-loss leads and are not: `session_checkpoint_meta` is a different table from the
  `checkpoint_manifest` partial-manifest rows §10.1 describes, and the four other `coordination_generation` columns are always written
  explicitly from the session row, so leaving their defaults at 0 is cosmetic. The `docs/` surface is eight sites and states no unit,
  baseline, or gate: `adapter-contract.md:68`, `:69`, `:96`; `metrics.md:307`, `:309-312`; `concepts.md:101`; `architecture.md:173`;
  `operator-guide/upgrades.md:47-54`. It has been re-derived eight times, from the docs, security, client, and operational sides, and nothing
  in it, in `charts/`, in `sdks/`, or in §16 is made wrong by a per-session generation, by D5, D6, D7, or the row baseline; no alert,
  runbook, or tier-11 test is reached. Its two misattributions of who sends the barrier and who drives the capture, and
  `adapter-contract.md:79`'s per-session op lock against the spec's pod-level one, are pre-existing drift for a docs loop. Landed migrations
  are not edit sites either: `0164`'s column comment states the barrier's match rule D7 narrows, and `0050`'s justifies the zero default
  CODE-4 falsifies. Known sub-line citation drifts that must not be filed: `slotsession.go` cited `:283-285` and `:282-285` for one struct
  declared at `:282`; `coordination_test.go` cited `:184-197` and `:185-197` for one test whose doc comment opens at `:185`;
  `coordination.go:408` cited for a backoff call at `:415` in the same block.
- **Two tier-0 gates the deliverable must satisfy.** `TestProtoStubsMatchGeneratedOutput` reproduces `make generate-proto` and diffs the
  whole `pkg/proto` tree, so any SCHEMA-1 comment edit needs `lenny-adapter.pb.go` (field and message comments) and
  `lenny-adapter_grpc.pb.go` (the two RPC comments, twice each, at `:180` and `:632`) regenerated in the same commit; the gate self-reports
  "unverified" when `buf` or the plugins are absent, so a green local tier 0 is not evidence the regeneration is unnecessary.
  `scripts/lint-migrations.sh` pass 3 greps `tests/tier2_component/migrations/` alone for a migration's number, and `prodMigrationSchema` in
  that same file drives the per-step rollback walk, so migration 0181 owes both a behaviour file in that directory and a table row even
  though it adds no column; 0180 and 0112 are the precedents for a column-less migration keeping a row. Lint passes 4 and 5 key on
  `add column` and `drop column` and do not reach 0181, which drops a constraint.
- **A refused barrier costs a duplicate capture rather than a lost checkpoint.** `dispatchOne` starts the gateway-driven `Checkpoint` stream
  before `dispatch.Send` and joins it after, so the checkpoint still runs and the stream finalises the manifest row. What is lost is the
  quiescence and the acked-barrier record, after which prestop checkpoints every session whose `barrierAcked` entry is false. The cost is
  the same for `InvalidArgument` and `FailedPrecondition`, since only the latter maps to `ErrGenerationStale` and prestop branches on the
  acknowledgement alone. Two traps: `quiesced_ms` is never persisted, so naming it among the records a rejected barrier loses is wrong, and
  `lenny_coordinator_handoff_stale_total` increments only on the fence path. Quiescence cannot wedge, being cleared in a deferred close and
  bounded by the 90s ack deadline.
- **Refuted classes. Do not re-file one without new evidence; each cost a round. Read a refutation's own scope first, and keep its body
  rather than its title: the one killing the "no second value" finding turned on that clause being rationale that lands nowhere in `spec/`,
  and it does not reach staged text.** (a) Sender-side against pod-side: `spec/28`'s `CH-FENCE` Preconditions, `spec/04`'s
  `CoordinatorFence` row, and `docs/reference/adapter-contract.md` state a gateway obligation inherited from step 2 while the staged clause
  states a pod-side gate, and the two coexist. This does not extend to §28.6's second-opener sentence or the §28.8 `CH-CHECKPOINT` cell,
  whose first clauses are pod-side rejection rules. (b) The exemption-unit argument, that D6 turns one exempt fence per pod into N: a fence
  issues only after a successful compare-and-swap, an out-of-order pair is self-healing by step 2's own clause, and `checkSessionBound`
  rejects an unbound session's fence first. The full sentence is what refutes; the one-line summary does not. (c) The "unqualified sentence
  inside a session-scoped narrative" class, killed on §29.8 step 7 and on the §7.2 parenthetical in `spec/07`. It does not cover `spec/04`
  §4.1's declared `pod` class for `CoordinatorFenceRequest`, which stands as an OPEN. (d) Session-less pod-scoped RPCs having no session to
  key on: none of the four carries a `coordination_generation` field. (e) "Step 3's unset clause permits what step 2 forbids": step 2's bar
  is a sender obligation, so the window stays closed for compliant coordinators. (f) A fencing hole in D7 admitting a superseded replica's
  barrier because the pod holds no value: for a never-fenced session the only replica accepted is the current coordinator or the immediately
  prior one inside the step-2 window, which step 2 sanctions. MISTAKE: (f) holds for the unset arm alone. It says nothing about the equality
  arm, where a matching stamp is accepted, and reading it as covering both is what let the stamp collision survive a round. (g) "The §10.1.4
  hold-exit predicate is unstaged", and its variant that a successful fence for any one session exits the hold for the pod: `exitHoldState`
  is reached only on the accepted arm of `CoordinatorFence`, so the staged bullet matches the tree, and SPEC-2 stages the consequence in
  §29.10's "Shared by the whole pod" bullet. (h) The collapse of the first-handoff and later-handoff states in the staged §10.1.8 and §29.7
  predicates: both now carry the unset arm and the pre-fence window explicitly. (i) The §10.1.2 step 2 edit instruction as an underspecified
  target: its two candidate insertion points differ in meaning, but the SPEC-1 state-model paragraph and the staged §28.5.1 Messages mirror
  both spell the intended sentence out, so a competent applier cannot land the pod-wide reading. (j) The staged §10.1 invariant "every
  generation a pod validates is positive" as falsified by the cache fallback's literal 0: that is the already-refuted "unreachable by
  construction" class, whose refutation covers the invariant and the retained adapter backstop. (k) A missed-edit-site finding over a path
  or a comment under `tests/` or `pkg/`: criterion (d) enumerates `spec/`, `docs/`, `schemas/`, and `charts/` and reaches neither tree, and a
  Go doc comment that becomes less precise while its assertions stay valid is not a site. The precedent is the refutation of
  `pkg/gateway/coordination/barrier/barrier.go:62-65`, and it settles the whole class, `adapterclient/coordinatorfence.go:37`,
  `coordinatorfence_test.go:15-16`, the stale `// spec:` parenthetical in `generation_fence_wire_test.go:134-135`, and `RecordHandoff`'s
  stale rationale comment included. MISTAKE: a round-2 lens nearly filed §9's omission of any `tests/tier3_contract` file on this ground and
  stopped only at the bar; §8 names the tier-3 case with its assertions, so criterion (f) is not met either. Do not spend a verification on
  it.
- **Staging lessons, each paid for with a round.** MISTAKE: a clause naming what an edit bullet leaves alone is relative to that bullet.
  Pass 11 split the §28.6 fence-acknowledgement sentence into its own bullet and carried the previous bullet's "the paragraph's remaining
  sentences are unchanged" across unchanged, where it asserted the opposite disposition for the sentence the bullet above stages, so an
  implementor would have left that sentence standing. Moving such a clause re-anchors it, and it is re-read against the new bullet's own
  subject. MISTAKE: §10.1.8 and §29.7 were staged in terms of whose fence had landed while step 3 owns the predicate, and the same round
  grounded D7 on the generation gate alone when an earlier non-positive guard refused the RPC first. A mirror applies the owning section's
  predicate by reference, and a claim about what refuses an RPC today is read against every guard preceding the one being changed. MISTAKE:
  the zero-stamp fact sat as an UNVERIFIED routed to another lane while the staged text still refused that barrier; pass 7 then reversed the
  text to accept it without anyone chasing the UNVERIFIED, so D7's repair was unreachable for a round. An UNVERIFIED handed to another lane
  is re-read by whoever reverses the text it qualifies. MISTAKE: pass 11 closed the step-3-versus-§10.1.8 reconciliation by asserting an
  outcome ("a barrier that reaches the pod after the successor's fence has landed carries the value the pod holds and is accepted") where
  the true statement is about provenance, and the clause had already been copied into three live rationale sites, so one wrong sentence cost
  four edits a pass later. When a reconciliation needs a fact about where a value comes from, state it about the read; an outcome sentence
  invites a reviewer to find the interleaving that falsifies it. MISTAKE: pass 12 then withdrew that consequence from the staged text and
  left it standing in §4's open-design bullet, which is the premise §7's first open decision hands the human reviewer. A withdrawn claim is
  swept across the whole proposal, the design blanks included. MISTAKE: pass 12 then replaced pass 11's false universal with a closed
  two-value enumeration of the generation a barrier can carry, which is the same defect one step weaker, and round 5 falsified the narrowing
  as well. An enumeration that cannot be closed is not repaired by widening it; the correction is to state the outcome rule and stop, which
  is what deleting it from staged §10.1.8 step 1 did. A step that applies another section's predicate by reference does not owe a list of
  the values the predicate will see, because reachability is a property of the value's producers rather than of that step. MISTAKE: the
  finding that drove the fixer named two sites while the wrong attribution stood in three, the third being §8's own preamble, so correcting
  only the named anchors would have left §8 contradicting itself. A finding's named sites are a starting set rather than the sweep. MISTAKE:
  the tier-4 fence misattribution came out of a pass record that justified the tier by the production `coordfence.Fencer`'s relinquish, and
  the justification was then copied into §8 and TEST-1 as a description of the harness. A rationale about the production path is not a
  description of the fixture that stands in for it. MISTAKE: the deliverable split put the sites that read relocated state
  (`checkpoint.go`'s link and complete, the barrier's quiesce-and-hold) in a later deliverable than the move itself, which is what let a
  false exemption be written for one of them and would have left S5 staging an edit S3 could not compile without. A deliverable that
  relocates state owns every site that reads it.
- **Every confirmed finding since round 1 sits in the D7, counter-baseline, and barrier-provenance cluster.** The per-session move (D1
  through D6), which is what the problem statement evidences, has drawn none since round 1, and rounds 2 through 5 each produced one
  finding, every one created by the previous round's own fix. MISTAKE (loop-level): the run kept scheduling the spec lane, whose corrections
  were already derived, while the lane holding the undone corrections never ran, so `non-spec-changes.md`, `implementation-checklist.md`,
  and `status.md` stood five rounds behind. A converged spec lane whose consequences are unwritten is not a converged proposal. The non-spec
  lane has since run five times and landed CODE-4, the CODE-1/CODE-3 step merge, the CODE-1/CODE-2 re-split over the registry-entry move,
  the tier-4 fence attribution, the proto stubs, the migration test registry, the S3/S4 tier-8 split, and the mid-flight deregistration
  case; what it left is in the residue entry.
- **§7's remaining decisions are deliberately open for the human reviewer**: whether the barrier gate stays equality, whether a fence for an
  unheld session is a rejection or a retryable race, and whether `coord.mu` becomes per-entry. D7 removed the fourth. Cite them by text,
  because D5 and D7 renumbered them. Round 5's falsification lens added a third option to the first decision that the menu does not carry:
  delete the barrier's generation gate outright. Its argument is that the gate as shipped refuses the two legitimate cases (a
  never-taken-over session's 0, and a just-taken-over session's lagging mirror) and admits the superseded replica it is documented to catch,
  and that deleting it dissolves D7, the provenance reconciliation, and the counter baseline together. It is not forced, because repairing
  `upsertMirror` and baselining the counter leaves a working gate, and it contradicts six shipped statements. Route it to the human, along
  with §4's "either order" claim and the three known-imprecise rationale sentences the residue entry carries. Note that CODE-1's move of
  `coordinationState` carries its embedded `mu` onto the entry, settling the third decision by construction while §4 still frames it as open.
- **A converged proposal retires its draft headers.** Only 0075 and 0076, both unconverged, carry `**IMPLEMENTOR TO FILL THE BLANKS.**`; the
  landed 0073 uses `IMPLEMENTOR'S CHOICE:` with a named constraint and carries no fill-the-blanks header over its staged sections. Round 6
  filed the header over `## 5. Proposed changes` on that ground, because SPEC-1 through SPEC-3 beneath it are verbatim staging and a header
  calling them indicative targets tells a maintainer applying tomorrow not to apply them as written. WATCHOUT: §4's own marker is a
  different case and is not that finding, because it names four derivable items each carrying a constraint; it stands as its own OPEN. Do
  not merge them.
- **Editing hazards in this proposal's own files.** `cat -n *spec-changes.md` globs two files: it matches `.non-spec-changes.md` and
  `.spec-changes.md` and numbers them continuously, so a line taken that way is off by 46. Name the file in full. Every line citation into
  the spec-changes file taken before run 3 has moved; re-derive rather than trusting a range from an earlier round's design, finding, or log
  entry, and anchor each edit on the sentence it quotes rather than on an offset, because a sibling group editing the same file in the same
  round shifts every later line. The spec-changes file wraps at 98 columns and the non-spec-changes file at 99 to 106, and a hand re-split
  cascades, so reflow a whole blank-line-delimited paragraph in one pass at the width of the file being edited. `spec/10:183` carries both
  anchors SPEC-1 edits in §10.1.8 step 1 on one physical line, so an edit that rewrites one must not clobber the other. §28.6's
  second-opener paragraph has two sentences that were both called "its closing sentence" in the live staged text, the fence-acknowledgement
  one and "The constraint excludes a second replica", and their verdicts differ, so name a sentence by content rather than by position. A
  pass record under `## Resolved in adversarial review` keeps the words it was written with; a later pass withdraws its text in a new entry,
  and a fixer who silently rewrites a citation inside one has edited a record meant to stand. Grep returns those records beside the live
  sites, so check which of the two a hit is before editing it, and a round that lifts a cost clause out of a pass record lifts the corrected
  one; a grep for a step identifier such as `S6` or `S7`, or for `coordfence.Fencer`, returns a dozen frozen records beside the one live
  site. Several fixers now append to that section in one round, so read the last heading rather than assuming the next pass number. A
  design's stated line range for a staged paragraph can stop mid-sentence, because that paragraph's closing sentence begins inside the same
  physical line as the sentence before it; take the blank-line-delimited extent instead, or the closing sentence is silently dropped when
  the paragraph is replaced. The `## Resolved in adversarial review` section lives only in the spec-changes file, even in a non-spec round,
  so appending a pass record touches the one file such a round is otherwise told to leave alone.
- **The adapter hold's edit set is closed, and it reads a per-session generation off a pointer that outlives the registry entry.**
  `holdState.gen` is written once at `pkg/adapter/holdstate.go:128` from the accessor read at `:119`, read once at `:187`, passed to
  `terminateHeldSession` (`:206`, declared `:225`) and on to `writeHoldPostMortem` (`:283`), and logged as `last_generation` at `:132` and
  `:228`. CORRECTED: CODE-3's enumeration was short by two for several rounds and is now complete, naming `:43`, `:119`, `:128`, `:130-132`,
  `:187`, `:206`, `:225`, and `:283`; do not re-file the omission. `heldSession{sessionID, state *slotState}` carries the entry pointer and
  `deregisterSlotLocked` deletes the map key and returns the pointer with no field zeroed, so the post-mortem reads each terminated
  session's own value after pass 1 removed it. WATCHOUT: a later edit that clears fields on deregistration silently zeroes every
  `coordinator_lost` record. The post-mortem's `LastGeneration` JSON key carries no `omitempty`, so the D5/D6 unset sentinel serialises as an
  explicit `0` and a test asserts it with no schema change. `onHoldTimeout` releases `hold.mu` before both passes, so the only nested pair
  stays `coord.mu` then `hold.mu` and CODE-3 closes no cycle; the comment at `pkg/adapter/coordination.go:126-128` ("the hold timeout never
  reaches back into coord.mu") becomes false in letter and is rewritten in the same pass. `pkg/adapter/holdstate_test.go:713` is the file's
  only generation assertion, inside `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1` (declared `:674`), and its
  slog-capture pattern is process-global, so the case cannot call `t.Parallel()`. WATCHOUT: the same file's doc comment at `:887-892`
  justifies a no-fields resolved line by saying the generation "is already on the coordinator_connection_lost line", which CODE-3 removes;
  the test still passes, so a fixer editing that file for §8's hold case should fix the comment while there.
- **Guard ordering is what the adapter deliverables rely on, and the resolved entry must be carried rather than looked up again.**
  `checkpointRootsForSession` (`pkg/adapter/slot.go:153-166`) fails the `Checkpoint` stream with `FailedPrecondition` at
  `pkg/adapter/checkpoint.go:94` when the session has no bound slot entry, and it runs before the barrier link at `:122`;
  `CoordinatorFence` and `CheckpointBarrier` both run `checkSessionBound` at `:89` and `:216` before reading any generation. A guard that
  exists says nothing about whether it precedes the site that needs it, so verify the order. MISTAKE: this bullet used to close "so a
  per-entry gate never has to handle a missing entry", which is false and is where CODE-2's false exemption came from. `s.ops.Begin` sits
  between the guard and the link at `:111` and queues a co-tenant checkpoint for the whole of the running session's upload (a distinct
  session id is admitted and waits; only the same id coalesces), and both deregistration paths run inside that window. The guard validates
  an entry the link site must be handed; the earlier success is an ordering fact rather than a persistence fact. `checkpointRootsForSession`
  therefore returns the resolved `*slotState` alongside the roots, and its only other caller is `pkg/adapter/resume.go:178`.
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
- **`coordfixture` is in the untagged tree; its consumers are not.** MISTAKE: a design instructed §8 to say the fixture "is compiled only
  under the integration, chaos, and load_local build tags, so tier 0 does not catch the accessor change".
  `tests/testinfra/coordfixture/coordfixture.go` carries no `//go:build` line, so tier 0's `go vet ./...` does compile it and does catch a
  break inside it. What tier 0 misses is the three tagged consumer trees (`tests/tier4_integration`, `tests/tier7a_load_local`,
  `tests/tier8_chaos`), because it vets the untagged tree plus `-tags=contract ./tests/tier3_contract/...` and nothing else, so §8 says to
  run the tagged vet after CODE-1. A signature change on `Server.LastFencedGeneration` breaks nothing visible until those tiers run.
- **MISTAKE: pass 9 of the spec changes tells a later pass to delete two things that were never written.** It says to drop
  `pkg/gateway/coordination/barrier/barrier.go` from the files-touched list and a dispatcher case from §8. Pass 8 wrote neither into the
  deliverable files, which were five rounds behind it, so both instructions are no-ops and a fixer who goes hunting concludes the file is not
  the one pass 9 describes. It cost about ten minutes of doubting the file. Grep for a sentence before hunting for it, and read an
  instruction written against a lane that never ran as a statement of its writer's intent rather than a description of the file.
- **Proposal-format facts. Do not re-derive them and do not invent against them.** The change-proposal format requires a
  `## Deliverable index` in the summary as "the ONLY place a deliverable id resolves", and no proposal in `proposals/` has one, because the
  requirement postdates the migrated layout; do not invent one for 0076. The same format requires that every staged deliverable appear in
  exactly one step and that no step name one that does not exist. `implement-proposal` treats a step declaring two lanes as `bad-lane` and
  stops the run, so 0073's `**S15 · migration, code**` and `**S11 · schema, code, test**` are historical rather than precedent; `code`,
  `schema`, `migration`, `test`, and `docs` all dispatch to the same build handler, so splitting a deliverable across lanes buys no handler
  difference. Each step is one commit, is gated on the tier set it names, and is never ticked on a gate that did not pass. Every test-lane
  step in the neighbouring proposals declares tier 0 explicitly, so tier 0 is not implied by naming a higher tier.
- **The S3/S4 split turns on a closed two-file class.** Exactly two files take both a CODE-1 accessor edit and a CODE-4 baseline shift:
  `tests/tier8_chaos/coordination_crash_takeover_test.go` and `tests/tier7a_load_local/coordination_colocation_race_test.go`. The third
  `pod.LastFenced` caller, `tests/tier4_integration/coordination_fence_split_brain_test.go`, seeds `CoordinationGeneration: 1` at `:72`, so
  CODE-4 does not reach it. The split is safe in both members because the accessor reads and the shifting assertions sit in different
  subtests: the tier-8 reads at `:150`, `:195`, and `:223` are in subtests seeded 1 at `:118` and `:179`, while the 1, 1, 2 assertions at
  `:267`, `:283`, and `:296` belong to the third subtest, whose seed leaves the field unset (`:239-241`) and which makes no `LastFenced`
  call. WATCHOUT: batching a file into one step turns a declared tier red in either direction. Batched into S3 the assertions read 2, 2, 3
  before migration 0181 and the `pgstore` floor exist, and that file runs against the production `pgstore` (`:85`); batched into S4 it still
  calls the zero-argument `LastFenced` after S3 changed the signature, so S3's own tier 8 does not compile. CODE-4 reaches tier 7a through
  the colocation race test's explicit `CoordinationGeneration: 0` seed via `memstore.Create` (`:144`) and its assertion at `:287-288`, and
  S4, §8's CODE-4 tier line, and the per-deliverable line all carry 7a. MISTAKE: the finding and its design named the tier-8 file alone
  while the neighbouring sentence covers the tier-7a file, so a file-scoped correction would have left the identical defect one sentence
  away. State a step split as a rule over the class when the class is closed. §9 is a flat list with no per-deliverable attribution, so a
  split needs no §9 change.
- **MISTAKE (avoided, recorded so no one spends the round on it): CODE-2 naming `pkg/adapter/checkpoint.go:122` while CODE-1 removes
  `Server.barrier` is not an S3/S5 compile break.** Checklist step S3's own text says the barrier gate moves onto the slot registry entry,
  so the move and every call site it drags land in S3, and CODE-2's sentence describes the end state of the link rather than staging a
  separate edit. Read a deliverable's sentence against the step text that owns the mechanism before filing a sequencing break.
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
  amendment, and `adapter.New(` appears in six `tests/tier3_contract` files, so the tier does host behavioural adapter cases. Separately, a
  `prodMigrationSchema` row for migration 0181 naming `sessions` while 0181 also alters `coordination_lease` is not a defect, because a row
  with no `columns` and `create:false` is inert in both migration tests and exists only for the lint's number grep and the rollback walk.
- **WATCHOUT: a file the proposal cites is not thereby a file the proposal touches.** `pkg/gateway/coordination/barrier/barrier.go` is read
  by CODE-1 (`:190-201`, `:238-245`) and by §8, and its doc comment at `:62-65` grounds a safety claim on the adapter's barrier gate as an
  unconditional rejection, yet §9 lists no file under `pkg/gateway/coordination/barrier/`. Grep §9 for the path rather than assuming a cited
  file is a listed one. Under refuted class (k) the stale comment itself is not a finding.

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

### [non-spec.4.review-edit-sites.1]

DECISION: returned an empty findings list — BECAUSE every identifier the proposal adds, changes, or removes
was swept against `spec/`, `docs/`, `schemas/`, `charts/`, `migrations/`, and the test tree, and each surface
that would become wrong is either staged, already carried as an OPEN ledger item, or below the materiality
bar — ALTERNATIVES: rejected filing the `0181 .down.sql` reversal gap and the §8 "tier 2 (the registry
state)" reading; both are recorded below as UNVERIFIED/CORRECTS rather than as findings.

FACT: `tests/claim-map.json` is generator-produced. `scripts/seed-claim-register.py` parses exactly one
document, root `gateway-runtime-comms.md` §7.1, and `TestClaimRegisterIsReproducibleFromItsGenerator`
re-runs the generator and diffs bytes. SPEC-2's `spec/28`/`spec/29` cell edits therefore cannot change a
register row, which is the mechanism behind the standing context's "no claim-map row moves" claim — the
reason is the generator's source document, not the validator being a schema gate.
EVIDENCE: scripts/seed-claim-register.py:11-13, :38-39; tests/tier0_static/claim_register_generator_test.go:20-31, :45

FACT: `prodMigrationSchema`'s `table` field is inert for a row with no `columns` and `create:false`.
`TestProdMigrationsApplyExpectedSchema` iterates `m.columns` (none) and `TestProdMigrationsRollBackPerStep`
`continue`s on `create` then iterates `m.columns` (none), so the 0181 row exists only so the rollback walk
steps its `.down.sql` and `lint-migrations.sh` pass 3 finds the number. Naming `sessions` rather than
`coordination_lease` costs nothing.
EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:588-603, :610-634; scripts/lint-migrations.sh:73-88

FACT: §8's class-1 inventory (assertions reading a session row's `CoordinationGeneration` after a create
that left the field unset) is complete. Every other assertion site in the tree seeds the field explicitly at
or above 1 and is unaffected by the baseline: `pkg/gateway/sessionserver/coordination_fence_test.go` (4, 9,
14, 10), `pkg/gateway/sessionserver/failure_test.go:360,:394,:426` (7, 1, 11),
`tests/tier4_integration/checkpoint_intent_generation_test.go` (10, 5, 3, 7),
`tests/tier4_integration/eviction_fallback_outage_test.go:159,:193` (2), and every
`barrier.Target{...CoordinationGeneration: n}` literal under `pkg/gateway/coordination/barrier/`. The
`INSERT INTO sessions` raw-SQL fixtures all omit the column.
EVIDENCE: grep -rn "CoordinationGeneration" --include=*.go over the tree, minus pkg/proto

FACT: the accessor blast radius is exactly what §9 lists. `Server.LastFencedGeneration()` has three callers
(`pkg/adapter/holdstate.go:119`, `pkg/adapter/coordination_test.go:73`,
`tests/testinfra/coordfixture/coordfixture.go:115`); `Pod.LastFenced()` is read only in
`tests/tier4_integration/coordination_fence_split_brain_test.go`,
`tests/tier8_chaos/coordination_crash_takeover_test.go`, and
`tests/tier7a_load_local/coordination_colocation_race_test.go`; `BarrierWaiting()` adds only
`pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163`; `isQuiescedForBarrier()` adds nothing.
`pkg/adapter/export_test.go` exports none of them.

FACT: the whole `spec/`+`docs/`+`schemas/`+`charts/` surface for the pod-level hold log line is two lines,
`spec/10_gateway-internals.md:60` and `spec/29_communication-scenarios.md:1274`, and SPEC-1/SPEC-2 stage
both. The post-mortem record's `lastGeneration` JSON key appears in no schema, doc, or chart, so dropping
the pod-level `last_generation` slog key reaches no other carrier.
EVIDENCE: pkg/adapter/holdstate.go:129-132 (the emit), :283-296 (the record)

FACT: the proto carries exactly the seven generation-rule carriers the standing context enumerates. A grep
for `fenced|older generation|handoff_stale|lifetime` over `schemas/lenny-adapter.proto` returns hits only at
:161-162, :167, :1444-1446, :1457-1460, :1465, :1472-1473, :1479 — all inside those seven. There is no
eighth carrier hiding in `CoordinatorFenceResponse`'s field comments or in `CheckpointBarrierResponse`.

FACT: no tier-0 or tier-11 gate reads the sentences SPEC-1/SPEC-2 rewrite. `spec_28_index_rows_test.go` and
`successor_pointer_test.go` read headings and anchors; `per_slot_substate_scope_doc_reconciliation_test.go`
cites §29.10 only in its `// spec:` annotation and asserts over `spec/06` and
`docs/reference/state-machines.md`; `tests/spec-map.json` and `tests/spec-anchor-moves.json` key on
headings, which this change does not move. `docs/reference/adapter-contract.md` is hand-authored rather than
generated from the proto, and its two fence/barrier rows (`:68`, `:69`) are unit-neutral.

CORRECTS [standing context, "Derived inventories"]: the bullet says the §28.8 matrix row gate runs "at tier
0". The gate that reconciles §28 rows against `spec/README` lives in `tests/tier11_docs/`
(`spec_28_index_rows_test.go`, `spec_28_taxonomy_test.go`, `spec_28_ownership_test.go`). Checklist S1
declares tiers 0 and 11, so the conclusion is unchanged, but do not expect a tier-0-only run to catch a §28
row defect.

UNVERIFIED: CODE-4's `.up.sql` changes two column defaults (`sessions.coordination_generation` and
`coordination_lease.coordination_generation`) while the `.down.sql` sentence names one: "restores the
`DEFAULT 0` and the `>= 0` check". §8's staged migration case asserts both defaults forward but only the
check on the way back, so nothing would catch a down that reverses `sessions` alone. Judged below the bar
(the lease column is always written explicitly from the session row, so a stale default is inert), but a
one-word widening to "restores both `DEFAULT 0`s" would close it. Whoever next writes `non-spec-changes.md`
should decide.
EVIDENCE: non-spec-changes.md:100-104, :226-230

CORRECTS [any future round tempted by §8's tier sentence]: "2 (the registry state, the migration, and the
Postgres session store's floor)" does NOT contradict "the per-session fenced value ... pinned at tier 1".
"the registry state" reads as the `prodMigrationSchema` migration registry, which is CODE-4's tier-2 work
alongside the migration behaviour file and the `pgstore` floor, so all three tier-2 subjects have listed
cases. I spent a pass on the slot-registry reading before ruling it out; do not re-derive it.

USEFUL [standing context, "Derived inventories"]: the `docs/`/`charts/`/`sdks/`/§16 sweep and the "five
`spec/04` sentences plus unit-neutral lines in `spec/07`, `spec/12`, `spec/16`, `spec/18`" boundary both held
on re-check. `docs/getting-started/concepts.md:101`, `docs/getting-started/architecture.md:173`,
`spec/04:200,:323,:461,:711,:712`, `spec/16:183,:185,:191,:192,:199,:208` and `docs/reference/metrics.md:307-312`
state no unit, no baseline, and no gate. `sdks/`, `schemas/README.md`, and `schemas/examples/` mention
`coordination_generation` nowhere at all.

### [nonspec.5.postfix-fix.1] · 2026-09-01 · run 3 non-spec round 5 · postfix correction to pass 21

CORRECTS the two §8 paragraphs that pass 21's new mid-flight deregistration bullet contradicted without
being revisited. Both defects are drift between the new bullet and text that states the same decision
for the whole case list, so both are fixed by making the older text agree rather than by weakening the
bullet.

FACT: the bullet's placement claim holds on the tree. `pkg/adapter/coordination_test.go:3` and
`pkg/adapter/holdstate_test.go:3` are `package adapter`, while `pkg/adapter/checkpoint_stream_test.go:3`,
`pkg/adapter/slot_test.go:3`, and `pkg/adapter/server_test.go:3` are `package adapter_test`, and the
fixtures the bullet names exist where cited (`slot_test.go:24` `concurrentServer`, `:37` `slotStartReq`,
`server_test.go:90` `adapterClient`, `checkpoint_stream_test.go:384` `driveCheckpointConc`, `:417`
`TestCheckpointStreamConcurrentPerSlotAllCaptured_spec_5_2`). So the IMPLEMENTOR'S CHOICE paragraph could
not keep offering the file and the second-session helper as open for this case: the only other tier-1
files TEST-1 names cannot reach those fixtures. The paragraph now scopes the choice to the cases whose
own bullet leaves it open and records this case's fixed placement.

FACT: `-race` is on at tier 1 as well as at tier 7a. `cmd/lenny-test/cmd_run.go:880` sets
`extra := []string{"-race", "-count=1", ...}` inside `runUnitTier` (`:869`), so `-race` does not
discriminate between the two tiers and the tier-split paragraph's "the concurrency cases are pinned at
tier 7a" was not the rule it read as. The paragraph now names the entry lifetime as a tier-1 subject
beside the per-session fenced value, the hold records, and the barrier-gate independence, and states the
discriminator that the placements actually run on: a case that arranges concurrent calls only as the way
to reach process-local state stays at tier 1, and a case whose subject is contention itself is pinned at
tier 7a. Both tier-7a bullets assert non-interference between two co-tenant sessions' RPCs, so no case
moves tier and the checklist is untouched.

DECISION: the corrections are appended to pass 21 in `...spec-changes.md` rather than opened as a new
pass, because they correct that pass's own edits. No staged spec text changed; the file was touched only
under `## Resolved in adversarial review`.


### [non-spec.5.fix-G1.1]

DECISION: CODE-1 now states the hold-the-pointer rule once over three handlers (`CoordinatorFence`,
`CheckpointBarrier`, `Checkpoint`) and absorbs CODE-2's quiesce-and-hold, link, and exemption sentences;
CODE-2 shrinks to the generation gate at `pkg/adapter/coordination.go:236` — BECAUSE the exemption was
false on the tree and the split also hid a step-ordering defect: `Server.barrier` ceases to exist in S3
(CODE-1), so `pkg/adapter/checkpoint.go` cannot compile until S3 rewires it, while CODE-2 lands in S5 —
ALTERNATIVES: replacing only CODE-2's last sentence (leaves the rule stated twice in two deliverables and
leaves S5 staging an edit S3 must already have made); a sibling helper returning the entry beside
`checkpointRootsForSession` (duplicates one concern and reintroduces the gap between two `s.mu`
acquisitions); a re-lookup at the link site with missing-entry handling (the implementation CODE-1 exists
to rule out); leaving the gate pod-wide (ruled out by staged §10.1.8 step 3 in earlier rounds); a
compensating close-the-gate action on deregistration (new mechanism, and it returns an empty
`checkpoint_ref` early where the detached pointer is correct).

CORRECTS [Standing context, "Guard ordering, rather than guard existence, is what the adapter deliverables
rely on"]: that bullet closes with "so a per-entry gate never has to handle a missing entry", which is
false. The guard is `checkpointRootsForSession` at `pkg/adapter/checkpoint.go:94`; the link is at `:122`;
between them sits `s.ops.Begin` at `:111`, which admits a checkpoint for a session identifier no pending
checkpoint names and queues it behind the running one (`pkg/adapter/oplock.go:119-129`). Both
deregistration paths can run in that window. What is true is the bullet's other half: guard ordering is
what the deliverables rely on, and the entry must be carried from the guard to the link rather than looked
up again. The same bullet's last sentence ("The pod-level op lock queues a second co-tenant checkpoint
rather than refusing it") is the evidence that refutes its own conclusion.

FACT: `checkpointRootsForSession` (`pkg/adapter/slot.go:153-166`) resolves the `*slotState` under `s.mu`
and returns only `([]workspace.NamedRoot, error)`, dropping the pointer. Its two callers are
`Server.Checkpoint` (`pkg/adapter/checkpoint.go:94`) and `Server.restoreChunks`
(`pkg/adapter/resume.go:178`) — EVIDENCE: pkg/adapter/slot.go:153, pkg/adapter/checkpoint.go:94,
pkg/adapter/resume.go:178

FACT: `pkg/adapter/coordination_test.go` is `package adapter` (internal); the checkpoint-stream fixtures a
mid-flight case needs are in the external `adapter_test` package: `checkpointServer`/`driveCheckpointConc`
(`pkg/adapter/checkpoint_stream_test.go:80`, `:384`), the concurrent co-tenant case at `:417`,
`concurrentServer` (`pkg/adapter/slot_test.go:24`), `slotStartReq` (`:37`), `adapterClient`
(`pkg/adapter/server_test.go:90`). A case written into `coordination_test.go` cannot reach any of them —
EVIDENCE: pkg/adapter/coordination_test.go:3, pkg/adapter/checkpoint_stream_test.go:3

FACT: both deregistration paths remove the session's slot tree after deregistering the entry
(`pkg/adapter/session.go:271`, `pkg/adapter/holdstate.go:249`), so a mid-flight case cannot assert a
successful `CheckpointSummary`; the assertions available are the link, the ack's `checkpoint_ref`, and the
RPC return — EVIDENCE: pkg/adapter/session.go:271, pkg/adapter/holdstate.go:249

WATCHOUT: the new §8 case does NOT fail against the pre-fix code, because the pod-wide gate is never
absent. §8's preamble was scoped to "Each must fail against the pre-fix code, except where a bullet states
otherwise" for that reason, which also regularises the two bullets already marked as amendments of landed
cases. A later round that re-tightens the preamble reopens all three — EVIDENCE:
proposals/0076_fix_scope-the-coordination-generation-to-the-session/0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md,
§8 preamble

WATCHOUT: tier 1 already runs `-race` over the whole repo, so an adapter-internal concurrency case does not
need tier 7a — EVIDENCE: cmd/lenny-test/cmd_run.go:880

MISTAKE: the deliverable split put the sites that read relocated state (`checkpoint.go`'s link and
complete, the barrier's quiesce-and-hold) in a later deliverable than the move itself. That is what let a
false exemption be written for one of them and would have left S5 staging an edit S3 could not compile
without. A deliverable that relocates state owns every site that reads it.

UNVERIFIED: whether the `pkg/adapter` external test package can stall `s.ops.Begin` deterministically
enough for the new case without a test-only hook. `driveCheckpointConc` and `concurrentServer` drive two
real streams, so the queue is reachable, but the case still has to hold the first stream open while it
deregisters the second's entry. The implementor of S6 should check before choosing the file.


### [non-spec.5.fix-design-G1.1]

DECISION: CODE-1 owns the whole registry-entry move, and CODE-2 shrinks to its one sentence. Move the three
sentences at non-spec-changes.md:69-73 (the quiesce-and-hold sites, the link site, and the false exemption)
out of CODE-2 and into CODE-1, restating the hold-the-pointer rule over three handlers rather than two:
`CoordinatorFence`, `CheckpointBarrier`, and `Checkpoint`, each under the guard that already precedes it
(`checkSessionBound` at pkg/adapter/coordination.go:89 and :216; `checkpointRootsForSession` at
pkg/adapter/checkpoint.go:94). `checkpointRootsForSession` returns the resolved `*slotState` alongside the
roots. BECAUSE CODE-2's exemption is false (the op lock sits between the guard and the link and queues rather
than refuses) and because sentences 2-4 of CODE-2 stage edits the step landing CODE-1 must make: `Server.barrier`
ceases to exist in S3, so checkpoint.go cannot compile until S3 rewires it, while CODE-2 lands in S5.
ALTERNATIVES: (a) keep the paragraph in CODE-2 and only replace its last sentence — leaves S5's deliverable
text staging an S3 edit, which is the split that produced this finding; (b) a second helper returning the entry
beside `checkpointRootsForSession` — duplication, and two `s.mu` acquisitions recreate the same missing-entry
class between them; (c) keep the barrier gate pod-wide so the missing-entry case never arises — ruled out by
earlier rounds (§10.1.8 step 3 fixes the gate's unit at the session; a pod-wide gate cross-links co-tenant
barriers); (d) have deregistration close in-flight gates — a new mechanism where the detached pointer already
gives correct semantics, and it would make the barrier return early with an empty ref.

CORRECTS [standing context, "Guard ordering, rather than guard existence"]: its closing clause "so a per-entry
gate never has to handle a missing entry" is false and is the source of CODE-2's exemption. The guard at
pkg/adapter/checkpoint.go:94 does precede the link at :122, but `s.ops.Begin` at :111 sits between them and
queues a co-tenant checkpoint for the whole of that session's upload (pkg/adapter/oplock.go:89-128: a distinct
session id is admitted and waits; only the same id coalesces). Both deregistration paths run in that window
(pkg/adapter/session.go:237-239, pkg/adapter/slotsession.go:347-361). The bullet's own last sentence already
states the queueing fact and does not connect it to the clause above it. What is true: the guard validates an
entry the link site must be handed, rather than one it can re-look-up.
EVIDENCE: pkg/adapter/checkpoint.go:94,111,122; pkg/adapter/oplock.go:89-128

FACT: `checkpointRootsForSession` (pkg/adapter/slot.go:153-166) copies three fields under `s.mu` and returns
only `[]workspace.NamedRoot`; it deliberately drops the entry pointer. It has exactly two callers,
pkg/adapter/checkpoint.go:94 and pkg/adapter/resume.go:178, so a signature change costs one `_` in resume.go
and one §9 line. EVIDENCE: pkg/adapter/slot.go:153-166

FACT: `pkg/adapter/coordination_test.go` is `package adapter` (internal) and `pkg/adapter/checkpoint_stream_test.go`
is `package adapter_test` (external). The bufconn fixtures a checkpoint-stream case needs (`concurrentServer`
slot_test.go:24, `adapterClient` server_test.go:90, `slotStartReq` slot_test.go:37, `driveCheckpointConc`
checkpoint_stream_test.go:384) all live in the external package. A case told to land in `coordination_test.go`
that drives a Checkpoint stream hits a package wall. EVIDENCE: pkg/adapter/coordination_test.go:3;
pkg/adapter/checkpoint_stream_test.go:3-4

FACT: tier 1 already runs `-race` on the whole repo (`extra := []string{"-race", ...}`), so an adapter-internal
concurrency case does not have to go to tier 7a to get the detector. EVIDENCE: cmd/lenny-test/cmd_run.go:880

FACT: both deregistration paths remove the slot tree after taking the entry out of the map
(pkg/adapter/session.go:271, pkg/adapter/holdstate.go:249), so an in-flight checkpoint whose session is
deregistered mid-upload can fail on its archive. A case on this path asserts the link, the ack's
`checkpoint_ref`, and the RPC's return, and must not assert a successful `CheckpointSummary` or it flakes.
`barrierGate.link` records the id at checkpoint.go:122 before any chunk work and `defer s.barrier.complete()`
fires on any stream return, so those three assertions hold whatever the upload does.
EVIDENCE: pkg/adapter/coordination.go:158-188; pkg/adapter/checkpoint.go:122-125

WATCHOUT: §8's preamble at non-spec-changes.md:159 says "Each must fail against the pre-fix code." The new case
does not: against the pre-fix pod-wide gate the link never sees a missing entry, so it passes. It fails against
the re-lookup reading of CODE-1's resolve, which is the implementation the deliverable rules out. The preamble
is already imprecise for the two bullets that call themselves amendments of landed cases, so scope it once
rather than adding an exception clause to the new bullet alone. EVIDENCE: non-spec-changes.md:159, :175-181, :197-198

UNVERIFIED: whether any tier-7a or tier-4 case would also break under a re-lookup implementation. I checked the
listed §8 cases and none drives a deregistration concurrently with a barrier or a stream; I did not sweep the
tagged suites for an incidental one. Whoever writes the case should grep tests/tier7a_load_local and
tests/tier8_chaos for a Shutdown issued while a barrier is open before assuming tier 1 is the only home.



### Post-fix review of G1 (run 3, round 5): CODE-2 exemption + missing mid-flight deregistration case

Scope: verify the fixer's own edits only (LANDED / DRIFT / CITATIONS). Diff taken against
/home/ec2-user/lenny/scratchpad/cp-snap/r5-prefix. Three files changed: non-spec-changes.md,
spec-changes.md (pass 21 record only), summary.md. review-log.md, implementation-checklist.md,
problem-statement.md, status.md, deviations.md untouched, as the fixer stated.

**1. LANDED — both findings close.**
- Finding 1. The false exemption sentence is gone. CODE-2 (non-spec-changes.md:85-88) is now the
  generation gate at coordination.go:236 plus a pointer to CODE-1's resolve rule. CODE-1
  (:45-66) states the rule over three handlers and gives the mechanism: `checkpointRootsForSession`
  returns the resolved `*slotState` alongside the roots (:58-59), with the op-lock window (:60-63)
  as the reason a re-lookup is unsafe and the empty `checkpoint_ref` as the failure mode (:64-65).
  §9 records the signature change on the slot.go line (:350-351), the link and deferred complete on
  the checkpoint.go line (:345-346), and adds resume.go (:348-349). This is the fix the finding
  asked for, at the sites it named.
- Finding 2. A tier-1 bullet is added at :192-210 pinning the mid-flight deregistration path, with
  the assertions the finding asked for (link, ack id, deferred quiescence clear against the detached
  entry, RPC returns, co-tenant barrier unaffected), the package constraint, and the tier named. It
  lands in TEST-1 (file list :142-144) and therefore in S6, which declares tier 1 and depends on S3.

**2. CITATIONS — every new citation opened and correct.**
  slot.go:153-166 (helper signature/guard, verified func at :153, guard at :163-166, returns roots
  only at :175); checkpoint.go:94/:111/:122/:124; oplock.go:119-129 (distinct session id admitted,
  `l.wait` blocks); session.go:237-239 and :271 (`_ = removeSlotTree(st)`); slotsession.go:347-361
  (`deregisterStartedSessions`, `deregisterSlotLocked` at :361); holdstate.go:249
  (`_ = removeSlotTree(m.state)`); coordination.go:89 and :216 (`checkSessionBound`), :236 (gate),
  :245-269 (quiesce-and-hold); resume.go:178 (`restoreChunks`, func at :169);
  checkpoint_stream_test.go:417 (`TestCheckpointStreamConcurrentPerSlotAllCaptured_spec_5_2`) and
  :384 (`driveCheckpointConc`); slot_test.go:24 (`concurrentServer`) and :37 (`slotStartReq`);
  server_test.go:90 (`adapterClient`); cmd/lenny-test/cmd_run.go:880 (`-race`, inside `runUnitTier`
  at :869). Package declarations confirmed: checkpoint_stream_test.go / slot_test.go /
  server_test.go are `package adapter_test`, coordination_test.go is `package adapter`.
  `checkpointRootsForSession` has exactly the two callers named (grep over the tree); `s.barrier`
  is read at checkpoint.go:122/:124 and coordination.go:64-66/:264/:269 plus in-package tests, all
  in §9's list; server.go:314 carries `barrier barrierGate` as summary.md:58 claims.

**3. DRIFT — two parallel statements in §8 went stale.** Reported as findings:
- non-spec-changes.md:246-250, the IMPLEMENTOR'S CHOICE paragraph, still leaves "which existing
  tier-1 file in `pkg/adapter` each new case lands in" and "the helper that binds and starts a
  second session on one `adapter.Server`" to the implementor, while the new bullet fixes both for
  the new case and gives the reason the in-package file cannot hold it.
- non-spec-changes.md:158-166, the paragraph that splits the cases by tier, still says the tier-1
  cases are "the per-session fenced value, the hold records, and the independence of the barrier
  gates" and that "the concurrency cases are pinned at tier 7a under `-race`". The new case is
  neither of the named tier-1 subjects and is a concurrent path pinned at tier 1.

No other drift found. CODE-2's missing tier line is pre-existing (present in the r5-prefix
snapshot) and outside this round. spec-changes.md pass 21 is a historical pass record and its
statements about what CODE-2 previously said are correct as history.


### [non-spec.5.review-applicability.1]

FACT: every test-lane step in the neighbouring proposals declares tier 0 explicitly, so tier 0 is
not implied by naming a higher tier in a step's `Tiers` list. 0079 S9-S12 read `Tiers 0, 1, 2`,
`Tiers 0, 10`, `Tiers 0, 5`, `Tiers 0, 7a`; 0078 S3-S5 read `Tiers 0, 1`, `Tiers 0, 4`,
`Tiers 0, 7a`; 0073 S19 reads `Tiers 0, 8`. 0076's S6 reads `Tiers 1, 4, 7a` and is the last step —
EVIDENCE: proposals/0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod.md:57-64;
proposals/0078_fix_keep-the-pod-listener-across-a-session-teardown.md:93-101;
proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:355-360;
.../0076_....implementation-checklist.md:19-21

FACT: the tier-0 omission on the TEST-1 step is inherited rather than new. Pass 9's staged target
sequence already wrote "**S7 · test** — TEST-1 ... Tiers 7a, 8", so the non-spec rewrite that turned
it into `Tiers 1, 4, 7a` carried the omission forward while fixing the tier-8 half — EVIDENCE:
.../0076_....spec-changes.md:1110-1111

DECISION: filed only the S6 tier-0 omission — BECAUSE it is the one candidate with a named gate
(`go vet ./...` plus golangci-lint at tier 0, which compiles the untagged `pkg/adapter/*_test.go`
TEST-1 adds), a named rule (.claude/rules/test-coverage.md "Always run tier 0 and tier 1 on every
touched package"), and no other step after it to catch a break — ALTERNATIVES: rejected three
candidates below.

DECISION: did NOT file "TEST-1 names `pkg/adapter/holdstate_test.go` while §8 assigns that amendment
to the step landing CODE-3" — BECAUSE §8's bullet resolves the ownership in one explicit sentence
("it is the disposition CODE-3 needs so that the step landing CODE-3 does not turn tier 1 red"), and
this loop has refuted four findings on exactly the "the proposal already handles this in its own
text" ground. The underlying tension is real: `holdstate_test.go:713` asserts `LastGeneration != 7`
for BOTH `sess-a` and `sess-b`, so S3 turns tier 1 red unless S3 amends it, yet TEST-1 (S6) lists the
file among "the cases land in" — EVIDENCE: .../non-spec-changes.md:127-131, :174-181;
pkg/adapter/holdstate_test.go:700-716 — OPEN for a later round or the human.

DECISION: did NOT file "S5 (CODE-2) omits tier 4 while §8 says 'Tier 4 covers the same flow across the
gateway, the session store, and the pod'" — BECAUSE S6 declares tier 4 and comes after S5, so the
case has a legal home, and the sentence is loose narrative rather than a staged case. Note the dual
horn if a later round wants it: attributing that tier-4 case to S4 makes S4 fail, since CODE-2 has
not landed there — EVIDENCE: .../non-spec-changes.md:248-249; .../implementation-checklist.md:15-21

FACT: derived inventories re-checked this round and clean. Every CODE-1/2/3/4 code citation resolves
verbatim: pkg/adapter/coordination.go :29-32 :44 :52 :63 :89 :92-94 :99 :108 :112-113 :126-128 :148
:158-166 :180-188 :211-212 :216 :223-226 :236 :246 :269; pkg/adapter/server.go :302 :307 :314;
pkg/adapter/holdstate.go :43 :119 :128 :130-132 :187 :206 :225 :283 (CODE-3's enumeration is now
complete, :187 and :206 included); pkg/adapter/slotsession.go :267 :282-285; pkg/adapter/slot.go
:153-166; pkg/adapter/checkpoint.go :94 :122; barrier.go :190-201 :229-246; pgstore.go :60 :140 :177
:244-248 :249 :260; memstore.go :46 :58-61; migrations/0050:38-39; migrations/0164:44;
prod_columns_test.go :295 (0112) :583 (0180) :610; Makefile:91-94; proto_no_drift_test.go:70;
schemas/lenny-adapter.proto:1451 = pkg/proto/adapter/v1/lenny-adapter.pb.go:4966;
cmd/lenny-test/cmd_run.go:498-509 and :635-641; scripts/lint-migrations.sh:45 and :74-91.

FACT: the coordfixture consumer set is exactly the three files §9 lists. `grep -rln coordfixture`
returns coordfixture.go plus tests/tier4_integration/coordination_fence_split_brain_test.go,
tests/tier7a_load_local/coordination_colocation_race_test.go, and
tests/tier8_chaos/coordination_crash_takeover_test.go. The pod-wide accessor callers are exactly
holdstate.go:119, pkg/adapter/coordination_test.go:73/:279/:298,
pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163, and coordfixture.go:115. §9 covers
every one. Do not re-derive.

FACT: no gate is tripped by the migration. `scripts/lint-migrations.sh` pass 5 keys on
`drop column`, and 0181 drops a CHECK constraint rather than a column, so the Phase-3 `DO $$`
preflight requirement does not apply. Pass 4 keys on `add column`, and 0181 adds none. No test
asserts a `column_default` for `sessions.coordination_generation` or
`coordination_lease.coordination_generation`. Every raw-SQL `INSERT INTO sessions` in the tree omits
the column and takes its default, so the tightened `>= 1` check breaks no seed path — EVIDENCE:
scripts/lint-migrations.sh:101-149; tests/tier2_component/rls/*.go;
tests/tier2_component/migrations/prod_schema_test.go:186;
tests/tier2_component/stores/diagnostics_prodsource_test.go:55-57;
tests/tier4_integration/diagnostics_test.go:81-83

FACT: §8's two baseline-shift classes are complete against the tree. Every other
`CoordinationGeneration` seed in a test seeds a non-zero constant explicitly
(sessionserver/failure_test.go, coordination_fence_test.go, barrier/*_test.go, coordlease_test.go,
coordination_mirror_test.go, wiring_test.go, checkpoint_intent_generation_test.go:46/:118/:128,
prestop_no_double_checkpoint_test.go:121), so none shifts. `tests/tier4_integration/
coordination_fence_split_brain_test.go:72` seeds at 1 explicitly and is in §9 for CODE-1's accessor
change alone, which is what §8 says.

WATCHOUT: `ls migrations/` returns `*_test.go` files as well as `*.sql`. Migration behaviour tests
live in BOTH `migrations/` and `tests/tier2_component/migrations/`, but
`scripts/lint-migrations.sh` pass 3 greps only the latter (`TEST_DIR` at :45). A case placed in
`migrations/` satisfies nothing — EVIDENCE: scripts/lint-migrations.sh:45,:76-91

FACT: `tests/claim-map.json` carries line-numbered code surfaces into
`pkg/adapter/coordination.go` (:177 "conceded at :81-82", :452 "handler :212", :464 "handler :85"),
which CODE-1 and CODE-2 will move. Nothing resolves them: `claim_register_test.go` only rejects a
surface that is ONLY a line reference, and `claim_register_proto_agreement_test.go` joins the
register to the proto rather than to a line. §9 correctly omits the file — EVIDENCE:
tests/claim-map.json:177,:452,:464; tests/tier0_static/claim_register_test.go:145-157,:275-281

UNVERIFIED: `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16` spells the exemption
as "the first CoordinatorFence on a pod's lifetime", which D6 retires, and the file is absent from
§9 — the ledger's residue entry claims it is "covered only by [its file] appearing in §9", which is
false. Not filed, because the same class (a stale code comment omitted from §9) was refuted this run
on `pkg/gateway/coordination/barrier/barrier.go`. A docs or edit-sites lens should decide.

UNVERIFIED: §8's tier sentence attributes "the registry state" to tier 2 while the next sentence says
the per-session fenced value, the hold records, and the barrier-gate independence "are observable
only inside `pkg/adapter` and are pinned at tier 1", and no tier-2 case in the list covers the
registry. S3 declares tier 2 on that basis. Harmless (an extra tier runs existing tests), so not
filed — EVIDENCE: .../non-spec-changes.md:137-142; .../implementation-checklist.md:10-14


### [non-spec.5.review-citations.1]

DECISION: returned an empty findings list — BECAUSE I re-resolved every concrete citation in
`non-spec-changes.md` (SCHEMA-1, CODE-1 through CODE-4, TEST-1, §8, §9), in `summary.md`, in the
checklist, and every anchor and code citation in D5/D6/D7, SPEC-1, SPEC-2, and SPEC-3, and each
one says what the proposal claims — ALTERNATIVES: rejected filing three sub-line drifts and two
loose rationale sentences, all recorded below as FACT/WATCHOUT rather than findings.

FACT: the round-5 snapshot is byte-identical to the live proposal outside the review log.
`diff -ru scratchpad/cp-snap/0076-run3/non-spec-r5 proposals/0076_.../ --exclude='*review-log*'`
is empty, so the "read the diff first" instruction had no target for a second consecutive round
(round 3's edit-sites lens recorded the same for `non-spec-r3`). The newest proposal text is still
round 3's S3/S4 split fix (files stamped 08:20-08:21); round 4 filed nothing and wrote nothing.
— EVIDENCE: `ls -la` on the folder; `diff -ru ... --exclude='*review-log*'`

FACT: three citations drift by a few lines without changing meaning, and a future citation lens
should NOT file any of them. (1) `pkg/gateway/coordination/coordination/coordination.go:408` is
cited in §8's tier-4 bullet for "the `Sweeper` records an adoption backoff"; :408 is inside the
comment that explains the backoff and the `s.recordAdoptionBackoff(row.ID)` call is at :415, in
the same `if ferr != nil` block. (2) `pkg/adapter/slotsession.go` is cited as `:283-285` in CODE-1
and `:282-285` in CODE-3 for the same `heldSession` struct; the type line is :282 and both ranges
contain the fields. (3) `pkg/adapter/coordination_test.go:184-197` for
`TestCheckpointBarrierRejectsWithoutFence`: :184 is blank, the doc comment opens at :185 and the
function closes at :197. — EVIDENCE: `pkg/gateway/coordination/coordination/coordination.go:399-416`;
`pkg/adapter/slotsession.go:282-285`; `pkg/adapter/coordination_test.go:185-197`

FACT: CODE-3's `holdState.gen` enumeration is now complete and correct, so the standing context's
bullet saying "CODE-3's staged enumeration names `:43`, `:119`, `:130-132`, `:225`, and `:283` and
omits `:187` and `:206`, so the enumeration is short by two" is stale. The live CODE-3 text names
:43, :119, :128, :187, :206, :225, :283, and :130-132, and `grep -n "hold\.gen"` returns exactly
:128 (write) and :187 (read). — EVIDENCE: `non-spec-changes.md:75-84`; `pkg/adapter/holdstate.go:128`, `:187`

WATCHOUT: two identical rationale sentences (`summary.md:78-80` and `non-spec-changes.md:115-118`)
say "a commit that tightens the session row's check to `>= 1` while either `Create` path still
writes an explicit zero rejects every session insert". Only `pgstore.Create` reaches Postgres;
`memstore.Create` cannot make the CHECK reject anything. The commit-grouping conclusion is right
for `pgstore`, so this is loose wording in a rationale rather than a defect, and it sits squarely
in the already-refuted "wording precision in an explanatory paragraph that lands nowhere in
`spec/`" class. Do not file it. — EVIDENCE: `pkg/gateway/session/sessionstore/memstore/memstore.go:46-61`;
`pkg/gateway/session/sessionstore/pgstore/pgstore.go:177`, `:249`

FACT: `pgstore` and `memstore` both clamp `CoordinationGeneration` to its previous value on the
mutate path, so nothing after `Create` can drive a row below the new baseline and the tightened
`CHECK (coordination_generation >= 1)` has exactly one write path to guard. Nobody had recorded
this, and it is what makes CODE-4's "both `Create` floors plus the migration in one commit" the
complete edit set rather than a partial one. — EVIDENCE:
`pkg/gateway/session/sessionstore/pgstore/pgstore.go:466-477`;
`pkg/gateway/session/sessionstore/memstore/memstore.go:135-146`

FACT: `coordination_lease` is created and touched by migration 0164 alone
(`grep -ln coordination_lease migrations/*.sql` returns only 0164's up and down), so CODE-4's
`DEFAULT 1` edit to `coordination_lease.coordination_generation` has one prior owner and no
intervening migration to reconcile with. The column carries no CHECK, only `NOT NULL DEFAULT 0`.
— EVIDENCE: `migrations/0164_coordination_lease.up.sql:44`

USEFUL [Standing context, "Derived inventories"]: the claim that every SPEC-1/SPEC-2 anchor and
every CODE-1..CODE-4/§8/§9 code citation resolves held on a full independent re-resolution this
round. I re-checked roughly seventy sites across `spec/10`, `spec/28`, `spec/29`, `spec/04`,
`schemas/lenny-adapter.proto`, `pkg/adapter`, `pkg/gateway`, `migrations/`, `scripts/`,
`cmd/lenny-test`, and the test tree, and found nothing beyond the three sub-line drifts above.
A future citation lens can trust the inventory and spend its budget on attributed behaviors instead.

USEFUL [Standing context, "Editing hazards in this proposal's own files"]: the warning that
`cat -n *spec-changes.md` globs two files was needed. I read every proposal file by full name.

OPEN: `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16` spells the exemption as
"the first CoordinatorFence on a pod's lifetime", D6 rescopes it to the first fence for that
session on that pod, and that file appears nowhere in §9 (§9 carries the sibling
`checkpointbarrier_test.go` but not this one). The ledger's D6-cascade OPEN already records the
citation; what it says about §9 coverage ("the other two are covered only by their files appearing
in §9") is true of `pkg/adapter/coordination_test.go` and false of this file. It is a stale test
comment whose assertions stay valid, so it falls in the same class as the refuted
`barrier.go` `Target.CoordinationGeneration` comment finding. Whoever next writes §9 should add
the path; a reviewer should not file it.

UNVERIFIED: SPEC-1 calls the "Generation counters" bullet "§10.1's" while the bullet lives in
§10.1.1 (`spec/10_gateway-internals.md:30`, under the `#### 10.1.1` heading at `:5`), and §9's
spec/10 parenthetical lists §10.1, §10.1.2, §10.1.4, and §10.1.8 without §10.1.1. §10.1.1 is
inside §10.1, so I judged it containment rather than misattribution and did not file it. A
lens that wants the parenthetical to name the subsection an edit actually lands in should decide.


### [non-spec.5.review-client-surface.1]

DECISION: Returned an empty findings list for the client-facing-surface lens — BECAUSE the only externally-consumed contract this proposal touches is the gateway-to-adapter proto, and every parallel representation of it is either already staged or provably unaffected; the two candidates I could have filed both fall inside classes this loop has already refuted — ALTERNATIVES: (a) filing the missing tier-3 case for D6's per-session fence acceptance, rejected because the wire's field set does not change, the behavior is pinned at tiers 1, 4, 7a, and 8, and §8's "3 (the wire gate's behavior)" plausibly denotes D7's barrier gate alone, so it is hardening rather than required coverage; (b) filing `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` as an unstaged pod-lifetime-exemption site, rejected because it is a Go doc comment in `pkg/`, which class (d) does not reach, and it is the same class the `barrier.go:62-65` finding was refuted on.

FACT: The client-surface inventory for `coordination_generation` is closed and empty outside the adapter proto. No REST/JSON tag exposes it (`grep -rn "coordination_generation\|coordinationGeneration" --include=*.go pkg/gateway cmd | grep json:` returns nothing), it appears in no `sdks/` file in any language, in no chart or CRD, and in no `schemas/*.json`. Its only reader-facing doc mentions state no unit, baseline, or gate — EVIDENCE: docs/getting-started/concepts.md:101; docs/getting-started/architecture.md:173; docs/reference/adapter-contract.md:68-69; docs/reference/metrics.md:307; spec/04_system-components.md:711-712. This is the "docs surface re-derived seven times" bullet checked an eighth time from the client side; the next round can drop it from the standing context.

FACT: SCHEMA-1's codegen mechanics all resolve exactly. `Makefile:91-94` is `.PHONY: generate-proto` through `buf generate`; `tests/tier0_static/proto_no_drift_test.go:70` is `TestProtoStubsMatchGeneratedOutput`; `schemas/lenny-adapter.proto:1451` ("protocol. Strictly monotonic on the pod side per session.") reappears verbatim at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966`; the `CoordinatorFence` RPC comment lands twice in `lenny-adapter_grpc.pb.go`, at `:180` (client interface) and `:632` (server interface). §9 lists both generated files. No further generated representation exists: `schemas/embed.go` embeds `lenny-adapter.proto` itself (the file, not a derived artifact) for `cmd/lenny-compliance` and `lenny runtime validate`, so it follows the source with no separate edit — EVIDENCE: schemas/embed.go:13-26; schemas/buf.gen.yaml.

FACT: The seven-carrier proto enumeration in the standing context is complete; there is no eighth. I looked specifically for a carrier describing the barrier-to-Checkpoint-stream link that CODE-1 makes per session. The `Checkpoint` RPC comment (`schemas/lenny-adapter.proto:114-133`) and `CheckpointStart.checkpoint_id` (`:1187-1190`, "The adapter echoes it back on CheckpointBarrierAck.checkpoint_ref for the barrier-window path") are both session-neutral and stay true under the per-entry gate. Do not widen SCHEMA-1 past seven on that ground — EVIDENCE: schemas/lenny-adapter.proto:114-133, :1186-1190.

FACT: No landed tier-3 contract test asserts a coordination gate outcome, so D6 and D7 turn nothing red at tier 3. Every tier-3 site is descriptor- or encoding-level: `adapter_generation_fence` reads protoreflect descriptors and marshals, `adapter_checkpointbarrier` pins `CheckpointBarrierResponse`'s field set and numbers, `gatewaycontrol_scrub:216` and `adapter_reportusage:66` pin field numbers. The D7 case §8 stages is genuinely new work rather than an amendment — EVIDENCE: tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:104-263; tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48-77.

OPEN: `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` reads "The first fence on a pod's lifetime is always accepted", a FOURTH site spelling D6's exemption per pod lifetime and the only one on the production side. The ledger's D6-cascade OPEN names three sites and all three are test-side (`pkg/adapter/coordination_test.go:58-61`, `tests/testinfra/coordfixture/coordfixture.go:106-108`, `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16`); this one is in no §9 entry and no deliverable. The sentence stays literally true after D6 (the pod's first fence is still accepted) and only becomes incomplete, which is why I did not file it, but a pass that rescopes the other three should take it in the same edit — EVIDENCE: pkg/gateway/runtime/adapterclient/coordinatorfence.go:33-41; review-log.md:325-331.

WATCHOUT: `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go` is NOT in §9, so the ledger's claim that the two unnamed D6-cascade sites "are covered only by their files appearing in §9" holds for `coordination_test.go` and fails for this one. §9's only `adapterclient` entry is `checkpointbarrier_test.go` — EVIDENCE: 0076...non-spec-changes.md:317; review-log.md:329-330.

USEFUL [standing context, "Derived inventories"]: the sentence "The `docs/`, `charts/`, `sdks/`, and §16 surface has been re-derived seven times and states no unit, baseline, or gate" is accurate and saved me from re-walking the docs tree twice; I spot-checked it from the client side and it held on every hit.

USEFUL [Refuted classes]: the refutation of the `barrier.go:62-65` stale-comment finding is what kept me from filing `adapterclient/coordinatorfence.go:37`. Keep it; it is the precedent that settles the whole "a code comment becomes less precise" class.


### [non-spec.5.review-docs-alignment.1]

FINDINGS: none. Eighth derivation of the `docs/` surface; still empty.

USEFUL [non-spec.2.review-docs-alignment.1]: its eight-site `docs/` inventory is complete and still
current at this round's text. I re-grepped the whole tree rather than trusting it and found no ninth
site. Re-verified live: `docs/reference/adapter-contract.md:68`, `:69`, `:96`, `:97`;
`docs/reference/metrics.md:307`, `:309`, `:310-312`; `docs/getting-started/concepts.md:101`;
`docs/getting-started/architecture.md:173`; `docs/operator-guide/upgrades.md:47-53`, `:313`.
`docs/reference/error-catalog.md`, `state-machines.md`, `glossary.md` (except the CH-ADAPTEREVENTS
message list at `:54`, which names `CheckpointBarrierAck` and no generation), `docs/api/`,
`docs/client-guide/`, and `docs/runtime-author-guide/` return nothing for
`CoordinatorFence|CheckpointBarrier|coordination_generation|fence|hold state`.
EVIDENCE: docs/reference/error-catalog.md (no hit); docs/api/internal.md (no hit)

FACT: no tier-11 test is reached, and the reason is stronger than "no test names the identifiers". The
tier-11 tests that read `spec/28` and `spec/29` read surfaces SPEC-2 does not touch:
`spec_28_index_rows_test.go` reads `spec/README.md` anchor rows, `spec_28_taxonomy_test.go` and
`spec_28_register_writers_test.go` read §28.2/§28.3 tables and pin byte-exact §12.6/§4.6.3/§7.2
sentences, `spec_28_ownership_test.go` reads proposal prose, `off_holder_matrix_stated_outcome_test.go`
reads the §29.3 off-holder matrix rather than §28.8 or §29.10, and
`concurrent_slot_lifecycle_doc_reconciliation_test.go` reads §5.2 and §6.2 only. Nothing reads a §28.8
cell body, §29.10's lists, §10.1.2, or §10.1.4's Observability bullet. Checklist S1's tier-11 claim is
defensive.
EVIDENCE: tests/tier11_docs/off_holder_matrix_stated_outcome_test.go:60-62;
tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:81, :91

FACT: the §10.1.4 Observability edit (dropping the generation from the pod-level
`coordinator_connection_lost` line) has exactly two mirrors in the tree and SPEC-1/SPEC-2 stage both.
`grep -rn coordinator_connection_lost spec/ docs/ schemas/` returns `spec/10_gateway-internals.md:60`
and `spec/29_communication-scenarios.md:1274` and nothing else. No CloudEvents catalog row, no runbook,
no operator page carries the event or a `last_generation` field.
EVIDENCE: spec/10_gateway-internals.md:60; spec/29_communication-scenarios.md:1274;
docs/reference/cloudevents-catalog.md:71

FACT: the round-4 migration-0181 fix checks out on all three of its citations, so a later round need not
re-derive them. `{migration: "0180", table: "checkpoint_manifest"}` sits at
`tests/tier2_component/migrations/prod_columns_test.go:583` with a comment saying it adds no column and
is present so `TestProdMigrationsRollBackPerStep` steps its `.down.sql`;
`{migration: "0112", table: "session_checkpoints"}` sits at `:295`; `TestProdMigrationsRollBackPerStep`
is declared at `:610` and walks `prodMigrationSchema` in reverse.
EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:575-583, :288-295, :610

FACT: SCHEMA-1's generated-stub claim verifies byte for byte.
`schemas/lenny-adapter.proto:1449-1451` reappears verbatim at
`pkg/proto/adapter/v1/lenny-adapter.pb.go:4964-4966`, and the `CoordinatorFence` RPC comment appears
twice in `lenny-adapter_grpc.pb.go`, at `:180` (client interface) and `:632` (server interface). The
field comment at `:1451` already reads "Strictly monotonic on the pod side per session.", which is why
SPEC-2 leaves it alone.
EVIDENCE: pkg/proto/adapter/v1/lenny-adapter.pb.go:4964-4966;
pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:180, :632

WATCHOUT: `docs/reference/adapter-contract.md:79` states "The adapter maintains a per-session operation
lock" while `spec/28_communication-channels.md:1806` states "a pod-level operation lock that serializes
`Checkpoint` and `Interrupt` across the pod's slots", and the tree agrees with the spec. It reads like a
co-tenancy defect this proposal should own and it is not one: 0076 changes neither lock, so it is
pre-existing docs drift for a docs loop. I nearly filed it; do not.
EVIDENCE: docs/reference/adapter-contract.md:79; spec/28_communication-channels.md:1806

FACT: `tests/claim-map.json` carries three line-number citations into `pkg/adapter/coordination.go`
(`:81-82` for the R16 `ABSENT` cancellation row, `:212` for the barrier handler, `:85` for the fence
handler). CODE-1 through CODE-3 rewrite that file and all three will drift. It is not a finding under
(d), which enumerates `spec/`, `docs/`, `schemas/`, and `charts/` and not `tests/`, and
`claim_register_test.go` is a schema gate that does not resolve a surface citation. Recording it so the
implementation loop can refresh them in passing.
EVIDENCE: tests/claim-map.json:177, :452, :464

MISTAKE (mine, avoided): I re-derived the D5 co-tenant-freeze residual as a candidate under this lens's
"accepted failure mode stated only in reasoning" category before checking round 2's shard. Its
observable outcome is shipped spec text: `spec/10_gateway-internals.md:58` states that on a pod serving
more than one session the hold timeout terminates every session the adapter has started on that pod.
The premise (two replicas coordinating two co-tenant sessions) is separately recorded as unreachable in
the shipped tree. Cost about ten minutes.
EVIDENCE: spec/10_gateway-internals.md:58

OPEN: this lens has now returned nothing twice on a surface that has been re-derived eight times. The
standing-context bullet asserting the `docs/`, `charts/`, `sdks/`, and §16 surface is empty is credited
by both runs and can be shortened to that sentence plus the eight-site inventory when the section is
next compacted; the per-lens re-derivation is what is costing rounds, not the surface.


### [non-spec.5.review-edit-sites.1]

FACT: nothing in the proposal changed this round. `diff -ru scratchpad/cp-snap/0076-run3/non-spec-r5 proposals/0076_.../` shows only
`*.review-log.md` differing (compaction pass 13). `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`,
`summary.md`, and `status.md` are byte-identical to the snapshot, so round 5's "read the changed sections hardest" instruction had
no changed sections to read. — EVIDENCE: scratchpad/cp-snap/0076-run3/non-spec-r5/ vs proposals/0076_fix_scope-the-coordination-generation-to-the-session/

FACT: the CODE-4 / §8 / §9 baseline inventory is exhaustively correct where I could check it. Re-verified end to end this round, do
not re-derive: `coordination_takeover_test.go` seeds unset at :74, :142, :241, :301 and its five generation assertions all shift;
`coordination_mirror_test.go` asserts a generation only at :116-117 on the row seeded at 2 (:84), so its unset rows (:85-86, :131-132,
:156) are genuinely unasserted; `coordination_scoping_test.go` seeds unset everywhere and asserts no generation, so its absence from
§9 is right; `tests/tier8_chaos/coordination_crash_takeover_test.go` has five generation assertions, of which only :267, :283, :296
sit on the unset-seeded session (:239-241) and shift, while :147 and :227 sit on the two subtests seeded explicitly at 1 (:118, :179)
and do not — which is exactly the split §8 states; `tests/tier7a_load_local/coordination_colocation_race_test.go` :287-288 is the
assertion of 0 and :264-265 the assertion of 2, both cited exactly. Raw-SQL session fixtures (tier2 rls/, tier2 migrations/,
tier4 integration, sessionusage pgstore) all omit `coordination_generation` from their INSERT column lists, so the `>= 1` CHECK
breaks no seed path. — EVIDENCE: pkg/gateway/coordination/coordination/coordination_takeover_test.go:74,:94,:142,:270,:301;
tests/tier8_chaos/coordination_crash_takeover_test.go:118,:147,:179,:227,:239-241,:267,:283,:296

FACT: every code citation in SCHEMA-1, CODE-1 through CODE-4, TEST-1, §8, and §9 that I resolved this round is exact, including the
ones easiest to get wrong: `pgstore.go:140` (Create), `:177` (the column list naming `coordination_generation`), `:244-248` (the
schemaVersion normalisation the floor sits beside); `memstore.go:46`, `:58-61`; `coordfence.go:143` (the read) and `:147-153` (the
floor, `if gen <= 0 {` through its closing brace); `coordfixture.go:76` (StartPod), `:98-102` (the StartSession the second co-tenant
rides), `:220-241`/`:231` (ReadoptAndFence and its `r.Pod.Fence`); `slotsession.go:282-285` (heldSession{sessionID, state *slotState});
`memstore_test.go:309-325` whose doc comment does say "Recovery and coordination generations start at zero".

FACT: the seven-carrier proto enumeration in the standing context is complete for the fence/barrier group. There is no eighth carrier
hiding on `CoordinatorFenceResponse.last_fenced_generation` — that field at schemas/lenny-adapter.proto:1465 carries no leading
comment at all. I chased it specifically because a field comment there would have been a carrier nobody named. — EVIDENCE:
schemas/lenny-adapter.proto:1463-1467

FACT: `spec/28`'s two remaining pod-side gap/window restatements are both staged. §28:335 is the `CH-FENCE` Degradation bullet and
§28:1807 is the §28.8 `CH-FENCE` cell, and SPEC-2 names each explicitly. A grep for `coordinator_generation_gap` outside
`spec/10`, `spec/28`, `spec/29` returns only `schemas/lenny-adapter.proto:160` and `:1461`, both SPEC-2/SCHEMA-1 carriers.

FACT: migration 0181's tier-2 registry story holds. `prodMigrationSchema` entries with `create:false` and no `columns` assert nothing
in either `TestProdMigrationsApplyExpectedSchema` or `TestProdMigrationsRollBackPerStep` — the row exists only so the rollback walk
steps the `.down.sql` and so `lint-migrations.sh` pass 3 finds the number. 0180 is that precedent verbatim. `lint-migrations.sh`
passes 4 and 5 do not reach 0181, because it neither ADDs nor DROPs a column. — EVIDENCE:
tests/tier2_component/migrations/prod_columns_test.go:575-583,:610-634; scripts/lint-migrations.sh:74-88,:97-120

FACT: tier-3 contract packages do start live in-process adapter servers (adapter_usage_wired, checkpoint_stream,
adapter_extendcredlease, adapter_session_address, adapter_frame_resolution, gatewaycontrol_scrub all do), so §8's tier-3 D7
acceptance case is reachable at the tier it names. This kills the "the tier-3 case has no reachable home" line of attack for good;
the earlier refutation was right for a reason nobody had checked. — EVIDENCE: `grep -rln "adapter.New(\|adapter\.Server" tests/tier3_contract/`

WATCHOUT: the review log's OPEN on the D6 test-side cascade asserts that two of its three sites "are covered only by their files
appearing in §9". That is false for one of them. `pkg/adapter/coordination_test.go` is in §9 (line 320) and
`tests/testinfra/coordfixture/coordfixture.go` is in §9 with its comment rescope spelled out (lines 321-323), but
`pkg/gateway/runtime/adapterclient/coordinatorfence_test.go` appears nowhere in §9. A future agent reading that OPEN will believe the
site is handled. It is not. — EVIDENCE: proposals/0076_.../0076_...review-log.md:325-331;
proposals/0076_.../0076_...non-spec-changes.md:296-332; pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16

DECISION: I filed exactly one finding, the §9 omission above, and nothing else — BECAUSE every other candidate my lens turned up is
either a close variant of an already-refuted finding (the `barrier.go` `Target.CoordinationGeneration` comment, the tier-3 case's
home, SCHEMA-1's carrier list, CODE-2's silence) or an item the ledger already owns as OPEN with the same remedy —
ALTERNATIVES: I considered and rejected filing (1) `spec/04:712`'s "`CoordinatorFence` … Precondition for any subsequent operational
RPC", which D7 appears to falsify but which refuted class (a) covers as a sender-side obligation inherited from §10.1.2 step 2;
(2) the tension between CODE-1's ground that "§10.1.8 step 3 already fixes the gate's unit at the session" and SPEC-2's staged §29.10
narrowing that keeps "the unit of the quiescence a barrier establishes" unanswered — I read §10.1.8 steps 2 and 3 directly and they
genuinely split, step 3 fixing the stream/ack/release per session while step 2 states "stops accepting new tool call dispatches" of
the adapter with no slot qualifier, so both statements are defensible and the adapter's `quiesced` flag has no production reader
anyway; (3) the six `pod.LastFenced()` call sites in the tier-8 file against §8's three cited lines, which the compiler catches.

FACT: the `docs/`, `charts/`, `schemas/*.json`, `pkg/alerting/`, and `tests/claim-map.json` surfaces are clean under this change, and
I checked them fresh rather than trusting the standing context. `docs/` mentions of `CheckpointBarrier`/`CoordinatorFence` are the
four unit-neutral rows at adapter-contract.md:68-69,:96, metrics.md:40,:197,:307,:309, glossary.md:54, concepts.md:101,
architecture.md:173, and upgrades.md:49. `tests/claim-map.json` contains no `10.1.8`, `10.1.2`, `CH-BARRIER`, or `CH-FENCE` string at
all, so SPEC-2's "no claim-register row moves" is right for a stronger reason than it states.

USEFUL [the standing-context bullet on `## Resolved in adversarial review` living only in the spec-changes file]: it stopped me
looking for pass records in the non-spec file, which is where a fresh reader expects the non-spec lane's history.

USEFUL [the standing-context bullet enumerating the seven proto carriers and warning that a message's doc comment sits above the
`message` line]: it is why I checked `:1463-1467` for a field comment on `last_fenced_generation` instead of assuming the
enumeration was closed.


### [non-spec.5.review-feasibility.1]

DECISION: filed exactly one finding, the two landed `memstore_test.go` cases missing from §8's first-class
baseline-shift enumeration — BECAUSE they sit inside a class §8 defines and then enumerates with a colon,
and §8 separately claims the step landing CODE-4 "amends it rather than leaving tier 1 red" naming only
`TestCreateDefaultsSessionRecordFields` — ALTERNATIVES: rejected three other candidates below, each on a
recorded refutation precedent.

FACT: the whole first class, re-derived independently this round. `grep -rn CoordinationGeneration
--include=*_test.go pkg/ tests/` returns every candidate; the ones on a row created UNSET are exactly:
`tests/tier2_component/coordination/sweep_test.go` (:275-:594), `coordination_takeover_test.go` (:94,:120,
:169,:182,:198,:270,:310,:322), `tests/tier8_chaos/coordination_crash_takeover_test.go` (:267,:283,:296),
`tests/tier7a_load_local/coordination_colocation_race_test.go` (:287-288), and
`pkg/gateway/session/sessionstore/memstore/memstore_test.go` :323-324, :429-431, :490-491. §8 names all but
the last two. Everything else seeds an explicit constant (7, 11, 4, 9, 14, 10, 3, 5, 2) or builds a
`barrier.Target` / manifest record with no session row — EVIDENCE:
`pkg/gateway/session/sessionstore/memstore/memstore_test.go:413`, `:416`, `:429-431`, `:468`, `:471`,
`:490-491`

FACT: §8's second class really does have one site. `TestCheckpointIntentRowCarriesSessionCoordinationGeneration`
and `TestCheckpointStaleCoordinatorDoesNotOrphanNewerWriter` both look like the supersede case but each sets
the session's generation explicitly through `Update` (10 and 3), so neither is calibrated against the create
default — EVIDENCE: `tests/tier4_integration/checkpoint_intent_generation_test.go:46`, `:118`

FACT: migration 0181 clears every gate in `scripts/lint-migrations.sh`. Pass 4 splits on `add column` and
pass 5 greps `drop[[:space:]]+column`; 0181 adds no column and drops a CONSTRAINT, so neither fires. Pass 3
greps the bare number as a substring anywhere under `tests/tier2_component/migrations/`, so the
`prodMigrationSchema` row alone would satisfy it, and the proposal's demand for a behaviour file there is
stricter than the lint rather than wrong — EVIDENCE: `scripts/lint-migrations.sh:74-88`, `:100-121`, `:125-149`

FACT: no raw-SQL fixture writes `sessions.coordination_generation`. `grep -rn "INSERT INTO sessions"` over
the whole tree returns `pgstore.go:170` and fourteen test fixtures, and every fixture's column list is
`(id, tenant_id, state, runtime_ref, root_session_id[, ...])` with the counter omitted, so the `>= 1` check
breaks no seed path. §8's one-sentence claim to that effect is correct and does not need re-deriving.

FACT: `docs/`, `charts/`, `sdks/`, and `schemas/` outside the adapter proto contain exactly one mention of
the counter and it states neither unit nor baseline, so the baseline reaches no doc edit site — EVIDENCE:
`docs/getting-started/concepts.md:101`

FACT: tier-3 contract packages do run real in-process adapter servers over bufconn (adapter_extendcredlease,
checkpoint_stream, adapter_usage_wired and others), so D7's staged tier-3 behavioural case is buildable at
the tier §8 assigns it. A "tier 3 is encode/decode only" finding is dead.

WATCHOUT: `CODE-1` and `CODE-2` split `barrierGate`'s move across steps S3 and S5, and the only reference
CODE-1's body does not explicitly claim is `pkg/adapter/checkpoint.go:122`/`:124` (`s.barrier.link` /
`defer s.barrier.complete()`), which CODE-2 names. Read literally that leaves S3's tree non-compiling under
its own tier-0 gate, the exact hazard the proposal invokes to merge CODE-1 and CODE-3. I did NOT file it,
because CODE-1 says the gate "moves onto the same entry" and its rationale paragraph already discusses
`link()`'s no-session-check behaviour at `:180-188`, so a reasonable reading puts the link site in CODE-1.
If a later round wants it, file it as a step-assignment fix (move the link-site sentence into CODE-1),
not as a design defect.

CORRECTS [spec.1-3.*, the D6 test-side-cascade OPEN]: that entry says "§9 states the coordfixture rescope
and the other two are covered only by their files appearing in §9". That is false for one of the two:
`pkg/gateway/runtime/adapterclient/coordinatorfence_test.go` is absent from §9 entirely (§9 carries
`checkpointbarrier_test.go` from that package and nothing else), so its `:15-16` "first CoordinatorFence on
a pod's lifetime" comment is covered by nothing. It is still not a finding — a stale Go test doc comment is
the same class the `barrier.go` `Target.CoordinationGeneration` comment finding was refuted under — but the
ledger's claim about §9 should be corrected rather than relied on — EVIDENCE:
`pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16`;
non-spec-changes.md:317; `.review-log.md` D6-cascade OPEN

USEFUL [standing context, "Editing hazards in this proposal's own files"]: the `cat -n *spec-changes.md`
glob warning saved a wrong citation set; I named both files in full on every read.

USEFUL [standing context, "Derived inventories"]: not re-deriving the spec-anchor set left the round free
for the test-tree sweep, which is where the finding was. I spot-checked five anchors (`spec/10:30`, `:38`,
`:41`, `:58`, `:183`) and all five resolve, so the inventory's claim holds after this round too.

OPEN: `CODE-1` declares tier 2 ("CODE-1 reaches tiers 0, 1, 2, 4, 7a, and 8") and checklist S3 gates on it,
but no tier-2 package touches the accessors CODE-1 changes: `coordfixture`'s only consumers are the
integration, load_local, and chaos trees, and the three tier-2 packages that build an `adapter.Server`
(slotrelease, warmlayout, translators) never call `LastFencedGeneration`, `BarrierWaiting`, or
`isQuiescedForBarrier`. Running a tier with no case is harmless, so I did not file it, but if a later round
tightens the tier lists this is the one to drop.

OPEN: §8 says "Tier 4 covers the same flow across the gateway, the session store, and the pod" for D7's
acceptance, and names no case, no file, and no step. S5 (CODE-2) declares tiers 0, 1, 3, 7a and not 4. The
sentence is either a claim about a case that does not exist or a loose pointer at the tier-4 co-tenant fence
bullet, which is a different flow. Left unfiled as ambiguous.


### [non-spec.5.review-fresh.1]

DECISION: filed exactly one finding, the D7 case/step mis-batching (§8 files the tier-3 acceptance case
and the tier-4 D7 flow under "The cases that pin CODE-4's baseline", whose step S4 declares tiers 3 and 4
but lands CODE-4 alone, while CODE-2 lands at S5) — BECAUSE it is the same class Pass 19 and Pass 20 both
adjudicated as material (a declared tier goes red under the checklist's own step split), it has a clean
fix inside the non-spec staging, and S4's tier-3 declaration has no other justification in the tree —
ALTERNATIVES: rejected the rolling-deploy/expand-contract objection to migration 0181's `>= 1` CHECK
(an old-version replica's `pgstore.Create` binds an explicit 0 at pgstore.go:260 and would be rejected;
spec/10_gateway-internals.md:420 "Mixed-version replicas must coexist during rollout" and :448), because
`code-best-practices.md` states the platform is pre-deployment, §10.5's operational rules are scoped to
new columns rather than constraint tightening, and no gate catches it; rejected filing
`pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16`'s absence from §9, because its
"first CoordinatorFence on a pod's lifetime is accepted" stays literally true under D6 on a
single-session server; rejected the §8-aggregate-tier-list-omits-11 vs S1-claims-11 nit and the
tier-8 `pod.LastFenced()` enumeration being short by three call sites (:151, :196, :224 are reads too),
both compiler- or harmless-level.

FACT: S4's tier-3 declaration has no independent basis in the tree. `tests/tier3_contract/adapter_checkpointbarrier/`
contains one test, `TestCheckpointBarrierResponseWireContract`, and `tests/tier3_contract/adapter_generation_fence/`
parameterises the generation (`generation_fence_wire_test.go:233-254`), so nothing there shifts under
CODE-4's row baseline. The only tier-3 reason CODE-4 carries is the D7 acceptance case §8 files under it —
EVIDENCE: tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48;
tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:233

FACT: pgstore's `Update` cannot violate a tightened `>= 1` CHECK: it re-reads the row `FOR UPDATE` and
floors `sess.CoordinationGeneration` to the previous row value before the UPDATE, so only `Create`
needs the floor CODE-4 stages — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:460, :475-477

FACT: `coordination_lease.coordination_generation` is always written explicitly by the mirror upsert, so
CODE-4's `DEFAULT 1` on it is cosmetic and cannot desync the mirror; the three other
`coordination_generation` columns (session_eviction_state 0060, session_checkpoint_meta 0148,
checkpoint_manifest 0178) keep `DEFAULT 0` and CODE-4 does not touch them, which is consistent because they
are written explicitly too — EVIDENCE: pkg/gateway/coordination/coordlease/pgstore/pgstore.go:48-58;
migrations/0060_session_eviction_state_columns.up.sql:33; migrations/0148_session_checkpoint_meta.up.sql:30;
migrations/0178_checkpoint_manifest.up.sql:44

FACT: no raw-SQL session fixture anywhere in `tests/` names `coordination_generation` in an INSERT column
list, and `pgstore.go:170` is the only `INSERT INTO sessions` in the tree, so CODE-4's claim that the
tightened check breaks no seed path holds — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:170

FACT: `tests/tier2_component/coordination/sweep_test.go` uses `memstore` with a real Redis, not pgstore,
so its :275-:594 assertions shift through CODE-4's *memstore* floor rather than through migration 0181 —
EVIDENCE: tests/tier2_component/coordination/sweep_test.go:26, :275

FACT: the D6 negative the ledger still records as owed ("a genuine skip on a second fence still producing a
gap") is already pinned by landed `TestCoordinatorFenceGapDetected`, which fences s1 at 3 then 7 on one
session and survives the per-session rescope unchanged, so §8 owes no new case for it —
EVIDENCE: pkg/adapter/coordination_test.go:135, :145, :157

USEFUL [standing context, "Editing hazards in this proposal's own files"]: the note that `cat -n *spec-changes.md`
globs two files saved a whole round of off-by-46 citations; I named every file in full.

USEFUL [standing context, "the extent is cited as :184-197 in the proposal and :185-197 in this log"]:
stopped me building a citation finding on `TestCheckpointBarrierRejectsWithoutFence`'s extent. The doc
comment does start at :185 and the func at :189.

UNVERIFIED: nothing in `§8` or `§9` gives the tier-2 resume-fence-then-takeover case a file, and no
existing tier-2 test constructs a `coordfixture` pod (`coordfixture` appears only under tier 4, 7a, and 8).
It is constructible at tier 2 (memstore + Redis container + the untagged in-process adapter), but nobody
has checked whether the tier-2 coordination suite is the intended home. A code-lane reviewer should settle it.

OPEN: `migrations/0164_coordination_lease.up.sql:40-45`'s column comment states "the adapter rejects a
barrier whose generation does not match its last fenced value", which D7 narrows. Landed migrations are not
edited, so this is a note rather than an edit site, but a later reader will hit it.


### [non-spec.5.review-kubernetes.1]

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE 0076 has no
Kubernetes API surface at all, and I confirmed that rather than assuming it — ALTERNATIVES: I
considered filing on the D5 pod-wide hold freezing co-tenant sessions (recorded as a non-goal, and a
topology question rather than an idiom violation), on the migration-0181 `CHECK (>= 1)` versus a
rolling gateway upgrade (barred by `code-best-practices.md`'s no-backward-compatibility rule), and on
`coord.mu` staying pod-wide versus the tier-7a concurrent-barrier case (falsified, see FACT below).

FACT: the whole change sits below the Kubernetes API layer, so this lens has nothing to grip. There is
no `coordination_generation` on any CRD type or chart CRD, and no controller reads it —
`grep -rn "CoordinationGeneration\|coordination_generation" pkg/apis/ pkg/controller/ charts/` returns
nothing, and the only schema carrier is `schemas/lenny-adapter.proto`. The proposal writes no status
subresource, adds no finalizer, touches no admission webhook, adds no reconcile, and puts nothing new
on the apiserver path. — EVIDENCE: `spec/10_gateway-internals.md:58` (the zero-RBAC posture the design
keeps: "agent pods have zero RBAC bindings and no network path to the kube-apiserver ... a direct
`Sandbox.status.phase = failed` CRD write is not possible"); SPEC-1's §10.1.4 edit is confined to the
Observability bullet at `spec/10_gateway-internals.md:60`, so `AdapterTerminating` plus the orphan
session reconciler at `:59` stay the notification path unchanged.

FACT: `coord.mu` is NOT held across the barrier's blocking wait, so §7 open decision 3 (pod-wide
`coord.mu` versus per-entry) cannot falsify §8's tier-7a case that two accepted co-tenant barriers
"neither waits on the other". `CheckpointBarrier` takes and releases `coord.mu` three times in short
critical sections (`pkg/adapter/coordination.go:232-235`, `:246-248`, `:253-257`) and blocks on
`select { case <-done: case <-ctx.Done(): }` at `:265-268` holding only the gate. The only
cross-session serialisation left is the pod-level `ops` opLock (`pkg/adapter/server.go:296-297`),
which queues rather than refuses. I spent real time chasing this as a lock-contention finding; it is
dead. — EVIDENCE: `pkg/adapter/coordination.go:232-268`

FACT: citations I re-resolved this round, all exact, so a later round need not: `pkg/adapter/server.go:302`
(`coord coordinationState`), `:307` (`hold holdState`), `:314` (`barrier barrierGate`);
`pkg/adapter/slot.go:21` (`type slotState struct`); `pkg/adapter/slotsession.go:267`
(`checkSessionBound`), `:282-285` (`heldSession{sessionID, state *slotState}`);
`pkg/adapter/coordination.go:236` (the `!initialized || gen != fenced` gate), `:224-226` (the
non-positive `InvalidArgument` guard), `:216` (`checkSessionBound` ahead of it);
`migrations/0050_session_record_fields.up.sql:38-39` (`NOT NULL DEFAULT 0` + inline
`CHECK (coordination_generation >= 0)`); `migrations/0164_coordination_lease.up.sql:44`
(`coordination_generation BIGINT NOT NULL DEFAULT 0`); `migrations/0180_*` is the last `.sql` pair, so
0181 is free; `tests/tier2_component/migrations/prod_columns_test.go:295`
(`{migration: "0112", table: "session_checkpoints"}`), `:583`
(`{migration: "0180", table: "checkpoint_manifest"}` — table named, no columns, exactly the precedent
CODE-4 cites), `:610` (`TestProdMigrationsRollBackPerStep`).

WATCHOUT: `migrations/0164_coordination_lease.up.sql:40-43` is a SQL comment on the mirror column that
reads "the adapter rejects a barrier whose generation does not match its last fenced value", which D7
narrows. It is a landed migration and is not an edit site — landed migration files are not rewritten —
so do not file it. CODE-4's 0181 changes that column's DEFAULT without touching 0164's text, which is
correct. — EVIDENCE: `migrations/0164_coordination_lease.up.sql:40-44`

FACT: the whole proposal is byte-identical to the round-4 snapshot; only the review log changed
(compaction pass 13). `diff -u` over each of the seven non-log files under
`scratchpad/cp-snap/0076-run3/non-spec-r5` against the proposal folder is empty. A round-5 reviewer
told to "read the changed sections first and hardest" has no changed proposal sections to read.


### [non-spec.5.review-mechanism.1]

FACT: `s.barrier` (the pod-wide `barrierGate`) has exactly five readers in the tree, and two of them sit
outside `pkg/adapter/coordination.go`: `pkg/adapter/checkpoint.go:122` (`s.barrier.link`) and `:124`
(`defer s.barrier.complete()`). The other three are `coordination.go:64-66` (`BarrierWaiting`), `:264`
(`open`), `:269` (`release`). `s.coord` is confined to `coordination.go`. So CODE-1's barrier-gate move
breaks exactly one file CODE-1 does not name — EVIDENCE: `grep -rn "s\.barrier\|s\.coord" pkg/adapter/*.go`;
pkg/adapter/checkpoint.go:122,:124; pkg/adapter/coordination.go:64,:264,:269

FACT: two landed memstore cases create a session row with `CoordinationGeneration` unset and then assert a
count of bumps from it, so CODE-4's `Create` floor shifts them by one, and §8's class-1 enumeration names
neither: `TestUpdateAdvancesGenerationCounters` (declared :413, create :416, asserts 2 at :430) and
`TestUpdateConcurrentGenerationBumpsPreserveMonotonicity` (declared :468, create :471, asserts 50 at :490)
— EVIDENCE: pkg/gateway/session/sessionstore/memstore/memstore_test.go:413,:416,:430,:468,:471,:490

FACT: the rest of the seeded-generation test surface is clean, checked by
`grep -rn "CoordinationGeneration" --include=*_test.go pkg/ tests/` filtered to assertions. Every other
site either seeds explicitly at or above 1 (`sessionserver/failure_test.go:360,:394`,
`sessionserver/coordination_fence_test.go`, `coordination_mirror_test.go:84`, `wiring_test.go`,
`coordlease_test.go`), or reads a different table (`sessioncheckpointmeta`, `evictionstatestore`,
`partialmanifeststore`), or is a pure wire test (`tests/tier3_contract/adapter_generation_fence`,
`tests/tier3_contract/adapter_checkpointbarrier` — the latter asserts only the response wire contract and
is untouched by D7). `tests/tier2_component/stores/sessionstore_test.go` carries no
`CoordinationGeneration` assertion at all.

FACT: the §8 tier-8 and tier-7a dispositions are numerically correct. tier8
`coordination_crash_takeover_test.go`: assertions at :267,:283,:296 are 1,1,2 and the subtest seeding
unset is the third (:239-241); the two `LastFenced` subtests seed at :118 and :179 explicitly at 1.
tier7a `coordination_colocation_race_test.go`: seed 0 at :144, assert 0 at :287-288, other session at
:130, assert 2 at :264-265, `LastFenced` at :260. All verified line by line.

WATCHOUT: §8's "disjoint lines" arguments enumerate only the `pod.LastFenced` reads, but CODE-1 also
rescopes `Pod.Fence` and `Pod.StaleRPCRejected`, which appear at tier8 :130,:165,:184 and tier7a :169 and
tier4 :83,:151. The disjointness conclusion still holds (all sit in the explicitly-seeded subtests), so
this is an incomplete enumeration rather than a wrong conclusion. Do not file it; the conclusion it
supports is true.

FACT: `pkg/gateway/runtime/adapterclient/coordinatorfence_test.go:15-16` spells the exemption as "the first
CoordinatorFence on a pod's lifetime" and the file is absent from §9 entirely, contrary to the ledger's
claim that the three pod-lifetime citations are "covered only by their files appearing in §9". Neither of
its two cases breaks (both drive one session), so this is a stale comment. Not filed, because round 6's
refutation of the `barrier.go` `Target.CoordinationGeneration` comment finding is directly on point.

FACT: `scripts/lint-migrations.sh` pass 3 greps the migration number as a plain substring in ANY `_test.go`
under `tests/tier2_component/migrations/`, and `prod_columns_test.go` lives there. Since CODE-4 also stages
the `{migration: "0181", ...}` row in that file, the row alone satisfies pass 3, so §8's justification "a
case landing in `tests/tier2_component/stores/` alone leaves tier 0 red" is not exact. The directory choice
it supports is still right (`TestProdMigrationsRollBackPerStep` needs the row and the behaviour file is the
convention). Wording, not a finding — EVIDENCE: scripts/lint-migrations.sh:74-88;
tests/tier2_component/migrations/prod_columns_test.go:583

FACT: `TestProdMigrationsRollBackPerStep` walks `prodMigrationSchema` highest-first calling
`MigrateTo(n-1)` and never re-applies a migration, so 0181's down/up round-trip is never exercised and a
`DROP CONSTRAINT` naming a constraint the down migration recreates under a different name cannot bite —
EVIDENCE: tests/tier2_component/migrations/prod_columns_test.go:610-634

FACT: `pgstore.Update` reads the row `FOR UPDATE`, captures `prevCoordGen`, and clamps the mutate callback
to a monotonic floor, so no UPDATE path can write a value below the row's current one. The tightened
`CHECK (coordination_generation >= 1)` therefore has exactly one write path to worry about, `Create`, which
is what CODE-4 floors — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:411,:421,:463-470

FACT: no production code compares `CoordinationGeneration` against a literal 0 as a sentinel, and no
`spec/`, `docs/`, `schemas/`, or `charts/` sentence states the counter's initial value, so SPEC-3/CODE-4's
baseline reaches no surface outside the ones §9 already lists. `docs/getting-started/concepts.md:101` is the
only docs mention and states no baseline.

DECISION: filed three findings, all fixable in the non-spec staging — BECAUSE the loop asked for findings
whose fix lands there: (1) CODE-1 moves `barrierGate` in S3 while `checkpoint.go:122`/`:124`, the gate's
only other reader, is staged under CODE-2 in S5, so S3's tree does not compile and its declared tier 0
cannot pass; (2) CODE-2 exempts that link site from CODE-1's own hold-the-pointer rule on a temporal
fallacy, and the window spans the blocking `s.ops.Begin`; (3) §8's class-1 inventory and its
"rather than leaving tier 1 red" claim miss two memstore cases the floor falsifies —
ALTERNATIVES: rejected the tier-2-for-registry-state tier-claim mismatch (harmless extra tier run),
the incomplete `Pod.Fence`/`StaleRPCRejected` enumerations (conclusion survives), the stale
`coordinatorfence_test.go` comment (refuted class), and the lint-migrations justification (wording).

OPEN [mechanism, for a later round or the human]: `coordinationState` embeds its own `mu` as its first
field (`pkg/adapter/coordination.go:26`), so CODE-1's "`coordinationState` ... moves onto the slot registry
entry" moves the mutex too and settles §7's third open decision by construction. §4's fourth bullet still
frames "whether the pod-wide `coord.mu` becomes per-entry or stays one lock" as open. Not filed, because §7
is explicitly reserved for the human reviewer, but a fixer touching CODE-1 should say which of the two it
means.


### [non-spec.5.review-operational.1]

DECISION: returned an empty findings list for the operational-consistency lens — BECAUSE every observability
surface the change touches is either staged or provably untouched (details below) — ALTERNATIVES: rejected
filing (a) the stale `tests/claim-map.json` `CheckpointBarrierRequest.coordination_generation` row, (b) the
drifted code-line citations in two claim-register `surface` fields, and (c) §8's tier list omitting 11 while
checklist S1 declares it; each is pre-existing, gate-invisible, or already recorded as marginal in the
refuted list.

FACT: the whole metrics/alerts/runbook surface for this change is two spec sentences and nothing else.
`coordinator_connection_lost` occurs in `spec/` at exactly `spec/10_gateway-internals.md:60` (the §10.1.4
Observability bullet) and `spec/29_communication-scenarios.md:1274` (§29.8 step 2). SPEC-1 stages the first
(spec-changes.md:272-288) and SPEC-2 the second (spec-changes.md:459-461). `pkg/alerting/rules`,
`charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, `docs/runbooks/`,
`docs/reference/metrics.md`, and `tests/tier11_docs/` carry no reference to it, to
`coordinator_generation_gap`, or to `last_generation`. — EVIDENCE: spec/10_gateway-internals.md:60;
spec/29_communication-scenarios.md:1274

FACT: the per-session gap event already carries a session id, so making gap detection per session needs no
log-field change. `slog.WarnContext(ctx, "coordinator_generation_gap", "session_id", sessionID,
"last_fenced_generation", ..., "new_generation", ...)`. A finding that an operator could not attribute a gap
to a session on a co-tenant pod is dead. — EVIDENCE: pkg/adapter/coordination.go:112-116

FACT: the positive arms of the two predicates D6 re-scopes are already pinned by landed single-session
cases, so §8 listing only the negative (no-gap, no-stale) co-tenant arms is not a coverage hole.
`TestCoordinatorFenceGapDetected` and `TestCoordinatorFenceStaleGenerationRejected` still exercise a bound
session's own entry after CODE-1 and go red if `initialized` is moved onto the entry but never set. —
EVIDENCE: pkg/adapter/coordination_test.go:103, :135

FACT: the §28.4 claim-register gate is schema-only for every row this change touches, and it explicitly
declines to resolve line numbers. `tests/tier0_static/claim_register_test.go` checks well-formedness, the
closed status set, that a `WIRED` row names "a file path or a symbol rather than a bare line number", the
deferral identifier, and anchor resolution against §28 headings; only the three credential rows get a
reachability gate, "Rows outside the credential set carry the schema rules alone". So the proposal's live
claim at spec-changes.md:430-432 ("No §28.4 claim-register row moves ... that file is not opened") survives
as a statement about the gates, even though two rows carry code-line citations into files this proposal
rewrites (`In-flight RPC cancellation on a generation gap` → `pkg/adapter/coordination.go:81-82`;
`CoordinatorFence` → `coordfence.go:160`, `coordination.go:85`) and CODE-1/CODE-4 shift both. —
EVIDENCE: tests/tier0_static/claim_register_test.go:29-46; tests/claim-map.json:174-178, :461-465

UNVERIFIED: `tests/claim-map.json`'s `CheckpointBarrierRequest.coordination_generation generation fence
field` row is `UNWIRED` with note "no production reader compares it until the generation fence lands", while
`pkg/adapter/coordination.go:236-239` compares exactly that field today. The row looks wrong before this
proposal and stays wrong after CODE-2, and `claim_register_proto_agreement_test.go` does not catch it
(`fenceReadersExempt` holds `CoordinatorFenceRequest` alone, and the coverage half only requires that a row
exist). I judged it pre-existing drift outside 0076's scope; a claim-register or fidelity loop should decide
whether the row moves to WIRED. — EVIDENCE: tests/claim-map.json:75-82;
tests/tier0_static/claim_register_proto_agreement_test.go:37-42

WATCHOUT: the snapshot at `scratchpad/cp-snap/0076-run3/non-spec-r5` differs from the live proposal folder in
the review log ALONE (compaction pass 13). `diff -rq` the two before spending time on "what changed since
last round": the four substantive files are byte-identical, so this round's reading order collapses to the
whole document. — EVIDENCE: `diff -rq scratchpad/cp-snap/0076-run3/non-spec-r5 proposals/0076_.../`

USEFUL [standing context, "The `docs/`, `charts/`, `sdks/`, and §16 surface has been re-derived seven
times"]: it is correct and I re-derived it independently (greps over `docs/`, `charts/`, `pkg/alerting`,
`spec/16`, `tests/tier11_docs`). The one thing it understates: no tier-11 or tier-0 gate string-matches any
sentence SPEC-1 or SPEC-2 rewrites, which I checked directly, so checklist S1's tier 11 is precautionary
rather than load-bearing. A future operational or docs lens can stop at that sentence.


### [non-spec.5.review-performance.1]

FACT: migrations in this tree run as a Helm `pre-install,pre-upgrade` hook at weight -5, i.e. while
100% of the OLD gateway fleet is still serving; the gateway Deployment is a normal resource applied
after all pre-* hooks. So "the migration and the code land in one commit" says nothing about deploy
order — the schema is always ahead of the binaries for the whole rolling window.
— EVIDENCE: charts/lenny/templates/migrate-job.yaml:10-16,:38-39; spec/10_gateway-internals.md:420,:425

FACT: spec §10.5 carries an explicit expand-contract operational rule for exactly this: a constraint that
old-version replicas' writes violate may only be added after every replica runs the new code, and each
phase is a separate migration file AND a separate deployment. The tree already has phase tracking
(`schema_migration_phase`, phase1_applied / phase3_applied).
— EVIDENCE: spec/10_gateway-internals.md:429-433; pkg/schemamigrate/phasestore_pg_test.go:48,:101

FACT: `sessions` has exactly one production INSERT path and it names `coordination_generation` in its
column list, bound from the struct with no floor; no create path in `pkg/gateway/sessionserver` sets the
field, so every old-binary insert writes a literal 0. `Update` is read-modify-write with a monotonic
floor, so only `Create` is exposed.
— EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:170,:177,:260,:475-477

FACT: the adapter's pod-level op lock serializes Checkpoint across a pod's slots — at most one runs, a
second co-tenant checkpoint blocks in `wait` until the first releases — and both spec/04 §4.7.2 and
spec/05 §5.2 state that serialization normatively ("one slot's checkpoint upload at a time"). A per-session
`barrierGate` therefore removes the gate cross-link but does NOT make two co-tenant barrier acks
independent: barrier B's ack is delayed by stream A's whole archive.
— EVIDENCE: pkg/adapter/oplock.go:43-51,:119-129; pkg/adapter/checkpoint.go:111,:122;
spec/04_system-components.md:730; spec/05_runtime-registry-and-pool-model.md:546

FACT (kills a tempting finding): the drain does NOT get slower or busier under D7. `dispatchOne` starts
`CheckpointWithTrigger` in a goroutine BEFORE `dispatch.Send` and joins it after, so the checkpoint work
and the ctx bound are identical whether the barrier is accepted or refused. Do not file "D7 makes the
drain hold N barriers open" as a new load; the load was always there and D7 only removes the SECOND
capture in the prestop eviction loop.
— EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:217-227,:169-179

FACT: `checkpointBarrierAckTimeoutSeconds` defaults to 90s and the CRD webhook validates it only against
ONE slot's `max_tiered_checkpoint_cap`, not against `maxConcurrentSessions × cap`, while §5.2 sizes the
agent pod's own preStop budget as the SUM across slots. The asymmetry is shipped and pre-existing; it is
not created by 0076, but it is why "both co-tenant barriers ack" cannot be assumed at high
`maxConcurrentSessions`.
— EVIDENCE: spec/10_gateway-internals.md:136,:138,:140; spec/05_runtime-registry-and-pool-model.md:546

DECISION: did not file the migration's lock/backfill cost (full-table `UPDATE sessions` plus a validating
`ADD CONSTRAINT ... CHECK` under ACCESS EXCLUSIVE, against a Tier-3 `sessions` table of ~17M rows at
200 new sessions/s) — BECAUSE the tree has precedent for both: 0180 rewrites `sessions.workspace_root`
across the table, and 0066/0063 drop-and-re-add a CHECK on `artifact_store` with no NOT VALID/VALIDATE
split. No migration anywhere in `migrations/` uses `NOT VALID`, so requiring it here is a new convention
rather than a defect — ALTERNATIVES: filing it as a top-tier bottleneck; rejected as hardening against
an established repo convention, and it would likely be refuted on precedent.

DECISION: did not file the un-migrated-row fence path — BECAUSE it is bounded by the new CHECK and the
proposal names it. Worth recording anyway: the proposal's "Both refusals are loud and fail closed"
(non-spec-changes.md:118-121) is imprecise for the FENCE half. An `InvalidArgument` from
`CoordinatorFence` falls into `coordfence.fence`'s `default:` transient arm, burns the whole attempt
budget, and then RELINQUISHES the coordination lease and aborts the resume — not a clean refusal.
The barrier half does return immediately. — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence.go:159-188

USEFUL [Standing context, "A refused barrier costs a duplicate capture rather than a lost checkpoint"]:
saved me from re-deriving the whole prestop/`ErrGenerationStale` cost model.

WATCHOUT: `diff -ru` of the r5 snapshot against the proposal folder shows ONLY the review log changed
(compaction pass 13). The spec-changes, non-spec-changes, checklist, summary, and status files are
byte-identical to round 4's snapshot, so "read the changed sections hardest" gives no signal this round —
read the whole document.

OPEN: nobody on this proposal has looked at deploy-time ordering at all. Grepping all proposal files for
`expand-contract`, `rolling deploy`, `mixed-version`, `pre-upgrade`, and `10.5` returns nothing. Any
future proposal in this repo that adds or tightens a constraint on a hot table owes the same check.


### [non-spec.5.review-reliability.1]

DECISION: returned an empty findings list — BECAUSE every reliability candidate I raised resolved into
pre-existing behaviour the proposal does not create, a recorded OPEN, or a refuted class whose scope
genuinely covers it — ALTERNATIVES: the four candidates below, each killed on stated evidence.

WATCHOUT: the proposal is byte-identical to the round-4 snapshot; only `review-log.md` moved (compaction
pass 13). `diff -ru scratchpad/cp-snap/0076-run3/non-spec-r5 proposals/0076_*/ --exclude='*review-log*'`
is empty. Two other round-5 lenses recorded the same. Budget the whole pass on a cold read.

FACT [the strongest candidate I found, and why it is not a finding]: D7 removes the prestop fallback
capture for the whole ordinary never-handed-off population, and that fallback was also the only retry for
a barrier-window checkpoint that failed. `dispatchOne` sets `out.CheckpointErr = cpErr` and then
`out.Acked = true` whenever `dispatch.Send` succeeded, so a failed `CheckpointWithTrigger` does NOT un-ack
the target; `fireBarrier` keys `acked[SessionID]` on `o.Acked` alone and ignores `CheckpointErr`; the
post-barrier loop then `continue`s past that session and counts it as `BarrierCheckpointedSessions`.
Before 0076 every never-handed-off session's barrier was refused, so all of them fell through to the
per-session capture loop; after D7 none do. Killed on three grounds: the acked-but-uncaptured gap is a
pre-existing property of `fireBarrier`/`dispatchOne` that 0076 neither introduces nor touches; the skip is
what shipped §10.1.8 mandates ("a session the barrier already checkpointed under quiesce-and-hold must not
be checkpointed a second time by this loop"), so filing it argues against shipped spec text rather than
against this proposal; and the ack-timeout arm degrades correctly (a deadline on `Send` is not
`ErrGenerationStale`, so `Acked` stays false and the fallback still runs) — EVIDENCE:
pkg/gateway/coordination/barrier/barrier.go:216-232, :243-244; pkg/gateway/podlifecycle/prestop/prestop.go:388-396, :509-514

FACT: the pod-level op lock serialises the gateway-driven `Checkpoint` streams per pod, so §8's tier-7a
phrase "neither waits on the other" is literally false even after CODE-1: barrier B's ack cannot return
before stream A's upload finishes. `Server.Checkpoint` calls `s.ops.Begin(ctx, opCheckpoint, sessionID)`
and `opLock` is documented as serialising Checkpoint and Interrupt "across the sessions the pod holds: at
most one runs at a time", admitting a distinct session id to a pending set that waits rather than being
refused. Not filed: the operative contrast §8 draws is against the pod-wide gate's 90s block ("Against a
pod-wide gate the loser blocks to the shared ack deadline"), and the assertions that carry the fix (each
ack echoes its own stream's id; both complete promptly) hold. The standing context already records the
queue-rather-than-refuse fact, so a finding here would be re-derivation plus wording — EVIDENCE:
pkg/adapter/oplock.go:42-60, :88-92; pkg/adapter/checkpoint.go:111, :122

FACT: migration 0181 needs no `coordination_lease` backfill and self-heals within one sweep. `upsertMirror`
runs unconditionally for every eligible held row at the end of the sweep loop, not only on the takeover
branch, and it is the only writer of that column besides `Upsert`'s explicit bind, so post-migration mirror
rows carrying 0 are corrected on the next sweep. The window's behaviour is identical to today's (barrier
carries 0, adapter refuses with InvalidArgument), so it is not a regression — EVIDENCE:
pkg/gateway/coordination/coordination/coordination.go:430, :544; pkg/gateway/coordination/coordlease/pgstore/pgstore.go:47-59

FACT: the `>= 1` CHECK and the DROP-by-auto-name are both sound against the tree. 0050 declares the check
inline on the column, so Postgres names it `sessions_coordination_generation_check` exactly as CODE-4
claims; the repo already has three precedents for drop-and-re-add of a CHECK with a backfill (0103, 0063,
0167), so 0181's pattern is not novel and no `NOT VALID`/`CONCURRENTLY` convention exists to violate
(only 0156 uses either) — EVIDENCE: migrations/0050_session_record_fields.up.sql:38-39;
migrations/0103_tenant_workspace_tier_check.up.sql:12; migrations/0167_runtime_definitions_execution_mode_service.up.sql:41

FACT: the crash-takeover repair CODE-4 claims is real and I re-derived both arms. Pre-fix: row 0, resume
fences the pod at the coordfence floor's 1, takeover CAS 0->1 fences at 1, the gate `gen <= lastFenced`
rejects, coordfence re-reads 1, `newGen > gen` is false, so it relinquishes and the sweeper records an
adoption backoff — one lost sweep cycle plus a stale/relinquish metric pair. Post-fix: row 1, resume fences
at 1, CAS 1->2 fences at 2, accepted with no gap (2 == 1+1). No new rejection is created anywhere: a second
resume fence at the same value is rejected identically before and after, because the floor already produced
the same number — EVIDENCE: pkg/adapter/coordination.go:99, :108; pkg/gateway/coordination/coordfence/coordfence.go:143-153, :170-180

FACT: `RecordHandoff`'s 0-return failure sentinel survives the baseline and its rationale comment goes
stale in letter. The comment reasons about "fencing the pod at the baseline generation 0"; after CODE-4 the
baseline is 1 and 0 is impossible as a row value, so the sentinel is strictly safer, but the sentence names
the wrong baseline. `pkg/gateway/coordination/coordination/coordination.go` is absent from §9. Judged the
same class as the already-refuted `barrier.go` doc-comment finding (stale code comment, nothing reads it,
no behaviour change), so not filed — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:371-381, :454-461

FACT: TEST-1's file list naming `pkg/adapter/holdstate_test.go` and `pkg/adapter/coordination_test.go` does
NOT contradict §8's step assignments. §8 names the owning step for each landed-case amendment explicitly
("the disposition CODE-3 needs so that the step landing CODE-3 does not turn tier 1 red"; "the step landing
CODE-2 amends it in the same commit"), so TEST-1's list reads as the files the cases touch rather than as a
claim that S6 owns them. I spent time on this as an ordering finding; it is dead — EVIDENCE:
non-spec-changes.md:127-131, :174-181, :246-248

USEFUL [standing context, "A refused barrier costs a duplicate capture rather than a lost checkpoint"]: it
is what let me get to the sharp version of the prestop question fast (the cost is the acked record and the
double capture, not the checkpoint), and it named the two traps (`quiesced_ms` never persisted; the stale
counter only on the fence path). Keep it.

USEFUL [non-spec.2.review-reliability.1]: its `>= 1`-CHECK-is-safe-on-every-writer bullet (both stores'
`Update` clamp to the previous value, every raw-SQL `INSERT INTO sessions` omits the column) saved me the
whole writer enumeration. Promote it rather than aging it out.


### [non-spec.5.review-security.1]

DECISION: returned an empty findings list for the security lens, the second empty return this lens has
produced on 0076 — BECAUSE every candidate resolved into an already-recorded OPEN, a refuted class whose
scope genuinely covers it, or a change that tightens rather than relaxes a control — ALTERNATIVES:
(1) filing D6's per-session first-fence exemption as widening the pod-wide hold's exit, since a fence for a
co-tenant session that the pod-wide gate rejects today is accepted under D6 and reaches `exitHoldState()`
(`pkg/adapter/coordination.go:130`). Killed on three grounds: the behavior already exists today whenever the
co-tenant's generation happens to sit above the pod's, so D6 changes its frequency rather than adding a
capability; a fence issues only after a successful Postgres CAS, so the sender is the legitimate coordinator
of the session it names (refuted class (b)); and SPEC-2 stages the consequence explicitly in §29.10's
"Shared by the whole pod" hold bullet ("A successful fence for any one of those sessions exits the hold for
the pod"), so it is a stated spec change rather than a silent bypass. (2) filing tier 9 as a reached-but-
unlisted tier, since the change alters a fencing gate. Killed because tier 9's two adapter files carry no
generation, fence, or accessor reference at all, so nothing there compiles against the changed surface or
asserts a shifted value.

FACT: the whole of `tests/tier9_security` is untouched by this change and correctly absent from every tier
list. `adapter_hold_termination_surface_test.go` drives the real `adapter.Server` through a coordinator-
stream drop and hold termination, but it never fences and never reads a generation, so CODE-1's accessor
signature change and CODE-3's `holdState.gen` deletion do not reach it; `concurrent_slot_isolation_test.go`
likewise. A grep for `last_generation|LastGeneration|coordinator_lost|LastFenced|CoordinatorFence` across
those files returns nothing — EVIDENCE: tests/tier9_security/adapter_hold_termination_surface_test.go:1-60,
:124, :239

FACT: `coordfixture.Pod.StaleRPCRejected` is the split-brain probe and it sends a `CheckpointBarrier`, so it
is the one fixture method D7 could have turned into a hang. It cannot: both call sites probe a session the
pod has already been fenced for, so the barrier hits the surviving `gen != fenced` arm and returns
`FailedPrecondition` immediately rather than quiescing and blocking to the ack deadline. A future co-tenant
case that probes an unfenced session would block instead of returning false — EVIDENCE:
tests/testinfra/coordfixture/coordfixture.go:118-127; tests/tier4_integration/coordination_fence_split_brain_test.go:151;
tests/tier8_chaos/coordination_crash_takeover_test.go:165

FACT: the surviving fail-closed arm of the barrier gate stays pinned after D7 without any staged
disposition, because `TestCheckpointBarrierRejectsGenerationMismatch` fences at 4 and then barriers at 3, so
it exercises `gen != fenced` rather than `!initialized`. Only `TestCheckpointBarrierRejectsWithoutFence`
sits on the retired arm, and §8 already amends that one. Do not file "the remaining refusal is untested" —
EVIDENCE: pkg/adapter/coordination_test.go:199-216 against :185-197

FACT: the session row's counter cannot be walked backwards by any caller, so CODE-4's baseline is durable
once written. Both stores' `Update` re-read the row under lock and restore the previous value when the
mutate callback lowers `CoordinationGeneration` — EVIDENCE:
pkg/gateway/session/sessionstore/pgstore/pgstore.go:460, :475-477;
pkg/gateway/session/sessionstore/memstore/memstore.go:129, :144-145. There is also exactly one production
`INSERT INTO sessions` in the tree (pgstore.go:170), so `Create`'s floor is the only write path the `>= 1`
check has to agree with.

FACT: §8's tier-8 baseline arithmetic is correct as written and I re-derived it. The two subtests holding
`pod.LastFenced` reads seed `CoordinationGeneration: 1` explicitly (`:118`, `:179`) and the third, which
holds the three shifting assertions, seeds the field unset (`:239-241`), so the S3/S4 split really does sit
on disjoint lines — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118, :179, :239-241,
:267, :283, :296. The tier-4 split-brain test's `!= 1` / `!= 2` assertions also take no shift, its session
being seeded at 1 explicitly — EVIDENCE: tests/tier4_integration/coordination_fence_split_brain_test.go:72,
:109, :144.

WATCHOUT: `diff -ru` against the round-4 snapshot shows the ONLY file that changed since round 4 is the
review log (compaction pass 13). `spec-changes.md`, `non-spec-changes.md`, `implementation-checklist.md`,
and `summary.md` are byte-identical. A reviewer told to "read the changed sections first and hardest" will
find nothing changed and should budget the whole pass for a cold read instead — EVIDENCE:
`diff -u scratchpad/cp-snap/0076-run3/non-spec-r5/*.spec-changes.md proposals/.../*.spec-changes.md` is empty

USEFUL [standing context, "Refuted classes ... Read a refutation's own scope first"]: class (b) is what
killed my hold-exit candidate, and only its full text ("a fence issues only after a successful
compare-and-swap") does so; the one-line summary of (b) as "the exemption-unit argument" does not. Keep the
body of each refuted class rather than compressing it to its title.

USEFUL [non-spec.2.review-security.1]: its trace of `CoordinatorFenceResponse.last_fenced_generation` as a
pod self-report that reaches no gateway decision is the single most load-bearing fact for this lens, and it
saved me the whole trust-boundary re-derivation. Promote it rather than aging it out.


### [non-spec.5.review-test-coverage.1]

FACT: no test under `tests/tier4_integration/` references `CheckpointBarrier` at all — `grep -rn "CheckpointBarrier" tests/tier4_integration/` returns nothing, and the four tier-4 files matching "barrier" use the word for the `/finalize` preparation barrier. So §8's closing sentence "Tier 4 covers the same flow across the gateway, the session store, and the pod" (`non-spec-changes.md:248-249`) points at nothing that exists and stages nothing new — EVIDENCE: tests/tier4_integration/eager_claim_lifecycle_test.go:16; tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48 (the only contract-tier barrier file, and it is descriptor-only, so it is not a behavioral home either)

FACT: `pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go` and `coordinatorfence_test.go` are a tier-1 in-process gRPC harness over a real `adapter.Server` (`barrierServer`), and they already pin the barrier's surviving stale-refusal arm end to end (`TestCheckpointBarrierGenerationStale_spec_10_1`, `:174-184`) and the fence's first-fence/stale/gap arms. That is why pass 7's "at tier 3 the wire gate ... refuses a stale one" is not a coverage gap. `waitBarrierGateOpen` calls `srv.BarrierWaiting()` with no argument (`:159-167`), which is why §9 lists that file — EVIDENCE: pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:40-55,:159-167,:171-184

FACT: `TestCoordinatorFenceGapDetected` (`pkg/adapter/coordination_test.go:135-172`) already pins a genuine within-session gap (fence 3 then 7, asserts `gap_detected` and the `coordinator_generation_gap` log). The ledger OPEN asking §8 for "a genuine skip on a second fence still producing a gap" is therefore satisfied by a landed case, and D6's positive arm is pinned by §8's `sess-b` first-fence-at-9 bullet. Do not file the D6 negative arm as missing — EVIDENCE: pkg/adapter/coordination_test.go:135-172; non-spec-changes.md:166-168

MISTAKE (mine, caught before filing): I nearly filed "S3 declares tier 2 with no tier-2 case". `tests/tier2_component/slotrelease/revoke_double_teardown_test.go` drives the coordinator-loss hold to termination against a real `adapter.Server` over envtest (`:309-334`, `:363-406`), so CODE-1/CODE-3 genuinely reach tier 2 as a regression gate even though §8 stages no new tier-2 case for them. §8's "2 (the registry state, ...)" parenthetical is defensible — EVIDENCE: tests/tier2_component/slotrelease/revoke_double_teardown_test.go:363-406

MISTAKE (mine, caught before filing): I nearly filed §8's tier-2 resume-then-takeover case (`non-spec-changes.md:240-242`) as wrongly tiered because the flow spans the resume fence, the store CAS, and the pod gate. `coordfence` tests do construct a `Fencer` with fake gen readers (`pkg/gateway/coordination/coordfence/coordfence_test.go:164,:178`), and `tests/tier2_component/` already builds `adapter.Server` in three packages, so a tier-2 construction is not impossible and the finding does not clear the bar. The standing-context claim "no test constructs a `Fencer`" is wrong as written; it is true only outside the `coordfence` package — EVIDENCE: pkg/gateway/coordination/coordfence/coordfence_test.go:164,:178

WATCHOUT: the three D7 cases pass 7 enumerated (`spec-changes.md:848-852`) — the tier-1 co-tenancy case (A fenced at 6, B never fenced, accept B's barrier and refuse A's at 5), the tier-3 stale-refusal arm, and the tier-8 "crash takeover whose fence has not yet landed does not lose the draining replica's barrier" — are still absent from §8 and nothing withdraws them. I did not file them: the stale arm is covered by landed tests (above), the co-tenancy case is caught indirectly by §8's gap bullet if `initialized` is left pod-wide, and the tier-8 one is doubtful after pass 12 withdrew the provenance universal (a barrier inside that window is accepted on the equality arm, not on D7's unset arm). A later round that wants them must argue past those three reasons rather than re-citing pass 7 — EVIDENCE: spec-changes.md:848-852

OPEN: §8's tier-1 disposition for `TestCheckpointBarrierRejectsWithoutFence` is the only landed-test disposition in §8 that states no replacement assertion ("amends it in the same commit rather than leaving tier 1 red", `non-spec-changes.md:246-248`), where the memstore, holdstate, and uploaddriver dispositions all state what the amended case asserts. Pass 7 records the intended assertion (`spec-changes.md:841-843`), so filing it invites the "already handled in its own text" refutation this loop has used twice. A fixer should still copy that assertion into §8.



### Verify: "CODE-2 exempts the checkpoint link site from CODE-1's hold-the-pointer rule" (mechanism, run 3)

VERDICT: CONFIRMED.

(1) Proposal text is quoted correctly.
- non-spec-changes.md:71-73 reads verbatim: "`checkpointRootsForSession` / (`pkg/adapter/slot.go:153-166`)
  has already failed the stream with `FailedPrecondition` when the entry is / absent, so the link site never
  sees a missing entry."
- non-spec-changes.md:45-48 reads verbatim: "`CoordinatorFence` and `CheckpointBarrier` each resolve the
  entry once ... Each holds the resolved pointer for / the life of the call, including the deferred
  quiescence clear, because a session deregistered mid-barrier / leaves the pointer valid while a second
  lookup by session id returns nothing." CODE-1's rule names two handlers; `Checkpoint` is a third path and
  is covered only by CODE-2's sentence.

(2) Tree citations check out.
- pkg/adapter/slot.go:153-166: `checkpointRootsForSession(sessionID string) ([]workspace.NamedRoot, error)`
  copies `st.paths.Current`, `st.paths.Sessions`, `st.sessionID` out under `s.mu` and returns roots only.
  The `*slotState` pointer is dropped.
- pkg/adapter/checkpoint.go:94 guard, :111 `s.ops.Begin(ctx, opCheckpoint, sessionID)`, :122
  `linked := s.barrier.link(start.GetCheckpointId())`, :124 `defer s.barrier.complete()`.
- pkg/adapter/oplock.go:89-130: a checkpoint naming a session id NOT already pending is admitted to
  `l.checkpoints` and `wait()` blocks on its promote channel; `errOpCoalesced` fires only for a session id
  already pending and `errOpBusy` only around an interrupt. A second co-tenant checkpoint therefore QUEUES;
  `release()` (:182-200) promotes it only after the running upload finishes. The wait at :111 is bounded by
  the other session's whole upload or by ctx.
- pkg/adapter/session.go:237-239 (`Shutdown`) and pkg/adapter/slotsession.go:347-368
  (`deregisterStartedSessions`, reached from `onHoldTimeout` at holdstate.go:192) both take `s.mu` and call
  `deregisterSlotLocked`, which deletes the map key (slotsession.go:180) and returns the still-valid
  pointer. Neither coordinates with an open `Checkpoint` stream or the op lock.
- pkg/adapter/coordination.go:264-269: `done := s.barrier.open()` then `select { <-done; <-ctx.Done() }`,
  and :269 `checkpointRef := s.barrier.release()` returns "" when nothing linked (comment :262-263).

(3) The conclusion follows. CODE-1 moves `barrierGate` onto the entry, so the link site at :122 must obtain
the entry; `checkpointRootsForSession` handed it no pointer, so it re-looks up by session id. The guard's
earlier success is an ordering fact, not a persistence fact: `Shutdown` or the hold timeout can delete the
entry inside the :94-to-:122 window, which spans the blocking op-lock wait. A re-lookup then returns
nothing, the session's OWN in-flight stream fails to link into the gate its own waiting `CheckpointBarrier`
holds by detached pointer, and that RPC blocks to the ack deadline and returns an empty `checkpoint_ref` --
the acked-barrier loss and prestop double-capture D7 exists to remove (spec-changes.md:1397-1400).

Prior-round entries that do NOT cover this and are themselves wrong or narrower:
- review-log.md:1054 "FACT: CODE-2's ordering claim holds ... so moving the gate onto the per-session entry
  cannot make the link site see a missing entry" is the unsound inference itself. Ordering was verified;
  entry persistence across the window was not.
- review-log.md:1285-1291 (WATCHOUT) and :250-256 (Standing context) likewise establish only ordering.
- review-log.md:1021-1026 (UNVERIFIED, reliability) covers the different case where NO stream links, and
  dismisses it because the pod-wide gate's completion there was an accidental cross-link. That reasoning
  does not reach this case, where the correct session's own stream exists and is lost to the re-lookup.

Nothing in non-spec-changes.md, spec-changes.md, or the checklist extends the hold-the-pointer rule to the
`Checkpoint` handler (`grep -n "pointer\|lookup\|resolved"` over non-spec-changes.md returns :45-48 only).

Two immaterial overstatements in the finding, neither affecting the verdict:
- "The two sentences cannot both be right about the same registry" is rhetorical. They govern different call
  sites; the defect is that CODE-2's stated rationale is an unsound temporal inference, rather than a formal
  contradiction with CODE-1.
- The suggested fix's last clause is redundant: §9 already names `pkg/adapter/slot.go`
  (non-spec-changes.md:310), so a signature change there needs no new entry, only an amended reason.


### [non-spec.6.review-mechanism.1]

DECISION: Returned an empty findings list — BECAUSE every mechanism I traced end to end (fence -> per-entry
state -> barrier gate -> Checkpoint stream link/complete -> ack -> `dispatchOne` meta write; hold arm ->
`deregisterStartedSessions` -> `terminateHeldSession` per-session read; row baseline -> `Create` floors ->
migration 0181 -> `coordfence` unfloored send -> adapter guards) closes, and every citation in the newest
text resolves — ALTERNATIVES: I costed four candidates and rejected each as below the bar (listed below).

FACT: The round-6 diff against the r5 snapshot is confined to `non-spec-changes.md` (the `checkpointRootsForSession`
returns-the-entry rule, the mid-flight deregistration tier-1 case, CODE-2's shrink to the gate alone, `resume.go`
in §9). The r6 snapshot itself differs from the live tree only in `review-log.md`, so `diff -ru` against
`cp-snap/0076-run3/non-spec-r6` shows nothing about the proposal — diff against `non-spec-r5` instead.
EVIDENCE: /home/ec2-user/lenny/scratchpad/cp-snap/0076-run3/non-spec-r5/

FACT: Every citation in the new CODE-1 paragraphs verifies exactly: `pkg/adapter/checkpoint.go:94` (guard),
`:111` (`s.ops.Begin`), `:122` (link), `:124` (deferred complete); `pkg/adapter/oplock.go:119-129` (distinct
session id admitted and queued, same id coalesced); `pkg/adapter/slot.go:153-166`; `pkg/adapter/session.go:237-239`
and `:271`; `pkg/adapter/slotsession.go:347-361`; `pkg/adapter/holdstate.go:249`; `pkg/adapter/resume.go:178`;
`pkg/adapter/checkpoint_stream_test.go:417`/`:384`, `slot_test.go:24`/`:37`, `server_test.go:90` (all in
`package adapter_test`); `cmd/lenny-test/cmd_run.go:880`, `:498-508`, `:635-641`.

FACT: `checkpointRootsForSession` has exactly two callers in the tree (`checkpoint.go:94`, `resume.go:178`), and
`Server.barrier` has exactly five production readers (`coordination.go:64-66`, `:264`, `:269`, `checkpoint.go:122`,
`:124`) plus four in `coordination_test.go` (`:224-226`, `:282`, `:285`, `:356-357`). Both files §9 needs for the
barrier move are listed. `BarrierWaiting()`'s only out-of-package caller is
`pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163`, and §9 lists that file.
EVIDENCE: pkg/adapter/coordination.go:44-67; pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:163

FACT (dead lead, do not re-derive): barrier ids collide across co-tenant sessions on one pod —
`nextBarrierID` is per-session monotonic (`pkg/gateway/coordination/barrier/barrier.go:265-272`), and the
`CheckpointBarrierAck` control event carries `barrier_id` with no session id
(`pkg/adapter/coordination.go:292-298`). It is NOT an ack-routing defect: no gateway code consumes the
control-stream mirror; `dispatchOne` uses the synchronous `dispatch.Send` return
(`pkg/gateway/coordination/barrier/barrier.go:226`, `:238-245`). CODE-1 makes concurrent co-tenant acks more
common but introduces no ambiguity.

FACT (dead lead): the tightened `CHECK (coordination_generation >= 1)` breaks no seed path. `pgstore.go:170`
is the only `INSERT INTO sessions` outside tests, and no raw-SQL fixture in the tree names
`coordination_generation` in its column list (checked with `grep -A3 "INSERT INTO sessions"` across all `.go`).

FACT (dead lead): a behavioral acceptance assertion at tier 3 is precedented — eight suites under
`tests/tier3_contract/` boot a live adapter over bufconn (e.g. `checkpoint_stream/checkpoint_stream_wire_test.go`,
`adapter_session_address/send_message_stamp_test.go`), so §8's tier-3 D7 case is writable at the tier it names.

MISTAKE (mine, corrected before filing): I nearly filed that the mid-flight deregistration case cannot assert
an accepted barrier's ack because an accepted `CheckpointBarrier` blocks. It can: the landed
`TestCheckpointBarrierAcksEchoedCheckpointID` (`pkg/adapter/coordination_test.go:243-300`) and
`TestCheckpointBarrierMapsAck_spec_10_1` (`pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go:112-154`)
both show the goroutine + wait-for-gate + drive-stream pattern. Any §8 bullet that says a barrier "is accepted"
is constructible that way.

OPEN (weighed and NOT filed, in descending strength, so a later round does not re-spend on them):
 (1) CODE-1 prescribes a resolve of the slot entry AFTER `checkSessionBound` on the fence and barrier paths
     (`pkg/adapter/coordination.go:89`, `:216`) — two lookups with `s.mu` released between — while the same
     paragraph establishes for the stream that "That guard does not make a second lookup safe". The proposal
     states nothing about what a fence/barrier resolve that misses does. I did not file it: the natural
     implementation (`if !ok { return FailedPrecondition }`) is fail-closed, the round already deleted the
     false "so the entry exists" exemption, and no stated claim becomes false. If a later round wants it
     closed, the symmetric fix is to have `checkSessionBound` return the `*slotState`.
 (2) §8's tier gloss "2 (the registry state, the migration, and the Postgres session store's floor)" names a
     subject its own next paragraph pins at tier 1 ("the lifetime of a resolved registry entry ... pinned at
     tier 1") and omits the tier-2 resume-then-takeover case that §8 does stage. Bookkeeping in a gloss; the
     staged tier-2 cases and S3/S4's tier-2 declarations are correct.
 (3) The tier-2 resume-then-takeover case names no file and §9 lists none for it; the IMPLEMENTOR'S CHOICE
     paragraph is scoped to tier-1 `pkg/adapter` cases only. Same class as the already-refuted tier-3 "no
     named home" finding.
 (4) §8's tier-8 and tier-7a S3/S4 split enumerates only the `pod.LastFenced` reads; `pod.Fence(ctx, 1)` at
     `tests/tier8_chaos/coordination_crash_takeover_test.go:130`, `:184`,
     `tests/tier7a_load_local/coordination_colocation_race_test.go:169`,
     `tests/tier4_integration/coordination_fence_split_brain_test.go:83`, and
     `pod.StaleRPCRejected(ctx, 1)` at tier8 `:165` and tier4 `:151` also take CODE-1's session key. The
     disjointness claim ("neither step turns tier 8 red") still holds, because all of those sites sit in the
     subtests seeded at 1 explicitly (`:118`, `:179`), so the omission changes no outcome.

USEFUL [standing context, refuted class (k)]: the "a path or comment under tests/ or pkg/ is not a missed edit
site" class saved me from filing `pkg/gateway/coordination/barrier/barrier.go:62-65`,
`migrations/0164_coordination_lease.up.sql:40-43` (whose column comment states the barrier match rule D7
narrows), and the three `tests/claim-map.json` `surface` rows carrying `pkg/adapter/coordination.go` line
numbers that CODE-1/CODE-2 shift.

USEFUL [standing context, "§10.1.8 step 3 already fixes the gate's unit at the session"]: verified verbatim at
spec/10_gateway-internals.md:185 ("opens the `Checkpoint` stream for each quiesced session **concurrently
with** the in-flight `CheckpointBarrier` RPC to that session"). It is the load-bearing justification for the
barrier-gate move and it holds.


### [non-spec.6.review-test-coverage.1]

DECISION: returned an EMPTY findings list — BECAUSE the deliverable text is byte-identical to what
`[non-spec.5.review-test-coverage.1]` reviewed (`diff -ru` of the round-6 snapshot against the live folder
changes only the review log; `non-spec-changes.md`, `implementation-checklist.md`, and `summary.md` are
unchanged), and my own independent sweep re-derived the same verdict rather than inheriting it —
ALTERNATIVES: I worked up and dropped four candidates, each recorded below so nobody re-spends on them.

FACT: every §8 and §9 citation I re-resolved is exact against the post-0073 tree. Verified this round:
`tests/tier8_chaos/coordination_crash_takeover_test.go` `pod.LastFenced()` at `:150`, `:195`, `:223`;
generation assertions 1/1/2 at `:267`, `:283`, `:296`; the unset seed at `:239-241`; the two explicit
`CoordinationGeneration: 1` seeds at `:118` and `:179`. The S3/S4 tier-8 split §8 claims therefore holds
literally: the three `LastFenced` reads live in the two subtests seeded at 1, and the three shifting
assertions live in the third subtest, which has no `LastFenced` read at all (grep for `LastFenced` in that
file returns `:150,:151,:195,:196,:223,:224` only). The third subtest's fence failure is forced by
`FenceReadopter{Fail: map[string]bool{sessID: true}}` at `:250`, not by a generation comparison, so the
baseline does not change its outcome — EVIDENCE: tests/tier8_chaos/coordination_crash_takeover_test.go:118,
:179, :239-241, :245-251, :262-297

FACT: `tests/tier7a_load_local/coordination_colocation_race_test.go` verifies as §8 describes: `:130` seeds
`CoordinationGeneration: 1`, `:144` seeds an explicit `0` through `memstore.Create`, `:260` is the
`pod.LastFenced()` read, `:264-265` asserts 2, `:287-288` asserts 0. The split across S3 and S4 is real —
EVIDENCE: tests/tier7a_load_local/coordination_colocation_race_test.go:130,:144,:260,:264,:287

FACT: the staged tier-1 barrier-gate case is writable in `package adapter` exactly as landed
`TestCheckpointBarrierAcksEchoedCheckpointID` is: it runs `CheckpointBarrier` on a goroutine, spins on
`waitBarrierWaiting`, then calls `s.barrier.link("gw-ckpt-1")` and `s.barrier.complete()` directly, so no
external `Checkpoint` stream is needed for the ack-echo assertion — EVIDENCE:
pkg/adapter/coordination_test.go:243-300, :221-233

CORRECTS [non-spec.5.review-test-coverage.1]: its opening FACT, "no test under `tests/tier4_integration/`
references `CheckpointBarrier` at all", is wrong and a later verification already refuted a finding built on
it. `tests/tier4_integration/coordination_fence_split_brain_test.go:151` calls
`pod.StaleRPCRejected(ctx, 1)`, and that helper's body is
`p.Client.CheckpointBarrier(ctx, p.SessionID, gen, "coordfixture-split-brain-probe")`. A grep for
`CheckpointBarrier` under `tests/tier4_integration/` misses it because the call sits behind the fixture —
EVIDENCE: tests/testinfra/coordfixture/coordfixture.go:117-125; tests/tier4_integration/coordination_fence_split_brain_test.go:151

USEFUL [non-spec.5.review-test-coverage.1]: its two "caught before filing" MISTAKE entries killed two of my
four candidates outright and saved the verification budget. Re-confirmed both: (1) tier 2 IS genuinely
reached by CODE-1/CODE-3 through `tests/tier2_component/slotrelease/revoke_double_teardown_test.go`, which
drops the CH-ADAPTEREVENTS stream and drives the hold to termination against a real `adapter.Server`, so
§8's "2 (the registry state, ...)" parenthetical and S3's tier 2 are defensible; and I add that the file
carries no generation, `coordinator_lost`, or `last_generation` assertion, so CODE-3 owes it no disposition
either. (2) the tier-2 resume-then-takeover case is constructible at tier 2 — EVIDENCE:
tests/tier2_component/slotrelease/revoke_double_teardown_test.go:306-334, :363-406

FACT: `grep -rn "coordinator_connection_lost\|last_generation\|lastGeneration" --include=*.go pkg/ tests/ cmd/`
returns only `pkg/adapter/holdstate.go:103,:130,:132,:156,:228,:290` and
`pkg/adapter/holdstate_test.go:708,:889`. No test outside `holdstate_test.go` asserts the pod-level line's
`last_generation` key, and `tests/tier8_chaos/` has no coordinator-loss hold coverage at all, so CODE-3's
"the hold carries no tier-8 case" and its closed edit set both hold on the current tree — EVIDENCE:
pkg/adapter/holdstate_test.go:708; tests/tier8_chaos/ (no `CoordinatorHoldTimeout` or `coordinator_lost` hit)

FACT: the tier-3 contract suites the change touches are descriptor- and encoding-only and are falsified by
nothing here. `generation_fence_wire_test.go` pins field number, kind, oneof placement, and the zero-value
encoding; `checkpointbarrier_wire_test.go` has one response-shape test. Neither asserts a gate outcome, so
D7 leaves them green and §8 owes them no disposition — EVIDENCE:
tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:104-263;
tests/tier3_contract/adapter_checkpointbarrier/checkpointbarrier_wire_test.go:48

MISTAKE (mine, caught before filing, candidate 3): I worked up "§8's tier enumeration omits tier 11 while
checklist S1 declares it" as a criterion-(f) omitted-tier finding. The omission is real (`non-spec-changes.md`
lists "0, 1, 2 ..., 3 ..., 4 ..., 7a ..., and 8" while `implementation-checklist.md:6` reads "Tiers 0, 11"),
and tier 11 is genuinely reached, because the §28.8 row gate lives in `tests/tier11_docs/`. I did not file
it: the gate S1 names still runs, no new tier-11 test is owed, and this loop has already refuted two
tier-list bookkeeping findings on exactly that reasoning (the S6-omits-tier-0 finding and the
S4-declares-tier-3 one). A later round wanting it must argue past both.

MISTAKE (mine, caught before filing, candidate 4): I worked up a `// diagnosis:` finding — §8 stages new
tier-2, tier-3, tier-4, and tier-7a cases and mentions `// diagnosis:` only to say the amended hold case
keeps its name so its existing one stays attached, while `.claude/rules/test-coverage.md` requires the
comment on every tier-2-and-higher test. Not filed: the rule is always-on and binds the implementor whether
or not §8 restates it, which is the redundancy category the bar excludes.

OPEN [carried from [non-spec.5.review-test-coverage.1], still unclosed]: §8's tier-1 disposition for
`TestCheckpointBarrierRejectsWithoutFence` is the only landed-test disposition in §8 that names no
replacement assertion ("the step landing CODE-2 amends it in the same commit rather than leaving tier 1
red", `non-spec-changes.md:290-292`), where the memstore, holdstate, and uploaddriver dispositions each
state what the amended case asserts. Pass 7 records the intended assertion at `spec-changes.md:841-843`. Two
test-coverage lenses have now declined to file it for the same reason (it invites the "already handled in
its own text" refutation). It is a fixer's job, not a reviewer's; whoever next writes §8 should copy the
assertion in and close this.

## Retired

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
