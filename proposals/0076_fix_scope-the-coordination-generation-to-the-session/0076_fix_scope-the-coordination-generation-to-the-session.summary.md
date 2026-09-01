# Summary: Scope the coordination generation to the session

## Summary

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` gate and gap
detection read the same per-session value, and the barrier gate that holds quiescence open and carries the
gateway-minted checkpoint id back into the ack moves onto the same entry, so two co-tenant sessions drained
together each hold their own gate. The coordinator-loss hold stays pod-scoped and reports
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
CODE-4's migration and both session-store `Create` floors land in one commit: `pgstore.Create` names
`coordination_generation` in its insert column list, so a commit that tightens the session row's check to
`>= 1` while either `Create` path still writes an explicit zero rejects every session insert. The step
landing CODE-2 amends `TestCheckpointBarrierRejectsWithoutFence`
(`pkg/adapter/coordination_test.go:184-197`), which asserts the refusal D7 retires, and the step landing
CODE-4 amends `TestCreateDefaultsSessionRecordFields`
(`pkg/gateway/session/sessionstore/memstore/memstore_test.go:309-325`), which asserts the counter baseline
CODE-4 replaces, and `TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1`
(`pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go:992`), whose fenced-newer-writer constant of 1
the baseline makes equal to the session row's own value; §8 states those amendments alongside the
assertions the baseline shifts elsewhere. CODE-4's tier-2 case over migration 0181 lands under
`tests/tier2_component/migrations/`, because the migration lint that runs inside tier 0 looks for the
migration's number in that directory alone. SCHEMA-1's proto edit lands together with the stubs
`make generate-proto` produces, because tier 0 diffs the committed `pkg/proto` tree against a fresh
regeneration. CODE-1 and CODE-3 land in one step, because CODE-1 gives `Server.LastFencedGeneration` a
session id while CODE-3 deletes its only production caller, the hold's pod-level arming read, which has
no session id to pass. That step also rewires `pkg/adapter/checkpoint.go`, because `Server.barrier`
ceases to exist there and the `Checkpoint` stream links and completes on the entry
`checkpointRootsForSession` resolves for it, so CODE-2 is the generation gate alone.

Deliverable-side corrections are still outstanding in SCHEMA-1 and in the status file. SCHEMA-1 names the request-field doc comments alone
and omits the `CoordinatorFence` RPC comment (`schemas/lenny-adapter.proto:153-162`), the message-level
`CoordinatorFenceRequest` comment (`:1442-1446`), the `CoordinatorFenceResponse` comment (`:1455-1462`),
the `CheckpointBarrier` RPC comment (`:165-179`), and the message-level `CheckpointBarrierRequest` comment
(`:1469-1474`), which carry the fence rule, the gap reset, the first-fence exemption, and the barrier gate
on the wire. The status file's scope bullet still states that one session's handoff releases another
session's coordinator-loss hold, which D5 refutes, and its closing paragraph still frames the hold's scope
as the open question this change answers. The pass records under "Resolved in adversarial review" in the
spec changes carry the rationale behind each correction already landed, and the review log's ledger carries
the rest.

The hold's scope is settled rather than open. D5 keeps it pod-scoped, because the only arming signal the
adapter has is the close of the pod's single CH-ADAPTEREVENTS stream, which names no session. The cost is
recorded as a non-goal: a pod whose stream holder crashes freezes co-tenant sessions whose own coordinators
are alive. Proposal 0073 is converged and is not reopened; this proposal sequences after it.
