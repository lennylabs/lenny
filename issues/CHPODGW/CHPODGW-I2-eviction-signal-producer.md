---
id: CHPODGW-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# The adapter does not send the preStop eviction signal

## Specification

§28.5.2 (Pod-to-gateway), `CH-ADAPTEREVENTS` **Messages.**: the channel carries "the signal an agent pod's preStop hook sends to its coordinating replica to have that replica drive the eviction checkpoint (§4.6.1)", and the specification states no message name for it.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`AdapterEvicting` producer and consumer (producer)", status `UNWIRED`, anchor `#2852-pod-to-gateway`, deferral `R16`.
- Surface recorded on the row: "`pkg/adapter/adapterevents.go:248`; no hit outside `pkg/adapter`".
- The row named the event `AdapterEvicting`, which no longer exists in the tree after revert `54292da1b`, so the row's status is absent rather than unwired (triage, 2026-11-17).

## Gap

The adapter's preStop path sends no event on `CH-ADAPTEREVENTS`.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

No member step builds the signal. CHPODGW-I1 records the consumer.
