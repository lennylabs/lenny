# Summary: Scope the coordination generation to the session

## Summary

**What changes.** The fenced coordination generation moves from one pod-wide field onto the per-session
slot registry entry, so a fence for one session cannot fence another. The `CheckpointBarrier` gate and gap
detection read the same per-session value. The coordinator-loss hold stays pod-scoped and reports
per-session generations: the pod-level arming event carries none, and each terminated session's
`coordinator_lost` record carries its own, or zero when no coordinator ever fenced it on that pod. The
proto doc comment that already claims per-session monotonicity becomes true. A `CheckpointBarrier` naming a
bound session for which the pod holds no fenced generation is accepted and records no value, which is D7 and
which changes the shipped gate. `spec/10` §10.1 states what a replica stamps for a session no replica has
taken over, which is generation 1 rather than the row's 0, and the gateway's barrier dispatcher applies that
floor under CODE-4, so the barrier carries the same well-formed value the fence path already carries. The
two sentences that state the barrier carries the row's current counter, §10.1.8 step 1 under SPEC-1 and
§29.7 trace step 4 under SPEC-2, are restated to name that stamp instead.

**What is fixed.** On a concurrent pod: a legitimate coordinator handoff rejected as
`coordinator_handoff_stale`, a drain barrier rejected so the drain checkpoint runs unquiesced, a
spurious `coordinator_generation_gap` event, a coordinator-lost post-mortem and log line that stamp
one session's generation on every session the hold terminates, and a split-brain counter that
increments on healthy handoffs. A hold released by an unrelated session's fence is not on that list:
the hold is pod-scoped by D5, so a fence from any session on the pod is the correct exit from it. D7
and the stamp rule together add a barrier repair that is not specific to a concurrent pod. The drain
barrier of a session that never resumed and was never taken over meets two refusals today: the
adapter rejects the non-positive generation its row still carries before the gate is reached, and
the gate then rejects the absence of a recorded value. The stamp rule removes the first and D7
removes the second, so that session is quiesced by its own preStop drain instead of being captured
twice against a live workspace. Neither change
delivers that repair without the other.

**Watch out for.** The spec-lane work covers `spec/10` under SPEC-1 and `spec/28` and `spec/29`
under SPEC-2. The wire mirrors in `schemas/lenny-adapter.proto` belong to SCHEMA-1. The
deliverable-side statements outside the staged spec edits that predate D5, D6, D7, and SPEC-2 are
enumerated here, and each is corrected before the checklist is run. The files-touched list carries
`spec/10_gateway-internals.md` as its only spec entry and omits `spec/28_communication-channels.md`
and `spec/29_communication-scenarios.md`. Step S1 names SPEC-1 and §10.1 alone. CODE-3 and step S5
defer to a §7 hold-state decision that D5 replaced. SCHEMA-1 names the request-field doc comments
alone and omits the `CoordinatorFence` RPC comment (`schemas/lenny-adapter.proto:153-162`), the
message-level `CoordinatorFenceRequest` comment (`:1442-1446`), the `CoordinatorFenceResponse`
comment (`:1455-1462`), the `CheckpointBarrier` RPC comment (`:165-179`), and the message-level
`CheckpointBarrierRequest` comment (`:1469-1474`), which carry the fence rule, the gap reset, the
first-fence exemption, and the barrier gate on the wire. TEST-1's pinning case asserts that one
session's fence leaves a co-tenant's hold intact, which D5 makes false. CODE-2 names
`pkg/adapter/coordination.go:211` alone and does not state the gate change D7 requires, which is
that the barrier refuses only when the named session's registry entry holds a recorded generation
the request does not match. TEST-1 and §8 do not carry the cases D7 needs, and
`TestCheckpointBarrierRejectsWithoutFence` (`pkg/adapter/coordination_test.go:185-197`) asserts the
refusal D7 retires. The files-touched list also omits `pkg/adapter/coordination_test.go` and names
no sections under `spec/10_gateway-internals.md`, which SPEC-1 now edits in §10.1, §10.1.2, §10.1.4,
and §10.1.8. The non-spec changes carry no CODE-4, which is the gateway-side half of the stamp rule:
`Coordinator.dispatchOne` in `pkg/gateway/coordination/barrier/barrier.go` normalises a non-positive
target generation to 1 before both the dispatch and the checkpoint-meta write. §8 gains the tier-1
case that pins that floor, the files-touched list gains that file and its test file, and the
checklist gains CODE-4's step after S4 with the two steps after it renumbered. Pass 7 of the spec
changes carries the replacement wording for the D7 corrections, pass 8 carries CODE-4's full
statement, the corrected account of what refuses the ordinary session's barrier, and the two stamp
sentences SPEC-1 and SPEC-2 restate, and the review log's ledger carries the rest.

The hold's scope is settled rather than open. D5 keeps it pod-scoped, because the only arming signal the
adapter has is the close of the pod's single CH-ADAPTEREVENTS stream, which names no session. The cost is
recorded as a non-goal: a pod whose stream holder crashes freezes co-tenant sessions whose own coordinators
are alive. Proposal 0073 is converged and is not reopened; this proposal sequences
after it.
