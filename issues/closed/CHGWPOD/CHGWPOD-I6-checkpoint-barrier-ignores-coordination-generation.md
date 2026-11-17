---
id: CHGWPOD-I6
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0116.S5
---

# CheckpointBarrier does not check the coordination generation

## Specification

§28.5.1 (Gateway-to-pod), `CH-BARRIER` **Messages.**: "`CheckpointBarrier` carries `coordination_generation` and `barrier_id`." §28.5.1 (Gateway-to-pod), `CH-ATTACH` **Preconditions.**: "The pod validates the `coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one". §10.1.2 (Coordinator Handoff Protocol) states the generation gate the pod applies.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "CheckpointBarrierRequest.coordination_generation generation fence field", status `UNWIRED`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "schemas/lenny-adapter.proto".
- Note recorded on the row: "the field is carried on the request and no production reader compares it until the generation fence lands".
- The row was in error when it was written. `CheckpointBarrier` in `pkg/adapter/coordination.go` already refused a generation that differs from the one the pod last fenced, with `coordinator_handoff_stale`, from commit `d353a8ef3` (2026-05-27), before commit `f0e36cd76` (2026-08-12) wrote the row. Commit `4c8e48134` (2026-09-06) only made the fenced state per session.

## Gap

None remains: the handler compares the generation.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

Closed at filing by 0116.S5 as obsolete: the row recorded a gap the tree never had.
