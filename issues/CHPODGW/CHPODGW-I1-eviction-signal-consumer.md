---
id: CHPODGW-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# The gateway does not consume the preStop eviction signal

## Specification

§28.5.2 (Pod-to-gateway), `CH-ADAPTEREVENTS` **Messages.**: the channel carries "the signal an agent pod's preStop hook sends to its coordinating replica to have that replica drive the eviction checkpoint (§4.6.1)", and the specification states no message name for it.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "`AdapterEvicting` producer and consumer (consumer)", status `ABSENT`, anchor `#2852-pod-to-gateway`, deferral `R16`.
- Surface recorded on the row: "`pkg/adapter/adapterevents.go:248`; no hit outside `pkg/adapter`".
- The row named the event `AdapterEvicting`, which no longer exists in the tree after revert `54292da1b` (triage, 2026-11-17).

## Gap

No gateway code reacts to an eviction signal from a pod.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

`CH-ADAPTEREVENTS` survives into the target state. No member step builds the signal. CHGWPOD-I2 records the checkpoint it drives, and CHPODGW-I2 the producer.
