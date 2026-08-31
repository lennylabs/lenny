# Review log — 0076_fix_scope-the-coordination-generation-to-the-session

## Standing context

**Compaction pass 3, 2026-08-31.** Round 3 (`[spec.3.*]`) is untouched. Round 1 keeps only the OPEN and
UNVERIFIED items it still owns; its facts and decisions are the bullets below. Round 2's twenty-six entries
merge into four by subject, because twelve lens entries reported one finding (§10.1.2 step 3 left unstaged)
and each re-derived the same inventories. Honoured round 3's two CORRECTS, so every line staging the
never-fenced session's barrier as refused now reads as D7's acceptance and the two watch-outs guarding that
refusal are deleted. Three UNVERIFIED lines become facts. Promoted the five items marked USEFUL. Neither
target is met and neither can be. This section runs 95 lines against 80, and every bullet left is a promoted
USEFUL, a rule-2 correction, or a trap that has already cost a round. The ledger runs 975 lines against a
target of 948, because round 3 is 652 of them and frozen, which leaves rounds 1 and 2 the 323 lines that
carry sixteen live OPEN and UNVERIFIED items and the correction record.

- **The barrier is accepted for a bound session the pod holds no fenced generation for (D7).** Round 2 staged
  the opposite and round 3 reversed it: §10.1.2 step 2 already has the pod accept a coordinator's RPCs before
  that coordinator's fence acknowledges, and a refusal would have contradicted §10.1.8's interruption bound and
  §29.7's closed outcome set. CODE-2's gate becomes `initialized && gen != fenced` read from the slot entry.
  Anything in the ledger reading the unset case as a refusal predates D7.
- **`initialized` moves with `lastFenced` or the defect comes back.** The first-fence exemption is a separate
  `initialized` bool on `coordinationState` (`pkg/adapter/coordination.go:29-32`), read by the stale rejection
  (`:99`) and the gap test (`:108`) and set at one site (`:121`). Leaving it on `Server` while `lastFenced`
  moves to the slot entry flips it on the first fence anywhere on the pod, so every later co-tenant's first
  fence tests initialized-true against its own entry's zero and reports a gap. D6 is the specification half:
  the pod holds `last_fenced_generation` per bound session, unset until that session's first accepted fence
  there, and the gap predicate and the stale rejection apply only against a recorded value.
- **The §10.1.2 gap reset is mirrored in several places.** `spec/28` and `spec/29` restate clauses (a) through
  (d) and cite §10.1 as their source, and SPEC-2 stages both. `schemas/lenny-adapter.proto` carries the two
  pod-side rules across seven comments, all SCHEMA-1 sites. Record-and-reject: the `CoordinatorFence` RPC
  comment `:153-162` (which also spells the exemption per pod lifetime), message-level
  `CoordinatorFenceRequest` `:1442-1446`, `CoordinatorFenceResponse` `:1455-1462`, and the
  `CoordinatorFenceRequest.coordination_generation` field comment `:1449-1451`, which keeps its per-session
  monotonicity wording. Acceptance: the `CheckpointBarrier` RPC comment `:165-179`, `CheckpointBarrierRequest`
  `:1469-1474`, and its field comment `:1477-1479`. A message's doc comment sits above the `message` line, so
  an evidence range starting at `message X {` misses it, which is how three rounds missed `:1442-1446`. The
  twelve other `coordination_generation` field comments are session-scoped and neutral; they are not edit sites.
- **The adapter does not perform the §10.1.2 cancel-and-reset.** Its gap branch logs the event, reports
  `gap_detected`, and records the new value on the path a non-gap fence takes
  (`pkg/adapter/coordination.go:108-121`, omission at `:112-113`). Staged text may re-scope that requirement
  and may not assert that the adapter meets it.
- **The coordinator-loss hold has no per-session arm and cannot be given one here (D5).** The sole arm is the
  close of the pod's single CH-ADAPTEREVENTS stream, and that stream names no session
  (`pkg/adapter/adapterevents.go:80-107`; `pkg/adapter/holdstate.go:39-44`, `:90-100`). §10.1.4 fixes the same
  posture: the timeout "terminates every session the adapter has started on that pod" and total connection loss
  "is always a whole-pod failure" (`spec/10_gateway-internals.md:58`, `:62`). D5 keeps both sentences, drops
  the generation from the pod-level arming line, and has each terminated session's `coordinator_lost` line and
  post-mortem read its own entry, reporting `0` when no coordinator fenced it there.
- **Every anchor SPEC-1 and SPEC-2 quote resolves verbatim and uniquely, and no gate reads a sentence they
  rewrite.** Verified twice, so do not re-verify: `spec/10_gateway-internals.md:38`, `:40`, `:41`, `:60`;
  `spec/28:315-316`, `:333-336`, `:1675`, `:1807`; `spec/29:1274`, `:1307-1313`, `:1464-1470`, `:1523-1527`.
  The §28.8 matrix enforces one row per §28.3 identifier in both directions at tier 0, so editing a cell is
  safe and deleting or splitting the `CH-FENCE` row is not; the §29.10 successor-pointer gate needs one `CH-`
  link in the section body and the opening paragraph carries it (`matrix_completeness_test.go:16-20`, `:143`;
  `successor_pointer_test.go:52-76`).
- **No `tests/claim-map.json` row moves for this change.** `claim_register_test.go` is a schema gate rather
  than a coverage gate: it checks well-formedness, the status set, a `WIRED` row's surface, a non-`WIRED` row's
  deferral identifier, and anchor resolution, and derives no coverage from §28's sentences, so a genuinely new
  §28.5.1 sentence owes no `ABSENT` row (`:23-60`). The `CheckpointBarrierRequest.coordination_generation`
  row's `UNWIRED` status under deferral `R16` is already false against `pkg/adapter/coordination.go:236-239`,
  which is pre-existing test-lane drift.
- **The defect's premise is unreachable in the shipped tree.** A second replica's fence at a lower generation
  is rejected on the pod-wide gate, and once it fences higher the first replica's RPCs are rejected on the same
  gate, so multi-coordinator co-tenancy does not arise today (`pkg/adapter/coordination.go:97-103`).
- **The `docs/` and §16 surface is nearly empty.** The mentions are `docs/reference/adapter-contract.md:69`,
  `metrics.md:307` and `:309`, `glossary.md:54`, `docs/getting-started/concepts.md:101` and
  `architecture.md:173`, and `docs/operator-guide/upgrades.md:47-49`, none made wrong by a per-session
  generation. The gauge stays pod-scoped under D5, so `spec/16_observability.md:185` needs no edit and `:183`
  defines `lenny_coordinator_handoff_stale_total` unit-neutrally; §16.6's catalog lists no adapter-side
  structured log event, so `coordinator_connection_lost`, `coordinator_lost`, and `coordinator_generation_gap`
  are in no inventory; and the only coordinator alert, `CoordinatorHandoffSlow`, is untouched. Re-derived twice.
- **`CoordinatorFence` has two senders and nothing fences a normally-started session.** The resume path
  (`fenceResumedPod`, `pkg/gateway/sessionserver/start.go:4237`) and the sweeper's crash-takeover re-adopt
  (`cmd/lenny-gateway/coordination_seams.go:233`) drive the same `coordfence.Fencer`, and `grep -rn "\.Fence("
  pkg/ cmd/` outside tests returns those two alone. Both reach a bound pod, so §4's first blank is about a
  released or racing entry rather than a never-bound one, and D6's unset case is the ordinary case.
  `checkSessionBound` runs before the generation gate on both handlers, so acceptance-when-unset never reaches
  an unbound session.
- **§10.1.2 step 3 fixes equality as the operational-RPC gate.** "The pod accepts only RPCs whose generation
  matches the fenced value", so the §4 alternative of loosening `CheckpointBarrier` to "at least the last
  fenced generation" is barred by spec text (`spec/10_gateway-internals.md:41`). SPEC-1 stages that sentence,
  the third place §10.1.2 states the pod-side gate, changing only the unit of the compared value and the unset
  case; §7's first open decision still owns the operator. Cite step 2 (`:38`) for the bar on sending before a
  fence acknowledges and step 3 for the acceptance predicate.
- **Round 2 closed four round-1 lens UNVERIFIEDs; do not re-open them.** The Sweeper re-acquires coordination
  for a session whose coordinating replica died while the pod is Running, adopting the lapsed lease, bumping
  the generation, and driving `ReadoptAndFence` (`pkg/gateway/coordination/coordination/coordination.go:72-104`,
  `:437-480`), so D5's pod-wide hold exit does not abandon co-tenants. A live session's slot entry is never
  deleted and re-created on the same pod, so per-entry storage does not reset the fence floor mid-session
  (`pkg/adapter/slotsession.go:174-196`). `lenny_coordinator_handoff_stale_total` increments on every
  `FailedPrecondition` fence rejection (`coordfence.go:170`), and the gateway issues one `CheckpointBarrier`
  per session, sourced as `SELECT session_id FROM coordination_lease`.
- **§7's remaining decisions are deliberately open for the human reviewer** (whether the barrier gate stays
  equality, whether a fence for an unheld session is a rejection or a retryable race, and whether `coord.mu`
  becomes per-entry). D7 removed the fourth, the unset case. Cite them by text: D5 and D7 renumbered them.
- **`cat -n *spec-changes.md` globs two files.** It matches `.non-spec-changes.md` and `.spec-changes.md` and
  numbers them continuously, so a line number taken that way is off by 46. Name the file in full.

## Ledger

### [spec.1.*] · residue of round 1 · the obligations it still owns

Round 1's facts, watch-outs, and decisions (D5, D6, SPEC-2, the mirror inventories) are in `## Standing
context`. What is left here is work it recorded that nothing has done. `[spec.1.followup-fix.1]`'s writable
set was the spec-changes, problem-statement, summary, and review-log files, so four of its five deliverable
corrections still have to be written by a loop that may edit the non-spec-changes, implementation-checklist,
and status files.

- OPEN: the files-touched list in the non-spec changes carries `spec/10_gateway-internals.md` as its only spec
  entry. It gains `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, under round 3
  also `pkg/adapter/coordination_test.go`, and names §10.1.2, §10.1.4, and §10.1.8 under `spec/10`. Step S1
  becomes "SPEC-1 and SPEC-2. §10.1 states the generation's scope on the pod side and what a hold covers, and
  §28 and §29 state the same scope. Tiers 0, 11. Depends on: —", with no new step.
- OPEN: CODE-3 in the non-spec changes reads "`pkg/adapter/holdstate.go`, per §7." and defers to a decision D5
  replaced. It becomes: `pkg/adapter/holdstate.go`. Under D5 the hold stays pod-scoped and its arming signal,
  its rejection set, and its termination set are unchanged. `holdState.gen` is dropped and
  `LastFencedGeneration()` with it, the pod-level `coordinator_connection_lost` line carries the
  started-session count and no generation, and `terminateHeldSession` and `writeHoldPostMortem` read each
  terminated session's own last fenced generation from its registry entry. Step S5 becomes "CODE-3. The hold
  reports each terminated session's own generation; its scope is unchanged under D5. Tiers 0, 1, 8. Depends
  on: S1, S3". The plumbing exists: the timeout's members arrive as `heldSession{sessionID, state}` carrying
  the deregistered `*slotState`, so pass 2 still reads a generation that lives on the entry pass 1
  deregistered. Today both take one `gen` argument captured at arming — EVIDENCE:
  `pkg/adapter/slotsession.go:282-285`, `:361-365`; `pkg/adapter/holdstate.go:119`, `:225-229`, `:283-296`.
- OPEN: SCHEMA-1 in the non-spec changes names two field doc comments. It widens to the seven carriers the
  standing context enumerates. The record-and-reject carriers take one qualifier: the pod records the new
  generation against the session the fence names and from that point rejects every RPC carrying a generation
  older than the one it holds for that session, with the `FailedPrecondition` and `coordinator_handoff_stale`
  detail string they already name; the `CoordinatorFence` RPC comment additionally states the exemption as the
  first call for that session on this pod and scopes the cancellation and the reset to that session, and the
  `CoordinatorFenceResponse` comment takes the qualifier on its stale-fence and `gap_detected` sentences. The
  acceptance carriers validate against the last fenced generation for the session the request names, and
  under D7, which corrected the round-1 and round-2 wording, they state that a barrier naming a bound session
  the pod holds no fenced generation for is ACCEPTED and records no value. The
  `CoordinatorFenceRequest.coordination_generation` field comment keeps its wording.
- OPEN: TEST-1's pinning case in the non-spec changes §8 asserts "that the first session's hold is not released
  by the second's fence", which D5 forbids. That clause is replaced by the assertion D5 and SPEC-1 create: on a
  pod holding two sessions at different generations, arm the hold and let the timeout fire, then each
  terminated session's `coordinator_lost` record and post-mortem carries its own last fenced generation and
  the pod-level line carries none. The same case covers the unfenced co-tenant, which carries `0` on both, the
  way SPEC-1's §10.1.4 text has the hold report D6's unset value. The existing case that becomes this one is
  `pkg/adapter/holdstate_test.go:700-716`, whose post-fix form is `sess-a` 7 and `sess-b` 0. The
  accepted-handoff, accepted-barrier, and no-gap assertions stand as written, and those three are what must
  fail against the pre-fix code. Round 3's `[spec.3.fix-G1.1]` OPEN carries the rest of §8.
- OPEN: the status file's scope bullet drops "and releases its coordinator-loss hold", and its closing
  paragraph replaces "The hold-state decision in §7 is genuinely open and is the substance of this change."
  with the settled position: D5 decides the hold's scope and what moves is the generation the hold reports.
- OPEN: two cascades of D6 live outside the fixer's editable set. CODE-1 must state that `initialized` moves
  with `lastFenced`, and TEST-1 and §8 need a co-tenant's first fence well above 1 producing neither a gap nor
  a stale rejection plus a genuine skip on a second fence still producing a gap. Two test-side citations name
  the exemption as per pod lifetime and must rescope in the same pass — EVIDENCE:
  `pkg/adapter/coordination_test.go:58-61`; `tests/testinfra/coordfixture/coordfixture.go:106-108`.
- OPEN: which replica's connection carries a multi-session pod's CH-ADAPTEREVENTS events when more than one
  replica holds a connection to the pod, and therefore whether two co-tenant sessions can be coordinated by two
  different replicas at all. `spec/28` records the specification as not stating it. The whole defect premise
  rests on the answer, and D5's residual cannot be removed until it is settled. Staged as a §6 non-goal.
- OPEN: `coordinationState` also carries `quiesced` and `initialized`. CODE-1 moves the whole struct per
  session while `s.barrier` (`barrierGate`) stays pod-wide, so barrier quiescence silently becomes per-session
  and the gate does not. `barrier.link` links a Checkpoint stream's `checkpoint_id` into whichever gate is open
  with no session check, so two concurrent barriers on one pod cross-link and the loser blocks to its deadline.
  A code-lane loop must settle it — EVIDENCE: `pkg/adapter/coordination.go:25-39`, `:148-154`, `:247`, `:264`;
  `pkg/adapter/checkpoint.go:122`.
- UNVERIFIED [citations]: whether `coord.quiesced` moving onto the registry entry with the rest of
  `coordinationState` needs its own §10.1.8 edit. §10.1.8 step 2 already speaks per session ("records the
  `barrier_id` in the session's checkpoint metadata"), so it was judged consistent and not reported. A
  mechanism lens should confirm — EVIDENCE: `spec/10_gateway-internals.md:186`;
  `pkg/adapter/coordination.go:246-257`.


### [spec.2.fix-G1] · merged · §10.1.2 step 3 and the wire mirrors

Merges `[spec.2.fix-G1.1]`, `[spec.2.fix-G1.2]`, `[spec.2.fix-G1.3]`, `[spec.2.fix-design-G1.1]`,
`[spec.2.followup-fix.1]`, `[spec.2.postfix-review.2]`, and `[spec.2.postfix-review.3]`: one subject, one
staging, and one correction chain round 3 finished.

DECISION: Staged §10.1.2 step 3 as SPEC-1's third edit site, re-scoping the compared value to "the value it
holds for the session the RPC names" — BECAUSE §10.1.2 states the pod-side gate in three places (step 2's
fence announcement, the gap bullet, and step 3), SPEC-1 named only the first two, and step 3's "the fenced
value" is the pod-singular state the change retires; leaving it would have made step 2 and step 3 contradict
each other three lines apart — ALTERNATIVES: restating D6's initial condition inside step 3 as well as in the
gap bullet, rejected because one rule stated twice is the drift this proposal removes. The qualifier is one
phrase reused verbatim at every site of the acceptance rule and §28.5.1's staged Messages wording at every
site of the record-and-reject rule; a blended sentence would make the barrier's equality gate read as a
staleness gate.

CORRECTED by round 3: this round staged the unset case as a refusal, to match the shipped barrier gate.
`[spec.3.fix-G1.1]` reversed it to acceptance (D7), so two intermediate corrections are history: the pass-4
narrowing of the refusal from every operational RPC down to `CheckpointBarrier`, and the pass-7 removal of the
appositive naming `CheckpointBarrier` as the one RPC the adapter validates, which had put an
implementation-coverage fact into normative text against `spec/10_gateway-internals.md:30`.

FACT: `spec/10_gateway-internals.md:41` is the only acceptance-gate sentence in `spec/` or `docs/`.
`grep -rn "accepts only RPCs\|matches the fenced\|fenced value" spec/ docs/` returns that line alone, so
`spec/28` and `spec/29` carry the record-and-reject rule and the gap reset but not the acceptance predicate,
and staging step 3 created no new mirror obligation.

FACT: step 2's two halves are load-bearing and easy to reverse. The bar ("the new coordinator MUST NOT send
any operational RPC to the pod until `CoordinatorFence` returns a successful acknowledgement") is on the
acquiring coordinator, and the acceptance window that follows it covers the generation the pod already holds.
Text paraphrasing step 2 as "the pod accepts a coordinator's RPCs until that coordinator's fence is
acknowledged" states the opposite of the bolded sentence — EVIDENCE: `spec/10_gateway-internals.md:38`.

FACT: every operational request other than the fence and the barrier carries `coordination_generation` on the
wire and its handler ignores it, and `initialized` is set only inside `CoordinatorFence`, so a session no
coordinator has fenced never sets it and a refusal keyed on it is permanent rather than transient.

FACT: CODE-2's own comment at `pkg/adapter/coordination.go:228-231` cites §10.1.2, which now contains step 3,
so the barrier's per-session gate has a source sentence to cite and the pointer needs no edit. This settles the
design-side OPEN that asked for a citation change to the non-spec-changes file.

MISTAKE: an earlier survey recorded the `CoordinatorFenceRequest` evidence range as
`schemas/lenny-adapter.proto:1447-1452`, which starts on the `message` line, one line below the message-level
doc comment at `:1442-1446`. That off-by-one lost the message-level carrier of the record-and-reject rule for
three passes, and it is why the SCHEMA-1 OPEN cited `:153-162`, the RPC comment inside `service`, as though it
were the message comment. Cost: two findings across two rounds.

WATCHOUT: a message's doc comment and its field's doc comment are separate sites in this proto and often state
the same rule at different levels of detail. `CheckpointBarrierRequest` states the equality gate at both
`:1469-1474` and `:1477-1479`. Grepping for a field name finds the field comment and silently misses the
message comment above the `message` line.

USEFUL [standing context, "The §10.1.2 gap reset is mirrored in several places"]: it is what sent me to the
proto at all, and the mirror-inventory habit it teaches is what turned up the message-level comments. Also
USEFUL: the fixer's deviation from its design, removing SPEC-1's "two sentences" count rather than changing it
to "three"; the count would have gone stale again and the enumeration carries the information.

OPEN: `spec/04` §4.1's Request Message Scope table declares `CoordinatorFenceRequest` pod-scoped
(`spec/04_system-components.md:175`, `:188`) while `CheckpointBarrierRequest` is session-scoped (`:171`), and a
tier-3 test pins the classification
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:38-43`). SPEC-1's step 3 states the
same per-session reading and no staged entry names `spec/04`. Whether the declared classification survives is
a question for the reviewer or a later round.


### [spec.2.fix-G2] · merged · the §10.1.4 per-session generation and the zero sentinel

Merges `[spec.2.fix-G2.1]` and `[spec.2.fix-design-G2.1]`: the fix and the design it was written from.

DECISION: SPEC-1's §10.1.4 half names the artifact as "the local-disk post-mortem §10.1.4 has the adapter write
when no coordinator ever returns", names the two carriers of the per-session generation explicitly (the
per-session `coordinator_lost` slog line and the post-mortem JSON), and states the unset case as a zero
sentinel: a session no coordinator fenced on that pod carries `0` on both, and the zero reports D6's unset
value rather than a generation the pod holds — BECAUSE zero is already an impossible fenced value under gates
that ship today, so the sentinel costs no wire change, no JSON change, and no new state, and the representation
clause is what keeps §10.1.4 from contradicting §10.1.2 step 3 — ALTERNATIVES: omit the field when unset,
rejected because it changes the post-mortem JSON contract and makes an absent key indistinguishable from an
older writer; delete `lastGeneration` outright, rejected because the generation reconciles the pod-side record
against the session row's counter, which the proposal's "what is fixed" list names as a repair; leave
"omitted, zero, or null" to the implementor, which is the blank the finding is about. D6 and SPEC-2's §29.8
step 2 stay unchanged, because D6 governs the predicates rather than the representation and §29.8 step 2
mirrors the pod-level arming sentence, which carries no generation either way.

FACT: no `CoordinatorFence` can carry zero. §10.1.2 step 1 CASes `$expected_generation + 1` before step 2
announces the fence, the session column is `NOT NULL DEFAULT 0` with a `>= 0` check, the gateway floors a zero
row at 1 before fencing, and the adapter rejects a non-positive generation with `InvalidArgument` — EVIDENCE:
`spec/10_gateway-internals.md:37`; `migrations/0050_session_record_fields.up.sql:38-39`;
`pkg/gateway/coordination/coordfence/coordfence.go:147-153`; `pkg/adapter/coordination.go:93-94`.

FACT: `coordinator_lost` is a reason string rather than a record. The only two artifacts carrying a per-session
generation are the slog line keyed `coordinator_lost` with `last_generation` and the post-mortem JSON's
`lastGeneration`. `AdapterTerminating` and `session.terminated` carry `session_id` and `reason` and no
generation — EVIDENCE: `pkg/adapter/holdstate.go:28`, `:226-229`, `:283-296`;
`spec/04_system-components.md:747`.

WATCHOUT: "each session's `coordinator_lost` record" reads equally as the terminal event, and an implementor
could answer it by adding a generation field to `AdapterTerminating`. Name the two carriers in the staged
sentence; adding a proto field is barred by the proposal's non-goals.

CORRECTS [round 1, D5's second DECISION]: that decision justified keeping `lastGeneration` on the post-mortem
partly with "it is the terminal record when no coordinator returns". §10.1.4 designates no terminal record on
the pod; it assigns the terminal transition to the gateway's orphan session reconciler (`failed`, reason
`orphan_pod_terminated`), because agent pods have zero RBAC bindings and no apiserver path. The decision
survives on its other half — EVIDENCE: `spec/10_gateway-internals.md:58-59`.

WATCHOUT: the design handed to this group quoted the target paragraph at `spec-changes.md:109-116`, and another
group's edits in the same round had already pushed it to `:132-139`. Anchor on sentence text rather than on a
line number when two groups write the same file in one round.

USEFUL [standing context, "`cat -n *spec-changes.md` globs two files"]: saved a mis-numbered anchor. Named each
file in full throughout.

OPEN: `pkg/adapter/holdstate_test.go:700-716` asserts `LastGeneration: 7` for both `sess-a` and `sess-b` when
only `sess-a` was fenced. Its post-fix form is `sess-a` 7 and `sess-b` 0. The testing section is outside a spec
loop's writable set, so the obligation is carried in the round-1 TEST-1 OPEN.

UNVERIFIED: whether tier 1 alone is the right home for the sess-b-is-zero assertion, or whether the
non-spec-changes §8 pinning case should also name it.


### [spec.2.fix-G3] · merged · the §1.3 consequence chain

Merges `[spec.2.fix-G3.1]`, `[spec.2.fix-G3.2]`, `[spec.2.fix-G3.3]`, and `[spec.2.fix-design-G3.1]`: one
recalibration of the problem statement's worked example and the summary sentence that restates it.

DECISION: Rewrote §1.3 step 3 to the §10.1.5 stale-replica path (re-read, no advance, relinquish,
`ErrRelinquished`) and moved the citation off §10.1.2, and restated the drain-barrier bullet as lost
quiescence, a missing barrier record, and a duplicated capture rather than as a lost partial manifest, bringing
summary.md's "what is fixed" list to the same words in the same edit — BECAUSE §10.1.2 gives no
re-read-and-re-issue instruction for a pod rejection, and because the gateway writes the manifest on both
fallback paths and the checkpoint goroutine is started before `dispatch.Send` and joined before the stale
branch — ALTERNATIVES: softening only the verb, which leaves the false attribution standing; deleting either
bullet, rejected because each still names a real defect the per-session generation fixes.

FACT: a generation-stale rejection does not strand the session. `Fencer.fence` relinquishes without
re-issuing, the sweeper releases the lease, records a per-session adoption backoff, and retakes on a later
sweep, and each takeover increments `coordination_generation`. On the resume path `ErrRelinquished` is
retryable: the row is held in `awaiting_client_action` so the client's `POST /resume` retry routes to the
rightful coordinator. There is no unconditional fixed-period retry loop — EVIDENCE:
`pkg/gateway/coordination/coordfence/coordfence.go:164-179`, `:195-200`;
`pkg/gateway/coordination/coordination/coordination.go:399-416`, `:463-468`;
`pkg/gateway/sessionserver/start.go:3668-3672`.

FACT: `dispatchOne` runs the gateway-driven `Checkpoint` stream concurrently with the barrier and `cpWG.Wait()`
runs before the `ErrGenerationStale` branch, so a generation-stale barrier sets only `Stale` and never un-does
the checkpoint. `Outcome.CheckpointErr`'s own comment records that the stream finalises the manifest row
itself, partial on abort — EVIDENCE: `pkg/gateway/coordination/barrier/barrier.go:209-232`, `:154-158`.

FACT (closes this group's own OPEN on the §1.3 counter bullet): `lenny_coordinator_handoff_stale_total` is
incremented in production on every `FailedPrecondition` fence rejection, including the co-tenant one this
proposal removes, so §1.3's "increments on legitimate handoffs" wording holds — EVIDENCE:
`pkg/gateway/coordination/coordfence/coordfence.go:164-179`, `:25-27`.

WATCHOUT: `quiesced_ms` is never persisted. It exists only on the client-side ack struct, and the
`sessioncheckpointmeta.Record` an acknowledged barrier upserts carries `barrier_id`, `checkpoint_ref`, and
`workspace_recovery_fraction`. Naming `quiesced_ms` among the records a rejected barrier loses is wrong —
EVIDENCE: `pkg/gateway/runtime/adapterclient/client.go:450-455`;
`pkg/gateway/coordination/barrier/barrier.go:237-246`.

WATCHOUT: a rejected barrier causes a duplicate capture rather than no capture. The concurrent stream already
ran, and prestop's post-barrier loop then checkpoints every session whose `barrierAcked` entry is false —
EVIDENCE: `pkg/gateway/podlifecycle/prestop/prestop.go:366`, `:390-397`.

FACT: `proposals/` is excluded from the specshift scope, so the N8 line-citation prohibition does not reach a
proposal file and the folder's convention of citing `spec/10_gateway-internals.md:NN` stands — EVIDENCE:
`scripts/specshift/scope/scope.go:99` (`readExcludedPrefix = "proposals/"`).

MISTAKE: §1.3 step 3 attributed the CAS-failure clause's "re-read Postgres and restart" to a pod fence
rejection. The stale replica's own duties are §10.1.5 (stop RPCs, clear state, back off, release the lease when
the generation has not advanced) — EVIDENCE: `spec/10_gateway-internals.md:37`, `:39`, `:66-71`.

OPEN: after this recalibration the headline harm in §1.3 is availability churn plus unquiesced capture, and no
bullet claims data loss. The rationale still stands on the false split-brain signal, the misattributed
`coordinator_lost` generation, and the stale counter, but a reviewer may want the severity restated once at
the top of §1 rather than only inside §1.3.


### [spec.2.review-*.1] · merged · the twelve lens reviews and the citation sweep

All twelve lenses converged on one finding, that §10.1.2 step 3 was left unstaged, and each re-derived the
`docs/`, proto, and `spec/28`-`spec/29` inventories now in `## Standing context`. The finding is closed: the
round-2 fixer staged step 3 and round 3's D7 settled its unset half. What survives is the refutation record
and the items nothing has closed.

FACT: every file:line citation the round-2 fixer newly wrote resolves and says what the new text claims, each
checked individually. Round 3's citation lens re-ran the sweep over the whole spec-changes file, about seventy
distinct sites, with the same result, so the citation question is closed for both rounds and the per-site
lists are not worth carrying.

FACT: no test pins the sentences SPEC-2 edits, and `spec/29` §29.10 sits in
`tests/spec-map-exceptions.yaml:388` under blocker R7 with the retention gate reading only the exception row
rather than the bullets — EVIDENCE: `tests/tier0_static/spec_map_exception_blocker_retention_test.go:65`.

FACT: `spec/18` puts `CoordinatorFence`, the `coordination_generation` CAS, and the uniform per-slot workspace
layout in Phase 4 while `maxConcurrentSessions > 1` is Phase 12c, so keying the generation on the per-slot
entry is not a phase inversion — EVIDENCE: `spec/18_build-sequence.md:235`, `:238`, `:532`.

WATCHOUT: the refuted list kills the "unqualified sentence inside a session-scoped narrative" class of finding
twice (§29.8 step 7, §4.1's declared `pod` class). Do not re-raise §28.6's "The second opener on those
channels" sentence (`spec/28_communication-channels.md:1680`) or the §28.8 `CH-CHECKPOINT` exclusivity cell
(`:1806`) on that ground: they describe a replica's status rather than the pod's comparison. What does license
a finding is written out in SPEC-2's own §28.6 rationale, that a sentence stating the pod's accept-or-reject
predicate is an edit site because a pod-wide gate satisfies it today. Do not stretch that to
precondition-ordering sentences such as §28.5.1's `CH-FENCE` Preconditions bullet.

WATCHOUT: the exemption-unit argument ("D6 moves the first-fence exemption from once-per-pod to
once-per-session, so N exempt fences replace 1, weakening the split-brain bound") was costed and rejected in
three lenses. A fence is issued only after a successful Postgres CAS, so every first fence carries a generation
current at CAS time; an out-of-order pair is self-healing by step 2's own clause; and a fence for a session
with no bound entry never reaches the predicate because `checkSessionBound` rejects it first. Do not re-file it
without new evidence — EVIDENCE: `spec/10_gateway-internals.md:37`, `:39`; `pkg/adapter/coordination.go:89`;
`pkg/adapter/slotsession.go:267`.

WATCHOUT: §29.10's "Shared by the whole pod" transport bullet distinguishes `LNK-POD-GRPC` (one connection per
gateway replica per pod) from `LNK-GWCONTROL` (one per pod process to one replica), so "loss of the
gateway-to-pod connection" is ambiguous in that section specifically — EVIDENCE:
`spec/29_communication-scenarios.md:1475-1478`.

DECISION: framed the D5 residual as a contradiction with §10.1.4's trigger predicate rather than as an accepted
cost the proposal should document — BECAUSE the variant framed as a coordination-topology gap was refuted as
scope expansion, and the durable defect is narrower: §10.1.4 arms the hold "upon detecting connection loss with
no active coordinator" while D5 arms it on the CH-ADAPTEREVENTS stream close with a co-tenant's coordinator
alive — EVIDENCE: `spec/10_gateway-internals.md:55`; `pkg/adapter/adapterevents.go:90-108`;
`pkg/adapter/holdstate.go:90-100`.

OPEN [applicability]: whether the applied §10.1.4 should give the pod-level `coordinator_connection_lost`
event's started-session count a field name. §10.1.4's current bullet names no field for the generation either,
so it was not raised; a docs or observability lens may disagree.

OPEN [citations]: proposal 0080's section "1.14 Whether the adapter's hold state is partitioned per slot is
still unstated" covers the same §29.10 bullet SPEC-2 stages for removal. Whichever lands second rebases;
nobody has recorded the overlap.

OPEN [mechanism, performance]: SPEC-1 states the initial condition as "unset until that session's first
accepted fence on that pod" while D6 states the unit as "the session's binding on the pod". Under D2 the value
lives on the slot registry entry, whose lifetime is the binding, so the two differ only if a session can unbind
and re-bind on the same pod. Nobody has settled whether the gateway ever re-binds the same session id onto the
same pod after `releaseSessionSlot` runs on a resume failure path (`pkg/adapter/resume.go:69-141`). Owner: a
gateway-side reviewer reading `pkg/gateway/sessionserver` resume placement.

UNVERIFIED [reliability]: whether the fence-retry path is idempotent. §10.1.2 tells a coordinator to retry the
fence with the same generation after a lost ack, but the adapter rejects `gen <= lastFenced`
(`pkg/adapter/coordination.go:99`), so a retry whose first attempt was recorded is rejected as stale while the
spec's own rule says only an older generation is rejected (`spec/10_gateway-internals.md:38`). Pre-existing on
both sides and unchanged by per-session scoping.

UNVERIFIED [security]: whether the gateway ever acts on the pod's self-reported `last_fenced_generation` on
`CoordinatorFenceResponse`. `adapterclient.CoordinatorFenceResult` was not traced into `coordfence.Fencer`'s
decision. If it does, a per-session value changes what the gateway is told on a rejection. Chase
`pkg/gateway/coordination/coordfence/coordfence.go:159` onward.


### [spec.3.fix-G1.1] · 2026-08-31 · fix · G1 (the never-fenced session's drain barrier)

DECISION: Closed the finding by stating ONE uniform acceptance rule rather than by adding the barrier's
sections to the edit lists as refusal-reconciliation sites. §2 gains D7 (a `CheckpointBarrier` naming a
bound session the pod holds no fenced generation for is accepted and records no value); staged §10.1.2
step 3 says the barrier is accepted for such a session; SPEC-1 gains §10.1.8 step 1 as an edit site where
the existing rejection sentence becomes a pair naming the state each outcome depends on; SPEC-2 gains
§29.7's framing paragraph taking the same qualifier; §7 open decision 1 keeps the operator open and drops
the paragraph asserting the refusal — BECAUSE §10.1.2 step 2 already states that the pod accepts a
coordinator's RPCs until that coordinator's fence is acknowledged
(`spec/10_gateway-internals.md:38`), so accepting an unfenced coordinator's barrier generalizes a rule
§10.1.2 already carries, and staging the refusal instead would have put step 3 in contradiction with step 2
three lines apart, falsified §10.1.8's interruption bound (`:198`) and §29.7's closed outcome set
(`spec/29_communication-scenarios.md:1150-1152`), and required a drain arm for a synchronously refused
barrier that nothing implements — ALTERNATIVES: (a) narrow step 3 and move the barrier sentence into §7,
rejected because D6 makes "unset" a named pod-side state and leaving the gate on it unstated hands the same
gap to a later round; (b) stage the refusal and reconcile §10.1.8, §29.7, §28.5.1, and §28.8, rejected as
writing a shipped defect into the specification; (c) fence every session before its drain barrier, rejected
as an RPC round trip inside the §10.1.8 drain budget and as contradicting §10.1.2's tie of `CoordinatorFence`
to lease acquisition; (d) delete the barrier's generation gate, rejected because the equality arm is what
§28.5.1's CH-BARRIER Exclusivity bullet and the §28.8 row enforce; (e) loosen the operator to "at least",
rejected because it settles §7 open decision 1 by arithmetic.

FACT: the shipped barrier gate is `!initialized || gen != fenced`, and the `!initialized` half refuses the
drain barrier of any session that never resumed and was never taken over, because the only two fence senders
are the resume path and the sweeper re-adopt — EVIDENCE: pkg/adapter/coordination.go:236-239;
pkg/gateway/sessionserver/start.go:4237; cmd/lenny-gateway/coordination_seams.go:233 (`grep -rn "\.Fence("
pkg/ cmd/ --include=*.go | grep -v _test.go` returns those two lines and nothing else).

FACT: §10.1.8 defines exactly one failure arm, the ack deadline at `spec/10_gateway-internals.md:187`, and a
synchronous `FailedPrecondition` never reaches it. The gateway's disposition of a refused barrier is
`out.Stale = true` with no `sessioncheckpointmeta` upsert, and prestop then checkpoints the session again
because `barrierAcked` is false — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:229-246;
pkg/gateway/podlifecycle/prestop/prestop.go:390-397.

FACT: §28.5.1's CH-BARRIER Exclusivity bullet (`spec/28_communication-channels.md:361-365`) and the §28.8
CH-BARRIER row (`:1808`) both state the refusal as "a barrier from a superseded replica", which is a session
the pod holds a recorded value for, so neither is an edit site under D7. Recording that explicitly in SPEC-2
is what keeps a later round from re-finding them as unstaged sites.

WATCHOUT: `checkSessionBound` runs before the generation gate in `CheckpointBarrier`, at
`pkg/adapter/coordination.go:216` rather than the `:215` an upstream design note gave. Acceptance-when-unset
therefore applies only to a session already bound on the pod, which is the property that keeps D7 from
opening the barrier to an unbound session — EVIDENCE: pkg/adapter/coordination.go:212-226.

MISTAKE: pass 4's correction narrowed step 3's refusal from every operational RPC down to `CheckpointBarrier`
without re-checking the surviving half against the barrier's own sections. Cost: one finding, one round, and
a design pass. The lesson is mechanical: when a fix narrows a predicate to one named RPC, read the sections
that own that RPC before the narrowing lands.

OPEN: this loop's writable set was the spec-changes, problem-statement, summary, and review-log files, and
three of the design's edits target the non-spec-changes file. They are recorded in pass 7 of the spec
changes and indexed in the summary's watch-out paragraph, for whichever loop may write that file. CODE-2
becomes the gate change: `CheckpointBarrier` resolves the slot registry entry for the named session and
refuses only when that entry holds a recorded generation the request does not match, so the condition is
`initialized && gen != fenced` read from the entry, with `initialized` moving onto the entry alongside
`lastFenced` and never read from `Server`; the `InvalidArgument` guards and the `checkSessionBound` ordering
are unchanged. §8 gains the tier-1 rewrite of `TestCheckpointBarrierRejectsWithoutFence`
(`pkg/adapter/coordination_test.go:185-197`, whose `// spec:` annotation encodes the retired reading),
keeps `TestCheckpointBarrierRejectsGenerationMismatch` (`:199-216`) as the surviving-refusal case, and adds
the co-tenancy case, a tier-3 wire case for both arms, and a tier-8 crash-takeover case. The files-touched
list gains `spec/28_communication-channels.md`, `spec/29_communication-scenarios.md`, and
`pkg/adapter/coordination_test.go`, and names §10.1.2, §10.1.4, and §10.1.8 under `spec/10`.

OPEN: the implementation checklist needs no structural change for D7. S4 ("CODE-2. `CheckpointBarrier`'s
gate reads the per-session generation") stays accurate, and SPEC-1's and SPEC-2's new sites both land in the
existing S1. No step is added, removed, or resequenced.

CORRECTS [spec.1.followup-fix.2, its SCHEMA-1 widening and its closing OPEN]: the acceptance carriers on the
wire state that a barrier naming a session the pod holds no fenced generation for is ACCEPTED, not refused.
That OPEN also framed the refusal as the thing §7's reviewer would decide; D7 settles it and leaves the
reviewer only the operator.

CORRECTS [spec.2.fix-design-G1.1, its first DECISION]: step 3's unset half was staged as a refusal to match
the shipped barrier gate, on the ground that writing the opposite would pre-empt §7 open decision 1. The
shipped gate is the defect, §7 item 1 owns the operator rather than the domain, and the refusal contradicts
§10.1.2 step 2. The unset half is now stated as acceptance.

USEFUL [standing context, "§10.1.2 step 3 fixes equality as the operational-RPC gate"]: it is what kept this
fix off the operator. Changing the domain of the comparison and leaving equality alone is what lets §7 item
1 stay open while the finding closes.


### [spec.3.fix-design-G1.1]

DECISION: Close the finding by making §10.1.2 step 3 state ONE uniform rule — a session for which the pod holds no fenced generation has no recorded value to match, so no gateway-to-pod RPC naming it is refused on generation grounds, `CheckpointBarrier` included — and by changing CODE-2's gate from `!initialized || gen != fenced` to `initialized && gen != fenced`. BECAUSE the staged exception clause enshrines a shipped defect: `pkg/adapter/coordination.go:234-239` refuses the barrier for every never-fenced session, and the only two fence senders are the resume path and the sweeper re-adopt, so an ordinary session is never fenced and its drain barrier is refused today. Making that normative in §10.1.2 forces §10.1.8, §29.7, §28.5.1 and §28.8 to grow a refusal arm nothing implements. ALTERNATIVES: (a) delete the barrier sentence from step 3 and defer to §7 — rejected, D6 makes "unset" a named spec state so the gate on it cannot stay unstated, and §7 item 1 still asserts the refusal; (b) stage the refusal and reconcile the four barrier sections — rejected, it specifies a defect and a later proposal undoes it; (c) delete the barrier's generation gate entirely — rejected, it is the only thing stopping a superseded replica quiescing a session it lost, and §28.8/§28.5.1/§29.7 all state it.

FACT: §10.1.2 step 2 already states "Until the pod acknowledges the fence, the pod still accepts RPCs carrying the previous generation" — EVIDENCE: spec/10_gateway-internals.md:38. This is the spec's own statement that an unfenced pod accepts the prior coordinator's RPCs, so option C generalizes an existing rule and option B would have contradicted step 2 directly. This is the decisive argument; do not re-derive it.

FACT: the adapter gates on `coordination_generation` in exactly two handlers, `CoordinatorFence` (`:92`) and `CheckpointBarrier` (`:223`) — EVIDENCE: `grep -n "GetCoordinationGeneration()" pkg/adapter/*.go`. So §10.1.2 step 3's "all gateway-to-pod RPCs" gate is implemented for one RPC, and today the barrier is STRICTER than every other RPC for a never-fenced session. Making it match the rest is the coherent rule.

FACT: the barrier gate is reached only after `checkSessionBound` (`pkg/adapter/coordination.go:215`), so accept-when-unset applies only to a session already bound on the pod. That binding is the safety fence; state it when staging.

WATCHOUT: §10.1.8 step 1's "Pods receiving a barrier for a session no longer coordinated by this replica ... reject the barrier as a generation-stale RPC" (spec/10_gateway-internals.md:183) is an over-statement TODAY, independent of this proposal: the rejection needs the acquiring replica's fence to have landed, and §10.1.2 step 2 defines a window before it does. Qualify that one sentence in SPEC-1 and the matching sentence in §29.7's framing paragraph (spec/29_communication-scenarios.md:1150-1152) in SPEC-2. Leave §28.5.1's CH-BARRIER card and the §28.8 CH-BARRIER row alone: they say "superseded replica", which means a later generation was fenced, and that stays exactly true.

FACT: on a Stale barrier the gateway records `out.Stale`, writes no barrier record, and prestop's per-session loop then captures the session a second time because `barrierAcked[sess.SessionID]` is false — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:229-246; pkg/gateway/podlifecycle/prestop/prestop.go:390-397. The chosen fix removes that double capture for the ordinary session, which is a positive cascade worth naming in the proposal.

WATCHOUT: `TestCheckpointBarrierRejectsWithoutFence` inverts under this fix — EVIDENCE: pkg/adapter/coordination_test.go:188-197. Its `// spec: §10.1.2 — fence is a precondition for any subsequent operational RPC` annotation is the shipped reading of step 3 that this change retires. Rewrite the test, do not delete it.

DECISION: keep §7 open decision 1 alive but correct its body. BECAUSE the standing context records §7's three decisions as carved out of findings for the human reviewer, and the operator question (equality vs a looser comparison against a recorded value) is untouched by this fix; what must go is the paragraph asserting the drain barrier is refused, which the fix removes. Replace it with a pointer to the new decision on the unset case. ALTERNATIVES: retiring item 1 into a decision — rejected, it removes a human decision point the carve-out protects.

UNVERIFIED: whether any tier-3 or tier-8 test asserts a refused barrier for a never-fenced session outside pkg/adapter. The implementor should grep tests/tier3_contract and tests/tier8_chaos for `coordinator_handoff_stale` and barrier assertions before landing CODE-2.


# [spec.3.postfix-review] · 2026-08-31 · post-fix review of round 3's G1 fix

SCOPE: verified only the round-3 edits (diff against `scratchpad/cp-snap/r3-prefix`), on the three
questions LANDED / DRIFT / CITATIONS. Did not re-review the proposal at large.

LANDED — the finding is corrected in substance. D7 is added (`spec-changes.md:47-65`), staged §10.1.2
step 3 no longer refuses the never-fenced session's barrier (`:129-138`), §10.1.8 step 1 is a new SPEC-1
edit site (`:164-176`), §29.7's framing paragraph is a new SPEC-2 edit site (`:276-282`), §28.5.1 and
§28.8 are recorded as non-sites with a reason (`:240-244`), §7 item 1 drops the refusal assertion and
keeps the operator open (`:641-647`), and the summary carries D7 and the deliverable-side corrections
(`summary.md:10-12`, `:18-22`, `:33-42`). The deferral of CODE-2 / §8 / files-touched into Pass 7 matches
the file's existing convention.

CITATIONS VERIFIED AS REAL AND ACCURATE: `spec/10_gateway-internals.md:41` (step 3), `:183` (barrier
signal, the SELECT and the false-positive sentence), `:184`, `:185`, `:187`, `:198`, `:30`;
`spec/29_communication-scenarios.md:1150-1152`, `:1193-1196` (§29.7 is 1142-1243);
`spec/28_communication-channels.md:1808` (§28.8 CH-BARRIER, fourth column);
`pkg/adapter/coordination.go:92`, `:99`, `:211`, `:212-226`, `:216`, `:223`, `:228-231`, `:236-239`;
`pkg/gateway/coordination/barrier/barrier.go:229-246`;
`pkg/gateway/podlifecycle/prestop/prestop.go:390-397`; `pkg/gateway/sessionserver/start.go:4237`;
`cmd/lenny-gateway/coordination_seams.go:233`; `pkg/adapter/coordination_test.go:185-197`, `:199-216`.
`grep -rn "GetCoordinationGeneration" pkg/adapter/*.go` returns `:92` and `:223` alone, so D7's
"only RPC the adapter validates" premise holds against the tree.

STATE / CALLERS / TEST checks on the new mechanism, all clean: `lastFenced` and `initialized` are
written only at `pkg/adapter/coordination.go:119-121` and cleared nowhere
(`grep -rn "lastFenced\|\.initialized" pkg/ cmd/ --include=*.go`); the barrier gate has one pod-side
caller and one gateway-side dispatcher (`barrier.go:226`); no interface changes; the rewritten
tier-1 case would fail pre-fix, because `!initialized` returns FailedPrecondition today, and the
quiesce-and-hold harness the rewritten case needs already exists
(`pkg/adapter/coordination_test.go:218-233`).

FINDINGS RETURNED (3): (1) staged step 3 names `CheckpointBarrier` "the one gateway-to-pod RPC the
adapter validates on this gate", which puts an implementation-coverage fact into normative spec text
against step 3's own first sentence, against the proposal's own domain claim at `spec-changes.md:136-138`,
and against the unstaged §10.1 bullet at `spec/10_gateway-internals.md:30`; (2) the D7 grounds and the
staged step-3 cross-reference paraphrase §10.1.2 step 2 as "the pod accepts a coordinator's RPCs until
that coordinator's fence is acknowledged" (`spec-changes.md:48`, `:133-134`, `:602`), which reverses
what `spec/10_gateway-internals.md:38` says and negates its bolded hard precondition; the staged §10.1.8
text at `spec-changes.md:168-170` reads step 2 correctly, so the proposal states it both ways;
(3) the §28 non-site paragraph attributes "so a barrier from a superseded replica is rejected on the
stamp" to both `spec/28_communication-channels.md:361-365` and `:1808`, and only `:1808` carries it.

NOT REPORTED, recorded for the next round: `lenny_coordinator_handoff_stale_total` is incremented only
on the fence path (`pkg/gateway/coordination/coordfence/coordfence.go:205`), never on a barrier
`ErrGenerationStale`, and `lenny_checkpoint_barrier_ack_total` has a catalog entry
(`pkg/observability/metrics/catalog.go:127`) with no gateway emitter. The proposal's text claims neither,
so this is not a defect in the edits; a design rationale that leans on either counter for barrier
observability is wrong against the tree. Also unreported: the pod-wide `barrierGate`
(`pkg/adapter/coordination.go:148-166`) is a single waiting slot, so a second concurrently accepted
barrier on a co-tenant pod clobbers the first one's `done` channel. D7 widens the set of accepted
barriers, but the per-session gate (CODE-1/CODE-2) already creates that exposure, and the proposal
deliberately leaves "the unit of the quiescence a barrier establishes" unanswered
(`spec-changes.md:271-275`).


### [spec.3.review-applicability.1]

DECISION: Returned zero findings — BECAUSE every anchor SPEC-1 and SPEC-2 name resolves verbatim and
uniquely, the one relocation stages both legs, no staged edit forward-references an artifact a later
sub-step creates, and no existing tier-0 or tier-11 gate reads any sentence the staging rewrites —
ALTERNATIVES: rejected reporting (a) step 3's staged text reading "the pod does not reject that session's
RPCs on generation grounds; `CheckpointBarrier` ... is the one RPC refused for such a session" as a
self-contradiction, because the semicolon clause is an explicit carve-out rather than a silent conflict and
the fix is punctuation; (b) that §28.5.1's Degradation bullet, the §28.8 `CH-FENCE` cell, and §29.8 step 7
restate the gap predicate without D6's initial condition, because silence on an initial condition is
underspecification and this loop has already refuted that exact pattern twice; (c) that SPEC-1's summary
paragraph says §29.10's two bullets "take the acceptance sentence" while SPEC-2's edit list gives a
paraphrase ("the generation the pod records on a fence and validates every gateway-to-pod RPC against is the
fenced session's"), because the two express one rule and SPEC-1 states explicitly why the unset-value clause
stays in §10.1.2.

FACT: only the review log changed between the round-3 snapshot and the current folder
(`diff -rq scratchpad/cp-snap/0076-run2/spec-r3 proposals/0076_...` reports one differing file), so the
spec-changes text this round reviews is byte-identical to round 2's post-fix state. Do not spend a round
diffing the staging; diff the log only to see what compaction retired.

FACT: the §28.4 claim-register gate is a SCHEMA gate, not a coverage gate. `claim_register_test.go:23-60`
checks well-formedness, the closed status set, a `WIRED` row's surface, a non-`WIRED` row's deferral
identifier against `gateway-runtime-comms-remediation.md`, and anchor resolution against §28 headings. It
does NOT check that every normative §28 sentence has a row, so adding a normative sentence to §28.5.1
breaks nothing and SPEC-2's "no claim-register row moves" holds — EVIDENCE:
tests/tier0_static/claim_register_test.go:23-46, :64-80.

FACT: `tests/claim-map.json` carries an UNWIRED row per gateway-to-pod request that declares
`coordination_generation`, each with `deferral_id: "R16"` and the note "the field is carried on the request
and no production reader compares it until the generation fence lands". That records a pending mechanism in
which EVERY such RPC compares. SPEC-1 leaves step 3's opening sentence unchanged and keeps the whole
gateway-to-pod RPC set as the acceptance rule's domain, so those rows stay correct and R16 stays open —
EVIDENCE: tests/claim-map.json:47-54, :76-82; spec/10_gateway-internals.md:41.

FACT: no tier-0 or tier-11 gate string-matches any sentence SPEC-1 or SPEC-2 rewrites. Grepping
tests/tier0_static and tests/tier11_docs for "rejects any RPC carrying an older", "matches the fenced
value", "last known generation", "last fenced generation", and "coordinator_generation_gap" returns only
`claim_register_proto_agreement_test.go`, which keys on proto FIELD declarations rather than comments, so
SCHEMA-1's comment-only edits cannot trip it.

FACT: §4.7's RPC table is not a mirror of the pod-side fence rule. `spec/04_system-components.md:711-712`
says only that `CheckpointBarrier` "Carries `coordination_generation` and `barrier_id`" and that
`CoordinatorFence` announces the new generation with "Gap detection handled on the adapter side". Neither
states record-and-reject nor the equality gate, so §4.7 is not an unstaged edit site. Chased once; do not
re-chase.

FACT: the CH-BARRIER card (spec/28_communication-channels.md:349-376) and the CH-PODHEALTH Preconditions
(:389-392) state no pod-unit acceptance predicate, which confirms SPEC-2's claim that neither `spec/28` nor
`spec/29` carries step 3's acceptance rule today. CH-PODHEALTH does record that the specification "does not
state how that rule applies to a probe against a pod that is not yet serving a coordinated session"; SPEC-1
narrows the rule's domain to RPCs naming a session but never says an RPC naming none is outside it, so that
"does not state" line survives the edit. I judged it clean rather than an unstaged site.

WATCHOUT: §29.10's removed "does not state" bullet contains no literal generation question. Its two
questions are whether hold state is partitioned per slot and whether a fence for one slot's session "holds
the RPCs of a sibling slot's session". SPEC-2 splits that second question into a hold half and a generation
half, which is a reading rather than a quotation. It is defensible (a pod-wide generation gate and a
pod-wide hold both make a sibling's RPCs fail), but a reviewer who reads "the removed bullet's generation
question" expecting to find one in the source text will not — EVIDENCE:
spec/29_communication-scenarios.md:1523-1527.

FACT: the §29.10 coordination bullet's closing cross-reference ("which the list of what the specification
does not state records below") points at the two-different-replicas question at
spec/29_communication-scenarios.md:1540-1543, which SPEC-2 keeps. Removing the first bullet does not
dangle it.

USEFUL [spec.2.review-applicability.1]: its anchor inventory and its §28.8 bijection and §29.10
successor-pointer gate findings held on re-check and saved a full pass. Its step-3 finding is the one this
round's staging closes.


### [spec.3.review-citations.1]

DECISION: Returned an empty findings list for the citation lens — BECAUSE I extracted every `file:line`,
spec-section, and quoted-text citation in the spec-changes file (about 70 distinct sites) and every one
resolved to text that says what the proposal claims — ALTERNATIVES: reporting the near-misses listed under
WATCHOUT below, rejected because each is off-by-a-few drift or historical-section wording that does not
change meaning.

FACT: the full citation set in `...spec-changes.md` was verified against the tree this round. Spot-check
results worth not re-deriving: `spec/10_gateway-internals.md:30` (§10.1 summary bullet, "if the generation
is stale, the pod rejects"), `:37` (step 1 CAS increment), `:38` (fence-announcement + hard-precondition),
`:39` (fence-failure retry/relinquish), `:40` (gap bullet, parenthetical is exactly "(the generation from
the last successfully acknowledged fence)"), `:41` (step 3, exactly "The pod accepts only RPCs whose
generation matches the fenced value"), `:58` (hold timeout + local-disk post-mortem), `:58-59` (orphan
reconciler owns the terminal transition), `:62` (whole-pod connection loss), `:185`/`:190` (§10.1.8 gateway
finalises the manifest, BarrierAck-timeout `manifest_reason = "timeout"`).
EVIDENCE: spec/10_gateway-internals.md:30,37,38,39,40,41,58,59,62,185,190

FACT: the seven proto carriers SPEC-2's closing paragraph enumerates all resolve exactly:
`schemas/lenny-adapter.proto:153-162` (CoordinatorFence RPC, `:161-162` is verbatim "The first call on a
pod's lifetime is never treated as a gap regardless of value"), `:165-179` (CheckpointBarrier RPC),
`:1442-1446` (message-level CoordinatorFenceRequest), `:1449-1451` ("Strictly monotonic on the pod side per
session"), `:1455-1462` (CoordinatorFenceResponse), `:1469-1474` (message-level CheckpointBarrierRequest),
`:1477-1479` (its field comment). The remaining twelve `coordination_generation` field comments in the file
all carry the identical "gateway's view of the active coordination generation for the session ... A pod
validates the generation on every gateway-to-pod RPC and rejects a stale coordinator's request" block, which
names no pod-wide value, so SPEC-2's "not edit sites" call holds. EVIDENCE: schemas/lenny-adapter.proto:969,
995, 1046, 1070, 1091, 1114, 1172, 1305, 1393, 1531, 1576, 1618

FACT: the `spec/28` and `spec/29` mirror inventory in SPEC-2 is complete for both rules. A grep for
`coordinator_generation_gap|last_fenced_generation|last fenced generation|fenced value` across `spec/` and
`docs/` returns exactly the four gap-reset sites SPEC-2 names (spec/10:40, spec/28:333-335, spec/28:1807,
spec/29:1307-1313) and nothing in `docs/`. A grep for the record-and-reject wording returns exactly
spec/28:315-316, spec/28:1675, spec/28:1807 plus neutral generation-stamp mentions at spec/28:274, :295,
:354 and spec/29:812, :1263, :1296, :1303 that assert no pod-wide value.
EVIDENCE: spec/28_communication-channels.md:315,333,1675,1807; spec/29_communication-scenarios.md:1309

FACT: `.Fence(` has exactly two non-test call sites in the tree, `pkg/gateway/sessionserver/start.go:4237`
and `cmd/lenny-gateway/coordination_seams.go:233`, and `GetCoordinationGeneration` has exactly two in
`pkg/adapter`, `pkg/adapter/coordination.go:92` (fence) and `:223` (barrier). Both are load-bearing for
§7's first open decision and for SPEC-1's "no other handler refuses an RPC on generation grounds today",
and both check out. `enterHoldState` likewise has one caller chain, adapterevents.go:107 →
holdstate.go:90-99, which is D5's whole basis.
EVIDENCE: pkg/adapter/coordination.go:92,223; pkg/adapter/adapterevents.go:107; pkg/adapter/holdstate.go:99

WATCHOUT: three near-misses that are NOT findings and cost a round if re-chased. (1) SPEC-1's "the value the
predicate reads is set at one site (`:121`)" — `lastFenced` is assigned at `:120` and `initialized` at
`:121`; both feed the predicate, so the cite is one line short of the pair, not wrong. (2) Pass 4's
correction says §29.10's coordination bullet "ends at 'so each slot's session carries its own lease and its
own generation' (`spec/29_communication-scenarios.md:1464-1468`)" — the bullet actually runs to :1470 with
its "does not state" cross-reference, but the claim it supports ("carries no validation sentence") is true
and SPEC-2 elsewhere correctly preserves that cross-reference. (3) SPEC-2 files §28.6's "The second opener
on those channels" paragraph under "hold sentences left as they stand"; that paragraph also opens with a
generation sentence at :1679-1681. Neither states a pod-wide fenced value, so leaving it is defensible.
EVIDENCE: pkg/adapter/coordination.go:119-121; spec/29_communication-scenarios.md:1464-1470;
spec/28_communication-channels.md:1679-1681

WATCHOUT: `spec/28` §28.8 is titled "Failure and degradation matrix", so a proposal phrase like "spec/28's
CH-ADAPTEREVENTS degradation row" (§6 non-goals) resolves to the §28.8 matrix row rather than to the
§28.5.2 contract card's Degradation bullet. Both spellings exist in the file; check the §28.8 heading before
calling such a reference wrong. EVIDENCE: spec/28_communication-channels.md:1785, :1810

UNVERIFIED: SPEC-1's staged step 3 text reads "The pod accepts only RPCs whose generation matches the value
it holds for the session the RPC names" and the very next sentence carves out the unset case. Read as
written the "only" and the carve-out are in tension, and a reviewer on the contradiction lens rather than
the citation lens should decide whether the carve-out reads as a scoping refinement or as a self-negation.
I did not report it because it resolves operationally and is a wording call.


### [spec.3.review-client-surface.1]

FACT: the only client-facing surface this change reaches is `schemas/lenny-adapter.proto`. `sdks/`,
`pkg/gateway/openapi/`, `schemas/*.json` (JSONL, runtime-ops-events, messagepart, workspaceplan),
`charts/lenny/crds/`, and `docs/api/` carry no `coordination_generation`, no fence, and no barrier text —
EVIDENCE: `grep -rln "coordination_generation\|fenced generation" sdks/ schemas/*.json pkg/gateway/openapi/
charts/` returns nothing but CRD `metadata.generation` noise. Do not spend a round re-deriving this.

FACT: the proto's seven fence/barrier carriers are the complete set, and every line range the SPEC-2 closing
paragraphs cite is exact — EVIDENCE: `schemas/lenny-adapter.proto:153-162` (RPC), `:165-179` (barrier RPC),
`:1442-1446`, `:1449-1451`, `:1455-1462`, `:1469-1474`, `:1477-1479`. An eighth carrier does not exist:
`grep -n "generation" schemas/lenny-adapter.proto` shows the only other sites are twelve identical
`coordination_generation` field comments and the unrelated `recovery_generation` ones.

FACT: those twelve field comments are uniform and already session-scoped ("the gateway's view of the active
coordination generation **for the session**. A pod validates the generation on every gateway-to-pod RPC and
rejects a stale coordinator's request"), so the spec-changes claim that they "describe the validation
neutrally" and are not edit sites is accurate — EVIDENCE: `schemas/lenny-adapter.proto:969-973`, `:1046-1050`,
`:1172-1178`, `:1618-1622`. Only `:1172-1178` and `:1618-1622` deviate, and only in a trailing clause about
the oneof and about teardown. Reporting them as an unmirrored carrier is a wasted round.

FACT: `tests/claim-map.json:76-81` carries `CheckpointBarrierRequest.coordination_generation` as `UNWIRED`
with the note "no production reader compares it until the generation fence lands", while
`pkg/adapter/coordination.go:236-239` compares it today. That row is already false before this proposal
touches anything, its remedy is a test-lane edit, and `tests/tier0_static/claim_register_proto_agreement_test.go`
does not check wiredness, only that a row exists per fence-carrying message. Out of this loop's scope, and
not made worse by 0076.

WATCHOUT: `spec/28_communication-channels.md:322` (`CH-FENCE` **Preconditions**: "The fence is itself the hard
precondition for every other operational RPC to the pod"), `spec/04_system-components.md:712` ("Precondition
for any subsequent operational RPC"), and `docs/reference/adapter-contract.md:69` read as pod-wide
preconditions that SPEC-1's new unset-value clause appears to contradict. They do not: all three state a
GATEWAY obligation inherited from `spec/10_gateway-internals.md:38` ("the **new coordinator** MUST NOT send
any operational RPC ... until `CoordinatorFence` returns"), while the staged clause states a POD-side gate.
The two are different statements and coexist. This is the same "narrative inherits its frame" ground on
which the §29.8 step-7 and §4.1 findings were refuted in round 2.

DECISION: returned an empty findings list for the client-surface lens — BECAUSE the wire staging is complete
and correctly cited, no parallel client representation is left half-changed, and every candidate I chased
resolved to pre-existing spec-vs-code drift or to a docs/test-lane remedy this loop does not own —
ALTERNATIVES: reporting the twelve generic field comments as unmirrored (rejected: they are unit-neutral and
stay true), reporting `spec/04:712` and the `CH-FENCE` Preconditions bullet (rejected: gateway obligation, not
the pod gate, and pre-existing), reporting the proto's "the gateway should re-read Postgres and re-issue"
sentence at `schemas/lenny-adapter.proto:1457-1458`, which Pass 6 established the `Fencer` does not do
(rejected: pre-existing wire-vs-§10.1.5 drift, the comment is already in SCHEMA-1's carrier list, and the
remedy is a proto edit rather than a staged spec edit).


### [spec.3.review-docs-alignment.1]

FACT: the proposal text did not change this round. `diff -rq` against the r3 snapshot shows only the
review-log differs (a compaction pass), so every non-log file is byte-identical to round 2 — EVIDENCE:
`diff -rq scratchpad/cp-snap/0076-run2/spec-r3 proposals/0076_fix_scope-the-coordination-generation-to-the-session`

FACT: the docs surface really is nearly empty for this change, and I re-derived it rather than trusting the
standing context. `grep -rn "coordination_generation\|coordinator_\|CoordinatorFence\|CheckpointBarrier"
docs/` returns only `docs/getting-started/concepts.md:101`, `docs/reference/metrics.md:307,309`,
`docs/reference/adapter-contract.md:68,69,96`, `docs/reference/glossary.md:54`, and
`docs/operator-guide/upgrades.md:47-49`. No metric, alert, or runbook gains or loses a row: the only
coordinator alert is `CoordinatorHandoffSlow` (`spec/16_observability.md:552`), and `coordinator_connection_lost`
is an adapter structured log rather than a §16.6 operational event — EVIDENCE: `spec/16_observability.md:654-660`.

FACT: `docs/operator-guide/upgrades.md:49` is the one narrative operator page for the drain barrier ("This
ensures workspace state is checkpointed before coordinator handoff"). It is the docs half of the finding
below and belongs to the non-spec loop, because this loop may only report fixes that land in staged spec text.

DECISION: reported one finding, the barrier-refusal narrative — BECAUSE SPEC-1's staged step 3 clause newly
states that `CheckpointBarrier` is refused for a session the pod holds no fenced generation for, which §7
records as the ordinary case, while `spec/10_gateway-internals.md:183` calls barrier rejection a
false-positive that "is safe and does not require special handling" and `:198` derives the rolling-update
bound from step-2 quiescence happening for every barriered session, and `spec/29_communication-scenarios.md:1150-1152`
enumerates the same false-positive as the only rejection outcome. Neither section is in any edit list —
ALTERNATIVES: raising the same collision at `spec/28`'s CH-BARRIER Preconditions bullet ("The generation stamp
and the fence acknowledgement that govern every gateway-to-pod RPC", `spec/28_communication-channels.md:354-357`),
rejected as the same defect at a weaker site that would dilute one verification.

WATCHOUT: the two fence drivers are the resume path and the sweeper's crash-takeover re-adopt, and nothing
fences a session that starts normally, so "the pod holds no fenced generation" is the ordinary state rather
than an edge — EVIDENCE: `grep -rn "\.Fence(" pkg/ cmd/ --include=*.go` outside tests returns
`pkg/gateway/sessionserver/start.go:4237` and `cmd/lenny-gateway/coordination_seams.go:233` alone.

WATCHOUT: the barrier refusal for a never-fenced session is CURRENT code behavior
(`pkg/adapter/coordination.go:236-239`, `!initialized || gen != fenced`), so a skeptic will call the finding
pre-existing. It is not: the applied spec today states no initial condition at all, so §10.1.8 and §29.7 read
consistently with §10.1.2 step 3. SPEC-1 is what writes the refusal into the spec, and that is what puts two
sections in conflict. Frame it that way or it gets refuted.

UNVERIFIED: whether §10.1.8 step 1's `coordination_generation` for a never-fenced session is sent as 0 (the
`NOT NULL DEFAULT 0` row) and therefore refused with `InvalidArgument` rather than `FailedPrecondition`
(`pkg/adapter/coordination.go:224-226`, `migrations/0050_session_record_fields.up.sql:38-39`,
`pkg/gateway/coordination/coordfence/coordfence.go:147-153` floors a zero row only on the fence path). The
refusal stands either way, but the error code the staged text implies may be wrong. A code-lane reviewer
should chase it.


### [spec.3.review-edit-sites.1]

FACT: the drain barrier has four spec homes besides §10.1.2, and none is in any 0076 edit list:
§10.1.8 (the CheckpointBarrier protocol, steps 1-3 and the closing guarantee), the §29.7 gateway-drain
trace (scope paragraph plus steps 4-6), the §28.5.1 `CH-BARRIER` card, and the §28.8 `CH-BARRIER` row.
Each enumerates barrier refusal as the superseded-replica case alone — EVIDENCE:
`spec/10_gateway-internals.md:183` ("Pods receiving a barrier for a session no longer coordinated by this
replica ... reject the barrier as a generation-stale RPC ... this is safe and does not require special
handling"); `spec/29_communication-scenarios.md:1150-1152`; `spec/28_communication-channels.md:349-365`;
`spec/28_communication-channels.md:1808` col 4 ("a barrier from a superseded replica is rejected on the
stamp").

WATCHOUT: SPEC-1's staged step-3 sentence is the first place `spec/` will state that the barrier is refused
for a session the pod holds no fenced generation for. Because the ordinary session is never fenced (only
the resume path and the sweeper's re-adopt drive `Fencer`), that sentence lands on the common case and the
four sites above become incomplete. Anyone rewording step 3 must decide whether to scope the clause to a
handed-off session or to stage §10.1.8 — EVIDENCE:
`proposals/0076_.../0076_....spec-changes.md:110-112`, `:535-538`; `pkg/adapter/coordination.go:236-239`.

FACT: `GetCoordinationGeneration()` is read at exactly two sites in `pkg/adapter`, the fence path and the
barrier path, so "the barrier is the one RPC refused for such a session" is accurate about the shipped tree
— EVIDENCE: `pkg/adapter/coordination.go:92`, `:223`.

FACT: the `coordinator_connection_lost` event has exactly two spec carriers, both staged, and the
`coordinator_generation_gap` reset has exactly four, all staged. `docs/`, `spec/16`, `charts/`, and
`pkg/alerting/rules` need no edit: the only coordinator alert is `CoordinatorHandoffSlow` and
`charts/lenny/files/alerting-rules.yaml` is generated from `pkg/alerting/rules` — EVIDENCE:
`spec/10_gateway-internals.md:60`, `spec/29_communication-scenarios.md:1274`;
`spec/28_communication-channels.md:333-335`, `:1807`, `spec/29_communication-scenarios.md:1309-1311`;
`charts/lenny/files/alerting-rules.yaml:1-8`.

USEFUL [standing context, "The §10.1.2 gap reset is mirrored in several places"]: the mirror inventory it
carries is complete for the gap reset and for `coordinator_connection_lost`; I re-derived both and found no
site it misses. What it does not cover is the barrier's own homes, which is where the new step-3 clause
lands.

OPEN: `spec/29` §29.2 step 11 records as unstated "whether that replica announces a generation on
`CH-FENCE` before its first gateway-to-pod message for a pod it has just claimed"
(`spec/29_communication-scenarios.md:204-206`). D6's unset-value model and §7's first open decision both
turn on the answer being "it does not". Nothing staged reconciles the two; a later round or the human
reviewer should decide whether SPEC-1 owes that bullet a change.


### [spec.3.review-feasibility.1]

FACT: only the review-log changed since the r3 snapshot (compaction pass 2). The staged spec text is byte-identical to round 2, so `diff -rq scratchpad/cp-snap/0076-run2/spec-r3 proposals/0076_.../` is the fastest way to see there is nothing new to read — EVIDENCE: `diff -rq` output names the review-log alone.

FACT: the drain barrier is refused for a session no coordinator ever fenced, and a test pins it. `if !initialized || gen != fenced` refuses, and `TestCheckpointBarrierRejects...` asserts "expected FailedPrecondition without fence" — EVIDENCE: `pkg/adapter/coordination.go:236-239`, `pkg/adapter/coordination_test.go:195`.

FACT: §10.1.8 nowhere allows for a barrier refused on a correctly-coordinated session. Step 1's only rejection sentence attributes it to "a false-positive surviving the cache fallback", and the target set is every session this replica coordinates — EVIDENCE: `spec/10_gateway-internals.md:183`, `:184`. This is what the pass-4 correction's surviving refusal clause collides with, and it is my one finding.

FACT: the four gateway-to-adapter request messages that carry no session field are `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, and `AdapterEventsRequest`, and none of them carries a `coordination_generation` field — EVIDENCE: `spec/04_system-components.md:188`; `grep -n coordination_generation schemas/lenny-adapter.proto` maps every occurrence to a session-scoped message or to the fence.

DECISION: did NOT report step 3's per-session acceptance rule as unfulfillable for session-less pod-scoped RPCs — BECAUSE spec/28 already records the same gap as unstated ("does not state how that rule applies to a probe against a pod that is not yet serving a coordinated session", `spec/28_communication-channels.md:389-392`), the four session-less messages carry no generation stamp, and step 3's domain sentence is unchanged pre-existing text — ALTERNATIVES: reporting it as an actor-action defect, rejected as pre-existing looseness a verifier would refute.

DECISION: did NOT report the staged §29.10 hold bullet's "rejects every inbound RPC on the pod other than `CoordinatorFence`" against the shipped five-method allowlist (`pkg/adapter/holdstate.go:53-59`, which also admits `NegotiateVersion`, `AdapterEvents`, and the two health methods) — BECAUSE the staged sentence copies standing §10.1.4 text (`spec/10_gateway-internals.md:57`) and the mismatch is a pre-existing code-vs-spec gap whose fix is not in the staged spec edits.

WATCHOUT: the fail-open reading of the unset clause ("the pod does not reject that session's RPCs on generation grounds") looks like it weakens §10.1.1's split-brain claim (`spec/10_gateway-internals.md:30`), but it does not: §10.1.2 step 2 bars a taker-over from sending any operational RPC before its fence acknowledges, so only the original coordinator ever sends unfenced, and there is exactly one of those. Chased and clean; do not spend a round on it.

UNVERIFIED: whether the shipped drain therefore never quiesces for an ordinary never-handed-off session, which is what the code implies. Nobody has run a tier-4/5 drain against a session that was never fenced. The implementation loop should confirm before treating the §10.1.8 reconciliation as documentation-only.


### [spec.3.review-mechanism.1]

FACT: the fence-before-any-operational-RPC precondition is stated in SEVEN places, and SPEC-1/SPEC-2
stage none of them. `spec/10_gateway-internals.md:34` ("it must execute the following sequence before
sending any RPCs to the pod"), `:38` ("MUST NOT send any operational RPC to the pod until
`CoordinatorFence` returns a successful acknowledgement"), `spec/28_communication-channels.md:237-240`
(CH-ATTACH Preconditions), `:318-322` (CH-FENCE Preconditions, "The fence is itself the hard
precondition for every other operational RPC to the pod"), `:1673-1675` (§28.6 "One holder per
session" — SPEC-2 stages only the SECOND half of this sentence), `:1805` and `:1806` (the §28.8
CH-ATTACH and CH-CHECKPOINT "Holder ... changes" cells), `spec/29_communication-scenarios.md:1322-1326`
(§29.8 step 9), and `spec/04_system-components.md:712` (the `CoordinatorFence` table row, "Precondition
for any subsequent operational RPC"). Any staged text about what a pod does for a session that has
never been fenced has to be reconciled against this set.

WATCHOUT: pass 4's correction (spec-changes.md:433-450) flipped step 3's clause from "refuse every
operational RPC for an unfenced session" to "serve them". The first direction was refuted; the second
direction collides with the precondition set above. The clause's own rationale
(spec-changes.md:132-135) checks it against only two neighbours, §10.1's summary bullet and SPEC-2's
§28.5.1 Messages wording, and both of those are about STALENESS rather than about the precondition, so
the collision is unexamined in the document. — EVIDENCE:
proposals/0076_.../0076_....spec-changes.md:108-116, :132-135

FACT: `spec/29_communication-scenarios.md:204-210` (§29.4 step 11, `unstated`) records that the
specification does not state when the replica CREATING a session acquires `REG-COORDLEASE`, nor
whether it fences before its first gateway-to-pod message, and explicitly notes that §10.1 states the
acquire-increment-fence sequence "without a section stating the initial acquisition at session
creation". This is the standing residue that the never-fenced session actually sits in. A staged
sentence that PERMITS traffic for such a session answers that open question in one direction; a
sentence that records the residue does not.

FACT: the drain barrier's target set carries `sessions.coordination_generation` verbatim
(`pkg/gateway/coordination/coordination/coordination.go:430`, `:544-556` upsert; consumed at
`pkg/gateway/coordination/barrier/wiring.go:108-114`). For a session that never handed off that value
is 0, so the adapter rejects the barrier at the positive-generation guard
(`pkg/adapter/coordination.go:224-226`) BEFORE the generation gate at `:236-239` is even reached. The
proposal's §7 open decision 1 attributes the refusal to the equality gate alone; the shipped refusal
for the ordinary session is the positive-generation guard. Nobody has re-derived this; a later round
deciding the barrier operator should know the operator is not what refuses today.

DECISION: did NOT file the §10.1.8 drain-barrier consequence (staged step 3 says the barrier is the
one RPC refused for an unfenced session; §10.1.8:184 has the adapter quiesce on receipt, :183 calls a
barrier rejection "safe and does not require special handling" for false positives alone, and :198
asserts the interruption bound for every session in the barrier-target set) — BECAUSE §7's first open
decision explicitly reserves the barrier operator for the human reviewer and records this exact
consequence, so the item is carved out of findings; if the reviewer loosens the operator the
consequence evaporates — ALTERNATIVES: filing it as an unstaged-site finding on §10.1.8, rejected on
the open-decision carve-out.

OPEN: if the reviewer keeps equality on the barrier gate, §10.1.8 becomes an edit site: its "this is
safe and does not require special handling" sentence (`spec/10_gateway-internals.md:183`) is scoped to
false positives and does not cover an ordinary never-fenced session whose drain then runs unquiesced,
which is the defect the proposal's own summary claims to fix.

WATCHOUT: `DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, and
`AdapterEventsRequest` are gateway-to-pod and carry NO session field
(`spec/04_system-components.md:188`), so the staged "matches the value it holds for the session the RPC
names" has no session to key on for them. This is NOT worth filing: none of the four carries a
`coordination_generation` field either (`grep -n coordination_generation schemas/lenny-adapter.proto`
lists fourteen sites, none of them in those messages), so the acceptance rule already did not reach
them before the edit. Chased and dropped.


### [spec.3.review-operational.1]

DECISION: Returned an empty findings list for the operational-consistency lens — BECAUSE every observability
surface the staged edits touch is either already staged or provably untouched, and the two candidates that
survived a first pass both collapsed on verification (details below) — ALTERNATIVES: reporting the §16.4
"session_id in every log line" tension against the pod-level `coordinator_connection_lost` event, rejected
because the shipped event already carries no session_id (`pkg/adapter/holdstate.go:130-132` logs only
`started_sessions` and `last_generation`), so SPEC-1 codifies existing behavior rather than creating the
tension; and reporting that §10.1.8's barrier protocol has no disposition for a generation-refused barrier,
rejected because §16.1 already carries an `error` value in the `lenny_checkpoint_barrier_ack_total{outcome}`
domain (`spec/16_observability.md:41`) and because §7's first open decision explicitly reserves that case for
the human reviewer.

FACT: the whole observability surface this change can reach is four sites and none needs an edit. The gauge
row `lenny_adapter_coordinator_hold` is unlabeled and stays pod-scoped under D5
(`spec/16_observability.md:185`, `docs/reference/metrics.md:309`); `lenny_coordinator_handoff_stale_total`
is defined as "increments when a replica receives a generation-stale rejection"
(`spec/16_observability.md:183`), which is unit-neutral; §16.6's Operational Events Catalog lists no
adapter-side structured log event, so `coordinator_connection_lost`, `coordinator_lost`, and
`coordinator_generation_gap` are in no inventory; and the only coordinator alert, `CoordinatorHandoffSlow`
(`pkg/alerting/rules/rules.go:1582`, `docs/runbooks/coordinator-handoff-slow.md`), is about delegation
handoff latency and is unrelated to `coordination_generation`.

FACT: `coordinator_connection_lost` has exactly two spec sites and both are staged —
`spec/10_gateway-internals.md:60` (SPEC-1) and `spec/29_communication-scenarios.md:1274` (SPEC-2). No
`spec/28` card or §28.8 cell names it. Do not re-derive this inventory; `grep -rn "coordinator_connection_lost" spec/`
returns those two lines and nothing else.

FACT: SPEC-1's §10.1.4 artifact names are accurate against the tree. The per-session log line's message is
literally `coordinator_lost` (`pkg/adapter/holdstate.go:226`, `slog.Warn(reasonCoordinatorLost, ...)`), the
post-mortem JSON carries `lastGeneration` (`:287-296`), and `AdapterTerminating`'s field list stays
`session_id`/`reason` (`spec/04_system-components.md:747`), which SPEC-1 explicitly avoids extending.

FACT: SPEC-1's three "zero names no fence" gates all check out as cited — `NOT NULL DEFAULT 0` with
`CHECK (coordination_generation >= 0)` at `migrations/0050_session_record_fields.up.sql:38-39`, the
gateway's floor of a zero row at 1 at `pkg/gateway/coordination/coordfence/coordfence.go:147-153`, and the
adapter's `gen <= 0` refusal at `pkg/adapter/coordination.go:93-94`. Chased once and clean.

WATCHOUT: §6's phrase "`spec/28`'s CH-ADAPTEREVENTS degradation row" reads like a false citation, because
the sentence it names lives in the card's **Exclusivity** bullet (`spec/28_communication-channels.md:476-479`)
rather than its Degradation bullet. It is not false: §28.8 is titled "Failure and degradation matrix"
(`spec/28_communication-channels.md:1785`) and its CH-ADAPTEREVENTS row carries the same sentence
(`:1810`). Do not report it — EVIDENCE: spec/28_communication-channels.md:1785, :1810

WATCHOUT: `spec/29_communication-scenarios.md:204-210` (§29.2 step 11) records as unstated whether the
session-creating replica fences before its first gateway-to-pod message. It does NOT contradict §7's first
open decision or D6's "unset is the ordinary case": that decision argues from the two code call sites, and
an unstated spec is consistent with either answer. A finding built on this pairing will be refuted.

FACT: the barrier's refusal for a never-fenced session is shipped behavior, not something SPEC-1 introduces —
`pkg/adapter/coordination.go:230-233` is `if !initialized || gen != fenced { return FailedPrecondition
"coordinator_handoff_stale" }`, and `initialized` is pod-wide today so a warm pod that never handed off
refuses every drain barrier already.

FACT: `gateway-runtime-comms.md` at the repo root is not an edit site. Its first three lines declare it a
point-in-time reading of the tree at `fcda83e3`, superseded by spec §28 and §29 and "not maintained", so its
`coordinator_generation_gap` and per-pod fence prose at `:415-440` is a historical record.

USEFUL [Standing context, "The `docs/` and §16 surface is nearly empty"]: correct and complete. I re-derived
it independently and found the same three `docs/` mentions and the same §16 rows, plus the two items in the
first FACT above. Promote it; it saved a round and it is still true after round 2's edits.


### [spec.3.review-reliability.1]

FACT: §10.1.2's fence sequence is triggered "When a replica acquires the coordination lease" and step 2 sends the fence "to the pod". For a session created fresh, the lease is acquired before any pod exists, so the applied specification never requires a normally-started session's pod to be fenced either. The "no fenced generation" state D6 introduces is therefore the ordinary case in the spec as well as in the tree, which is what makes SPEC-1's step 3 unset-value clause load-bearing rather than an edge — EVIDENCE: spec/10_gateway-internals.md:34, :38; pkg/gateway/sessionserver/start.go:4237 and cmd/lenny-gateway/coordination_seams.go:233 are the only two Fence senders.

FACT: §10.1.8 has exactly one failure path for a barrier, and it is keyed to the ack deadline, not to a synchronous refusal. Rules 1-5 of the BarrierAck-timeout partial-capture path open with "Pods that do not ack within `checkpointBarrierAckTimeoutSeconds`". The only rejection §10.1.8 contemplates is the cache-fallback false positive, which it calls "safe and does not require special handling" because that session has a live coordinator elsewhere — EVIDENCE: spec/10_gateway-internals.md:183, :187, :198.

DECISION: filed one finding, the drain-barrier one — BECAUSE it is the only place where a sentence this loop staged makes a recovery mechanism in an unstaged section wrong under the exact failure that section exists to handle — ALTERNATIVES: (a) "the unset-value clause turns the pod-side generation gate fail-open and breaks §10.1.1's split-brain claim" — dropped after tracing it: any *competing* coordinator must fence before it sends anything, and fencing records the value and re-arms the gate, so the clause never admits two senders at once; the harm is not derivable. (b) "D6's justification sentence, that a never-fenced session 'has accumulated no state for the gap path's reset to act on', is false for a bound long-running session" — dropped: the sentence is a bad justification for a conclusion that is right, since running clause (b)'s reset on a healthy session's first fence would destroy its live state. (c) "spec/07 §7.2 step 4's mid-resume generation bump relies on the pod rejecting a lower generation, which the unset-value clause disables on an unfenced replacement pod" — dropped as too contingent: a stale coordinator that fences installs its own value and the guard is already weak there today.

WATCHOUT: the staged step 3 clause is the newest text in the document and it was written twice. Pass 4 first staged a refusal of every operational RPC, then corrected it to refuse the barrier alone. The correction fixed the over-broad half and left the barrier half pointing at a section nobody opened — EVIDENCE: 0076_...spec-changes.md:433-450, :108-113.

FACT: §10.1.8 is named nowhere in this proposal as an edit site. Its only appearance is inside a "Resolved in adversarial review" pass-6 entry about where the manifest write lives — EVIDENCE: 0076_...spec-changes.md:514.

FACT: no pod-scoped request message carries `coordination_generation`. The field appears on SendMessage, Attach, RotateCredentials, ExtendCredentialLease, RevokeCredentials, Interrupt, Checkpoint, SignalDeadline, Resume, ExportPaths, ReportUsage, Shutdown, CoordinatorFence, and CheckpointBarrier requests, and §4.1 classifies every one of those except `CoordinatorFenceRequest` as session-scoped. So SPEC-1's "the session the RPC names" phrasing leaves no gateway-to-pod RPC without an address to validate against, and chasing that is wasted effort — EVIDENCE: schemas/lenny-adapter.proto:969,995,1046,1070,1091,1114,1172,1305,1393,1449,1477,1531,1576,1618; spec/04_system-components.md:155-179.

USEFUL [spec.2 standing context]: the "§10.1.2 step 3 fixes equality as the operational-RPC gate" bullet and the "Fencing a session the pod has not bound is not a deadlock, and `CoordinatorFence` has two senders" bullet together are what let this pass go straight to the drain consequence instead of re-deriving the fence-sender set.


### [spec.3.review-security.1]

FACT: the proposal text is byte-identical to the r3 snapshot; only the review log changed between rounds
(`diff -rq scratchpad/cp-snap/0076-run2/spec-r3 proposals/0076_.../`). The "newest fix-stage text" this
round is still pass 4's step 3 rewrite and pass 5/6's §10.1.4 clauses.

FACT: `spec/10` §10.1.8 and `spec/29` §29.7 both characterise a rejected `CheckpointBarrier` as meaning the
replica no longer coordinates the session, and §10.1.8's closing paragraph states the one-in-flight-tool-call
bound that quiescence delivers — EVIDENCE: `spec/10_gateway-internals.md:183` ("Pods receiving a barrier for
a session no longer coordinated by this replica ... this is safe and does not require special handling"),
`:198` ("This protocol bounds the rolling-update interruption window to at most one in-flight tool call per
session ... the step-2 quiescence stops new tool-call dispatch"), `spec/29_communication-scenarios.md:1150-1152`
("A barrier addressed to a session this replica no longer coordinates is rejected by the pod as a
generation-stale RPC ... Those outcomes are named here and are not traced"). Neither section is in SPEC-1's
or SPEC-2's edit list. This is the one finding I return.

DECISION: I did NOT report the step 3 unset-value carve-out as a weakening of the split-brain gate, even
though `spec/10_gateway-internals.md:30`, `spec/28_communication-channels.md:237`, `:1679-1680`, and ten
proto field comments all state universal validation — BECAUSE every reachable state stays covered: a replica
that has *lost* coordination lost it to a successor that fenced, which records a value, so the rejection the
mirrors promise is in force; the only uncovered window is the pre-fence window §10.1.2 step 2 already
blesses ("Until the pod acknowledges the fence, the pod still accepts RPCs carrying the previous
generation") — ALTERNATIVES: filing it against §28.6's "second opener" paragraph, which SPEC-2 excludes on
the mistaken ground that the paragraph "states the hold" when its first sentence is the generation-stamp
rejection; rejected because the sentence stays true in every reachable state.

DECISION: I did NOT report the D6 exemption as a hold-exit bypass (a stale replica fencing a never-fenced
co-tenant at a low generation exits the pod-wide hold, where today the pod-wide value would reject it) —
BECAUSE `coordfence.Fencer` re-reads the authoritative generation from Postgres at fence time
(`pkg/gateway/coordination/coordfence/coordfence.go:143-153`), so no sender can produce a stale low value,
and the accepted-fence case is exactly the legitimate co-tenant handoff this proposal exists to unblock.

FACT: `spec/04` §4.7's RPC table states `CoordinatorFence` and `CheckpointBarrier` without restating the
record-and-reject rule or the acceptance predicate, so §4.7 is not a mirror site despite §28's cards citing
it alongside §10.1 — EVIDENCE: `spec/04_system-components.md:711-712`. Chased once; do not re-chase.

FACT: outside `spec/10`, `spec/28`, and `spec/29`, the only occurrences of the fence vocabulary anywhere in
`spec/` or `docs/` are the `lenny_coordinator_handoff_stale_total` rows, which are neutral — EVIDENCE:
`spec/16_observability.md:183`, `docs/reference/metrics.md:307`. The mirror inventory is closed.

FACT: the three citations SPEC-1 uses to argue that no fence carries zero all check out — the column is
`coordination_generation BIGINT NOT NULL DEFAULT 0` at `migrations/0050_session_record_fields.up.sql:38-39`,
the floor is `if gen <= 0 { gen = 1 }` at `pkg/gateway/coordination/coordfence/coordfence.go:147-153`, and
the adapter refuses non-positive at `pkg/adapter/coordination.go:92-94`. `spec/04_system-components.md:747`
does fix `AdapterTerminating`'s fields as `session_id` and `reason`.

FACT: the hold-state allowlist admits `NegotiateVersion`, health checks, and `AdapterEvents` besides
`CoordinatorFence` (`pkg/adapter/holdstate.go:325-348`), so §29.10's staged "rejects every inbound RPC on
the pod other than `CoordinatorFence`" restates a spec/code divergence that predates this proposal
(`spec/10_gateway-internals.md:57`, `spec/28_communication-channels.md:1805`). Not a finding here; it is
copied text, not new text.


### [spec.4.followup-fix.1] · 2026-08-31 · fix · the stamp rule's two unstaged contradictions

DECISION: Staged §10.1.8 step 1's stamp sentence under SPEC-1 and §29.7's trace step 4 under SPEC-2, each
naming the coordinating replica's generation stamp for the session as §10.1 states it, and narrowed the
non-site list to §28.5.1's `CH-BARRIER` Messages bullet alone — BECAUSE the new §10.1 sentence has the
replica stamp 1 for a session no replica has taken over while both of those sentences state that the message
carries the row's current `coordination_generation`, which for that session is 0, so applied as staged the
specification named two values for one wire field for exactly the session class the rule governs. The
non-site rationale, that restating the floor would recreate the drift, did not reach the objection: each
sentence's own definite statement of the value is what becomes false. ALTERNATIVES: (a) leave both as
non-sites, rejected because an implementor reading either would read CODE-4's floor as a violation and D7's
acceptance would sit behind the adapter's non-positive-generation guard again; (b) restate the floor at each
site, rejected because three statements of one rule is the drift this staging removes.

FACT: §28.5.1's `CH-BARRIER` Messages bullet reads "`CheckpointBarrier` carries `coordination_generation` and
`barrier_id`", naming no value, and its Preconditions bullet points at §10.1 for the stamp — EVIDENCE:
spec/28_communication-channels.md:349, :354-357. Its non-site call stands and needs no re-derivation.

FACT: the two sentences that do name a value are `spec/10_gateway-internals.md:183` ("carries the current
`coordination_generation` and a `barrier_id` (monotonically increasing per session)") and
`spec/29_communication-scenarios.md:1186` ("carrying the session's current `coordination_generation`"). The
value that counter holds for a never-taken-over session is 0 — EVIDENCE:
migrations/0050_session_record_fields.up.sql:38-39. The guard the stamp must clear is
pkg/adapter/coordination.go:224-226.

WATCHOUT: `spec/29_communication-scenarios.md` is already on the files-touched list pass 7 records, so §29.7
step 4 adds no file entry. A future round adding a third statement of the barrier's carried value should
point at §10.1 rather than name a number.


### [spec.4.fix-G1.1]

DECISION: Deleted the case analysis from the staged §10.1.8 step 1 replacement and its §29.7 mirror rather than widening it, leaving staged §10.1.2 step 3 as the sole statement of the acceptance predicate and having both mirrors apply it by reference — BECAUSE a predicate stated at three sites drifts, and this framing was worse than under-specified: it was anti-correlated with the outcome. In the ordinary false positive the draining replica's barrier carries the generation the acquiring replica's CAS wrote (the target's stamp is read from the lease mirror or live from the session row) while the pod still holds the value the draining replica fenced, so the fencing rules refuse that barrier BEFORE the successor's fence lands and accept it after — ALTERNATIVES: the finding's own three-case enumeration at both mirrors, rejected because §29.7 carries strictly less detail than §10.1.8 so "the same wording" cannot hold for a three-item list; adding the equality arm as a third case to the successor-fence framing, rejected because the axis stays false; deleting §10.1.8's sentence outright, rejected because it tells the drain reader the false positive needs no special handling, which §10.1.2 does not carry.

DECISION: Closed the reachability finding on the SENDER, staging one sentence into §10.1's "Generation counters" bullet (a session no replica has taken over carries generation 0 on its row; the coordinating replica stamps 1 on its gateway-to-pod messages for such a session) and recording CODE-4 as the gateway-side half — BECAUSE the two senders of one session's stamp disagree today: the fence path floors a non-positive row value at 1 (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`) and the barrier path copies the row's 0 to the wire — ALTERNATIVES: moving the adapter's `InvalidArgument` guard behind the unset test, rejected because it leaves the two senders divergent and breaks the resumed session (resume fences at 1 without bumping the row, so its barrier at 0 would be refused on 0 != 1); baselining the session row at 1 in the migrations and deleting coordfence's floor, which is the smallest end state and the only option that deletes a mechanism, rejected on scope (it opens §4.2's counter semantics, adds a migration deliverable, and makes the shipped floor dead code) and recorded here so a later round neither re-derives it nor half-adopts it; flooring in `PodDispatcher.Send`, rejected because the checkpoint-meta record would then store 0 while the wire carried 1; flooring at each of the two Target producers, rejected as the same duplication that caused the divergence.

FACT: the adapter refuses a non-positive `coordination_generation` with `InvalidArgument` three statements BEFORE the barrier's generation gate, so a barrier carrying 0 never reaches the gate any reasoning about `!initialized` concerns — EVIDENCE: `pkg/adapter/coordination.go:224-226` (guard), `:236-239` (gate).

FACT: an ordinary session's `sessions.coordination_generation` is still 0 at drain time. `fenceResumedPod` calls `Fencer.Fence` and nothing else, so the resume path fences the pod at coordfence's floored 1 while leaving the row at 0; only the sweeper's `RecordHandoff` increments it — EVIDENCE: `pkg/gateway/sessionserver/start.go:4233-4245`; `migrations/0050_session_record_fields.up.sql:38-39`; `pkg/gateway/coordination/coordination/coordination.go:430`.

FACT: the barrier's stamp reaches the wire unchanged from either producer. `MirrorTargetLister.Targets` copies the lease mirror's column (`pkg/gateway/coordination/barrier/wiring.go:112`), the cache fallback reads the session row live (`cmd/lenny-gateway/httpsurface.go:592-599`), and `PodDispatcher.Send` passes `t.CoordinationGeneration` (`wiring.go:49`). `Coordinator.dispatchOne` (`pkg/gateway/coordination/barrier/barrier.go:207`) is the single boundary both producers pass through, and it is also where the checkpoint-meta record takes the value (`:241`), which is why the floor belongs there and not in `Send`.

WATCHOUT: a refused barrier costs the same whether the refusal is `InvalidArgument` or `FailedPrecondition`. Only `FailedPrecondition` maps to `ErrGenerationStale`, so the outcome records an error rather than staleness, but prestop branches on the acknowledgement alone and captures the session a second time either way — EVIDENCE: `pkg/gateway/coordination/barrier/wiring.go:51-53`; `pkg/gateway/podlifecycle/prestop/prestop.go:395`, `:510`.

WATCHOUT: D7 and the stamp rule are not substitutes. With the stamp floored to 1 the pod still holds no value for a never-fenced session, so the pre-D7 `!initialized` arm refuses it; with only D7 the barrier never reaches the gate. A later round that drops either leaves the ordinary session's drain barrier refused.

MISTAKE: round 3 staged the §10.1.8 and §29.7 mirrors in terms of whose fence had landed, and grounded D7 on the generation gate alone. Cost: two findings in one round, both on text that had just been written. The lesson is that a mirror should apply the owning section's predicate by reference rather than re-derive it, and that a claim about what refuses an RPC today must be read against every guard that precedes the one being changed.

USEFUL [standing context, "§10.1.2 step 3 fixes equality as the operational-RPC gate"]: it is what kept both fixes off the operator. Changing the compared value's unit and making the compared value well-formed leaves §7 open decision 1 genuinely open.

OPEN: CODE-4, its §8 tier-1 case, the files-touched entries for `pkg/gateway/coordination/barrier/barrier.go`, and CODE-4's checklist step all live in files a spec loop may not write. Pass 8 of the spec changes carries the full statement and the summary's watch-out paragraph indexes it, for whichever loop may write the non-spec-changes and checklist files.

OPEN: whether the session row's `coordination_generation` should be baselined at 1 instead of 0, which would delete both floors and the §10.1 stamp sentence. It deserves its own proposal; the human reviewer may prefer to absorb it here.


### [spec.4.fix-design-G1.1] · 2026-08-31 · fix-design · G1 (barrier acceptance for a session with no fenced generation)

DECISION: Closed both findings with ONE principle applied twice — state each rule exactly once, at the
section that owns it, and have every mirror point at it rather than restate it. (1) The acceptance predicate
lives only in staged §10.1.2 step 3; staged §10.1.8 step 1 and SPEC-2's §29.7 bullet lose their case analysis
and become a single sentence each ("the pod rejects the barrier when it holds a generation for that session
the barrier does not carry, and otherwise accepts it"). (2) The stamp rule lives only in §10.1's "Generation
counters" bullet (`spec/10_gateway-internals.md:30`), which gains one sentence: a session no replica has taken
over carries generation 0 on its row and the coordinating replica stamps 1 on its gateway-to-pod messages for
it, so every generation a pod validates is positive — BECAUSE the "until the acquiring replica's fence is
recorded" framing is not merely a two-case statement over a three-case predicate, it is anti-correlated with
the outcome in the ordinary false positive: the cache-fallback target path reads the session row live
(`cmd/lenny-gateway/httpsurface.go:592-598`), so after a handoff CAS the barrier carries the NEW generation
while the pod still holds the OLD one, which the fencing rules refuse before the successor's fence and accept
after it, the exact inverse of what the staged pair says — ALTERNATIVES: (a) write the finding's own
three-case enumeration into §10.1.8 and §29.7, rejected because it puts the full predicate at two mirror sites
that must then track step 3 forever, and §29.7 carries strictly less detail than §10.1.8 so the "same wording"
tie the proposal asserts cannot hold for a three-item list; (b) move the adapter's non-positive-generation
`InvalidArgument` guard behind the unset test (the finding's option (i)), rejected — see WATCHOUT below;
(c) make the session row's counter start at 1 (`migrations/0050` and `migrations/0164` defaults) and delete
coordfence's floor, which is the smallest end state and deletes the mechanism rather than adding one, rejected
here as opening §4.2's counter semantics and a migration deliverable in a proposal whose lane is spec + proto
comments + adapter code — worth raising as its own proposal; (d) floor in `PodDispatcher.Send` rather than in
`dispatchOne`, rejected because the checkpoint-meta record at `barrier.go:241` would then store 0 while the
wire carried 1.

FACT: the barrier's generation stamp and the fence's generation stamp for the SAME session diverge today.
`coordfence` floors a non-positive read to 1 before fencing (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`);
the barrier path applies no floor and copies the session row's counter, default 0
(`migrations/0050_session_record_fields.up.sql:38-39`), through the mirror
(`pkg/gateway/coordination/coordination/coordination.go:430`, no floor) or the cache fallback
(`cmd/lenny-gateway/httpsurface.go:592-598`) to the wire (`pkg/gateway/coordination/barrier/wiring.go:49`).
That divergence, not the generation gate, is what refuses the ordinary never-handed-off session's drain
barrier today: `pkg/adapter/coordination.go:222-226` returns `InvalidArgument` on `gen <= 0` three statements
before the gate at `:236-239`.

FACT: an `InvalidArgument` barrier refusal has the same downstream consequence as a `FailedPrecondition` one,
so D7's consequence chain survives the correction of its grounds unchanged. Only `FailedPrecondition` maps to
`ErrGenerationStale` (`pkg/gateway/coordination/barrier/wiring.go:51-53`), so `InvalidArgument` lands in
`out.Err` rather than `out.Stale`, but `prestop` branches only on `Acked`
(`pkg/gateway/podlifecycle/prestop/prestop.go:395`, `:510`), so both refusals push the session into the
Stage-2 eviction checkpoint that captures it a second time.

WATCHOUT: do not close the reachability finding by relaxing the adapter's non-positive-generation guard. The
resume path fences WITHOUT bumping the row — `fenceResumedPod` calls `Fencer.Fence` and nothing else
(`pkg/gateway/sessionserver/start.go:4233-4245`) — so a resumed session can sit at row generation 0 with the
pod fenced at 1 by coordfence's floor. Relaxing the guard would then have that session's drain barrier carry 0
and be refused on `0 != 1` by the very gate D7 narrows. It moves the defect from one session class to another
rather than closing it — EVIDENCE: `pkg/gateway/coordination/coordfence/coordfence.go:147-153`;
`pkg/gateway/sessionserver/start.go:4237`.

FACT: the floor does NOT substitute for D7. With the stamp floored to 1, a never-fenced session's barrier
still meets a pod entry holding no value, so `initialized` is false and the pre-D7 gate refuses it. Both
halves are required and neither closes the finding alone — EVIDENCE: `pkg/adapter/coordination.go:236-239`.

FACT: `spec/28`'s `CH-BARRIER` Messages bullet says only that the barrier "carries `coordination_generation`
and `barrier_id`" and points at §10.1 in its Preconditions bullet, so it states no baseline and needs no edit
under the stamp rule. The only other mirror of the stamp sentence is §10.1.8 step 1 itself — EVIDENCE:
`spec/28_communication-channels.md:349`, `:354-357`; `spec/10_gateway-internals.md:183`.

FACT: nothing compares `sessioncheckpointmeta.Record.CoordinationGeneration`; it is written and read back as a
record field only, so flooring the stamp before that write is harmless — EVIDENCE:
`pkg/gateway/session/sessioncheckpointmeta/pgstore/pgstore.go:61`, `:79`.

MISTAKE: round 3 staged D7 on the belief that the shipped gate is what refuses the ordinary session's drain
barrier (`spec-changes.md:54-58`). The gate is never reached for that session class. The cost is a correction
to D7's grounds, to SPEC-1's supporting paragraph for step 3, and a code deliverable (CODE-4) the proposal did
not have.

OPEN [scope]: whether the coordination generation's baseline should be 1 on the session row rather than 0 with
a floor applied at each sender. That deletes the floor entirely and is the smaller end state, but it touches
§4.2, two migrations, and coordfence. Nobody has costed it. Owner: a human reviewer deciding whether 0076
absorbs it or a successor proposal does.


### [spec.4.postfix-review-G1.1]

DECISION: Both confirmed findings LANDED; every new file:line citation checks out; one DRIFT finding
reported — BECAUSE the new §10.1 stamp rule contradicts two unstaged spec sentences that state the barrier
carries the session's *current* `coordination_generation` — ALTERNATIVES: rejected reporting the §29.7
mirror's predicate wording ("does not match the value the pod holds") as narrower than §10.1.8's, since
§29.7's domain is a handed-off session for which a value is always held; rejected reporting pass 7's now
superseded description of its own edit, since resolved-review passes are historical records this proposal
explicitly leaves as written (spec-changes.md:653-657).

FACT: finding 1 landed. The retired "until that fence is recorded the pod holds no generation" framing is
gone from both staged sites; §10.1.8 step 1 now reads "the pod rejects the barrier as a generation-stale RPC
when it holds a generation for that session that the barrier does not carry, and otherwise accepts it"
(spec-changes.md:187-191) and §29.7 mirrors it at :320-324. `grep -n "acquiring replica\|holds no generation"`
returns only D7's own correct reading (:50) and the pass-8 record of what was retired (:704-709) —
EVIDENCE: spec-changes.md:187-198, :319-325.

FACT: finding 2 landed and the acceptance is now reachable. With the staged §10.1 sentence the barrier for a
never-fenced session carries 1, clears the `gen <= 0` guard at pkg/adapter/coordination.go:224-226, and is
accepted on staged step 3's unset arm once CODE-2 makes the gate `initialized && gen != fenced` —
EVIDENCE: spec-changes.md:200-215; pkg/adapter/coordination.go:223-226, :236-239.

FACT: every new code citation resolves and says what is claimed. pkg/adapter/coordination.go:224-226 is the
`gen <= 0` InvalidArgument guard and :236-239 the `!initialized || gen != fenced` gate; coordfence.go:147-153
is `if gen <= 0 { ... gen = 1 }`; migrations/0050_session_record_fields.up.sql:38-39 is the
`NOT NULL DEFAULT 0` column; migrations/0164_coordination_lease.up.sql:44 the lease column's same default;
barrier.go:207 is `func (c *Coordinator) dispatchOne`, :226 `c.dispatch.Send`, :241 the record's
`CoordinationGeneration`; wiring.go:41/:49/:51-53/:112; httpsurface.go:592-599 the fallback closure;
coordination.go:430 the mirror upsert; prestop.go:395/:510; start.go:4233-4245 `fenceResumedPod`;
barrier_test.go:39 and checkpoint_drive_test.go:109 the two fakes — EVIDENCE: each read directly.

FACT: the Dispatcher caller list in pass 8 is complete. `grep` finds exactly three implementations of
`Send(ctx, t Target, barrierID string)` (wiring.go:44, checkpoint_drive_test.go:109, barrier_test.go:39) and
`dispatchOne` is the only caller of `dispatch.Send`, so the single normalisation site holds — EVIDENCE:
pkg/gateway/coordination/barrier/barrier.go:84, :197, :226.

FACT: coordfence never writes the row back (only `CoordinationGeneration` reads at coordfence.go:143 and
:171), and the only incrementer is the sweeper's cross-replica handoff path, so D7's "fences without
incrementing the session row" holds — EVIDENCE: pkg/gateway/coordination/coordination/coordination.go:256-263.

FACT (reported): spec/10_gateway-internals.md:183 states "The `CheckpointBarrier` message carries the current
`coordination_generation`" and spec/29_communication-scenarios.md:1186 states the replica sends
`CheckpointBarrier` "carrying the session's current `coordination_generation`". The new §10.1 sentence states
that such a session "carries generation 0 on its row" while the replica "stamps generation 1". §10.1.8's
stamp sentence is declared a non-site (spec-changes.md:211-215) on a rationale about restating the floor, and
§29.7 step 4 is in no edit list at all — EVIDENCE: spec-changes.md:203-215; spec/10:183; spec/29:1186.


### [spec.4.review-docs-alignment.1]

FACT: the proposal text did not change this round either. `diff -rq` against the r4 snapshot shows only the
review-log differs (compaction pass 3), so every non-log file is byte-identical to round 3 — EVIDENCE:
`diff -rq scratchpad/cp-snap/0076-run2/spec-r4 proposals/0076_fix_scope-the-coordination-generation-to-the-session`

FACT: I re-derived the `docs/` surface a third time and it is clean. `grep -rn
"coordination_generation|coordinator_|CoordinatorFence|CheckpointBarrier|drain barrier" docs/` returns
`docs/reference/adapter-contract.md:68,69,96,97`, `docs/reference/metrics.md:307,309,310,311,312`,
`docs/reference/glossary.md:54`, `docs/getting-started/concepts.md:101`,
`docs/operator-guide/upgrades.md:47-49`, and the `CoordinatorHandoffSlow` runbook. None is made wrong by a
per-session generation, by D5, by D6, or by D7, and no metric or alert gains or loses a row. Stop
re-deriving this; three rounds have now paid for it.

FACT: **the barrier for a never-fenced session carries generation 0 on the wire, and the adapter refuses a
non-positive generation with `InvalidArgument` before the generation gate.** This is the load-bearing fact
D7 was written without. The barrier-target row's generation is `row.CoordinationGeneration` copied raw from
the session row (`pkg/gateway/coordination/coordination/coordination.go:430`; the cache fallback does the
same, `cmd/lenny-gateway/httpsurface.go:592-598`), the column is `NOT NULL DEFAULT 0`
(`migrations/0050_session_record_fields.up.sql:38-39`), and nothing floors it on the barrier path — only
`coordfence.fence` floors a zero row at 1, and that is the fence path
(`pkg/gateway/coordination/coordfence/coordfence.go:147-153`). `CheckpointBarrier` then returns
`InvalidArgument` at `pkg/adapter/coordination.go:222-226`, three statements above the
`!initialized || gen != fenced` gate at `:236-239` — EVIDENCE: `pkg/gateway/coordination/barrier/wiring.go:49`,
`:110-114`.

MISTAKE: round 3 left this as UNVERIFIED, framed as "the error code the staged text implies may be wrong",
and routed it to "a code-lane reviewer". It was harmless while the staged text REFUSED the never-fenced
session's barrier. Pass 7 then reversed the staged text to ACCEPT it (D7) without anyone chasing the
UNVERIFIED, so the zero stamp became load-bearing and D7's whole repair became unreachable. Cost: a round.
The general lesson is that an UNVERIFIED handed to another lane must be re-read by whoever reverses the text
it qualifies.

WATCHOUT: `bumpCoordinationGenerationOnSnapshotClose` (`pkg/gateway/sessionserver/start.go:4452`) does
increment the counter without a fence, so it looks like a way a running session reaches generation 1. It is
not: it runs only on the §7.2 snapshot-close terminal collapse, and the sweep releases the mirror row for a
terminal session before the barrier query sees it
(`pkg/gateway/coordination/coordination/coordination.go:276-280`).

DECISION: reported one finding, D7's unreachable acceptance — BECAUSE it is the only defect I found whose
fix lands in the staged spec edits and whose evidence is mechanical rather than a judgement call.
ALTERNATIVES rejected: (a) §29.10's two staged mirrors take the acceptance sentence without the unset
clause, which reads as the refusal D7 retires — rejected because the SPEC-2 bullet texts as actually
described (`spec-changes.md:260-266`, `:273-277`) are scoping statements that never use "accepts only", so
the contradiction is not in the text that lands; (b) `spec/28:237`, `:274`, `:354`, `:390` state that the
pod validates the stamp on every gateway-to-pod RPC and are in no edit list — rejected as a close variant of
the already-refuted sender-side/pod-side finding, since "validates and rejects a stale one" passes vacuously
on an unset value; (c) the staged §10.1.8 pair's "Both outcomes are safe" for a false-positive barrier that
now quiesces a session a peer replica coordinates — rejected as hardening on a bounded, non-lossy outcome.

OPEN: if the fix is "floor the barrier stamp at 1 like the fence path", note that the floored value is then
indistinguishable from a genuine first generation, and the pod's equality gate would refuse it once a fence
at 1 has been recorded. If the fix is "admit on the unset predicate regardless of the stamp", CODE-2 must
move the non-positive-generation refusal behind the unset test, which `spec-changes.md:622-624` currently
states as unchanged. A human or the next fixer picks; both need a staged sentence.


### [spec.4.review-edit-sites.1]

FACT: the complete inventory of spec/ sentences that state the POD-SIDE record-and-reject rule, re-derived
this round so nobody has to grep it again: `spec/10_gateway-internals.md:30` (§10.1 summary bullet, declared
consistent), `:38` (step 2, staged), `:41` (step 3, staged), `:183` (§10.1.8 step 1, staged);
`spec/28_communication-channels.md:315-316` (CH-FENCE Messages, staged), `:333-336` (CH-FENCE Degradation,
staged), `:1675` (§28.6 One holder per session, staged), `:1679-1680` (§28.6 second opener, NOT staged),
`:1808` (§28.8 CH-BARRIER exclusivity cell, declared a non-site); `spec/29_communication-scenarios.md:1151`
(§29.7 framing, staged), `:1264` (§29.8 preconditions, session-scoped), `:1307` (§29.8 step 7, staged),
`:1328` (§29.8 step 10, conditional on a rejection having happened). Neutral, not sites:
`spec/28:238`, `:252`, `spec/29:622`, `:819`, `:1013`, `spec/07_session-lifecycle.md:215`,
`spec/04_system-components.md:712`.
— EVIDENCE: `grep -rn "generation-stale\|rejects a stale\|rejected on the stamp" spec/`

WATCHOUT: D7 (accept the barrier when the pod holds no fenced generation) is the newest decision in the
document and it falsifies every unqualified "a non-holder's RPC is rejected on the stamp" sentence for the
never-fenced session, which the proposal itself calls the ordinary case. Two such sentences are still
unstaged and one of them is excluded on a premise SPEC-1's own staged §10.1.8 text contradicts
— EVIDENCE: proposal `.spec-changes.md:170-172` vs `:244-246`; `spec/28_communication-channels.md:1679-1680`, `:1808`.

FACT: both surviving §28 rejection sentences are ALSO loose today inside §10.1.2 step 2's window (the pod
still accepts the prior coordinator's RPCs until the successor fences), so a skeptic can reach for
"pre-existing". The distinguishing argument is that D7 turns a transient window into a permanent state for
the ordinary session, and that the proposal's stated exclusion grounds are independently false
— EVIDENCE: `spec/10_gateway-internals.md:38`.

FACT: charts/ carries nothing this change reaches; `grep -rn -i "coordination\|fence" charts/` returns only
`coordination.k8s.io` RBAC verbs and the Redis coordination URL value. spec/16 §16.6 lists no adapter-side
structured log event, so `coordinator_connection_lost`, `coordinator_lost`, and `coordinator_generation_gap`
are in no inventory — EVIDENCE: `spec/16_observability.md:654-668`.

MISTAKE: do not re-report "the §10.1.4 hold-exit predicate is unstaged". It is on the refuted list and the
§29.10 hold-bullet overreach ("a successful fence for any one of those sessions exits the hold for the pod",
which no §10.1.4 sentence states) is a close variant of it. I looked at it and dropped it for that reason
— EVIDENCE: proposal `.spec-changes.md:267-272`; `spec/10_gateway-internals.md:56`.

UNVERIFIED: whether the §28.5.1 `CH-BARRIER` Preconditions bullet ("The generation stamp and the fence
acknowledgement that govern every gateway-to-pod RPC", `spec/28_communication-channels.md:345-347`) is a
sender-side statement (and so a non-site, per the refuted precondition-set finding) or a pod-side one that
D7 falsifies. I read it as sender-side and did not report it. A mechanism lens should settle it.


### [spec.4.review-feasibility.1]

DECISION: Reported three findings, all against SPEC-2's `spec/28` non-site declarations and against the staged §10.1.8/§29.7 sentence pair — BECAUSE D7's acceptance branch and the per-session scope both invalidate flat pod-side rejection sentences that SPEC-2 explicitly carves out, and the staged pair states a pod state the pod does not have — ALTERNATIVES: rejected a finding on step 3's session-less pod-scoped RPCs (`DemoteSDKRequest`, `NegotiateVersionRequest`, `GetObservedIntegrationLevelRequest`, `AdapterEventsRequest` carry no session field per `spec/04_system-components.md:188`) because none of them carries a `coordination_generation` field at all, so the predicate is already vacuous for them under both the current and the staged wording; a skeptic refutes it as pre-existing overbreadth in step 3's unchanged opening sentence.

FACT: only three sentences in `spec/` state the barrier's generation-stale rejection flatly: `spec/10_gateway-internals.md:183` (staged), `spec/29_communication-scenarios.md:1151` (staged), and the §28.8 `CH-BARRIER` "Holder of the exclusivity constraint changes" cell (`spec/28_communication-channels.md:1808`, declared a non-site). `grep -rn "generation-stale\|rejected on the stamp" spec/` returns the complete set — EVIDENCE: spec/28_communication-channels.md:1808

FACT: §28.6's "The second opener on those channels" paragraph is NOT a hold paragraph. Its first sentence (`spec/28_communication-channels.md:1679-1681`) states the generation-stamp rejection; only its second sentence states the hold. SPEC-2 files the whole paragraph under "the hold sentences ... Each states the hold" — EVIDENCE: proposals/…spec-changes.md:235-238

WATCHOUT: the proposal's own criterion for staging §28.6's `CH-FENCE` sentence ("this sentence states the pod's rejection rule, which the unit sentence does not entail, and which a pod-wide gate satisfies today", spec-changes.md:227-229) selects the second-opener sentence at `:1679-1681` word for word, and that sentence is left unstaged. Any later pass re-deriving the §28.6 inventory should apply the criterion to both sentences — EVIDENCE: spec/28_communication-channels.md:1675 vs :1679-1681

WATCHOUT: "until the successor's fence is recorded the pod holds no generation for the session" is false for any session that was fenced before. The pod then holds the PRIOR coordinator's value, which the draining replica's own barrier matches, so the barrier is accepted for a different reason. §10.1.2 step 2 already states this ("Until the pod acknowledges the fence, the pod still accepts RPCs carrying the previous generation") — EVIDENCE: spec/10_gateway-internals.md:38

FACT: `heldSession` carries the deregistered `*slotState` (`pkg/adapter/slotsession.go:282-285`, `:361-365`), so SPEC-1's §10.1.4 requirement that each terminated session's `coordinator_lost` record read its own entry's generation IS performable by the adapter after CODE-1. I checked this as a feasibility risk and it is not one; do not re-derive it.

FACT: no §16 inventory covers the adapter's structured log events. §16.6's list at `spec/16_observability.md:658` is gateway-emitted only, and `lenny_adapter_coordinator_hold` at `:185` carries no generation label, so dropping the generation from `coordinator_connection_lost` opens no §16 or docs site. Re-derived; matches the standing context.

OPEN: does the one genuinely new §28.5.1 sentence ("A fence for one session does not change the generation the pod holds for another") owe a `tests/claim-map.json` row under §28.4's "Every normative statement this section makes about a mechanism carries a row"? SPEC-2 says no row moves. The test is a schema gate rather than a coverage gate, so nothing fails, but the §28.4 sentence is not satisfied. Out of this loop's scope (fix lands in tests/), still open for the code/test loop.


### [spec.4.review-fresh.1]

DECISION: Reported three findings, all on the D7 acceptance rule's blast radius through the rejection
sentences — BECAUSE the staged §10.1.8/§29.7 pair and the §28 non-site list all describe the barrier's
acceptance with a predicate that is not the one staged step 3 fixes — ALTERNATIVES: rejected reporting the
"step 3 permission defeats §10.1's split-brain claim at spec/10:30" angle (adjacent to a finding the
material skeptic already refuted, and the proposal argues line 30 explicitly at spec-changes.md:157-160);
rejected the §29.10 "neither fences nor unfences another" vs the pod-wide hold-exit bullet (the
Partitioned/Shared headings disambiguate); rejected §28.5.1's Preconditions bullet (sender-side, per
replica, not made false).

FACT: the acceptance predicate staged in §10.1.2 step 3 ("the pod holds no fenced generation for the
session") and the one staged in §10.1.8 step 1 / §29.7 ("the successor's fence has not been recorded") are
different predicates, and they diverge for a session the DRAINING replica itself fenced on that pod: the pod
then holds that replica's own value and the barrier is accepted by equality, not by absence — EVIDENCE:
proposals/.../0076...spec-changes.md:130-133 vs :168-172 and :280-283; spec/10_gateway-internals.md:38.

FACT: "superseded replica" appears exactly once in the whole spec, at spec/28_communication-channels.md:1808,
and is nowhere defined. The proposal's non-site argument stipulates a definition ("one whose successor's
fence the pod has recorded", spec-changes.md:244-245) that the spec does not carry — EVIDENCE:
`grep -rn "superseded replica" spec/` returns one hit.

FACT: §28.6's "The second opener on those channels" paragraph is TWO sentences, and only the second states
the hold. The first (spec/28_communication-channels.md:1679-1681) states the generation-stamp rejection and
is parallel to the §28.6 sentence at :1675 that SPEC-2 does stage. SPEC-2 files the whole paragraph under
"Each states the hold" (spec-changes.md:235-238), which is false of its first sentence.

FACT: only the review-log changed since round 3 (`diff -rq` against the r4 snapshot). The staged text under
review is identical to what round 3 signed off, so a fresh lens has to re-derive rather than read a diff.

WATCHOUT: barrier.go's checkpoint goroutine starts BEFORE dispatch.Send and is joined after, so a
generation-stale barrier still runs the gateway-driven checkpoint; the duplicate-capture cost D7 cites is
prestop's second pass, not a lost checkpoint — EVIDENCE: pkg/gateway/coordination/barrier/barrier.go:217-236;
pkg/gateway/podlifecycle/prestop/prestop.go:390-397. Verified again this round; do not re-derive.

FACT: every anchor SPEC-1/SPEC-2/D5/D6/D7/§4/§7 cite resolves verbatim. Re-verified this round:
spec/10:30,37,38,39,40,41,58,60,62,183,184,198; spec/28:315-316,333-336,361-365,1675,1808;
spec/29:1150-1152,1274,1307-1313,1464-1470,1523-1543; schemas/lenny-adapter.proto:153-162,165-179,
1442-1446,1449-1451,1455-1462,1469-1474,1477-1479; pkg/adapter/coordination.go:92,99,108-121,216,223,
228-231,236-239; holdstate.go:39-44,90-100,107-112,172-176,192; slotsession.go:267;
migrations/0050_session_record_fields.up.sql:38-39; coordfence.go:147-153; start.go:3975,4067,4233,4237;
coordination_seams.go:155-160,233; coordination.go:399; spec/04:747. Citation-lens work on this proposal is
finished; spend the round elsewhere.

OPEN: the staged §10.1.4 text names "the per-session `coordinator_lost` log line" as a spec artifact. The
code emits it (`slog.Warn(reasonCoordinatorLost, "last_generation", gen)`,
pkg/adapter/holdstate.go:225-228) but no spec section introduces it; §10.1.4:58 uses `coordinator_lost` only
as a `session.terminated` reason. Judged not a finding because the staged sentence is itself the definition
site, but a later lens may disagree.


### [spec.4.review-reliability.1]

FACT: `CheckpointBarrier`'s held quiescence is bounded — the handler blocks on `select { <-done; <-ctx.Done() }`
and clears `quiesced` in a deferred close, so the 90s RPC deadline releases it. A "quiescence held forever"
finding is dead on arrival — EVIDENCE: `pkg/adapter/coordination.go:253-269`

FACT: D7's three consequence citations all check out verbatim. `dispatchOne` starts
`CheckpointWithTrigger` in a goroutine before `dispatch.Send` and joins it, `ErrGenerationStale` sets only
`out.Stale` with no `meta.Upsert`, and the preStop loop re-checkpoints any session whose barrier did not ack.
So the refused barrier really does cost the acked-barrier record and really does double-capture — EVIDENCE:
`pkg/gateway/coordination/barrier/barrier.go:217-246`; `pkg/gateway/podlifecycle/prestop/prestop.go:388-398`

FACT: SPEC-1's zero-sentinel rationale is sound on all three gates it names — the column is
`BIGINT NOT NULL DEFAULT 0`, the `Fencer` floors a zero row at 1 before fencing, and the adapter refuses a
non-positive generation — EVIDENCE: `migrations/0050_session_record_fields.up.sql:38-39`;
`pkg/gateway/coordination/coordfence/coordfence.go:147-153`; `pkg/adapter/coordination.go:93-94`

WATCHOUT: the staged §10.1.8 / §29.7 sentence "until that fence is recorded the pod holds no generation for
the session" is only true on a session's FIRST handoff. §10.1.8 step 1's population is "a session no longer
coordinated by this replica", which on any second-or-later handoff has a value recorded on the pod, and
§10.1.2 step 2 says the pod still accepts the previous generation in exactly that window. D7's own opening
distinguishes the two states correctly and the staged text collapses them. Reported as the round's one
finding — EVIDENCE: `...spec-changes.md:170-172`, `:281-283` against `:48-50` and
`spec/10_gateway-internals.md:38`

MISTAKE: I spent most of the round hunting a fencing hole in D7 (a superseded replica's barrier admitted
because the pod holds no value). It is not one. For a never-fenced session the only replica that can have a
barrier accepted is the current coordinator or the immediately prior one inside the step-2 window, and step 2
sanctions the prior coordinator's RPCs in that window explicitly. Do not re-derive this.

MISTAKE: I also chased §28.6's "second opener" paragraph as an unstaged mirror D7 falsifies. It is not: the
paragraph's own closing sentence already carries the step-2 window ("the fence acknowledgement closes the
window in which the prior coordinator's RPCs are still accepted"), so the second opener it describes is the
ACQUIRING replica, which still must fence first under D7 — EVIDENCE: `spec/28_communication-channels.md:1679-1686`

UNVERIFIED [d6-reset-ground]: D6's stated ground for the first-fence exemption is "a session that has never
been fenced on this pod has accumulated no state for the gap path's reset to act on", justified from
`checkSessionBound`. The inference does not follow — a session running for an hour under its original
coordinator has never been fenced and has accumulated exactly the transient tool-call and lifecycle state
clause (b) names, which is the ordinary case the proposal itself establishes. The exemption is still right
(the reset has no defined starting point with no recorded fence, and running it would discard the legitimate
original coordinator's work), so only the stated ground is wrong and no staged spec sentence carries it. I
judged it below the bar for this loop. A later fixer should restate the ground rather than defend it —
EVIDENCE: `...spec-changes.md:36-38` against `:56-58`

OPEN: does §28.8's `CH-CHECKPOINT` cell ("A stream opened by a replica that no longer holds the current
generation is rejected on the stamp") survive staged step 3's unset-value clause, which says the pod does not
reject on generation grounds when it holds no value for the session? The proposal declares the two
`CH-BARRIER` cells non-sites on a reading of "superseded" that would cover this cell too, so it is either
both or neither. Not reported: the same reading refuted an earlier `CH-BARRIER` finding — EVIDENCE:
`spec/28_communication-channels.md:1806`; `...spec-changes.md:130-136`, `:240-246`


### [spec.4.review-security.1]

DECISION: Reported two findings, both on the D7/§10.1.8 side of the staging rather than on D7 itself —
BECAUSE D7's substance (accept a barrier for a bound session with no recorded generation) has already been
argued through three passes and twice refuted when attacked head-on, but the sentences pass 7 wrote AROUND
it carry a factual error and leave one pod-side rejection rule unstaged. ALTERNATIVES: attacking D7 as a
fail-closed regression (refuted twice already, once as "sender-side obligation", once as "preference between
workable designs"); attacking §29.10's mirror for taking the acceptance sentence without the unset clause
(the proposal's own pass-7 record reads that sentence as SILENT on the unset case, so a mirror that is
likewise silent is underspecification, which this loop refutes).

FACT: the staged §10.1.8 step 1 sentence pair states a two-case dichotomy over a three-case predicate. Its
"Until that fence is recorded the pod holds no generation for the session" is false whenever the session was
fenced by an EARLIER handoff: the pod then still holds that earlier value, and the draining replica's
false-positive barrier carries exactly that value, so it is accepted on the equality arm rather than on the
absence of a value. §10.1.2 step 2 says this in so many words — EVIDENCE:
`spec/10_gateway-internals.md:38` "Until the pod acknowledges the fence, the pod still accepts RPCs carrying
the previous generation"; proposal `...spec-changes.md:168-173`, mirrored at `:280-283` (§29.7).

FACT: §28.6's "The second opener on those channels" paragraph opens with a POD-SIDE rejection rule, not a
hold rule: "A replica that opens one of those four channels without holding the current generation is
rejected on the generation stamp", the four being CH-ATTACH, CH-CHECKPOINT, CH-FENCE, CH-BARRIER, and the
paragraph closes "The constraint excludes a second replica" — EVIDENCE:
`spec/28_communication-channels.md:1669-1670`, `:1679-1681`, `:1686`. SPEC-2 declares the whole paragraph a
non-site on the ground that it "states the hold" (`...spec-changes.md:235-238`), which is true only of its
second and third sentences. The same first-clause problem sits in the §28.8 `CH-CHECKPOINT` exclusivity cell
("A stream opened by a replica that no longer holds the current generation is rejected on the stamp",
`spec/28_communication-channels.md:1806`), which no edit list names either.

WATCHOUT: an earlier round's refutation of "step 3's unset clause permits what step 2 forbids" quoted only
the SECOND halves of the §28.8 `CH-ATTACH` and `CH-CHECKPOINT` cells ("the acquiring replica may not send
this RPC", "the new holder must complete its fence before it opens one") and concluded the whole family is
sender-side. It is not: `:1806`'s first clause and `:1679`'s first sentence are pod-side rejection rules.
Do not let the earlier refutation retire them.

FACT: the hold allowlist in code is wider than §10.1.4's "only CoordinatorFence" — it also serves
NegotiateVersion, AdapterEvents, and the two gRPC health methods — EVIDENCE:
`pkg/adapter/holdstate.go:53-59`. Pre-existing spec-vs-code drift; the staged §29.10 hold bullet mirrors the
spec sentence faithfully, so it is not this proposal's defect. Do not report it here.

FACT: the zero-sentinel gates SPEC-1's §10.1.4 text names all check out — `NOT NULL DEFAULT 0` with
`CHECK (coordination_generation >= 0)` at `migrations/0050_session_record_fields.up.sql:38-39`, the fencer's
floor of a zero row at 1 at `pkg/gateway/coordination/coordfence/coordfence.go:147-153`, and the adapter's
refusal of a non-positive generation at `pkg/adapter/coordination.go:93-94`. Verified once; do not re-derive.

FACT: the gateway never trusts the pod's self-reported `LastFencedGeneration` as a bound — on a rejected
fence `coordfence.fence` re-reads the authoritative Postgres value and relinquishes when it has not advanced
— EVIDENCE: `pkg/gateway/coordination/coordfence/coordfence.go:164-179`. The security lens's
"pod self-report as the authoritative bound" check is clean for this change.

UNVERIFIED: whether a barrier accepted under D7 for a session with no recorded generation can be issued by a
replica that never coordinated the session at all (as opposed to the draining ex-coordinator §10.1.8 step 1
describes), and what §28.6's exclusivity constraint then means on the pod side. A mechanism or fresh lens
should decide whether that needs a stated bound in §10.1.2 step 3.

## Retired

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
