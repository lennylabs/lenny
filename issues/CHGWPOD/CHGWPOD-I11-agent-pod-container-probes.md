---
id: CHGWPOD-I11
status: open
kind: build-gap
severity: medium
requirements: []
---

# Agent pod containers carry no liveness or startup probe

## Specification

§28.5.1 (Gateway-to-pod), `CH-PODHEALTH` **Exclusivity.**: "the consumer of the result is the warm pool controller, which marks a pod `idle` only once the health check passes". §28 states no kubelet probe.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Kubelet probes on agent-pod containers", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R6`.
- Surface recorded on the row: "no probe field anywhere in `podspec.go`".
- Since commit `0c6e34e49` (2026-10-24), `adapterReadinessProbe` in `pkg/controller/sandbox/podspec/sidecar.go` adds a TCP readiness probe on the adapter's `grpc` port to every sidecar pod, and the runtime container carries no probe. No liveness or startup probe exists (triage, 2026-11-17).

## Gap

The row recorded that the pod spec carries no probe. The readiness probe has landed; the agent pod's containers still carry no liveness or startup probe, and §28 does not state whether they need one.

## Suggested resolution

The row deferred the work to step R6, "Podspec and chart decoupling, and the deployment-boundary gate", of `gateway-runtime-comms-remediation.md`.

Decide whether the agent pod needs liveness or startup probes. 0118 adds a startup probe for `netagent`. If none is needed, close this issue as fixed.
