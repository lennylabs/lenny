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

**D6. A session's fenced generation is held for the duration of that session's binding on the pod, and
within a binding it is unset until that session's first accepted fence.** The
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

**D8. The barrier gate's comparison is equality.** The pod refuses a `CheckpointBarrier` whose
`coordination_generation` does not match the value it holds for the session the request names, and does not
widen to accept any value at or above that one. The authority is shipped §10.1.2 step 3, which fixes the
comparison for the whole class the barrier belongs to: "All subsequent gateway→pod RPCs include the local
generation stamp. The pod accepts only RPCs whose generation matches the fenced value"
(`spec/10_gateway-internals.md:41`). SPEC-1's §10.1.8 step 1 applies that predicate by reference rather than
restating a comparison, and the shipped gate already performs it (`pkg/adapter/coordination.go:236`), so
CODE-2 carries the operator unchanged. Widening admits a barrier carrying a generation no coordinator has
fenced on the pod, which the barrier's cache fallback can produce by reading the live session row between a
successor's compare-and-swap and its fence acknowledgement (`cmd/lenny-gateway/httpsurface.go:588-602`),
and step 2 makes that acknowledgement a hard precondition for every operational RPC
(`spec/10_gateway-internals.md:38`). Widening also recovers nothing on the healthy path, because the refusal
that occurs there comes from the mirror carrying a value below the one the pod holds, which a wider
comparison refuses exactly as equality does.

**D9. D7, SPEC-1's §10.1 counter-baseline paragraph, SPEC-3, CODE-4, and migration 0181 land in this
proposal.** They are one deliverable rather than four separable ones. Once the fenced generation is held
per session under D1, a bound session for which the pod holds no fenced generation becomes an ordinary
reachable state that the shipped specification states no rule for, so SPEC-1 states one and that rule is
D7. The adapter refuses a non-positive `coordination_generation` on the barrier
(`pkg/adapter/coordination.go:224-225`) before it reaches the generation gate (`:236-238`), so for a
never-handed-off session whose row still reads 0 the acceptance arm D7 states is unreachable, and the
counter baseline SPEC-1, SPEC-3, and CODE-4 carry is the condition under which D7 fires at all. That chain
is read from the shipped guard order rather than declared. Migration 0181 runs as a
`pre-install,pre-upgrade` hook at weight -5 (`charts/lenny/templates/migrate-job.yaml:38-39`) that
completes before the gateway Deployment rolls, and the platform is recorded as pre-deployment with no
deployments in the wild (`.claude/rules/code-best-practices.md:62`), so the schema change carried here
costs review attention rather than exposure of a running installation.

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
session neither fences nor unfences another. The pod holds that value for as long as the session is bound
to it, and the value does not survive an unbind. Within one binding the value is unset until that session's
first accepted fence, that first fence is recorded at whatever value it carries and is subject to neither
the stale rejection nor the gap predicate, and the gap predicate
`new_generation > last_fenced_generation + 1` applies from a session's second accepted fence onward. The gap
path's reset is per session: it cancels and discards the in-flight RPCs received for that session after that
session's last fenced generation, resets the transient tool-call and lifecycle state that session
accumulated since its last fenced coordinator, and logs `coordinator_generation_gap` recording that
session's two generations.

That text lands in sentences §10.1.2 already carries, and it adds no bullet. The sentences are these. In
the fence-announcement step, "The pod records the new generation and from this point rejects any RPC
carrying an older generation" gains "for that session". In the "Gap detection on the pod" bullet, the
`last_fenced_generation` parenthetical becomes "the generation from the last successfully acknowledged
fence for that session on this pod", the initial condition is stated immediately after that parenthetical
and before the clause list, and clauses (a), (b), and (c) gain the session qualifier. The predicate keeps
its current form, and clause (d) and the closing sentence stating that a gap of exactly 1 is the normal
case are unchanged. In step 3, the acceptance sentence and the sentence that follows it become "The pod
accepts only RPCs whose generation matches the value it holds for the session the RPC names. A session
bound to the pod for which the pod holds no fenced generation has no recorded value to match, so the pod
does not reject that session's RPCs on generation grounds, and a `CheckpointBarrier` naming such a
session is accepted and records no value. Because fence confirmation is required before this step is
reached, the pod holds one generation for that session at a time and accepts only RPCs carrying it, so an
RPC carrying a generation a later fence superseded is refused from the moment that fence is
acknowledged." Step 3's opening sentence, which states that all subsequent gateway-to-pod RPCs include
the local generation stamp, is unchanged, because it states what the coordinator this step addresses
stamps on the RPCs it sends. The acceptance sentence that follows it is the one whose domain is the whole
set of gateway-to-pod RPCs, including `CheckpointBarrier`, whose generation §10.1.8 step 1 states is the
session's current `coordination_generation` read from state the replicas share, as D7 records.

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
fence path's admission of an uninitialized session (`:99`) answers a different question, which is
D6's exemption for a session's own first fence rather than the gate on the operational RPCs that
follow it. The clause also agrees with the two neighbouring statements of the same rule: §10.1's
summary bullet has the pod reject when the generation is stale (`spec/10_gateway-internals.md:30`,
whose rejection sentence stands and which SPEC-1 opens below only to add the baseline sentence), and
the §28.5.1 Messages wording SPEC-2 stages has the pod reject a generation older than the one it
holds for that session. The comparison stays equality, under D8, and this edit changes only the
unit of the value compared and the domain over which the comparison applies.

§10.1.8 step 1 states the barrier's own refusal as the only outcome, and it takes the qualifier D7
requires. Its closing sentence, which reads "Pods receiving a barrier for a session no longer coordinated
by this replica (a false-positive surviving the cache fallback) reject the barrier as a generation-stale
RPC under the normal fencing rules — this is safe and does not require special handling"
(`spec/10_gateway-internals.md:183`), becomes "Pods receiving a barrier for a session no longer
coordinated by this replica (a false-positive surviving the cache fallback) apply to it the same
generation rule as to any other gateway-to-pod RPC, stated in step 3 of the handoff protocol: for a
session bound to the pod, the pod rejects the barrier as a generation-stale RPC when it holds a
generation for that session that the barrier does not carry, and otherwise accepts it. The generation a
barrier carries is read from the session's coordination state when the barrier-target set is assembled,
rather than stamped by the draining replica, so the outcome follows from the generation the barrier
carries alone. Either outcome is safe and requires no special handling." The acceptance predicate itself
is stated once, in §10.1.2 step 3, and §10.1.8 applies it by reference. A restatement here in terms of
which replica has fenced would be false in the false positive where the barrier carries the generation
the acquiring replica's compare-and-swap wrote and the pod still holds the value the draining replica
fenced, so the fencing rules refuse the barrier before the successor's fence lands and accept it after.
§10.1.8 states no enumeration of the values a barrier can reach the pod carrying, because the assembly
reads state that can sit on either side of the value the pod holds and which values are reachable is a
property of the target producers rather than of this step. The rest of §10.1.8 is unchanged: steps 2, 3,
and 4, the BarrierAck-timeout partial-capture rules, and the closing sentence bounding the rolling-update
interruption window to one in-flight tool call per session all hold under D7 rather than needing a
qualifier, because a barrier the pod accepts establishes the step-2 quiescence and the ack deadline stays
the only failure arm §10.1.8 defines.

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
before it fences today (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`), and CODE-4 leaves that
floor in place, on the same §10.5 ground that keeps the session row's `CHECK (coordination_generation >= 0)`:
the migrate Job completes before the gateway Deployment rolls
(`charts/lenny/templates/migrate-job.yaml:10-16`), so the still-running old fleet inserts rows at 0 that the
create-path floors never see. The adapter's refusal of a non-positive generation on the fence path
(`pkg/adapter/coordination.go:93-94`) and on the barrier path (`:224-226`) is unchanged, and it stays as the
fail-closed backstop for a value the gateway should never send.

The sentences elsewhere that state which value a barrier carries are not edit sites, and that ruling does
not rest on the baseline. §10.1.8 step 1 reads that the `CheckpointBarrier` message carries the current
`coordination_generation` (`spec/10_gateway-internals.md:183`), and §29.7's trace step 4 reads that the
replica sends it "carrying the session's current `coordination_generation`"
(`spec/29_communication-scenarios.md:1186`). Neither is restated, because each is already false on the
handoff path in the shipped tree. On the healthy path the dispatcher copies onto the wire the generation
the `coordination_lease` mirror row carries (`pkg/gateway/coordination/barrier/wiring.go:104-114`, `:49`)
rather than the session row's, and the sweep writes that mirror from its pre-bump List snapshot
(`pkg/gateway/coordination/coordination/coordination.go:430`) in the same iteration in which
`RecordHandoff` bumps the row and the pod is fenced at the post-bump value (`:371`, `:399`, `:463-481`),
so after a cross-replica takeover the mirror carries the prior generation for a whole sweep interval. That
lag is a defect in the shipped tree that this proposal records rather than stages, and the baseline neither
creates nor repairs it.

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
generation the pod holds. Zero names no fence: §10.1.2 step 1 increments `coordination_generation`
before step 2 announces it (`spec/10_gateway-internals.md:37`), and the value every other fence path sends
is floored at 1, so no `CoordinatorFence` ever carries zero. That is held by the session row's baseline of
1, which CODE-4 lands as the column default and as the create path's floor; by the gateway fence path's
floor of a non-positive row value at 1 (`pkg/gateway/coordination/coordfence/coordfence.go:147-153`), which
CODE-4 keeps for a row an old binary wrote at 0 during the rolling window, because `fenceResumedPod`
(`pkg/gateway/sessionserver/start.go:4233-4245`) fences on the value it reads without incrementing it; and
by the adapter's refusal of a fence carrying a non-positive generation (`pkg/adapter/coordination.go:93-94`)
as the fail-closed backstop. The bullet also states that the hold itself and the
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
- §28.8's `CH-BARRIER` row, its "Holder of the exclusivity constraint changes" cell. Its closing clause "so
  a barrier from a superseded replica is rejected on the stamp" (`:1808`) is replaced by the predicate
  §10.1.8 step 1 states, at the level of detail the cell carries: for a session bound to the pod, the pod
  rejects the barrier when it holds a generation for the session the barrier names that the barrier does
  not carry, and otherwise accepts it. The rest of the constraint sentence, which states the constraint,
  names `REG-COORDLEASE` and the generation stamp as the guard, and records that the barrier carries that
  generation in its own message, is unchanged, as is the cell's second sentence recording that the
  specification states no separate pod-level barrier lock. Only the trailing clause of the constraint
  sentence is replaced, and the row stays one line. The clause is an edit site because the pod cannot
  reject a barrier on the identity of its sender: the message carries no replica identity and its
  generation is read from state the replicas share, so a superseded replica's barrier can carry the very
  generation the pod holds, in which case the staged predicate accepts it. The mechanism the clause would
  need to stay true, a replica identifier on the wire and a pod-side view of `REG-COORDLEASE`, is a new
  protocol surface on the one channel where a wrong rejection costs the session's quiescence and the
  acked-barrier record, and forces a second capture.

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

No §28.4 claim-register row moves, and none carries a wrong status during the interval in which the spec
edits above have landed and the code has not. §28.4 defines each status against the mechanism a row names:
`WIRED` means the mechanism is reachable from production code, `UNWIRED` that it is implemented and has no
production caller, and `ABSENT` that it is specified and not implemented
(`spec/28_communication-channels.md:163-165`). The `CoordinatorFence` row (`tests/claim-map.json:460-465`)
and the `CheckpointBarrier` row (`:448-453`) are both `WIRED`, and the surfaces they name stay reachable
across every step of this proposal, because the code deliverables edit those surfaces rather than remove
them: the call sites at `pkg/gateway/coordination/coordfence/coordfence.go:159` and
`pkg/gateway/coordination/barrier/wiring.go:49`, and the handlers at `pkg/adapter/coordination.go:84` and
`:211`. Re-scoping what the specification says about a mechanism leaves the register's statement about that
mechanism true, so no row is restatused and no deferral identifier is owed. The rows in
`tests/claim-map.json` name mechanisms and wire fields rather than the scope a sentence states, and their
anchors resolve to headings this change does not move, so that file is not opened by this proposal.

**`spec/29_communication-scenarios.md`.** The §29.10 co-tenancy classification changes, §29.7's framing
paragraph applies the predicate §10.1.8 step 1 applies, and the Preconditions paragraph and steps 2, 7, and
9 of the §29.8 crash-takeover trace change.

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
  carry, and, for a session bound to the pod, is accepted otherwise. The guard on the pod holding a value
  is carried word for word from §10.1.8 step 1, because a session for which the pod holds no fenced
  generation has no value the carried generation could match, and that session is inside §29.7's own
  population. Step 5 of the trace, on which
  the adapter quiesces, is unchanged, and the paragraph's set of named outcomes stays closed, because both
  arms are now named.
- §29.8's Preconditions paragraph fixes the pod's fenced value at the session row's counter: "the session's
  `coordination_generation` is the generation the pod last fenced"
  (`spec/29_communication-scenarios.md:1259-1261`). SPEC-1's unset arm and SPEC-3's baseline leave that
  false for the trace's ordinary subject, because a session that started normally, never resumed, and was
  never taken over carries the counter's baseline on its row while the pod holds no fenced generation for
  it, and D7 records the resume path and the sweeper's crash-takeover re-adopt as the only fence drivers.
  The clause is deleted rather than restated. The paragraph's next sentence already states that every
  gateway-to-pod RPC carries the coordinator's generation stamp and that the pod rejects a stale one,
  deferring to §10.1 for the value the stamp is measured against, which is the non-site form the criterion
  above states, and §29.7's Preconditions names the lease and the `REG-COORDMIRROR` row and no generation.
  The lease clause and its two citations, the `replica B` sentence, and the stale-rejection sentence are
  unchanged. The paragraph is unit-neutral, which is the ground an earlier sweep left it on, and the
  falsification is by the unset arm and the baseline rather than by the per-session unit.
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
rule and the acceptance rule, and SCHEMA-1 carries onto each carrier of those two rules the wording the
section that owns the rule states, so the wire text and the applied specification state one rule apiece. One
wire sentence states a rule no section of `spec/` states, the fence's own acceptance predicate on
`CoordinatorFenceResponse`, and it takes the session unit alone.

The record-and-reject rule is carried by the `CoordinatorFence` RPC doc comment
(`schemas/lenny-adapter.proto:153-162`), which states the rule, the gap reset, and the first-fence
exemption, each with the pod as the unit, and by the message-level `CoordinatorFenceRequest` comment
(`:1442-1446`), which states that the pod records the new generation and from that point rejects every RPC
carrying a strictly older generation. Both take the §28.5.1 Messages wording above: the
pod records the generation against the session the fence names, and from that point rejects every RPC
carrying a generation older than the one it holds for that session. The `CoordinatorFence` RPC comment also
takes D6's unit for the exemption, so it reads as the first fence for that session within its current
binding on this pod, and its gap sentence takes the session qualifier the Degradation bullet takes. The
`CoordinatorFenceRequest.coordination_generation` field comment (`:1449-1451`) already states per-session
monotonicity, which D1 and D3 make true, and it keeps its wording.

The `CoordinatorFenceResponse` comment (`:1455-1462`) is a carrier of the session unit rather than of the
record-and-reject rule, and each of its two sentences takes the session qualifier and nothing else. Its
first sentence defines `accepted`, whose false condition, that the supplied generation is not greater than
the last fenced generation, states the fence's own acceptance predicate. That predicate is the one
`pkg/adapter/coordination.go:99` implements, and no section of `spec/` states it. It becomes "false when the
supplied generation is not greater than the generation the pod holds for the session the fence names", so
the sentence changes the unit of the compared value and leaves the comparison as the adapter performs it.
Its second sentence defines `gap_detected`, and it takes the session qualifier the `CH-FENCE` Degradation
bullet takes: the skip is measured against the generation the pod holds for that session, and the state the
sentence names is the transient tool-call state that session accumulated. The §28.5.1 Messages wording does
not land on this comment. Applied to it, that wording would replace the two field definitions with a
statement about the pod's treatment of later RPCs, which this message does not carry, and it would restate
the acceptance predicate as "older than", which accepts a fence carrying the generation the pod already
holds and contradicts `pkg/adapter/coordination.go:99`. No deliverable here changes what the handler does
with a fence carrying the generation the pod already holds, so the wire text keeps the comparison the
shipped handler performs.

The acceptance rule is carried by the `CheckpointBarrier` RPC doc comment (`:165-179`), by the message-level
`CheckpointBarrierRequest` comment (`:1469-1474`), and by that message's `coordination_generation` field
comment (`:1477-1479`). Each states the gate against "the last fenced generation" with the pod as the
unit. Each takes the §10.1.2 step 3 wording SPEC-1 stages, so the barrier's generation is validated against
the value the pod holds for the session the request names, and the message-level and the field-level text do
not disagree about the comparison.

The remaining `coordination_generation` field comments in the file are carriers too. Each states the
gateway's view of the active coordination generation for the session and then closes with an
unconditional consequence. Most read that a pod validates the generation on every gateway-to-pod RPC and
rejects a stale coordinator's request, "so a replica that has lost coordination cannot drive the pod
(§10.1)", and `ShutdownRequest`'s variant closes "cannot tear the session down (§10.1)". SPEC-1's staged
§10.1.2 step 3 states that a session for which the pod holds no fenced generation has no recorded value
to match, and D6 together with D7's enumeration of the only two fence drivers make that the ordinary
state of a session that has neither resumed nor been taken over, so for that session class the pod
rejects nothing on generation grounds and the consequence clause is false against the section it cites.

Each of those comments takes one replacement for the span running from "A pod validates" to the end of the
consequence clause: "A pod validates the generation on every gateway-to-pod RPC against the value it holds
for the session the RPC names, and rejects a request whose generation does not match it (§10.1)." The
opening sentence stating the field's meaning is unchanged, and the unset case is not restated on these
comments, because §10.1.2 step 3 owns it and the fence and barrier carriers above already state the
record-and-reject rule and the barrier gate once each. `AttachRequest`'s and `CheckpointRequest`'s trailing
sentences, which state that the generation is carried on every frame of the stream rather than on the
opening frame alone, are unrelated to the gate and are unchanged.

The comments are the `coordination_generation` field comments on `SendMessageRequest` (`:969-973`),
`AttachRequest` (`:995-1001`), `RotateCredentialsRequest` (`:1046-1050`), `ExtendCredentialLeaseRequest`
(`:1070-1074`), `RevokeCredentialsRequest` (`:1091-1095`), `InterruptRequest` (`:1114-1118`),
`CheckpointRequest` (`:1172-1178`), `SignalDeadlineRequest` (`:1305-1309`), `ResumeRequest` (`:1393-1397`),
`ExportPathsRequest` (`:1531-1535`), `ReportUsageRequest` (`:1576-1580`), and `ShutdownRequest`
(`:1618-1622`). They are named by message because a list built from the shared "cannot drive the pod" phrase
alone misses `ShutdownRequest`, whose clause closes "cannot tear the session down". SCHEMA-1's own target
list is in the non-spec changes and names the whole carrier set in the order SPEC-2 states it.

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

- **Renaming `CoordinatorFenceRequest`'s session field.** That is proposal 0075's subject. This proposal
  stages no change to that field's name or type, and no deliverable here depends on 0075 landing.
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

1. **Whether a fence for an unheld session is a rejection or a retryable race.**
2. **Whether `coord.mu` becomes per-entry.**

## 10. Dependencies

Applies after proposal 0073, which supplies `checkSessionBound` and the slot registry this proposal keys
the generation on. It is independent of proposal 0075.

