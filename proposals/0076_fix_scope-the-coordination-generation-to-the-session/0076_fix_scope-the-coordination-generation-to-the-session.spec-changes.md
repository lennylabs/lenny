# Spec changes: Scope the coordination generation to the session

The design prose below motivates both the specification and the code, so it is carried here. The staged
edit under `spec/` follows it.

## 2. Decisions

**D1. The fenced generation is per session, matching the specification and Postgres.** The pod side stops
holding one value for the process and holds one per bound session.

**D2. The generation lives on the slot registry entry rather than in a second map.** Proposal 0073
establishes the slot registry as the per-session state the adapter already keys everything else on, and a
parallel map keyed by session identifier would be a second lifetime to get wrong.

**D3. The proto doc comment becomes true rather than being deleted.** It already states the intended
design.

**D4. 0073 is not reopened.** This proposal sequences after it and depends on `checkSessionBound`.

**D5. The coordinator-loss hold stays pod-scoped.** The fenced generation moves onto the slot registry
entry and the hold does not move with it. The hold's only arming signal is the close of the pod's single
CH-ADAPTEREVENTS stream, which names no session and which the adapter refuses to let a second replica open
(`pkg/adapter/adapterevents.go:80-96`, `pkg/adapter/holdstate.go:90-100`), so there is no per-session
signal a per-session hold could arm on. `holdState` carries `mu`, `active`, `timer`, and
`gen` and names no session (`pkg/adapter/holdstate.go:39-44`), and the set the timeout terminates is read
from the slot registry when the timeout fires rather than recorded at arming, deliberately, so a session
admitted between arming and firing is still covered (`pkg/adapter/holdstate.go:107-112`, `:172-176`,
`:192`). `spec/10` §10.1.4's "Whole-pod connection loss when `maxConcurrentSessions > 1`" paragraph already
fixes total connection loss as a whole-pod failure that puts every slot on the pod into `resume_pending`
and fires the whole-pod replacement trigger, so a partly held pod would contradict the gateway behavior the
specification states. What moves is the generation the hold reports, which SPEC-1 and CODE-3 carry.

**D6. A session's fenced generation is unset until that session's first accepted fence on the pod.** The
first fence for a session is recorded at whatever value it carries and is never a gap, and the gap predicate
and the stale rejection are both defined only against a recorded value, so both apply from that session's
second accepted fence onward. The pod admits no session-scoped RPC for a session before it is bound
(`checkSessionBound`, `pkg/adapter/slotsession.go:267`, refuses one that is absent or not yet bound), so a
session that has never been fenced on this pod has accumulated no state for the gap path's reset to act on.
This re-scopes an exemption that already exists rather than adding one. The adapter guards both the stale
rejection and the gap test on an `initialized` flag (`pkg/adapter/coordination.go:29-32`, `:99`, `:108`),
and the `CoordinatorFence` RPC doc comment states the rule on the wire as "The first call on a pod's
lifetime is never treated as a gap regardless of value" (`schemas/lenny-adapter.proto:161-162`). Only
`spec/` never carried it. What changes is the exemption's unit, from the pod's lifetime to the session's
binding on the pod. SCHEMA-1 carries the new unit onto that comment, because a wire comment left at the
pod's lifetime states the retired exemption once SPEC-1 lands.

**D7. A `CheckpointBarrier` naming a bound session for which the pod holds no fenced generation is
accepted.** §10.1.2 step 2 states the acceptance window for RPCs carrying a generation the pod already
holds, which lasts until a successor's fence is acknowledged (`spec/10_gateway-internals.md:38`); it does not
cover a session for which the pod holds no generation at all, which is the state D6 makes reachable. The
barrier is the only gateway-to-pod RPC the adapter validates on the generation gate, because
`coordination_generation` is read on the fence path and the barrier path alone
(`pkg/adapter/coordination.go:92`, `:223`), so refusing the barrier while every other RPC naming the same
session is served protects nothing. This changes shipped behavior, and the drain barrier of a session that
never resumed and was never taken over meets two refusals today. The only fence drivers are the resume path (`pkg/gateway/sessionserver/start.go:4237`),
which fences without incrementing the session row, and the sweeper's crash-takeover re-adopt
(`cmd/lenny-gateway/coordination_seams.go:233`), so a session that started normally and was never handed off
still carries `coordination_generation = 0` on its row
(`migrations/0050_session_record_fields.up.sql:38-39`). The barrier path copies that counter onto the wire
unchanged, through the lease mirror (`pkg/gateway/coordination/coordination/coordination.go:430`) or the
cache fallback (`cmd/lenny-gateway/httpsurface.go:592-599`) and then the dispatcher
(`pkg/gateway/coordination/barrier/wiring.go:49`). The adapter therefore refuses the barrier with
`InvalidArgument` on the non-positive generation (`pkg/adapter/coordination.go:224-226`) before the gate
`!initialized || gen != fenced` (`:236-239`) is reached, and the gate's `!initialized` arm refuses it once
the stamp is positive. D7 removes the second refusal, and the stamp rule SPEC-1 states in §10.1 removes the
first; the `!initialized` arm stays reachable for a session handed off whose successor's fence the pod has
not yet recorded. Either refusal costs the step-2 quiescence §10.1.8 states, leaves
the gateway-driven `Checkpoint` stream running against a live workspace, writes no acknowledged-barrier
record (`pkg/gateway/coordination/barrier/barrier.go:229-246`), and pushes the session into the post-barrier
eviction checkpoint that captures it a second time
(`pkg/gateway/podlifecycle/prestop/prestop.go:390-397`). The consequence is the same for both because
prestop branches on the acknowledgement alone (`pkg/gateway/podlifecycle/prestop/prestop.go:395`, `:510`)
while only `FailedPrecondition` maps to `ErrGenerationStale`
(`pkg/gateway/coordination/barrier/wiring.go:51-53`). A barrier carrying a generation that does not match
a value the pod does hold for the named session is still refused with `FailedPrecondition` and the
`coordinator_handoff_stale` detail, so a barrier from a superseded replica is still rejected on the stamp,
which is what the §28.8 `CH-BARRIER` row states and what §28.5.1's `CH-BARRIER` Exclusivity bullet
constrains. SPEC-1 stages the specification half in §10.1's generation-counter bullet, §10.1.2 step 3, and §10.1.8
step 1, SPEC-2 stages the §29.7 half, CODE-2 carries the gate change, and CODE-4 carries the stamp.

## 3. Design overview

`coordinationState` moves from `Server` onto the per-session slot registry entry. `CoordinatorFence`
resolves the entry for the named session and compares against that entry's `lastFenced`.
`CheckpointBarrier` does the same. Gap detection becomes per session and applies from a session's second
accepted fence onward, so a gap is a real skip in one session's lineage, and under D6 the first fence for a
session on a pod is recorded at whatever value it carries and is never a gap. Hold state does not move:
under D5 the hold stays pod-scoped, arms on the same pod-level signal, and terminates the same set. Only
the generation it reports moves onto the entry, so each terminated session's `coordinator_lost` record
carries its own last fenced generation, or zero when no coordinator fenced it on that pod, and the pod-level
arming event carries none.

## 4. Detailed design

**IMPLEMENTOR TO FILL THE BLANKS.** The per-entry move is straightforward and is not written out here. What
must be derived during convergence:

- What a fence means for a session the pod holds no bound entry for. Today the guard rejects it. Under
  0073's registry model a released session's entry may be absent, and a fence arriving for it is either a
  stale coordinator (reject) or a race with a bind (retry), and the two must be distinguishable. D6's
  initial condition holds under either resolution, because `checkSessionBound` rejects a fence for an
  unbound session before the generation is read (`pkg/adapter/coordination.go:89`,
  `pkg/adapter/slotsession.go:267`), so every fence that reaches the predicate names a bound entry.
- Whether `CheckpointBarrier`'s gate stays equality or becomes "at least the last fenced generation".
  Equality is what makes a barrier from a superseded coordinator fail; confirm that a per-session counter
  does not change which coordinators that catches.
- How the per-session gap reset is built, given that the adapter does not perform it today. SPEC-1 stages
  the reset's scope on the specification side, so the requirement is per session before any code is
  written. The adapter's gap branch logs `coordinator_generation_gap`, reports `gap_detected`, and then
  records the new value on the same path a non-gap fence takes
  (`pkg/adapter/coordination.go:108-121`), and its comment at `:112-113` records the cancellation as
  unimplemented. The staged text re-scopes the requirement and does not assert that the adapter meets it.
- Whether the pod-wide `coord.mu` becomes per-entry or stays one lock. A single lock is simpler and the
  critical sections are short; per-entry locking is only worth it if fences contend.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** These are indicative targets; the text is written during convergence,
against the post-0073 state of each file.

### SPEC-1. State the pod-side scope

`spec/10` §10.1.2 states the pod-side fenced generation as one state model covering its scope, its initial
condition, and what the gap path resets. The pod holds `last_fenced_generation` per bound session, and that
value is the one every gateway-to-pod RPC naming that session is validated against, so a handoff for one
session neither fences nor unfences another. The value is unset until that session's first
accepted fence on that pod, that first fence is recorded at whatever value it carries and is subject to
neither the stale rejection nor the gap predicate, and the gap predicate
`new_generation > last_fenced_generation + 1` applies from a session's second accepted fence onward. The gap
path's reset is per session: it cancels and discards the in-flight RPCs received for that session after that
session's last fenced generation, resets the transient tool-call and lifecycle state that session
accumulated since its last fenced coordinator, and logs `coordinator_generation_gap` recording that
session's two generations.

That text lands in sentences §10.1.2 already carries, and it adds no bullet. The sentences are these. In the
fence-announcement step, "The pod records the new generation and from this point rejects any RPC carrying an
older generation" gains "for that session". In the "Gap detection on the pod" bullet, the
`last_fenced_generation` parenthetical becomes "the generation from the last successfully acknowledged fence
for that session on this pod", the initial condition is stated immediately after that parenthetical and
before the clause list, and clauses (a), (b), and (c) gain the session qualifier. The predicate keeps its
current form, and clause (d) and the closing sentence stating that a gap of exactly 1 is the normal case are
unchanged. In step 3, the acceptance sentence and the no-window sentence that follows it become "The pod
accepts only RPCs whose generation matches the value it holds for the session the RPC names. A session for
which the pod holds no fenced generation has no recorded value to match, so the pod does not reject that
session's RPCs on generation grounds, and a `CheckpointBarrier` naming such a session is accepted and
records no value. Because fence confirmation is required before this step is reached, there is no window in
which both the old and the new coordinator for that session can simultaneously issue RPCs the pod accepts
for it." Step 3's opening sentence, which states that all
subsequent gateway-to-pod RPCs include the local generation stamp, is unchanged, and it keeps the whole set of
gateway-to-pod RPCs as the acceptance rule's domain.

Step 3 is an edit site because it restates the pod-side gate in the pod-singular form this change
retires: "The pod accepts only RPCs whose generation matches the fenced value"
(`spec/10_gateway-internals.md:41`) names one definite value for the pod, so applying the
fence-announcement edit alone would leave §10.1.2 stating the rule per session in step 2 and
pod-wide in step 3. It is also the sentence the adapter's `CheckpointBarrier` gate cites
(`pkg/adapter/coordination.go:228-231`), so CODE-2's per-session gate needs it re-scoped to have a
source. The unset-value clause states what the gate does with an operational RPC for a bound session
the pod holds no fenced value for, which D6's initial condition newly makes a reachable state, and
it states one rule across step 3's whole domain: the barrier follows the same rule as every other
gateway-to-pod RPC, which is D7. The adapter reads `coordination_generation` on the fence path and
the barrier path alone (`pkg/adapter/coordination.go:92`, `:223`), so the barrier is the only
handler that enforces step 3's gate today, and that gate is reached only after `checkSessionBound`
(`pkg/adapter/coordination.go:216`), so the clause governs a session already bound on the pod. The
drain barrier of the ordinary session that has neither resumed nor been taken over is refused today
for the two reasons D7 enumerates: the adapter's non-positive-generation `InvalidArgument` guard
(`:224-226`) refuses it before the gate is reached, because that session's row counter is still 0
and the barrier path copies it onto the wire unchanged, and the shipped gate then refuses it on the
absence of a recorded value for the named session (`:236-239`). Both §10.1.8's step-2 quiescence and
its closing interruption bound presuppose that neither happens (`spec/10_gateway-internals.md:184`,
`:198`). SPEC-1 therefore also stages §10.1.8 step 1 and the §10.1 stamp sentence, both below, and
CODE-2 and CODE-4 carry the matching code changes. The fence path's admission of an uninitialized
session (`:99`) answers a different question, which is D6's exemption for a session's own first
fence rather than the gate on the operational RPCs that follow it. The clause also agrees with the
two neighbouring statements of the same rule: §10.1's summary bullet has the pod reject when the
generation is stale (`spec/10_gateway-internals.md:30`, whose rejection sentence stands and which
SPEC-1 opens below only to add the stamp sentence), and the §28.5.1 Messages wording SPEC-2 stages
has the pod reject a generation older than the one it holds for that session. The comparison stays
equality, because §7's first open decision reserves the operator for the reviewer and this edit
changes only the unit of the value compared and the domain over which the comparison applies.

§10.1.8 step 1 states the barrier's own refusal as the only outcome, and it takes the qualifier D7
requires. Its closing sentence, which reads "Pods receiving a barrier for a session no longer
coordinated by this replica (a false-positive surviving the cache fallback) reject the barrier as a
generation-stale RPC under the normal fencing rules — this is safe and does not require special
handling" (`spec/10_gateway-internals.md:183`), becomes "Pods receiving a barrier for a session no
longer coordinated by this replica (a false-positive surviving the cache fallback) apply to it the
same generation rule as to any other gateway-to-pod RPC, stated in step 3 of the handoff protocol:
the pod rejects the barrier as a generation-stale RPC when it holds a generation for that session
that the barrier does not carry, and otherwise accepts it, quiescing the session and capturing the
checkpoint the drain would otherwise have taken. Either outcome is safe and requires no special
handling." The acceptance predicate itself is stated once, in §10.1.2 step 3, and §10.1.8 applies it
by reference. A restatement here in terms of which replica has fenced would be false in the ordinary
false positive, where the barrier carries the generation the acquiring replica's compare-and-swap
wrote and the pod still holds the value the draining replica fenced, so the fencing rules refuse the
barrier before the successor's fence lands and accept it after. The rest of §10.1.8 is unchanged:
steps 2, 3, and 4, the BarrierAck-timeout partial-capture rules, and the closing sentence bounding
the rolling-update interruption window to one in-flight tool call per session all hold under D7
rather than needing a qualifier, because a barrier the pod accepts establishes the step-2 quiescence
and the ack deadline stays the only failure arm §10.1.8 defines.

§10.1's "Generation counters" bullet states what a replica stamps, and it gains one sentence, because D7's
acceptance is unreachable unless the stamp a replica puts on a gateway-to-pod message for a session no
replica has taken over is positive. After "When a replica takes over coordination (via either mechanism), it
increments the generation" (`spec/10_gateway-internals.md:30`) the bullet states: a session that no replica
has taken over carries generation 0 on its row, and the coordinating replica stamps generation 1 on its
gateway-to-pod messages for such a session, so every generation a pod validates is positive. The fence path
already applies that rule in code, flooring a non-positive row value at 1 before it fences
(`pkg/gateway/coordination/coordfence/coordfence.go:147-153`), and the barrier path does not, so the two
senders of the same session's stamp disagree and only the fence path's value is well-formed. §10.1 is the
section that owns the counter's role on the wire, so the rule is stated there once. Two sentences elsewhere
state which value the barrier carries, and each becomes false for the session class this rule governs, so
each is restated to name the stamp rather than the row. §10.1.8 step 1's stamp sentence, which reads "The
`CheckpointBarrier` message carries the current `coordination_generation` and a `barrier_id` (monotonically
increasing per session)" (`spec/10_gateway-internals.md:183`), becomes "The `CheckpointBarrier` message
carries the coordinating replica's generation stamp for the session, as §10.1 states it, and a `barrier_id`
(monotonically increasing per session)", and §29.7's trace step 4 takes the same correction under SPEC-2.
Neither restatement names a value, so the drift this staging removes does not return. §28.5.1's `CH-BARRIER`
Messages bullet is not an edit site: it reads that `CheckpointBarrier` carries `coordination_generation` and
`barrier_id` (`spec/28_communication-channels.md:349`), which names no value, and its Preconditions bullet
already points at §10.1 for the stamp. CODE-4 carries the code half.

`spec/28` and `spec/29` carry mirrors of the same reset clauses, and SPEC-2 takes its wording from the
clauses written here so the mirrors do not state more than the section they mirror. Neither file states step
3's acceptance rule today. SPEC-2 stages it into `spec/29` §29.10 twice, in the "Partitioned per slot"
coordination bullet and in the narrowed barrier bullet, and each takes the acceptance sentence above. The
unset-value clause stays in §10.1.2, which owns the state model D6 states, because §29.10's two bullets
classify what is per slot and what is per pod and the clause adds no classification. SCHEMA-1 carries the
acceptance sentence onto the barrier-side wire comments SPEC-2's closing paragraph names, together with the
unset-value clause, which states the barrier's own gate.

§10.1.4's Observability bullet states that the pod-level
`coordinator_connection_lost` event names the number of started sessions the pod holds and carries no
generation, because the fenced generation is per session and no pod-level fenced generation remains. The
same bullet states what each session the hold timeout terminates carries. The per-session `coordinator_lost`
log line, and the local-disk post-mortem §10.1.4 has the adapter write when no coordinator ever returns
(`spec/10_gateway-internals.md:58`), each carry that session's own last fenced generation. A session no
coordinator fenced on that pod carries `0` on both, which reports the unset value D6 states rather than a
generation the pod holds. Zero names no fence, because §10.1.2 step 1 increments `coordination_generation`
before step 2 announces it (`spec/10_gateway-internals.md:37`), so no `CoordinatorFence` ever carries zero.
The gates that hold that today are the session row's `NOT NULL DEFAULT 0` column
(`migrations/0050_session_record_fields.up.sql:38-39`), the gateway's floor of a zero row at 1 before it
fences (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`), and the adapter's refusal of a fence
carrying a non-positive generation (`pkg/adapter/coordination.go:93-94`). The bullet also states that the
hold itself and the `lenny_adapter_coordinator_hold` gauge remain pod-scoped under D5, so the per-session
generation stated in §10.1.2 is not read as scoping the hold.

### SPEC-2. The mirrors state the same scope

`spec/28` and `spec/29` restate the pod-side fence rule and its gap reset without a session qualifier and
cite §10.1 as their source, and §29.7 states the barrier's rejection without the pod-side state that
rejection depends on. SPEC-2 carries the statements SPEC-1 makes into those two files, so the applied
specification states one pod-side rule rather than two incompatible ones. It edits the generation half of
each mirror and leaves the hold half alone: under D5 the hold's unit is the pod, and `spec/10` §10.1.4's
"Whole-pod connection loss when `maxConcurrentSessions > 1`" paragraph already fixes total connection loss
as a whole-pod failure, so qualifying a hold sentence per session would stage a retraction of both.

**`spec/28_communication-channels.md`.** The sentences below change.

- The `CH-FENCE` contract card's Messages bullet in §28.5.1. "The pod records the generation and from that
  point rejects any RPC carrying an older one" becomes "The pod records the generation against the session
  the fence names, and from that point rejects any RPC carrying a generation older than the one it holds for
  that session. A fence for one session does not change the generation the pod holds for another."
- The `CH-FENCE` card's Degradation bullet, its first sentence. It takes SPEC-1's §10.1.2 clause wording:
  when the announced generation exceeds that session's last fenced generation by more than one, the adapter
  cancels and discards the in-flight RPCs received for that session after that session's last fenced
  generation, resets the transient state that session accumulated since it, logs a
  `coordinator_generation_gap` event, and acknowledges the fence normally. The rest of the bullet, which
  states the hold and its timeout, is unchanged.
- §28.6's "One holder per session" paragraph, its `CH-FENCE` sentence. "and from the fence onward the pod
  rejects every RPC carrying an older generation" becomes "and from the fence onward the pod rejects every
  RPC carrying a generation older than the one it holds for that session". The paragraph already states that
  the unit of the exclusivity constraint is the session; this sentence states the pod's rejection rule,
  which the unit sentence does not entail, and which a pod-wide gate satisfies today.
- §28.8's `CH-FENCE` row, its "Holder of the exclusivity constraint changes" cell. The gap sentence the cell
  repeats takes the same re-scoping as the Degradation bullet, word for word. The cell's other sentences are
  unchanged, and the row stays one line carrying its cells, because the §28.8 matrix carries exactly one row
  per channel identifier in §28.3 and a tier-0 gate reads that correspondence in both directions.

SPEC-2 leaves the hold sentences in `spec/28` as they stand: the hold half of the `CH-FENCE` Degradation
bullet, §28.6's "The second opener on those channels" paragraph, and the "while the adapter is in hold
state" cells of the `CH-ATTACH`, `CH-CHECKPOINT`, and `CH-BARRIER` rows in §28.8. Each states the hold,
whose unit stays the pod under D5.

The §28.5.1 `CH-BARRIER` card's Exclusivity bullet and the §28.8 `CH-BARRIER` row's exclusivity cell are not
edit sites either. The Exclusivity bullet states the one-coordinating-replica-per-session constraint and
that the barrier carries the generation in its own message
(`spec/28_communication-channels.md:361-365`), and the §28.8 row's cell restates that constraint and adds
that a barrier from a superseded replica is rejected on the stamp (`:1808`). A superseded replica is one
whose successor's fence the pod has recorded, so the pod holds a value for that session, and both cells stay
true under D7 and under the per-session scope SPEC-1 states.

No §28.4 claim-register row moves. The rows in `tests/claim-map.json` name mechanisms and wire fields rather
than the scope a sentence states, and their anchors resolve to headings this change does not move, so that
file is not opened by this proposal.

**`spec/29_communication-scenarios.md`.** The §29.10 co-tenancy classification changes, §29.7's framing
paragraph applies the predicate §10.1.8 step 1 applies, §29.7's trace step 4 takes §10.1.8 step 1's corrected
stamp sentence, and steps 2 and 7 of the §29.8 crash-takeover trace change.

- §29.10's "What the specification does not state" list loses its first bullet, which asks whether the
  adapter's hold state is partitioned per slot and whether a fence driven for one slot's session holds the
  RPCs of a sibling slot's session. §10.1.2 and §10.1.4 state both answers once SPEC-1 lands, and the list's
  contract is that it holds questions the specification does not answer.
- §29.10's "Partitioned per slot" coordination bullet gains the pod-side half of the removed bullet's
  generation question. After "so each slot's session carries its own lease and its own generation" it states
  that the generation the pod records on a fence and validates every gateway-to-pod RPC against is the
  fenced session's, so a fence for one slot's session neither fences nor unfences another. The bullet keeps
  its existing text and its closing cross-reference to the "does not state" list, because the question that
  reference points at, whether two slots on one pod may be coordinated by two different replicas, stays
  unanswered.
- §29.10's "Shared by the whole pod" list gains a bullet for the coordinator-loss hold. Loss of the
  gateway-to-pod connection is a whole-pod failure, the hold rejects every inbound RPC on the pod other than
  `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` error detail, and the hold timeout
  terminates every session the adapter has started on the pod. A successful fence for any one of those
  sessions exits the hold for the pod, and the generation that fence records is the fenced session's alone.
  The bullet carries the citations the removed bullet carried, and it states this as settled under D5.
- §29.10's bullet asking whether the `Interrupt` RPC under the operation lock and the drain barrier are
  addressed to a slot is narrowed rather than removed. It states that the generation a barrier carries is
  validated against the fenced generation of the session the barrier names, and it keeps as unanswered the
  unit of the quiescence a barrier establishes and the addressing of the `Interrupt` RPC, neither of which
  this proposal settles.
- §29.7's framing paragraph names a barrier rejection the trace does not follow: "A barrier addressed to a
  session this replica no longer coordinates is rejected by the pod as a generation-stale RPC under the
  fencing rules" (`spec/29_communication-scenarios.md:1150-1152`). It takes the same predicate §10.1.8 step 1
  applies, in one clause at the level of detail §29.7 carries: the barrier is rejected as a generation-stale
  RPC under the fencing rules when the generation it carries does not match the value the pod holds for that
  session, and is accepted otherwise. Step 5 of the trace, on which the adapter quiesces, is unchanged, and
  the paragraph's set of named outcomes stays closed, because both arms are now named.
- §29.7's trace step 4 states which value the barrier carries: the replica sends `CheckpointBarrier`,
  "carrying the session's current `coordination_generation` and a `barrier_id` that is monotonically
  increasing per session" (`spec/29_communication-scenarios.md:1186`). It takes §10.1.8 step 1's corrected
  wording, so the clause reads that the replica sends `CheckpointBarrier` carrying the coordinating replica's
  generation stamp for the session and a `barrier_id` that is monotonically increasing per session. The step
  already cites §10.1, which is where that stamp is stated, so the clause adds no cross-reference. The rest
  of the step, which states the simultaneous fan-out to every pod in the barrier-target set and the single
  wall-clock deadline, is unchanged.
- §29.8 step 2's `coordinator_connection_lost` sentence carries "the last known generation". It takes
  SPEC-1's §10.1.4 Observability statement: the event names the number of started sessions the pod holds and
  carries no generation.
- §29.8 step 7 states the fence rule and its gap reset a second time. Both halves are re-scoped: the
  record-and-reject half takes the §28.5.1 Messages wording above, and the gap half gains the session
  qualifier on the same clauses SPEC-1 re-scopes in §10.1.2, so the cancellation covers the fenced session's
  in-flight RPCs received after that session's last fenced generation, the reset covers the transient
  tool-call and lifecycle state that session accumulated since its last fenced coordinator, and the logged
  event records that session's two generations.

The gap reset is stated in §10.1.2, in the `CH-FENCE` card's Degradation bullet, in the §28.8 `CH-FENCE`
row, and in §29.8 step 7. Each mirror keeps the level of detail it carries today and gains the session
qualifier on the clauses SPEC-1 re-scopes, so no mirror states more or less than the section it mirrors.

Further mirrors sit outside `spec/`, so SPEC-2 does not reach them. The wire mirrors the record-and-reject
rule and the acceptance rule, and SCHEMA-1 carries onto each carrier the wording the section that owns that
rule states, so the wire text and the applied specification state one rule apiece.

The record-and-reject rule is carried by the `CoordinatorFence` RPC doc comment
(`schemas/lenny-adapter.proto:153-162`), which states the rule, the gap reset, and the first-fence
exemption, each with the pod as the unit; by the message-level `CoordinatorFenceRequest` comment
(`:1442-1446`), which states that the pod records the new generation and from that point rejects every RPC
carrying a strictly older generation; and by the `CoordinatorFenceResponse` comment (`:1455-1462`), which
repeats the stale-fence sentence and the gap sentence. Each takes the §28.5.1 Messages wording above: the
pod records the generation against the session the fence names, and from that point rejects every RPC
carrying a generation older than the one it holds for that session. The `CoordinatorFence` RPC comment also
takes D6's unit for the exemption, so it reads as the first fence for that session on this pod, and its gap
sentence takes the session qualifier the Degradation bullet takes. The
`CoordinatorFenceRequest.coordination_generation` field comment (`:1449-1451`) already states per-session
monotonicity, which D1 and D3 make true, and it keeps its wording.

The acceptance rule is carried by the `CheckpointBarrier` RPC doc comment (`:165-179`), by the message-level
`CheckpointBarrierRequest` comment (`:1469-1474`), and by that message's `coordination_generation` field
comment (`:1477-1479`). Each states the gate against "the last fenced generation" with the pod as the
unit. Each takes the §10.1.2 step 3 wording SPEC-1 stages, so the barrier's generation is validated against
the value the pod holds for the session the request names, and the message-level and the field-level text do
not disagree about the comparison.

The remaining `coordination_generation` field comments in the file state the gateway's view of the active
coordination generation for the session and describe the validation neutrally, so this change does not make
them wrong and they are not edit sites. SCHEMA-1's own target list is in the non-spec changes and names the
request-field comments alone; the review log's ledger records the correction and carries the same carrier
list in the same order.

## 6. Non-goals

- **Renaming `CoordinatorFenceRequest`'s session field.** That is proposal 0075's subject. If both land,
  whichever is second rebases onto the first.
- **Changing the gateway or Postgres side.** Both are already per session and are correct.
- **Reopening 0073.**
- **Forced acquisition or any change to the lease protocol.** Proposal 0060 built the lease co-location and
  crash-takeover path; this proposal changes only what the pod does with the generation it is handed.
- **Changing which sessions a pod-level hold covers.** D5 keeps the hold pod-scoped, and it carries a
  residual: a pod whose CH-ADAPTEREVENTS stream holder crashes freezes co-tenant sessions whose own
  coordinators are alive. That follows from carrying one control stream per pod over a per-session
  coordination lease, and closing it would first require settling which replica's connection carries the
  pod's events when more than one replica holds one, which `spec/28`'s CH-ADAPTEREVENTS degradation row
  records the specification as not stating. It is a coordination-topology question rather than a
  generation-scope one.

## Resolved in adversarial review

### Pass 1 (2026-08-31, automated)

- **§7's hold-state decision was framed against a `holdState` that carries a session, and the struct carries
  none.** `holdState` is `{mu, active, timer, gen}` (`pkg/adapter/holdstate.go:39-44`), it names no session,
  and the set it terminates is read from the slot registry when the timeout fires rather than recorded at
  arming (`pkg/adapter/holdstate.go:107-112`, `:172-176`, `:192`). Both options in the deleted item were
  costed against that false baseline, and the per-session branch had no arming signal to build on, because
  the only arm is the close of the pod's single CH-ADAPTEREVENTS stream
  (`pkg/adapter/adapterevents.go:80-96`, `pkg/adapter/holdstate.go:90-100`). The decision is now settled in
  §2 as D5, the hold stays pod-scoped, the open-decisions item is deleted and the remaining items are
  renumbered, §6 records the residual as a non-goal, and the same false claim in the problem statement's
  §1.3 is corrected to the record defect that path actually carries.
- **§10.1.4's `coordinator_connection_lost` event required a pod-wide generation that a per-session counter
  cannot supply.** The Observability bullet in `spec/10` §10.1.4 requires the arming event to carry "the
  last known generation", filled today from the pod-wide accessor
  (`pkg/adapter/holdstate.go:119`, `pkg/adapter/coordination.go:44-48`) that CODE-1 removes, and the same
  value is stamped on every terminated session's `coordinator_lost` line and post-mortem record
  (`pkg/adapter/holdstate.go:225-229`, `:283-296`). SPEC-1 now stages the Observability bullet: the
  pod-level event reports pod-level facts and carries no generation, and the per-session generation moves
  onto the per-session `coordinator_lost` record and post-mortem, read from the terminated session's own
  registry entry. The mirrored sentence in `spec/29` §29.8 is staged under SPEC-2.

### Pass 2 (2026-08-31, automated)

- **Nothing staged exempted a session's first fence from gap detection, so every co-tenant bind read as a
  gap.** §10.1.2 states the gap predicate as `new_generation > last_fenced_generation + 1` with no clause
  for the case where no prior fence exists, and with one pod-wide counter that case arises once per pod.
  Once the counter is per session, a session binding to a pod at a generation well above 1 has no recorded
  value of its own, so the predicate as written reported a gap on the first fence of every session on the
  pod and the applied specification would not have delivered the spurious-event fix this proposal claims.
  The exemption the adapter already relies on (`pkg/adapter/coordination.go:29-32`, `:99`, `:108`) and the
  `CoordinatorFence` doc comment already states (`schemas/lenny-adapter.proto:161-162`) was never written
  into `spec/`. It is now settled in §2 as D6 and staged in SPEC-1 as one state model rather than as an
  exception appended to the predicate: the value is per bound session and unset until that session's first
  accepted fence, the first fence is recorded at whatever value it carries and is subject to neither the
  stale rejection nor the gap predicate, and both apply from a session's second accepted fence onward.
  SPEC-1's staged §10.1.2 text also carries the session qualifier onto clauses (a), (b), and (c) of the gap
  bullet's reset, because those clauses read the very value this change makes per session and because
  SPEC-2's mirrors in `spec/28` and `spec/29` take their wording from them. §3's gap sentence and §4's
  reset bullet carry the same predicate.

### Pass 3 (2026-08-31, automated)

- **`spec/28` restated the fence and its gap reset with the pod as the unit, and no edit list named the
  file.** The `CH-FENCE` contract card's Messages bullet states that the pod records the generation and
  rejects any RPC carrying an older one, its Degradation bullet states that a gap makes the adapter cancel
  every in-flight RPC received after the last fenced generation and reset the transient state accumulated
  since it, §28.6's "One holder per session" paragraph states that from the fence onward the pod rejects
  every RPC carrying an older generation, and the §28.8 `CH-FENCE` row repeats the Degradation sentence.
  Each becomes false once the generation is per session: on a pod holding one session at a higher generation
  than another, the lower session's fence carries a generation older than the one the pod last recorded and
  must now be accepted, and a gap in one session's lineage must not cancel a co-tenant's in-flight RPCs or
  reset its state, which is the cross-session bleed this proposal removes. SPEC-2 now stages those four
  sentences. The hold sentences in the same card, in §28.6's "second opener" paragraph, and in the
  `CH-ATTACH`, `CH-CHECKPOINT`, and `CH-BARRIER` rows are left as they stand, because D5 and §10.1.4's
  whole-pod connection-loss paragraph both state the pod as the hold's unit and qualifying them per session
  would stage a retraction of both. SPEC-2 records that no §28.4 claim-register row moves, because the rows
  in `tests/claim-map.json` name mechanisms and wire fields rather than the scope a sentence states.
- **SPEC-1 answered a question `spec/29` §29.10 records as unanswered, and no edit list named that file
  either.** §29.10's "What the specification does not state" list opens with a bullet asking whether the
  adapter's hold state is partitioned per slot and whether a fence driven for one slot's session holds the
  RPCs of a sibling slot's session. Once §10.1.2 and §10.1.4 state both answers, that bullet asserts the
  specification is silent on a fail-closed control the specification now states, and §29.10's "Partitioned
  per slot" and "Shared by the whole pod" lists no longer classify the generation and the hold. SPEC-2 now
  stages the bullet's removal and relocates its content: the generation half joins the "Partitioned per
  slot" coordination bullet, and the hold half becomes a "Shared by the whole pod" bullet stating the
  whole-pod failure, the pod-wide rejection, the pod-wide timeout, and the pod-level exit on any one
  session's fence. The barrier and `Interrupt` bullet is narrowed rather than removed, because this proposal
  settles the barrier's generation gate and settles neither the unit of the quiescence a barrier establishes
  nor the addressing of the `Interrupt` RPC. SPEC-2 also carries the two `spec/29` §29.8 mirror sites, step
  2's `coordinator_connection_lost` sentence and step 7's copy of the fence rule and its gap reset, so the
  file has one staging home.
- **SCHEMA-1 stopped short of the wire text D6 re-scopes.** D6 moves the first-fence exemption's unit from
  the pod's lifetime to the session's binding on the pod, and names the `CoordinatorFence` RPC doc comment
  as the wire carrier of the retired unit (`schemas/lenny-adapter.proto:153-162`). That comment also states
  the record-and-reject rule and the gap reset with the pod as the unit, and the `CoordinatorFenceResponse`
  comment repeats both (`:1455-1462`). SCHEMA-1 names the `CoordinatorFenceRequest.coordination_generation`
  and `CheckpointBarrierRequest.coordination_generation` comments alone, so the wire would state the retired
  rule after SPEC-1 and SPEC-2 land, which is the mirror defect SPEC-2 was added to close. D6 and the mirror
  paragraph closing SPEC-2 now state that SCHEMA-1 carries the qualifiers onto both comments. SCHEMA-1's
  target list is in the non-spec changes, which this loop may not edit, and the required text is in the
  review log's ledger.
- **Pass 3's findings named the applied-edit list and the checklist step, and only the staged prose landed.**
  SPEC-2 stages edits in `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, and
  the files-touched list still carries `spec/10_gateway-internals.md` as its only spec entry while step S1
  still names SPEC-1 and §10.1 alone. An implementor working the checklist applies SPEC-1, marks S1 done,
  and never reaches SPEC-2, which leaves §10.1.2 per session and the two mirrors pod-wide. Both sites are
  outside this loop's writable set. The summary's watch-out paragraph and the review log's ledger carry the
  correction.
- **The hold-state item pass 1 deleted left two live pointers to it.** CODE-3 reads
  "`pkg/adapter/holdstate.go`, per §7." and step S5 reads "CODE-3. Hold state takes the scope §7's decision
  settles." §7 now carries the barrier gate, the unheld-session fence, and `coord.mu`, and settles nothing
  about the hold. The decision both should cite is D5, whose closing sentence already routes the work
  through CODE-3, and §3 states what CODE-3 does. Both sites are outside this loop's writable set, and the
  replacement text is in the review log's ledger.
- **TEST-1's pinning case still asserts the behavior D5 settles as correct.** The case requires that the
  first session's hold is not released by the second session's fence, and requires the case to fail against
  the pre-fix code. D5 keeps the hold pod-scoped and the adapter exits it on every accepted fence
  (`pkg/adapter/coordination.go:129`), so that assertion fails both before and after the change. The
  assertion the change does create is that each terminated session's `coordinator_lost` record and
  post-mortem carries its own last fenced generation while the pod-level `coordinator_connection_lost` line
  carries none. The testing section is outside this loop's writable set, and the replacement assertion is in
  the review log's ledger.
- **The status file still frames the hold decision as open and the hold release as a defect.** Its scope
  bullet lists "releases its coordinator-loss hold" among the consequences, and its closing paragraph states
  that the §7 hold-state decision is genuinely open and is the substance of this change. D5 settles the
  scope and the summary already removes the hold release from the defect list. The status file is outside
  this loop's writable set, and the correction is in the review log's ledger.

### Pass 4 (2026-08-31, automated)

- **§10.1.2 states the pod-side gate in step 3 as well, and SPEC-1's edit list did not name it.** Step 3
  reads "The pod accepts only RPCs whose generation matches the fenced value"
  (`spec/10_gateway-internals.md:41`), which names one definite value for the pod and is the pod-singular
  state SPEC-1's own model retires. Applying SPEC-1 as it stood would have left §10.1.2 stating the rule per
  session in step 2 and pod-wide in step 3, so on the concurrent pod this proposal exists to repair, step 2
  requires a co-tenant's RPC at its own generation to be accepted and step 3 requires it to be rejected. Step
  3 is also the sentence the adapter's `CheckpointBarrier` gate cites
  (`pkg/adapter/coordination.go:228-231`), so CODE-2's per-session gate had no source, and SPEC-2's §29.10
  sentence stating that the pod validates every gateway-to-pod RPC against the fenced session's generation
  stated more than the §10.1 section it mirrors. SPEC-1 now stages step 3's acceptance sentence and the
  no-window sentence that follows it, keeping equality as the comparison so §7's first open decision is
  untouched, and states the refusal for a bound session the pod holds no fenced generation for, which D6's
  initial condition newly makes reachable and which the adapter answers differently on the fence path and on
  the barrier path today (`pkg/adapter/coordination.go:99`, `:236-239`). SPEC-1's state-model paragraph gains
  the clause that the per-session value is what every gateway-to-pod RPC naming that session is validated
  against, and §7's first open decision records the paths that issue `CoordinatorFence`, so the reviewer can
  see which sessions hold no fenced generation when they decide the operator.
- **SPEC-2's wire-mirror paragraph named the RPC and response comments and missed the message-level ones
  that carry the same retired rule.** The message-level `CoordinatorFenceRequest` comment states that the pod
  "rejects every RPC carrying a strictly older generation" with the pod as the unit
  (`schemas/lenny-adapter.proto:1442-1446`), and it is a different comment from the
  `CoordinatorFenceRequest.coordination_generation` field comment SCHEMA-1 already names (`:1449-1451`). The
  message-level `CheckpointBarrierRequest` comment (`:1469-1474`) and the `CheckpointBarrier` RPC comment
  (`:165-179`) state the barrier gate against "the last fenced generation" with the same unit. Left as they
  stood, the published gateway-to-adapter wire contract would reject a co-tenant session's RPC that the
  applied specification accepts. The closing paragraph now enumerates every wire carrier, split by which of
  the rules it states, with the record-and-reject carriers taking the §28.5.1 Messages wording and the
  barrier carriers taking the §10.1.2 step 3 wording, and it records that the remaining
  `coordination_generation` field comments are already session-scoped and are not edit sites. SCHEMA-1's
  target list is in the non-spec changes, which this loop may not edit, so the review log's ledger carries
  the corrected list and the summary's watch-out sentence indexes the same one.
- **Correction to this pass: step 3's refusal clause barred every operational RPC for a session no
  coordinator had fenced, which is the ordinary case.** The clause as first staged refused every RPC naming a
  session the pod holds no fenced generation for, and step 3's unchanged opening sentence makes every
  gateway-to-pod RPC its domain (`spec/10_gateway-internals.md:41`). The only fence drivers are the resume
  path and the sweeper's re-adopt, which §7's first open decision records, so a session that started normally
  and was never handed off holds no fenced generation on its pod and would have had its `Interrupt`,
  `Checkpoint`, `ExportPaths`, and `Shutdown` RPCs refused from its own healthy first coordinator. Nothing
  implemented that refusal: the adapter reads `coordination_generation` on the fence path and the barrier path
  alone (`pkg/adapter/coordination.go:92`, `:223`), CODE-2 stages the barrier alone, and the value the
  predicate reads is set at one site (`:121`), so the refusal would have been permanent for such a session
  rather than transient. It also contradicted two statements the same edit left standing, §10.1's summary
  bullet, which rejects on staleness (`spec/10_gateway-internals.md:30`), and SPEC-2's §28.5.1 Messages
  wording, which rejects a generation older than the one the pod holds for that session. The staged step 3
  text now states that a session with no recorded value has nothing to match, that the pod does not refuse
  that session's RPCs on generation grounds, and that `CheckpointBarrier`'s equality gate is the one RPC
  refused for it, which is the shipped behavior (`pkg/adapter/coordination.go:236-239`) and what §7's first
  open decision turns on. The rationale paragraph and §7's first open decision take the same wording, and the
  review log's TEST-1 entry carries the two assertions this reading pins.
- **Correction to this pass: the closing mirror paragraph named an incomplete set of staging sites for the
  acceptance rule.** It read that neither `spec/28` nor `spec/29` carries step 3's acceptance predicate and
  concluded that the step 3 wording is staged in §10.1.2 alone and in the barrier-side wire comments. The
  first half is true of the two files as they stand: §29.10's "Partitioned per slot" coordination bullet ends
  at "so each slot's session carries its own lease and its own generation" and carries no validation sentence
  (`spec/29_communication-scenarios.md:1464-1468`). The conclusion was false against SPEC-2's own edit list,
  which stages the acceptance rule into `spec/29` §29.10 twice, in that coordination bullet and in the
  narrowed barrier bullet. The paragraph now names §10.1.2 step 3, both §29.10 bullets, and the barrier-side
  wire comments, and it states that the mirrors take the acceptance sentence while the unset-value clause
  stays in §10.1.2, so SPEC-2's rule that no mirror states more or less than the section it mirrors holds
  after SPEC-1 lands.

### Pass 5 (2026-08-31, automated)

- **SPEC-1 required every terminated session's `coordinator_lost` record to carry a generation D6 leaves
  unset in the ordinary case.** The §10.1.4 half stated the per-session generation unconditionally, while the
  §10.1.2 half and D6 state that a session's fenced generation is unset until that session's first accepted
  fence on the pod. The uncovered case is the common one rather than an edge: a fence reaches a session only
  through a coordinator handoff, whose sole drivers are the resume path
  (`pkg/gateway/sessionserver/start.go:4233`, called at `:3975` and `:4067`) and the sweeper's re-adopt
  (`pkg/gateway/coordination/coordination/coordination.go:488`), and the hold arms on the close of the pod's
  single CH-ADAPTEREVENTS stream with a started session (`pkg/adapter/holdstate.go:96-99`), which involves no
  fence. SPEC-1's §10.1.4 text now states the unset case in the same form it uses for the pod-level event: a
  session no coordinator fenced on that pod carries `0` on the `coordinator_lost` log line and on the
  post-mortem, and that zero reports D6's unset value rather than a generation the pod holds, so §10.1.2's
  admission predicate still reads the value as unset and refuses. Zero is unambiguous because §10.1.2 step 1
  increments the counter before step 2 announces it, so no `CoordinatorFence` carries zero; the text names the
  three gates that hold that today. The text also names the two artifacts that carry the value, the log line
  and the post-mortem JSON, so the requirement is not read as a new field on `AdapterTerminating`, whose field
  list `spec/04_system-components.md:747` fixes as `session_id` and `reason`. §3's design overview and the
  summary's "what changes" sentence take the same qualifier, and the review log's ledger carries the case to
  TEST-1, whose existing form asserts `LastGeneration: 7` for a session that was never fenced
  (`pkg/adapter/holdstate_test.go:700-716`).
- **SPEC-1 called the local-disk post-mortem the terminal record, and §10.1.4 designates no terminal record
  on the pod.** §10.1.4 says only that the adapter writes the `session.terminated` event to local disk for
  post-mortem when no coordinator ever returns, and it assigns the terminal transition for that case to the
  gateway's orphan session reconciler, which forcibly transitions the session to `failed` with reason
  `orphan_pod_terminated` (`spec/10_gateway-internals.md:58-59`). The same bullet states why the pod cannot
  own one: agent pods have zero RBAC bindings and no network path to the kube-apiserver, so a direct
  `Sandbox.status.phase` write is not possible. The staged clause now identifies the artifact by what
  §10.1.4 says about it, "the local-disk post-mortem §10.1.4 has the adapter write when no coordinator ever
  returns", so the staged text adds the per-session generation without asserting a terminality the same
  subsection assigns elsewhere.

### Pass 6 (2026-08-31, automated)

- **§1.3 step 3 attributed a re-read-and-re-issue loop to §10.1.2, and derived a permanent outage from it.**
  §10.1.2 gives no re-read-and-re-issue instruction for a fence the pod rejects. Its fence-failure bullet
  retries the same generation value for a failed or timed-out fence and then relinquishes the lease
  (`spec/10_gateway-internals.md:39`), and its re-read-and-restart clause is the 0-row compare-and-swap
  outcome, which restarts from lease acquisition and therefore increments
  (`spec/10_gateway-internals.md:37`). The duties of a replica holding a generation-stale rejection are
  §10.1.5, and the implemented `Fencer` matches them: on a `FailedPrecondition` it re-reads
  `coordination_generation`, and with no advance it relinquishes without re-issuing the rejected value
  (`pkg/gateway/coordination/coordfence/coordfence.go:164-179`, `:195-200`). The problem statement's step 3
  now states that path and cites §10.1.5. The consequence sentence is corrected with it: B has no
  coordinator on that pod and every operational RPC is barred until a fence acknowledges
  (`spec/10_gateway-internals.md:38`), the sweeper records an adoption backoff and retakes on a later sweep
  (`pkg/gateway/coordination/coordination/coordination.go:399-416`), and each takeover bumps B's own
  generation (`:463-468`), so the stall is bounded churn that increments
  `lenny_coordinator_handoff_stale_total` and `lenny_coordinator_fence_relinquished_total` on every cycle
  rather than a session that stays uncoordinatable until an unrelated session stops advancing.
- **§1.3 said a rejected drain barrier loses the partial manifest, which the gateway writes on both fallback
  paths.** §10.1.8 step 3 puts the manifest write on the gateway: its barrier dispatcher opens the
  `Checkpoint` stream for each quiesced session concurrently with the `CheckpointBarrier` RPC and finalises
  the manifest row itself (`spec/10_gateway-internals.md:185`), and the BarrierAck-timeout path finalises the
  intent row with `manifest_reason = "timeout"` (`spec/10_gateway-internals.md:190`). The code matches:
  `dispatchOne` starts the checkpoint goroutine before `dispatch.Send` and joins it before the error branch,
  and `ErrGenerationStale` sets only `Stale` (`pkg/gateway/coordination/barrier/barrier.go:209-232`). The
  bullet now states the costs the rejection does carry, which are the lost quiescence, the barrier record the
  `sessioncheckpointmeta` upsert writes only on an acknowledged barrier (`:237-246`), and the duplicate
  capture by the post-barrier per-session eviction checkpoint
  (`pkg/gateway/podlifecycle/prestop/prestop.go:390-397`). `quiesced_ms` is not named among the lost records,
  because it is never persisted and exists only on the client-side ack struct
  (`pkg/gateway/runtime/adapterclient/client.go:450-455`). The summary's "what is fixed" list carries the
  same claim in the same terms, so §1.3 now asserts no data loss anywhere.

### Pass 7 (2026-08-31, automated)

- **Step 3's surviving barrier clause refused the drain barrier of the ordinary never-fenced session, and
  no edit list named the sections that own the barrier.** The clause pass 4 left standing stated that
  `CheckpointBarrier` is the one RPC refused for a session the pod holds no fenced generation for, and §7's
  first open decision restated the same refusal as current behavior. The refused set is the ordinary set
  rather than an edge: the barrier-target set is every session the draining replica coordinates, sourced as
  `SELECT session_id FROM coordination_lease WHERE coordinator_replica = $this_replica_id AND released_at IS
  NULL` (`spec/10_gateway-internals.md:183`), and the only fence drivers are the resume path
  (`pkg/gateway/sessionserver/start.go:4237`) and the sweeper's crash-takeover re-adopt
  (`cmd/lenny-gateway/coordination_seams.go:233`), so a session that started normally and was never handed
  off holds no fenced generation. Staged as it stood, §10.1.2 would have mandated a refusal that §10.1.8 and
  §29.7 neither expect nor recover from: §10.1.8 step 1 gives one rejection case and calls it safe
  (`spec/10_gateway-internals.md:183`), steps 2 and 3 have the adapter quiesce and ack with no refusal branch
  (`:184`, `:185`), the closing sentence derives the rolling-update interruption bound from that quiescence
  (`:198`), the only failure arm defined is the ack deadline (`:187`), which a synchronous
  `FailedPrecondition` never reaches, and §29.7's framing paragraph names the same single non-traced
  rejection and closes its outcome set (`spec/29_communication-scenarios.md:1150-1152`). The refusal is a
  shipped defect rather than a design: the gate is `!initialized || gen != fenced`
  (`pkg/adapter/coordination.go:236-239`), which refuses on the absence of a recorded value rather than on a
  mismatch with one. §2 now carries D7, staged step 3 states one rule for its whole domain
  and has the pod accept such a barrier and record no value, SPEC-1 gains §10.1.8 step 1 as an edit site
  where the existing rejection sentence becomes a pair naming the state each outcome depends on, SPEC-2 gains
  §29.7's framing paragraph taking the same qualifier, and §7's first open decision keeps the operator open
  and drops the refusal it asserted. The §28.5.1 `CH-BARRIER` card and the §28.8 `CH-BARRIER` row are
  recorded as non-sites, because the §28.8 row states the refusal of a barrier from a superseded replica and
  the §28.5.1 card states the exclusivity constraint that refusal enforces, and a superseded replica is one
  whose successor's fence the pod has recorded, so the pod holds a value for that session. The surviving equality arm still refuses a barrier whose generation does
  not match a recorded value, which is what those two sentences enforce. Two earlier records carry the
  reading this pass retires and are left as the records they are: pass 4's correction bullet, which staged
  the refusal, and pass 5's zero-sentinel rationale, whose closing clause has the admission predicate read an
  unset value and refuse. Under D7 an unset value has nothing to match and the pod serves the RPC, which
  leaves pass 5's own staged §10.1.4 text unchanged, because that text states the zero representation rather
  than an outcome.
- **The deliverable-side corrections D7 creates are outside this loop's writable set.** CODE-2's target reads
  `pkg/adapter/coordination.go:211` alone; it becomes the gate change, in which `CheckpointBarrier` resolves
  the slot registry entry for the named session and refuses only when that entry holds a recorded generation
  the request does not match, so the condition is `initialized && gen != fenced` read from the entry.
  `initialized` moves onto the entry with `lastFenced` and is never read from `Server`, the `InvalidArgument`
  guards on the session id, the `barrier_id`, and a non-positive generation are unchanged, and
  `checkSessionBound` still runs first (`pkg/adapter/coordination.go:212-226`). SCHEMA-1's acceptance
  carriers state the same acceptance rather than the refusal the ledger's earlier widening recorded. §8 gains
  the cases D7 needs: at tier 1, `TestCheckpointBarrierRejectsWithoutFence`
  (`pkg/adapter/coordination_test.go:185-197`) is rewritten, with its `// spec:` annotation, to assert that
  the barrier is accepted and quiescence established for a bound session with no recorded generation, while
  `TestCheckpointBarrierRejectsGenerationMismatch` (`:199-216`) stands as the case pinning the surviving
  refusal, and a co-tenancy case puts session A fenced at 6 and session B never fenced on one pod, accepting
  B's barrier at its own generation and refusing A's barrier at 5; at tier 3 the wire gate accepts an
  unset-generation barrier and refuses a stale one; at tier 8 a crash takeover whose fence has not yet landed
  does not lose the draining replica's barrier. The files-touched list names the sections under
  `spec/10_gateway-internals.md` (§10.1.2, §10.1.4, §10.1.8), gains
  `spec/28_communication-channels.md` and `spec/29_communication-scenarios.md`, which SPEC-2 edits, and gains
  `pkg/adapter/coordination_test.go`. The summary's watch-out paragraph indexes each correction.
- **Correction: the staged step 3 text named the barrier as the one RPC the adapter validates, which is an
  implementation fact rather than a rule.** Landing it would have had §10.1.2 state both that the pod gates
  every gateway-to-pod RPC on the generation and that `CheckpointBarrier` is the only one gated, and it would
  have contradicted §10.1's summary bullet (`spec/10_gateway-internals.md:30`), which no edit list touches.
  The staged sentence now reads "A session for which the pod holds no fenced generation has no recorded value
  to match, so the pod does not reject that session's RPCs on generation grounds, and a `CheckpointBarrier`
  naming such a session is accepted and records no value", so it states one rule across step 3's whole
  domain. The coverage fact stays in the rationale below the staged block, where it supports D7 as a tree
  observation.
- **Correction: D7's opening grounds, the staged step 3 cross-reference, and this pass's record reversed
  what §10.1.2 step 2 says.** Step 2's bolded precondition bars the acquiring coordinator from sending any
  operational RPC until its own fence is acknowledged, and the acceptance it grants in that window is for
  RPCs carrying the generation the pod already holds (`spec/10_gateway-internals.md:38`). It says nothing
  about a session for which the pod holds no value at all, which is the state D7 governs. D7 now states step
  2's window as what step 2 states, the cross-reference is out of the staged step 3 text so no such
  reference lands in `spec/`, and this pass's record grounds the shipped refusal on the gate's own predicate.
  The staged §10.1.8 text already read step 2 this way, so the proposal now states it one way.
- **Correction: the §28 non-site record attributed the rejection clause to both cited cells.** §28.5.1's
  `CH-BARRIER` Exclusivity bullet states the one-coordinating-replica-per-session constraint and that the
  barrier carries the generation in its own message, and states no rejection
  (`spec/28_communication-channels.md:361-365`); the §28.8 `CH-BARRIER` row's cell restates that constraint
  and adds that a barrier from a superseded replica is rejected on the stamp (`:1808`). SPEC-2's non-site
  paragraph and D7's closing sentence now attribute each clause to the cell that carries it. The non-site
  conclusion is unchanged.

### Pass 8 (2026-08-31, automated)

- **The staged §10.1.8 step 1 replacement and its §29.7 mirror keyed the barrier's acceptance on whether the
  acquiring replica had fenced, which is the wrong axis and is anti-correlated with the outcome.** The pair
  read that the pod rejects the barrier once the acquiring replica's fence has been recorded and that until
  then the pod holds no generation for the session and accepts it. SPEC-1's own state model states that the
  pod's value is unset only until that session's first accepted fence on that pod, so on any handoff after
  the first the pod already holds the value the draining replica fenced. The barrier the draining replica
  sends carries the generation the acquiring replica's compare-and-swap wrote, because the target's stamp is
  read from the lease mirror (`pkg/gateway/coordination/coordination/coordination.go:430`) or live from the
  session row (`cmd/lenny-gateway/httpsurface.go:592-599`), so the fencing rules refuse that barrier before
  the successor's fence lands and accept it after, which is the reverse of what the pair stated. Applied, it
  would also have given §10.1.8 and §29.7 one acceptance predicate and §10.1.2 step 3 another. Both sites now
  apply step 3's predicate by reference in a single sentence, naming the two arms without re-deriving them:
  the pod rejects the barrier when it holds a generation for that session that the barrier does not carry,
  and otherwise accepts it, quiescing the session and capturing the checkpoint the drain would otherwise have
  taken. The acceptance predicate is stated once, in §10.1.2 step 3, which is unchanged by this pass.
- **D7's acceptance was unreachable, because the ordinary session's barrier carries generation 0 and the
  adapter refuses a non-positive generation before the gate.** D7's grounds named the gate
  `!initialized || gen != fenced` (`pkg/adapter/coordination.go:236-239`) as what refuses the ordinary
  never-handed-off session's drain barrier. That session's row still carries
  `coordination_generation = 0` (`migrations/0050_session_record_fields.up.sql:38-39`), because the resume
  path fences without incrementing the row (`pkg/gateway/sessionserver/start.go:4233-4245`) and the sweeper's
  re-adopt is the only other fence driver; the barrier path copies the counter onto the wire unchanged
  (`pkg/gateway/coordination/coordination/coordination.go:430`,
  `cmd/lenny-gateway/httpsurface.go:592-599`, `pkg/gateway/coordination/barrier/wiring.go:49`); and the
  adapter's `InvalidArgument` guard on a non-positive generation (`pkg/adapter/coordination.go:224-226`)
  refuses the request three statements before the gate. Applied as it stood, §10.1.2 step 3, §10.1.8 step 1,
  and §29.7 would have asserted an acceptance the applied system cannot produce for the one session class D7
  exists to repair. The root defect is that the two senders of a session's stamp disagree: the fence path
  floors a non-positive row value at 1 (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`) and the
  barrier path does not. SPEC-1 now stages the rule once, in §10.1's "Generation counters" bullet, which owns
  the counter's role on the wire, and D7's grounds name both refusals and which change removes each. The
  adapter's `InvalidArgument` guards are untouched, so CODE-2's statement that they are unchanged stays true,
  and the resumed session whose pod is fenced at 1 while its row still reads 0 keeps a positive stamp on its
  own barrier. D7 and the stamp rule are not substitutes for each other: with the stamp floored the pod still
  holds no value for a never-fenced session, so the `!initialized` arm would refuse it, and without the stamp
  rule the barrier never reaches the gate.
- **The deliverable-side work the stamp rule creates is outside this loop's writable set.** The non-spec
  changes gain CODE-4: in `pkg/gateway/coordination/barrier/barrier.go`, `Coordinator.dispatchOne` (`:207`)
  normalises a non-positive `Target.CoordinationGeneration` to 1 at the top of the function, before both
  `dispatch.Send` (`:226`) and the `sessioncheckpointmeta.Record` write (`:237-246`, the generation at
  `:241`), citing §10.1, so the wire value and the persisted record name the same generation. The state it
  reads is `Target.CoordinationGeneration`, written at two producer sites, `MirrorTargetLister.Targets` from
  the `coordination_lease` mirror rows (`pkg/gateway/coordination/barrier/wiring.go:112`) and the
  cache-fallback closure reading the session row (`cmd/lenny-gateway/httpsurface.go:592-599`); the underlying
  value is written by the sweeper's mirror upsert
  (`pkg/gateway/coordination/coordination/coordination.go:430`) from `sessions.coordination_generation`,
  whose default is 0 (`migrations/0050_session_record_fields.up.sql:38-39`) and which only a recorded handoff
  increments, and the lease column carries the same default
  (`migrations/0164_coordination_lease.up.sql:44`). Nothing clears either value, and `dispatchOne` is the one
  boundary both producers pass through, so the normalisation has a single site. The `Dispatcher` interface is
  unchanged and its implementations need no change, because the normalisation runs before `Send`:
  `PodDispatcher` (`pkg/gateway/coordination/barrier/wiring.go:41`) and the fakes at
  `pkg/gateway/coordination/barrier/barrier_test.go:39` and
  `pkg/gateway/coordination/barrier/checkpoint_drive_test.go:109`. When the normalisation does not fire, the
  barrier carries 0, the adapter returns `InvalidArgument`, only `FailedPrecondition` maps to
  `ErrGenerationStale` (`pkg/gateway/coordination/barrier/wiring.go:51-53`) so the outcome records an error
  rather than staleness, the session is absent from the acknowledged set, and prestop captures it a second
  time (`pkg/gateway/podlifecycle/prestop/prestop.go:395`, `:510`). §8 gains a tier-1 case in
  `pkg/gateway/coordination/barrier` asserting that a target at generation 0 dispatches a barrier carrying 1
  and persists 1 in the checkpoint-meta record, with a `// spec:` annotation naming §10.1, and the tier-3
  wire case D7 already stages is extended so a gateway-stamped barrier for a never-fenced bound session
  reaches the pod and is accepted on the unset arm. The files-touched list gains
  `pkg/gateway/coordination/barrier/barrier.go` and its test file, and names §10.1 alongside §10.1.2,
  §10.1.4, and §10.1.8 under `spec/10_gateway-internals.md`. The checklist gains a code step for CODE-4,
  inserted after S4 because the tier-3 wire case reaches the pod only once the stamp is positive:
  "**S5 · code** — CODE-4. The gateway stamps the baseline generation on the barrier path. Tiers 0, 1, 3.
  Depends on: S1". The steps after it renumber, so CODE-3 becomes S6 and TEST-1 becomes S7, whose Depends-on
  becomes S3, S4, S5, S6. The summary's watch-out paragraph indexes each of these.
- **Correction: the stamp rule contradicted two unstaged sentences that state which value the barrier
  carries, and this pass ruled one of them a non-site.** §10.1's new sentence has the coordinating replica
  stamp generation 1 for a session no replica has taken over, whose row still reads 0
  (`migrations/0050_session_record_fields.up.sql:38-39`). Two sentences state that the message carries the
  current counter, which for that session is 0: §10.1.8 step 1's stamp sentence
  (`spec/10_gateway-internals.md:183`) and §29.7's trace step 4
  (`spec/29_communication-scenarios.md:1186`). Applied as staged, §10.1 and each of those two would have
  named different values for the same wire field, and an implementor reading either as the statement of what
  the dispatcher sends would read CODE-4's floor as a violation, putting D7's acceptance back behind the
  adapter's non-positive-generation guard (`pkg/adapter/coordination.go:224-226`). The non-site rationale
  did not reach that, because the objection is the definite value each sentence states rather than a
  restatement of the floor. SPEC-1 now stages §10.1.8 step 1's stamp sentence and SPEC-2 stages §29.7 step 4,
  each naming the coordinating replica's generation stamp for the session as §10.1 states it, so neither
  carries a value and the drift the non-site paragraph guarded against does not return. The non-site list
  keeps §28.5.1's `CH-BARRIER` Messages bullet alone, whose wording carries no "current"
  (`spec/28_communication-channels.md:349`) and whose Preconditions bullet already points at §10.1 for the
  stamp. `spec/29_communication-scenarios.md` is already on the files-touched list this pass records, so
  §29.7 step 4 adds no file entry.

## 7. Open decisions for review

1. **Whether the barrier gate stays equality.** The gateway issues `CoordinatorFence` on the resume path
   (`pkg/gateway/sessionserver/start.go:3975`, `:4067`, through `fenceResumedPod` at `:4233`) and on the
   sweeper's crash-takeover re-adopt (`pkg/gateway/coordination/coordination/coordination.go:399` through
   `cmd/lenny-gateway/coordination_seams.go:155-160`). Both paths drive the same `coordfence.Fencer`, and no
   other call site drives it (`pkg/gateway/sessionserver/start.go:4237`,
   `cmd/lenny-gateway/coordination_seams.go:233`), so a session that has neither resumed nor been taken over
   holds no fenced generation on its pod. That case is settled by D7: the pod accepts a barrier naming a
   bound session it holds no fenced generation for. What remains for the reviewer is whether the comparison
   against a value the pod does hold stays equality.
2. **Whether a fence for an unheld session is a rejection or a retryable race.**
3. **Whether `coord.mu` becomes per-entry.**

## 10. Dependencies

Applies after proposal 0073, which supplies `checkSessionBound` and the slot registry this proposal keys
the generation on. It is independent of proposal 0075.
