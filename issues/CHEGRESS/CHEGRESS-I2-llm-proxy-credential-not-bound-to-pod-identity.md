---
id: CHEGRESS-I2
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0133
---

# The LLM proxy does not bind a lease to the pod's SPIFFE URI

## Specification

§28.5.5 (Pod-egress), `CH-LLMPROXY` **Preconditions.**: the gateway "verifies the peer SPIFFE URI from the authenticated mTLS connection against the SPIFFE URI recorded in the lease record" for multi-tenant deployments.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Proxy SPIFFE lease binding", status `UNWIRED`, anchor `#2855-pod-egress`, deferral `R14`.
- Surface recorded on the row: "`pkg/credential/lease.go:335`; `PodSpiffeURI` never set".
- `PodSpiffeURI` is carried through `podsession` and never set (triage, 2026-11-17).

## Gap

The lease record holds no SPIFFE URI, so the check has nothing to compare.

## Suggested resolution

The row deferred the work to step R14, "Agent-pod mTLS client identity", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0133 ("LU-3", "Session binding") replaces the lease binding with a per-session proxy token stored with the pod's SPIFFE URI, refused on a mismatching peer when `spiffeBinding` is on. Step 0133.S2 builds it. It needs the pod identity that CHGWPOD-I1 and CHGWPOD-I3 record.
