---
id: CHGWPOD-I6
status: closed
kind: build-gap
severity: medium
requirements: []
closed: fixed
closed-by: commit 4c8e48134
---

# CheckpointBarrier does not check the coordination generation

## Specification

§28.5.1 (Gateway-to-pod), `CH-BARRIER` **Messages.**: "`CheckpointBarrier` carries `coordination_generation` and `barrier_id`." §28.5.1 (Gateway-to-pod), `CH-ATTACH` **Preconditions.**: "The pod validates the `coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one". §10.1.2 (Coordinator Handoff Protocol) states the generation gate the pod applies.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "CheckpointBarrierRequest.coordination_generation generation fence field", status `UNWIRED`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "schemas/lenny-adapter.proto".
- Note recorded on the row: "the field is carried on the request and no production reader compares it until the generation fence lands".
- `CheckpointBarrier` in `pkg/adapter/coordination.go` refuses a generation that differs from the one the pod last fenced for the session with `coordinator_handoff_stale`. Commits `4c8e48134` and `08d03565c` (2026-09-06) landed the check after the row was written.

## Gap

None remains: the handler compares the generation.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

Closed at filing: The tree carries the check; no member change was needed.
