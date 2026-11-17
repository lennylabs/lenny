---
id: CHGWPOD-I19
status: open
kind: build-gap
severity: medium
requirements: []
---

# No client calls the pod's gRPC health service

## Specification

§28.5.1 (Gateway-to-pod), `CH-PODHEALTH` **Degradation.**: a pod whose health check has not passed is not marked `idle` and so is not offered to a session (§4.7).

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`grpc.health.v1.Health` service on the pod (client)", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R23`.
- Surface recorded on the row: "`pkg/adapter/transport.go:52-54`; no health client anywhere in the gateway or the controller".
- The adapter serves `grpc.health.v1.Health` (`pkg/adapter/transport.go`), and no gateway or controller code calls it. Pod readiness uses the kubelet TCP probe and the readiness gate instead (triage, 2026-11-17).

## Gap

The warm pool controller marks a pod `idle` without the gRPC health check the card and §4.7 (Runtime Adapter), **Health check:**, state.

## Suggested resolution

The row deferred the work to step R23, "Pre-connect and SDK-warm at the deployment boundary", of `gateway-runtime-comms-remediation.md`.

R23 is overtaken: 0118 ("PL-8") removes `preConnect` and SDK-warm, which R23 built on. No member change plans the health client, so this issue needs a new owner.
