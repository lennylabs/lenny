---
id: CHAN-I4
status: open
kind: build-gap
severity: medium
requirements: []
---

# Barrier quiescence is not enforced against operational RPCs

## Specification

§28.5.1 (Gateway-to-pod), `CH-BARRIER` **Messages.**: "The adapter quiesces tool-call dispatch and holds quiescence while the gateway drives the `Checkpoint` stream against the held pod". §28.6 (Exclusivity and concurrency model) states the barrier's exclusion.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Quiesce enforcement against operational RPCs", status `ABSENT`, anchor `#286-exclusivity-and-concurrency-model`, deferral `R16`.
- Surface recorded on the row: "`coord.quiesced` has no reader in any handler".
- `CheckpointBarrier` sets the quiesced state, and only the test helper `isQuiescedForBarrier` reads it (triage, 2026-11-17).

## Gap

Tool-call dispatch and the other operational RPCs run while a barrier holds the session quiesced.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

No member change removes the barrier's quiescence or plans its enforcement. 0139 ("RP-3") moves tool calls onto `CH-MSGSOCK` frames, where dispatch still has to stop.
