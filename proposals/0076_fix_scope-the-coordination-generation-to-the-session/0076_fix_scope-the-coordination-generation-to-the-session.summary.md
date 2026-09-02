# Summary: Scope the coordination generation to the session

## Summary

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` gate and gap
detection read the same per-session value, and the barrier gate that holds quiescence open and carries the
gateway-minted checkpoint id back into the ack moves onto the same entry, so two co-tenant sessions drained
together each hold their own gate. A session's fenced generation is unset until that session's first
accepted fence on the pod, so the exemption that makes a first fence neither stale nor a gap moves from the
pod's lifetime to the session's binding on it, which is D6, and SCHEMA-1 carries that unit onto the wire
comment that states the exemption. The coordinator-loss hold stays pod-scoped and reports
per-session generations: the pod-level arming event carries none, and each terminated session's
`coordinator_lost` record carries its own, or zero when no coordinator ever fenced it on that pod. The
proto doc comment that already claims per-session monotonicity becomes true. A `CheckpointBarrier` naming a
bound session for which the pod holds no fenced generation is accepted and records no value, which is D7 and
which changes the shipped gate. `spec/10` §10.1 and `spec/04` §4.2 state the counter's baseline: a
session row carries `coordination_generation = 1` from creation, so the value a replica carries for a session
no replica has taken over is positive, and the first takeover's compare-and-swap mints 2 strictly above it.
CODE-4 lands that baseline in the schema and on the session-store create path and deletes the gateway fence
path's floor of a zero row. Every sentence stating that a gateway-to-pod message carries the session's
current `coordination_generation` stays true and is left unedited.

**What is fixed.** On a concurrent pod: a legitimate coordinator handoff rejected as
`coordinator_handoff_stale`, a drain barrier rejected so the drain checkpoint runs unquiesced, a
spurious `coordinator_generation_gap` event, a coordinator-lost post-mortem and log line that stamp
one session's generation on every session the hold terminates, and a split-brain counter that
increments on healthy handoffs. A hold released by an unrelated session's fence is not on that list:
the hold is pod-scoped by D5, so a fence from any session on the pod is the correct exit from it. D7
and the counter baseline together add a barrier repair that is not specific to a concurrent pod. The
drain barrier of a session that never resumed and was never taken over meets two refusals today: the
adapter rejects the non-positive generation its row still carries before the gate is reached, and
the gate then rejects the absence of a recorded value. The baseline removes the first and D7 removes
the second, so that session is quiesced by its own preStop drain instead of being captured twice
against a live workspace. Neither change delivers that repair without the other. The baseline also
repairs a takeover defect in the shipped tree: a session fenced on resume at the gateway floor's 1
while its row still reads 0 has its first crash-takeover fence rejected as
`coordinator_handoff_stale`, so the takeover costs a sweep cycle of delay and a pair of split-brain
metric increments that report a handoff that was healthy.

**Watch out for.** The spec-lane work covers `spec/10` under SPEC-1, `spec/28` and `spec/29` under
SPEC-2, and `spec/04` §4.2 under SPEC-3, and step S1 lands all three together. Landing SPEC-1 alone leaves
`spec/28` and `spec/29` restating the pod-wide rule while citing the `spec/10` that now contradicts them,
which is the state SPEC-2 exists to prevent, and no tier-0 or tier-11 gate string-matches those sentences.
CODE-4's migration and both session-store `Create` floors land in one commit, and 0181 deliberately leaves
the session row's `CHECK (coordination_generation >= 0)` alone. Migrations run as a
`pre-install,pre-upgrade` hook that completes before the gateway Deployment rolls, so the schema is ahead
of the binaries for the whole rolling window while `pgstore.Create` names `coordination_generation` in its
insert column list and writes an explicit zero. Tightening the check to `>= 1` in this release would reject
every old-binary session insert for that window, which §10.5's expand-contract rule places in a later
Phase 3 migration. The step landing CODE-2 amends `TestCheckpointBarrierRejectsWithoutFence`
(`pkg/adapter/coordination_test.go:184-197`), which asserts the refusal D7 retires, and the step landing
CODE-4 amends `TestCreateDefaultsSessionRecordFields`
(`pkg/gateway/session/sessionstore/memstore/memstore_test.go:309-325`), which asserts the counter baseline
CODE-4 replaces, and `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1`
(`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go:992`), whose fenced-newer-writer constant of 1
the baseline makes equal to the session row's own value, and `TestFenceZeroGenerationFencesAtBaseline`
(`pkg/gateway/coordination/coordfence/coordfence_test.go:173-183`), whose baseline floor CODE-4 deletes;
§8 states those amendments alongside the assertions the baseline shifts elsewhere. CODE-4's tier-2 case
over migration 0181 lands under
`tests/tier2_component/migrations/`, because the migration lint that runs inside tier 0 looks for the
migration's number in that directory alone. SCHEMA-1's proto edit lands together with the stubs
`make generate-proto` produces, because tier 0 diffs the committed `pkg/proto` tree against a fresh
regeneration. CODE-1 and CODE-3 land in one step, because CODE-1 gives `Server.LastFencedGeneration` a
session id while CODE-3 deletes its only production caller, the hold's pod-level arming read, which has
no session id to pass. That step also rewires `pkg/adapter/checkpoint.go`, because `Server.barrier`
ceases to exist there and the `Checkpoint` stream links and completes on the entry
`checkpointRootsForSession` resolves for it, so CODE-2 is the generation gate alone.

A deliverable-side correction is still outstanding in the status file. SCHEMA-1's target list now
names the whole carrier set SPEC-2 states, the fence and barrier RPC and message comments and the
operational-RPC `coordination_generation` field comments included. The status file's scope bullet
still states that one session's handoff releases another session's coordinator-loss hold, which D5
refutes, and its closing paragraph still frames the hold's scope as the open question this change
answers. The pass records under "Resolved in adversarial review" in the spec changes carry the
rationale behind each correction already landed, and the review log's ledger carries the rest.

The hold's scope is settled rather than open. D5 keeps it pod-scoped, because the only arming signal the
adapter has is the close of the pod's single CH-ADAPTEREVENTS stream, which names no session. The cost is
recorded as a non-goal: a pod whose stream holder crashes freezes co-tenant sessions whose own coordinators
are alive. Proposal 0073 is converged and is not reopened; this proposal sequences after it.

## Open decisions

Every decision below is open and needs a reviewer's answer before this proposal moves to `Approved`.
Each was derived independently three times and then validated independently five more times against the
working tree, with the validators instructed to falsify each recommendation rather than confirm it. Where
a recommendation changed under that pass, the entry says so. Two earlier entries were withdrawn and are
recorded at the end, because a decision list that quietly drops an item teaches the next reader that it
was answered.

**OD1. The barrier gate's comparison.** §10.1.8's gate compares the barrier's `coordination_generation`
with the session's last fenced value, and the question is whether that comparison stays equality.
**Recommendation: keep equality.** §10.1.2 step 2 makes an acknowledged fence a hard precondition for
every operational RPC, and widening admits a barrier carrying a value above what the pod holds. There are
two distinct producers of such a value, and the second is permanent: a successor between its
compare-and-swap and its fence acknowledgement, and `bumpCoordinationGenerationOnSnapshotClose`
(`pkg/gateway/sessionserver/start.go:4452-4457`), which increments the row on the resuming-to-terminal
edges with no fence ever following, so the row sits above the pod's fenced value for the rest of the
session's life. Widening would accept a barrier carrying a generation no coordinator ever installed.
Widening also fixes nothing: the refusal that actually occurs on the healthy path comes from the mirror
carrying a value *below* the pod's, which a wider comparison rejects just as equality does. The staged
text already writes equality in every carrier, so this decision costs no edit. §4's stated ground for it
is false and every validator confirmed the falsification independently. That sentence is proposal prose
under a draft header and lands in no file under `spec/`, so correcting it is proposal hygiene rather than
a specification risk. Replace it with step 2's precondition.

**OD2. A fence carrying the generation the pod already recorded.** §10.1.2 step 2 requires the new
coordinator to retry a failed fence with the same generation value. `pkg/adapter/coordination.go:99`
rejects it, and `pkg/gateway/coordination/coordfence/coordfence.go:164-179` then relinquishes the lease.
The collision fires inside a single `Fence` call: attempt one lands, its acknowledgement is lost, the
driver's transient arm retries at the same value, and attempt two is refused as stale. A handoff that
succeeded costs its lease, a sweep cycle, and one increment each of
`lenny_coordinator_handoff_stale_total` and `lenny_coordinator_fence_relinquished_total`.
**Recommendation: accept the equal case, and land it after the counter baseline rather than before.**

The ordering is load-bearing and is the finding that reconciles a dissent in the validation pass. Today
two different replicas can legitimately carry the same generation: `fenceResumedPod`
(`pkg/gateway/sessionserver/start.go:4233-4240`) fences without incrementing, and `coordfence.go:147-153`
floors a zero row to 1, so a resume coordinator fences at 1 while a later takeover's `RecordHandoff`
bumps 0 to 1 and fences at 1. Accepting equality in that state would admit a genuine split-brain. Once
CODE-4 baselines the row at 1 and the first takeover mints 2, the collision is unreachable and the
remedy is safe. If the reviewer splits CODE-4 out under OD5, this fix follows it.

The rationale carried in the earlier draft of this section was wrong and is withdrawn: `RecordHandoff`
(`pkg/gateway/coordination/coordination/coordination.go:463-480`) is an unconditional increment rather
than §10.1.2 step 1's compare-and-swap, a misattribution proposal 0052 already recorded. Uniqueness comes
from `pgstore.Update`'s `SELECT ... FOR UPDATE` inside a transaction, together with the baseline.

Whatever the reviewer decides about the code, SPEC-2's staged §28.5.1, §28.6, §28.8, and §29.8 arms
enumerate the older and the higher case and are silent on the equal one, which reads as accepting it.
`schemas/lenny-adapter.proto:1456-1458` is the one carrier that answers the question today, and SCHEMA-1
rewrites it. Deferring the code fix without naming the equal case in those arms freezes a fresh
contradiction into text this change is writing.

**OD3. `spec/04` §4.1's message-scope row, and proposal 0075.** The row classifies
`CoordinatorFenceRequest` as pod-scoped, and the paragraph below it grounds that on the message staying
pod-scoped, which SPEC-1 falsifies. **Recommendation: reclassify the row to session scope and rewrite the
declaring sentence, naming the pod-scoped hold exit as the one pod-wide effect that remains under D5.**
§4.1 already carries the precedent: `ShutdownRequest` is classified session-scoped despite carrying a
whole-pod scrub, and the paragraph below the table rejects the reading that a field's presence stands in
for a scope. No gate catches the contradiction either way. The tier-0 check accepts either value, and in
the tier-3 session-address suite the message appears only inside a comment claiming it is covered
elsewhere, while every assertion iterates a map that excludes it.

**The material consequence, which the earlier draft of this section missed entirely and every validator
found: this decision determines proposal 0075's fate.** 0075 exists to derive the scope classification
from the address type, and `CoordinatorFenceRequest` is its sole counterexample. Its stated ground is
that the handler mutates one pod-wide `coordinationState`, so the write target is the pod and the
identifier selects nothing. CODE-1 deletes that field. After this change the identifier resolves the slot
entry the generation is recorded on, so it is an address, 0075's counterexample disappears, and its
retyping deliverable loses its justification. §6 currently files this as a rename whichever lands second
rebases onto, which understates it. 0075 is an unreviewed draft, so the cost of obsoleting it is low, and
the reviewer should be told they are deciding it. Reclassifying also obliges a rewrite of the tier-3
comment, and neither that file nor the tier-0 scope test is named in §9.

**OD4 is withdrawn.** It asked whether to state the barrier quiescence unit or to delete a design claim,
and the validation pass found unanimously that it was mis-stated. The design claims that §10.1.8 step 3
fixes *the gate's* unit at the session, which is about the `barrierGate` rather than the `quiesced` flag,
and that claim is true and load-bearing for CODE-1. SPEC-2 already stages the narrowing that leaves
§29.10's clause unanswered. Nothing is owed and nothing should be deleted. The strongest ground for not
stating the unit is one no earlier reading found: the only quiescence primitive the specification
defines, the `checkpoint_request` frame in §28, carries no session identifier and is delivered to the
pod's shared runtime process, so stating the session would commit the specification to a unit the wire
contract underneath it cannot express. CODE-1 should state that relocating `quiesced` onto the entry
carries no specification claim about the quiescence unit, so a later implementor does not read the
field's placement as settling it.

**OD5. Whether D7, the counter baseline, and migration 0181 belong in this proposal.**
**Recommendation: land them here.** The argument carried in the earlier draft of this section does not
support the conclusion and is replaced. Inseparability is an argument for co-landing, and a successor
carrying D7, SPEC-3, CODE-4, and 0181 together would preserve it perfectly, so the split remains a live
option. The argument that does support landing here is that the scope change forces the rest: once
`initialized` is per session, a bound session for which the pod holds no fenced generation becomes an
ordinary reachable state that the current specification has no rule for, so SPEC-1 must state one, and
that rule is D7. D7 in turn forces the baseline, because its acceptance arm is unreachable for the
ordinary never-handed-off session while the adapter refuses the row's zero with `InvalidArgument` before
the gate is reached. Both halves were verified.

The cost of landing here is that the reviewer approves a production migration and both session-store
create paths inside a proposal whose subject is the generation's scope. The cost of splitting is that
SPEC-1's unset arm ships with no code behind it, leaving a spec-to-code gap on the barrier path for the
interval, and that the successor must still amend the same landed tests this proposal already schedules.
One repair the earlier draft credited to the baseline, the first crash-takeover fence rejected at 1, is
also fixed by OD2's remedy, so it is not exclusive to CODE-4.

**OD6 is replaced.** Its load-bearing claim was that the pod-wide field is an accidental mutual exclusion
holding a pod to one coordinating replica, and every validator refuted it. The predicate at
`pkg/adapter/coordination.go:99` compares generations and carries no replica term, so the identical
interference fires when one replica coordinates both co-tenant sessions and fences the lower-generation
one second. It is a cross-session interlock rather than a cross-replica one. It also does not exclude,
because a higher generation is accepted today, and it is not durable: this proposal's own problem
statement traces the refused session climbing past the co-tenant's value after a bounded number of
cycles. Two replicas coordinating co-tenant sessions on one pod is reachable in the shipped tree.

**Recommendation: keep the co-tenancy question a non-goal, and record that this change removes a
cross-session generation interlock that delayed and mis-metered co-tenant handoffs regardless of how many
replicas were involved.** It does not create multi-replica co-tenancy, which the per-session
`REG-COORDLEASE` already permits.

**OD7. Rebind and the unset state.** The review log routed this to a human and the earlier draft of this
section dropped it without classification. `Shutdown` and the hold timeout's teardown delete the slot
registry entry, so a session that unbinds and re-binds on the same pod returns with its per-entry fenced
value unset, where the shipped pod-wide field would have retained it. Under D6 and D7 that makes the next
fence exempt from both predicates and the next barrier accepted with no recorded value. **Recommendation:
accept the reset and have SPEC-1 state that the value's lifetime is the session's binding on the pod
rather than the session itself.** The blast radius is bounded: only the fence and the barrier read the
value, no operational RPC is gated on it, and the resume path fences immediately after binding. A
tombstone surviving the entry would reintroduce the second per-session lifetime D2 exists to remove. This
is a behavioural consequence the per-session move creates, so it needs stating rather than leaving to be
discovered.

### Items §7 lists, and how they should be dispositioned

- **`coord.mu`.** Not a reviewer decision. `coordinationState` embeds its mutex as its first field, so D2
  moves the lock with the struct. Delete the item as an implementation choice with no external
  consequence rather than as a settled design question. CODE-1 owes the resulting lock order, registry
  lock then entry lock then hold lock, and the ordering was checked for an inverse path and has none;
  CODE-3 removes the one opposite-order acquisition that exists today.
- **A fence for a session the pod holds no entry for.** The earlier draft of this section called this
  settled, and the validation pass found that wrong by a majority. The adapter half is settled, because
  `checkSessionBound` refuses before the generation is read. The half §7 actually asks, whether such a
  fence is a rejection or a retryable race, is unanswered, and §4 states that the two must be
  distinguishable. They are not: the guard returns the same `FailedPrecondition` the stale path returns,
  and the fence driver maps every `FailedPrecondition` to the stale branch, so a fence that raced a bind
  costs the lease and increments the split-brain counter. Disposition: out of scope for this proposal,
  with a named consequence and an owner, rather than settled.
- **The `IMPLEMENTOR TO FILL THE BLANKS` headers.** Drop them, but not on the ground the earlier draft
  gave. That ground was that every item beneath is derived or settled, and it is false: the second item
  is OD1, which this document declares open, and it carries the false rationale OD1 corrects. Drop the
  header together with that correction so the correction keeps a home. There are three such headers
  rather than one, in §4, in §5, and in the non-spec changes.

### Defects in the shipped tree that this proposal does not stage

None blocks sign-off. Each was confirmed against the working tree.

- **The barrier-target mirror lags one generation across a takeover.** On the sweep that performs a
  handoff, `pkg/gateway/coordination/coordination/coordination.go:430` passes the pre-bump snapshot value
  to `upsertMirror` while the pod was fenced at the post-handoff generation minted in the same iteration.
  Every drain barrier assembled from the mirror in that interval is refused under any comparison
  operator. This is what makes §10.1.8 step 1's description of the value as current false on the healthy
  path.
- **The fence driver conflates three failure classes into one metric.**
  `pkg/gateway/coordination/coordfence/coordfence.go:164` reads every `FailedPrecondition` as
  generation-stale, covering a genuine stale fence, a fence for a session the pod does not hold, and a
  re-fence at the recorded generation after a lost acknowledgement.
- **A tier-3 comment claims a coverage that does not exist.** The session-address suite states that
  `CoordinatorFenceRequest` is covered by the session-address arm alone, while every assertion iterates a
  map keyed by the retired duplicate-address field number, which the fence never carried.
- **The gateway has no `CH-ADAPTEREVENTS` client.** `tests/claim-map.json` records the client side as
  `UNWIRED` under deferral R12, and a repository-wide search finds no opener outside the adapter and the
  generated code. The coordinator-loss hold therefore never arms in a deployed system, which reweights
  D5's recorded cost, makes CODE-3's per-session records unreachable outside unit tests, and means §6's
  residual is not reachable until R12 closes.

### Cross-proposal consequences

- **Proposal 0075.** Recorded under OD3. This change removes 0075's stated justification rather than
  merely conflicting with its rename.
- **Proposal 0080.** It records that a hold entered for one session and released by another's fence is
  taken by this proposal, which D5 refutes, and it records the §29.10 hold-partitioning bullet as an
  outstanding gap while SPEC-2 stages that bullet's removal. Approving this proposal invalidates both
  entries. 0080 is an early draft and can be corrected.

### Corrections outstanding in the proposal

- `pkg/gateway/runtime/adapterclient/coordinatorfence.go:37` states the first-fence exemption as per pod
  lifetime, which SPEC-1 falsifies and which is already false today for a non-positive generation and for
  an unbound session. It is the only production site outside the proto and §9 does not list the file.
- The §28.4 claim-register work is narrower than the earlier draft stated. `tests/claim-map.json` already
  carries a non-wired row for the barrier's generation field under a deferral id, so the correction is to
  restatus it and to replace its note, which is false against the adapter's current comparison. The fence
  request has no row and is exempted from the tier-0 gate by name, and whether that exemption survives
  SPEC-2's new sentences is the open half.
- The status file's scope bullet contradicts D5. The earlier draft also named a closing paragraph framing
  the hold's scope as an open question, and no such paragraph exists.
- §7's heading numbering jumps from 7 to 10 because §8 and §9 live in the non-spec changes. This is an
  artifact of the folder layout rather than a defect.

## Deliverable index

| Deliverable | Lands in | What it does |
|:--|:--|:--|
| SPEC-1 | `spec/10_gateway-internals.md` | §10.1.2 states the pod-side fenced generation per bound session with its initial condition, its per-session gap reset, and step 3's acceptance rule for a session the pod holds no value for, §10.1.8 step 1 applies that rule to the barrier, §10.1 states the counter's baseline of 1, and §10.1.4 states what the pod-level arming event and each terminated session's records carry. |
| SPEC-2 | `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md` | The `CH-FENCE`, `CH-CHECKPOINT`, `CH-BARRIER`, §28.6, §28.8, and §29 mirrors of the record-and-reject rule, the gap reset, and the barrier's outcome take the wording SPEC-1 states, and §29.10's co-tenancy classification records the per-session generation and the pod-scoped hold as answered, so the applied specification carries one pod-side rule. |
| SPEC-3 | `spec/04_system-components.md` | §4.2's session-record paragraph states that a newly created session row carries `coordination_generation = 1` and that the first handoff mints 2. |
| SCHEMA-1 | `schemas/lenny-adapter.proto`, with the stubs `make generate-proto` regenerates | The fence and barrier RPC, message, and field doc comments and the operational-RPC `coordination_generation` field comments take the wording SPEC-2 states for each. |
| CODE-1 | `pkg/adapter/coordination.go`, `server.go`, `slot.go`, `checkpoint.go`, `resume.go`, and `tests/testinfra/coordfixture/coordfixture.go` | `coordinationState` and `barrierGate` move from `Server` onto the slot registry entry, each RPC resolves that entry once for the session it names, and the pod-wide accessors become per-session reads. |
| CODE-2 | `pkg/adapter/coordination.go` | `CheckpointBarrier`'s generation gate reads the resolved entry's value and accepts a barrier for a bound session the pod holds no fenced generation for. |
| CODE-3 | `pkg/adapter/holdstate.go` and `pkg/adapter/slotsession.go` | The hold stays pod-scoped, its arming line drops the generation, and each terminated session's `coordinator_lost` record and post-mortem carry that session's own value or zero. |
| CODE-4 | `migrations/0181_sessions_coordination_generation_baseline.up.sql` and its `.down.sql`, `pgstore.go`, `memstore.go`, and `coordfence.go` | The session row's counter is baselined at 1 in the schema and on both session-store create paths, and the gateway fence path's floor of a non-positive row value is deleted. |
| TEST-1 | `pkg/adapter/*_test.go`, `tests/tier4_integration`, and `tests/tier7a_load_local` | Two co-tenant sessions hand off, drain, and lose their coordinator independently, on proposal 0060's two-replica harness. |
