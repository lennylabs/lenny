---
id: CHGWPOD-I14
status: open
kind: build-gap
severity: medium
requirements: []
---

# RevokeCredentials does not check the coordination generation

## Specification

§28.5.1 (Gateway-to-pod), `CH-ATTACH` **Preconditions.**: "The pod validates the `coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one". §10.1.2 (Coordinator Handoff Protocol) states the generation gate the pod applies.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "RevokeCredentialsRequest.coordination_generation generation fence field", status `UNWIRED`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "schemas/lenny-adapter.proto".
- Note recorded on the row: "the field is carried on the request and no production reader compares it until the generation fence lands".
- In production only `CoordinatorFence`, `CheckpointBarrier` (`pkg/adapter/coordination.go`), and `SealSession` and `UnsealSession` (`pkg/adapter/sessionseal.go`) compare the generation a request carries; no other adapter handler reads the field (triage, 2026-11-17).

## Gap

`RevokeCredentialsRequest` carries `coordination_generation`, and the adapter's `RevokeCredentials` handler does not compare it with the generation the pod holds for the session, so a request from a replica that has lost coordination is served.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

0133 removes the other credential RPCs and does not name `RevokeCredentials`. If 0133's validation item finds no VCS path that uses the adapter's credentials file, 0133.S2 makes this issue obsolete with CHINTRA-I1.
