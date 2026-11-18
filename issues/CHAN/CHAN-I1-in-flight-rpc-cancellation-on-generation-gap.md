---
id: CHAN-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# In-flight RPCs are not cancelled on a generation gap

## Specification

§28.6 (Exclusivity and concurrency model): a replica whose stream is rejected on the generation stamp "cancels every in-flight RPC for that session without retrying and discards its cached in-memory streams". §10.1.2 (Coordinator Handoff Protocol), **Gap detection on the pod:**, states the pod's half: the adapter cancels and discards the in-flight RPCs it received for that session after the last fenced generation.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "In-flight RPC cancellation on a generation gap", status `ABSENT`, anchor `#286-exclusivity-and-concurrency-model`, deferral `R16`.
- Surface recorded on the row: "conceded at `pkg/adapter/coordination.go:81-82`".

## Gap

The adapter detects a generation gap and leaves the in-flight RPCs of the session running.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

No member change plans it.
