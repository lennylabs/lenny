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
CODE-4 lands that baseline in the schema and on both session-store create paths, and leaves the gateway
fence path's floor of a zero row in place for the rows the rolling window creates at 0. Every sentence
stating that a gateway-to-pod message carries the session's current `coordination_generation` stays true and
is left unedited.

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

**Fixed decisions.** These are closed. An implementor applies them and does not re-derive them.

- **D1.** The pod side holds one fenced coordination generation per bound session in place of one value for
  the whole process.
- **D2.** That value lives on the slot registry entry. No second map keyed by session identifier is
  introduced, because a parallel map would be a second lifetime to get wrong.
- **D3.** The proto doc comment claiming per-session monotonicity is corrected to describe the adapter and
  is not deleted.
- **D4.** The merged session guard and the slot registry this proposal keys the generation on stay closed.
  This proposal sequences after them and reads `checkSessionBound`.
- **D5.** The coordinator-loss hold stays pod-scoped, arms on the close of the pod's single
  CH-ADAPTEREVENTS stream, and terminates the same set of sessions it terminates today. Only the generation
  it reports moves onto the entry.
- **D6.** A session's fenced generation is unset until that session's first accepted fence on the pod. The
  stale rejection and the gap predicate are both defined against a recorded value, so both apply from that
  session's second accepted fence onward, and the exemption's unit is the session's binding on the pod
  rather than the pod's lifetime.
- **D7.** A `CheckpointBarrier` naming a bound session for which the pod holds no fenced generation is
  accepted and records no value. A barrier carrying a value that does not match one the pod does hold for
  the named session is still refused with `FailedPrecondition` and the `coordinator_handoff_stale` detail.
- CODE-1's lock order is the registry lock, then the entry lock, then the hold lock. The one
  opposite-order acquisition in the tree is the read CODE-3 removes.
- Relocating the `quiesced` flag onto the entry carries no specification claim about the quiescence unit,
  so a later implementor cannot read the field's placement as settling it.

Three questions adjacent to these are open rather than fixed, and `## Open decisions` carries each: the
comparison operator the barrier gate uses against a value the pod does hold (OD1), whether D7, the counter
baseline, and migration 0181 land in this proposal at all (OD5), and how the `spec/04` §4.1 message-scope
row is classified (OD3).

**Watch out for.** The spec-lane work covers `spec/10` under SPEC-1, `spec/28` and `spec/29` under
SPEC-2, and `spec/04` §4.2 under SPEC-3, and steps S1, S2, and S3 land them in that order. Landing SPEC-1
alone leaves `spec/28` and `spec/29` restating the pod-wide rule while citing the `spec/10` that now
contradicts them, which is the state SPEC-2 exists to prevent, and no tier-0 or tier-11 gate string-matches
those sentences.
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
the baseline makes equal to the session row's own value; §8 states those amendments alongside the
assertions the baseline shifts elsewhere. CODE-4's tier-2 case
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
answers. The archived pass records in the review log's archive carry the
rationale behind each correction already landed, and the review log's ledger carries the rest.

The hold's scope is settled rather than open. D5 keeps it pod-scoped, because the only arming signal the
adapter has is the close of the pod's single CH-ADAPTEREVENTS stream, which names no session. The cost is
recorded as a non-goal: a pod whose stream holder crashes freezes co-tenant sessions whose own coordinators
are alive. Proposal 0073 is converged and is not reopened; this proposal sequences after it.

**Impact on other proposals.** This is the only place this proposal states anything about another
proposal's continued validity. `## Non-goals` states what this proposal will not do and `## 10.
Dependencies` in the spec changes states what it applies after; neither restates a row below.

| Proposal | Status | What this change does to it | What it must do about it |
|:--|:--|:--|:--|
| 0060 | Implemented | Nothing. The lease protocol, forced acquisition, and the lease co-location 0060 built are untouched, and TEST-1 runs on the two-replica harness it landed. | Nothing. |
| 0073 | Implemented | Nothing. This change reads the `checkSessionBound` guard and the slot registry 0073 landed, and D4 keeps both closed. | Nothing. A landed proposal is not edited. |
| 0075 | Draft | Removes its sole counterexample. 0075 derives message scope from the address type, and its one exception is `CoordinatorFenceRequest`, on the ground that the handler mutates one pod-wide `coordinationState` so the session identifier selects nothing. CODE-1 deletes that field and the identifier then resolves the entry the generation is recorded on. OD3 puts the reclassification to the reviewer. | If OD3 reclassifies the `spec/04` §4.1 row to session scope, 0075 drops its retyping deliverable or restates its ground on a field CODE-1 does not delete. |
| 0080 | Draft | Invalidates two of its entries. It records a hold entered for one session and released by another session's fence as work this proposal takes, which D5 refutes, and it records the §29.10 hold-partitioning bullet as an outstanding gap while SPEC-2 stages that bullet's removal. | Correct both entries. 0080 is an early draft, so both are editable. |

## Goals

- The pod records and compares the coordination generation per bound session, so a fence for one session
  neither rejects, gap-flags, nor mis-attributes anything belonging to a co-tenant session.
- A co-tenant session's drain barrier is accepted and quiesces the pod, so the drain checkpoint captures a
  stopped workspace and the acknowledged-barrier record is written.
- `lenny_coordinator_handoff_stale_total` counts a genuine stale fence, and the
  `coordinator_generation_gap` event reports a genuine skip in one session's lineage.
- Each session a coordinator-loss hold terminates carries its own last fenced generation in its
  `coordinator_lost` log line and post-mortem record, or zero when no coordinator fenced it on that pod,
  and the pod-level arming event carries none.
- A session that never resumed and was never taken over is quiesced by its own preStop drain instead of
  being captured a second time against a live workspace.
- `spec/10`, `spec/04`, `spec/28`, `spec/29`, the adapter proto comments, and the adapter state one
  pod-side rule.

## Non-goals

- Renaming `CoordinatorFenceRequest`'s session field.
- Changing the scoping of the gateway or the Postgres side. Both are per session already. What changes on
  the Postgres side is the value `sessions.coordination_generation` starts at, under SPEC-3 and CODE-4.
  The lease protocol and §10.1.2 step 1's compare-and-swap stay as they are.
- Reopening the merged session guard or the slot registry this proposal keys the generation on.
- Forced acquisition, or any other change to the lease protocol. This proposal changes what the pod does
  with the generation it is handed.
- Changing which sessions a pod-level hold covers. D5 keeps the hold pod-scoped, and the residual is that a
  pod whose CH-ADAPTEREVENTS stream holder crashes freezes co-tenant sessions whose own coordinators are
  alive. Closing it would first require settling which replica's connection carries the pod's events when
  more than one replica holds one, which `spec/28`'s CH-ADAPTEREVENTS degradation row records the
  specification as not stating.
- Tightening `migrations/0050_session_record_fields.up.sql`'s `CHECK (coordination_generation >= 0)` to
  `>= 1`. Migration 0181 carries the two `DEFAULT 1` clauses and the backfill, and the two session-store
  `Create` floors are the whole enforcement of the baseline. §10.5's expand-contract rule places the
  tightening in a later phase, and OD9 records the question.
- Deciding whether two replicas may coordinate co-tenant sessions on one pod. The per-session
  `REG-COORDLEASE` already permits it, and this change removes a cross-session generation interlock that
  delayed and mis-metered co-tenant handoffs whatever the number of replicas involved (OD6).
- Repairing the defects listed under `### Defects in the shipped tree that this proposal does not stage`.
  None of them blocks sign-off.

## Open decisions

Every decision below is open and needs a reviewer's answer before this proposal moves to `Approved`.
Each was derived independently three times and then validated independently five more times against the
working tree, with the validators instructed to falsify each recommendation rather than confirm it. Where
a recommendation changed under that pass, the entry says so. An entry that leaves the list is restated in
place as withdrawn or replaced rather than deleted, because a decision list that quietly drops an item
teaches the next reader that it was answered.

**OD1. The barrier gate's comparison.** §10.1.8's gate compares the barrier's `coordination_generation`
with the session's last fenced value, and the question is whether that comparison stays equality.
**Recommendation: keep equality.** §10.1.2 step 2 makes an acknowledged fence a hard precondition for
every operational RPC, and widening admits a barrier carrying a value above what the pod holds. One
producer of such a value reaches a barrier: a successor between its compare-and-swap and its fence
acknowledgement. The barrier's cache-fallback path reads the live session row per binding
(`cmd/lenny-gateway/httpsurface.go:588-602`), so it can carry the successor's post-compare-and-swap value
while the pod is still fenced one below it. Widening would accept a barrier carrying a generation the
pod's coordinator has not yet installed there.
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

One row class escapes that argument, and the reviewer should weigh it before accepting the equal case. For
a session row an old binary minted at 0 during the rolling window, the resume path fences at the retained
`coordfence` floor's 1 while a takeover's `RecordHandoff` bumps 0 to 1 and fences at 1 as well, so two
replicas carry the same generation for that row after CODE-4 lands. The class is permanent, because 0181's
backfill runs once and `pgstore.Create` binds the struct value straight through, so accepting the equal
case admits for those rows the split-brain the ordering argument above says the baseline prevents.

The rationale carried in the earlier draft of this section was wrong and is withdrawn: `RecordHandoff`
(`pkg/gateway/coordination/coordination/coordination.go:463-480`) is an unconditional increment rather
than §10.1.2 step 1's compare-and-swap, a misattribution proposal 0052 already recorded. Uniqueness comes
from `pgstore.Update`'s `SELECT ... FOR UPDATE` inside a transaction, together with the baseline.

Whatever the reviewer decides about the code, SPEC-2's staged §28.5.1, §28.6, §28.8, and §29.8 arms
enumerate the older and the higher case and are silent on the equal one.
`schemas/lenny-adapter.proto:1456-1458` is the one carrier that states the fence's own acceptance
predicate, and SCHEMA-1 re-scopes it to the session while keeping the "not greater than" comparison the
shipped handler performs, so nothing this change applies contradicts the code on the equal case. Answering
this decision the other way moves that comment, `pkg/adapter/coordination.go:99`, and the staged §28.6
`CH-FENCE` arm together.

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
comment, and neither that file nor the tier-0 scope test is named in §9. The review log routed one
further knock-on that OD3 does not name: `CoordinatorFenceRequest` is the only Adapter-service example
`spec/04:151` carries, so reclassifying the row obliges a restatement of that line as well.

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
discovered. The specification half of the question is answered: `spec/07` fixes the `resuming → running`
transition as a re-attach on a replacement pod, so in specification terms a session does not return to the
pod it unbound from. What remains open is code-side reachability, because the adapter bars nothing and
nobody has checked the gateway placement path.

The decisions below reached this section from the review log's open list rather than from the derivation and
validation pass the entries above went through. Each states the ground the log entry gives, and an entry the
loop left without a recommendation says so.

**OD8 is withdrawn.** It asked whether CODE-4 deletes the gateway fence path's floor of a non-positive row
value at `pkg/gateway/coordination/coordfence/coordfence.go:147-153`, and the ground SPEC-1 gave for the
deletion was that a session row can no longer carry a non-positive value. That ground is false for the
rolling window: the migrate Job is a `pre-install,pre-upgrade` hook that completes before the gateway
Deployment rolls (`charts/lenny/templates/migrate-job.yaml:10-16`), and the still-running old fleet's
`pgstore.Create` binds `sess.CoordinationGeneration` straight through (`pgstore.go:177`, `:260`), so it
inserts rows at 0 that 0181's one-shot backfill has already run past. The design keeps the floor, and CODE-4
now states the reason: `fenceResumedPod` fences on the value it reads without bumping it, so with the floor
deleted that fence would carry 0, the adapter would refuse it with `InvalidArgument`, the refusal would fall
into `coordfence.fence`'s transient arm rather than its stale arm, and the driver would burn its attempt
budget, relinquish the lease, and abort the resume. The floor
is retired by the release that tightens the check to `CHECK (coordination_generation >= 1)`, which is OD9's
subject, so this rests on the same §10.5 argument that defers the tightening. The withdrawal also retires the
request for a human's eye on an amended `TestFenceZeroGenerationFencesAtBaseline`, because the test is left
as it landed.

**OD9. Whether a later release adds `CHECK (coordination_generation >= 1)` as a Phase-3 migration.** 0181
leaves `migrations/0050_session_record_fields.up.sql`'s `CHECK (coordination_generation >= 0)` in place, and
the two session-store `Create` floors are the whole enforcement of the baseline. The review recorded the
tightening as defensible defence in depth that costs a separate proposal, and recorded that nothing in this
proposal depends on it. The answer also settles the retained `coordfence` floor: OD8's withdrawal
conditions that floor's retirement on this tightening release, so answering OD9 no leaves the floor
permanent and owned by nothing. The coupling runs one way and is recorded in neither entry. No
recommendation was derived.

**OD10. Whether the sentences calling a barrier's generation the current one are edit sites.** SPEC-2 leaves
§10.1.8 step 1 and §29.7's trace step 4 unedited, on the ground that each names the session row value the
dispatcher copies onto the wire, which the baseline makes positive for every session. On the sweep that
performs a handoff, `pkg/gateway/coordination/coordination/coordination.go:430` passes the pre-bump snapshot
value to `upsertMirror` while the pod is fenced at the post-handoff generation minted in the same iteration, so
a barrier assembled from the mirror in that interval carries a value that is not the session's current one and
both sentences are false on that path. The mirror lag is recorded below as a shipped-tree defect this proposal
does not stage. No recommendation was derived.

**OD11. Whether this proposal stages a claim-register deliverable for the interval between S2 and S7.**
§28.4 requires every normative statement a section makes about a mechanism to carry a row in
`tests/claim-map.json`, and requires a row that is not `WIRED` to name, through a deferral identifier, the step
that closes it. SPEC-2 stages §28.5.1, §28.6, and §28.8 statements that do not hold in the shipped adapter until
CODE-1 and CODE-2 land, so the `CoordinatorFence` and `CheckpointBarrier` rows carry a status that is wrong for
that interval. Both rows are already present and `WIRED`, so the obligation to carry a row is met and the
subject is the status those rows carry. The register is generated from the root `gateway-runtime-comms.md`
§7.1 by `scripts/seed-claim-register.py` and byte-diffed at tier 0, so a status change lands in that source
document and in a regeneration rather than in the register file. §28.4's status set has no value for a mechanism
that is rescoped in part, so a deliverable closing this has to argue that the obligation falls on the statement
rather than on the mechanism. No deliverable stages it today, and no recommendation was derived.

**OD12. A superseded replica's checkpoint stream against a quiesced pod.** D7 has the pod accept a barrier
whose generation matches the value it holds for the named session, and the barrier's generation is read from
state the replicas share, so a replica that has lost coordination can have its barrier accepted. Whether the
stream that replica then opens against the quiesced pod is acceptable, and whether an accepted false-positive
barrier is followed by a stream at all, is unanswered. The review recorded the drain-budget cost as bounded to
one 90-second wall-clock window across all pods, with the manifest write guarded by supersede-on-write and
`partial_manifest_active_uniq`. No recommendation was derived.

**OD13. Whether TEST-1 owes a case for a barrier naming an unbound session.** D7 removes the generation gate's
refusal for a bound session the pod holds no fenced generation for, which leaves `checkSessionBound` the sole
fail-closed guard on the barrier path. No test in the tree asserts that `CheckpointBarrier` for a session with
no slot binding is refused. The gap was filed in the non-spec review and landed in neither TEST-1 nor §8. No
recommendation was derived.

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
| CODE-4 | `migrations/0181_sessions_coordination_generation_baseline.up.sql` and its `.down.sql`, `pgstore.go`, and `memstore.go` | The session row's counter is baselined at 1 in the schema and on both session-store create paths. |
| TEST-1 | `pkg/adapter/*_test.go`, `tests/tier4_integration`, and `tests/tier7a_load_local` | Two co-tenant sessions hand off, drain, and lose their coordinator independently, on proposal 0060's two-replica harness. |
