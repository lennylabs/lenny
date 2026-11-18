---
id: CHGWPOD-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# Agent pod spec carries no mTLS certificate material

## Specification

§28.5.1 (Gateway-to-pod), `CH-ATTACH` **Endpoint.**: "Gateway-to-pod communication runs over gRPC with mTLS (§15.3), whose certificate issuance is stated in §10.3."

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Agent podspec mTLS certificate material", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R14`.
- Surface recorded on the row: "spec/04_system-components.md §4.4".
- Note recorded on the row: "the agent-pod mTLS client identity step supplies the certificate volumes".
- The pod spec the controller renders has no certificate volume and no TLS flags (triage, 2026-11-17).

## Gap

The agent pod receives no certificate material for the mTLS connection the card states.

## Suggested resolution

The row deferred the work to step R14, "Agent-pod mTLS client identity", of `gateway-runtime-comms-remediation.md`.

No member change plans it. 0127 ("Transport security") states that its hops do not depend on R14.
