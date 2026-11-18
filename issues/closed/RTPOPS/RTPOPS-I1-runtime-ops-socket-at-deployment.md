---
id: RTPOPS-I1
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0139
---

# The runtime-operations socket is not set at deployment

## Specification

§28.5.3 (Intra-pod), `CH-RUNTIMEOPS` **Endpoint.**: "The abstract Unix socket `@lenny-runtime-ops`, advertised in the adapter manifest as `runtimeOps.socket`".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`CH-RUNTIMEOPS` channel (at deployment)", status `UNWIRED`, anchor `#2853-intra-pod`, deferral `R12`.
- The row's claim names the channel by its retired spelling, which the naming law reserves; it is given here with the canonical identifier.
- Surface recorded on the row: "`cmd/lenny-adapter/main.go:373`; no `--runtime-ops-socket` setter".
- `cmd/lenny-adapter/main.go` defines `--runtime-ops-socket`, and nothing the chart or the pod spec renders sets it.

## Gap

A deployed adapter does not open the `CH-RUNTIMEOPS` socket.

## Suggested resolution

The row deferred the work to step R12, "The adapter-to-gateway control direction, with hold state", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0139 ("RP-1 Handshake", decision O7 (a)) removes `CH-RUNTIMEOPS`, the second socket, and the `runtimeOps` manifest field, and moves the channel's frames onto `CH-MSGSOCK`.
