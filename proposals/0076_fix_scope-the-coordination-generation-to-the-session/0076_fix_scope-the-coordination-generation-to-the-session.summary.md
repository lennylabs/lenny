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
fence path's floor of a zero row in place for the rows the rolling window creates at 0. No sentence stating
that a gateway-to-pod message carries the session's current `coordination_generation` is restated. Each is
already false on the handoff path in the shipped tree, through the barrier-target mirror lag recorded below
under the defects this proposal does not stage, so this change creates no edit site there.

**What is fixed.** On a concurrent pod: a legitimate coordinator handoff rejected as
`coordinator_handoff_stale`, a drain barrier rejected so the drain checkpoint runs unquiesced, a
spurious `coordinator_generation_gap` event, a coordinator-lost post-mortem and log line that stamp
one session's generation on every session the hold terminates, and a split-brain counter that
increments on healthy handoffs. A hold released by an unrelated session's fence is not on that list:
the hold is pod-scoped by D5, so a fence from any session on the pod is the correct exit from it. The
coordinator-lost repair on that list is reached in the test suite alone until a gateway client opens the
CH-ADAPTEREVENTS stream, which the defects section records. D7 and the counter baseline together add a
barrier repair that is not specific to a concurrent pod. The
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
- `lenny_coordinator_handoff_stale_total` stops counting a co-tenant session's legitimate lower
  generation as a stale fence, and the `coordinator_generation_gap` event reports a genuine skip in one
  session's lineage. The counter's two other false producers are outside this change and are recorded
  under the defects it does not stage.
- Each session a coordinator-loss hold terminates carries its own last fenced generation in its
  `coordinator_lost` log line and post-mortem record, or zero when no coordinator fenced it on that pod,
  and the pod-level arming event carries none. The hold's only arming signal is the close of a stream no
  gateway code opens, so this goal is reached in the test suite and in no deployed system until deferral
  R12 lands the client. The gap is recorded under the defects this change does not stage.
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
Widening also fixes nothing: the refusal that occurs on the healthy path comes from the mirror
carrying a value *below* the pod's, which a wider comparison rejects just as equality does. The staged
text already writes equality in every carrier, so this decision costs no edit. §4's stated ground for it
is false and every validator confirmed the falsification independently. That sentence is proposal prose
under a draft header and lands in no file under `spec/`, so correcting it is proposal hygiene rather than
a specification risk. Replace it with step 2's precondition.

**OD2. A fence carrying the generation the pod already recorded.** When a fence fails or times out,
§10.1.2 step 2 orders the new coordinator to retry "with the same generation value"
(`spec/10_gateway-internals.md:39`), and the shipped pod refuses that retry.
`pkg/adapter/coordination.go:99` rejects `gen <= lastFenced` with `FailedPrecondition`, and the fence
driver reads every `FailedPrecondition` as generation-stale, re-reads the row, finds no advance, and
relinquishes the lease (`pkg/gateway/coordination/coordfence/coordfence.go:164-179`). The collision fires
inside a single `Fence` call: attempt one lands, its acknowledgement is lost, the driver's transient arm
retries at the same value, and attempt two is refused as stale. A handoff that succeeded costs its lease,
a sweep cycle, and one increment each of `lenny_coordinator_handoff_stale_total` and
`lenny_coordinator_fence_relinquished_total`. The question for the reviewer is whether this proposal
stages the change that makes the pod accept the equal case, or a successor owns it.

**Recommendation: a successor owns it, and the staging here stands as it is. Confidence: moderate.**
Nothing staged in this proposal depends on the equal case being accepted, and SPEC-2's staged §28.5.1,
§28.6, §28.8, and §29.8 arms enumerate the older and the higher case and are silent on the equal one, so
the applied specification is compatible with either answer. The recommendation would flip on a staged
deliverable whose correctness requires the equal case, and there is none.

The weight of a "yes" sits in the contract rather than in the code. CODE-1 moves `lastFenced` and
`initialized` off `Server.coord` onto the slot entry, so the predicate at `:99` is rewritten by this
proposal whichever way the decision goes, and the remedy is one comparison operator on a line CODE-1
already edits. What a "yes"
adds is the behavioural change and its carriers: the `accepted` sentence in the
`CoordinatorFenceResponse` comment (`schemas/lenny-adapter.proto:1456-1458`), which is the one carrier
that states the fence's own acceptance predicate and which SCHEMA-1 otherwise re-scopes to the session
while keeping the "not greater than" comparison the shipped handler performs; the staged §28.6 `CH-FENCE`
arm; a §8 test case; and a checklist step. A "yes" also commits the platform on a point `spec/` leaves
open, because step 2 orders the retry without stating that the pod must honour it and the gap clause
governs only the higher case, and it reverses the strictly-monotonic handler that `BUILD-GAPS.md` records
as finding F-4.7.2's resolution.

The residual a "yes" accepts is permanent. For a session row an old binary minted at 0 during the rolling
window, the resume path fences at the retained `coordfence` floor's 1
(`pkg/gateway/coordination/coordfence/coordfence.go:147-153`) while a takeover's `RecordHandoff` bumps 0
to 1 and fences at 1 as well, so two replicas carry the same generation for that row and accepting
equality admits a genuine split-brain there. CODE-4's baseline does not reclaim the class, because 0181's
backfill runs once and `pgstore.Create` binds the struct value straight through.

One alternative was weighed and lost. Fixing the gateway alone, so that a re-fence at the recorded value
keeps the lease rather than counting as a stale handoff, does not avoid the wire question: the adapter
returns its `CoordinatorFenceResponse` alongside a non-OK status
(`pkg/adapter/coordination.go:102-106`), so the client drops the body and returns a zero-valued result
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56`), and the driver cannot tell a re-fence at
the recorded value from a genuinely stale one without parsing the detail string or changing what the
adapter returns.

The cost of the recommendation is that until the successor lands, every lost fence acknowledgement keeps
costing its lease and one increment of each metric above, one of which is a split-brain counter. That
defect is recorded under the defects this proposal does not stage, as the fence driver's conflation of
three failure classes.

**OD3. `spec/04` §4.1's message-scope row for `CoordinatorFenceRequest`, and proposal 0075.** This entry
carries two questions. The second is reached only if the first is answered yes.

**Question A. After this change, is `CoordinatorFenceRequest` session-scoped?** §4.1's table declares it
pod-scoped (`spec/04_system-components.md:175`), and the paragraph under the table grounds that on the
message carrying `session_id` and staying pod-scoped, "which is why the classification is declared rather
than derived" (`:188`). CODE-1 removes the pod-wide `coordinationState` from `Server` and records the
generation on the slot entry the request's identifier resolves, so the ground that sentence states is gone.

**Recommendation: yes, reclassify the row to session scope and rewrite the declaring sentence, naming the
pod-scoped hold exit as the one pod-wide effect that remains under D5. Confidence: moderate.** §4.1 carries
the precedent at `:190`: `ShutdownRequest` is declared session-scoped although its handler runs the
whole-pod scrub, and the paragraph there rejects the reading that a field's presence stands in for a scope.
After CODE-1 the identifier selects the entry the fence writes, which is what §4.1 treats as an address.

One alternative was weighed. Leaving the row pod-scoped rests on the fence keeping a genuinely pod-wide
effect: accepting a fence is the only way out of hold state (`spec/10_gateway-internals.md:57`), the hold
timeout terminates every session the adapter started on the pod (`:58`), and D5 keeps the hold pod-scoped.
That reading is defensible, which is why the answer is a reviewer's rather than a derivation. It loses on
the `ShutdownRequest` precedent, which classifies by what a request addresses rather than by how broad its
handler's effect is. Nothing in the tree forces either answer. The tier-0 gate accepts either word in the
scope cell (`tests/tier0_static/adapter_proto_message_scope_test.go:75-79`), and the tier-3 session-address
suite excludes the message from the map its scope and reservation assertions iterate, under a comment
claiming the session-address arm covers it
(`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:38-44`).

**A "yes" costs a third `spec/04` edit and decides proposal 0075.** The paragraph at `:151` grounds
declaring the classification rather than deriving it on `session_id` appearing on messages of both classes.
After the reclassification no pod-scoped row carries a session field, the other pod-scoped `Adapter`
messages carrying none by `:188` and `ReportPodScrubRequest` declaring `pod_id` alone
(`schemas/lenny-adapter.proto:492-496`), so `:151` needs a replacement ground or the declared-rather-than-
derived rule is retired. That rule is proposal 0075's subject. 0075 derives message scope from the address
type and `CoordinatorFenceRequest` is its sole counterexample, on the stated ground that the handler
mutates one pod-wide `coordinationState` so the identifier selects nothing. CODE-1 deletes that ground and
a "yes" deletes the counterexample, which leaves 0075's retyping deliverable without a subject. 0075 is an
unreviewed draft, so the cost of obsoleting it is low. The reviewer answers Question A knowing that they
are deciding it. The staged spec changes file this under §6 as a rename whichever proposal lands second
rebases onto, which understates it.

**Question B. If yes, does this proposal stage the `spec/04` §4.1 edit, or does a successor?**
**Recommendation: a successor, and the staging here stands as it is. Confidence: moderate.** No `spec/04`
§4.1 edit is staged in this proposal; SPEC-3 edits §4.2's session-record paragraph alone. Staging a "yes"
here adds three edit sites (`:151`, `:175`, and `:188`) and a rewrite of the tier-3 comment that claims
the fence is covered, and `## 9. Files touched on application` in the staged non-spec changes names neither
that suite nor the tier-0 scope test that reads the table. It also puts the replacement ground for `:151`,
which is the contract question 0075 owns, inside a proposal whose subject is the generation's scope.

What each other answer costs. "Yes, here" widens the deliverable as above and settles §4.1's
declared-rather-than-derived rule in the same change. "Yes, successor" leaves the applied specification
carrying a `:188` sentence this change falsifies until that successor lands, and no gate catches the
contradiction in the interval. "No" keeps `:188` as written and keeps 0075's counterexample standing, at
the price of a declared classification that disagrees with what the message addresses once CODE-1 lands,
and of 0075 owing a restated ground for the exception on a field this proposal deletes.

**OD4 is withdrawn.** It asked whether to state the barrier quiescence unit or to delete a design claim,
and the validation pass found unanimously that it was mis-stated. The design claims that §10.1.8 step 3
fixes *the gate's* unit at the session, which is about the `barrierGate` rather than the `quiesced` flag,
and that claim is true and CODE-1 depends on it. SPEC-2 already stages the narrowing that leaves
§29.10's clause unanswered. Nothing is owed and nothing should be deleted. The strongest ground for not
stating the unit is one no earlier reading found: the only quiescence primitive the specification
defines, the `checkpoint_request` frame in §28, carries no session identifier and is delivered to the
pod's shared runtime process, so stating the session would commit the specification to a unit the wire
contract underneath it cannot express. CODE-1 should state that relocating `quiesced` onto the entry
carries no specification claim about the quiescence unit, so a later implementor does not read the
field's placement as settling it.

**OD5. Whether D7, the counter baseline, and migration 0181 land in this proposal or in a successor.**
This proposal's subject is the unit the coordination generation is scoped to, and it also carries D7 (the
pod accepts a barrier naming a bound session it holds no fenced generation for), SPEC-1's §10.1 baseline
paragraph, SPEC-3's §4.2 baseline sentence, CODE-4's two session-store `Create` floors, and production
migration 0181, which runs as a blocking `pre-install,pre-upgrade` Helm hook at weight -5 that completes
before the gateway Deployment rolls (`charts/lenny/templates/migrate-job.yaml:10-16`). A "split" answer
deletes SPEC-3 in full, SPEC-1's §10.1 baseline paragraph, CODE-4, migration 0181, and both `Create`
floors from this proposal and moves them into a successor that carries the four together. That is the
largest effect any open decision here has on the staged text. A "land here" answer keeps them and costs no
edit.

**Recommendation: land them here. Confidence: moderate.** The scope change forces the rest. Once
`initialized` is per session, a bound session for which the pod holds no fenced generation becomes an
ordinary reachable state that the current specification has no rule for, so SPEC-1 must state one, and
that rule is D7. D7 in turn forces the baseline: the adapter refuses a non-positive
`coordination_generation` with `InvalidArgument` (`pkg/adapter/coordination.go:224-226`) before the gate
at `:236-239` is reached, so for an ordinary never-handed-off session whose row still reads 0, D7's
acceptance arm is unreachable. Both halves were verified against the shipped tree. The confidence is
moderate rather than high because the forcing chain argues that the four move together and does not argue
that they move here, which is the reviewer's judgement.

Two alternatives were weighed. **Splitting all four into a successor** loses on the interval it opens:
SPEC-1's unset arm ships specified with no reachable code behind it, and the successor must still amend
the same landed tests this proposal already schedules (`non-spec-changes.md:335-367`). It stays a live
option, because a successor carrying D7, SPEC-3, CODE-4, and 0181 together preserves the deliverable
exactly, so inseparability is an argument for co-landing rather than for co-landing here. **Splitting only
SPEC-3, CODE-4, and 0181 and keeping D7 here** loses outright: without the baseline, the acceptance arm
D7 states is unreachable on the ordinary path for the same `InvalidArgument` refusal, so that answer
separates the rule from the only condition under which it fires.

What each answer costs. **Landing here** has the reviewer approve a production migration and both
session-store create paths inside a proposal whose subject is the generation's scope. A migration carried
by a behaviour proposal has precedent here: proposal 0049 carried
`migrations/0179_sessions_credential_deny.up.sql` as part of a credential-propagation fix. Precedent does
not rank the trade, because the good at stake is the reviewer's own willingness to approve that migration
inside this change, which no artifact in the tree spends on their behalf. **Splitting** leaves a
spec-to-code gap on the barrier path until the successor lands.
`.claude/rules/spec-driven-development.md:23` states that ordering as the normal one, "A spec change lands
and is verified before the code that depends on it is written", so the gap a split opens is not a rule
violation and settles nothing either way. Nothing else in the tree ranks the two costs: no rule bars a
migration from riding inside a fix proposal, 0181 is free under either answer
(`migrations/0180_drop_checkpoint_slot_id.up.sql` is the last number taken), and no other proposal turns
on it.

What follows the answer. OD14, which asks whether 0181's backfill runs unbatched and what bounds a
migration's run time, travels with 0181: a split moves that question into the successor rather than
answering it. OD10's withdrawal stands under either answer, on its own statement that it does not rest on
the baseline. One repair the earlier draft credited to the baseline, the first crash-takeover fence
rejected at 1, is also reached by OD2's remedy, so it is not exclusive to CODE-4 and does not weigh here.

**OD6 is replaced.** Its central claim was that the pod-wide field is an accidental mutual exclusion
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

**OD7. Rebind and the unset state.** This entry reached the section from the review log rather than from
the derivation and validation pass the preamble above describes, and it was then read three times
independently. Under this change a session's fenced generation lives on its slot registry entry (D2), and
every path that ends a session's binding deletes that entry. `Shutdown` (`pkg/adapter/session.go:238`) and
the hold teardown (`pkg/adapter/holdstate.go:192` through `pkg/adapter/slotsession.go:361`) each call
`deregisterSlotLocked`, which deletes the map key outright (`pkg/adapter/slotsession.go:174-188`), and the
start and resume rollback paths reach the same deletion through `releaseSessionSlot`
(`pkg/adapter/slotsession.go:214-215`). A session that unbinds from a pod and later binds to that same pod
therefore returns with its fenced generation unset, where the shipped pod-wide field retains its value: `s.coord.lastFenced` is written only by an accepted
fence (`pkg/adapter/coordination.go:120-121`) and nothing clears it. Under D6 and D7 the unset state exempts
that session's next fence from both the stale rejection and the gap predicate, and has its next barrier
accepted recording no value.

The reviewer is asked two things. **Is that reset accepted as the behaviour of the per-session value?** And
**does SPEC-1 state the value's lifetime as the session's binding on the pod rather than as the session's
first accepted fence on that pod?** The second question exists because the staged text already carries both
forms. SPEC-1 stages "The value is unset until that session's first accepted fence on that pod"
(`spec-changes.md:142`), which a rebind onto the same pod falsifies, while D6's closing clause names "the
session's binding on the pod rather than the pod's lifetime" (`summary.md:56-59`), which holds whether or
not such a rebind is reachable. D6 is a fixed decision whose two halves disagree with each other, so
answering the second question aligns D6 with itself rather than reopening it.

**Recommendation: accept the reset, and take the binding form. Confidence: moderate on the first half, high
on the second.**

The ground for accepting the reset is the blast radius. Three sites in the shipped adapter read the value:
the fence's own stale and gap predicates (`pkg/adapter/coordination.go:99`, `:108`), the barrier gate
(`:233-239`), and the coordinator-loss hold's report of the generation it holds
(`pkg/adapter/holdstate.go:119`). No operational RPC is gated on it, and the resume path fences immediately
after binding (`pkg/gateway/sessionserver/start.go:3975`, `:4067`, through `fenceResumedPod` at `:4233`), so
a rebound session's window with no recorded value closes on that fence. Confidence is moderate rather than
high because the residual is a real loss against the shipped tree and how often it can be incurred is
unmeasured, which is stated under the unverified fact below.

The ground for the binding form is that it is true under both readings of a fact nobody has established.
Shipped §10.1.2 states no initial condition for the value at all (`spec/10_gateway-internals.md:40` carries
the gap bullet with no lifetime clause), so SPEC-1 writes new text under either answer, and the entry's
lifetime is fixed by the code this proposal also writes: the entry is created on the first reference to the
slot identifier (`pkg/adapter/slot.go:82-101`) and destroyed at unbind.

Three alternatives were weighed. **Accepting the reset and keeping SPEC-1's "first accepted fence on that
pod" wording** loses on truth conditions rather than on style: it is correct only while a rebind onto the
same pod is unreachable, and nobody has traced that. **Carrying a tombstone that outlives the registry
entry, so the value survives an unbind**, loses to D2, which is fixed and states that the value lives on the
slot registry entry with no second map keyed by session identifier (`summary.md:47-48`). **Keeping the value
pod-wide for the rebind case** loses to D1, which is the proposal's subject.

What each answer costs. **Accepting the reset and taking the binding form** costs one staged sentence at
`spec-changes.md:142`, and aligning D6's first sentence at `spec-changes.md:33` and `summary.md:56` with its
own closing clause. No deliverable, checklist step, or test moves, and SPEC-2 carries SPEC-1's statements
into `spec/28` and `spec/29` rather than restating a unit of its own, so the mirrors follow the one edit.
**Keeping the staged wording** costs no edit now and ships a specification sentence whose truth depends on
an untraced code path. **Refusing the reset** is not reachable without reopening D1 or D2, which is a
different proposal.

One fact behind this entry is unverified and the recommendation does not wait on it. In specification terms
a session does not return to the pod it unbound from: `spec/07_session-lifecycle.md:196` fixes
`resuming → running` as a re-attach on a replacement pod. Code-side reachability is open, because the
adapter bars nothing and no one has traced whether `pkg/gateway/sessionserver` placement can put a session
back on a pod it unbound from. That question stays here rather than moving to
`### Defects in the shipped tree that this proposal does not stage`, which holds only findings confirmed
against the working tree. It bounds how often the residual can be incurred and it changes neither
recommendation, because the binding form holds under both answers.

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

**OD9. Whether a later release adds `CHECK (coordination_generation >= 1)` as a Phase-3 migration.** This
entry is about a release after this one. Nothing this proposal stages moves either way, and the answer is
not derivable from the specification, so it is here for a reviewer to settle.

Migration 0181 carries `DEFAULT 1` on both columns and the backfill and leaves the session row's
`CHECK (coordination_generation >= 0)` (`migrations/0050_session_record_fields.up.sql:38-39`) as it stands.
The reason is the rolling window: the migrate Job is a Helm `pre-install,pre-upgrade` hook that completes
before the gateway Deployment rolls, so a `>= 1` check would be live while the still-running old fleet
inserts an explicit zero through `pgstore.Create`. §10.5 places a constraint that old-version writes violate
in a Phase 3 migration in a subsequent deployment (`spec/10_gateway-internals.md:429`, `:432`). The baseline
does not rest on the check either way: `Create` names `coordination_generation` in its insert column list,
so the column default baselines nothing and CODE-4's two session-store `Create` floors are the whole
enforcement (`non-spec-changes.md:130-135`). No reader of the check exists in the tree.

**The question for the reviewer: is a successor commissioned to tighten the check to `>= 1` in a later
release, or is the retained `coordfence` floor accepted as permanent?** The second half is what gives the
question a consequence. CODE-4 keeps `pkg/gateway/coordination/coordfence/coordfence.go:147-153`, the fence
path's floor of a non-positive row value, and states that the floor is retired by the release that tightens
the check (`non-spec-changes.md:154-155`); OD8's withdrawal above carries the same coupling. That sentence
is the only thing in the repository that owns the floor's retirement. `BUILD-GAPS.md`, `TEST-GAPS.md`, and
`PROPOSAL-QUEUE.md` carry no finding or queued proposal for it, and no file outside this proposal mentions
the tightening. Answering no therefore makes the floor permanent, correct, and owned by nothing.

**No recommendation is offered, and no ground for one exists.** The specification does not compel the
constraint: §10.5's Phase 3 rule is a permission and an ordering ("The `NOT NULL` constraint may only be
added in Phase 3, after all replicas run the new code", `spec/10_gateway-internals.md:432`), and `spec/`
carries no DDL constraint text for this column anywhere. What is left is a trade between defence in depth on
a column already floored in both `Create` paths and the cost of a separate proposal, which the repository
cannot settle. **Confidence in the framing: high.** Both facts the answer turns on, that nothing in this
release depends on the check and that nothing else owns the floor, are verified against the working tree.

Two alternatives were weighed. **Commissioning the successor** costs a proposal and a Phase 3 migration
file, which under §10.5 must open with a preflight verification block whose count query captures the
un-migrated rows for the affected column, here the rows still carrying a value below 1
(`spec/10_gateway-internals.md:434`), and it must wait the §10.5 minimum inter-phase interval,
`maxSessionAge` for the session table (`:433`). It buys a database-level guarantee and an owner for the
floor's retirement. **Accepting the floor as permanent** costs no edit anywhere and leaves a defensive
branch in the fence path that nothing will ever remove. Removing OD9 from this list without answering it is
a third option and it loses: the floor's retirement would then be recorded only inside a sentence in a
landed proposal's staged text, which no later reader is looking at.

One qualification bears on a yes. The tightening would not on its own discharge the floor. The fence reads
the generation through `coordfence.GenerationReader`, wired in production to the configured session store
(`cmd/lenny-gateway/metricsbackfill.go:76-82`, `cmd/lenny-gateway/main.go:373-380`), which is the Postgres
store only when a pool exists and otherwise the in-memory store (`cmd/lenny-gateway/stores.go:1015`,
`:1035`); and the in-memory store's §17.4 restore path replaces its map wholesale from JSON without passing
through `Create` (`pkg/gateway/session/sessionstore/memstore/snapshot.go:27-37`). A Postgres check constrains
one of the two stores behind that interface, so the release that retires the floor has to establish that no
configured store can deliver a non-positive value, rather than resting on the check alone.

**OD10 is withdrawn.** It asked whether the sentences calling a barrier's generation the current one are edit
sites, and it named the wrong deliverable. SPEC-1 owns `spec/10_gateway-internals.md`, and SPEC-1 is what
states that §10.1.8 step 1's clause reading that the `CheckpointBarrier` message carries the current
`coordination_generation`, and §29.7's trace step 4, are not restated (`spec-changes.md:259-271`). SPEC-2's
`spec/29` scope names §29.7's framing paragraph and leaves trace step 4 alone (`spec-changes.md:435-437`).
SPEC-1 also does not leave §10.1.8 step 1 unedited: it rewrites step 1's closing sentence
(`spec-changes.md:206-222`), and that closing sentence sits on the same physical line of
`spec/10_gateway-internals.md` as the clause this entry is about, so a reviewer told that step 1 is untouched
does not open the rewrite. Neither sentence is an edit site, on the ground SPEC-1 now states. On the sweep
that performs a handoff, `pkg/gateway/coordination/coordination/coordination.go:430` passes the pre-bump
snapshot value to `upsertMirror` while the pod is fenced at the post-handoff generation minted in the same
iteration, so a barrier assembled from the mirror in that interval carries a value that is not the session's
current one, and both sentences are already false on that path in the shipped tree. That lag is recorded below
as a shipped-tree defect this proposal does not stage, and this change neither creates nor repairs it. SPEC-1's
earlier ground, that each stays true because the baseline makes the row value positive, reached the right
conclusion by a route that does not hold, and SPEC-1 now closes on the mirror lag instead. This disposition
does not rest on the baseline, so a decision to split D7, the baseline, and migration 0181 out under OD5
leaves it standing.

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

**OD14. Whether migration 0181's backfill runs unbatched, and what bounds a migration's run time.** 0181
backfills the counter with a single `UPDATE sessions SET coordination_generation = 1 WHERE
coordination_generation = 0` over the whole table, and the migrate Job is a `pre-install,pre-upgrade` hook
that completes before the gateway Deployment rolls, so the update's duration is added to every upgrade.
Two review readings reached opposite answers and neither corrects the other. Against the unbatched form:
`sessions` rows are never purged, the table reaches order 10^6 rows within a year at Tier 3, the predicate
matches essentially every row, and the landed `UPDATE ... SET` backfills in the tree all target small
configuration tables, so the precedent does not cover a session-scale one. For it: a backfill that wins the
row lock cannot corrupt a concurrent writer, because `RecordHandoff` goes through `Update`'s
`SELECT ... FOR UPDATE` and both stores clamp to the previous value, and unbatched backfills have precedent
in migrations 0053, 0054, 0058, 0064, 0105, and 0178. The two grounds do not meet: one is about the hook's
duration and the other about correctness.

**The second question for the reviewer: does this proposal write down a wall-clock or row-count budget for
a migration run against a live top-tier fleet, or does the backfill's duration stay unbounded as every other
migration's is?** A decision to keep the backfill whole is also a decision on this one, because the
unbatched form's duration is added to every upgrade and no landed backfill runs against a table of the
session table's size. This half of the entry reached the section from the review log's `### Open` list
rather than from the derivation pass, and its premise was re-verified against the working tree in this
round.

Nothing in the repository states such a budget. §10.5 supplies expand-contract phasing, `golang-migrate`
tooling, pre-deploy-hook execution, an advisory lock, and re-runnability after a partial completion, and its
one row-count number, the operator confirmation of the gate query plan for tables exceeding 1 million rows
(`spec/10_gateway-internals.md:434`), is scoped to a Phase 3 gate's `COUNT(*)` preflight rather than to a
Phase 1 backfill. The chart does not bound the Job either: `charts/lenny/templates/migrate-job.yaml` renders
`backoffLimit` (`:42`) and `ttlSecondsAfterFinished: 600` (`:45`) and no `activeDeadlineSeconds`, and the
`migrate` values block exposes only `backoffLimit: 5` (`charts/lenny/values.yaml:3791`). The chart bounds a
Job where the spec gives it a number and leaves it unbounded otherwise, so the omission is per Job rather
than uniform: `charts/lenny/templates/preflight-job.yaml:259` and
`charts/lenny/templates/crd-validate-job.yaml:81` render `activeDeadlineSeconds` from
`preflight.timeoutSeconds` and `charts/lenny/templates/backup-job.yaml:134` renders 7200, while the
migrate, bootstrap, MinIO lifecycle, and deployment-config-sync hook Jobs render none. `spec/` states those
two deadlines and no third (`spec/17_deployment-topology.md:571`, `spec/25_agent-operability.md:3995`), so
writing a budget for the migrate Job means writing the number as well as the knob. The only bound in force today is
whatever the deployer passes to Helm, and the documented upgrade command passes `--wait --timeout 10m`
(`docs/operator-guide/upgrades.md:22`), which is an example command rather than a stated contract.
`docs/runbooks/schema-migration-failure.md` states no expected or maximum run time.

**No recommendation is offered on whether the platform needs the budget.** The trade is between an
operational guarantee and the cost of owning one, and neither `spec/` nor the chart takes a position the
repository can read off. **Confidence in the framing: high.** The three facts the answer turns on were each
verified against the working tree: no budget is stated anywhere, the chart bounds the preflight,
crd-validate, and backup Jobs and not the migrate Job, and §10.5's only row-count number belongs to the
Phase 3 gate.

What each answer costs. **Writing the budget here** widens this proposal from session-scoping the
coordination generation into migration policy, because the remedy lands in §10.5 or in the migrate Job
template and this proposal stages an edit to neither. Its files-touched list carries `spec/10`'s §10.1
subsections, `spec/28`, `spec/29`, `spec/04`, the proto, migration 0181, the two session stores, and tests,
and no chart file (`non-spec-changes.md`, `## 9. Files touched on application`). **Leaving it unwritten**
keeps the scope and leaves the migrate Job's run time bounded by nothing inside the release, so a backfill
that blocks on a row lock runs until the deployer's own Helm timeout fires with the Job still running.
**Commissioning a successor** carries the same edit into a proposal whose subject is migration policy and
costs the delay. It is the option that survives an answer of yes to the budget and no to writing it here, and
nothing owns it today: `BUILD-GAPS.md` and `PROPOSAL-QUEUE.md` carry no finding or queued proposal for the
migrate Job's deadline.

**OD15. Whether `checkpointBarrierAckTimeoutSeconds` is sized for the sessions D7 admits into the shared
window.** The gateway sends every barrier at once and waits for all acks under one wall-clock deadline
(`checkpointBarrierAckTimeoutSeconds`, default 90s), which the shipped code implements as a single
`context.WithTimeout` around the whole fan-out (`pkg/gateway/podlifecycle/prestop/prestop.go:503`). The
admission floor on that deadline is `checkpointBarrierAckTimeoutSeconds >= max_tiered_checkpoint_cap`
(`spec/10_gateway-internals.md:140`), one workspace tier cap with no session or slot multiplier. D7 newly
admits into that window a class of session the pod previously refused there: one that has neither resumed
nor been taken over, so the pod holds no fenced generation for it. The question is whether the floor should
carry a multiplier covering the whole coordinated set, or whether the residual is accepted and recorded.
**Recommendation: accept the residual and record it, on the ground that the specification already sizes the
window for the coordinated set across pods and the remaining gap is a co-tenancy question wider than this
proposal's subject. Confidence: high on the ground below, moderate on the recommendation, because the
co-tenant half of the question has no answer in the tree either way.**

Half the question already has the specification's own answer with a stated justification. §10.1.8 step 3
says the deadline is deliberately one window across pods: the gateway "issues the `CheckpointBarrier` to all
pods simultaneously ... and waits for `CheckpointBarrierAck` from all of them under a single wall-clock
deadline ... not per-pod", which "is why the **gateway pod's** grace-period budget ... adds
`max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` once rather than multiplying by
session count" (`spec/10_gateway-internals.md:185`). The same paragraph names the modelled worst case, up to
400 simultaneous uploads at Tier 3, and ties it to the §17.8.2 "burst, max workspace" row, whose derivation
sizes the store for "every active session checkpoints a 512 MB workspace"
(`spec/17_deployment-topology.md:1218`). Across pods the window is sized for the whole set by design, and
the sessions D7 admits are active sessions on pods that are already in the barrier-target set.

The half the specification does not answer is two sessions on one pod. §5.2 states that "Eviction
checkpoints for concurrent-session pods are serialized across slots (not fully parallel)", that "The adapter
processes one slot's checkpoint upload at a time", and that the pod's total preStop budget is "the **sum**
of the per-session caps across the sessions active on the pod"
(`spec/05_runtime-registry-and-pool-model.md:546`). §10.1's justification for the single window is
parallelism across pods and says nothing about co-tenant slots, and D7 together with CODE-1's per-slot gate
is what puts two co-tenant sessions of one pod inside one 90-second window that carries no slot multiplier.
The two sections are unreconciled in the shipped tree, which is why the review split on this entry.

The residual is bounded. A session that enters the window and does not ack is not abandoned: `Acked` is set
only after a successful send (`pkg/gateway/coordination/barrier/barrier.go:237`), and the post-barrier
per-session loop skips only acked sessions (`pkg/gateway/podlifecycle/prestop/prestop.go:395`), so an
unacked session still receives its own capture under its own tier cap, on a wall-clock budget the loop
starts fresh after the barrier returns (`prestop.go:380`). The cost of a slow co-tenant upload is drain
wall-clock against the pod's real grace period rather than a lost checkpoint.

The alternatives, and why each lost:

- **Widen the BarrierAck floor so it multiplies the cap by the coordinated set or by
  `maxConcurrentSessions`.** Lost on scope. It edits the §10.1 rule at `spec/10_gateway-internals.md:140`
  and the webhook that enforces it (`pkg/admission/pool_config_validator/validator.go:626-649`), and §9's
  file list carries no CRD field, no chart value, no admission code, and no timeout constant. It also
  crosses the §10.1 across-pods design against the §5.2 same-pod serialization rule, and reconciling those
  two is a wider specification question than the unit the coordination generation is scoped to.
- **Withdraw the entry as already answered by §10.1.8 step 3.** Lost because that paragraph's justification
  is parallelism across pods, and what D7 newly admits is a co-tenant slot on one pod, which §5.2 says
  serializes against its neighbour.
- **Accept the residual and record it.** Recommended. The staging does not move and the gap is written
  down where a later change to the floor can find it.

What the other answer costs: widening the floor adds a §10.1 spec edit, a webhook change, a CRD-field
documentation update, and a tier-2 admission case to a proposal that stages none of them, and it reopens
the §17.8.2 capacity derivation the current floor rests on. Accepting the residual costs a co-tenant
worst case that no evidence in the tree prices against the 90-second default.

### Items §7 lists, and how they should be dispositioned

- **`coord.mu`.** Not a reviewer decision. `coordinationState` embeds its mutex as its first field, so D2
  moves the lock with the struct. Delete the item as an implementation choice with no external
  consequence rather than as a settled design question. CODE-1 owes the resulting lock order, registry
  lock then entry lock then hold lock, and the ordering was checked for an inverse path and has none;
  CODE-3 removes the one opposite-order acquisition that exists today.
- **A fence for a session the pod holds no entry for.** The earlier draft of this section called this
  settled, and the validation pass found that wrong by a majority. The adapter half is settled, because
  `checkSessionBound` refuses before the generation is read. The half §7 asks, whether such a
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

- **The barrier-target mirror lags one generation across a takeover.** On the sweep iteration that
  performs a coordinator handoff, the sweeper mints the post-handoff generation through `RecordHandoff`
  and fences the pod at it (`pkg/gateway/coordination/coordination/coordination.go:371`), then passes the
  pre-bump value from its own List snapshot to `upsertMirror` (`:430`), so the `coordination_lease` row
  carries the prior generation while the pod holds the new one. The barrier assembly reads that row
  (`pkg/gateway/coordination/barrier/wiring.go:104-114`) and the dispatcher puts its value on the wire
  (`:49`), so every drain barrier assembled from the mirror in that interval is refused under any
  comparison operator. This is what makes §10.1.8 step 1's description of the value as current
  (`spec/10_gateway-internals.md:183`), and §29.7 step 4's
  (`spec/29_communication-scenarios.md:1186`), false on the healthy path.

  The window is bounded and the outcome inside it fails safe. `upsertMirror` runs for every lease the
  replica holds on every sweep, from a fresh List row, so the next sweep writes the post-handoff value;
  the sweep cadence defaults to 15s
  (`pkg/gateway/coordination/coordination/coordination.go:182-185`). Inside the window the pod refuses
  with `FailedPrecondition`, the dispatcher marks the target stale and leaves `Acked` false
  (`pkg/gateway/coordination/barrier/barrier.go:230-237`), and preStop's post-barrier per-session loop
  therefore captures that session instead of skipping it
  (`pkg/gateway/podlifecycle/prestop/prestop.go:395`). The cost is an unquiesced capture rather than a
  lost one.

  This proposal records the lag and stages no repair. Repairing it is a change to the gateway sweeper,
  which the Non-goals exclude. SPEC-1's ruling that §10.1.8 step 1 and §29.7 step 4 are not edit sites
  rests on both sentences already being false through this lag
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:259-271`), so staging a
  repair here would turn both into edit sites this proposal has not scoped, one of them in a file SPEC-1
  already owns. Nothing this proposal stages depends on the mirror agreeing with the session row: the
  never-handed-off session this change repairs has never run `RecordHandoff`, so its mirror value equals
  its row value. No other proposal and no `BUILD-GAPS.md` finding owns the repair, which is why it is
  written down here.
- **The fence driver conflates three failure classes into one metric.** `Fencer.fence`'s rejection arm
  matches on the gRPC status code alone
  (`pkg/gateway/coordination/coordfence/coordfence.go:164`) and increments
  `lenny_coordinator_handoff_stale_total` before it discriminates (`:170`), so the counter records three
  distinct adapter refusals as one. The three are a genuine stale fence, where the requested generation is
  at or below the value the pod recorded (`pkg/adapter/coordination.go:99`, refused at `:105-106`); a
  fence for a session the pod's slot registry does not hold bound, which `checkSessionBound` refuses with
  the same code before the generation is read (`pkg/adapter/slotsession.go:271-273`); and a re-fence at
  the already-recorded generation after a lost acknowledgement, which reaches the same
  `gen <= lastFenced` predicate at `:99`. The status code is all the driver has to go on, because the
  adapter returns its response body alongside a non-OK status and the client drops the body
  (`pkg/gateway/runtime/adapterclient/coordinatorfence.go:55-56`). §16.1's row for the counter states that
  it increments on a generation-stale rejection (`spec/16_observability.md:183`), and the shipped tree
  already contradicts that row for two of the three classes.

  This change removes one producer of false increments and leaves the conflation. The pod-wide generation
  makes a co-tenant session's legitimate lower value read as stale, and recording the value per bound
  session ends that class. The other two are independent of the unit the generation is scoped to and
  survive unchanged. OD2 owns the third: it puts the equal case to the reviewer, recommends that a
  successor stage the pod-side change, and records this entry as where the standing cost is written down.

  This proposal records the conflation and stages no repair. No deliverable touches the stale arm. CODE-4
  cites `coordfence.go` only to keep its existing non-positive floor
  (`0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md:146-155`), and §9's file
  list names no file under `pkg/gateway/coordination/coordfence/`. No staged spec sentence states what the
  counter counts, so nothing this proposal lands becomes false while the conflation stands; `spec/16` is
  not an edit site here. Separating the classes means naming the new counter or label and adding its §16.1
  row, which is an observability surface this proposal does not open, and it means changing what the
  adapter returns on a refusal or parsing its detail string, which OD2 records as the reason a
  gateway-only fix does not reach the case.
- **A tier-3 comment claims a coverage that does not exist.** The comment above `sessionScopedMessages`
  in the session-address contract suite places `CoordinatorFenceRequest` outside that map and states
  that it and the two messages beside it "are covered by the session-address arm below alone"
  (`tests/tier3_contract/adapter_session_address/session_address_wire_test.go:40-43`). No arm covers it.
  The three assertions that reach a message by name iterate `sessionScopedMessages` (`:81`, `:102`,
  `:130`), which the comment's own exclusion keeps the fence out of, and the fourth walks the file for
  the retired wrapper type and names no request at all (`:150-158`).

  The exclusion itself is correct. `CoordinatorFenceRequest` declares `session_id` and
  `coordination_generation` and reserves nothing (`schemas/lenny-adapter.proto:1447-1453`), and the
  commit that introduced the duplicate address put it on `InterruptRequest`, `SignalDeadlineRequest`,
  `ResumeRequest`, `CheckpointBarrierRequest`, and `ReportUsageRequest` alone (`01d19af01`), so the fence
  never carried the field whose number the map records against each member. What is false is the coverage
  clause, which tells a reader that the fence's address is pinned somewhere in the file when it is pinned
  nowhere.

  This proposal records the false clause and stages no repair. No deliverable touches the file: `## 9.
  Files touched on application` names no path under `tests/tier3_contract/`
  (`0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md:380-421`), and TEST-1's
  targets are `pkg/adapter/*_test.go`, `tests/tier4_integration`, and `tests/tier7a_load_local`. Nothing
  staged moves the clause in either direction. The comment's membership rule is §4.1's message-scope
  table, that table still classifies the fence as pod-scoped (`spec/04_system-components.md:175`), and
  the only `spec/04` edit staged here is SPEC-3's on §4.2. No gate turns on the classification either:
  the tier-0 scope test accepts either class word in the cell
  (`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`), and the suite asserts nothing about
  the fence.

  The entry is conditional on OD3. A "yes" to OD3's Question A makes the fence session-scoped, and OD3
  already names the rewrite of this comment among the costs of staging that answer here rather than
  leaving it to a successor (`0076_fix_scope-the-coordination-generation-to-the-session.summary.md:283-289`).
  On that branch the repair is the addition of the fence to the suite rather than the deletion of a false
  clause, and it has to settle what value a map recording each member's retired field number holds for a
  message that never declared one. Repairing the comment now would pre-commit to whichever of the two the
  reviewer has not yet chosen.
- **The gateway has no `CH-ADAPTEREVENTS` client.** `tests/claim-map.json` files the client side of the
  stream as `UNWIRED` under deferral R12 (`tests/claim-map.json:513-518`), and the tree agrees. Outside
  the generated code, the only reference to the stream under `pkg/gateway` or `cmd/` is a comment
  (`pkg/gateway/runtime/adapterclient/client.go:464`), and the one production construction of an adapter
  client (`:38`) exposes no method that opens it. The pod's coordinator-loss hold has a single arming
  path: the `defer` in `Server.AdapterEvents` calls `onCoordinatorChannelClosed`
  (`pkg/adapter/adapterevents.go:100-108`), which calls `enterHoldState` when the pod has started a
  session (`pkg/adapter/holdstate.go:90-100`), and `enterHoldState` has no other caller outside the
  tests. The hold therefore never arms in a deployed system.

  What the gap makes unreachable is the deployed path rather than the code. Tier 2 opens the real stream
  through the generated client and drops it
  (`tests/tier2_component/slotrelease/revoke_double_teardown_test.go:309-336`), and
  `TestHoldTerminatedSessionDecrementsItsSlotExactlyOnce_spec_10_1` (`:365`) drives the hold timeout
  through `terminateHeldSession` (`pkg/adapter/holdstate.go:225`), which is where CODE-3's
  `coordinator_lost` log line and post-mortem record live (`:278-307`); tier 9 opens and drops the same
  stream (`tests/tier9_security/adapter_hold_termination_surface_test.go:87-90`). CODE-3's per-session
  records are therefore pinned by the suite and reach no operator until R12 closes. The same absence
  reweights D5's recorded cost and leaves the residual §6 of the staged spec changes records, a pod whose
  CH-ADAPTEREVENTS stream holder crashes freezing co-tenant sessions whose own coordinators are alive
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:606-612`), unreachable
  until then.

  This proposal records the gap and stages no client. R12 owns the client, and building it is a whole
  channel implementation: the dial, the reconnect policy, and the arbitration of which replica's
  connection carries the pod's events, which §28.8's CH-ADAPTEREVENTS row records the specification as
  not stating (`spec/28_communication-channels.md:1810`). Nothing staged here becomes wrong or
  unimplementable while the client is absent. CODE-3 is a change to `holdstate.go` and `slotsession.go`
  that compiles and is pinned by the tier-1 hold case §8 amends
  (`0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md:243-249`), and SPEC-1's
  §10.1.4 text and SPEC-2's §29.10 hold bullet state what the pod must do when the hold fires, which an
  unwired trigger does not falsify. Recording that separation is what the `UNWIRED` row exists for. The
  absence also bounds the risk of the one production-path behaviour change CODE-3 makes, which no
  deployed system reaches today.

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
