---
id: CHGWPOD-I3
status: open
kind: build-gap
severity: medium
requirements: []
---

# Agent pod has no mTLS client identity

## Specification

§28.5.1 (Gateway-to-pod), `CH-ATTACH` **Endpoint.**: "Gateway-to-pod communication runs over gRPC with mTLS (§15.3), whose certificate issuance is stated in §10.3."

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Agent-pod mTLS client identity", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R14`.
- Surface recorded on the row: "no TLS flags in `podspec.go`".
- The pod spec renderer under `pkg/controller/sandbox/podspec/` sets no TLS flag (triage, 2026-11-17).

## Gap

The adapter presents no client identity on the gateway-to-pod connection. The same missing identity leaves the `CH-LLMPROXY` peer check of §28.5.5 (Pod-egress) with no SPIFFE URI to compare, so the proxy credential is not bound to the pod (CHEGRESS-I2).

## Suggested resolution

The row deferred the work to step R14, "Agent-pod mTLS client identity", of `gateway-runtime-comms-remediation.md`.

No member change plans it. CHGWPOD-I1 records the certificate material the identity needs.
