---
id: CHGWPOD-I20
status: open
kind: build-gap
severity: medium
requirements: []
---

# No conformance harness runs the slot-bind rules against a third-party adapter

## Specification

§15.4 (Runtime Adapter Specification), "Bind attempt token contract", states the slot-bind rules and that the statement is normative for a third-party adapter. §28.5.1 (Gateway-to-pod) carries the RPCs they govern.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "third-party adapter conformance harness for the §15.4 slot-bind rules", status `ABSENT`, anchor `#2851-gateway-to-pod`, deferral `R8`.
- Surface recorded on the row: "`tests/contract/adapter_bind_attempt/`, `tests/conformance/slot_bind_attempt_conformance_test.go`".
- Note recorded on the row: "the rules are normative for any adapter implementation; cmd/lenny-compliance drives a runtime binary over JSONL against a fake adapter and imports no gRPC, so the project runs the clauses against its own adapter only".

## Gap

The project runs the slot-bind rules against its own adapter only.

## Suggested resolution

The row deferred the work to step R8, "Reciprocal host-conformance battery", of `gateway-runtime-comms-remediation.md`.

No member change removes third-party adapters or plans the harness. 0138's conformance kit (0138.S5) covers client-facing protocol adapters rather than the runtime adapter.
