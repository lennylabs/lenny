---
id: CHPODGW-I3
status: open
kind: build-gap
severity: medium
requirements: []
---

# The gateway does not consume the AdapterEvents stream

## Specification

§28.5.2 (Pod-to-gateway): "The §28.3 channel register places one channel on this boundary, `CH-ADAPTEREVENTS`, and it is carried on the same `LNK-POD-GRPC` connection the gateway dials for the §28.5.1 channels".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "gRPC control stream `Adapter/AdapterEvents` (client)", status `UNWIRED`, anchor `#2852-pod-to-gateway`, deferral `R12`.
- Surface recorded on the row: "handler `pkg/adapter/adapterevents.go:108`; one comment in `pkg/gateway`".
- No production code under `pkg/` or `cmd/` calls the stream (triage, 2026-11-17).

## Gap

Events the adapter emits on `CH-ADAPTEREVENTS` reach no gateway consumer.

## Suggested resolution

The row deferred the work to step R12, "The adapter-to-gateway control direction, with hold state", of `gateway-runtime-comms-remediation.md`.

`CH-ADAPTEREVENTS` survives into the target state: 0139 adds a `runtime_handshake` event that the gateway stores, and 0145 has the adapter report dead slots on it. 0139.S1 or 0145.S1 needs the consumer, and neither names it.
