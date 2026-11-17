---
id: CHEGRESS-I1
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0133
---

# The pod has no client path to the LLM proxy

## Specification

§28.5.5 (Pod-egress), `CH-LLMPROXY` **Endpoint.**: "The gateway's LLM proxy port, TCP 8443, reached at the `proxyUrl` the pod receives in its `CredentialLease`".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "LLM proxy (pod client)", status `ABSENT`, anchor `#2855-pod-egress`, deferral `R11`.
- Surface recorded on the row: "`cmd/lenny-gateway/main.go:475`, `charts/lenny/values.yaml:1695`".
- `cmd/lenny-gateway/llmproxy_server.go` serves one route, and `charts/lenny/values.yaml` sets `features.llmProxy: false` (triage, 2026-11-17).

## Gap

No pod reaches the LLM proxy through the lease's `proxyUrl` in a default install.

## Suggested resolution

The row deferred the work to step R11, "LLM proxy dialects reach a route", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0133 ("LU-3" and "LU-4 Removal") redesigns the path: the `CredentialLease`, its `proxyUrl`, and the lease token leave, and the pod receives a per-session proxy token and the base URL of each format in `session_start`. `CH-LLMPROXY` keeps its identifier. Step 0133.S2 builds the routes and the session binding.
