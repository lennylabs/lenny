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
holds, which lasts until a successor's fence is acknowledged (`spec/10_gateway-internals.md:38`); it does
not cover a session for which the pod holds no generation at all, which is the state D6 makes reachable. The
barrier is the only gateway-to-pod RPC the adapter validates on the generation gate, because
`coordination_generation` is read on the fence path and the barrier path alone
(`pkg/adapter/coordination.go:92`, `:223`), so refusing the barrier while every other RPC naming the same
session is served protects nothing. This changes shipped behavior, and the drain barrier of a session that
never resumed and was never taken over meets two refusals today. The only fence drivers are the resume path
(`pkg/gateway/sessionserver/start.go:4237`), which fences without incrementing the session row, and the
sweeper's crash-takeover re-adopt (`cmd/lenny-gateway/coordination_seams.go:233`), so a session that started
normally and was never handed off still carries the counter's baseline on its row. That baseline is 0 today,
because the column is declared `NOT NULL DEFAULT 0`
(`migrations/0050_session_record_fields.up.sql:38-39`), and CODE-4 moves it to 1. The barrier path copies
that counter onto the wire unchanged, through the lease mirror
(`pkg/gateway/coordination/coordination/coordination.go:430`) or the cache fallback
(`cmd/lenny-gateway/httpsurface.go:592-599`) and then the dispatcher
(`pkg/gateway/coordination/barrier/wiring.go:49`). The adapter therefore refuses that barrier today with
`InvalidArgument` on the non-positive generation (`pkg/adapter/coordination.go:224-226`) before the gate
`!initialized || gen != fenced` (`:236-239`) is reached, and the gate's `!initialized` arm refuses it once
the carried value is positive. D7 removes the second refusal, and the counter baseline SPEC-1 and SPEC-3
state removes the first, because the ordinary never-handed-off session's barrier then carries the 1 its own
row holds; the `!initialized` arm stays reachable for a session handed off whose successor's fence the pod
has not yet recorded. Either refusal costs the step-2 quiescence §10.1.8 states, leaves the gateway-driven
`Checkpoint` stream running against a live workspace, writes no acknowledged-barrier record
(`pkg/gateway/coordination/barrier/barrier.go:229-246`), and pushes the session into the post-barrier
eviction checkpoint that captures it a second time (`pkg/gateway/podlifecycle/prestop/prestop.go:390-397`).
The consequence is the same for both because prestop branches on the acknowledgement alone
(`pkg/gateway/podlifecycle/prestop/prestop.go:395`, `:510`) while only `FailedPrecondition` maps to
`ErrGenerationStale` (`pkg/gateway/coordination/barrier/wiring.go:51-53`). A barrier carrying a generation
that does not match a value the pod does hold for the named session is still refused with
`FailedPrecondition` and the `coordinator_handoff_stale` detail. That refusal is on the value rather than on
the sender: `CheckpointBarrierRequest` carries a session identifier, a generation, and a barrier identifier
and no replica identity (`schemas/lenny-adapter.proto:1475-1483`), and the generation on it is read from
state the replicas share, either the lease mirror row (`pkg/gateway/coordination/barrier/wiring.go:104-114`)
or the live session row on the cache fallback (`cmd/lenny-gateway/httpsurface.go:592-599`), rather than from
the sending replica's own stamp. §28.5.1's `CH-BARRIER` Exclusivity bullet states the
one-coordinating-replica-per-session constraint that `REG-COORDLEASE` guards, and SPEC-2 stages the §28.8
`CH-BARRIER` row's rejection clause onto the predicate stated here. SPEC-1 stages the specification half in
§10.1's generation-counter bullet, §10.1.2 step 3, and §10.1.8 step 1, SPEC-2 stages the §29.7 half, SPEC-3
states the same baseline in §4.2, CODE-2 carries the gate change, and CODE-4 lands the baseline on the
session row.

## 3. Design overview

`coordinationState` moves from `Server` onto the per-session slot registry entry.
`CoordinatorFence` resolves the entry for the named session and compares against that entry's
`lastFenced`. `CheckpointBarrier` does the same, and the barrier gate that carries the
gateway-minted checkpoint id back into the ack moves onto the entry with that state, because
§10.1.8 step 3 already fixes the gate's unit at the session. Gap detection becomes per session and
applies from a session's second accepted fence onward, so a gap is a real skip in one session's
lineage, and under D6 the first fence for a session on a pod is recorded at whatever value it
carries and is never a gap. Hold state does not move: under D5 the hold stays pod-scoped, arms on
the same pod-level signal, and terminates the same set. Only the generation it reports moves onto
the entry, so each terminated session's `coordinator_lost` record carries its own last fenced
generation, or zero when no coordinator fenced it on that pod, and the pod-level arming event
carries none.

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
  Equality catches a barrier carrying a generation other than the one the pod holds for the session the
  barrier names. The barrier's generation is read when the target set is assembled rather than stamped by
  the sending replica, so that catches a superseded sender whenever the assembly and the successor's fence
  straddle each other in either order; confirm that a per-session counter does not change which barriers
  that catches.
- How the per-session gap reset is built, given that the adapter does not perform it today. SPEC-1 stages
  the reset's scope on the specification side, so the requirement is per session before any code is
  written. The adapter's gap branch logs `coordinator_generation_gap`, reports `gap_detected`, and then
  records the new value on the same path a non-gap fence takes
  (`pkg/adapter/coordination.go:108-121`), and its comment at `:112-113` records the cancellation as
  unimplemented. The staged text re-scopes the requirement and does not assert that the adapter meets it.
- Whether the pod-wide `coord.mu` becomes per-entry or stays one lock. A single lock is simpler and the
  critical sections are short; per-entry locking is only worth it if fences contend. The `barrierGate` that
  moves onto the entry beside the state carries its own leaf mutex and is unaffected by this question.

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
unchanged. In step 3, the acceptance sentence and the sentence that follows it become "The pod
accepts only RPCs whose generation matches the value it holds for the session the RPC names. A session for
which the pod holds no fenced generation has no recorded value to match, so the pod does not reject that
session's RPCs on generation grounds, and a `CheckpointBarrier` naming such a session is accepted and
records no value. Because fence confirmation is required before this step is reached, the pod holds one
generation for that session at a time and accepts only RPCs carrying it, so an RPC carrying a generation a
later fence superseded is refused from the moment that fence is acknowledged." Step 3's opening sentence,
which states that all subsequent gateway-to-pod RPCs include the local generation stamp, is unchanged,
because it states what the coordinator this step addresses stamps on the RPCs it sends. The acceptance
sentence that follows it is the one whose domain is the whole set of gateway-to-pod RPCs, including
`CheckpointBarrier`, whose generation §10.1.8 step 1 states is the session's current
`coordination_generation` read from state the replicas share, as D7 records.

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
`:198`). SPEC-1 therefore also states the counter's baseline in §10.1 and stages §10.1.8
step 1's closing sentence, both below, and CODE-2 and CODE-4 carry the matching code
changes. Step 3's closing sentence holds under that baseline on a session's first handoff
as well as on later ones: the predecessor stamps the 1 its row holds on the RPCs it sends, the
successor's compare-and-swap mints 2 and fences the pod at 2, so the equality gate refuses the
predecessor's stamped RPCs from the moment that fence is acknowledged. The
fence path's admission of an uninitialized session (`:99`) answers a different question, which is D6's exemption for a session's own first
fence rather than the gate on the operational RPCs that follow it. The clause also agrees with the
two neighbouring statements of the same rule: §10.1's summary bullet has the pod reject when the
generation is stale (`spec/10_gateway-internals.md:30`, whose rejection sentence stands and which
SPEC-1 opens below only to add the baseline sentence), and the §28.5.1 Messages wording SPEC-2 stages
has the pod reject a generation older than the one it holds for that session. The comparison stays
equality, because §7's first open decision reserves the operator for the reviewer and this edit
changes only the unit of the value compared and the domain over which the comparison applies.

§10.1.8 step 1 states the barrier's own refusal as the only outcome, and it takes the qualifier D7
requires. Its closing sentence, which reads "Pods receiving a barrier for a session no longer
coordinated by this replica (a false-positive surviving the cache fallback) reject the barrier as
a generation-stale RPC under the normal fencing rules — this is safe and does not require special
handling" (`spec/10_gateway-internals.md:183`), becomes "Pods receiving a barrier for a session no
longer coordinated by this replica (a false-positive surviving the cache fallback) apply to it the
same generation rule as to any other gateway-to-pod RPC, stated in step 3 of the handoff protocol:
the pod rejects the barrier as a generation-stale RPC when it holds a generation for that session
that the barrier does not carry, and otherwise accepts it. The generation a barrier carries is
read from the session's coordination state when the barrier-target set is assembled, rather than
stamped by the draining replica, so the outcome follows from the generation the barrier carries
alone. Either outcome is safe and requires no special handling." The acceptance predicate itself
is stated once, in §10.1.2 step 3, and §10.1.8 applies it by reference. A restatement here in
terms of which replica has fenced would be false in the false positive where the barrier carries
the generation the acquiring replica's compare-and-swap wrote and the pod still holds the value
the draining replica fenced, so the fencing rules refuse the barrier before the successor's fence
lands and accept it after. §10.1.8 states no enumeration of the values a barrier can reach the pod
carrying, because the assembly reads state that can sit on either side of the value the pod holds
and which values are reachable is a property of the target producers rather than of this step. The
rest of §10.1.8 is unchanged: steps 2, 3, and 4, the BarrierAck-timeout partial-capture rules, and
the closing sentence bounding the rolling-update interruption window to one in-flight tool call
per session all hold under D7 rather than needing a qualifier, because a barrier the pod accepts
establishes the step-2 quiescence and the ack deadline stays the only failure arm §10.1.8 defines.

§10.1's "Generation counters" bullet states the counter's role on the wire, and it gains one sentence,
because D7's acceptance is unreachable unless the generation a replica carries on a gateway-to-pod message
for a session no replica has taken over is positive and is strictly below the value the first takeover mints
from it. After "When a replica takes over coordination (via either mechanism), it increments the generation"
(`spec/10_gateway-internals.md:30`) the bullet states: the counter's baseline is 1, so a session row carries
`coordination_generation = 1` from creation; a replica coordinating a session no replica has taken over
carries that value on its gateway-to-pod messages for it; and §10.1.2 step 1's compare-and-swap mints 2 on
the first takeover, so every generation a pod validates is positive and is strictly greater than the value
carried before the takeover that fenced it.

The rule is stated on the counter rather than on the wire, so the value a replica carries is the value its
session row holds and there is no second value to keep in agreement with the row. That is what makes step 3's
closing sentence, in which the equality gate refuses a generation a later fence superseded, true on a
session's first handoff as well as on later ones: the predecessor carries 1, the successor's
compare-and-swap mints 2 and fences the pod at 2, and the pod's equality gate separates them. A
fixed positive value carried on the wire would not, because §10.1.2 step 1 mints `$expected_generation + 1`,
which for a row at the baseline is that same fixed value, so the first fence for a session would fence nobody
out.

CODE-4 lands the baseline in the column default and on the session-store create path, and SPEC-3 states it in
§4.2, which owns the session record's counters. The gateway's fence path floors a non-positive row value at 1
before it fences today (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`), and CODE-4 deletes that
floor, because a session row can no longer carry a non-positive value. The adapter's refusal of a non-positive
generation on the fence path (`pkg/adapter/coordination.go:93-94`) and on the barrier path (`:224-226`) is
unchanged. It becomes unreachable by construction rather than by a floor, and it stays as the fail-closed
backstop for a value the gateway should never send.

The sentences elsewhere that state which value a barrier carries are not edit sites under the baseline.
§10.1.8 step 1 reads that the `CheckpointBarrier` message carries the current `coordination_generation`
(`spec/10_gateway-internals.md:183`), and §29.7's trace step 4 reads that the replica sends it "carrying the
session's current `coordination_generation`" (`spec/29_communication-scenarios.md:1186`). Each names the row
value the dispatcher copies onto the wire (`pkg/gateway/coordination/barrier/wiring.go:49`), which is positive
for every session once the baseline is 1, so each stays true and neither is restated.

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
That is held by the session row's baseline of 1, which CODE-4 lands as the column default and as the create
path's floor, and by the adapter's refusal of a fence carrying a non-positive generation
(`pkg/adapter/coordination.go:93-94`). The gateway's floor of a zero row at 1 before it fences
(`pkg/gateway/coordination/coordfence/coordfence.go:147-153`) is deleted by CODE-4, because the row value it
guards against can no longer exist. The bullet also states that the hold itself and the
`lenny_adapter_coordinator_hold` gauge remain pod-scoped under D5, so the per-session generation stated in
§10.1.2 is not read as scoping the hold.

### SPEC-2. The mirrors state the same scope

`spec/28` and `spec/29` restate the pod-side fence rule and its gap reset without a session qualifier and
cite §10.1 as their source, and §29.7 states the barrier's rejection without the pod-side state that
rejection depends on. SPEC-2 carries the statements SPEC-1 makes into those two files, so the applied
specification states one pod-side rule rather than two incompatible ones. It edits the generation half of
each mirror and leaves the hold half alone: under D5 the hold's unit is the pod, and `spec/10` §10.1.4's
"Whole-pod connection loss when `maxConcurrentSessions > 1`" paragraph already fixes total connection loss
as a whole-pod failure, so qualifying a hold sentence per session would stage a retraction of both.

**`spec/28_communication-channels.md`.** A `spec/28` sentence is an edit site when it states what the pod
does with an RPC's generation and fixes the value that outcome is measured against, because SPEC-1 changes
both the unit of that value and what the pod does when it holds none. A sentence is not an edit site when it
states the exclusivity constraint and the guard that enforces it, states a duty on the sending replica,
states the hold, or states the pod's validation without fixing the compared value and defers to §10.1 for
the rule. The sentences below change.

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
- The `CH-FENCE` card's Exclusivity bullet in §28.5.1, its window clause. "the acknowledgement of
  this fence is what closes the window in which the prior coordinator's RPCs are still accepted"
  (`spec/28_communication-channels.md:330-331`) becomes "the acknowledgement of this fence is what
  closes the window in which RPCs carrying the prior generation are still accepted". The bullet's
  constraint sentence and its naming of `REG-COORDLEASE` as the guard are unchanged. The clause is
  an edit site even though the bullet's subject is the exclusivity constraint, on the same reading
  that makes the §28.8 `CH-BARRIER` and `CH-CHECKPOINT` "Holder of the exclusivity constraint
  changes" cells edit sites below: a sentence that states the constraint and also fixes when the
  pod stops accepting an RPC falls under the site arm of the criterion for the clause that fixes
  it. The owner form is false on `CH-BARRIER`, because the barrier's generation is read from state
  the replicas share rather than from the sending replica's own stamp, so a superseded replica's
  barrier can carry the very generation the pod holds, in which case the pod accepts it. The value
  form also restores the wording of the sentence this one mirrors, since §10.1.2 step 2 states the
  window as the one in which "the pod still accepts RPCs carrying the previous generation"
  (`spec/10_gateway-internals.md:38`) and SPEC-1 leaves step 2 unchanged.
- §28.6's "One holder per session" paragraph, its `CH-FENCE` sentence. "and from the fence onward the pod
  rejects every RPC carrying an older generation" becomes "and from the fence onward the pod rejects every
  RPC carrying a generation older than the one it holds for that session". The paragraph already states that
  the unit of the exclusivity constraint is the session; this sentence states the pod's rejection rule,
  which the unit sentence does not entail, and which a pod-wide gate satisfies today.
- §28.6's "The second opener on those channels" paragraph, its first sentence. "A replica that opens one of
  those four channels without holding the current generation is rejected on the generation stamp, and it
  cancels every in-flight RPC for the session without retrying and discards its cached in-memory streams"
  (`spec/28_communication-channels.md:1679-1681`) becomes "A replica that opens `CH-ATTACH`,
  `CH-CHECKPOINT`, or `CH-BARRIER` carrying a generation other than the one the pod holds for the session
  the RPC names is rejected on the generation stamp, and it cancels every in-flight RPC for that session
  without retrying and discards its cached in-memory streams. A session for which the pod holds no fenced
  generation has no recorded value to match, so the pod rejects none of that session's RPCs on generation
  grounds. `CH-FENCE` is the channel that installs the value: the pod rejects a fence carrying a generation
  older than the one it holds for that session and records a higher one." The relation is the value the pod
  holds, which is the relation §10.1.2 step 3 states, rather than whether the opening replica holds the
  session row's current value. The row's value is the wrong relation because §10.1.2 step 2 states that
  until the pod acknowledges a successor's fence the pod still accepts RPCs carrying the previous
  generation (`spec/10_gateway-internals.md:38`). Between the successor's compare-and-swap and its fence
  the predecessor no longer holds the row's current value while the pod still holds the predecessor's, so a
  rejection stated on the row's value would contradict step 2 and staged step 3 alike, and would refuse a
  periodic checkpoint the predecessor opens inside that window. `CH-FENCE` is stated separately rather than
  folded into the same relation because an acquiring replica's fence carries a generation above the one the
  pod holds and must be accepted, so one predicate cannot span all four channels.
- §28.6's "The second opener on those channels" paragraph, its fence-acknowledgement sentence.
  "the fence acknowledgement closes the window in which the prior coordinator's RPCs are still
  accepted" (`spec/28_communication-channels.md:1683-1685`) becomes "the fence acknowledgement
  closes the window in which RPCs carrying the prior generation are still accepted". The rest of
  that sentence, which states that a replica becomes the holder by acquiring `REG-COORDLEASE` and
  winning the generation compare-and-swap, is unchanged, and so is every other sentence in the
  paragraph except its first, which the bullet above stages. It is an edit site under the
  criterion above because it states when the pod stops accepting an RPC and fixes the compared
  value by naming its owner, and the owner form is false on `CH-BARRIER`: the barrier's generation
  is read from state the replicas share rather than from the sending replica's own stamp, so a
  superseded replica's barrier can carry the very generation the pod holds, in which case the pod
  accepts it. Stating the window on the generation an RPC carries makes the sentence true across
  all four channels and matches staged §10.1.2 step 3, which states the same rule on the carried
  value.
- §28.8's `CH-FENCE` row, its "Holder of the exclusivity constraint changes" cell. The gap sentence the cell
  repeats takes the same re-scoping as the Degradation bullet, word for word, and the cell's window sentence,
  "The acknowledgement closes the window in which the prior coordinator's RPCs are still accepted"
  (`spec/28_communication-channels.md:1807`), takes the same change as the §28.5.1 Exclusivity bullet above
  and becomes "The acknowledgement closes the window in which RPCs carrying the prior generation are still
  accepted". The cell's other sentences are
  unchanged, and the row stays one line carrying its cells, because the §28.8 matrix carries exactly one row
  per channel identifier in §28.3 and a tier-0 gate reads that correspondence in both directions.
- §28.8's `CH-CHECKPOINT` row, its "Holder of the exclusivity constraint changes" cell. "A stream opened by
  a replica that no longer holds the current generation is rejected on the stamp"
  (`spec/28_communication-channels.md:1806`) becomes "A stream carrying a generation other than the one the
  pod holds for the session the stream names is rejected on the stamp, and a session for which the pod holds
  no fenced generation has no recorded value to match", which is the relation the second-opener sentence
  above states. The cell names one channel, so it takes that relation with no `CH-FENCE` arm. The cell's
  second clause, that the new holder must complete its fence before it opens one, is a sender-side duty and
  is unchanged, as are the cell's constraint sentence and the rest of the row, and the row stays one line.
- §28.8's `CH-BARRIER` row, its "Holder of the exclusivity constraint changes" cell. Its closing
  clause "so a barrier from a superseded replica is rejected on the stamp" (`:1808`) is replaced
  by the predicate §10.1.8 step 1 states, at the level of detail the cell carries: the pod rejects
  the barrier when it holds a generation for the session the barrier names that the barrier does
  not carry, and otherwise accepts it. The cell's constraint sentence and its closing sentence
  recording that the specification states no separate pod-level barrier lock are unchanged, and
  the row stays one line. The clause is an edit site because the pod cannot reject a barrier on
  the identity of its sender: the message carries no replica identity and its generation is read
  from state the replicas share, so a superseded replica's barrier can carry the very generation
  the pod holds, in which case the staged predicate accepts it. The mechanism the clause would
  need to stay true, a replica identifier on the wire and a pod-side view of `REG-COORDLEASE`, is
  a new protocol surface on the one channel where a wrong rejection costs the session's quiescence
  and the acked-barrier record, and forces a second capture.

SPEC-2 leaves the hold sentences in `spec/28` as they stand: the hold half of the `CH-FENCE` Degradation
bullet, the hold sentence of §28.6's "The second opener on those channels" paragraph, and the "while the
adapter is in hold state" cells of the `CH-ATTACH`, `CH-CHECKPOINT`, and `CH-BARRIER` rows in §28.8. Each
states the hold, whose unit stays the pod under D5. That paragraph's closing sentence, "The constraint
excludes a second replica", is checked and left as it stands, because §28.6 names `REG-COORDLEASE` together
with the stamp as the guard, and the lease alone excludes the second replica for a session the pod holds no
generation for.

The sentences below were checked against the criterion above and are not edit sites. §28.5.1's `CH-ATTACH`
Preconditions bullet states that the pod validates the `coordination_generation` stamp on every
gateway-to-pod RPC and rejects a stale one (`spec/28_communication-channels.md:237-240`), which names no
unit for the compared value and defers to §10.1 for the rule. The generation-stale sentence in §28.5.1's
`CH-ATTACH` Degradation bullet (`:251-253`) and the matching sentence in the §28.8 `CH-ATTACH` cell
(`:1805`) state what a replica does with a rejection it has received, which is a sender-side duty.
§28.5.1's `CH-CHECKPOINT` Exclusivity bullet (`:291-296`) and §28.5.1's `CH-BARRIER` Exclusivity bullet
(`:361-365`) state the constraint and the guard that enforces it and name no rejection outcome. §28.5.1's
`CH-BARRIER` Messages bullet (`:349-353`) was checked against the counter baseline rather than against the
criterion above: it states what the message carries and does not call that value the current one, so the
baseline SPEC-1 and SPEC-3 state leaves it true.

No §28.4 claim-register row moves. The rows in `tests/claim-map.json` name mechanisms and wire fields rather
than the scope a sentence states, and their anchors resolve to headings this change does not move, so that
file is not opened by this proposal.

**`spec/29_communication-scenarios.md`.** The §29.10 co-tenancy classification changes, §29.7's framing
paragraph applies the predicate §10.1.8 step 1 applies, and steps 2, 7, and 9 of the §29.8 crash-takeover
trace change.

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
  RPC under the fencing rules when the pod holds a generation for that session that the barrier does not
  carry, and is accepted otherwise. The guard on the pod holding a value is carried word for word from
  §10.1.8 step 1, because a session for which the pod holds no fenced generation has no value the carried
  generation could match, and that session is inside §29.7's own population. Step 5 of the trace, on which
  the adapter quiesces, is unchanged, and the paragraph's set of named outcomes stays closed, because both
  arms are now named.
- §29.8 step 2's `coordinator_connection_lost` sentence carries "the last known generation". It takes
  SPEC-1's §10.1.4 Observability statement: the event names the number of started sessions the pod holds and
  carries no generation.
- §29.8 step 7 states the fence rule and its gap reset a second time. Both halves are re-scoped: the
  record-and-reject half takes the §28.5.1 Messages wording above, and the gap half gains the session
  qualifier on the same clauses SPEC-1 re-scopes in §10.1.2, so the cancellation covers the fenced session's
  in-flight RPCs received after that session's last fenced generation, the reset covers the transient
  tool-call and lifecycle state that session accumulated since its last fenced coordinator, and the logged
  event records that session's two generations.
- §29.8 step 9 states the fence window by naming its owner, and cites the two `spec/28` sentences staged
  above as its source. "no operational RPC reaches the pod before the fence closes the window in which
  the prior coordinator's RPCs are still accepted" (`spec/29_communication-scenarios.md:1322-1326`)
  becomes "no operational RPC reaches the pod before the fence closes the window in which RPCs carrying
  the prior generation are still accepted". It takes the value form for the reason the §28.5.1 `CH-FENCE`
  Exclusivity bullet, the §28.6 fence-acknowledgement sentence, and the §28.8 `CH-FENCE` cell take it:
  the owner form is false on `CH-BARRIER`. The rest of the step, which states the acknowledgement as the
  hard precondition and the new coordinator's duty to stamp its local generation on every gateway-to-pod
  RPC it sends for the session, is unchanged, as are the step's citations.

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

### SPEC-3. The session record states the counter's baseline

`spec/04` §4.2's session-record paragraph states what `coordination_generation` is and how it moves, and it
gains the counter's baseline: a newly created session row carries `coordination_generation = 1`, and the
first coordinator handoff for that session mints 2 under §10.1.2 step 1's compare-and-swap. The paragraph's
existing statements are unchanged. The counter is incremented on coordinator handoff across gateway replicas,
it is independent of `recovery_generation`, and both counters are monotonically non-decreasing and are never
reset (`spec/04_system-components.md:200`). `recovery_generation` keeps its own baseline and this proposal
does not touch it, and neither does the §7.2 snapshot-close bump §4.2 states in the same paragraph, which
advances the counter from whatever value the row holds.

§4.2 owns the session record's fields, so the baseline is stated there, and §10.1 states what the baseline
means for the value a replica carries to the pod. CODE-4 lands it in the schema and on the session-store
create path.

## 6. Non-goals

- **Renaming `CoordinatorFenceRequest`'s session field.** That is proposal 0075's subject. If both land,
  whichever is second rebases onto the first.
- **Changing the scoping of the gateway or Postgres side.** Both are already per session and are correct.
  What this proposal changes on the Postgres side is the value the `sessions.coordination_generation` counter
  starts at, under SPEC-3 and CODE-4. The lease protocol and §10.1.2 step 1's compare-and-swap are unchanged.
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
  (`pkg/gateway/coordination/coordination/coordination.go:398-416`), and each takeover bumps B's own
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

- **Step 3's surviving barrier clause refused the drain barrier of the ordinary never-fenced session, and no
  edit list named the sections that own the barrier.** The clause pass 4 left standing stated that
  `CheckpointBarrier` is the one RPC refused for a session the pod holds no fenced generation for, and §7's
  first open decision restated the same refusal as current behavior. The refused set is the ordinary set
  rather than an edge: the barrier-target set is every session the draining replica coordinates, sourced as
  `SELECT session_id FROM coordination_lease WHERE coordinator_replica = $this_replica_id AND released_at IS
  NULL` (`spec/10_gateway-internals.md:183`), and the only fence drivers are the resume path
  (`pkg/gateway/sessionserver/start.go:4237`) and the sweeper's crash-takeover re-adopt
  (`cmd/lenny-gateway/coordination_seams.go:233`), so a session that started normally and was never handed
  off holds no fenced generation. Staged as it stood, §10.1.2 would have mandated a refusal that §10.1.8 and
  §29.7 neither expect nor recover from: §10.1.8 step 1 gives one rejection case and calls it safe
  (`spec/10_gateway-internals.md:183`), steps 2 and 3 have the adapter quiesce and ack with no refusal
  branch (`:184`, `:185`), the closing sentence derives the rolling-update interruption bound from that
  quiescence (`:198`), the only failure arm defined is the ack deadline (`:187`), which a synchronous
  `FailedPrecondition` never reaches, and §29.7's framing paragraph names the same single non-traced
  rejection and closes its outcome set (`spec/29_communication-scenarios.md:1150-1152`). The refusal is a
  shipped defect rather than a design: the gate is `!initialized || gen != fenced`
  (`pkg/adapter/coordination.go:236-239`), which refuses on the absence of a recorded value rather than on a
  mismatch with one. §2 now carries D7, staged step 3 states one rule for its whole domain and has the pod
  accept such a barrier and record no value, SPEC-1 gains §10.1.8 step 1 as an edit site where the existing
  rejection sentence becomes a pair naming the state each outcome depends on, SPEC-2 gains §29.7's framing
  paragraph taking the same qualifier, and §7's first open decision keeps the operator open and drops the
  refusal it asserted. The §28.5.1 `CH-BARRIER` card and the §28.8 `CH-BARRIER` row are recorded as
  non-sites, because the §28.8 row states the refusal of a barrier from a superseded replica and the §28.5.1
  card states the exclusivity constraint that refusal enforces, and a superseded replica is one whose
  successor's fence the pod has recorded, so the pod holds a value for that session. The surviving equality
  arm still refuses a barrier whose generation does not match a recorded value, which is what those two
  sentences enforce. Two earlier records carry the reading this pass retires and are left as the records
  they are: pass 4's correction bullet, which staged the refusal, and pass 5's zero-sentinel rationale,
  whose closing clause has the admission predicate read an unset value and refuse. Under D7 an unset value
  has nothing to match and the pod serves the RPC, which leaves pass 5's own staged §10.1.4 text unchanged,
  because that text states the zero representation rather than an outcome.
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

### Pass 9 (2026-08-31, automated)

- **The staged §10.1 wire value of 1 equalled the generation the first takeover mints, so the first fence for
  a session fenced nobody out.** Pass 8 staged into §10.1's "Generation counters" bullet that a session no
  replica has taken over carries generation 0 on its row and that the coordinating replica carries 1 on its
  gateway-to-pod messages for it. For exactly that session class, §10.1.2 step 1's compare-and-swap mints
  `$expected_generation + 1`, which is 1 (`spec/10_gateway-internals.md:37`), so the successor's local value
  and the fence it records on the pod are also 1. The pod had nothing to separate the two: 1 is neither older
  than 1 for step 2's rejection rule nor a mismatch with 1 for staged step 3's equality gate, so the pod
  would have accepted the superseded coordinator's RPCs after the takeover fenced. Staged step 3's no-window
  sentence and §10.1's own split-brain sentence (`:30`) would both have been false on every session's first
  handoff, which is the ordinary case, and the drained replica's `CheckpointBarrier` would have been accepted
  for a session it no longer coordinates, quiescing it inside the ack window and double-capturing a
  checkpoint. The rule pass 8 staged also removed the refusal that blocks that today, the adapter's
  non-positive guard (`pkg/adapter/coordination.go:224-226`).
- **The fix baselines the counter rather than the value on the wire.** A session row carries
  `coordination_generation = 1` from creation, a replica carries its row's current value unchanged on its
  gateway-to-pod messages, and §10.1.2 step 1 is untouched, so the first takeover mints 2 and the value
  carried before that takeover is strictly below the value the fence records. §10.1's bullet states the
  baseline, SPEC-3 states the same baseline in §4.2, which owns the session record's counters, and CODE-4
  lands it. The staged §10.1.2 step 3 sentences are unchanged: they were correct as written, and the
  no-window sentence they close with becomes true for the first handoff, which is what the finding disputed.
  Because a replica now carries the row's own value, the sentences that state a gateway-to-pod message
  carries the session's current `coordination_generation` are true again, so §10.1.8 step 1's stamp sentence
  and §29.7's trace step 4 are no longer edit sites and pass 8's restatements of both are withdrawn. The
  §10.1.8 step-1 edit pass 7 staged, the D7 qualifier on the closing false-positive sentence, stands.
  §10.1.4's account of what makes zero name no fence loses the gateway floor and names the row baseline and
  the adapter's fence-side refusal. The §28.5.1 `CH-BARRIER` Messages bullet remains a non-site; that record
  belongs with SPEC-2's `spec/28` non-site enumeration rather than in SPEC-1, which no longer edits any
  sentence about the value a barrier carries.
- **Why the alternatives were rejected.** Flooring §10.1.2 step 1's compare-and-swap above the baseline
  needs the same floor on every writer of the counter, which are that compare-and-swap,
  `Sweeper.RecordHandoff` (`pkg/gateway/coordination/coordination/coordination.go:463-481`), and
  `bumpCoordinationGenerationOnSnapshotClose` (`pkg/gateway/sessionserver/start.go:4452-4456`), and the
  snapshot-close bump from 0 to 1 would still land on the value the predecessor already carried, so the §7.2
  fence would still fence nobody out. A baseline sentinel that no compare-and-swap can produce encodes on the
  wire the state the pod already tracks with `initialized` (`pkg/adapter/coordination.go:29-32`, `:236-239`,
  and D6), and every consumer of the value would have to learn it. Deleting the adapter's non-positive guards
  so 0 travels the wire fails closed nowhere: `coordination_generation` is a proto3 `int64`, so an omitted
  field arrives as 0 and would be indistinguishable from a deliberate baseline, landing on the unset arm,
  which accepts; and 0 would stop naming "no coordinator fenced this session here" in §10.1.4's
  `coordinator_lost` line and post-mortem. Keeping the row at 0 and having the fence path skip the fence
  instead of flooring it retracts §4.2's statement that the counter is used for coordinator fencing on resume
  (`spec/04_system-components.md:323`) and would leave a resumed session with no fenced value on its pod at
  all, which is weaker than what the tree enforces today.
- **The baseline also repairs a shipped-tree defect with the same root.** The resume path fences the pod at
  the gateway floor's 1 without bumping the row (`pkg/gateway/sessionserver/start.go:4233-4245`), so a later
  crash takeover of that session bumps the row from 0 to 1
  (`pkg/gateway/coordination/coordination/coordination.go:463-481`), fences at 1, and is rejected as
  `coordinator_handoff_stale` because the pod already holds 1 (`pkg/adapter/coordination.go:98-106`); the
  fencer re-reads, finds no advance, and relinquishes
  (`pkg/gateway/coordination/coordfence/coordfence.go:171-179`). The takeover is not lost. The sweeper
  records a per-session adoption backoff and a later sweep bumps the row to 2 and fences successfully
  (`pkg/gateway/coordination/coordination/coordination.go:398-416`). What it costs is a sweep cycle of delay
  on a healthy takeover plus a `lenny_coordinator_handoff_stale_total` and
  `lenny_coordinator_fence_relinquished_total` pair that report a split-brain that did not happen. Under the
  baseline the resume fences at 1 against a row that already holds 1, and the takeover mints 2, so the first
  attempt is accepted.
- **The deliverable-side work the baseline creates is outside this loop's writable set, so it is stated
  here.** CODE-4 is restated: pass 8's normalisation in `pkg/gateway/coordination/barrier/barrier.go` is
  withdrawn with the wire value it existed to manufacture, and CODE-4 becomes the step that lands the
  baseline. It keeps its identifier and the summary's watch-out paragraph indexes it.
  - `migrations/0181_sessions_coordination_generation_baseline.up.sql` sets
    `sessions.coordination_generation` to `DEFAULT 1`, backfills with
    `UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0`, drops the inline
    `CHECK (coordination_generation >= 0)` that `migrations/0050_session_record_fields.up.sql:38-39` created
    and adds a named `CHECK (coordination_generation >= 1)`, and sets
    `coordination_lease.coordination_generation` to `DEFAULT 1` so the mirror column's default states the
    same baseline as the row it mirrors (`migrations/0164_coordination_lease.up.sql:44`). Every mirror write
    supplies the value (`pkg/gateway/coordination/coordination/coordination.go:430`), so that default is the
    schema's statement of the baseline rather than a value any code path reads. The down migration restores
    the `DEFAULT 0` and the `>= 0` check and does not roll the rows back, because §4.2 states the counter is
    never reset.
  - `pkg/gateway/session/sessionstore/pgstore/pgstore.go` `Create` (`:140`) floors a zero
    `CoordinationGeneration` to 1 before the insert, alongside the `schemaVersion == 0` normalisation already
    there (`:244-248`), so a caller that builds a `Session` without setting the field writes the baseline
    rather than depending on the column default.
    `pkg/gateway/session/sessionstore/memstore/memstore.go` `Create` (`:46`) takes the same floor next to its
    own `SchemaVersion` normalisation (`:59-62`), so both stores agree.
  - `pkg/gateway/coordination/coordfence/coordfence.go:147-153` loses its floor of a non-positive row value
    and the comment on it. The value read at `:143` is sent as it stands.
  - The state is `sessions.coordination_generation`. It is set by both `Create` paths and by the column
    default, advanced by §10.1.2 step 1's compare-and-swap, by `Sweeper.RecordHandoff`
    (`pkg/gateway/coordination/coordination/coordination.go:463-481`), and by
    `bumpCoordinationGenerationOnSnapshotClose` (`pkg/gateway/sessionserver/start.go:4452-4456`) under §7.2,
    and never cleared or reset. The store-side monotonicity floors hold it non-decreasing
    (`pgstore.go:460`, `:475-477`; `memstore.go:129`, `:144-146`).
  - Readers need no change, because the domain shifts by one and every reader compares or copies the value:
    `coordfence.Fence` (`coordfence.go:143`), the barrier dispatcher
    (`pkg/gateway/coordination/barrier/wiring.go:49`), the cache-fallback target producer
    (`cmd/lenny-gateway/httpsurface.go:592-599`), the checkpointer's intent row, and the
    `session_checkpoint_meta` and `checkpoint_manifest` generation comparisons. No interface changes, so no
    `Dispatcher`, `SessionStore`, or `FenceClient` implementation moves.
  - `RecordHandoff` keeps 0 as its sentinel for a bump that did not land
    (`pkg/gateway/coordination/coordination/coordination.go:371-397`, `:463-481`). Under the baseline a
    successful bump returns 2 or more, so the sentinel cannot collide with a generation the counter produces.
  - When the baseline does not fire and a row still carries 0, the fence carries 0 and the adapter refuses it
    with `InvalidArgument` (`pkg/adapter/coordination.go:93-94`), and a barrier carrying 0 is refused the
    same way (`:224-226`). Both refusals are loud and fail closed: the fence surfaces as a fence error on the
    resume and re-adopt paths, and the barrier surfaces as a target absent from the acknowledged set, which
    pushes the session into the post-barrier eviction checkpoint. The `>= 1` check on the session row is the
    store-side backstop.
  - Caller consequence in the tests: `tests/tier2_component/coordination/sweep_test.go` asserts the
    pre-baseline values and each assertion shifts by one (`:275-276` reads the baseline as 0, `:283-294`
    expects 1 and then 3, and the same shift applies at `:325-327`, `:351-353`, `:390-393`, `:425-426`,
    `:446-447`, `:508-509`, `:564-565`, `:577-578`, and `:593-594`). No fixture inserts an explicit zero into
    `sessions.coordination_generation`, so the tightened check breaks no seed path.
  - Recorded as a residual and not fixed here: `cmd/lenny-gateway/httpsurface.go:592-596` initialises the
    fallback target's generation to 0 and overwrites it only on a successful session-row read, so a Postgres
    read fault during drain still sends 0 and the barrier is refused with `InvalidArgument`. That is the
    pre-existing degraded path, and flooring it would put a generation the gateway does not hold onto the
    wire and into the checkpoint-meta record.
- **§8 and the files-touched list under the baseline.** §8 drops the tier-1 case pass 8 staged in
  `pkg/gateway/coordination/barrier`, which asserted that a target at generation 0 dispatches a barrier
  carrying 1, because the normalisation it pinned is withdrawn. It gains: a tier-1 case in each session store
  asserting that `Create` of a `Session` with a zero `CoordinationGeneration` writes 1; a tier-1 case in
  `pkg/gateway/coordination/coordfence` asserting that the fencer sends the row's value with no floor; a
  tier-2 migration case asserting that 0181 backfills a row at 0 to 1 and that the check rejects an insert at
  0; a tier-2 case asserting that a resume fence at 1 followed by a crash-takeover compare-and-swap to 2 is
  accepted rather than rejected as `coordinator_handoff_stale`, which must fail against pre-fix code; and the
  tier-3 wire case D7 already stages, now reached with the session's own row value. Each carries a `// spec:`
  annotation naming §10.1 and §4.2. The files-touched list gains `spec/04_system-components.md`,
  `migrations/0181_sessions_coordination_generation_baseline.up.sql` and its `.down.sql`,
  `pkg/gateway/session/sessionstore/pgstore/pgstore.go`,
  `pkg/gateway/session/sessionstore/memstore/memstore.go`,
  `pkg/gateway/coordination/coordfence/coordfence.go` and its test, and
  `tests/tier2_component/coordination/sweep_test.go`, and it loses
  `pkg/gateway/coordination/barrier/barrier.go` and its test file, which pass 8 added for the withdrawn
  normalisation. CODE-4 reaches tiers 0, 1, 2, 3, and 4.
- **The target checklist sequence.** CODE-4's step is ordered before CODE-2's, because the tier-3 wire case
  CODE-2 carries exercises a never-fenced session's barrier and that barrier reaches the pod only once the
  session row's baseline is positive. Every staged deliverable appears once, and the sequence is: "**S1 ·
  spec** — SPEC-1, SPEC-2, and SPEC-3. §10.1 states the generation's scope on the pod side, what a hold
  covers, and the counter's baseline; §4.2 states the same baseline on the session record; and §28 and §29
  state the same scope. Tiers 0, 11. Depends on: —"; "**S2 · schema** — SCHEMA-1. The proto doc comments
  state the settled scope. Tiers 0, 3. Depends on: S1"; "**S3 · code** — CODE-1. The fenced generation moves
  onto the slot registry entry. Tiers 0, 1, 2. Depends on: S1"; "**S4 · code** — CODE-4. The session row's
  coordination generation is baselined at 1. Tiers 0, 1, 2, 3, 4. Depends on: S1"; "**S5 · code** — CODE-2.
  `CheckpointBarrier`'s gate reads the per-session generation. Tiers 0, 1, 3. Depends on: S3, S4"; "**S6 ·
  code** — CODE-3. The hold reports each terminated session's own generation; its scope is unchanged under
  D5. Tiers 0, 1, 8. Depends on: S1, S3"; and "**S7 · test** — TEST-1. Two co-tenant sessions handing off
  independently, on proposal 0060's two-replica harness. Tiers 7a, 8. Depends on: S3, S4, S5, S6".
- **The §28.8 `CH-BARRIER` exclusivity cell was recorded as a non-site on a reading of "superseded replica"
  the staged §10.1.8 does not support.** Pass 7 filed the cell under non-sites on the ground that a
  superseded replica is one whose successor's fence the pod has recorded, so the pod holds a value for that
  session. Holding a value is not holding a different one. The pod has no other datum to reject on:
  `CheckpointBarrierRequest` carries a session identifier, a generation, and a barrier identifier and names
  no replica (`schemas/lenny-adapter.proto:1475-1483`), and both barrier-target producers read the
  generation from state the replicas share rather than from the sending replica, the lease mirror row
  (`pkg/gateway/coordination/barrier/wiring.go:104-114`) or the live session row on the cache fallback
  (`cmd/lenny-gateway/httpsurface.go:592-599`). So the superseded replica's barrier carries the successor's
  own generation: staged §10.1.8 step 1 refuses it between the successor's compare-and-swap and its fence,
  and accepts it after, which is the state that makes the sender superseded. Applied as it stood, the
  unedited cell would have stated that such a barrier "is rejected on the stamp" while §10.1.8 stated it is
  accepted. SPEC-2 now stages the cell's closing clause onto §10.1.8 step 1's predicate and keeps the cell's
  constraint sentence and its pod-level-lock sentence. The alternative, keeping the exclusivity claim by
  narrowing §10.1.8's acceptance arm to exclude a replica that no longer holds `REG-COORDLEASE`, was
  rejected: it needs a replica identifier on the wire and a pod-side view of the lease, which is a new
  protocol surface on the one channel where a wrong rejection costs the drain checkpoint and double-captures,
  and it reverses D7. Acceptance after the successor's fence is already the shipped behaviour
  (`pkg/adapter/coordination.go:236-239`), so the cell and `spec/10_gateway-internals.md:183` already
  disagree with the tree and the edit makes that visible. D7's closing clause and §4's second blank drop the
  superseded-sender framing with it and state what equality catches, which is a barrier carrying a
  generation other than the one the pod holds for the session it names.
- **§28.6's "second opener" paragraph was filed whole under hold sentences, and its first sentence states
  the pod-side rejection SPEC-1 re-scopes.** Only the paragraph's second sentence is about hold state
  (`spec/28_communication-channels.md:1679-1683`). The first states that a replica opening one of the four
  gateway-to-pod channels without holding the current generation is rejected on the generation stamp, in the
  pod-singular unconditional form staged §10.1.2 step 3 retires, and it is selected by the same reasoning
  SPEC-2 gives for staging the `CH-FENCE` sentence one paragraph earlier. SPEC-2 now stages that first
  sentence and narrows the leave-alone entry to the paragraph's hold sentence. The re-scoped sentence keeps
  its existing relation, whether the opening replica holds the session row's current value, because it spans
  `CH-FENCE`, where an acquiring replica's fence carries a generation above the one the pod holds and must be
  accepted; giving it the barrier's equality predicate would turn every legitimate handoff into a rejection.
  The paragraph's closing sentence, "The constraint excludes a second replica", was checked and left,
  because §28.6 names `REG-COORDLEASE` together with the stamp as the guard and the lease alone excludes the
  second replica for a session the pod holds no generation for. Pass 3's record listing the whole paragraph
  among the hold sentences left alone stands as the record it is.
- **The §28.8 `CH-CHECKPOINT` exclusivity cell states a pod-side rejection and sat in no list.** The cell's
  first clause, that a stream opened by a replica that no longer holds the current generation is rejected on
  the stamp (`spec/28_communication-channels.md:1806`), is inside staged step 3's domain, which is the whole
  set of gateway-to-pod RPCs. For a session the pod holds no fenced generation for, the pod rejects nothing
  on generation grounds, and that state is reachable on a path §10.1.2 already defines: the fence-failure
  bullet leaves the generation increment in Postgres when the acquiring replica exhausts its retries and
  relinquishes (`spec/10_gateway-internals.md:39`), so the row's current generation is above the prior
  coordinator's stamp while the pod still holds no value. SPEC-2 now stages the clause in the same
  construction the second-opener sentence takes, so the mirrors read alike, and its sender-side second
  clause is unchanged. Recording it as a non-site was rejected, because no rationale covers the unset class.
- **SPEC-2's `spec/28` accounting was a set of enumerated lists with no membership rule, which is what
  produced the `spec/28` findings above.** SPEC-2 now states the criterion once before the lists: a
  sentence is an edit site when it states what the pod does with an RPC's generation and fixes the value
  that outcome is measured against, and it is not one when it states the exclusivity constraint and its
  guard, a duty on the sending replica, the hold, or the pod's validation without fixing the compared
  value. The non-site record
  is enumerated under it and now names §28.5.1's `CH-ATTACH` Preconditions bullet, the generation-stale
  sentence in §28.5.1's `CH-ATTACH` Degradation bullet and its §28.8 counterpart, §28.5.1's `CH-CHECKPOINT`
  and `CH-BARRIER` Exclusivity bullets, and §28.5.1's `CH-BARRIER` Messages bullet, which pass 9's first
  entry left to be recorded here.
- **The staged §29.7 clause dropped §10.1.8 step 1's guard, so it refused the case D7 accepts.** The clause
  read that the barrier is rejected when the generation it carries does not match the value the pod holds
  for that session, which presupposes a value exists and therefore refuses the never-fenced session, while
  §10.1.8 step 1 conditions the rejection on the pod holding a value at all. The clause now carries step 1's
  wording word for word: the barrier is rejected as a generation-stale RPC under the fencing rules when the
  pod holds a generation for that session that the barrier does not carry, and is accepted otherwise. The
  same guarded form is what SPEC-2 stages into the §28.8 `CH-BARRIER` cell, so every mirror of the predicate
  reads alike and §10.1.2 step 3 stays the one place that states the unset case.
- **The re-scoped §28.6 second-opener sentence and the §28.8 `CH-CHECKPOINT` cell kept the session row's
  current value as their relation, which §10.1.2 step 2 falsifies for the window it defines.** Both staged
  sentences read that a replica which no longer holds the current generation for the session is rejected
  when the pod holds a fenced generation for that session, and the record of the choice above stated that
  relation deliberately. Take a session whose pod holds generation 5 from the draining replica. The
  acquiring replica executes §10.1.2 step 1's compare-and-swap, so the row holds 6, and it has not yet
  fenced. The draining replica no longer holds the row's current value and the pod does hold a fenced
  generation for that session, so both staged sentences said its `Attach` or `Checkpoint` stream is
  rejected, while §10.1.2 step 2 states that "until the pod acknowledges the fence, the pod still accepts
  RPCs carrying the previous generation" (`spec/10_gateway-internals.md:38`) and staged step 3 accepts an
  RPC whose generation matches the value the pod holds. The window is ordinary rather than exotic: a
  periodic checkpoint the prior coordinator opens inside it is a `CH-CHECKPOINT` stream in exactly that
  position. The §28.6 paragraph was also self-contradictory as staged, because its closing sentence, left
  unchanged, states that the fence acknowledgement closes the window in which the prior coordinator's RPCs
  are still accepted (`spec/28_communication-channels.md:1683-1686`). D7's own grounds rest on the same
  step-2 sentence, so the proposal cannot stage text that denies it.
- **Both sentences now state the relation on the value the pod holds, and `CH-FENCE` is stated separately.**
  The §28.6 sentence names `CH-ATTACH`, `CH-CHECKPOINT`, and `CH-BARRIER`, gives them staged §10.1.2 step
  3's relation and its unset-value clause, and states `CH-FENCE`'s own rule in a following clause: the pod
  rejects a fence carrying a generation older than the one it holds for that session and records a higher
  one. The §28.8 `CH-CHECKPOINT` cell names one channel, so it takes the same relation with no `CH-FENCE`
  arm. The reason the earlier record gave for keeping the row-value relation holds only for `CH-FENCE`,
  where an acquiring replica's fence carries a generation above the one the pod holds and must be accepted;
  it does not extend to the other three channels, and the correction is to split the sentence by channel
  rather than to carry one relation across all four. Every mirror of the pod-side gate now states the same
  relation as §10.1.2 step 3, which is what SPEC-2's framing sentence claims for it.

### Pass 10 (2026-09-01, automated)

- **Staged §10.1.8 step 1 had the pod capture the drain checkpoint, which step 3 of the same protocol
  assigns to the gateway.** Pass 8's replacement for step 1's closing sentence ended its acceptance arm with
  "quiescing the session and capturing the checkpoint the drain would otherwise have taken", and both
  participles attach to the sentence's subject, the pod. Step 3 of the same numbered protocol, which this
  proposal leaves unchanged, states the opposite division of labour: the adapter holds the quiesced state
  open rather than driving its own checkpoint, and the gateway's barrier dispatcher opens the `Checkpoint`
  stream, drives the upload, and finalises the manifest row (`spec/10_gateway-internals.md:185`). §29.7 step
  5 states the same division for the same protocol (`spec/29_communication-scenarios.md:1193-1196`), and the
  tree matches step 3 rather than the staged sentence. The clause was also false on its own terms with the
  actor corrected, because the gateway-driven `Checkpoint` stream is started before the barrier RPC is sent
  and joined after it returns, so the capture runs on the refused arm as well
  (`pkg/gateway/coordination/barrier/barrier.go:217-227`); what acceptance buys is a quiesced workspace and
  the acked-barrier record. The consequence clause is withdrawn and the acceptance arm now ends at
  acceptance, which leaves the three staged mirrors of the predicate reading alike: §10.1.8 step 1, the
  §28.8 `CH-BARRIER` cell, and §29.7's framing paragraph all end at "and otherwise accepts it". Nothing is
  lost, because the paragraph's own rationale already states the correct consequence, that a barrier the pod
  accepts establishes the step-2 quiescence and the ack deadline stays the only failure arm §10.1.8 defines.
- **SPEC-2's live rationale for the §28.8 `CH-BARRIER` cell still asserted the consequence the entry above
  withdrew.** The rationale closed by calling `CH-BARRIER` "the one channel where a wrong rejection costs the
  drain checkpoint and double-captures", which the same tree reading refutes: `dispatchOne` starts
  `CheckpointWithTrigger` in a goroutine before `dispatch.Send` and joins it with `cpWG.Wait()` immediately
  after, and `ErrGenerationStale` sets only `out.Stale` on a path reached after that join
  (`pkg/gateway/coordination/barrier/barrier.go:217-233`), so the capture has already run when the barrier is
  refused. The sentence's argument, that the alternative mechanism is a new protocol surface on a channel
  where a wrong rejection is expensive, is unchanged; its cost clause now names what a refusal actually
  costs, which is the session's quiescence and the acked-barrier record, and the second capture the preStop
  loop then takes. The parallel sentence in the pass 9 record keeps the words it was written with.

### Pass 11 (2026-09-01, automated)

- **Staged §10.1.2 step 3 measured acceptance by which replica sent the RPC, while D7 measures it by the
  generation the RPC carries.** The staged closing sentence read that there is no window in which both the
  old and the new coordinator for a session can simultaneously issue RPCs the pod accepts for it, and staged
  §10.1.8 step 1 states the opposite outcome for `CheckpointBarrier`: the pod rejects the barrier when it
  holds a generation for that session the barrier does not carry, and otherwise accepts it. The barrier's
  generation is read from state the replicas share, either the `coordination_lease` mirror row
  (`pkg/gateway/coordination/barrier/wiring.go:104-114`) or the live session row on the cache fallback
  (`cmd/lenny-gateway/httpsurface.go:592-599`), and the dispatcher copies it onto the wire unchanged
  (`pkg/gateway/coordination/barrier/wiring.go:49`). After the successor's fence lands, a draining replica's
  barrier therefore carries the value the pod holds, clears `!initialized || gen != fenced`
  (`pkg/adapter/coordination.go:236-239`), and is accepted while the successor is issuing accepted RPCs for
  the same session. That is the window the staged sentence denied, and it quiesces tool-call dispatch under
  the new coordinator.
- **The fix states the rule on the carried value rather than adding a barrier exception.** Staged step 3 now
  closes with the pod holding one generation for that session at a time and accepting only RPCs carrying it,
  so an RPC carrying a generation a later fence superseded is refused from the moment that fence is
  acknowledged. That is D7's own principle, which already records that the refusal "is on the value rather
  than on the sender". No exception clause enters step 3, the comparison stays equality so §7's first open
  decision keeps the operator, the acceptance sentence keeps the whole set of gateway-to-pod RPCs as its
  domain, and step 2's two halves are untouched. The provenance a reader needs to reconcile the two sections
  is stated once, in staged §10.1.8 step 1, which already says the message carries the current
  `coordination_generation`: a barrier reaching the pod after the successor's fence has landed carries the
  value the pod holds and is accepted. SPEC-1's rationale is re-grounded to match, so the claim that step 3's
  opening sentence carries the acceptance rule's domain now attaches to the acceptance sentence that does
  carry it, and the two paragraphs defending the counter baseline name step 3's closing sentence by the
  equality gate it states.
- **The `spec/28` mirrors of §10.1.2 step 2's window restated it by its owner, and take the same
  correction.**
  §10.1.2 step 2 states the window as the one in which "the pod still accepts RPCs carrying the previous
  generation" (`spec/10_gateway-internals.md:38`) and SPEC-1 leaves it unchanged. §28.6's second-opener
  paragraph, §28.5.1's `CH-FENCE` Exclusivity bullet, and the §28.8 `CH-FENCE` row's "Holder of the
  exclusivity constraint changes" cell each restate it as the window in which the prior coordinator's RPCs
  are still accepted, which is false on `CH-BARRIER` for the reason above. Each takes the value form. The
  §28.6 sentence was previously recorded as agreeing with the staged first sentence and that verdict is
  withdrawn; it is named by its content, the fence-acknowledgement sentence, so it is no longer confused with
  the paragraph's "The constraint excludes a second replica" sentence, which stands. The `CH-FENCE`
  sentences are edit sites on the reading the proposal already applies to the §28.8 `CH-BARRIER` and
  `CH-CHECKPOINT` cells, which sit in the same exclusivity column and are staged: a sentence that states the
  constraint and also fixes when the pod stops accepting an RPC falls under the site arm of SPEC-2's
  criterion for the clause that fixes it. The criterion itself is unchanged.
- **The `spec/29` mirror of the same window clause was left unstaged.** §29.8 step 9 states that the
  acknowledgement is the hard precondition for the step, "so no operational RPC reaches the pod before
  the fence closes the window in which the prior coordinator's RPCs are still accepted"
  (`spec/29_communication-scenarios.md:1322-1326`), and it cites §28.5.1 `CH-FENCE` and §28.6, the two
  sentences the bullet above stages, so the specification's own cross-reference makes it their mirror.
  SPEC-2 now stages it into the value form with them, and the `spec/29` preamble names step 9 alongside
  steps 2 and 7. Applied without it, §28.5.1, §28.6, and §28.8 would state the window on the generation
  an RPC carries while §29.8 step 9 kept the owner form this pass has established is false on
  `CH-BARRIER`. The site went stale in this pass rather than earlier: before it, staged §10.1.2 step 3
  closed on the same owner form and step 9 agreed with it.
- **The new §28.6 fence-acknowledgement bullet declared the paragraph's other sentences unchanged, which
  covered the first sentence the bullet above it stages.** The clause it carried read "the paragraph's
  remaining sentences are unchanged" while it sat inside the first-sentence bullet, where "remaining" was
  relative to the sentence that bullet staged. Moving it into its own bullet widened it to every sentence
  but the fence-acknowledgement one, so two adjacent bullets of one edit list asserted opposite
  dispositions for the paragraph's first sentence. The clause now excepts that sentence by name.

### Pass 12 (2026-09-01, automated)

- **Pass 11 reconciled staged §10.1.2 step 3 with staged §10.1.8 step 1 by asserting a consequence that is
  false for an ordering the specification does not exclude, and that consequence is withdrawn.** The staged
  closing sentence of §10.1.8 step 1 read that, because the barrier carries the session's current
  `coordination_generation` rather than the draining replica's own stamp, a barrier reaching the pod after
  the successor's fence has landed carries the value the pod holds and is accepted. The generation is fixed
  when the barrier-target set is assembled rather than when the barrier arrives: the healthy path copies it
  from the `coordination_lease` mirror row (`pkg/gateway/coordination/barrier/wiring.go:104-114`), the cache
  fallback copies it from the live session row (`cmd/lenny-gateway/httpsurface.go:591-600`), and the
  dispatcher puts it on the wire unchanged (`pkg/gateway/coordination/barrier/wiring.go:49`,
  `pkg/gateway/coordination/barrier/barrier.go:183`, `:226`). When the set is assembled before the
  successor's compare-and-swap, the barrier carries the superseded value while the pod holds the value the
  successor fenced, and the same staged sentence's own predicate rejects it. On the healthy path that
  ordering is not a narrow race, because the mirror row is written from the sweep's pre-handoff snapshot
  (`pkg/gateway/coordination/coordination/coordination.go:430`) and therefore carries the predecessor's
  value for up to one sweep interval after the takeover.
- **The provenance the reconciliation needs is kept and restated on the read rather than on the arrival.**
  The staged sentence now reads that the generation a barrier carries is read from the session's
  coordination state when the barrier-target set is assembled rather than stamped by the draining replica,
  so a false positive reaches the pod carrying either the value the pod holds or a value a later fence
  superseded. Both outcomes stay reachable and neither is asserted, which is what §10.1.8 can state without
  bounding the interval between the read and the arrival. The predicate before it and the sentence recording
  that either outcome is safe are unchanged, so §10.1.2 step 3 remains the single owner of the acceptance
  predicate, the comparison stays equality for §7's first open decision, and D7's rule that the refusal is
  on the carried value rather than on the sender is untouched. SPEC-1's rationale for the replacement drops
  the word "ordinary" from the false positive it describes, because that false positive is one of the two
  orderings rather than the usual one.
- **The live rationale sites that repeated the withdrawn consequence take one uniform weaker form.**
  SPEC-2 argues that the §28.5.1 `CH-FENCE` Exclusivity bullet, §28.6's fence-acknowledgement sentence, and
  the §28.8 `CH-BARRIER` exclusivity cell are edit sites because the owner form of the window is false on
  `CH-BARRIER`. Each argument needs only that an accepting case exists, so each now reads that a superseded
  replica's barrier can carry the very generation the pod holds, in which case the pod accepts it. The
  §28.8 bullet also drops its trailing clause identifying that acceptance with the state that makes a
  replica superseded, which is where the universal re-entered. Every edit-site verdict stands under the
  weaker form and no staged replacement text changes. §29.7's staged framing paragraph and the §28.8
  `CH-BARRIER` cell's own staged text already end at acceptance with no consequence clause and are
  untouched.
- **The withdrawn consequence still stood in the §4 `CheckpointBarrier` gate bullet, which is the
  derivation §7's first open decision hands the reviewer.** The bullet bounded the case where
  equality refuses a superseded sender's barrier to the window between the successor's
  compare-and-swap and its fence. Under the read-time provenance this pass landed, a target set
  assembled before that compare-and-swap carries the superseded value while the pod holds the
  value the successor fenced, so the gate refuses it after the fence as well. The bullet now
  states that the barrier's generation is read when the target set is assembled and that equality
  catches a superseded sender whenever the assembly and the successor's fence straddle each other
  in either order, which is the weaker form the staged §10.1.8 sentence and the SPEC-2 rationale
  sites already take. The open decision it feeds is unchanged: what the reviewer settles is
  whether the comparison against a value the pod does hold stays equality.
- **The review log carried the withdrawn universal as an established fact and nothing retired
  it.** The `## Standing context` bullet on barrier provenance and the round-3 ledger FACT it was
  compacted from both ended on the window bound, and the standing context is the section every
  later agent reads, so the universal would have been re-imported into the staged text. Both are
  retired in the log's own ledger entry for this pass, keeping the surviving half, which is that
  the generation is read from shared state when the barrier-target set is assembled, so acceptance
  and refusal are both reachable and neither is asserted.

### Pass 13 (2026-09-01, automated)

- **Staged §10.1.8 step 1 closed the set of generations a false-positive barrier can carry, and the
  set is not closable from inside that step, so the enumeration is deleted rather than widened.** The
  staged replacement read that the generation is read from the session's coordination state when the
  barrier-target set is assembled rather than stamped by the draining replica, "so a false positive
  reaches the pod carrying either the value the pod holds or a value a later fence superseded", and
  closed on "Either outcome is safe and requires no special handling". More than one producer puts a
  mismatching value on the wire, and only one of those values is a superseded one. The `coordination_lease`
  mirror is written from the sweep's pre-`RecordHandoff` snapshot, so it can lag below the value the
  pod holds. The cache fallback reads the live session row
  (`cmd/lenny-gateway/httpsurface.go:592-596`), which §10.1.2 step 1's compare-and-swap has already
  advanced (`spec/10_gateway-internals.md:37`) while the pod still holds the previous value until it
  acknowledges the fence (`spec/10_gateway-internals.md:38`), so that read sits one above the pod's
  value. The same fallback seeds the target at `int64(0)` and overwrites it only on a successful
  read, so a Postgres fault during drain puts a value on the wire that no fence ever installed. The
  staged sentence now reads that the outcome follows from the generation the barrier carries alone,
  and the section states no enumeration of the reachable values, because which values are reachable
  is a property of the target producers rather than of this step. The provenance clause stays,
  because it is what explains how a replica that no longer coordinates the session can send a barrier
  the pod accepts, which is D7 and is what makes the §28.8 `CH-BARRIER` and `CH-CHECKPOINT` cells
  edit sites. The acceptance predicate stays stated once, in §10.1.2 step 3, and the comparison stays
  equality, which §7's first open decision reserves for the reviewer. The staged §29.7 replacement
  and the §7 open-decision bullet carry the predicate without an enumeration and are unchanged. The
  review log's `## Standing context` bullet on barrier provenance carried the same two-way
  enumeration and takes the same correction, with each producer named so a later round does not
  re-derive them.

### Pass 14 (2026-09-01, automated)

- **CODE-1 cited `pkg/adapter/server.go:304`, which is the first comment line of the `hold
  holdState` field D5 keeps, rather than the `coord coordinationState` field the deliverable
  removes.** The field is at `pkg/adapter/server.go:302` and the hold's declaration is at `:307`.
  `:304` is a comment line in the current tree and in the revisions before it, because the field
  block shifted up by three lines while `coord`, `hold`, and `barrier` kept their order, so the
  citation named a comment line from the start and now falls inside the doc comment of the field
  D5 keeps. The proposal's own problem statement already carried the correct citation (`:302`), so
  the two staged documents disagreed about one field. CODE-1 now names the field as well as the
  line, states that `:307` stays under D5 with only its `gen` field dropped by CODE-3, and states
  that `initialized` moves with `lastFenced`, which is D6's cascade: left on `Server` it flips on
  the first fence anywhere on the pod and makes every later co-tenant's first fence report a gap.
- **CODE-1 moved `coordinationState`, which carries `quiesced`, while `barrierGate` stayed a single
  pod-wide gate, so D7 and the counter baseline together made the co-tenant collision the ordinary drain
  outcome.** `Server.barrier` (`pkg/adapter/server.go:314`) has one waiting slot, no session key, an
  `open()` that overwrites `done`, `checkpointID`, and `signaled` unconditionally
  (`pkg/adapter/coordination.go:158-166`), and a `link()` with no session check (`:180-188`). Once a
  never-handed-off co-tenant's barrier is accepted on the unset arm, the gateway's concurrent per-target
  fan-out (`pkg/gateway/coordination/barrier/barrier.go:190-201`) puts two barriers in that one slot: the
  first blocks to the shared 90s ack deadline and returns an empty or cross-linked `checkpoint_ref`, which
  `dispatchOne` persists into `session_checkpoint_meta` under the wrong session id (`:238-245`), after
  which prestop captures that session a second time against a live workspace, the harm D7 exists to
  remove. CODE-1 now moves `barrierGate` onto the slot registry entry with the state, and CODE-2 has the
  gateway-driven `Checkpoint` stream link its checkpoint id into the gate of the session its
  `CheckpointStart` names (`pkg/adapter/checkpoint.go:122`). §10.1.8 step 3 already fixes the gate's unit
  at the session, so this is a departure from shipped specification text that the deliverable closes
  rather than a new decision, and no open decision and no further deliverable are added. §3 and the
  `coord.mu` bullet in §4 carry the same statement, the latter recording that the gate keeps its own leaf
  mutex and is unaffected by the `coord.mu` question. A second session-keyed map was rejected under D2,
  and serialising co-tenant barriers behind one pod-wide gate was rejected because the two barriers share
  one wall-clock deadline, so the second session's barrier would time out on an ordinary drain.
- **CODE-2 stated a bare line citation and no predicate.** It now states that the gate refuses the
  barrier when `initialized && gen != fenced` holds for the entry resolved for the session the request
  names, which is D7 and staged §10.1.2 step 3, with the comparison operator left to §7's first open
  decision, and it names the quiesce-and-hold and link sites that read the same entry. The summary's
  watch-out that CODE-2 does not state the gate change is retired in the same edit.

### Pass 15 (2026-09-01, automated)

- **§8's pinning case asserted that one session's fence leaves a co-tenant's hold intact, which is
  the negation of what D5 stages.** D5 keeps the hold pod-scoped, §6 lists changing which sessions
  a pod-level hold covers as a non-goal, and the staged §29.10 bullet states that a successful
  fence for any one session exits the hold for the pod. The tree agrees: `exitHoldState` runs on
  the accepted arm of `CoordinatorFence` with no session predicate
  (`pkg/adapter/coordination.go:124-129`; `pkg/adapter/holdstate.go:142-153`). A case written to
  that clause fails against the post-fix code as well as the pre-fix code, and because the clause
  was §8's only hold statement, nothing pinned what CODE-3 changes, while the landed case that
  encodes the pod-wide value (`TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1`,
  `pkg/adapter/holdstate_test.go:674`, block at `:700-716`) sat in no edit list and would have
  turned tier 1 red with no disposition recorded. §8 now states the cases by tier and by what each
  tier can observe: the per-session fence, barrier, and gap behavior and the independence of the
  barrier gates in `pkg/adapter/coordination_test.go`; the per-session hold record as an amendment
  of that landed case in `pkg/adapter/holdstate_test.go`, with `sess-a` at 7, an unfenced `sess-b`
  at 0, and the pod-level `coordinator_connection_lost` line carrying `started_sessions` and no
  `last_generation` key; the co-tenant handoff that the pre-fix pod refuses, on proposal 0060's
  two-replica harness at tier 4, where the gateway-side harm is observable (`FenceReadopter`
  relinquishes the lease and the `Sweeper` records an adoption backoff); and the concurrent-fence
  and concurrent-barrier cases at tier 7a under `-race`. The accepted-handoff, accepted-barrier,
  and no-gap assertions stand as written.
- **§8's tier list omitted tier 4, and its harness sentence left the case's home as a guess.** The
  tier list is now 0, 1, 2, 3, 4, 7a, and 8, stated once in §8. Tier 4 is where the production
  harm this change repairs is observable, because the refused fence makes `coordfence.Fencer`
  relinquish leadership and leaves the co-tenant session in adoption backoff, and
  `test-coverage.md` puts a flow crossing the gateway and a datastore at tier 4. The hedge that
  the case "probably belongs in that harness" is replaced by the split above. A tier-8 co-tenant
  case was rejected: the co-tenancy and crash dimensions do not interact in the gateway, since the
  sweep adopts one session at a time and the only cross-session state is inside the adapter, so
  tier 8 stays on CODE-3. The checklist's TEST-1 step still reads "Tiers 7a, 8" and its CODE-1
  step still reads "Tiers 0, 1, 2". Both take the tiers stated here in the single step-list
  rewrite this round makes, rather than here, so that no step number is anchored twice while the
  list is being renumbered.
- **Nothing recorded that the fixture `CODE-1` breaks is consumed only from build-tagged test
  trees.** §8 now states that `tests/testinfra/coordfixture` carries no build tag of its own, so
  tier 0's `go vet ./...` catches the accessor change inside the fixture, while its callers in
  `tests/tier4_integration`, `tests/tier7a_load_local`, and `tests/tier8_chaos` compile only under
  `integration`, `load_local`, and `chaos`, which tier 0 does not vet
  (`cmd/lenny-test/cmd_run.go:498-508`). `go vet` runs under each of those tags after CODE-1,
  because a signature change left unvetted there surfaces as a compile failure that loses every
  case in the package at once. The path `tests/tier7a_load/` named in the files-touched list does
  not exist, because the local tier-7a suite is `tests/tier7a_load_local/`. That correction and
  the files the cases above name, `pkg/adapter/coordination_test.go`,
  `pkg/adapter/holdstate_test.go`, `tests/testinfra/coordfixture/coordfixture.go`,
  `tests/tier4_integration/coordination_fence_split_brain_test.go`,
  `tests/tier7a_load_local/coordination_colocation_race_test.go`, and
  `tests/tier8_chaos/coordination_crash_takeover_test.go`, land in §9's files-touched list, which
  is rewritten once for this round together with CODE-4's entries rather than patched twice.

### Pass 16 (2026-09-01, automated)

- **The staged spec text named CODE-4 as the deliverable that lands the counter baseline, and no
  deliverable, files-touched entry, or checklist step produced it.** Applying the proposal as it
  stood would have landed SPEC-1's and SPEC-3's statement that a session row carries
  `coordination_generation = 1` from creation while
  `migrations/0050_session_record_fields.up.sql:38-39` still read `NOT NULL DEFAULT 0` with a `>= 0`
  check and `pkg/gateway/coordination/coordfence/coordfence.go:147-153` still floored a non-positive
  row value at 1, which SPEC-1 asserts as already deleted. D7's repair was stranded with it, because
  an ordinary never-handed-off session's drain barrier still carried 0 and was refused by the
  adapter's non-positive guard (`pkg/adapter/coordination.go:224-226`) before the gate CODE-2
  changes was reached. The non-spec changes now carry `### CODE-4`, taking the body pass 9 stated:
  the 0181 migration pair, the `Create` floors in both session stores, and the `coordfence` floor
  deletion, as one deliverable in one commit. Splitting the migration from the floors was rejected
  in both orderings, because `pgstore.Create` names `coordination_generation` in its insert column
  list (`pkg/gateway/session/sessionstore/pgstore/pgstore.go:177`), so a commit that tightens the
  check while either `Create` path still writes an explicit zero rejects every session insert, and
  the reverse ordering leaves a commit whose only content is a default and a check nothing depends
  on. §8 gains CODE-4's cases and the assertions the baseline shifts in landed tests, and the
  checklist gains CODE-4's step ordered before CODE-2's, because CODE-2's tier-3 wire case exercises
  a never-fenced session's barrier and that barrier reaches the pod only once the row's baseline is
  positive. Pass 9's enumeration of the shifted assertions was incomplete: it named `sweep_test.go`
  as the whole test-lane consequence and missed `:362-365` inside it,
  `pkg/gateway/coordination/coordination/coordination_takeover_test.go`, whose rows are seeded
  through `mustCreate` with the field unset (`coordination_mirror_test.go:65-72`),
  `pkg/gateway/session/sessionstore/memstore/memstore_test.go:309-325`, and
  `tests/tier7a_load_local/coordination_colocation_race_test.go:144`, `:287-288`. §8 therefore
  states the class rather than a line list, so a later fixture does not fall outside it. Pass 9's
  removal instructions, dropping `pkg/gateway/coordination/barrier/barrier.go` from the
  files-touched list and a dispatcher case from §8, were checked and are no-ops: pass 8 never wrote
  either into the deliverable files.
- **The files-touched list and the checklist reached `spec/10` alone, so SPEC-2 and SPEC-3 would
  never have been applied and step S6 named a deliverable that did not exist.** The staged spec
  edits are SPEC-1, SPEC-2, and SPEC-3. SPEC-2 edits `spec/28_communication-channels.md` and
  `spec/29_communication-scenarios.md`, and SPEC-3 edits `spec/04_system-components.md` §4.2.
  Neither file was listed and neither deliverable had a step, so running the checklist as one
  sequence would have left `spec/28` and `spec/29` restating the pod-wide fence rule while citing a
  `spec/10` that says the opposite, which is the two-incompatible-rules state SPEC-2 exists to
  prevent. §9 is rewritten once for this round, carrying `spec/28`, `spec/29`, `spec/04`, the
  sections SPEC-1 edits under `spec/10`, CODE-4's migration pair, both session stores, `coordfence`, the adapter
  sites CODE-1 and CODE-2 reach, the fixture, and the test files pass 15 named, and correcting
  `tests/tier7a_load/`, which names no directory in the tree, to `tests/tier7a_load_local/`. The
  checklist is rewritten as one sequence, taking pass 9's target sequence with the tier corrections
  pass 15 derived: S1 bundles SPEC-1, SPEC-2, and SPEC-3, because each step is one commit and
  separating them leaves the applied specification self-contradictory at two commit boundaries that
  no tier-0 or tier-11 gate catches; S3 carries CODE-1's own tier list rather than the narrower one,
  because the fixture it changes is consumed from build-tagged trees tier 0 does not vet; S5 gains
  tier 7a for the concurrent-barrier case and names S1 in its Depends-on, since every code step's
  Depends-on names the spec step staging the statements its work implements; and S7 carries TEST-1's
  tiers as §8 states them. TEST-1 gains a deliverable heading naming where its cases land and
  pointing at §8 for the assertions, so no step names a deliverable that does not exist. Renaming S7
  to an existing deliverable was rejected, because no other deliverable covers the co-tenant handoff
  test and pass 9, the summary, and the ledger all name it TEST-1.
- **CODE-3's body still deferred to a §7 hold-state decision D5 replaced, which the new S6 line
  contradicted.** The step's one-line description states the D5 position, so the body states it too
  rather than leaving the two disagreeing: the hold stays pod-scoped with its arming signal,
  rejection set, and termination set unchanged, `holdState.gen` (`pkg/adapter/holdstate.go:43`) and
  the pod-wide `LastFencedGeneration()` read that arms the hold (`:119`) are dropped, the pod-level
  `coordinator_connection_lost` line carries the started-session count and no generation
  (`:130-132`), and `terminateHeldSession` (`:225`) and `writeHoldPostMortem` (`:283`) read each
  terminated session's own value off the `*slotState` the `heldSession` carries
  (`pkg/adapter/slotsession.go:282-285`).
- **The rewritten baseline-shift inventory named one file whose assertion does not shift and omitted one
  that does.** `pkg/gateway/coordination/coordination/coordination_mirror_test.go` contributes no shifting
  assertion. Its only generation assertion (`:116-117`) reads `s1`, which
  `TestSweepMirrorsHeldLeasesAndReleasesTerminal_spec_10_1_165` seeds explicitly at 2 (`:84`), so §8's own
  exemption for a generation seeded above the baseline covers it, and the two rows that file does seed with
  the field unset (`:85-86`) are never asserted on the generation. The mirror test moves into the exemption
  list beside `wiring_test.go:171`, which also records why it is absent from §9. The enumeration now cites
  the takeover test's own seed sites (`coordination_takeover_test.go:74`, `:142`, `:241`, `:301`) rather
  than the `mustCreate` helper declaration in the mirror file, and it gains
  `tests/tier8_chaos/coordination_crash_takeover_test.go`, whose session is seeded with the field unset
  (`:239-241`) and whose assertions of 1, 1, and 2 at `:267`, `:283`, and `:296` become 2, 2, and 3. That
  file was already in §9 for CODE-1's accessor change with no statement that its generation assertions move
  as well.
- **§8's case list stated no tier-8 case while the tier sentence, CODE-3's body, and checklist S6 all
  claimed tier 8.** The pass-15 rewrite replaced the one case whose tiers were 7a and 8 with cases at tiers
  1, 4, and 7a, so tier 8 was asserted three times and pinned nowhere, and the landed tier-8 file was left
  with no disposition even though it stops compiling under CODE-1: `pod.LastFenced()` at `:150`, `:195`, and
  `:223` calls `coordfixture.Pod.LastFenced` (`tests/testinfra/coordfixture/coordfixture.go:115`), which
  CODE-1 gives a session key. §8 gains a tier-8 bullet stating that amendment, the generation shift, and
  what a tier-8 failure means. Pass 15's "tier 8 stays on CODE-3" does not survive the check: the chaos
  suite carries no coordinator-loss hold case, so nothing at tier 8 exercises `holdstate.go`. Tier 8 is
  reached through CODE-1's fixture accessor and CODE-4's baseline instead. CODE-3's tier line and checklist
  S6 drop tier 8 and state why, and CODE-4's tier line and checklist S4 gain it.
- **§8 asked for a tier-1 case in each session store, and `pgstore.Create` cannot run at tier 1.**
  `pgstore.New` takes a `*pgxpool.Pool` (`pkg/gateway/session/sessionstore/pgstore/pgstore.go:60`) and
  `Create` executes its insert through `pgtenant.InTx` (`:249`), so exercising the floor there needs a live
  database, which is tier 2 under `test-coverage.md`. `pgstore_test.go` is an in-package unit file whose two
  tests do not reach `Create`. The sentence splits: the `memstore` half stays at tier 1 as the amendment of
  `TestCreateDefaultsSessionRecordFields`, and the `pgstore` half becomes a tier-2 case in
  `tests/tier2_component/stores/sessionstore_test.go`, which builds the store over a Postgres container with
  the production migrations applied (`:79`), asserting that a zero `CoordinationGeneration` reads back 1 and
  that the tightened `>= 1` check does not reject the insert. That file joins §9, which named the pgstore
  source file with no test file able to hold its half of the floor.

### Pass 17 (2026-09-01, automated)

- **SCHEMA-1 edited the adapter proto and no edit list carried the stubs a tier-0 gate diffs against a
  fresh regeneration.** protoc-gen-go copies a proto leading comment into the committed stubs verbatim, so
  `schemas/lenny-adapter.proto:1451` reappears at `pkg/proto/adapter/v1/lenny-adapter.pb.go:4966` and the
  `CoordinatorFence` RPC comment (`schemas/lenny-adapter.proto:153`) reappears at
  `pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go:180` and `:632`.
  `TestProtoStubsMatchGeneratedOutput` (`tests/tier0_static/proto_no_drift_test.go:70`) reproduces the
  `generate-proto` make target (`Makefile:91-94`) and diffs its output against the committed `pkg/proto`
  tree, so applying SCHEMA-1 without regenerating fails tier 0, the tier step S2 declares, and leaves the
  tracked Go doc comments stating the pod-wide record-and-reject rule SPEC-1 and D6 replace. SCHEMA-1's
  body now states that `make generate-proto` regenerates both stubs into the same commit and names which
  comment class lands in which stub, §9 names both stubs as regenerated output that is never hand-edited,
  following how proposals 0026 and 0034 list the same file, and step S2's body carries the regeneration
  clause with its tiers unchanged. Hand-editing either stub is barred by `code-best-practices.md`, and
  SCHEMA-1's own carrier list is left as it stands, since widening it is a separate residue and this
  correction reads the same either way.
- **Migration 0181 was referenced by no test under `tests/tier2_component/migrations/`, which the tier-0
  migration lint requires.** Pass 3 of `scripts/lint-migrations.sh` (`:45`, `:74-88`) fails a migration
  whose sequence number appears in no `_test.go` under that directory alone, and `cmd/lenny-test/cmd_run.go`
  runs the script inside the tier-0 static block (`:635-641`). Every migration on disk is referenced there
  today and 0181 was not, so applying CODE-4 as staged turned tier 0 red with "no test references migration
  0181". §8 now lands the tier-2 case it already declared in a new file under
  `tests/tier2_component/migrations/`, states the directory constraint as the reason the location is fixed,
  and adds the `.down.sql` and both column defaults to the assertions; §9 carries the new file. 0181 also
  takes the `prodMigrationSchema` entry `{migration: "0181", table: "sessions"}`, naming no column, which is
  the suite's convention for a column-less migration (0180 at `prod_columns_test.go:583`, 0112 at `:295`)
  and is what makes `TestProdMigrationsRollBackPerStep` (`:610`) step the migration's `.down.sql`; the
  behavioral assertions live in the new file, as `checkpoint_slot_id_drop_test.go` holds 0180's. §9 carries
  `prod_columns_test.go` beside the new file. Satisfying the lint with a number-bearing comment in
  `prod_columns_test.go`, the 0062 and 0150 convention, was rejected: that convention is for a migration
  with no behavior case of its own, and it would leave the suite that owns migration contracts silent about
  the only migration this proposal adds.
- **§8's baseline-shift class was too narrow, and the class it omitted holds a landed tier-1 test CODE-4
  breaks.** The class as written covered assertions that read a session row's `CoordinationGeneration`, and
  `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1`
  (`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go:992`) asserts nothing about that row. It seeds
  a prior active manifest row at `CoordinationGeneration: 1` as a fenced newer writer (`:1007`) against a
  session `runningSession` creates with the field unset (`checkpointer_test.go:89-96`), and its own doc
  comment states that dependence (`:993-995`). Under the baseline both the supersede guard
  (`pkg/gateway/checkpoint/checkpointer/uploaddriver.go:422`) and the store's fence
  (`pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore.go:394`), which compare strictly
  greater, see 1 against 1 and stop firing, so the case fails at its stale-generation `t.Fatal` (`:1015`)
  and at every assertion that the higher-generation row was left untouched. §8 now states two classes, the
  second being a fixture that seeds another row's generation as a constant chosen relative to a session row
  created unset, records the constant moving to 2 and the doc comment moving with it, states that a sweep
  of the seeded constants finds no other site of that class, and reconciles the closing exemption, which
  covers a constant seeded above the baseline rather than at it. §9 carries the file, and the step landing
  CODE-4 amends it in the same commit. Editing the shared `runningSession` fixture instead was rejected,
  because it is the checkpointer package's whole session fixture and no other case there reads the field.
  Weakening either guard to greater-or-equal was rejected: both are spec-cited fences, and the test is what
  shifts.
- **Correction to this pass's migration-0181 bullet: the 0180 precedent it cited says the opposite, and the
  omitted registry entry costs a rollback step.** The bullet above, and the §8 text it landed, stated that
  0181 takes no `prodMigrationSchema` row and that migration 0180 sits in the suite that way. 0180 has a row
  (`tests/tier2_component/migrations/prod_columns_test.go:583`), and the comment above it records that the
  row exists because the migration adds no column, so that `TestProdMigrationsRollBackPerStep` steps its
  `.down.sql` while the SQL surface is asserted in `checkpoint_slot_id_drop_test.go`. Migration 0112 (`:295`)
  is handled the same way. The consequence of the omission is behavioral: `TestProdMigrationsRollBackPerStep`
  (`:610`) iterates `prodMigrationSchema` alone, highest number first, and calls `MigrateTo(number-1)` per
  entry, so with 0181 absent the walk goes from HEAD to 0179 and 0181's `.down.sql` is never applied as its
  own step, leaving its rollback unasserted by the suite that owns per-step rollback. The tier-0 lint this
  pass was about is satisfied by the new behavior file either way. §8 and the bullet above now state the
  convention the suite follows and give 0181 the entry `{migration: "0181", table: "sessions"}`, and §9
  carries `prod_columns_test.go`. Relying on the new behavior file to cover the rollback was rejected: it
  applies the migration's own `.up.sql` and `.down.sql` against a container, which does not exercise the
  full-chain per-step walk that the table drives.

### Pass 18 (2026-09-01, automated)

- **TEST-1 and §8 attributed the tier-4 fence to a real `coordfence.Fencer`, which proposal 0060's harness
  does not hold and no test in the tree constructs.** The harness's `coordination.Readopter` is
  `coordfixture.FenceReadopter`, whose `ReadoptAndFence`
  (`tests/testinfra/coordfixture/coordfixture.go:220-241`) calls `Pod.Fence`, the `adapterclient`
  `CoordinatorFence` RPC, directly (`:231`). `coordfixture`'s lenny import block (`:35-39`) does not
  include `pkg/gateway/coordination/coordfence`, nothing under `tests/` constructs a `Fencer`, and the
  fixture's own comment records that it stands in for the component: a session listed in `Fail` "models the
  coordfence terminal relinquish" (`:196`). The distinction is behavioral, because `Fencer` carries the
  re-read-and-retry-or-relinquish policy loop and the stale and retry counters
  (`pkg/gateway/coordination/coordfence/coordfence.go:155-188`) that `FenceReadopter` has no counterpart
  for. The attribution stood in TEST-1's landing sentence, in §8's tier-4 justification, and in the tier-4
  case bullet, whose other half already named `FenceReadopter` with a citation that resolves onto it, so
  the bullet named two components as the actor on one path. TEST-1 and the case bullet now name the real
  `Sweeper` driving `coordfixture.FenceReadopter` and a genuine `CoordinatorFence` over the in-process
  adapter, and §8's justification states the path over the sweeper, the lease store, and the pod, records
  that production runs it through `coordfence.Fencer`'s policy loop, and states that the harness
  substitutes `FenceReadopter` without that loop while the case asserts the pod's per-session verdict,
  which the substitution does not change. This agrees with CODE-1, which already named `FenceReadopter` as
  the harness's fence driver. The case bullet also said one pod holds two sessions without saying how,
  while `coordfixture.StartPod` starts exactly one session per adapter (`:76`, `:98-102`), so it now states
  that the second session is started over the pod's already-dialed `Pod.Client`. Wiring a real `Fencer`
  into `coordfixture` was rejected: both drivers produce the same observable outcome on both arms of this
  case, it would duplicate production wiring that exists once in
  `cmd/lenny-gateway/coordination_seams.go:155-160`, and it would pin a policy loop that
  `pkg/gateway/coordination/coordfence/coordfence_test.go` already pins at tier 1 over the `FenceClient`
  seam the loop is parameterized for. Dropping the tier-4 case was rejected, because tier 4 is where the
  production harm is observable and pass 15 already adjudicated tier 4 as this case's home. §9's file list,
  the implementation checklist, and the summary are unaffected: `coordfixture.go` stays staged for the
  session-key parameterisation and the fence comment alone, step S7 names no component, and the summary
  names none.

### Pass 19 (2026-09-01, automated)

- **CODE-1 changed `Server.LastFencedGeneration` while the checklist left its only production
  caller to a later step, so that step could not compile.** CODE-1 gives the accessor a session id
  and states that no pod-wide variant survives beside it, while the checklist ran CODE-1 as S3 and
  CODE-3 as S6, and CODE-3 carried the deletion of the call at `pkg/adapter/holdstate.go:119`. A
  tree with S3 landed and S6 not landed still reads `gen := s.LastFencedGeneration()` with no
  argument at that site, and `:128` and `:130-132` consume the value, so package `pkg/adapter`
  does not build and every tier S3 names fails with it. The dependency is mutual, because CODE-3's
  per-session read takes the value off the `*slotState` entry that CODE-1 creates, so reordering
  only reverses which side fails. Checklist step S6 is therefore merged into S3, which now lands
  CODE-1 and CODE-3 together at CODE-1's tier set, and the old S7 becomes S6 with `Depends on: S3,
  S4, S5`. The deliverable identifiers are unchanged, and S1 landing SPEC-1, SPEC-2, and SPEC-3
  together is the existing precedent for one step carrying several deliverables. CODE-1 now states
  that the accessor's only production caller is the hold's pod-level arming read, which sits where
  no session id exists, so CODE-3 deletes it rather than giving it an argument, and that the two
  deliverables land in one step because no tree between them compiles. CODE-3 completes its
  enumeration of what dropping `holdState.gen` drags, so the atomicity is checkable: the arming
  read is the field's only writer (`:119`, `:128`), `onHoldTimeout` is its only reader (`:187`),
  and the value flows from there into `terminateHeldSession` (`:206`) and `writeHoldPostMortem`.
  The summary's "Watch out for" paragraph records the co-landing constraint beside the S1 and
  CODE-4 ones. Keeping a pod-wide accessor variant beside the per-session one was rejected:
  CODE-1's own text bars it, the project ships one canonical implementation per concern, D5 leaves
  the pod-level `coordinator_connection_lost` line carrying no generation so the survivor would
  end the change with no caller, and it does not make tier 1 green, since
  `TestCoordinatorHoldTimeoutDropsItsEmissionsWithNoSink_spec_10_1`
  (`pkg/adapter/holdstate_test.go:674`) fences `sess-a` at 7 and asserts that generation for both
  terminated sessions (`:713`), which only CODE-3's per-session read corrects. Moving the field
  deletion and the `:119` read into CODE-1 while leaving CODE-3 the two per-session reads was
  rejected, because the deletion does not stop at `:119`: it drags `:187` and the parameter at
  `:206`, at which point CODE-1 has absorbed CODE-3, and a variant that keeps the field unwritten
  compiles and goes red at tier 1 on that landed hold case. Merging the deliverables themselves
  was rejected, because it renumbers CODE-4 or leaves a gap while §8 refers to CODE-3 by name.

### Pass 20 (2026-09-01, automated)

- **§8 put a tier-8 file's accessor edit and its baseline shift in one pass, which turns a declared
  tier red under the checklist's S3 and S4 split.** The baseline-shift paragraph said of
  `tests/tier8_chaos/coordination_crash_takeover_test.go` that it "is already in §9 for CODE-1's
  accessor change, and it takes both edits in the same pass", while CODE-1 lands in S3 and CODE-4
  lands in S4 and each step declares tier 8. Both batchings fail. In S3 the generation assertions
  would read 2, 2, and 3 while migration 0181 and the `pgstore.Create` floor have not landed, and
  the file builds its store over the production `pgstore`
  (`tests/tier8_chaos/coordination_crash_takeover_test.go:85`), so a session row seeded with the
  field unset still baselines at 0. In S4 the file still calls `pod.LastFenced()` with no session
  argument after S3 changed the signature at `tests/testinfra/coordfixture/coordfixture.go:115`.
  §8's own tier-8 case bullet already states the disposition in the plural ("the steps landing them
  do not turn tier 8 red"), so the two sentences gave incompatible instructions for one file. The
  sentence now states the split: the step landing CODE-1 rescopes the `pod.LastFenced` reads at
  `:150`, `:195`, and `:223` and leaves the assertions at 1, 1, and 2, and the step landing CODE-4
  shifts them. The edits sit on disjoint lines, those reads belonging to the two subtests whose
  sessions are seeded at 1 explicitly (`:118`, `:179`).
- **The same split was stated for the other file that takes both edits.**
  `tests/tier7a_load_local/coordination_colocation_race_test.go` carries a `pod.LastFenced` read at
  `:260` and a `CoordinationGeneration: 0` seed at `:144` with its assertion of 0 at `:287-288`, so
  it is the second and last member of the two-edit class, and stating the rule for the tier-8 file
  alone would have left the identical defect one sentence later. Because CODE-4's `Create` floor
  rewrites that explicit zero, S4 edits a tier-7a file: the checklist's S4 gains tier 7a and the
  CODE-4 tier line in §8 reads 0, 1, 2, 3, 4, 7a, and 8. No dependency edge was added between S3
  and S4, because the two edits are disjoint by line and by subtest in both files, so either order
  leaves both tiers green.

### Pass 21 (2026-09-01, automated)

- **CODE-2 exempted the `Checkpoint` link site from CODE-1's hold-the-pointer rule, on a guard that
  runs before an unbounded wait.** CODE-2 said that `checkpointRootsForSession`
  (`pkg/adapter/slot.go:153-166`) "has already failed the stream with `FailedPrecondition` when the
  entry is absent, so the link site never sees a missing entry", while CODE-1 said one paragraph
  earlier that a handler holds the resolved pointer for the life of the call because "a second lookup
  by session id returns nothing". The exemption is false on the tree. The guard runs at
  `pkg/adapter/checkpoint.go:94` and returns only the resolved roots, the link runs at `:122`, and
  between them sits `s.ops.Begin` (`:111`), which admits a checkpoint for a session identifier no
  pending checkpoint names and queues it behind the running one (`pkg/adapter/oplock.go:119-129`), so
  the interval is bounded by a co-tenant session's whole upload. Both deregistration paths run in that
  window with no coordination with an open stream: `Shutdown` (`pkg/adapter/session.go:237-239`) and
  the hold timeout's `deregisterStartedSessions` (`pkg/adapter/slotsession.go:347-361`). CODE-1 now
  states the rule once over the three handlers that touch the relocated state, `CoordinatorFence`,
  `CheckpointBarrier`, and `Checkpoint`, each named with the guard that precedes its resolve, and it
  backs the third with the mechanism: `checkpointRootsForSession` returns the resolved `*slotState`
  alongside the roots, so the stream links and completes on the entry its own guard validated.
  `restoreChunks` (`pkg/adapter/resume.go:178`) is the helper's other caller and discards the entry.
  CODE-2's quiesce-and-hold and link sentences moved into CODE-1 with the rule, which also repairs a
  step-ordering defect the deliverable split created: `Server.barrier` ceases to exist in the step
  landing CODE-1, so `pkg/adapter/checkpoint.go` cannot compile until that step rewires it, while
  CODE-2 lands two steps later. CODE-2 keeps the generation gate at `pkg/adapter/coordination.go:236`,
  which is what its heading names. §9 records the helper's signature change on the `pkg/adapter/slot.go`
  line, the link and the deferred complete on the `pkg/adapter/checkpoint.go` line, and adds
  `pkg/adapter/resume.go`.
- **Nothing in §8 pinned the mid-flight deregistration path CODE-1's per-entry move creates.** The two
  readings of "resolve the entry", the detached pointer CODE-1 requires and a re-lookup by session id,
  pass every case §8 listed identically, so the invariant had no witness and `test-coverage.md`'s
  requirement on the concurrent path was unmet. §8 takes one tier-1 case in `pkg/adapter`: a
  `Checkpoint` stream past the guard and queued on the op lock behind a co-tenant's checkpoint has its
  registry entry deregistered before the link, and the stream still links into the entry the waiting
  `CheckpointBarrier` holds, the ack carries that id, the barrier's deferred quiescence clear runs
  against the detached entry, and a co-tenant's open barrier is unaffected. The bullet fixes the two
  constraints an implementor cannot derive: the case lands in the external `adapter_test` package
  beside the concurrent stream fixture (`pkg/adapter/checkpoint_stream_test.go:417`), because
  `pkg/adapter/coordination_test.go` is `package adapter` and cannot reach it, and both deregistration
  paths remove the slot tree (`pkg/adapter/session.go:271`, `pkg/adapter/holdstate.go:249`), so the
  assertions are on the link, the ack, and the return rather than on a successful `CheckpointSummary`.
  It is at tier 1 because tier 1 already runs `-race` (`cmd/lenny-test/cmd_run.go:880`) and the path is
  observable only inside `pkg/adapter`. §8's preamble now reads "Each must fail against the pre-fix
  code, except where a bullet states otherwise", which the new case needs, since a pod-wide gate is
  never absent, and which also regularises the two bullets already marked as amendments of landed
  cases. TEST-1's file list and §9's test-file line take the new file. The checklist is unchanged: the
  case is tier 1, S6 already declares tier 1 and depends on S3, and no step's tier set or dependency
  moves.
- **§8's IMPLEMENTOR'S CHOICE paragraph still left the new case's file and helper open.** The paragraph
  gave "which existing tier-1 file in `pkg/adapter` each new case lands in" and "the helper that binds
  and starts a second session on one `adapter.Server`" to the implementor for every new case, while the
  mid-flight deregistration bullet fixes both and states why one of the permitted answers is impossible:
  the other tier-1 files TEST-1 names, `pkg/adapter/coordination_test.go` and
  `pkg/adapter/holdstate_test.go`, are `package adapter` and cannot reach the external fixtures the case
  binds its second session with. The paragraph now scopes the choice to the cases whose own bullet
  leaves it open, and records that the mid-flight deregistration case lands in
  `pkg/adapter/checkpoint_stream_test.go` in the external `adapter_test` package with the fixtures its
  bullet names. The `// spec:` annotation and the amended hold case's name are unchanged.
- **§8's tier-split paragraph enumerated the tier-1 subjects and pinned every concurrency case at tier
  7a, which excluded the new case.** The new case's subject is the lifetime of a resolved registry entry
  across a deregistration, which the tier-1 list did not name, and its arrangement is concurrent, which
  the tier-7a sentence claimed as a rule. The tier-1 list now names the entry lifetime beside the
  per-session fenced value, the hold records, and the independence of the barrier gates. The tier-7a
  sentence now states the discriminator the two placements actually run on: tier 1 runs under `-race`
  (`cmd/lenny-test/cmd_run.go:880`), so a case that arranges concurrent calls only as the way to reach
  process-local state stays at tier 1, and the cases whose subject is contention itself, where the
  assertion is that two co-tenant sessions' RPCs do not interfere, are pinned at tier 7a. Both tier-7a
  bullets are of that second kind, so no case moves tier.
