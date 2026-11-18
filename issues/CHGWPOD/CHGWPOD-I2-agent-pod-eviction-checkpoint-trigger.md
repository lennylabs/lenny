---
id: CHGWPOD-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# Agent pod eviction does not trigger a checkpoint

## Specification

§28.5.1 (Gateway-to-pod), `CH-CHECKPOINT` **Preconditions.**: "On the eviction path the agent pod cannot open the stream itself: it signals its coordinating replica on `CH-ADAPTEREVENTS`, and that replica drives the stream under its held lease (§4.6.1)."

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Agent-pod eviction checkpoint", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "no trigger on that edge".
- `pkg/adapter/prestop/prestop.go` signals the adapter and polls; the gateway drives `TriggerEviction` only from its own drain (triage, 2026-11-17).

## Gap

An agent pod's preStop hook does not signal its coordinating replica, so an evicted pod's sessions get no eviction checkpoint.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

The mechanism survives into the target state: 0118 ("PL-5") and 0131 ("IC-7") keep the eviction checkpoint, and 0131.S7 changes only its budget. No member step builds the trigger. The pod-to-gateway signal is CHPODGW-I1 and CHPODGW-I2.
