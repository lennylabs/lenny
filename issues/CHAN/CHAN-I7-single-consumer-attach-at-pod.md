---
id: CHAN-I7
status: open
kind: build-gap
severity: medium
requirements: []
---

# The pod does not enforce a single Attach consumer per session

## Specification

§28.6 (Exclusivity and concurrency model): "The holding replica holds one `CH-ATTACH` stream per session". §28.5.1 (Gateway-to-pod), `CH-ATTACH`: "The gateway holds one `Attach` stream for a session from its first message delivery until it releases the session's binding".

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Single-consumer enforcement on `Attach` at the pod", status `ABSENT`, anchor `#286-exclusivity-and-concurrency-model`, deferral `R20`.
- Surface recorded on the row: "no registry or counter anywhere in `pkg/adapter/attach.go`".
- `pkg/adapter/attach.go` keeps no registry or counter of open streams per session (triage, 2026-11-17).

## Gap

The pod accepts a second `Attach` stream for a session that already has one.

## Suggested resolution

The row deferred the work to step R20, "Message-plane durability and cross-replica input wait", of `gateway-runtime-comms-remediation.md`.

No member change plans it.
