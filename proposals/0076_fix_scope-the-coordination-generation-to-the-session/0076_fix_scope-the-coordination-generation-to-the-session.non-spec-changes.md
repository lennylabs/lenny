# Non-spec changes: Scope the coordination generation to the session

The staged changes below target the schema, the adapter code, and the tests. The caveat that opens the
proposal's "Proposed changes" section applies to them.

**IMPLEMENTOR TO FILL THE BLANKS.** Indicative targets; the text is written during convergence, against the
post-0073 state of each file.

### SCHEMA-1. Make the comments true

`schemas/lenny-adapter.proto`: the `CoordinatorFenceRequest.coordination_generation` and
`CheckpointBarrierRequest.coordination_generation` doc comments state the settled scope.

### CODE-1. Move the state

`pkg/adapter/coordination.go`: `coordinationState` moves onto the slot registry entry;
`pkg/adapter/server.go:304`'s field is removed. `CoordinatorFence` resolves the entry for the named session.

### CODE-2. The barrier reads the same place

`pkg/adapter/coordination.go:211`.

### CODE-3. Hold state

`pkg/adapter/holdstate.go`, per §7.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers this change reaches are 0, 1, 2 (the registry state), 3 (the
wire gate's behavior), 7a (concurrent handoffs), and 8 (crash takeover). Proposal 0060 built a two-replica
gateway harness and tier-8 crash-takeover coverage for §10.1; read what it built before designing here,
because the per-session fencing case probably belongs in that harness rather than in a new one.

The case that pins this defect: two sessions co-tenant on one pod, each handed off independently, asserting
that the second handoff is accepted, that its barrier is accepted, that no gap is logged, and that the
first session's hold is not released by the second's fence. It must fail against the pre-fix code.

## 9. Files touched on application

- `spec/10_gateway-internals.md`
- `schemas/lenny-adapter.proto`
- `pkg/adapter/coordination.go`
- `pkg/adapter/server.go`
- `pkg/adapter/holdstate.go`
- `pkg/adapter/slotsession.go` (the registry entry gains the generation)
- Tests under `pkg/adapter/`, `tests/tier7a_load/`, and `tests/tier8_chaos/`
