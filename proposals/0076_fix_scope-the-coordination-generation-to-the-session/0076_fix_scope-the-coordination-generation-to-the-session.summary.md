# Summary: Scope the coordination generation to the session

## Summary

**Problem statement.** On a concurrent pod: a legitimate coordinator handoff rejected as
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

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` gate and gap
detection read the same per-session value, and the barrier gate that holds quiescence open and carries the
gateway-minted checkpoint id back into the ack moves onto the same entry, so two co-tenant sessions drained
together each hold their own gate. The pod holds a session's fenced generation for the duration of that
session's binding on it, and within a binding the value is unset until that session's first accepted fence,
so the exemption that makes a first fence neither stale nor a gap moves from the pod's lifetime to the
session's binding on it, which is D6, and SCHEMA-1 carries that unit onto the wire comment that states the
exemption. A session that unbinds and later binds to the same pod comes back with no recorded generation,
where the shipped pod-wide field retains it, and its first fence in the new binding is exempt from the
stale rejection and the gap predicate. The coordinator-loss hold stays pod-scoped and reports
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

**Decisions.** These are closed. An implementor applies them and does not re-derive them.

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
- **D6.** A session's fenced generation is held for the duration of that session's binding on the pod, and
  within a binding it is unset until that session's first accepted fence. The
  stale rejection and the gap predicate are both defined against a recorded value, so both apply from that
  session's second accepted fence onward, and the exemption's unit is the session's binding on the pod
  rather than the pod's lifetime.
- **D7.** A `CheckpointBarrier` naming a bound session for which the pod holds no fenced generation is
  accepted and records no value. A barrier carrying a value that does not match one the pod does hold for
  the named session is still refused with `FailedPrecondition` and the `coordinator_handoff_stale` detail.
- **D8.** The barrier gate compares for a match against the value the pod holds for the named session and
  does not widen to accept a higher one. Shipped §10.1.2 step 3 fixes that comparison for every
  gateway-to-pod RPC (`spec/10_gateway-internals.md:41`), the staged §10.1.8 applies it by reference, and
  the shipped gate already performs it (`pkg/adapter/coordination.go:236`).
- **D9.** D7, SPEC-1's §10.1 counter-baseline paragraph, SPEC-3, CODE-4's two session-store `Create`
  floors, and migration 0181 land in this proposal rather than in a successor, because they are one
  deliverable. Once the fenced generation is held per session, a bound session the pod holds no value for
  is an ordinary reachable state that the shipped specification states no rule for, so SPEC-1 states one,
  and that rule is D7. The adapter refuses a non-positive `coordination_generation` on the barrier
  (`pkg/adapter/coordination.go:224-225`) before it reaches the generation gate (`:236-238`), so D7's
  acceptance arm is unreachable for a never-handed-off session whose row still reads 0, and the counter
  baseline is the condition under which D7 fires at all. That chain is read from the shipped guard order
  rather than asserted. Migration 0181 runs as a `pre-install,pre-upgrade` hook at weight -5
  (`charts/lenny/templates/migrate-job.yaml:38-39`) that completes before the gateway Deployment rolls,
  and the platform is recorded as pre-deployment with no deployments in the wild
  (`.claude/rules/code-best-practices.md:62`), so carrying it here costs a reviewer's attention to a
  schema change rather than exposure of a running installation.
- CODE-1's lock order is the registry lock, then the entry lock, then the hold lock. The one read that
  would become an inversion if it moved inside the hold critical section is the read CODE-3 removes.
- Relocating the `quiesced` flag onto the entry carries no specification claim about the quiescence unit,
  so a later implementor cannot read the field's placement as settling it.

One question adjacent to these is open rather than fixed, and `## Open decisions for human to make`
carries it: how the `spec/04` §4.1 message-scope row is classified (OD3).

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

The hold's scope is settled rather than open. D5 keeps it pod-scoped, because the only arming signal the
adapter has is the close of the pod's single CH-ADAPTEREVENTS stream, which names no session. The cost is
recorded as a non-goal: a pod whose stream holder crashes freezes co-tenant sessions whose own coordinators
are alive.

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
  more than one replica holds one, which `spec/28`'s CH-ADAPTEREVENTS exclusivity bullet records the
  specification as not stating.
- Tightening `migrations/0050_session_record_fields.up.sql`'s `CHECK (coordination_generation >= 0)` to
  `>= 1`. Migration 0181 carries the two `DEFAULT 1` clauses and the backfill, and the two session-store
  `Create` floors are the whole enforcement of the baseline. §10.5's expand-contract rule places the
  tightening in a later phase, and OD9 records the question.
- Deciding whether two replicas may coordinate co-tenant sessions on one pod. The per-session
  `REG-COORDLEASE` already permits it, and this change removes a cross-session generation interlock that
  delayed and mis-metered co-tenant handoffs whatever the number of replicas involved.
- Repairing the defects listed under `## Defects in the shipped tree that this proposal does not stage`.
  None of them blocks sign-off.

## Open decisions for human to make

Every decision below is open and needs a reviewer's answer before this proposal moves to `Approved`.
OD2 and OD3 were derived independently three times and then validated independently five more times
against the working tree, with the validators instructed to falsify each recommendation rather than
confirm it, and where one changed under that pass the entry says so. An entry that is withdrawn or
replaced leaves this section, its record is kept in the review log, and its identifier is not reused.

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
The successor is named: proposal 0080 §1.16 carries the case, the remedy this entry derives, and the two
things that remedy must take on, and it sequences itself after this proposal because CODE-1 rewrites the
predicate the remedy changes. Nothing staged in this proposal depends on the equal case being accepted, and SPEC-2's staged §28.5.1,
§28.6, §28.8, and §29.8 arms enumerate the older and the higher case and are silent on the equal one, so
the applied specification is compatible with either answer. The recommendation would flip on a staged
deliverable whose correctness requires the equal case, and there is none.

The weight of a "yes" sits in the contract rather than in the code. CODE-1 moves `lastFenced` and
`initialized` off `Server.coord` onto the slot entry, so the predicate at `:99` is rewritten by this
proposal whichever way the decision goes, and the remedy is one comparison operator on a line CODE-1
already edits. What a "yes"
adds is the behavioural change and its carriers: the `accepted` sentence in the
`CoordinatorFenceResponse` comment (`schemas/lenny-adapter.proto:1455-1458`), which is the one carrier
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

The cost of the recommendation is that until 0080 §1.16 lands, every lost fence acknowledgement keeps
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
are deciding it.

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

The decisions below reached this section from the review log's open list rather than from the derivation and
validation pass the entries above went through. Each states the ground the log entry gives, and an entry the
loop left without a recommendation says so.

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
the check (`non-spec-changes.md:154-155`), which the withdrawn OD8 also recorded. That sentence
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

**OD12. A superseded replica holding a session quiesced while it drives a checkpoint.** The pod's barrier
gate carries no term for whether the sender still coordinates the session: it compares the barrier's
generation with the value the pod holds and nothing else (`pkg/adapter/coordination.go:236`). The barrier's
generation is read from the shared coordination state when the barrier-target set is assembled rather than
stamped by the draining replica, so a replica that has just lost coordination can still have its barrier
accepted, and the pod then holds that session quiesced while the replica drives a checkpoint stream against
it. **The question for the reviewer: is it acceptable that a replica which no longer coordinates a session
can hold that session quiesced for the length of the barrier ack window while it drives a checkpoint, and is
the specification sentence asserting that both the acceptance and the rejection outcome are safe the sentence
to sign off on?**

**Recommendation: accept it, and take the staged sentence. Confidence: moderate on the first half, high on
the facts below.** The confidence is moderate because the acceptance arm is a normative claim no shipped
sentence makes, so a reviewer may reasonably want it argued in the specification rather than asserted.

Three facts the answer turns on were each verified against the working tree. First, the acceptance is
shipped behaviour that this proposal does not create. The gate at `pkg/adapter/coordination.go:236` tests
`!initialized || gen != fenced`, which is the same test before and after D7; D7's own addition, the arm for
a bound session the pod holds no value for, is unreachable for a superseded replica, because being
superseded means a successor's fence was recorded and the pod therefore holds a value. Second, the stream
is not consequent on acceptance. `pkg/gateway/coordination/barrier/barrier.go:220-227` starts the
gateway-driven `Checkpoint` stream in a goroutine before `c.dispatch.Send` and joins it afterwards, for
every target unconditionally, and `:228` records the stream's error without un-acking, so the superseded
replica opens the stream whether the barrier is accepted or refused. The second half of the question as it
was first written, whether an accepted false-positive barrier is followed by a stream at all, is answered by
the tree and answered the other way round: the stream is not gated on the barrier. Third, the cost is a
frozen session rather than a corrupted one. The adapter imposes no bound of its own on the hold
(`pkg/adapter/coordination.go:264-268` waits on the stream or on the RPC context and starts no timer), so
the only reclaimer is the gateway's RPC deadline, set from `checkpointBarrierAckTimeoutSeconds`
(`pkg/gateway/podlifecycle/prestop/prestop.go:503`, default 90s), and that deadline is one wall-clock window
across all of a replica's pods rather than per pod (`spec/10_gateway-internals.md:138`). The manifest write
the stream performs is guarded by supersede-on-write and `partial_manifest_active_uniq`, so a stale
coordinator's write cannot displace a successor's.

What the proposal does put at stake is the specification's assertion. Shipped
`spec/10_gateway-internals.md:183` gives rejection as the only outcome and asserts safety of that outcome
alone. The staged §10.1.8 step 1 rewrite has the pod, "for a session bound to the pod", reject "when it holds
a generation for that session that the barrier does not carry, and otherwise accepts it", and closes "Either outcome is safe and requires
no special handling", which is a normative claim over the acceptance arm that no shipped sentence makes.
Answering yes ratifies it.

The alternatives, and why each lost. **Withdrawing the entry as mis-stated** was the first reading's
recommendation, on the ground that nothing staged creates the acceptance. It lost because the staged
sentence is a new assertion about a case the shipped text does not cover, and a reviewer signs off on the
assertion even when the behaviour predates it. **Repairing the behaviour**, by carrying the sending
replica's coordination status on the barrier or having the pod re-read the lease before quiescing, lost
because it reaches into the gateway's barrier dispatch and the adapter's quiesce path, both of which the
Non-goals exclude, and because it would leave the shipped acceptance undescribed for however long the
successor takes. **Keeping the shipped rejection-only sentence** and staging no rewrite lost because the
sentence is then false about the code under D7's per-session gate, which is the defect this proposal exists
to remove.

Deciding otherwise costs a successor proposal over the barrier dispatch and, until it lands, a §10.1.8
step 1 that describes an outcome the pod does not take. Nothing in the deliverables changes either way:
the answer selects whether the staged sentence stands, and the code lands as staged in both cases.

**OD14. Whether the migrate Job gets a run-time budget, and whether 0181's backfill stays unbatched.**
Migration 0181 lands in this proposal under D9, so this question stays here with it. D9 settles where the
migration lands and does not answer what follows.

The question, in one sitting: migration 0181 backfills the `sessions` table with a single
`UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0` over the whole table,
inside a Helm `pre-install,pre-upgrade` hook that completes before the gateway Deployment rolls, and no
migration in this platform has a stated run-time budget. Accept the unbatched form and the absent budget, or
commission a successor that writes a budget for the migrate Job?

Correctness is not at issue, so the reviewer is ranking upgrade duration against scope rather than judging
safety. A backfill that wins the row lock cannot corrupt a concurrent writer: `RecordHandoff` goes through
`Update`'s `SELECT ... FOR UPDATE` and both stores clamp `CoordinationGeneration` to the previous value
(`pgstore.go:460`, `:475-477`; `memstore.go:129`, `:144-146`).

The ground on duration, and when it applies. 0181 is a `golang-migrate` file, so it applies once, inside the
`pre-install,pre-upgrade` hook (`charts/lenny/templates/migrate-job.yaml:38`). That places its single
execution either at a fresh install, where `sessions` is empty and the predicate matches no row, or at the
first upgrade of an installation that exists today, and `.claude/rules/code-best-practices.md:62` records the
platform as pre-deployment with no deployments in the wild. The figures that follow therefore price a table
state 0181 will not meet unless that posture changes before this release ships. In that steady state no
retention sweeper deletes session rows (`retention_expires_at` is written and read at
`pkg/gateway/session/sessionstore/pgstore/pgstore.go:106`, `:174`, and `:416`, and no path deletes on it,
while the deletions that exist are tenant and user erasure), the table reaches order 10^6 rows within a year
at Tier 3's 10,000 concurrent sessions (`spec/02_goals-and-non-goals.md:13`), and the predicate matches
essentially every row. An unbatched whole-table backfill of `sessions` has a direct precedent: the migration
immediately preceding this one runs `UPDATE sessions SET workspace_root = '/workspace/slots/' || id::text ||
'/current' WHERE workspace_root = '/workspace/current'` over the whole table
(`migrations/0180_drop_checkpoint_slot_id.up.sql:147-149`), unbatched, in the same migrate Job, alongside two
`DROP COLUMN` statements and three index rebuilds in the same file, and its header states that it targets
every session still holding the retired path (`:17-21`). The other landed `UPDATE ... SET` backfills
(migrations 0053, 0054, 0058, and 0105) target small configuration tables. Corrected on 2026-09-05: an
earlier form of this paragraph listed migrations 0064 and 0178 among the backfills and stated that no
precedent covered a session-scale one. Neither of those two files contains a backfill, both carrying only a
comment describing the soft-delete write pattern, and 0180 is the precedent. One fact bears on the batching half: both migration runners build the driver as `migratepg.WithInstance(db, &migratepg.Config{})`
(`cmd/lenny-migrate/main.go:226`, `pkg/schemamigrate/schemamigrate.go:382`), which leaves multi-statement
splitting off, so a migration file executes as one statement batch and batching inside 0181 would commit
nothing between batches and release no locks. Batching that shortens the lock hold therefore means either
splitting 0181 across migration files or changing the runner, neither of which this proposal stages.

The ground on the budget. Nothing in the repository states one. §10.5 supplies expand-contract phasing,
`golang-migrate` tooling, pre-deploy-hook execution, an advisory lock, and re-runnability after a partial
completion, and its one row-count number, the operator confirmation of the gate query plan for tables
exceeding 1 million rows (`spec/10_gateway-internals.md:434`), is scoped to a Phase 3 gate's `COUNT(*)`
preflight rather than to a Phase 1 backfill. The chart does not bound the Job either:
`charts/lenny/templates/migrate-job.yaml` renders `backoffLimit` (`:42`) and `ttlSecondsAfterFinished: 600`
(`:45`) and no `activeDeadlineSeconds`, and the `migrate` values block exposes only `backoffLimit: 5`
(`charts/lenny/values.yaml:3791`). The omission is per Job rather than uniform:
`charts/lenny/templates/preflight-job.yaml:259` and `charts/lenny/templates/crd-validate-job.yaml:81` render
`activeDeadlineSeconds` from `preflight.timeoutSeconds` and `charts/lenny/templates/backup-job.yaml:134`
renders 7200, while the migrate, bootstrap, MinIO lifecycle, and deployment-config-sync hook Jobs render
none. `spec/` states those two deadlines and no third (`spec/17_deployment-topology.md:571`,
`spec/25_agent-operability.md:3995`), so writing a budget for the migrate Job means writing the number as
well as the knob. The only bound in force today is whatever the deployer passes to Helm, and the documented
upgrade command passes `--wait --timeout 10m` (`docs/operator-guide/upgrades.md:22`), which is an example
command rather than a stated contract. `docs/runbooks/schema-migration-failure.md` states no expected or
maximum run time.

**No recommendation is offered.** The trade is between an operational guarantee and the cost of owning one,
and neither `spec/` nor the chart takes a position the repository can read off. The two halves differ in what
the tree bears on. The pre-deployment posture above bears on the backfill half, since the row count 0181 will
actually meet is the one an installation carries at the upgrade that applies it. Nothing bears on the budget
half, which is a forward-looking operational contract on the migrate Job that outlives 0181 and whose number
does not exist anywhere in `spec/` or the chart. **Confidence in the framing: high.** The facts the answer turns on were each verified against the working tree: no budget is stated
anywhere, the chart bounds the preflight, crd-validate, and backup Jobs and not the migrate Job, §10.5's only
row-count number belongs to the Phase 3 gate, the runner applies a migration file as one statement batch, and
migration 0180 already backfills the whole `sessions` table unbatched in the same Job.

What each answer costs. **Accepting both** keeps the scope and leaves the migrate Job's run time bounded by
nothing inside the release, so a backfill that blocks on a row lock runs until the deployer's own Helm
timeout fires with the Job still running. It also accepts for a second time what 0180 already established,
rather than settling it. **Writing the budget here** widens this proposal from
session-scoping the coordination generation into migration policy, because the remedy lands in §10.5 or in
the migrate Job template and this proposal stages an edit to neither. Its files-touched list carries
`spec/10`'s §10.1 subsections, `spec/28`, `spec/29`, `spec/04`, the proto, migration 0181, the two session
stores, and tests, and no chart file (`non-spec-changes.md`, `## 9. Files touched on application`).
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
wall-clock against the pod's real grace period rather than a lost checkpoint. The specification designs that
outcome rather than leaving it undefined: a session that misses the deadline enters the BarrierAck-timeout
partial-capture path, which finalises the session's partial-manifest intent row and preserves every chunk
already committed, and falls back to the last periodic checkpoint only where no intent-row state exists
(`spec/10_gateway-internals.md:187`, `:198`).

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

What the answer changes is the size of the exposed population rather than the mechanism. The adapter's
coordination state is pod-wide today (`pkg/adapter/coordination.go:25-32`) and the barrier gate compares
the request's generation against that pod-wide value (`pkg/adapter/coordination.go:236`), so two co-tenant
sessions whose generations coincide, which is the ordinary outcome when one replica hands off all of its
sessions, already enter one 90-second window and already serialize on the pod's checkpoint op lock
(`pkg/adapter/checkpoint.go:111`). D7 together with CODE-1's per-slot gate widens the affected set from
co-tenants whose generations coincide to every co-tenant pair, so the exposure grows under this proposal
while the unmultiplied window predates it.

**OD16. Whether the three `IMPLEMENTOR TO FILL THE BLANKS` headers stay in the staged-change files.**
`spec-changes.md:107` and `:134` and `non-spec-changes.md:6` each open a staged section with
`**IMPLEMENTOR TO FILL THE BLANKS.** These are indicative targets; the text is written during convergence,
against the post-0073 state of each file.` The question is whether a maintainer applying this proposal
reads the staged sections as verbatim text to apply, in which case the three headers come out, or as
targets to write against at application time, in which case they stay.

The ground the review loop derived. The cleanup phase dispositioned all three as coming out, on the ground
that a converged proposal's staged sections are verbatim staging and a header calling them indicative
targets tells a maintainer applying tomorrow not to apply them as written. It also recorded that the
earlier ground for dropping them, that every item beneath each header is derived or settled, was false
when the disposition was taken, because the `## 4. Detailed design` header then carried an item this
section declared open. That item has since been answered and removed, so the ground now holds for that
header. One archived refutation stands against four separate filings of
the same finding, and no lens has broken the tie across four runs, which is why the item reaches the
reviewer rather than a fixer.

One fact bears on the answer and postdates the disposition. The spec loop's last run ended with findings
still open, so the staged spec text has not converged and the headers' own condition, that the text is
written during convergence, is not yet met for every section beneath them. Removing the headers while the
staging is unsettled states a completeness the files do not have; keeping them past convergence leaves the
instruction the disposition calls misleading.

**No recommendation is offered.** The loop derived a disposition and a standing refutation against it and
could not close either, and the convergence fact above bears on both. What either answer costs: dropping
the headers touches three sites in two staged-change files and states that every item beneath is text to
apply as written; keeping them leaves a maintainer to decide per section which staged text is verbatim.


## Defects in the shipped tree that this proposal does not stage

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
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:283-295`), so staging a
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
  survive unchanged. OD2 owns the third: it puts the equal case to the reviewer, recommends that proposal
  0080 §1.16 stage the pod-side change, and records this entry as where the standing cost is written down.

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
  leaving it to a successor (`0076_fix_scope-the-coordination-generation-to-the-session.summary.md:262-268`).
  On that branch the repair is the addition of the fence to the suite rather than the deletion of a false
  clause, and it has to settle what value a map recording each member's retired field number holds for a
  message that never declared one. Repairing the comment now would pre-commit to whichever of the two the
  reviewer has not yet chosen.
- **The gateway has no `CH-ADAPTEREVENTS` client.** `tests/claim-map.json` files the client side of the
  stream as `UNWIRED` under deferral R12 (`tests/claim-map.json:512-518`), and the tree agrees. Outside
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
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:638-644`), unreachable
  until then.

  This proposal records the gap and stages no client. R12 owns the client, and building it is a whole
  channel implementation: the dial, the reconnect policy, and the arbitration of which replica's
  connection carries the pod's events, which §28.8's CH-ADAPTEREVENTS row records the specification as
  not stating (`spec/28_communication-channels.md:1810`). Nothing staged here becomes wrong or
  unimplementable while the client is absent. CODE-3 is a change to `holdstate.go` and `slotsession.go`
  that compiles and is pinned by the tier-1 hold case §8 amends
  (`0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md:255-262`), and SPEC-1's
  §10.1.4 text and SPEC-2's §29.10 hold bullet state what the pod must do when the hold fires, which an
  unwired trigger does not falsify. Recording that separation is what the `UNWIRED` row exists for. The
  absence also bounds the risk of the one production-path behaviour change CODE-3 makes, which no
  deployed system reaches today.
- **The adapter does not perform the §10.1.2 cancel-and-reset on a generation gap.** The gap branch logs
  `coordinator_generation_gap`, sets `GapDetected`, and then records the new value on the same path a
  non-gap fence takes (`pkg/adapter/coordination.go:108-121`). Nothing cancels the in-flight RPCs received
  under the missing generations and nothing resets the transient tool-call state, and the code concedes the
  absence twice, in the RPC doc comment (`:80-81`) and in the gap branch itself (`:112-113`). The absence is
  already carried under §28.4's claim-register discipline as `"In-flight RPC cancellation on a generation
  gap"`, status `ABSENT`, deferral `R16` (`tests/claim-map.json:173-178`); that row's `surface` names
  `coordination.go:81-82` while the concession it points at sits at `:80-81`.

  This proposal re-scopes the requirement and stages no implementation. SPEC-1 states the reset per session
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:145-148`) and says in the same
  file that the staged text re-scopes the requirement without asserting that the adapter meets it (`:126-127`).
  SCHEMA-1 does the same on the wire lane: `schemas/lenny-adapter.proto:157-162` states that the adapter
  cancels and discards every in-flight RPC and resets transient tool-call state, and `:1458-1462` defines
  `gap_detected` as reporting that the adapter "reset transient tool-call state per §10.1", and both
  sentences take the session qualifier rather than being deleted
  (`0076_fix_scope-the-coordination-generation-to-the-session.spec-changes.md:523-524`, `:535-537`). Both
  are false about the tree before and after this change, so what moves is the unit of an obligation the
  adapter meets in neither unit.

  Implementing the reset is a whole adapter mechanism, covering cancellation of in-flight RPCs and a reset
  of transient tool-call and lifecycle state, outside a code lane that relocates one recorded value onto the
  slot registry entry. Nothing staged here depends on the reset: CODE-1 touches the gap branch only to read
  `lastFenced` and `initialized` from the entry, CODE-2 is the barrier gate, and the staged gap cases assert
  `gap_detected` false on a co-tenant's first fence
  (`0076_fix_scope-the-coordination-generation-to-the-session.non-spec-changes.md:212-217`, `:260`). Two
  things follow for the implementor. CODE-1 rewrites the doc comment the claim-register row's `surface`
  points at, so that pointer is re-resolved when CODE-1 lands, and both concession sentences survive the
  rewrite rather than being dropped with the pod-wide wording around them.

## Impacts on other proposals

This is the only place this proposal states anything about another proposal's continued validity.
`## Non-goals` states what this proposal will not do and `## 10. Dependencies` in the spec changes states
what it applies after; neither restates a row below.

| Proposal | Status | What this change does to it | What it must do about it |
|:--|:--|:--|:--|
| 0060 | Implemented | Nothing. The lease protocol, forced acquisition, and the lease co-location 0060 built are untouched, and TEST-1 runs on the two-replica harness it landed. | Nothing. |
| 0075 | Draft, rewritten 2026-09-06 against this proposal (`proposals/0075_fix_derive-message-scope-from-the-address-type.md:4-5`) | Removed the ground under its sole counterexample, and 0075 has since been rewritten to take that. 0075 derives message scope from the address type and excepts `CoordinatorFenceRequest`. Its earlier revision grounded the exception on the handler verifying the session and then mutating `s.coord`, one pod-wide `coordinationState` for the whole adapter process, so "the identifier selects nothing". CODE-1 deletes `Server.coord` (`pkg/adapter/server.go:302`) and records the generation on the slot entry the identifier resolves, so the identifier addresses that entry while remaining a staleness guard against pod reuse. The rewrite restates the ground as the specification's rather than the tree's, and makes its SCHEMA-1, CODE-1, and DOCS-1 conditional on OD3 rather than restating or dropping them: under a "yes" the exception disappears and those three lose their subject, and under a "no" they carry an exception whose restated ground is the answering reviewer's to supply. SPEC-1 (state the derivation rule, retire 0073's §4.1 table) and TEST-1 (replace the tier-0 reconciliation gate) keep their subject either way, and a "yes" to OD3 strengthens both by removing the counterexample the rule has to except. Nothing here renames or retypes the field, which `## Non-goals` excludes (`summary.md:150`), so 0075's rename remains available under a "no". | Nothing further before OD3 is answered. The rewrite already corrected `## 10. Dependencies`, which called this proposal independent and the collision a rebase for whichever landed second, and the Non-goals bullet that called the pod-wide counter a defect this proposal owns; it also refreshed the proto and code anchors that had drifted independently of this change. What remains is 0075's to do once OD3 is answered: drop SCHEMA-1, CODE-1, and DOCS-1 under a "yes", or supply the restated ground for the exception under a "no". |
| 0080 | Draft, dated 2026-08-31 (`proposals/0080_fix_discharge-the-residues-proposal-0073-recorded-and-deferred.md:7`), which is also the file's last-commit date | Invalidates two of its entries. §1.14 records the §29.10 hold-partitioning bullet as still unstated (`0080...:184-192`), and SPEC-2 stages that bullet's removal from §29.10's "What the specification does not state" list (`spec/29_communication-scenarios.md:1523-1527`) on the ground that SPEC-1's §10.1.2 and §10.1.4 state both answers (`0076...spec-changes.md:475-478`), so the entry loses its subject outright. That entry's framing sentence, which calls this the first of the five gaps `spec/29` records as unstated (`0080...:186`), is already wrong against a list that carries four bullets today (`spec/29_communication-scenarios.md:1523`, `:1528`, `:1536`, `:1540`) and drops to three on application. §2 records the hold entered for one session and released by another's fence as work this proposal takes (`0080...:211`, restated at `:191-192`); D5 keeps the hold pod-scoped and makes a fence from any bound session the correct exit, so this proposal declines that item rather than taking it. §1.12's claim-register rows are untouched, including the one nearest this change: `In-flight RPC cancellation on a generation gap` (`tests/claim-map.json:174-178`) stays `ABSENT`, because SPEC-1 re-scopes the gap reset per session and does not assert that the adapter meets it (`0076...spec-changes.md:146-151`), and no claim-register row moves (`...spec-changes.md:457-469`). §1.13's §29.10 spec-map exception is untouched, and §29.10's `Interrupt`-and-barrier bullet is narrowed rather than removed, so the gap it records survives in reduced form. It also hands 0080 an entry rather than only invalidating two: OD2 recommends that a successor accept the equal-case fence retry and names 0080 §1.16 as that successor, so the residue this proposal declines to stage now has a home. | Correct both entries, and the five-gap count with §1.14. Carry §1.16, which sequences after this proposal because CODE-1 rewrites the predicate its remedy changes. 0080 self-describes as an early draft that is an inventory rather than a design (`0080...:3-6`), so all three are editable. |

Proposal 0073 carries no row. It is Implemented (2026-08-31,
`proposals/0073_fix_give-every-session-a-slot-and-absence-one-meaning.md:3`), this change is built on the
`checkSessionBound` guard and the slot registry it landed, D4 keeps both closed, and no staged deliverable
edits a file under its directory, so its record stands unchanged. The sequencing is stated under `## 10.
Dependencies` in the spec changes.

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
