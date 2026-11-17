---
id: CHEGRESS-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# The `CH-LLMPROXY` credential is not bound to a pod identity

## Specification

§28.5.5 (Pod-egress), `CH-LLMPROXY` **Preconditions.**: the gateway "verifies the peer SPIFFE URI from the authenticated mTLS connection against the SPIFFE URI recorded in the lease record" for multi-tenant deployments. §4.9 (Credential Leasing Service), **Deployment note — admission enforcement of SPIFFE-binding in multi-tenant mode:**, makes the binding mandatory in multi-tenant mode and refuses a pool that disables it.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Proxy SPIFFE lease binding", status `UNWIRED`, anchor `#2855-pod-egress`, deferral `R14`.
- Surface recorded on the row: "`pkg/credential/lease.go:335`; `PodSpiffeURI` never set".
- `PodSpiffeURI` is carried through `podsession` and never set (triage, 2026-11-17).
- 0133 ("LU-3", "Session binding") replaces the lease binding with a per-session proxy token stored with the pod's SPIFFE URI, refused on a mismatching peer when `spiffeBinding` is on. Step 0133.S2 builds it.

## Gap

The lease record holds no SPIFFE URI, so the peer check has nothing to compare, and the multi-tenant binding that §4.9's deployment note requires is skipped. The redesign in 0133.S2 keeps the gap: its `spiffeBinding` check compares the peer with the pod's SPIFFE URI, and the agent pod has no SPIFFE identity that any change plans (CHGWPOD-I1 and CHGWPOD-I3). Until the pod has that identity, a proxy credential presented from another pod is not refused.

## Suggested resolution

The row deferred the work to step R14, "Agent-pod mTLS client identity", of `gateway-runtime-comms-remediation.md`.

Give the agent pod a SPIFFE identity (CHGWPOD-I1 and CHGWPOD-I3), set `PodSpiffeURI` from it at bind, and have the proxy refuse a peer whose SPIFFE URI differs from the one stored with the credential. Open decision 4 of `changes/0133-llm-upstreams-and-cost-budgets/decisions.md`, raised by the security review of 0116.S5, asks whether 0133.S2 depends on that identity.
