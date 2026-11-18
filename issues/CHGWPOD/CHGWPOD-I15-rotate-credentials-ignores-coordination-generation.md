---
id: CHGWPOD-I15
status: planned
kind: build-gap
severity: medium
requirements: []
change: 0133.S2
---

# RotateCredentials does not check the coordination generation

## Specification

§28.5.1 (Gateway-to-pod), `CH-ATTACH` **Preconditions.**: "The pod validates the `coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one". §10.1.2 (Coordinator Handoff Protocol) states the generation gate the pod applies.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "RotateCredentialsRequest.coordination_generation generation fence field", status `UNWIRED`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "schemas/lenny-adapter.proto".
- Note recorded on the row: "the field is carried on the request and no production reader compares it until the generation fence lands".
- In production only `CoordinatorFence`, `CheckpointBarrier` (`pkg/adapter/coordination.go`), and `SealSession` and `UnsealSession` (`pkg/adapter/sessionseal.go`) compare the generation a request carries; no other adapter handler reads the field (triage, 2026-11-17).

## Gap

`RotateCredentialsRequest` carries `coordination_generation`, and the adapter's `RotateCredentials` handler does not compare it with the generation the pod holds for the session, so a request from a replica that has lost coordination is served. The gateway calls `RotateCredentials` today, so the gap is live until 0133.S2 removes the RPC.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

Planned for 0133.S2, which lists this issue under `closes`. 0133 removes `RotateCredentials` and credential rotation into pods ("LU-4 Removal" and the §4.7.1 row of its specification changes). Step 0133.S2 lands it.
