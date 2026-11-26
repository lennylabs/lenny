---
id: BILLEV-I2
status: open
kind: build-gap
severity: medium
requirements: []
---

# A billing append slower than 250 ms skips the Redis tier and lands in the in-memory buffer

## Specification

§11.2.1 and §12.4 describe the billing write path: the Postgres primary, then the Redis stream, then the in-memory write-ahead buffer as the last resort.

## Evidence

- `pkg/billing/billingfanout/billingfanout.go` (about lines 35 and 61): `Emitter.Emit` wraps every append in a 250 ms timeout.
- `pkg/billing/billingstore/failover/failover.go`: when the primary insert fails on that expired context, the Redis publish fails on the same context, and the event is staged in the in-memory buffer, which takes no context.
- Found by the analysis of change 0176 open decision O2 on 2026-11-26.

## Gap

An event whose primary insert takes longer than 250 ms goes to the least durable tier instead of the Redis stream, so a replica restart in that window loses it, and the buffer is the tier that F-12.8.53 (ERASE-I4) leaves outside user erasure on other replicas.

## Suggested resolution

Give the Redis publish its own short deadline independent of the primary attempt, so a slow primary falls through to the stream, and add a test with a primary slower than the emitter timeout.
